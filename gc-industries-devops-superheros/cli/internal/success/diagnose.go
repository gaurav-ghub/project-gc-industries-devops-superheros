package success

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gc-ghub/endurance/internal/render"
)

// Diagnosis (14.5) — why a pod broke, not just that it did.
//
// # The fault this exists for
//
// `status` reported `CrashLoopBackOff` and offered a `kubectl logs` line to
// type; ArgoCD said `Degraded`; the browser said `no healthy upstream`. Both
// real causes in the first outside run — `portfolio-backend` missing a
// required `GITHUB_USERNAME`, `portfolio-frontend` refused a privileged port
// under the platform's own security defaults — were one `kubectl logs` away,
// and the platform explained neither. A container that exits in under a
// second with a one-line reason on stdout is the easiest failure in
// Kubernetes to diagnose, and the tool was not diagnosing it.
//
// # What this reads, and what it deliberately does not
//
// Three things, each cheap and each already true of the cluster before this
// file existed: the waiting reason Kubernetes itself recorded
// (`CrashLoopBackOff`, `ImagePullBackOff`, …, often with a message), the last
// termination (exit code and reason, when the container has actually run and
// stopped), and the tail of the container's own log — first the current
// attempt, falling back to `--previous` for a container that has already
// restarted and left nothing in its current log.
//
// It does not call the AI enricher. That model is installed for Prometheus
// alerts and reachable from the cluster, not from wherever `endurance status`
// happens to run, and giving this command a second, differently-authenticated
// path to the same OpenAI account is a decision worth making on its own,
// deliberately — not a rider on the three facts below, which are already
// enough to explain both failures the first outside run actually hit.

// diagnosePod asks the cluster why one pod is not ready, and returns the
// lines success.Screen attaches under it. Called only for a pod already
// classified StateFailed — a pod that is merely still starting has nothing to
// explain, and asking would print a stale reason.
func diagnosePod(kube KubectlFunc, ns, pod string) []string {
	var out []string
	if reason, msg, exit := podState(kube, ns, pod); reason != "" || exit != "" {
		if reason != "" {
			line := "waiting: " + reason
			if msg != "" {
				line += " — " + msg
			}
			out = append(out, line)
		}
		if exit != "" {
			out = append(out, "last exit: "+exit)
		}
	}
	out = append(out, podLogTail(kube, ns, pod, 3)...)
	return out
}

// podStatus is the slice of `kubectl get pod -o json` this command reads —
// just the container statuses, which is where Kubernetes itself records why a
// container is not running.
type podStatus struct {
	Status struct {
		ContainerStatuses []struct {
			RestartCount int `json:"restartCount"`
			State        struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
			} `json:"state"`
			LastState struct {
				Terminated *struct {
					Reason   string `json:"reason"`
					Message  string `json:"message"`
					ExitCode int    `json:"exitCode"`
				} `json:"terminated"`
			} `json:"lastState"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// podState reads the waiting reason and the last termination straight from
// the object Kubernetes already maintains — no guessing from the one-word
// status column `kubectl get pods` prints.
func podState(kube KubectlFunc, ns, pod string) (waitingReason, waitingMessage, lastExit string) {
	out, err := kube("get", "pod", pod, "-n", ns, "-o", "json")
	if err != nil {
		return "", "", ""
	}
	var p podStatus
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		return "", "", ""
	}
	for _, c := range p.Status.ContainerStatuses {
		if c.State.Waiting != nil && c.State.Waiting.Reason != "" {
			waitingReason, waitingMessage = c.State.Waiting.Reason, c.State.Waiting.Message
		}
		if c.LastState.Terminated != nil && c.RestartCount > 0 {
			t := c.LastState.Terminated
			lastExit = fmt.Sprintf("%s (exit %d)", orReason(t.Reason), t.ExitCode)
			if t.Message != "" {
				lastExit += " — " + t.Message
			}
		}
		if waitingReason != "" || lastExit != "" {
			break // one failing container is the common case and the one worth naming
		}
	}
	return waitingReason, waitingMessage, lastExit
}

func orReason(s string) string {
	if s == "" {
		return "terminated"
	}
	return s
}

// podLogTail is the last n lines of the pod's own log — the fastest route to
// "a missing env var" or "permission denied", both one `kubectl logs` away
// and neither surfaced anywhere else on this screen.
//
// It tries the current container first, and falls back to --previous: a
// container that has already restarted (CrashLoopBackOff, most of the time
// this command is asked about anything) usually has nothing in its *current*
// log yet, and the useful lines are in the attempt that just failed.
func podLogTail(kube KubectlFunc, ns, pod string, n int) []string {
	tail := fmt.Sprintf("--tail=%d", n)
	if out, err := kube("logs", pod, "-n", ns, tail); err == nil {
		if lines := lastLines(out, n); len(lines) > 0 {
			return prefixed(lines)
		}
	}
	if out, err := kube("logs", pod, "-n", ns, tail, "--previous"); err == nil {
		if lines := lastLines(out, n); len(lines) > 0 {
			return prefixed(lines)
		}
	}
	return nil
}

func lastLines(s string, n int) []string {
	var lines []string
	for l := range strings.SplitSeq(strings.ReplaceAll(strings.TrimRight(s, "\n"), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func prefixed(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "log: " + l
	}
	return out
}

// attachDiagnoses fills in Detail for every pod the screen already marked
// StateFailed. Separated from Build so Build stays pure and testable without
// a kubectl fake — every existing test that constructs a Result by hand keeps
// working unchanged, and this is the one place that reaches for the cluster a
// second time, after the first read already decided which pods need it.
func attachDiagnoses(kube KubectlFunc, ns string, pods []render.Pod) {
	if kube == nil {
		return
	}
	for i := range pods {
		if pods[i].State != render.StateFailed {
			continue
		}
		pods[i].Detail = diagnosePod(kube, ns, pods[i].Name)
	}
}
