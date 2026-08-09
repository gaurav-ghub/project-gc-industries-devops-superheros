// Package offboard implements `endurance offboard` — onboard's inverse (14.6).
//
// # The fault this exists for
//
// `endurance onboard` has no opposite. `catalog list` read `apps/*/app.yaml`
// on a fresh clone of the platform repo and reported eleven applications;
// nine were left behind by the author's own earlier test runs and inherited
// by the fork, because nothing had ever removed one. `apps/bad-app` was a
// tenth, and it cost Phase 13's Part B twenty minutes on its own: `init`
// *regenerated* the leftover instead of creating it, and ArgoCD was already
// syncing the old image before the push, producing an ImagePullBackOff
// symptomatically identical to the one under test and caused by something
// else entirely. Clearing it by hand was two commands:
//
//	kubectl -n argocd delete application bad-app
//	kubectl delete ns bad-app
//
// in that order, because ArgoCD's selfHeal recreates a namespace deleted
// first — delete the thing that watches before the thing it would restore.
// That ordering is this package's specification, not a detail of it.
//
// # What it removes, and in what order
//
//  1. The ArgoCD Application. Deleting the registration first means nothing is
//     watching this namespace by the time step 2 runs.
//  2. The namespace ArgoCD created for it — every workload along with it.
//  3. apps/<app>/ and specs/<app>.yaml, so a fresh clone of the platform repo
//     answers `catalog list` with only the applications actually onboarded to
//     it, not every one anybody has ever tried.
package offboard

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/prompt"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/version"
)

// Options configures one offboard.
type Options struct {
	Root   string // platform repo root
	App    string // the registered application to remove
	Yes    bool   // skip the confirmation
	Commit bool   // stage + commit the removal (never pushes)

	// The edges of the world. Tests replace them; the CLI never sets any —
	// same rule as the rest of this tool's break-glass and destructive verbs.
	Confirm func(question string) (bool, error)
	Kubectl func(args ...string) (string, error)
}

// Run removes a registered application: the cluster's half, then the repo's.
func Run(opts Options) error {
	render.Banner(version.Current)

	root, err := platform.FindRoot(opts.Root)
	if err != nil {
		return err
	}
	app, err := gitops.Load(root, opts.App)
	if err != nil {
		return fmt.Errorf("no registered app %q — `endurance catalog list` shows what is (%v)", opts.App, err)
	}

	render.Section("Offboard · " + app.Name)
	render.Info("deletes the ArgoCD Application, then the namespace " + render.Value(app.Namespace))
	render.Detail("in that order — deleting the namespace first leaves ArgoCD's selfHeal free to recreate it")
	render.Detail("removes apps/" + app.Name + "/ and specs/" + app.Name + ".yaml from this repo")
	render.Blank()

	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = askConfirm
		}
		ok, err := confirm("Offboard " + app.Name + "?")
		if err != nil {
			return err
		}
		if !ok {
			render.Info("nothing was removed")
			return nil
		}
	}

	removeFromCluster(resolveKubectl(opts.Kubectl), app)

	removed, err := removeFiles(root, app.Name)
	if err != nil {
		return err
	}
	for _, r := range removed {
		render.Success("removed " + r)
	}
	if len(removed) == 0 {
		render.Info("nothing under apps/ or specs/ to remove — the registry was already gone")
	}

	if opts.Commit && len(removed) > 0 {
		if err := gitops.Commit(root, removed, "endurance: offboard "+app.Name); err != nil {
			render.Warn("commit skipped: " + err.Error())
		} else {
			render.Success("staged and committed, not pushed — " + gitops.HeadSubject(root))
		}
	} else if len(removed) > 0 {
		render.Info("not committed — review the removal, then commit when ready")
	}
	return nil
}

// removeFromCluster deletes the Application and then the namespace, in the
// order that is this package's whole reason to exist. A resource already gone
// is not a failure — `offboard` run twice, or run after a `destroy`, must say
// so rather than error.
func removeFromCluster(kube func(args ...string) (string, error), app spec.App) {
	if kube == nil {
		render.Warn("kubectl is not on PATH — nothing was removed from the cluster")
		render.Detail("kubectl -n argocd delete application " + app.Name)
		render.Detail("kubectl delete namespace " + app.Namespace + "   (in that order)")
		return
	}
	if out, err := kube("-n", "argocd", "delete", "application", app.Name, "--ignore-not-found"); err != nil {
		render.Warn("could not delete the ArgoCD Application: " + firstLine(out))
	} else {
		render.Success("deleted ArgoCD Application " + app.Name)
	}
	if out, err := kube("delete", "namespace", app.Namespace, "--ignore-not-found"); err != nil {
		render.Warn("could not delete namespace " + app.Namespace + ": " + firstLine(out))
	} else {
		render.Success("deleted namespace " + app.Namespace)
	}
}

// removeFiles deletes apps/<name>/ and specs/<name>.yaml, and returns the
// paths that existed and were removed — exactly what a caller should stage
// for the removal commit, and empty when there was nothing to do.
func removeFiles(root, name string) ([]string, error) {
	var removed []string
	appDir := gitops.AppDir(root, name)
	if _, err := os.Stat(appDir); err == nil {
		if err := os.RemoveAll(appDir); err != nil {
			return removed, fmt.Errorf("removing %s: %w", appDir, err)
		}
		removed = append(removed, appDir)
	}
	specPath := gitops.SpecPath(root, name)
	if _, err := os.Stat(specPath); err == nil {
		if err := os.Remove(specPath); err != nil {
			return removed, fmt.Errorf("removing %s: %w", specPath, err)
		}
		removed = append(removed, specPath)
	}
	return removed, nil
}

func resolveKubectl(k func(args ...string) (string, error)) func(args ...string) (string, error) {
	if k != nil {
		return k
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil
	}
	return func(args ...string) (string, error) {
		out, err := exec.Command("kubectl", args...).CombinedOutput()
		return string(out), err
	}
}

// askConfirm prompts on a terminal, and refuses to guess anywhere else — the
// same rule every destructive verb in this tool follows.
func askConfirm(question string) (bool, error) {
	if !render.Default().IsTTY() {
		return false, fmt.Errorf("offboard needs confirmation and this is not a terminal — re-run with --yes")
	}
	answer := false
	err := prompt.Run(huh.NewConfirm().
		Title(question).
		Description("deletes the ArgoCD Application, the namespace and everything in it, and the registry files").
		Affirmative("Offboard it").
		Negative("Keep it").
		Value(&answer))
	return answer, err
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' || s[i] == '\r' {
			return s[:i]
		}
	}
	return s
}
