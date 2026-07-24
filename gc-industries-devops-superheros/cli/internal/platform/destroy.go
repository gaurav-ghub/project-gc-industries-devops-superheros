package platform

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// `endurance destroy` — delete the cluster the platform runs on.
//
// Everything Endurance installed lives inside that one kind cluster, so this is
// the whole teardown; nothing in git is touched and no application is
// unregistered. `apps/` still holds every onboarded application, which is what
// makes destroy safe to reach for: the next bootstrap plus a git push puts it
// all back.
//
// It asks first. This is the only command in the CLI that removes something a
// user spent ten minutes waiting for, and a --yes flag exists so a script can
// answer in advance rather than by having no prompt at all.

// DestroyOptions configures a teardown.
type DestroyOptions struct {
	Root    string // platform repo root ("" = discover)
	Yes     bool   // skip the confirmation
	Confirm func(prompt string) (bool, error)

	run      runFunc                  // tests replace it
	clusters func() ([]string, error) // tests replace it
}

// kindClusters lists the kind clusters on this machine.
func kindClusters() ([]string, error) {
	out, err := exec.Command("kind", "get", "clusters").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("kind get clusters: %s", firstLine(string(out)))
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" && !strings.Contains(name, " ") {
			names = append(names, name)
		}
	}
	return names, nil
}

// Destroy tears the local platform down.
func Destroy(opts DestroyOptions) error {
	render.Banner(version.Current)

	root, err := FindRoot(opts.Root)
	if err != nil {
		return err
	}
	run := opts.run
	if run == nil {
		run = runScript
	}
	cluster := ClusterName(root)
	list := opts.clusters
	if list == nil {
		list = kindClusters
	}

	render.Section("Destroy the local platform")

	// Whether there is anything to destroy is asked before anything is said
	// about destroying it. A teardown that reports a cluster "deleted" when
	// none existed is the same class of untruth as a success screen claiming
	// health it never observed — and the transcript underneath would contradict
	// it, which is worse.
	present, known := clusterPresent(list, cluster)
	switch {
	case known && !present:
		render.Info("no kind cluster named " + render.Value(cluster) + " — nothing to destroy")
		render.Info("the teardown still runs, to clear any kubectl context left behind")
		render.Blank()
	default:
		render.Info("cluster " + render.Value(cluster) + " and everything installed in it:")
		render.Detail("Istio, monitoring, AI alert enrichment, ArgoCD, Kyverno")
		render.Detail("every application ArgoCD deployed into it")
		render.Blank()
		render.Info("nothing in git is touched — `apps/` keeps every onboarded application, so a later `endurance bootstrap` brings the same platform back")
		render.Blank()
	}

	// Nothing to delete, nothing to confirm.
	if known && !present {
		opts.Yes = true
	}

	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = askConfirm
		}
		ok, err := confirm(fmt.Sprintf("Delete the kind cluster %q?", cluster))
		if err != nil {
			return err
		}
		if !ok {
			render.Info("nothing was deleted")
			return nil
		}
	}

	p := render.NewProgress("Destroying the platform", "Deleting the kind cluster")
	if err := runModule(run, p, root, Module{"Deleting the kind cluster", destroyScript}); err != nil {
		p.Finish()
		return err
	}
	p.Finish()

	title, verb := "Platform destroyed", "deleted"
	if known && !present {
		title, verb = "Nothing to destroy", "was not running"
	}
	render.Dashboard(title, [][2]string{
		{"Cluster", render.Value(cluster) + " " + render.Muted("("+verb+")")},
		{"Context", render.Value(ContextName(root)) + " " + render.Muted("(removed)")},
		{"Git", render.Muted("untouched — `apps/` still holds every application")},
	}, []string{
		"endurance bootstrap — stand the platform up again",
		"endurance doctor — check the tools first",
	})
	return nil
}

// clusterPresent reports whether the platform's cluster exists, and whether the
// answer is known at all. When kind cannot be asked — it is missing, or it
// errored — the teardown proceeds and says nothing it cannot support; the bash
// module is the one that decides, and it refuses without kind.
func clusterPresent(list func() ([]string, error), cluster string) (present, known bool) {
	names, err := list()
	if err != nil {
		return false, false
	}
	for _, n := range names {
		if n == cluster {
			return true, true
		}
	}
	return false, true
}

// askConfirm prompts on a terminal, and refuses to guess anywhere else.
//
// A confirmation that cannot be shown must not be assumed. Piped or scripted,
// destroy stops and names the flag that says yes on purpose.
func askConfirm(prompt string) (bool, error) {
	if !render.Default().IsTTY() {
		return false, fmt.Errorf("destroy needs confirmation and this is not a terminal — re-run with --yes")
	}
	answer := false
	err := huh.NewConfirm().
		Title(prompt).
		Description("the cluster and everything in it; git is untouched").
		Affirmative("Delete it").
		Negative("Keep it").
		Value(&answer).
		Run()
	return answer, err
}
