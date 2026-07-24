package render

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// bootstrapSteps is the Phase 9 run this primitive exists for.
var bootstrapSteps = []string{
	"Creating the kind cluster",
	"Installing Istio",
	"Installing monitoring",
	"Installing AI alert enrichment",
	"Installing ArgoCD",
	"Installing Kyverno policies",
}

func TestProgressGolden(t *testing.T) {
	r, buf, c := fixture(t)
	p := r.NewProgress("Bootstrapping the platform", bootstrapSteps...)
	for _, name := range bootstrapSteps {
		s := p.Start(name)
		c.advance(15 * time.Second)
		s.Done()
	}
	if !p.Finish() {
		t.Error("a run in which every step succeeded reported failure")
	}
	golden(t, "progress", buf.String())
}

// TestProgressSkipsAndFailsGolden — the counter must tell the truth about a run
// that did not go to plan: steps jumped over are printed as skipped, and one
// failure makes the run a failure however many steps passed.
func TestProgressSkipsAndFailsGolden(t *testing.T) {
	r, buf, c := fixture(t)
	p := r.NewProgress("Bootstrapping the platform", bootstrapSteps...)

	s := p.Start("Creating the kind cluster")
	c.advance(12 * time.Second)
	s.DoneWith("kind-superheros")

	// Istio was already there — jump straight to monitoring.
	s = p.Start("Installing monitoring")
	c.advance(30 * time.Second)
	s.Done()

	s = p.Start("Installing ArgoCD")
	c.advance(3 * time.Second)
	_ = s.Fail(errors.New("exit status 1"))

	if p.Finish() {
		t.Error("a run with a failed step reported success")
	}
	golden(t, "progress-skips-and-fails", buf.String())
}

// TestProgressAppendsUnplannedStep — a plan that turns out to be wrong grows
// rather than silently mislabelling the work.
func TestProgressAppendsUnplannedStep(t *testing.T) {
	r, buf, _ := fixture(t)
	p := r.NewProgress("Run", "one", "two")
	p.Start("one").Done()
	p.Start("three").Done()
	if p.Total() != 3 {
		t.Errorf("Total() = %d, want 3 after an unplanned step", p.Total())
	}
	if !strings.Contains(buf.String(), "[3/3] ✓ three") {
		t.Errorf("unplanned step was not counted:\n%s", buf.String())
	}
	p.Finish()
}

// TestProgressClosesAnAbandonedStep — a step whose caller forgot to finish it
// still gets a result line when the next one starts, so the transcript never
// has a dangling ▸.
func TestProgressClosesAnAbandonedStep(t *testing.T) {
	r, buf, _ := fixture(t)
	p := r.NewProgress("Run", "one", "two")
	p.Start("one")
	p.Start("two").Done()
	p.Finish()
	if !strings.Contains(buf.String(), "[1/2] ✓ one") {
		t.Errorf("abandoned step was never resolved:\n%s", buf.String())
	}
}

func TestBar(t *testing.T) {
	r, _, _ := fixture(t)
	cases := []struct {
		done, total, width int
		want               string
	}{
		{0, 6, 6, "░░░░░░"},
		{3, 6, 6, "███░░░"},
		{6, 6, 6, "██████"},
		{9, 6, 6, "██████"}, // never overflows
		{-1, 6, 6, "░░░░░░"},
		{1, 0, 6, "░░░░░░"}, // no plan, no progress
		{1, 2, 0, ""},
	}
	for _, c := range cases {
		if got := r.Bar(c.done, c.total, c.width); got != c.want {
			t.Errorf("Bar(%d,%d,%d) = %q, want %q", c.done, c.total, c.width, got, c.want)
		}
	}
}
