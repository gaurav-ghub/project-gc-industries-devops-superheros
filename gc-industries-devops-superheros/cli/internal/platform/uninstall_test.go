package platform

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// A fakeFS records what uninstall tried to do to the disk.
type fakeFS struct {
	removed  []string
	renamed  [][2]string
	refuseRm bool // the running-binary case: unlink fails, rename works
	refuseMv bool
}

func (f *fakeFS) remove(p string) error {
	if f.refuseRm {
		return errors.New("The process cannot access the file because it is being used by another process")
	}
	f.removed = append(f.removed, p)
	return nil
}

func (f *fakeFS) rename(from, to string) error {
	if f.refuseMv {
		return errors.New("access is denied")
	}
	f.renamed = append(f.renamed, [2]string{from, to})
	return nil
}

func uninstallOpts(root string, fs *fakeFS, exe string, clusters func() ([]string, error)) UninstallOptions {
	return UninstallOptions{
		Root: root, Yes: true,
		executable: func() (string, error) { return exe, nil },
		lookPath:   func(string) (string, error) { return "", errors.New("not on PATH") },
		remove:     fs.remove,
		rename:     fs.rename,
		tempDir:    func() string { return "/tmp" },
		clusters:   clusters,
	}
}

// TestUninstallRemovesTheBinaryAndNotTheCluster.
//
// The whole reason this command exists as a separate verb: `destroy` takes the
// cluster, `uninstall` takes the binary, and neither does the other's job.
func TestUninstallRemovesTheBinaryAndNotTheCluster(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	fs := &fakeFS{}

	if err := Uninstall(uninstallOpts(root, fs, "/opt/bin/endurance", running(root))); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(fs.removed) != 1 {
		t.Fatalf("removed %v, want one file", fs.removed)
	}
	got := buf.String()
	if !strings.Contains(got, "still running") {
		t.Errorf("uninstall did not say the cluster survives:\n%s", got)
	}
	if !strings.Contains(got, "endurance destroy") {
		t.Errorf("uninstall does not name the command that removes the cluster:\n%s", got)
	}
}

// TestUninstallSaysMovedWhenItCouldOnlyMove.
//
// Windows will not unlink the image of a running process. Renaming it takes it
// off PATH, which is the outcome the user asked for — and a closing box that
// said "removed" about a file still sitting on the disk would be the same
// untruth as a success screen claiming health it never observed. Found by
// running the command, which is where these are always found.
func TestUninstallSaysMovedWhenItCouldOnlyMove(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	fs := &fakeFS{refuseRm: true}

	if err := Uninstall(uninstallOpts(root, fs, "/opt/bin/endurance.exe", noClusters)); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if len(fs.renamed) != 1 {
		t.Fatalf("renamed %v, want one file", fs.renamed)
	}
	got := buf.String()
	if !strings.Contains(got, "moved") {
		t.Errorf("the move is not reported:\n%s", got)
	}
	if strings.Contains(got, "(removed)") {
		t.Errorf("the box claims a removal that did not happen:\n%s", got)
	}
	if !strings.Contains(got, "Endurance CLI moved off PATH") {
		t.Errorf("the title claims more than happened:\n%s", got)
	}
}

// TestUninstallReportsAFileItCouldNotRemove — neither unlink nor rename worked,
// so nothing happened and the command says so rather than closing with a box.
func TestUninstallReportsAFileItCouldNotRemove(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	fs := &fakeFS{refuseRm: true, refuseMv: true}

	err := Uninstall(uninstallOpts(root, fs, "/opt/bin/endurance", noClusters))
	if err == nil {
		t.Fatal("a failed removal was reported as success")
	}
	if got := buf.String(); strings.Contains(got, "CLI removed") {
		t.Errorf("the success box printed after a failure:\n%s", got)
	}
}

// TestUninstallAsksFirst — it removes something, so it asks, and answering no
// leaves the file alone.
func TestUninstallAsksFirst(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	fs := &fakeFS{}

	opts := uninstallOpts(root, fs, "/opt/bin/endurance", noClusters)
	opts.Yes = false
	opts.Confirm = func(string) (bool, error) { return false, nil }

	if err := Uninstall(opts); err != nil {
		t.Fatalf("declining returned an error: %v", err)
	}
	if len(fs.removed)+len(fs.renamed) != 0 {
		t.Fatal("the binary was removed after the user said no")
	}
	if got := buf.String(); !strings.Contains(got, "nothing was removed") {
		t.Errorf("the outcome was not stated:\n%s", got)
	}
}

// TestTheTwoRemovalsNameEachOther.
//
// destroy takes the cluster and uninstall takes the binary. Confusing them is
// expensive in both directions — destroying a cluster you meant to keep costs
// ten minutes, and removing the binary you meant to keep leaves a kind cluster
// running with the tool that knew how to delete it gone. So each command's
// output points at the other, and this fails if either stops.
func TestTheTwoRemovalsNameEachOther(t *testing.T) {
	root := repoRoot(t)

	buf := capture(t)
	if err := Destroy(DestroyOptions{
		Root: root, Yes: true, run: (&fakeRun{}).run, clusters: running(root),
	}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "endurance uninstall") {
		t.Errorf("destroy does not mention uninstall:\n%s", got)
	}

	buf = capture(t)
	if err := Uninstall(uninstallOpts(root, &fakeFS{}, "/opt/bin/endurance", noClusters)); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "endurance destroy") {
		t.Errorf("uninstall does not mention destroy:\n%s", got)
	}
}

// TestUninstallRefusesToGuessWhenItCannotAsk — same rule as destroy.
func TestUninstallRefusesToGuessWhenItCannotAsk(t *testing.T) {
	capture(t) // not a TTY
	ok, err := askUninstallConfirm("Remove the endurance binary?")
	if ok {
		t.Fatal("a prompt that could not be shown was answered yes")
	}
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the error does not name the flag: %v", err)
	}
}

// TestInspectClusterNoticesAClusterThatPublishesNothing.
//
// The Phase 10 trap, and the reason `endurance init` asks before it spends ten
// minutes: kind fixes extraPortMappings at cluster creation, so a cluster made
// before the access layer installs every module perfectly and answers nothing on
// the host. There is no runtime signal — from inside, everything worked.
func TestInspectClusterNoticesAClusterThatPublishesNothing(t *testing.T) {
	root := repoRoot(t)
	name := ClusterName(root)

	cases := []struct {
		what             string
		list             func() ([]string, error)
		port             func(string, int) (string, error)
		exists, publishd bool
	}{
		{
			what: "no cluster at all",
			list: noClusters,
			port: func(string, int) (string, error) { return "", errors.New("no such container") },
		},
		{
			what:   "a cluster that publishes the port",
			list:   running(root),
			port:   func(string, int) (string, error) { return "0.0.0.0:8080\n", nil },
			exists: true, publishd: true,
		},
		{
			what:   "a cluster created before the access layer",
			list:   running(root),
			port:   func(string, int) (string, error) { return "", errors.New("Error: No public port") },
			exists: true,
		},
		{
			what:   "docker answered with nothing, which is not a port",
			list:   running(root),
			port:   func(string, int) (string, error) { return "  \n", nil },
			exists: true,
		},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			st := inspectCluster(root, c.list, c.port)
			if st.Name != name {
				t.Errorf("cluster name %q, want %q", st.Name, name)
			}
			if st.Exists != c.exists {
				t.Errorf("Exists = %v, want %v", st.Exists, c.exists)
			}
			if st.Published != c.publishd {
				t.Errorf("Published = %v, want %v", st.Published, c.publishd)
			}
			if want, _ := HostPort(root); st.HostPort != want {
				t.Errorf("HostPort = %d, want %d", st.HostPort, want)
			}
		})
	}
}

// TestCheckHealthSaysNothing.
//
// `endurance init` uses it to decide whether to spend ten minutes on a
// bootstrap, in the middle of a run that is already drawing its own screens. A
// health check that printed a component list there would be a second status
// command nobody asked for.
func TestCheckHealthSaysNothing(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	all := func(args ...string) (string, error) {
		if len(args) > 1 && args[1] == "nodes" {
			return "kind-control-plane   Ready   control-plane   10m   v1.31.0\n", nil
		}
		return "pod-abc   1/1   Running   0   5m\n", nil
	}
	h := checkHealth(all, root)
	if !h.Complete() {
		t.Errorf("every component answered Running but Complete() is false: %+v", h)
	}
	if h.Ready != h.Total || h.Total != len(components) {
		t.Errorf("got %d of %d, want %d of %d", h.Ready, h.Total, len(components), len(components))
	}
	if got := buf.String(); got != "" {
		t.Errorf("CheckHealth wrote to the terminal:\n%s", got)
	}
}

// TestCheckHealthIsNotCompleteWithoutACluster — the answer init needs when there
// is nothing there: not reachable, and therefore not complete, without pretending
// the component count means anything.
func TestCheckHealthIsNotCompleteWithoutACluster(t *testing.T) {
	root := repoRoot(t)
	capture(t)

	down := func(args ...string) (string, error) {
		return "Unable to connect to the server", fmt.Errorf("exit status 1")
	}
	h := checkHealth(down, root)
	if h.Reachable || h.Complete() {
		t.Errorf("an unreachable cluster reported as healthy: %+v", h)
	}
	if h := checkHealth(nil, root); h.Reachable || h.Complete() {
		t.Errorf("no kubectl reported as healthy: %+v", h)
	}
}
