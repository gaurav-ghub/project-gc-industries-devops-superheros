// Package observe implements `endurance logs` and `endurance metrics`: the two
// questions a developer asks about a running application that the success
// screen does not answer.
//
// # These are wrappers, and they stay wrappers
//
// kubectl already reads logs and already reads the metrics API, and it does
// both better than a reimplementation would. What it does not do is know that
// "superheros" is an application with five services in a namespace of its own,
// selected by app.kubernetes.io/part-of — so what Endurance adds here is the
// selector, and nothing else. The bytes kubectl produces are printed exactly as
// kubectl produced them.
//
// That is a deliberate boundary and it is easy to erode: the next step would be
// parsing log lines to colour them, and the step after that is a log viewer
// nobody asked for that is wrong about multiline stack traces. The commands
// print a short header saying what they asked and hand over.
//
// # Metrics is honest about a thing the platform does not install
//
// `kubectl top` needs the metrics.k8s.io API, which comes from metrics-server —
// and this platform does not install it. kube-prometheus-stack ships
// kube-state-metrics and node-exporter, which feed Prometheus, not the metrics
// API. So `endurance metrics` asks, and when the API is not there it says so
// and points at Grafana, which is where this platform's metrics actually are.
// Printing an error from kubectl and leaving the user to work out that the
// platform never had the feature would be the unhelpful half of honest.
package observe

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// PartOfLabel is how every workload the platform renders is tagged with the
// application it belongs to. charts/app writes it; these commands select on it.
const PartOfLabel = "app.kubernetes.io/part-of"

// NameLabel is the per-service label, for narrowing to one service.
const NameLabel = "app.kubernetes.io/name"

// A Runner runs kubectl with its output going wherever the caller says. Tests
// replace it; the CLI never does.
//
// It takes the writers rather than returning a string because `logs -f` never
// returns: a follow streams until the user stops it, and buffering that into a
// string first would show nothing until it ended.
type Runner func(stdout, stderr *os.File, args ...string) error

func realRunner(stdout, stderr *os.File, args ...string) error {
	cmd := exec.Command("kubectl", args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// A CaptureFunc runs kubectl and returns its combined output.
type CaptureFunc func(args ...string) (string, error)

func realCapture(args ...string) (string, error) {
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	return string(out), err
}

// LogOptions configures `endurance logs`.
type LogOptions struct {
	Root    string
	App     string
	Service string // "" = every service in the application
	Follow  bool
	Tail    int
	Since   string

	Run Runner
}

// Logs prints an application's logs.
func Logs(opts LogOptions) error {
	app, err := load(opts.Root, opts.App)
	if err != nil {
		return err
	}
	selector, err := selectorFor(app, opts.Service)
	if err != nil {
		return err
	}

	args := []string{"logs", "-n", app.Namespace, "-l", selector,
		"--all-containers=true", "--prefix=true"}
	if opts.Tail > 0 {
		args = append(args, fmt.Sprintf("--tail=%d", opts.Tail))
	}
	if opts.Since != "" {
		args = append(args, "--since="+opts.Since)
	}
	if opts.Follow {
		args = append(args, "-f")
	}

	render.Section("Logs · " + app.Name + describeService(opts.Service))
	render.Info("kubectl " + strings.Join(args, " "))
	if opts.Follow {
		render.Info("following · Ctrl-C to stop")
	}
	render.Blank()

	run := opts.Run
	if run == nil {
		run = realRunner
	}
	// Straight through: kubectl's own format, unmodified. The renderer's detail
	// channel is for a subprocess whose output is Endurance's to frame; a log
	// line belongs to the application and is not.
	// kubectl wrote its own diagnosis to stderr on the way past, so the error
	// here names where to look rather than repeating an exit code as though it
	// were a reason.
	if err := run(os.Stdout, os.Stderr, args...); err != nil {
		return fmt.Errorf("kubectl logs did not succeed — its message is above · "+
			"`endurance status %s` says whether those pods exist yet", app.Name)
	}
	return nil
}

// MetricOptions configures `endurance metrics`.
type MetricOptions struct {
	Root    string
	App     string
	Service string

	Capture CaptureFunc
}

// Metrics prints an application's per-pod CPU and memory.
func Metrics(opts MetricOptions) error {
	app, err := load(opts.Root, opts.App)
	if err != nil {
		return err
	}
	selector, err := selectorFor(app, opts.Service)
	if err != nil {
		return err
	}

	capture := opts.Capture
	if capture == nil {
		capture = realCapture
	}
	args := []string{"top", "pods", "-n", app.Namespace, "-l", selector}

	render.Section("Metrics · " + app.Name + describeService(opts.Service))
	render.Info("kubectl " + strings.Join(args, " "))
	render.Blank()

	out, runErr := capture(args...)
	if runErr == nil {
		render.Print(strings.TrimRight(out, "\n"))
		render.Blank()
		render.Info("point-in-time · Grafana keeps the history: " +
			render.Value(platform.BaseURL(root(opts.Root))+"/grafana"))
		return nil
	}

	// The failure that is not a fault. This platform installs
	// kube-prometheus-stack, which feeds Prometheus — not metrics-server, which
	// is what serves metrics.k8s.io. `kubectl top` has nothing to talk to, and
	// saying only "error from server" would leave a user debugging a component
	// the platform never had.
	if metricsAPIMissing(out) {
		render.Warn("the metrics API is not available on this cluster")
		render.Detail("`kubectl top` reads metrics.k8s.io, which comes from metrics-server")
		render.Detail("Endurance installs kube-prometheus-stack instead — the same numbers, kept over time")
		render.Blank()
		render.Info("Grafana has them: " + render.Value(platform.BaseURL(root(opts.Root))+"/grafana"))
		render.Detail("Prometheus: " + platform.BaseURL(root(opts.Root)) + "/prometheus")
		render.Detail("`endurance status " + app.Name + "` reports whether the pods are up, which is the other half of the question")
		return nil
	}
	// Anything else is a real failure and is reported as one, with kubectl's own
	// first line as the reason — "exit status 1" is not a diagnosis.
	render.Print(strings.TrimRight(out, "\n"))
	render.Blank()
	if clusterUnreachable(out) {
		render.Info("`endurance status` says whether the cluster is up at all")
		return fmt.Errorf("the cluster did not answer — %s", firstLine(out))
	}
	render.Info("`endurance status " + app.Name + "` reports the pods from the same cluster")
	return fmt.Errorf("kubectl top: %s", firstLine(out))
}

// clusterUnreachable recognises the API server not answering, as opposed to
// answering with a refusal. The two want different next steps: one is
// `endurance bootstrap`, the other is a look at what was asked for.
func clusterUnreachable(out string) bool {
	l := strings.ToLower(out)
	for _, s := range []string{
		"unable to connect to the server",
		"connection refused",
		"was refused",
		"no such host",
		"the connection to the server",
		"couldn't get current server api group list",
	} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// metricsAPIMissing recognises the cluster saying it has no metrics API, in the
// several shapes kubectl reports it. Anything else is a real error and is
// returned as one — a command that swallowed every failure as "no metrics
// server" would hide an unreachable cluster.
func metricsAPIMissing(out string) bool {
	l := strings.ToLower(out)
	for _, s := range []string{
		"metrics api not available",
		"metrics.k8s.io",
		"could not find the requested resource",
		"the server could not find the requested resource",
	} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// load reads a registered application, with the error a developer can act on.
func load(root, name string) (spec.App, error) {
	app, err := gitops.Load(root, name)
	if err != nil {
		return app, fmt.Errorf("no registered app %q (%v) — `endurance catalog list` shows what there is", name, err)
	}
	app.ApplyDefaults()
	return app, nil
}

// selectorFor narrows to one service, or to the whole application.
//
// A service the application does not declare is refused rather than passed to
// kubectl, which would answer "no pods" — indistinguishable from a service that
// exists and is not running, which is a completely different problem.
func selectorFor(app spec.App, service string) (string, error) {
	if service == "" {
		return PartOfLabel + "=" + app.Name, nil
	}
	if app.FindService(service) < 0 {
		return "", fmt.Errorf("app %q has no service %q — services are: %s",
			app.Name, service, strings.Join(app.ServiceNames(), ", "))
	}
	// Both labels, not just the service name: two applications may perfectly
	// well each have a service called `frontend`, and they are in different
	// namespaces today but that is the namespace's guarantee and not this
	// selector's.
	return PartOfLabel + "=" + app.Name + "," + NameLabel + "=" + service, nil
}

func describeService(service string) string {
	if service == "" {
		return ""
	}
	return " · " + service
}

// root resolves the platform repo for the dashboard addresses, falling back to
// what the caller passed. `logs` and `metrics` are developer commands and must
// work from a checkout the platform tree is not under; the URL block's own
// rules about guessing apply and BaseURL says what it read.
func root(start string) string {
	if r, err := platform.FindRoot(start); err == nil {
		return r
	}
	return start
}
