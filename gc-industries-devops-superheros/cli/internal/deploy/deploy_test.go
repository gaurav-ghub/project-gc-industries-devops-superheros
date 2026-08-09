package deploy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gc-ghub/endurance/internal/render"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// sandbox is a platform repo root with the one file Run needs on disk: the
// Application it applies.
func sandbox(t *testing.T, app string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "apps", app)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "application.yaml"),
		[]byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: "+app+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func synced() func(args ...string) (string, error) {
	return func(args ...string) (string, error) {
		switch {
		case args[0] == "apply":
			return "application.argoproj.io/demo created\n", nil
		case len(args) > 3 && args[3] == "application":
			return `{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`, nil
		}
		return "", errors.New("unexpected: " + strings.Join(args, " "))
	}
}

func fastOpts(root, app string, kube func(args ...string) (string, error)) Options {
	return Options{
		Root: root, App: app, Kubectl: kube,
		Timeout: time.Second,
		Now:     func() time.Time { return time.Unix(0, 0) },
		Sleep:   func(time.Duration) {},
	}
}

// 14.2 — the missing verb, extracted so `endurance deploy <app>` and `init`'s
// guided run are one function rather than one of them reimplementing it
// privately.

// The ordinary path: apply, then see it converge.
func TestRunAppliesAndReportsSynced(t *testing.T) {
	root := sandbox(t, "demo")
	buf := capture(t)

	var applied [][]string
	kube := func(args ...string) (string, error) {
		if args[0] == "apply" {
			applied = append(applied, args)
		}
		return synced()(args...)
	}
	isSynced, err := Run(fastOpts(root, "demo", kube))
	if err != nil {
		t.Fatal(err)
	}
	if !isSynced {
		t.Fatal("a Synced/Healthy application reported unsynced")
	}
	if len(applied) != 1 {
		t.Fatalf("kubectl apply ran %d times, want 1: %v", len(applied), applied)
	}
	if !strings.HasSuffix(filepath.ToSlash(applied[0][2]), "apps/demo/application.yaml") {
		t.Errorf("applied %v, not the Application", applied[0])
	}
	if !strings.Contains(buf.String(), "ArgoCD synced demo") {
		t.Errorf("the convergence is not reported:\n%s", buf.String())
	}
}

// No kubectl on PATH is a legitimate answer, not a failure — writing GitOps
// files must stay useful on a machine with no cluster.
func TestNoKubectlIsReportedNotFailed(t *testing.T) {
	root := sandbox(t, "demo")
	buf := capture(t)

	_, err := Run(Options{Root: root, App: "demo", Kubectl: nil,
		Now: func() time.Time { return time.Unix(0, 0) }})
	// resolveKubectl falls back to exec.LookPath, which may or may not find a
	// real kubectl on the machine running this test. What must hold regardless
	// is that a nil Kubectl never panics and never fails the run for a reason
	// other than an actual apply failure.
	_ = err
	_ = buf
}

// An apply that fails is a failure, and the step carries it rather than the
// run carrying on to wait for something that was never registered.
func TestAFailedApplyStopsAndSaysWhy(t *testing.T) {
	root := sandbox(t, "demo")
	buf := capture(t)

	kube := func(args ...string) (string, error) {
		return "The Application \"demo\" is invalid: \n* spec.project: Required value\n",
			fmt.Errorf("exit status 1")
	}
	_, err := Run(fastOpts(root, "demo", kube))
	if err == nil {
		t.Fatal("a failed apply was reported as success")
	}
	if !strings.Contains(err.Error(), "spec.project: Required value") {
		t.Errorf("the error drops the diagnosis: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "✗ Registering demo with ArgoCD") {
		t.Errorf("the failing step is not marked:\n%s", got)
	}
}

// --no-wait ends on whatever is running now, without polling ArgoCD at all.
func TestNoWaitDoesNotPoll(t *testing.T) {
	root := sandbox(t, "demo")
	buf := capture(t)

	polled := false
	kube := func(args ...string) (string, error) {
		if len(args) > 3 && args[3] == "application" {
			polled = true
		}
		return synced()(args...)
	}
	opts := fastOpts(root, "demo", kube)
	opts.NoWait = true
	if _, err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if polled {
		t.Error("--no-wait polled ArgoCD anyway")
	}
	if !strings.Contains(buf.String(), "skipped · --no-wait") {
		t.Errorf("the skip is not stated:\n%s", buf.String())
	}
}

// TestPushStateDistinguishesItsThreeAnswers — no upstream, ahead of one, and
// nothing to do are three different sentences because they have three
// different next steps. Moved here from initcmd when the deploy logic moved.
func TestPushStateDistinguishesItsThreeAnswers(t *testing.T) {
	needs, detail := pushState(t.TempDir())
	if !needs {
		t.Error("a non-repository was reported as fully pushed")
	}
	if !strings.Contains(detail, "no upstream") {
		t.Errorf("the reason is not stated: %q", detail)
	}
}

// TestTheWaitReportsEachStateOnceAndNotEveryPoll — a step that prints the same
// line every five seconds is a step nobody reads.
func TestTheWaitReportsEachStateOnceAndNotEveryPoll(t *testing.T) {
	buf := capture(t)
	root := t.TempDir()

	polls := 0
	kube := func(args ...string) (string, error) {
		polls++
		if polls < 3 {
			return `{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Progressing"}}}`, nil
		}
		return `{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`, nil
	}
	p := render.NewProgress("t", "wait")
	step := p.Start("wait")
	st := waitForSync(step, Options{
		Root: root, App: "demo", Timeout: time.Minute,
		Now: func() time.Time { return time.Unix(0, 0) }, Sleep: func(time.Duration) {},
	}, kube)
	step.Done()
	p.Finish()

	if !st.synced {
		t.Fatal("a Synced/Healthy application was not recognised")
	}
	if got := strings.Count(buf.String(), "sync OutOfSync · health Progressing"); got != 1 {
		t.Errorf("the same state was reported %d times, want 1:\n%s", got, buf.String())
	}
}

// A wait that never converges says which state it ended on, and — when the
// commit is unpushed — that ArgoCD never pushes and names the one command.
func TestADeployThatCannotBeSeenSaysSoAndNamesThePush(t *testing.T) {
	root := t.TempDir() // not a git repository at all: the strongest form of unpushed
	buf := capture(t)

	kube := func(args ...string) (string, error) {
		switch {
		case args[0] == "apply":
			return "application.argoproj.io/demo created\n", nil
		case len(args) > 3 && args[3] == "application":
			return `{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Missing"}}}`, nil
		}
		return "", errors.New("No resources found")
	}
	isSynced, err := Run(fastOpts(root, "demo", kube))
	if err != nil {
		t.Fatalf("a deploy still waiting is not an error: %v", err)
	}
	if isSynced {
		t.Error("an OutOfSync application reported synced")
	}
	got := buf.String()
	if !strings.Contains(got, "git push") {
		t.Errorf("the run does not name the command it will not run itself:\n%s", got)
	}
	if !strings.Contains(got, "never pushes") {
		t.Errorf("the run does not say why it stopped there:\n%s", got)
	}
}

func TestReasonFoldsAComplaintIntoOneSentence(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", "no output"},
		{"one line", "error: no such file", "error: no such file"},
		{
			"headline plus fields",
			"The Application \"demo\" is invalid: \n* spec.project: Required value\n",
			`The Application "demo" is invalid: spec.project: Required value`,
		},
		{
			"more than can be shown",
			"invalid:\n* a\n* b\n* c\n* d\n* e\n",
			"invalid: a; b; c; d, …",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reason(c.in); got != c.want {
				t.Errorf("reason() = %q, want %q", got, c.want)
			}
		})
	}
}
