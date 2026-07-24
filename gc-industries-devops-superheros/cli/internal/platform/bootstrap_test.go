package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// fakeRun records which modules were asked for and answers however the test
// says. No bash, no cluster: what is under test is the chain, not the modules.
type fakeRun struct {
	ran  []string
	fail map[string]error
	warn map[string]int
}

func (f *fakeRun) run(_ *render.LiveStep, _ string, script string) (moduleOutput, error) {
	f.ran = append(f.ran, script)
	if err, ok := f.fail[script]; ok {
		return moduleOutput{lines: 3}, err
	}
	return moduleOutput{lines: 12, warnings: f.warn[script]}, nil
}

func TestBootstrapRunsEveryModuleInOrder(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}

	if err := Bootstrap(BootstrapOptions{
		Root: root, SkipPreflight: true, run: f.run,
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if len(f.ran) != len(Chain) {
		t.Fatalf("ran %d modules, want %d: %v", len(f.ran), len(Chain), f.ran)
	}
	for i, m := range Chain {
		if f.ran[i] != m.Script {
			t.Errorf("step %d ran %s, want %s", i+1, f.ran[i], m.Script)
		}
	}

	got := buf.String()
	// The counter is the difference between a tool that feels finished and one
	// that feels stuck, so assert it is actually drawn.
	for _, want := range []string{"[1/6]", "[6/6]", "✓ Bootstrapping the platform — 6 steps in"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The closing box, and the fact that it names the cluster it just made.
	if !strings.Contains(got, "Platform ready") || !strings.Contains(got, ClusterName(root)) {
		t.Errorf("no closing summary:\n%s", got)
	}
}

// TestBootstrapStopsAtTheFirstFailure — the modules are ordered by dependency,
// so continuing past a failure means installing ArgoCD into a cluster that does
// not exist and reporting five errors about one problem.
func TestBootstrapStopsAtTheFirstFailure(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{fail: map[string]error{
		Chain[1].Script: errors.New("exit status 1"),
	}}

	err := Bootstrap(BootstrapOptions{Root: root, SkipPreflight: true, run: f.run})
	if err == nil {
		t.Fatal("a failed module did not fail the bootstrap")
	}
	if len(f.ran) != 2 {
		t.Errorf("ran %v — the chain did not stop at the failure", f.ran)
	}

	got := buf.String()
	if !strings.Contains(got, "✗ Installing Istio") {
		t.Errorf("the failing step is not marked:\n%s", got)
	}
	// One failure makes the run a failure, however many steps passed.
	if !strings.Contains(got, "1 of 6 steps done, 1 failed") {
		t.Errorf("the verdict does not report the failure honestly:\n%s", got)
	}
	if strings.Contains(got, "Platform ready") {
		t.Errorf("a failed bootstrap printed the success box:\n%s", got)
	}
}

// TestBootstrapDryRunTouchesNothing — the whole shape of a bootstrap, on a
// machine with no Docker, and six skips rather than six ✓s for work nobody did.
func TestBootstrapDryRunTouchesNothing(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}

	if err := Bootstrap(BootstrapOptions{
		Root: root, SkipPreflight: true, DryRun: true, run: f.run,
	}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(f.ran) != 0 {
		t.Fatalf("a dry run executed %v", f.ran)
	}

	got := buf.String()
	if strings.Contains(got, "✓ Creating the kind cluster") {
		t.Errorf("a dry run claimed a step succeeded:\n%s", got)
	}
	if !strings.Contains(got, "0 of 6 steps done, 6 skipped") {
		t.Errorf("the dry run verdict is not honest:\n%s", got)
	}
	for _, m := range Chain {
		if !strings.Contains(got, "bash "+m.Script) {
			t.Errorf("the dry run does not show the command for %q:\n%s", m.Step, got)
		}
	}
}

// TestBootstrapPreflightGatesTheRun — a missing tool must stop the bootstrap
// before it creates anything. Failing four steps in, with a cluster already on
// disk, is the outcome this gate exists to prevent.
func TestBootstrapPreflightGatesTheRun(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}

	err := Bootstrap(BootstrapOptions{Root: root, run: f.run, probe: missingTool("kind")})
	if err == nil {
		t.Fatal("bootstrap ran with a required tool missing")
	}
	if len(f.ran) != 0 {
		t.Errorf("modules ran despite a failed preflight: %v", f.ran)
	}
	if got := buf.String(); !strings.Contains(got, "✗ kind") {
		t.Errorf("the preflight did not name the missing tool:\n%s", got)
	}
}
