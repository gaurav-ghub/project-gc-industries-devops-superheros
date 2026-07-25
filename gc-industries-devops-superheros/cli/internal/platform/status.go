package platform

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// `endurance status` (no application named) — is the platform actually up?
//
// The counterpart to `endurance version`: that one says what this build
// installs, this one says what is running. It asks the cluster and reports what
// the cluster said. A component with no pods is `not installed` and not a
// failure — a platform where the AI module was never installed is a valid
// platform, and calling that broken teaches people to ignore the output.
//
// Like doctor, this is a lookup: a rule, an aligned list, a verdict, no box.

// A component is one platform capability and the pods that constitute it.
//
// Pods are found by label rather than by workload name. The names are helm
// release-dependent (`prometheus-prometheus-kube-prometheus-prometheus` and
// friends) and would break the moment anyone renamed a release; the labels are
// the chart's own contract.
type component struct {
	name     string
	ns       string
	selector string
}

var components = []component{
	{"istio control plane", "istio-system", "app=istiod"},
	{"istio ingress gateway", "istio-system", "app=istio-ingressgateway"},
	{"prometheus", "monitoring", "app.kubernetes.io/name=prometheus"},
	{"alertmanager", "monitoring", "app.kubernetes.io/name=alertmanager"},
	{"grafana", "monitoring", "app.kubernetes.io/name=grafana"},
	{"ai alert enrichment", "monitoring", "app=superhero-ai-alertmanager"},
	{"argocd", "argocd", "app.kubernetes.io/name=argocd-server"},
	{"kyverno", "kyverno", "app.kubernetes.io/part-of=kyverno"},
	// Installed by platform/access since Phase 10. Reported as `not installed`
	// on a platform that predates it, which is the honest answer and not a
	// failure — same rule as every other component here.
	{"kiali", "istio-system", "app.kubernetes.io/name=kiali"},
}

// A kubectlFunc runs kubectl and returns its combined output. Tests replace it.
type kubectlFunc func(args ...string) (string, error)

func realKubectl(args ...string) (string, error) {
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	return string(out), err
}

// StatusOptions configures a platform status report.
type StatusOptions struct {
	Root    string // platform repo root ("" = discover)
	kubectl kubectlFunc
}

// Status reports the platform's health.
//
// It exits non-zero when the cluster is unreachable or a component is degraded,
// so a script can gate on it, and zero when a component was simply never
// installed. That distinction is the whole judgement call in this command: a
// platform without the AI module is a platform someone configured that way; a
// platform whose ArgoCD pods are crash-looping is broken.
func Status(opts StatusOptions) error {
	render.Banner(version.Current)

	root, err := FindRoot(opts.Root)
	if err != nil {
		return err
	}
	kube := opts.kubectl
	if kube == nil {
		if _, err := exec.LookPath("kubectl"); err != nil {
			render.Section("Platform status")
			render.Error("kubectl is not on PATH — run `endurance doctor`")
			return fmt.Errorf("kubectl not found")
		}
		kube = realKubectl
	}

	render.Section("Platform status · " + ContextName(root))

	nodes, err := clusterNodes(kube, ContextName(root))
	if err != nil {
		render.Checks([]render.Check{{
			Name: "cluster", State: render.StateFailed,
			Note: "not reachable — run `endurance bootstrap`",
		}})
		render.Blank()
		render.Info("`endurance bootstrap` stands it up · `endurance doctor` checks the tools first")
		// The verdict is the returned error: main renders it, and printing it
		// here as well would say the same thing twice in two voices.
		return fmt.Errorf("the platform is not running — %s is not reachable", ContextName(root))
	}

	checks := []render.Check{{
		Name: "cluster", State: render.StateReady,
		Note: fmt.Sprintf("%s · %s", ContextName(root), plural(nodes, "node")+" Ready"),
	}}
	for _, c := range components {
		checks = append(checks, c.check(kube))
	}
	render.Checks(checks)
	render.Blank()
	return statusVerdict(checks)
}

// A Health is the same question Status answers, without printing it.
//
// It exists for `endurance init`, which has to decide whether to spend ten
// minutes running a bootstrap. Asking the user "is your platform already
// installed?" would be asking them a question the tool can answer itself, and
// re-running seven modules against a healthy cluster to be safe is ten minutes
// of a first-run experience spent on nothing.
type Health struct {
	Reachable bool
	Ready     int
	Total     int
}

// Complete reports whether every component this build knows about is up.
func (h Health) Complete() bool { return h.Reachable && h.Total > 0 && h.Ready == h.Total }

// CheckHealth asks the cluster about every component and says nothing.
func CheckHealth(root string) Health { return checkHealth(resolveStatusKubectl(nil), root) }

func checkHealth(kube kubectlFunc, root string) Health {
	h := Health{Total: len(components)}
	if kube == nil {
		return h
	}
	if _, err := clusterNodes(kube, ContextName(root)); err != nil {
		return h
	}
	h.Reachable = true
	for _, c := range components {
		if c.check(kube).State == render.StateReady {
			h.Ready++
		}
	}
	return h
}

// resolveStatusKubectl returns the kubectl to use, or nil when there is none —
// which is simply another way of not knowing, and is what CheckHealth reports.
func resolveStatusKubectl(k kubectlFunc) kubectlFunc {
	if k != nil {
		return k
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil
	}
	return realKubectl
}

// clusterNodes counts Ready nodes, and doubles as the reachability probe: if
// the API server does not answer, nothing below it is worth asking about.
func clusterNodes(kube kubectlFunc, context string) (int, error) {
	out, err := kube("get", "nodes", "--no-headers")
	if err != nil {
		return 0, fmt.Errorf("cluster %s not reachable: %s", context, firstLine(out))
	}
	ready := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == "Ready" {
			ready++
		}
	}
	if ready == 0 {
		return 0, fmt.Errorf("no Ready nodes")
	}
	return ready, nil
}

func (c component) check(kube kubectlFunc) render.Check {
	out, err := kube("get", "pods", "-n", c.ns, "-l", c.selector, "--no-headers")
	if err != nil {
		// A missing namespace is the ordinary "never installed" case, and
		// kubectl reports it as an error. It is not one.
		return render.Check{Name: c.name, State: render.StatePending, Note: "not installed"}
	}
	pods := ParsePodTable(out)
	if len(pods) == 0 {
		return render.Check{Name: c.name, State: render.StatePending, Note: "not installed"}
	}
	ready := 0
	for _, p := range pods {
		if p.Ready {
			ready++
		}
	}
	note := fmt.Sprintf("%d/%d pods ready · %s", ready, len(pods), c.ns)
	switch {
	case ready == len(pods):
		return render.Check{Name: c.name, State: render.StateReady, Note: note}
	case ready > 0:
		return render.Check{Name: c.name, State: render.StateWarn, Note: note}
	default:
		return render.Check{Name: c.name, State: render.StateFailed,
			Note: note + " · " + pods[0].Status}
	}
}

// A PodState is one row of `kubectl get pods --no-headers`.
//
// Exported because the application half of the CLI reads the same table for the
// success screen, and "what counts as ready" is a rule this platform has been
// deliberate about since Phase 8. Two implementations of it would be two
// answers, and the one that mattered would be whichever a screenshot came from.
type PodState struct {
	Name   string
	Status string
	Ready  bool
}

// ParsePodTable reads kubectl's table: NAME READY STATUS RESTARTS AGE.
//
// A pod counts as ready only when its READY column says every container is up
// *and* its status is Running. Both halves matter: a terminating pod can read
// 2/2, and a pod that is Running with 1/2 containers is not serving anything.
// This is the same rule the Phase 8 success screen was built around — only an
// observed-healthy thing earns a ✓.
func ParsePodTable(out string) []PodState {
	var pods []PodState
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		p := PodState{Name: f[0], Status: f[2]}
		p.Ready = p.Status == "Running" && allContainersReady(f[1])
		pods = append(pods, p)
	}
	return pods
}

// allContainersReady reads the "2/2" column.
func allContainersReady(col string) bool {
	got, want, found := strings.Cut(col, "/")
	if !found {
		return false
	}
	g, err1 := strconv.Atoi(got)
	w, err2 := strconv.Atoi(want)
	return err1 == nil && err2 == nil && w > 0 && g == w
}

func statusVerdict(checks []render.Check) error {
	var ready, degraded, missing int
	for _, c := range checks {
		switch c.State {
		case render.StateReady:
			ready++
		case render.StateWarn, render.StateFailed:
			degraded++
		default:
			missing++
		}
	}
	total := len(checks)
	switch {
	case degraded > 0:
		render.Info("`kubectl get pods -A` shows which pods · `endurance bootstrap` re-runs the modules")
		return fmt.Errorf("%d of %d components healthy, %d degraded", ready, total, degraded)
	case missing > 0:
		render.Warn(fmt.Sprintf("%d of %d components healthy, %d not installed",
			ready, total, missing))
		render.Info("`endurance bootstrap` installs what is missing")
	default:
		render.Success(fmt.Sprintf("the platform is healthy — %d of %d components", ready, total))
	}
	return nil
}
