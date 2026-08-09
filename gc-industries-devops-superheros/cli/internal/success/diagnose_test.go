package success

import (
	"slices"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// 14.5 — say why it broke, not just that it did. Both real causes in the
// first outside run — a missing required env var, a privileged port refused
// under the platform's non-root defaults — are reproduced below almost
// verbatim from issues.md §5b.

// portfolioBackendKubectl answers the way the actual failing pod did:
// CrashLoopBackOff, one restart, and a one-line reason on stdout from
// config.Load's log.Fatalf.
func portfolioBackendKubectl(t *testing.T) KubectlFunc {
	t.Helper()
	return func(args ...string) (string, error) {
		switch {
		case args[0] == "get" && args[1] == "pod":
			return `{"status":{"containerStatuses":[{"restartCount":1,
				"state":{"waiting":{"reason":"CrashLoopBackOff","message":"back-off 40s restarting failed container"}},
				"lastState":{"terminated":{"reason":"Error","exitCode":1}}}]}}`, nil
		case args[0] == "logs" && !contains(args, "--previous"):
			// The current attempt has nothing yet — it just restarted.
			return "", nil
		case args[0] == "logs":
			return "2026-08-08T10:00:00Z FATAL missing required env GITHUB_USERNAME\n", nil
		}
		return "", nil
	}
}

func contains(args []string, s string) bool {
	return slices.Contains(args, s)
}

func TestDiagnosePodSurfacesTheWaitingReasonAndTheLog(t *testing.T) {
	lines := diagnosePod(portfolioBackendKubectl(t), "portfolio", "backend-6d9f7c8b4d-r2x9p")
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "CrashLoopBackOff") {
		t.Errorf("the waiting reason is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "back-off 40s") {
		t.Errorf("the waiting message is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "Error (exit 1)") {
		t.Errorf("the last termination is missing:\n%s", joined)
	}
	if !strings.Contains(joined, "GITHUB_USERNAME") {
		t.Errorf("the log line naming the missing env var is missing — this is the fact that "+
			"was one `kubectl logs` away and the platform explained nothing:\n%s", joined)
	}
}

// The current container's log is empty right after a restart; the useful
// line is in the attempt that just failed, which --previous reads.
func TestDiagnosePodFallsBackToThePreviousLog(t *testing.T) {
	calledPrevious := false
	kube := func(args ...string) (string, error) {
		switch {
		case args[0] == "get":
			return `{"status":{"containerStatuses":[]}}`, nil
		case contains(args, "--previous"):
			calledPrevious = true
			return "the actual failure\n", nil
		default:
			return "", nil // current log: nothing yet
		}
	}
	lines := diagnosePod(kube, "ns", "pod")
	if !calledPrevious {
		t.Fatal("an empty current log did not fall back to --previous")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "the actual failure") {
		t.Errorf("the previous log's content did not surface: %v", lines)
	}
}

// A pod that never ran a container has no lastState.terminated — nothing to
// report there, and reporting one anyway would be inventing a fact.
func TestNoLastTerminationIsNotFabricated(t *testing.T) {
	kube := func(args ...string) (string, error) {
		if args[0] == "get" {
			return `{"status":{"containerStatuses":[{"restartCount":0,
				"state":{"waiting":{"reason":"ImagePullBackOff"}}}]}}`, nil
		}
		return "", nil
	}
	lines := diagnosePod(kube, "ns", "pod")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "ImagePullBackOff") {
		t.Errorf("the waiting reason is missing: %v", lines)
	}
	if strings.Contains(joined, "last exit") {
		t.Errorf("a last exit was reported for a container that never ran: %v", lines)
	}
}

// A cluster that will not answer produces no detail and no panic — the same
// "report, do not fabricate" rule the rest of this screen already keeps.
func TestAnUnreachableClusterProducesNoDetail(t *testing.T) {
	kube := func(args ...string) (string, error) { return "", errUnreachable }
	if lines := diagnosePod(kube, "ns", "pod"); lines != nil {
		t.Errorf("an unreachable cluster produced detail: %v", lines)
	}
}

var errUnreachable = &fakeErr{"connection refused"}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

// attachDiagnoses only reaches for a pod already marked StateFailed — asking
// about a pod that is merely still starting would print a stale or invented
// reason for something that is not actually broken.
func TestAttachDiagnosesOnlyAsksAboutFailedPods(t *testing.T) {
	asked := map[string]bool{}
	kube := func(args ...string) (string, error) {
		if args[0] == "get" {
			asked[args[2]] = true
		}
		return `{"status":{"containerStatuses":[]}}`, nil
	}
	pods := []render.Pod{
		{Name: "ready-pod", State: render.StateReady},
		{Name: "starting-pod", State: render.StatePending},
		{Name: "broken-pod", State: render.StateFailed},
	}
	attachDiagnoses(kube, "ns", pods)
	if asked["ready-pod"] || asked["starting-pod"] {
		t.Errorf("a pod that is not failed was diagnosed: %v", asked)
	}
	if !asked["broken-pod"] {
		t.Error("the failed pod was not diagnosed")
	}
}

func TestAttachDiagnosesWithNoKubectlDoesNothing(t *testing.T) {
	pods := []render.Pod{{Name: "broken-pod", State: render.StateFailed}}
	attachDiagnoses(nil, "ns", pods)
	if pods[0].Detail != nil {
		t.Error("attachDiagnoses with no kubectl invented a detail")
	}
}

// End to end: the success screen a developer actually reads carries the
// reason, not just a `kubectl logs` line to go type themselves.
func TestScreenSurfacesWhyAFailingPodBroke(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	backendKube := portfolioBackendKubectl(t)
	err := Screen(Options{Root: root, App: "superheros", Kubectl: func(args ...string) (string, error) {
		if args[0] == "get" && args[1] == "pods" {
			return "backend-6d9f7c8b4d-r2x9p   0/1   CrashLoopBackOff   1   2m\n" +
				"frontend-abc                  1/1   Running             0   2m\n", nil
		}
		return backendKube(args...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "CrashLoopBackOff") {
		t.Errorf("the pod's own status line is missing:\n%s", got)
	}
	if !strings.Contains(got, "GITHUB_USERNAME") {
		t.Errorf("the screen a developer reads does not say why the pod broke — "+
			"it is back to offering a kubectl logs line to type by hand:\n%s", got)
	}
	if strings.Contains(got, "frontend-abc") == false {
		t.Errorf("the healthy pod dropped off the screen:\n%s", got)
	}
}
