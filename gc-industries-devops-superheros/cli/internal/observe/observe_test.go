package observe

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// repoRoot is the real platform repo, because these commands read a registered
// application out of apps/ and superheros is the one this repo has.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "superheros", "app.yaml")); err != nil {
		t.Skipf("superheros is not registered under %s", root)
	}
	return root
}

// recorder captures the kubectl argv without running anything.
type recorder struct {
	args []string
	out  string
	err  error
}

func (r *recorder) run(stdout, stderr *os.File, args ...string) error {
	r.args = args
	return r.err
}

func (r *recorder) capture(args ...string) (string, error) {
	r.args = args
	return r.out, r.err
}

// TestLogsAddsTheSelectorAndNothingElse.
//
// The whole value of this command over typing kubectl is that it knows
// "superheros" is an application with five services in a namespace of its own.
// The whole risk is that it grows into a log viewer. This pins the argv.
func TestLogsAddsTheSelectorAndNothingElse(t *testing.T) {
	root := repoRoot(t)
	capture(t)
	r := &recorder{}

	if err := Logs(LogOptions{Root: root, App: "superheros", Tail: 200, Run: r.run}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"logs", "-n", "superheros",
		"-l", PartOfLabel + "=superheros",
		"--all-containers=true", "--prefix=true", "--tail=200",
	}
	if strings.Join(r.args, " ") != strings.Join(want, " ") {
		t.Errorf("argv\n got %v\nwant %v", r.args, want)
	}
}

// TestLogsNarrowsToOneServiceByBothLabels.
//
// Two applications may each have a service called `frontend`. They are in
// different namespaces today, but that is the namespace's guarantee and not this
// selector's, and a selector that relies on someone else's invariant is one
// refactor from being wrong.
func TestLogsNarrowsToOneServiceByBothLabels(t *testing.T) {
	root := repoRoot(t)
	capture(t)
	r := &recorder{}

	if err := Logs(LogOptions{
		Root: root, App: "superheros", Service: "catalog", Follow: true, Run: r.run,
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.args, " ")
	if !strings.Contains(joined, PartOfLabel+"=superheros,"+NameLabel+"=catalog") {
		t.Errorf("the selector does not carry both labels: %v", r.args)
	}
	if !strings.Contains(joined, " -f") {
		t.Errorf("--follow did not reach kubectl: %v", r.args)
	}
}

// TestAnUnknownServiceIsRefusedRatherThanPassedOn.
//
// kubectl would answer "no pods found", which is indistinguishable from a
// service that exists and is not running — a completely different problem with a
// completely different next step.
func TestAnUnknownServiceIsRefusedRatherThanPassedOn(t *testing.T) {
	root := repoRoot(t)
	capture(t)
	r := &recorder{}

	err := Logs(LogOptions{Root: root, App: "superheros", Service: "fronted", Run: r.run})
	if err == nil {
		t.Fatal("a service the application does not declare was passed to kubectl")
	}
	if r.args != nil {
		t.Errorf("kubectl ran anyway: %v", r.args)
	}
	if !strings.Contains(err.Error(), "frontend") {
		t.Errorf("the error does not list what could have been typed: %v", err)
	}
}

// TestAnUnknownApplicationNamesTheCommandThatLists.
func TestAnUnknownApplicationNamesTheCommandThatLists(t *testing.T) {
	root := repoRoot(t)
	capture(t)

	err := Logs(LogOptions{Root: root, App: "nope", Run: (&recorder{}).run})
	if err == nil || !strings.Contains(err.Error(), "catalog list") {
		t.Errorf("the error does not point anywhere useful: %v", err)
	}
}

// TestMetricsPrintsWhatKubectlPrinted — a wrapper, not a formatter.
func TestMetricsPrintsWhatKubectlPrinted(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	const table = "NAME                    CPU(cores)   MEMORY(bytes)\n" +
		"frontend-6d9f7c8b4d-r2   1m           12Mi"
	r := &recorder{out: table + "\n"}

	if err := Metrics(MetricOptions{Root: root, App: "superheros", Capture: r.capture}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, table) {
		t.Errorf("kubectl's own table did not survive:\n%s", got)
	}
	if strings.Join(r.args, " ") != "top pods -n superheros -l "+PartOfLabel+"=superheros" {
		t.Errorf("argv: %v", r.args)
	}
}

// TestMetricsSaysWhereTheMetricsActuallyAre.
//
// This platform installs kube-prometheus-stack, which feeds Prometheus — not
// metrics-server, which is what serves metrics.k8s.io. So `kubectl top` has
// nothing to talk to on a stock Endurance cluster. Printing kubectl's error and
// stopping would leave a user debugging a component the platform never had, so
// the command says which one is missing and points at Grafana, where the numbers
// genuinely are. It is not an error: the question was answerable, elsewhere.
func TestMetricsSaysWhereTheMetricsActuallyAre(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	r := &recorder{
		out: "error: Metrics API not available",
		err: errors.New("exit status 1"),
	}

	if err := Metrics(MetricOptions{Root: root, App: "superheros", Capture: r.capture}); err != nil {
		t.Fatalf("a missing metrics API was reported as a failure: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"metrics-server", "kube-prometheus-stack", "/grafana"} {
		if !strings.Contains(got, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, got)
		}
	}
}

// TestAnUnreachableClusterIsStillAFailure — the other half of the rule above:
// only the one specific "this platform does not install that" case is softened.
// A cluster that is not there is an error, and it says which.
func TestAnUnreachableClusterIsStillAFailure(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	r := &recorder{
		out: "Unable to connect to the server: dial tcp [::1]:8080: connection refused",
		err: errors.New("exit status 1"),
	}

	err := Metrics(MetricOptions{Root: root, App: "superheros", Capture: r.capture})
	if err == nil {
		t.Fatal("an unreachable cluster was reported as success")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the error does not say what happened: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "endurance status") {
		t.Errorf("no next step offered:\n%s", got)
	}
}

// TestNeitherCommandEverWritesToTheCluster.
//
// `logs` and `metrics` read. Both argv lists start with a verb that cannot
// change anything, and this fails if either ever grows one that can.
func TestNeitherCommandEverWritesToTheCluster(t *testing.T) {
	root := repoRoot(t)
	capture(t)

	readOnly := map[string]bool{"logs": true, "top": true, "get": true, "describe": true}

	rl := &recorder{}
	if err := Logs(LogOptions{Root: root, App: "superheros", Run: rl.run}); err != nil {
		t.Fatal(err)
	}
	rm := &recorder{out: "ok\n"}
	if err := Metrics(MetricOptions{Root: root, App: "superheros", Capture: rm.capture}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*recorder{rl, rm} {
		if len(r.args) == 0 || !readOnly[r.args[0]] {
			t.Errorf("kubectl was invoked with a verb that is not read-only: %v", r.args)
		}
	}
}
