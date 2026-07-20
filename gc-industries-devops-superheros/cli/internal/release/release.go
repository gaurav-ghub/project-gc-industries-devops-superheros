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
	"github.com/gc-ghub/launchpad/internal/render"
	"github.com/gc-ghub/launchpad/internal/version"
)

// Options configures a release.
type Options struct {
	Root    string // platform repo root
	App     string // registered application name
	Service string // the one service to promote
	Tag     string // the image tag to promote to
	Commit  bool   // stage + commit the changed files (never pushes)
	DryRun  bool   // report what would change, write nothing
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

	// A dry run must not write, so resolve it against the loaded spec instead of
	// SetServiceTag. This is also the hook Phase 3's policy gate will use to
	// inspect a release before it is allowed to touch the repo.
	if opts.DryRun {
		return dryRun(opts)
	}

	bump, err := gitops.SetServiceTag(opts.Root, opts.App, opts.Service, opts.Tag)
	if err != nil {
		return err
	}

	if bump.NoOp {
		render.Warn(fmt.Sprintf("%s is already at %s — nothing to do",
			render.Value(opts.Service), render.Value(opts.Tag)))
		render.Info("no files changed, nothing to commit")
		return nil
	}

	render.Step(fmt.Sprintf("%s  %s → %s",
		render.Value(bump.Service), bump.OldTag, render.Value(bump.NewTag)))
	for _, w := range bump.Written {
		render.Success("wrote " + w)
	}

	untouched := otherServices(bump)
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
			{"Service", render.Value(bump.Service)},
			{"Tag", bump.OldTag + " → " + render.Value(bump.NewTag)},
			{"Namespace", bump.App.Namespace},
			{"Untouched", join(untouched)},
		},
		[]string{
			"git push the platform repo so ArgoCD can see it",
			"launchpad status " + bump.App.Name + "   to watch only " + bump.Service + " roll",
		},
	)
	return nil
}

// dryRun reports the change without writing anything.
func dryRun(opts Options) error {
	app, err := gitops.Load(opts.Root, opts.App)
	if err != nil {
		return fmt.Errorf("no registered app %q — run `launchpad list` to see what is registered", opts.App)
	}
	i := app.FindService(opts.Service)
	if i < 0 {
		return fmt.Errorf("app %q has no service %q — services are: %s",
			opts.App, opts.Service, join(app.ServiceNames()))
	}
	old := app.Services[i].Tag
	if old == opts.Tag {
		render.Warn(fmt.Sprintf("%s is already at %s — nothing to do",
			render.Value(opts.Service), render.Value(opts.Tag)))
		return nil
	}
	render.Step(fmt.Sprintf("would bump %s  %s → %s",
		render.Value(opts.Service), old, render.Value(opts.Tag)))
	render.Info("dry run — no files written")
	return nil
}

// otherServices lists the services a release leaves alone. Naming them is the
// point of the per-service model, so the CLI says it out loud.
func otherServices(b gitops.Bump) []string {
	var out []string
	for _, n := range b.App.ServiceNames() {
		if n != b.Service {
			out = append(out, n)
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
	msg := fmt.Sprintf("launchpad: release %s/%s %s", b.App.Name, b.Service, b.NewTag)
	if out, err := exec.Command("git", "-C", root, "commit", "-m", msg).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s", out)
	}
	return nil
}
