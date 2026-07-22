package policy

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gc-ghub/launchpad/internal/render"
	"github.com/gc-ghub/launchpad/internal/spec"
)

// Options configures a gate run.
type Options struct {
	Root string // platform repo root
	Dir  string // policy directory; "" means <root>/infra/kyverno_policy
	Skip bool   // break glass: report but never block
}

// Dir resolves the policy directory for a run.
func (o Options) dir() string {
	if o.Dir != "" {
		return o.Dir
	}
	return filepath.Join(o.Root, DefaultDir)
}

// Gate is the deploy-time policy check that onboard and release run before they
// write anything.
//
// It returns an error when an Enforce-mode policy is violated, and the caller is
// expected to abandon the operation — the point of the gate is that the repo
// never receives a manifest the cluster would refuse.
func Gate(opts Options, app spec.App) error {
	render.Section("Policy gate")

	dir := opts.dir()
	policies, err := Load(dir)
	if err != nil {
		if os.IsNotExist(errUnwrap(err)) {
			// No policy directory is a legitimate state for a platform that has
			// not adopted Kyverno yet. Say so loudly rather than reporting a
			// pass that checked nothing.
			render.Warn("no policy directory at " + dir + " — nothing was checked")
			return nil
		}
		return err
	}
	if len(policies) == 0 {
		render.Warn("no ClusterPolicies found in " + dir + " — nothing was checked")
		return nil
	}

	rep := Check(policies, app)
	Print(rep, dir)

	blocking := rep.Blocking()
	if len(blocking) == 0 {
		return nil
	}
	if opts.Skip {
		render.Warn(fmt.Sprintf("--skip-policy: continuing despite %d enforced violation(s)", len(blocking)))
		render.Warn("the cluster will still reject this at admission — you are only deferring the failure")
		return nil
	}
	return fmt.Errorf("policy gate failed: %d enforced violation(s) — nothing was written", len(blocking))
}

// Print renders a report through the shared CLI look.
func Print(rep Report, dir string) {
	render.Info(fmt.Sprintf("%d policies from %s · %d checks", rep.Policies, dir, rep.Checked))

	for _, w := range rep.Warnings {
		render.Warn(w)
	}
	for _, s := range rep.Skipped {
		render.Info(fmt.Sprintf("skipped %s/%s — %s", s.Policy, s.Rule, s.Reason))
	}

	var enforced, audited int
	for _, v := range rep.Violations {
		if v.Action == Enforce {
			enforced++
			render.Error(v.String())
		} else {
			audited++
			render.Warn("audit  " + v.String())
		}
	}

	switch {
	case enforced > 0:
		render.Error(fmt.Sprintf("%d enforced violation(s)", enforced))
	case audited > 0:
		render.Success(fmt.Sprintf("no enforced violations (%d audit-mode finding(s))", audited))
	default:
		render.Success("all policies satisfied")
	}
}

// errUnwrap digs out the wrapped error so os.IsNotExist can see it.
func errUnwrap(err error) error {
	type unwrapper interface{ Unwrap() error }
	for {
		u, ok := err.(unwrapper)
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}
