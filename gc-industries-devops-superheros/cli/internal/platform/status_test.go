package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// fakeKubectl answers `kubectl get ...` from a table the test writes: the key
// is the namespace and selector, the value is what kubectl would print.
func fakeKubectl(nodes string, pods map[string]string) kubectlFunc {
	return func(args ...string) (string, error) {
		if len(args) >= 2 && args[1] == "nodes" {
			if nodes == "" {
				return "The connection to the server localhost:8080 was refused", errors.New("exit status 1")
			}
			return nodes, nil
		}
		var ns, sel string
		for i, a := range args {
			if a == "-n" && i+1 < len(args) {
				ns = args[i+1]
			}
			if a == "-l" && i+1 < len(args) {
				sel = args[i+1]
			}
		}
		out, ok := pods[ns+"|"+sel]
		if !ok {
			// What kubectl says about a namespace that was never created.
			return `Error from server (NotFound): namespaces "` + ns + `" not found`,
				errors.New("exit status 1")
		}
		return out, nil
	}
}

const twoReadyNodes = `superheros-control-plane   Ready    control-plane   40m   v1.31.0
superheros-worker          Ready    <none>          39m   v1.31.0`

func allHealthy() map[string]string {
	out := map[string]string{}
	for _, c := range components {
		pod := strings.ReplaceAll(c.name, " ", "-")
		out[c.ns+"|"+c.selector] = pod + "-6d9f7c8b4d-r2x9p   1/1     Running   0     10m"
	}
	return out
}

// TestParsePodsCountsOnlyWhatIsActuallyServing.
//
// The Phase 8 rule, applied to real kubectl output: only an observed-healthy
// pod earns a ✓. A pod that is Running with 1 of 2 containers up is not serving
// anything, and a Terminating pod can still read 2/2.
func TestParsePodsCountsOnlyWhatIsActuallyServing(t *testing.T) {
	pods := ParsePodTable(`istiod-1   1/1   Running            0   10m
istiod-2   1/2   Running            3   2m
istiod-3   2/2   Terminating        0   9m
istiod-4   0/1   ContainerCreating  0   5s
istiod-5   0/1   CrashLoopBackOff   7   4m`)

	if len(pods) != 5 {
		t.Fatalf("parsed %d pods, want 5", len(pods))
	}
	want := []bool{true, false, false, false, false}
	for i, p := range pods {
		if p.Ready != want[i] {
			t.Errorf("%s (%s): ready = %v, want %v", p.Name, p.Status, p.Ready, want[i])
		}
	}
}

func TestStatusReportsAHealthyPlatform(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	err := Status(StatusOptions{Root: root, kubectl: fakeKubectl(twoReadyNodes, allHealthy())})
	if err != nil {
		t.Fatalf("a healthy platform reported an error: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, "2 nodes Ready") {
		t.Errorf("the node count is missing:\n%s", got)
	}
	for _, c := range components {
		if !strings.Contains(got, c.name) {
			t.Errorf("component %s is not reported:\n%s", c.name, got)
		}
	}
	if !strings.Contains(got, "the platform is healthy") {
		t.Errorf("no verdict:\n%s", got)
	}
	if !strings.Contains(got, ContextName(root)) {
		t.Errorf("status does not say which cluster it asked:\n%s", got)
	}
}

// TestStatusFailsWhenTheClusterIsGone — the report must not read as "nothing
// installed" when the truth is "nothing answered", and it has to exit non-zero
// so a script can gate on it.
func TestStatusFailsWhenTheClusterIsGone(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	err := Status(StatusOptions{Root: root, kubectl: fakeKubectl("", nil)})
	if err == nil {
		t.Fatal("an unreachable cluster was reported as success")
	}
	got := buf.String()
	if !strings.Contains(got, "✗ cluster") || !strings.Contains(got, "endurance bootstrap") {
		t.Errorf("the report does not say what happened or what to do:\n%s", got)
	}
	// Nothing below the cluster is worth asking about, so nothing is claimed.
	if strings.Contains(got, "argocd") {
		t.Errorf("components were reported against a cluster that did not answer:\n%s", got)
	}
}

// TestStatusSeparatesNotInstalledFromBroken is the judgement call this command
// turns on: a platform without the AI module is one somebody configured that
// way (·, exit 0); a platform whose pods are crash-looping is broken (✗, exit
// non-zero). Collapsing the two teaches people to ignore the output.
func TestStatusSeparatesNotInstalledFromBroken(t *testing.T) {
	root := repoRoot(t)

	t.Run("not installed", func(t *testing.T) {
		buf := capture(t)
		pods := allHealthy()
		delete(pods, "monitoring|app=superhero-ai-alertmanager")

		if err := Status(StatusOptions{Root: root, kubectl: fakeKubectl(twoReadyNodes, pods)}); err != nil {
			t.Errorf("a never-installed module failed the status: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "· ai alert enrichment") {
			t.Errorf("a missing module is not reported as pending:\n%s", got)
		}
		if !strings.Contains(got, "1 not installed") {
			t.Errorf("the verdict does not count it:\n%s", got)
		}
	})

	t.Run("degraded", func(t *testing.T) {
		buf := capture(t)
		pods := allHealthy()
		pods["argocd|app.kubernetes.io/name=argocd-server"] =
			"argocd-server-7c5f6d4b88-k4t2m   0/1   CrashLoopBackOff   7   4m"

		err := Status(StatusOptions{Root: root, kubectl: fakeKubectl(twoReadyNodes, pods)})
		if err == nil {
			t.Fatal("a crash-looping component did not fail the status")
		}
		got := buf.String()
		if !strings.Contains(got, "✗ argocd") {
			t.Errorf("the broken component is not marked:\n%s", got)
		}
		if !strings.Contains(got, "CrashLoopBackOff") {
			t.Errorf("the report does not say why:\n%s", got)
		}
	})

	t.Run("partially ready", func(t *testing.T) {
		buf := capture(t)
		pods := allHealthy()
		pods["kyverno|app.kubernetes.io/part-of=kyverno"] =
			"kyverno-admission-1   1/1   Running   0   10m\nkyverno-admission-2   0/1   ContainerCreating   0   5s"

		if err := Status(StatusOptions{Root: root, kubectl: fakeKubectl(twoReadyNodes, pods)}); err == nil {
			t.Error("a half-ready component was reported as fine")
		}
		if got := buf.String(); !strings.Contains(got, "1/2 pods ready") {
			t.Errorf("the pod counts are not reported:\n%s", got)
		}
	})
}

func TestAllContainersReady(t *testing.T) {
	cases := map[string]bool{
		"1/1": true, "2/2": true, "0/1": false, "1/2": false,
		"": false, "1": false, "0/0": false, "x/y": false,
	}
	for col, want := range cases {
		if got := allContainersReady(col); got != want {
			t.Errorf("allContainersReady(%q) = %v, want %v", col, got, want)
		}
	}
}

func TestStatusVerdictStates(t *testing.T) {
	capture(t)
	ready := render.Check{State: render.StateReady}

	if err := statusVerdict([]render.Check{ready, ready}); err != nil {
		t.Errorf("an all-healthy platform returned %v", err)
	}
	if err := statusVerdict([]render.Check{ready, {State: render.StatePending}}); err != nil {
		t.Errorf("a not-installed component returned %v", err)
	}
	if err := statusVerdict([]render.Check{ready, {State: render.StateWarn}}); err == nil {
		t.Error("a degraded component did not return an error")
	}
}
