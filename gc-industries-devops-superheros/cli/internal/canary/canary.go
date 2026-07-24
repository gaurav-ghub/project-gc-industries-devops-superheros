// Package canary implements `endurance canary` — running several versions of
// one service at once and choosing how much traffic each one gets.
//
// The distinction from `release` is the whole point of the feature. A release
// answers "which image should this service run?" and replaces the running pods
// to make it true. A canary shift answers "how much of the traffic should each
// running version receive?" and replaces nothing — it rewrites weights in an
// Istio VirtualService, and every pod that was serving before is still serving
// after. Being able to move traffic without moving workloads is what makes a
// rollback instant and a bad version cheap.
//
// Like every other Endurance command, this one only writes files. ArgoCD is
// still the only thing that talks to the cluster.
package canary

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/notify"
	"github.com/gc-ghub/endurance/internal/policy"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/version"
)

// Options configures a traffic shift.
type Options struct {
	Root       string         // platform repo root
	App        string         // registered application name
	Service    string         // the canary service whose traffic is being split
	Weights    map[string]int // version name -> percent; must sum to 100
	Commit     bool           // stage + commit the changed files (never pushes)
	DryRun     bool           // report what would change, write nothing
	PolicyDir  string         // override the Kyverno policy directory
	SkipPolicy bool           // break glass: report policy violations but do not block
	NoSpec     bool           // do not write the new weights back into specs/<app>.yaml
	NoNotify   bool           // do not send the CLI notification
}

// ParseWeights reads the --weights form "v1=90,v2=10".
func ParseWeights(s string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("bad weight %q — use the form v1=90,v2=10", part)
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("bad weight for %q: %q is not a number", name, value)
		}
		name = strings.TrimSpace(name)
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("version %q given twice", name)
		}
		out[name] = n
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--weights is empty — use the form v1=90,v2=10")
	}
	return out, nil
}

// PromoteWeights returns the weight set that sends all traffic to one version.
// Promotion is deliberately expressed as a weight set rather than as a special
// case, so `promote` and `set` write the identical thing and there is only one
// way for traffic to be described in the registry.
func PromoteWeights(s spec.Service, to string) (map[string]int, error) {
	if s.FindVersion(to) < 0 {
		return nil, fmt.Errorf("service %q has no version %q — versions are: %s",
			s.Name, to, strings.Join(s.VersionNames(), ", "))
	}
	out := map[string]int{}
	for _, v := range s.Versions {
		out[v.Name] = 0
	}
	out[to] = 100
	return out, nil
}

// Shift applies a new weight split to one service and prints the result.
func Shift(opts Options) error {
	render.Banner(version.Current)
	render.Section("Canary · " + opts.App + "/" + opts.Service)

	if opts.Service == "" {
		return fmt.Errorf("--service is required (which service are you splitting?)")
	}

	// Plan → gate → write, the same order a release uses. A weight change cannot
	// realistically violate a Kyverno policy, but running the gate anyway means
	// there is exactly one answer to "did the platform check this before it was
	// written", regardless of which command wrote it.
	change, err := gitops.PlanWeights(opts.Root, opts.App, opts.Service, opts.Weights)
	if err != nil {
		return err
	}

	printSplit(change)

	if change.NoOp {
		render.Warn("traffic is already split this way — nothing to do")
		render.Info("no files changed, nothing to commit")
		return nil
	}

	for _, w := range change.App.MeshWarnings() {
		render.Warn(w)
	}

	if err := policy.Gate(policy.Options{
		Root: opts.Root, Dir: opts.PolicyDir, Skip: opts.SkipPolicy,
	}, change.App); err != nil {
		return err
	}

	if opts.DryRun {
		render.Info("dry run — no files written")
		return nil
	}

	if change, err = gitops.WriteWeights(opts.Root, change); err != nil {
		return err
	}

	// Close the loop on the input file too. A weight is the one field that
	// drifts between specs/ and apps/, because it is operational state a human
	// moves rather than an intention they wrote down — and a stale spec is
	// exactly what silently reverted Phase 1's namespace fix.
	if !opts.NoSpec {
		p, err := gitops.SyncSpecWeights(opts.Root, opts.App, opts.Service, opts.Weights)
		switch {
		case err != nil:
			render.Warn("could not update the spec file: " + err.Error())
			render.Detail("the generated files are correct; specs/" + opts.App + ".yaml now disagrees and a re-onboard would reset this split")
		case p != "":
			change.Written = append(change.Written, p)
		}
	}

	for _, w := range change.Written {
		render.Success("wrote " + w)
	}

	if opts.Commit {
		msg := fmt.Sprintf("endurance: canary %s/%s %s", change.App.Name, change.Service, summarizePlain(change.Deltas))
		if err := gitops.Commit(opts.Root, change.Written, msg); err != nil {
			render.Warn("commit skipped: " + err.Error())
		} else {
			render.Success("staged and committed, not pushed — " + gitops.HeadSubject(opts.Root))
		}
	} else {
		render.Info("not committed — review the diff, then commit when ready")
	}

	if !opts.NoNotify {
		e := notify.New(spec.StageRequested, change.App)
		e.Service = change.Service
		e.Detail = "traffic shift " + summarizePlain(change.Deltas) + " (no pods restart)"
		notify.Send(change.App, e)
	}

	render.Dashboard("Traffic shift prepared",
		[][2]string{
			{"App", render.Value(change.App.Name)},
			{"Service", render.Value(change.Service)},
			{"Split", summarize(change.Deltas)},
			{"Namespace", change.App.Namespace},
			{"Restarts", "none — only the VirtualService changes"},
		},
		[]string{
			"git push the platform repo so ArgoCD can see it",
			"kubectl -n " + change.App.Namespace + " get virtualservice " + change.Service + " -o yaml   to confirm the weights",
			"endurance canary status " + change.App.Name + "   to see the split Endurance believes in",
		},
	)
	return nil
}

// Status prints every service of an application and how its traffic is split.
func Status(root, appName string) error {
	app, err := gitops.Load(root, appName)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", appName, err)
	}
	render.Banner(version.Current)
	render.Section("Canary · " + app.Name)
	render.Info(fmt.Sprintf("namespace=%s  mesh=%v", app.Namespace, app.Mesh.Enabled))

	canaries := 0
	for _, s := range app.Services {
		if !s.IsCanary() {
			render.Step(render.Value(s.Name) + "  " + s.Ref() + "  " + render.Value("100%") + " (single version)")
			continue
		}
		canaries++
		render.Step(render.Value(s.Name) + fmt.Sprintf("  %d versions", len(s.Versions)))
		for _, v := range s.Versions {
			render.Detail(fmt.Sprintf("%-6s %3d%%  %s  replicas=%d  → Deployment %s",
				v.Name, v.Weight, s.VersionRef(v), replicas(v), s.WorkloadName(v)))
		}
	}
	if canaries == 0 {
		render.Info("no service declares versions — add `versions:` to a service in specs/" + app.Name + ".yaml to start a canary")
	}
	for _, w := range app.MeshWarnings() {
		render.Warn(w)
	}
	return nil
}

func replicas(v spec.Version) int {
	if v.Replicas == 0 {
		return 1
	}
	return v.Replicas
}

// printSplit shows the before/after for every version, including the ones that
// did not move — a traffic split is only meaningful as a whole.
func printSplit(c gitops.WeightChange) {
	for _, d := range c.Deltas {
		line := fmt.Sprintf("%-6s %3d%% → %s", d.Version, d.Old, render.Value(fmt.Sprintf("%d%%", d.New)))
		if d.Old == d.New {
			render.Info(line + "  (unchanged)")
		} else {
			render.Step(line)
		}
	}
}

func summarize(deltas []gitops.WeightDelta) string {
	parts := make([]string, 0, len(deltas))
	for _, d := range deltas {
		parts = append(parts, fmt.Sprintf("%s=%d%%", d.Version, d.New))
	}
	sort.Strings(parts)
	return strings.Join(parts, "  ")
}

func summarizePlain(deltas []gitops.WeightDelta) string {
	parts := make([]string, 0, len(deltas))
	for _, d := range deltas {
		parts = append(parts, fmt.Sprintf("%s=%d", d.Version, d.New))
	}
	return strings.Join(parts, ",")
}
