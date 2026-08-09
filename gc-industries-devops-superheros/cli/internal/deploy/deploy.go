// Package deploy is `endurance deploy` — the missing verb (14.2).
//
// # The fault this exists for
//
// `endurance onboard` writes `apps/<app>/application.yaml` and nothing applies
// it. `endurance init` always deployed, but the code that did it was a private
// function of the init command, reachable from nowhere else. So app 1 in the
// first outside run went through `init` and came up; apps 2 and 3 went through
// `onboard` and needed `kubectl apply -f apps/<app>/application.yaml` typed by
// hand, twice, before ArgoCD had ever heard of them. cli/main.go had `init`,
// `bootstrap`, `doctor`, … `onboard`, `catalog`, … and no `deploy`.
//
// This package is that verb, extracted so both doors — `endurance deploy <app>`
// on its own, and `init`'s guided run — call the same function rather than one
// of them reimplementing it privately. See internal/onboard for the third
// caller and the judgment call about what a failed deploy should do there.
//
// # What it does, and does not do
//
// Two steps: `kubectl apply -f apps/<app>/application.yaml`, then poll ArgoCD
// until the Application is Synced and Healthy (or the timeout, or --no-wait).
// The applied file is the *registration* — the Application CR that tells ArgoCD
// which repo to watch and where — never a workload. ArgoCD is still the only
// thing that deploys; this package is the one line that tells it an application
// exists.
package deploy

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/render"
)

// waitPoll is how often the wait asks ArgoCD where it has got to, and
// DefaultTimeout is how long it keeps asking — six minutes, comfortably longer
// than a first sync of a single-service application and comfortably shorter
// than the patience of somebody watching a terminal.
const (
	waitPoll       = 5 * time.Second
	DefaultTimeout = 6 * time.Minute
)

// Options configures one deploy.
type Options struct {
	Root string // platform repo root
	App  string // the registered application to deploy

	NoWait  bool // do not wait for ArgoCD; return on whatever is running now
	Timeout time.Duration

	// Kubectl runs kubectl and returns its combined output. nil resolves the
	// real one, or nil again when kubectl is not on PATH — which is a
	// legitimate answer, not a failure: it means "nothing was applied", stated
	// as such.
	Kubectl func(args ...string) (string, error)

	// Now and Sleep are the wall clock, for a test that wants to watch several
	// poll attempts without waiting five seconds between each. nil is the real
	// clock; the CLI never sets either.
	Now   func() time.Time
	Sleep func(time.Duration)
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultTimeout
	}
	return o.Timeout
}

func (o Options) pause(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}

func resolveKubectl(k func(args ...string) (string, error)) func(args ...string) (string, error) {
	if k != nil {
		return k
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil
	}
	return func(args ...string) (string, error) {
		out, err := exec.Command("kubectl", args...).CombinedOutput()
		return string(out), err
	}
}

// Run applies the application's Application CR and waits for ArgoCD to
// converge. Renders its own framed progress, in the same voice every other
// gate in this CLI uses.
//
// The bool result is whether ArgoCD actually reported the application synced
// and healthy, so a caller can close its own run with a claim or without one —
// see internal/success, which is the only thing entitled to draw a ✓.
func Run(opts Options) (bool, error) {
	opts.Timeout = opts.timeout()
	kube := resolveKubectl(opts.Kubectl)
	appFile := filepath.Join(gitops.AppDir(opts.Root, opts.App), "application.yaml")

	p := render.NewProgress("Deploying "+opts.App,
		"Registering "+opts.App+" with ArgoCD", "Waiting for ArgoCD")

	step := p.Start("Registering " + opts.App + " with ArgoCD")
	if kube == nil {
		step.Skip("kubectl is not on PATH")
		p.Finish()
		render.Warn("nothing was applied — `kubectl apply -f " + relPath(opts.Root, appFile) + "` registers it")
		return false, nil
	}
	if out, err := kube("apply", "-f", appFile); err != nil {
		// kubectl puts the diagnosis on the lines *after* the headline — an
		// invalid Application says "is invalid:" and then names each field. A
		// first-line-only report throws away the only part worth reading.
		for _, l := range outputLines(out) {
			step.Detail(l)
		}
		err = step.Fail(fmt.Errorf("kubectl apply: %s", reason(out)))
		p.Finish()
		return false, err
	}
	step.Rename(opts.App + " registered with ArgoCD")
	step.Done()

	wait := p.Start("Waiting for ArgoCD")
	if opts.NoWait {
		wait.Skip("--no-wait")
		p.Finish()
		return false, nil
	}
	state := waitForSync(wait, opts, kube)
	switch {
	case state.synced:
		wait.Rename("ArgoCD synced " + opts.App)
		wait.Done()
	default:
		// Not a failure. ArgoCD may still be working, or it may be waiting for
		// a push, and either way the caller reports what is actually running
		// rather than this step guessing.
		wait.Warn(state.why)
	}
	p.Finish()
	if !state.synced && state.needsPush {
		render.Blank()
		render.Warn("ArgoCD only ever sees pushed state, and this commit is not pushed")
		render.Detail(state.pushDetail)
		render.Detail("Endurance never pushes — that is yours to run:")
		render.Detail("  git push")
		render.Detail("then `endurance status " + opts.App + "` · ArgoCD picks it up on its own")
	}
	return state.synced, nil
}

// syncState is what the wait concluded.
type syncState struct {
	synced     bool
	needsPush  bool
	why        string
	pushDetail string
}

// waitForSync polls ArgoCD until the Application is Synced and Healthy.
//
// It reports what it is waiting for, and re-reports only when the answer
// changes: a step that prints the same line every five seconds is a step
// nobody reads. The push check happens once, up front the first time a poll
// comes back unsynced, because "your commit is not on the remote" is the
// difference between waiting usefully and waiting forever.
func waitForSync(step *render.LiveStep, opts Options, kube func(args ...string) (string, error)) syncState {
	st := syncState{}
	askedAboutPush := false
	checkPush := func() {
		if askedAboutPush {
			return
		}
		askedAboutPush = true
		if ahead, detail := pushState(opts.Root); ahead {
			st.needsPush, st.pushDetail = true, detail
			step.Detail(detail)
			step.Detail("Endurance never pushes · run `git push` and this picks it up")
		}
	}

	// The loop is bounded twice over, by a deadline and by a count of
	// attempts. Belt and braces on purpose: the wall clock is injectable so a
	// test can pin it, and a pinned clock never reaches a deadline — so a wait
	// with only the first bound is a wait that can run forever on a frozen
	// clock.
	deadline := opts.now().Add(opts.Timeout)
	attempts := int(opts.Timeout / waitPoll)
	if attempts < 1 {
		attempts = 1
	}
	last := ""
	for attempt := 1; ; attempt++ {
		sync, health, err := argoStatus(kube, opts.App)
		switch {
		case err != nil:
			describe(step, &last, "ArgoCD has not reported on "+opts.App+" yet")
			checkPush()
		case sync == "Synced" && health == "Healthy":
			st.synced = true
			return st
		default:
			describe(step, &last, "sync "+orUnknown(sync)+" · health "+orUnknown(health))
			checkPush()
		}
		if attempt >= attempts || !opts.now().Before(deadline) {
			st.why = "still " + orUnknown(last) + " after " + opts.Timeout.String()
			if st.needsPush {
				st.why = "waiting for a push"
			}
			return st
		}
		opts.pause(waitPoll)
	}
}

func describe(step *render.LiveStep, last *string, msg string) {
	if msg == *last {
		return
	}
	*last = msg
	step.Detail(msg)
}

// argoApp is the slice of ArgoCD's Application status this command reads.
type argoApp struct {
	Status struct {
		Sync struct {
			Status string `json:"status"`
		} `json:"sync"`
		Health struct {
			Status string `json:"status"`
		} `json:"health"`
	} `json:"status"`
}

func argoStatus(kube func(args ...string) (string, error), app string) (sync, health string, err error) {
	out, err := kube("-n", "argocd", "get", "application", app, "-o", "json")
	if err != nil {
		return "", "", fmt.Errorf("%s", firstLine(out))
	}
	var a argoApp
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		return "", "", err
	}
	return a.Status.Sync.Status, a.Status.Health.Status, nil
}

// pushState reports whether this repo has commits ArgoCD cannot see.
//
// Three answers, and they are different: there is no upstream at all (nothing
// to push to — a fresh clone with no remote, or a branch never pushed), there
// are commits ahead of it, or everything is published. Only the first two make
// the wait futile, and each needs a different sentence.
func pushState(root string) (needsPush bool, detail string) {
	branch := strings.TrimSpace(git(root, "rev-parse", "--abbrev-ref", "HEAD"))
	head := strings.TrimSpace(git(root, "rev-parse", "--short", "HEAD"))
	upstream := strings.TrimSpace(git(root, "rev-parse", "--abbrev-ref", "@{upstream}"))
	if upstream == "" {
		return true, "branch " + orUnknown(branch) + " has no upstream — nothing has been pushed anywhere"
	}
	ahead := strings.TrimSpace(git(root, "rev-list", "--count", "@{upstream}..HEAD"))
	if ahead == "" || ahead == "0" {
		return false, ""
	}
	return true, "commit " + head + " is " + ahead + " ahead of " + upstream
}

func git(root string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// maxReasons bounds how much of a command's complaint is folded into a
// one-line error. Every line is still shown as a detail above it.
const maxReasons = 4

// outputLines returns the non-empty, trimmed lines of a command's output.
func outputLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// reason folds a multi-line complaint into one sentence: the headline, then the
// specifics it introduced. "The Application "x" is invalid:" on its own names
// no fault anybody can act on; with "spec.sources[0].repoURL: Required value"
// after it, it does.
func reason(out string) string {
	lines := outputLines(out)
	if len(lines) == 0 {
		return "no output"
	}
	head := lines[0]
	rest := lines[1:]
	if len(rest) == 0 {
		return head
	}
	head = strings.TrimSuffix(head, ":")
	more := ""
	if len(rest) > maxReasons {
		rest, more = rest[:maxReasons], ", …"
	}
	for i, r := range rest {
		rest[i] = strings.TrimPrefix(r, "* ")
	}
	return head + ": " + strings.Join(rest, "; ") + more
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// relPath is a command to type from the directory the user is already standing
// in, not 90 columns of an absolute Windows path they do not need.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}
