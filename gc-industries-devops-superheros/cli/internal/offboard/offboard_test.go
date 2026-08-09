package offboard

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

func runGit(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	return string(out), err
}

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// sandbox is a platform repo with one registered application on disk —
// apps/<name>/{app,application,values}.yaml and specs/<name>.yaml, the same
// files `onboard` writes and this package's whole job is to remove.
func sandbox(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", name)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(appDir, "app.yaml"),
		"name: "+name+"\nnamespace: "+name+"\nservices:\n  - name: "+name+"\n    image: i\n    tag: v1\n    port: 80\n")
	writeFile(t, filepath.Join(appDir, "application.yaml"), "kind: Application\n")
	writeFile(t, filepath.Join(appDir, "values.yaml"), "app: {}\n")
	writeFile(t, filepath.Join(root, "specs", name+".yaml"), "name: "+name+"\n")
	writeFile(t, filepath.Join(root, "platform/scripts/cluster.sh"), "#!/usr/bin/env bash\n")
	writeFile(t, filepath.Join(root, "platform/lib/version.sh"),
		"CLUSTER_NAME=\"endurance\"\nKUBERNETES_CONTEXT=\"kind-${CLUSTER_NAME}\"\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTheOrderIsApplicationThenNamespace — the whole reason this package
// exists over two hand-typed kubectl commands. `selfHeal` recreates a
// namespace deleted first, so deleting it before the Application that
// watches it is a teardown that undoes itself.
func TestTheOrderIsApplicationThenNamespace(t *testing.T) {
	root := sandbox(t, "bad-app")
	capture(t)

	var calls []string
	kube := func(args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		return "", nil
	}
	if err := Run(Options{Root: root, App: "bad-app", Yes: true, Kubectl: kube}); err != nil {
		t.Fatal(err)
	}
	var appIdx, nsIdx = -1, -1
	for i, c := range calls {
		if strings.Contains(c, "delete application") {
			appIdx = i
		}
		if strings.Contains(c, "delete namespace") {
			nsIdx = i
		}
	}
	if appIdx < 0 || nsIdx < 0 {
		t.Fatalf("did not delete both the Application and the namespace: %v", calls)
	}
	if appIdx > nsIdx {
		t.Fatalf("the namespace was deleted before the Application — this is exactly the "+
			"ordering fault that lets selfHeal recreate it: %v", calls)
	}
}

// TestOffboardRemovesTheRegistryFiles — the repo half. A fresh clone of the
// platform repo must answer `catalog list` with only what is actually
// onboarded to it.
func TestOffboardRemovesTheRegistryFiles(t *testing.T) {
	root := sandbox(t, "bad-app")
	capture(t)

	noop := func(args ...string) (string, error) { return "", nil }
	if err := Run(Options{Root: root, App: "bad-app", Yes: true, Kubectl: noop}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "bad-app")); !os.IsNotExist(err) {
		t.Error("apps/bad-app/ was not removed")
	}
	if _, err := os.Stat(filepath.Join(root, "specs", "bad-app.yaml")); !os.IsNotExist(err) {
		t.Error("specs/bad-app.yaml was not removed")
	}
}

// A resource already gone is not a failure — `offboard` run twice, or run
// after a `destroy`, must say so rather than error.
func TestAnAlreadyGoneResourceIsNotAnError(t *testing.T) {
	root := sandbox(t, "bad-app")
	capture(t)

	kube := func(args ...string) (string, error) {
		return "Error from server (NotFound): applications.argoproj.io \"bad-app\" not found", errors.New("exit 1")
	}
	if err := Run(Options{Root: root, App: "bad-app", Yes: true, Kubectl: kube}); err != nil {
		t.Fatalf("an already-gone Application failed the whole run: %v", err)
	}
}

// TestNoKubectlStillRemovesTheFiles — a machine with no cluster access can
// still clean the repo, and is told the two commands for the cluster half.
func TestNoKubectlStillRemovesTheFiles(t *testing.T) {
	root := sandbox(t, "bad-app")
	buf := capture(t)

	if err := Run(Options{Root: root, App: "bad-app", Yes: true, Kubectl: nil,
		Confirm: func(string) (bool, error) { return true, nil }}); err != nil {
		// resolveKubectl may find a real kubectl on the machine running this
		// test; either way the run must not fail.
		t.Fatalf("offboard with no injected kubectl errored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "bad-app")); !os.IsNotExist(err) {
		t.Error("apps/bad-app/ was not removed even with no cluster access")
	}
	_ = buf
}

// TestDecliningTheConfirmationRemovesNothing.
func TestDecliningTheConfirmationRemovesNothing(t *testing.T) {
	root := sandbox(t, "bad-app")
	buf := capture(t)

	called := false
	kube := func(args ...string) (string, error) { called = true; return "", nil }
	err := Run(Options{Root: root, App: "bad-app", Confirm: func(string) (bool, error) { return false, nil }, Kubectl: kube})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("a declined offboard still touched the cluster")
	}
	if _, statErr := os.Stat(filepath.Join(root, "apps", "bad-app")); statErr != nil {
		t.Error("a declined offboard removed the files anyway")
	}
	if !strings.Contains(buf.String(), "nothing was removed") {
		t.Errorf("a declined offboard did not say so:\n%s", buf.String())
	}
}

// TestAnUnregisteredApplicationIsRefused — there is nothing to remove and no
// cluster question worth asking, the same shape success.Screen refuses an
// unregistered application.
func TestAnUnregisteredApplicationIsRefused(t *testing.T) {
	root := sandbox(t, "bad-app")
	capture(t)

	if err := Run(Options{Root: root, App: "nope", Yes: true}); err == nil {
		t.Fatal("offboarding an application that was never registered was accepted")
	}
}

// TestCommitStagesTheRemoval — `git add` on an explicit path that no longer
// exists stages its deletion, which is the whole trick this relies on.
func TestCommitStagesTheRemoval(t *testing.T) {
	root := sandbox(t, "bad-app")
	capture(t)
	initGitRepo(t, root)

	noop := func(args ...string) (string, error) { return "", nil }
	if err := Run(Options{Root: root, App: "bad-app", Yes: true, Commit: true, Kubectl: noop}); err != nil {
		t.Fatal(err)
	}
	out, err := runGit(root, "log", "-1", "--stat")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "bad-app") {
		t.Errorf("the removal was not committed:\n%s", out)
	}
	st, err := runGit(root, "status", "--short")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(st) != "" {
		t.Errorf("the working tree is not clean after the commit:\n%s", st)
	}
}

func initGitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"add", "-A"},
		{"commit", "-m", "initial"},
	} {
		if _, err := runGit(root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
}
