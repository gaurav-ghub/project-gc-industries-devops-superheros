package render

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestLiveStepGolden — the primitive Phase 9's bootstrap is built out of.
// Off a terminal, a step prints its start line and then its result line, so a
// piped log still records both that work began and how it ended.
func TestLiveStepGolden(t *testing.T) {
	r, buf, c := fixture(t)

	s := r.StartStep("Creating the kind cluster")
	c.advance(12300 * time.Millisecond)
	s.DoneWith("kind-endurance · 1 node")

	s = r.StartStep("Installing Istio")
	s.Detail("istioctl 1.23.0")
	c.advance(900 * time.Millisecond)
	s.Done()

	s = r.StartStep("Installing Kiali")
	s.Skip("already installed")

	s = r.StartStep("Installing monitoring")
	c.advance(2 * time.Second)
	s.Warn("Grafana has no persistent volume")

	s = r.StartStep("Installing ArgoCD")
	c.advance(4500 * time.Millisecond)
	_ = s.Fail(errors.New("exit status 1"))

	golden(t, "live-step", buf.String())
}

// TestStepFailReturnsErr — `return step.Fail(err)` must not swallow the error.
func TestStepFailReturnsErr(t *testing.T) {
	r, _, _ := fixture(t)
	want := errors.New("boom")
	if got := r.StartStep("x").Fail(want); !errors.Is(got, want) {
		t.Errorf("Fail returned %v, want %v", got, want)
	}
}

// TestStepFinishesOnce — a step resolved twice must not print two result lines,
// so `defer s.Fail(err)` alongside an explicit s.Done() stays safe.
func TestStepFinishesOnce(t *testing.T) {
	r, buf, _ := fixture(t)
	s := r.StartStep("work")
	s.Done()
	s.Fail(errors.New("late"))
	s.Skip("later still")
	if n := strings.Count(buf.String(), "\n"); n != 2 {
		t.Errorf("expected a start line and one result line, got %d lines:\n%s", n, buf.String())
	}
	if strings.Contains(buf.String(), "late") {
		t.Errorf("a finished step accepted a second result:\n%s", buf.String())
	}
}

// TestStepOnTTYRewritesInPlace — on a terminal the ▸ line is erased and
// replaced by its ✓, rather than leaving two lines behind.
func TestStepOnTTYRewritesInPlace(t *testing.T) {
	var buf bytes.Buffer
	c := newClock()
	r := New(&buf, WithColor(false), WithTTY(true), WithClock(c.now))
	r.spin = false // the spinner is a goroutine; this test is about the rewrite

	s := r.StartStep("Creating the kind cluster")
	c.advance(time.Second)
	s.Done()

	got := buf.String()
	if !strings.Contains(got, "\r\x1b[2K") {
		t.Errorf("expected the line to be erased and rewritten, got %q", got)
	}
	if n := strings.Count(got, "\n"); n != 1 {
		t.Errorf("expected one finished line on a TTY, got %d:\n%q", n, got)
	}
	if !strings.HasSuffix(got, "✓ Creating the kind cluster  1.0s\n") {
		t.Errorf("unexpected final line: %q", got)
	}
}

// TestStepOnTTYDoesNotRewriteAfterOtherOutput — once a subprocess has written
// under a step, its line has scrolled away; resolving it must append rather
// than erase whatever is now on the cursor's line.
func TestStepOnTTYDoesNotRewriteAfterOtherOutput(t *testing.T) {
	var buf bytes.Buffer
	c := newClock()
	r := New(&buf, WithColor(false), WithTTY(true), WithClock(c.now))
	r.spin = false

	s := r.StartStep("Installing Istio")
	s.Detail("istioctl 1.23.0")
	c.advance(time.Second)
	s.Done()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected start, detail and result lines, got %d:\n%q", len(lines), buf.String())
	}
	if strings.Contains(lines[1], "\r\x1b[2K") || strings.Contains(lines[2], "\r\x1b[2K") {
		t.Errorf("erased a line that was no longer the step's:\n%q", buf.String())
	}
}

// TestSpinnerStopsAndLeavesOneLine — the spinner is the only goroutine in the
// package. It must animate, then stop, and leave exactly the same transcript.
func TestSpinnerStopsAndLeavesOneLine(t *testing.T) {
	var buf bytes.Buffer
	c := newClock()
	r := New(&buf, WithColor(false), WithTTY(true), WithClock(c.now))

	s := r.StartStep("slow thing")
	time.Sleep(3 * spinnerInterval)
	c.advance(300 * time.Millisecond)
	s.Done()

	got := buf.String()
	if !strings.Contains(got, "⠙") && !strings.Contains(got, "⠹") {
		t.Errorf("the spinner never drew a frame: %q", got)
	}
	if n := strings.Count(got, "\n"); n != 1 {
		t.Errorf("expected one finished line, got %d:\n%q", n, got)
	}
	if !strings.HasSuffix(got, "✓ slow thing  300ms\n") {
		t.Errorf("unexpected final line: %q", got)
	}
	// Nothing may be written after the step is finished.
	before := buf.Len()
	time.Sleep(3 * spinnerInterval)
	if buf.Len() != before {
		t.Errorf("the spinner kept writing after Done: %q", buf.String()[before:])
	}
}

func TestChecksGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.Section("Preflight")
	r.Checks([]Check{
		{Name: "docker", State: StateReady, Note: "27.1.1 · daemon running"},
		{Name: "kind", State: StateReady, Note: "v0.23.0"},
		{Name: "kubectl", State: StateReady, Note: "v1.31.0"},
		{Name: "helm", State: StateWarn, Note: "v3.12.0 — 3.14+ recommended"},
		{Name: "git", State: StateFailed, Note: "not found — https://git-scm.com/downloads"},
		{Name: "cluster", State: StatePending, Note: "no kind cluster yet"},
	})
	golden(t, "checks", buf.String())
}
