// Package release implements `launchpad release` — promoting a new image for
// one service of a registered application.
//
// Onboarding is the one-time act of defining an application; release is the
// everyday one. A developer's code change produces a new image in the
// application repo's CI, and release is how that image reaches the cluster:
// bump exactly one tag, commit, and let ArgoCD roll exactly one Deployment.
// Like every other LaunchPad command it only writes files — ArgoCD deploys.
package release

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gc-ghub/launchpad/internal/gitops"
	"github.com/gc-ghub/launchpad/internal/policy"
	"github.com/gc-ghub/launchpad/internal/render"
	"github.com/gc-ghub/launchpad/internal/version"
)

// Options configures a release.
type Options struct {
	Root       string // platform repo root
	App        string // registered application name
	Service    string // the one service to promote
	Version    string // the canary version to promote ("" for a plain service)
	Tag        string // the image tag to promote to
	Commit     bool   // stage + commit the changed files (never pushes)
	DryRun     bool   // report what would change, write nothing
	PolicyDir  string // override the Kyverno policy directory
	SkipPolicy bool   // break glass: report policy violations but do not block
}

// Run promotes one service to a new image tag and prints the result.
func Run(opts Options) error {
	render.Banner(version.Current)
	render.Section("Release · " + opts.App)

	if opts.Service == "" {
		return fmt.Errorf("--service is required (which service are you promoting?)")
	}
	if opts.Tag == "" {
		return fmt.Errorf("--tag is required (which image tag are you promoting to?)")
	}

	// Plan first, always. A release is only allowed to touch the repo after the
	// policy gate has seen the manifests it would produce, so the plan/gate/write
	// order is the same whether this is a dry run or the real thing — a dry run
	// is simply the same pipeline stopped one step early.
	bump, err := gitops.PlanServiceTag(opts.Root, opts.App, opts.Service, opts.Version, opts.Tag)
	if err != nil {
		return err
	}

	if bump.NoOp {
		render.Warn(fmt.Sprintf("%s is already at %s — nothing to do",
			render.Value(bump.Target()), render.Value(opts.Tag)))
		render.Info("no files changed, nothing to commit")
		return nil
	}

	render.Step(fmt.Sprintf("would bump %s  %s → %s",
		render.Value(bump.Target()), bump.OldTag, render.Value(bump.NewTag)))

	if err := policy.Gate(policy.Options{
		Root: opts.Root, Dir: opts.PolicyDir, Skip: opts.SkipPolicy,
	}, bump.App); err != nil {
		return err
	}

	if opts.DryRun {
		render.Info("dry run — no files written")
		return nil
	}

	if bump, err = gitops.WriteBump(opts.Root, bump); err != nil {
		return err
	}

	render.Step(fmt.Sprintf("%s  %s → %s",
		render.Value(bump.Target()), bump.OldTag, render.Value(bump.NewTag)))
	for _, w := range bump.Written {
		render.Success("wrote " + w)
	}

	untouched := otherWorkloads(bump)
	if len(untouched) > 0 {
		render.Info("unchanged: " + join(untouched))
	}

	if opts.Commit {
		if err := commit(opts.Root, bump); err != nil {
			render.Warn("commit skipped: " + err.Error())
		} else {
			render.Success("staged and committed (not pushed)")
		}
	} else {
		render.Info("not committed — review the diff, then commit when ready")
	}

	render.Dashboard("Release prepared",
		[][2]string{
			{"App", render.Value(bump.App.Name)},
			{"Service", render.Value(bump.Target())},
			{"Tag", bump.OldTag + " → " + render.Value(bump.NewTag)},
			{"Namespace", bump.App.Namespace},
			{"Untouched", join(untouched)},
		},
		[]string{
			"git push the platform repo so ArgoCD can see it",
			"launchpad status " + bump.App.Name + "   to watch only " + bump.Target() + " roll",
		},
	)
	return nil
}

// otherWorkloads lists the Deployments a release leaves alone. Naming them is
// the point of the per-service model, so the CLI says it out loud — and for a
// canary service that means naming the *sibling versions* too, since releasing
// catalog/v2 must leave catalog/v1 and catalog/v3 exactly where they were.
func otherWorkloads(b gitops.Bump) []string {
	var out []string
	for _, s := range b.App.Services {
		if !s.IsCanary() {
			if s.Name != b.Service {
				out = append(out, s.Name)
			}
			continue
		}
		for _, v := range s.Versions {
			if s.Name == b.Service && v.Name == b.Version {
				continue
			}
			out = append(out, s.Name+"/"+v.Name)
		}
	}
	return out
}

func join(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func commit(root string, b gitops.Bump) error {
	args := append([]string{"-C", root, "add"}, b.Written...)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s", out)
	}
	msg := fmt.Sprintf("launchpad: release %s/%s %s", b.App.Name, b.Target(), b.NewTag)
	if out, err := exec.Command("git", "-C", root, "commit", "-m", msg).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s", out)
	}
	return nil
}
