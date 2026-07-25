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
		Root: root, SkipPreflight: true, run: f.run, probeURL: allAnswer,
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
	for _, want := range []string{"[1/7]", "[7/7]", "✓ Bootstrapping the platform — 7 steps in"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The closing box, and the fact that it names the cluster it just made.
	if !strings.Contains(got, "Platform ready") || !strings.Contains(got, ClusterName(root)) {
		t.Errorf("no closing summary:\n%s", got)
	}
}

// allAnswer is the network, scripted: every address replies 200.
//
// Bootstrap probes its own URLs before claiming they work, and a unit test has
// no cluster — without this the suite would spend its retry budget failing to
// reach localhost and then assert on the wrong branch.
func allAnswer(string) (int, error) { return 200, nil }

// noneAnswer is the other half: nothing is listening.
func noneAnswer(string) (int, error) { return 0, errors.New("connection refused") }

// TestAccessDetailsComeOnceAtTheEnd.
//
// The modules used to each print their own URLs and admin passwords the moment
// they finished — Grafana's three minutes before ArgoCD existed. Now Endurance
// prints them once, after the run, and **prints no credential at all**: the
// command that fetches one is the same information with a shelf life, and it
// does not end up in a screenshot.
//
// Since Phase 10 it also prints *addresses* rather than port-forward commands,
// which is asserted here because the whole point of the access layer is that
// there is nothing left to keep running in another terminal.
func TestAccessDetailsComeOnceAtTheEnd(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	if err := Bootstrap(BootstrapOptions{
		Root: root, SkipPreflight: true, run: (&fakeRun{}).run, probeURL: allAnswer,
	}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{"Access", "ArgoCD", "Grafana", "Prometheus", "Alertmanager"} {
		if !strings.Contains(got, want) {
			t.Errorf("the access block does not mention %s:\n%s", want, got)
		}
	}
	// Once, not once per module.
	if n := strings.Count(got, "── Access"); n != 1 {
		t.Errorf("the access block appears %d times, want 1", n)
	}
	// It comes after the run, not in the middle of it.
	if strings.Index(got, "── Access") < strings.Index(got, "Bootstrapping the platform —") {
		t.Errorf("the access block was printed before the run finished:\n%s", got)
	}
	// It hands out commands, never secrets.
	for _, forbidden := range []string{"Password:", "password:", "Username:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("bootstrap printed something shaped like a credential (%q):\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "argocd-initial-admin-secret") {
		t.Errorf("it does not say how to fetch the ArgoCD password:\n%s", got)
	}

	// Real addresses on the platform's one host, not four port-forwards.
	base := BaseURL(root)
	for _, want := range []string{base + "/argocd", base + "/kiali", base + "/grafana"} {
		if !strings.Contains(got, want) {
			t.Errorf("the access block does not print %s:\n%s", want, got)
		}
	}
	// The verdict line says the words "no port-forward", so match on the command
	// rather than the noun: what must not survive is an instruction to run one.
	if strings.Contains(got, "kubectl port-forward") {
		t.Errorf("bootstrap still offers a port-forward — that is what the access layer replaced:\n%s", got)
	}
	// Probed, not asserted: a bootstrap that ends with an unverified list of
	// URLs is a bootstrap that finds out it was wrong in front of an audience.
	if !strings.Contains(got, "every address answered") {
		t.Errorf("the access block did not report checking the addresses:\n%s", got)
	}
}

// TestBootstrapIsHonestWhenNothingAnswers — the same run, with nothing
// listening. The chain succeeded, so the modules keep their ✓; what must not
// happen is the closing block claiming the platform is reachable.
func TestBootstrapIsHonestWhenNothingAnswers(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	if err := Bootstrap(BootstrapOptions{
		Root: root, SkipPreflight: true, run: (&fakeRun{}).run, probeURL: noneAnswer,
	}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if strings.Contains(got, "every address answered") {
		t.Errorf("bootstrap claimed the addresses answered when nothing was listening:\n%s", got)
	}
	if !strings.Contains(got, "none of the addresses answered") {
		t.Errorf("bootstrap did not say the addresses are unreachable:\n%s", got)
	}
	// The way out has to be in the output, and for this failure it is a cluster
	// recreate — kind fixes its port mappings at creation time.
	if !strings.Contains(got, "endurance destroy") {
		t.Errorf("the unreachable hint does not name the fix:\n%s", got)
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

	err := Bootstrap(BootstrapOptions{Root: root, SkipPreflight: true, run: f.run, probeURL: allAnswer})
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
	if !strings.Contains(got, "1 of 7 steps done, 1 failed") {
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
		Root: root, SkipPreflight: true, DryRun: true, run: f.run, probeURL: allAnswer,
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
	if !strings.Contains(got, "0 of 7 steps done, 7 skipped") {
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

	err := Bootstrap(BootstrapOptions{Root: root, run: f.run, probe: missingTool("kind"), probeURL: allAnswer})
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
