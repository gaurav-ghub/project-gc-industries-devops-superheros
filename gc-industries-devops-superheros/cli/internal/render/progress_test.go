package render

import (
	"bytes"
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

// TestProgressStepsCarryABarNotASpinner.
//
// One shape for "in progress": the thin bar on each step is the same bar, in
// the same green, as the broad one that closes the run. A spinner frame was
// worse than nothing here — every step of a bootstrap streams output, so the
// step's line scrolls away within a second and the transcript keeps whichever
// braille character the animation happened to be on. A bar frozen at "three of
// six done" still says something true a day later.
func TestProgressStepsCarryABarNotASpinner(t *testing.T) {
	r, buf, _ := fixture(t)
	p := r.NewProgress("Bootstrapping the platform", bootstrapSteps...)
	p.Start(bootstrapSteps[0]).Done()
	p.Start(bootstrapSteps[1])

	got := buf.String()
	if !strings.Contains(got, "[2/6] "+strings.Repeat(IconBarFull, 1)) {
		t.Errorf("the running step does not carry a bar of the run's progress:\n%s", got)
	}
	for _, frame := range spinnerFrames {
		if strings.Contains(got, frame) {
			t.Errorf("a spinner frame reached a progress step (%q):\n%s", frame, got)
		}
	}
	if strings.Contains(got, "[2/6] "+IconStep) {
		t.Errorf("the running step still draws ▸ instead of a bar:\n%s", got)
	}
}

// TestProgressBarsAreThinOnStepsAndBroadAtTheEnd — two widths, one meaning.
func TestProgressBarsAreThinOnStepsAndBroadAtTheEnd(t *testing.T) {
	r, buf, _ := fixture(t)
	p := r.NewProgress("Bootstrapping the platform", bootstrapSteps...)
	for _, name := range bootstrapSteps {
		p.Start(name).Done()
	}
	p.Finish()

	got := buf.String()
	if !strings.Contains(got, strings.Repeat(IconBarFull, barWidth)) {
		t.Errorf("no broad bar closes the run:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat(IconBarFull, stepBarWidth+1)+IconBarVoid) {
		t.Errorf("a step's bar is wider than a step's bar should be:\n%s", got)
	}
}

// TestDangerRunDrawsARedBar — a teardown filling a green bar as it removes
// things has the colour exactly backwards. Colour is the only difference: a
// deletion that worked is still a ✓.
func TestDangerRunDrawsARedBar(t *testing.T) {
	var buf bytes.Buffer
	c := newClock()
	r := New(&buf, WithColor(true), WithTTY(false), WithClock(c.now))

	p := r.NewProgress("Destroying the platform", "Deleting the kind cluster").Danger()
	p.Start("Deleting the kind cluster").Done()
	p.Finish()

	got := buf.String()
	red := r.bad.Render(IconBarFull)
	green := r.ok.Render(IconBarFull)
	if !strings.Contains(got, red[:strings.Index(red, IconBarFull)]) {
		t.Errorf("a destructive run did not draw its bar in the error colour:\n%q", got)
	}
	if strings.Contains(got, green[:strings.Index(green, IconBarFull)]+IconBarFull) {
		t.Errorf("a destructive run drew a green bar:\n%q", got)
	}
	if !strings.Contains(got, r.ok.Render(IconOK)) {
		t.Errorf("a deletion that worked lost its green ✓:\n%q", got)
	}
}

// TestOneStepIsNotOneSteps — "1 steps in 5.9s" is the kind of thing a reader
// notices and a tool should not say.
func TestOneStepIsNotOneSteps(t *testing.T) {
	r, buf, c := fixture(t)
	p := r.NewProgress("Destroying the platform", "Deleting the kind cluster")
	s := p.Start("Deleting the kind cluster")
	c.advance(6 * time.Second)
	s.Done()
	p.Finish()

	got := buf.String()
	if strings.Contains(got, "1 steps") {
		t.Errorf("the verdict says \"1 steps\":\n%s", got)
	}
	if !strings.Contains(got, "1 step in 6.0s") {
		t.Errorf("the verdict does not read as a sentence:\n%s", got)
	}
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
