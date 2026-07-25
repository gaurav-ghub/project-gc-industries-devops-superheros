package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// `endurance uninstall` — remove the CLI.
//
// # The distinction this command exists to make
//
// There are two things a user might mean by "remove Endurance", and they are
// not the same thing at all:
//
//   - `endurance destroy` deletes the kind cluster and everything installed in
//     it. The binary survives, and a later `bootstrap` brings the platform back.
//   - `endurance uninstall` deletes the binary. The cluster survives, and
//     nothing in this repo is touched.
//
// Confusing them is expensive in both directions — destroying a cluster when
// you meant to remove a binary costs ten minutes, and removing the binary when
// you meant to destroy the cluster leaves a kind cluster running with the tool
// that knew how to delete it gone. So both commands name the other one, and
// uninstall refuses to be quiet about a cluster it is about to orphan.
//
// # What it does not do
//
// It does not uninstall the platform from a cluster, and it does not delete
// apps/, specs/ or anything else in git. Phase 12 adds the installer this is
// the other half of; until then the binary is wherever `go build` put it, and
// removing it is the whole job.

// UninstallOptions configures removing the CLI.
type UninstallOptions struct {
	Root string // platform repo root ("" = discover; only used to name the cluster)
	Yes  bool   // skip the confirmation

	// The edges of the operating system this command touches. Tests replace
	// them; the CLI never sets any of them.
	Confirm    func(prompt string) (bool, error)
	executable func() (string, error)
	lookPath   func(name string) (string, error)
	remove     func(path string) error
	rename     func(from, to string) error
	tempDir    func() string
	clusters   func() ([]string, error)
}

// binaryName is what the installed CLI is called. `.exe` on Windows is added by
// the toolchain, and os.Executable reports whatever the file is actually named,
// so this is only used for the PATH lookup.
const binaryName = "endurance"

// Uninstall removes the CLI binary from this machine.
func Uninstall(opts UninstallOptions) error {
	render.Banner(version.Current)
	render.Section("Uninstall the Endurance CLI")

	exe, lookedUp := uninstallTargets(opts)
	if len(exe) == 0 {
		render.Info("could not work out which file is running — nothing was removed")
		render.Detail("delete the `endurance` binary by hand; it is a single self-contained file")
		return fmt.Errorf("the running binary could not be located")
	}

	render.Info("this removes the CLI itself:")
	for _, p := range exe {
		render.Detail(shortPath(p))
	}
	if !lookedUp {
		render.Detail("nothing named " + binaryName + " is on PATH — this is the file you ran")
	}
	render.Blank()

	// The thing a user is most likely to get wrong here, said before they
	// answer rather than after: the cluster is not what this deletes.
	render.Info("`endurance destroy` is the other command — it deletes the kind cluster")
	render.Detail("this one leaves the cluster running and touches nothing in git")
	render.Blank()

	cluster := ClusterName(rootOrEmpty(opts.Root))
	list := opts.clusters
	if list == nil {
		list = kindClusters
	}
	present, known := clusterPresent(list, cluster)
	if known && present {
		// Removing the tool that knows how to delete the cluster is allowed, and
		// it is worth one sentence: kind is what is left to do it with.
		render.Warn("the kind cluster " + render.Value(cluster) + " is still running")
		render.Detail("it outlives this binary · `endurance destroy` first, or `kind delete cluster --name " + cluster + "` later")
		render.Blank()
	}

	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = askUninstallConfirm
		}
		ok, err := confirm("Remove the endurance binary?")
		if err != nil {
			return err
		}
		if !ok {
			render.Info("nothing was removed")
			return nil
		}
	}

	removed, moved, failed := removeAll(opts, exe)

	for _, p := range removed {
		render.Success("removed " + shortPath(p))
	}
	for _, m := range moved {
		// A running binary cannot always delete itself — Windows holds an open
		// handle on the image for the life of the process. Moving it out of the
		// way is the same outcome for anyone typing `endurance`, and saying
		// "removed" about a file that is still there would not be.
		render.Warn("moved " + shortPath(m.from) + " out of the way")
		render.Detail("a running binary cannot delete itself on this platform")
		render.Detail("it is now " + shortPath(m.to) + " · delete it once this process exits")
	}
	for _, f := range failed {
		render.Error("could not remove " + shortPath(f.path) + ": " + f.err.Error())
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s could not be removed", plural(len(failed), "file"))
	}

	// What the box says has to be what happened. A file this command could only
	// rename is off PATH, which is the outcome asked for, and it is still on the
	// disk — saying "removed" about it would be the success-screen untruth this
	// project has fixed twice already.
	outcome := "removed"
	switch {
	case len(removed) == 0 && len(moved) > 0:
		outcome = "moved out of the way, not deleted"
	case len(moved) > 0:
		outcome = plural(len(removed), "file") + " removed, " +
			plural(len(moved), "file") + " moved out of the way"
	}
	rows := [][2]string{
		{"CLI", render.Value(binaryName) + " " + render.Muted("("+outcome+")")},
	}
	if known && present {
		rows = append(rows, [2]string{"Cluster",
			render.Value(cluster) + " " + render.Muted("(still running — this command does not touch it)")})
	} else {
		rows = append(rows, [2]string{"Cluster", render.Muted("no kind cluster named " + cluster)})
	}
	rows = append(rows, [2]string{"Git", render.Muted("untouched — `apps/`, `specs/` and `platform/` are still here")})

	next := []string{
		"go build -o endurance ./cli   rebuilds it from this repo",
	}
	if len(moved) > 0 {
		next = append(next, "delete "+shortPath(moved[0].to)+"   once this process has exited")
	}
	if known && present {
		next = append(next, "kind delete cluster --name "+cluster+"   removes the cluster without the CLI")
	}
	title := "Endurance CLI removed"
	if len(removed) == 0 && len(moved) > 0 {
		title = "Endurance CLI moved off PATH"
	}
	render.Dashboard(title, rows, next)
	return nil
}

// rootOrEmpty resolves the platform repo when it can, so the cluster is named
// from platform/lib/version.sh rather than guessed. Uninstalling a binary is
// not a reason to require a repo, so a failure here is not an error.
func rootOrEmpty(start string) string {
	if root, err := FindRoot(start); err == nil {
		return root
	}
	return ""
}

// uninstallTargets lists the files to remove: the running binary, plus a
// different file of the same name on PATH.
//
// Both, because they are routinely different — this repo builds to cli/endurance
// and Phase 12's installer will put a copy somewhere on PATH, and removing only
// the one that happens to be running leaves the other answering the same
// command. Same file twice is deduplicated by path.
func uninstallTargets(opts UninstallOptions) (paths []string, lookedUp bool) {
	executable := opts.executable
	if executable == nil {
		executable = os.Executable
	}
	lookPath := opts.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			p = resolved
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	if p, err := executable(); err == nil {
		add(p)
	}
	if p, err := lookPath(binaryName); err == nil {
		add(p)
		lookedUp = true
	}
	return paths, lookedUp
}

type movedFile struct{ from, to string }
type failedFile struct {
	path string
	err  error
}

// removeAll deletes each target, falling back to moving it out of the way.
func removeAll(opts UninstallOptions, paths []string) (removed []string, moved []movedFile, failed []failedFile) {
	rm := opts.remove
	if rm == nil {
		rm = os.Remove
	}
	mv := opts.rename
	if mv == nil {
		mv = os.Rename
	}
	tmp := opts.tempDir
	if tmp == nil {
		tmp = os.TempDir
	}

	for _, p := range paths {
		err := rm(p)
		if err == nil {
			removed = append(removed, p)
			continue
		}
		// Windows refuses to unlink the image of a running process. A rename is
		// permitted, and it takes the file off PATH, which is what the user
		// asked for. Anything else is reported as the failure it is.
		dest := filepath.Join(tmp(), filepath.Base(p)+".removed")
		if mvErr := mv(p, dest); mvErr == nil {
			moved = append(moved, movedFile{from: p, to: dest})
			continue
		}
		failed = append(failed, failedFile{path: p, err: err})
	}
	return removed, moved, failed
}

// askUninstallConfirm prompts on a terminal and refuses to guess anywhere else,
// for the same reason `destroy` does: a confirmation that cannot be shown must
// not be assumed.
func askUninstallConfirm(prompt string) (bool, error) {
	if !render.Default().IsTTY() {
		return false, fmt.Errorf("uninstall needs confirmation and this is not a terminal — re-run with --yes")
	}
	answer := false
	err := huh.NewConfirm().
		Title(prompt).
		Description("the binary only · the cluster and this repo are untouched").
		Affirmative("Remove it").
		Negative("Keep it").
		Value(&answer).
		Run()
	return answer, err
}

// InspectCluster reports what `endurance init` needs to know before it offers
// to build anything: whether the platform's cluster already exists, and whether
// it is one this build can actually reach.
//
// The second half is the Phase 10 trap. kind fixes extraPortMappings at cluster
// creation time, so a cluster created before the access layer installs every
// module perfectly, reports ten of ten components healthy, and answers nothing
// on the host. A first-run command that spent ten minutes reinstalling into one
// of those and then handed over five dead links would be the worst possible
// first impression, so init asks first.
type ClusterState struct {
	Name      string
	Exists    bool
	Known     bool // kind could be asked at all
	Published bool // the node publishes the host port kind-config.yaml declares
	HostPort  int
}

// InspectCluster looks at the machine without changing anything on it.
func InspectCluster(root string) ClusterState {
	return inspectCluster(root, kindClusters, dockerPort)
}

func inspectCluster(root string, list func() ([]string, error), port func(node string, container int) (string, error)) ClusterState {
	st := ClusterState{Name: ClusterName(root)}
	st.HostPort, _ = HostPort(root)
	st.Exists, st.Known = clusterPresent(list, st.Name)
	if !st.Exists {
		return st
	}
	out, err := port(st.Name+"-control-plane", ingressNodePort)
	st.Published = err == nil && strings.TrimSpace(out) != ""
	return st
}

// dockerPort asks docker what a node container publishes. It is the same
// question platform/scripts/cluster.sh asks, from the other side: cluster.sh
// warns during a bootstrap, and this lets init refuse before one starts.
func dockerPort(node string, container int) (string, error) {
	out, err := exec.Command("docker", "port", node, fmt.Sprint(container)+"/tcp").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker port %s: %s", node, firstLine(string(out)))
	}
	return string(out), nil
}
