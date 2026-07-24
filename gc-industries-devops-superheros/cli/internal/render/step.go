package render

import (
	"strings"
	"sync"
	"time"
)

// spinnerFrames animate a step that is still running. Braille dots because they
// occupy one cell in every font anyone runs a terminal in.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

// A LiveStep is one unit of work that announces itself before it happens and
// reports how it went afterwards.
//
// This is the primitive Phase 9 needs. `endurance bootstrap` is a list of slow
// things (create a cluster, install Istio, install ArgoCD); the user must see
// which one is running now, not a frozen terminal, and must end up with a
// transcript that says what happened. On a terminal the ▸ line spins and is
// rewritten in place with ✓ and the elapsed time. Piped to a file, the same
// step prints its start line and then its result line.
//
// A step is finished exactly once — by Done, Fail or Skip. Calling twice is a
// no-op, so `defer s.Fail(err)`-style cleanup cannot corrupt the transcript.
type LiveStep struct {
	r      *Renderer
	msg    string
	prefix string // progress counter, e.g. "[2/6] "
	marker string // drawn while running in place of ▸ — a Progress draws a bar
	start  time.Time

	mu   sync.Mutex
	done bool
	stop chan struct{}
	wg   sync.WaitGroup

	// onFinish lets a Progress learn how its step ended.
	onFinish func(State)
}

// StartStep announces work and returns the handle that finishes it.
//
// Prefer a [Progress] for anything a user watches for more than a moment: its
// steps carry a bar rather than a spinner, which is the one in-progress shape
// Endurance draws.
func (r *Renderer) StartStep(msg string) *LiveStep { return r.startStep(msg, "", "") }

func (r *Renderer) startStep(msg, prefix, marker string) *LiveStep {
	s := &LiveStep{r: r, msg: msg, prefix: prefix, marker: marker, start: r.now()}
	r.mu.Lock()
	r.parkLocked(s.runningLine(""))
	spin := r.spin
	r.mu.Unlock()
	if spin {
		s.startSpinner()
	}
	return s
}

// runningLine is what a step looks like while it is still running.
//
// With a marker (a Progress step) it is bar + label + elapsed: one shape for
// "in progress", the same green as the run's own bar, and a clock that ticks so
// a three-minute helm install does not look like a hung terminal. Without one
// it is the ▸ glyph, animated by the spinner.
func (s *LiveStep) runningLine(frame string) string {
	head := s.marker
	if head == "" {
		if frame == "" {
			frame = IconStep
		}
		return s.prefix + s.r.head.Render(frame) + " " + s.msg
	}
	line := s.prefix + head + " " + s.msg
	if e := s.r.now().Sub(s.start); e >= time.Second {
		line += "  " + s.r.faint.Render(formatDuration(e))
	}
	return line
}

func (s *LiveStep) startSpinner() {
	s.stop = make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(spinnerInterval)
		defer t.Stop()
		for i := 1; ; i++ {
			select {
			case <-s.stop:
				return
			case <-t.C:
				frame := spinnerFrames[i%len(spinnerFrames)]
				s.r.mu.Lock()
				// Only redraw while the line is still ours. If anything
				// else has written (a detail line, a streamed subprocess),
				// the step's line is scrolled away and must be left alone.
				s.r.reparkLocked(s.runningLine(frame))
				s.r.mu.Unlock()
			}
		}
	}()
}

// Rename replaces the step's label, so a step can announce itself in the
// present tense and resolve in the past: "Installing Istio" while it runs,
// "Istio installed" once it has. Call it before Done; a failure keeps the
// running label, because "Istio installed — failed" is nonsense.
func (s *LiveStep) Rename(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done && msg != "" {
		s.msg = msg
	}
}

func (s *LiveStep) stopSpinner() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	s.wg.Wait() // never hold r.mu here: the spinner takes it
	s.stop = nil
}

// Elapsed is how long the step has been running.
func (s *LiveStep) Elapsed() time.Duration { return s.r.now().Sub(s.start) }

// Detail prints an indented, muted line under the step. Use it for the one or
// two facts worth keeping ("kind v0.23.0", "3 nodes Ready"); use Stream for a
// subprocess's whole output.
func (s *LiveStep) Detail(msg string) { s.r.Detail(msg) }

// Stream returns the step's subprocess detail channel: an io.WriteCloser that
// renders every line a child process writes as indented, muted output. Wire it
// to exec.Cmd's Stdout and Stderr and the command's noise arrives in
// Endurance's voice instead of its own.
func (s *LiveStep) Stream() *Stream { return s.r.Stream() }

// StreamWith is Stream with a line filter: the same detail channel, at the same
// indent, but the caller gets a look at every line first.
//
// It is the hook Phase 8 left for promoting a subprocess's own severities into
// Endurance's — the bash modules prefix the lines that matter with `warning:`
// or `error:`, and `endurance bootstrap` uses this to finish a module that
// warned as ⚠ rather than ✓. The filter reports whether the line should be
// shown, so it can also drop the noise a tool insists on repeating.
//
// The indent is not a parameter on purpose: there is one detail channel and it
// has one indent.
func (s *LiveStep) StreamWith(filter func(line string) bool) *Stream {
	return s.r.StreamWith(detailIndent, filter)
}

// Done finishes the step successfully.
func (s *LiveStep) Done() { s.finish(StateReady, "") }

// DoneWith finishes the step successfully and records one closing fact —
// "cluster kind-superheros, 1 node" — beside the ✓.
func (s *LiveStep) DoneWith(note string) { s.finish(StateReady, note) }

// Skip finishes the step as not-run, with the reason. A skipped step is not a
// success and must not be dressed as one: it gets · and the reason, so a
// bootstrap that found Istio already installed says exactly that.
func (s *LiveStep) Skip(reason string) { s.finish(StatePending, reason) }

// Warn finishes the step as done-with-a-caveat.
func (s *LiveStep) Warn(note string) { s.finish(StateWarn, note) }

// Fail finishes the step as failed and returns err unchanged, so a caller can
// write `return step.Fail(err)` and neither lose the error nor forget the line.
func (s *LiveStep) Fail(err error) error {
	note := ""
	if err != nil {
		note = err.Error()
	}
	s.finish(StateFailed, note)
	return err
}

func (s *LiveStep) finish(state State, note string) {
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return
	}
	s.done = true
	s.mu.Unlock()

	s.stopSpinner()
	elapsed := formatDuration(s.r.now().Sub(s.start))

	r := s.r
	r.mu.Lock()
	line := s.prefix + r.Icon(state) + " " + s.msg
	switch {
	case state == StateFailed:
		line += "  " + r.bad.Render("failed after "+elapsed)
		if note != "" {
			line += r.muted.Render(": " + note)
		}
	case state == StatePending:
		line += "  " + r.muted.Render("skipped")
		if note != "" {
			line += r.muted.Render(" · " + note)
		}
	default:
		if note != "" {
			line += "  " + r.muted.Render(note)
		}
		line += "  " + r.faint.Render(elapsed)
	}
	r.resolveLocked(line)
	r.mu.Unlock()

	if s.onFinish != nil {
		s.onFinish(state)
	}
}

// Steps runs a static list of already-finished results — the shape `doctor`
// wants in Phase 9, where nothing is live and every check already has an
// answer.
type Check struct {
	Name  string
	State State
	Note  string
}

// Checks prints a list of finished checks, names aligned.
func (r *Renderer) Checks(checks []Check) {
	w := 0
	for _, c := range checks {
		if len(c.Name) > w {
			w = len(c.Name)
		}
	}
	for _, c := range checks {
		line := r.Icon(c.State) + " " + pad(c.Name, w)
		if c.Note != "" {
			line += "  " + r.muted.Render(c.Note)
		}
		r.emit(strings.TrimRight(line, " "))
	}
}
