package render

import (
	"fmt"
	"strings"
	"time"
)

// A Progress renders a run whose steps are known in advance.
//
// This is the second primitive Phase 9 needs. `endurance bootstrap` is not an
// open-ended stream of work — it is six named things in a fixed order, and the
// difference between a tool that feels finished and one that feels stuck is
// whether the user can see "4 of 6" while the fifth one runs.
//
// A Progress prints a counter in front of each step's line and a bar plus a
// one-line verdict at the end. It counts what actually happened: steps that
// were skipped are not successes, and one failure makes the whole run a
// failure, however many steps passed.
type Progress struct {
	r       *Renderer
	title   string
	names   []string
	i       int // index of the next step expected
	start   time.Time
	states  []State
	current *LiveStep
}

// NewProgress announces a run of known steps and returns its handle.
func (r *Renderer) NewProgress(title string, steps ...string) *Progress {
	p := &Progress{r: r, title: title, names: append([]string(nil), steps...), start: r.now()}
	p.states = make([]State, len(p.names))
	r.Section(title)
	return p
}

// Total is the number of steps currently planned.
func (p *Progress) Total() int { return len(p.names) }

// Start begins the named step and returns it.
//
// The name is looked up in the remaining plan rather than assumed to be next.
// A run that jumps ahead has skipped the steps in between — they are printed as
// skipped rather than silently dropped — and a name that is not in the plan at
// all is appended, growing the total. Both are honest about a plan that turned
// out to be wrong; neither loses a line of the transcript.
func (p *Progress) Start(name string) *LiveStep {
	if p.current != nil {
		p.current.Done() // a step left unfinished still gets a result line
	}
	if j := p.indexOf(name); j >= 0 {
		for ; p.i < j; p.i++ {
			p.states[p.i] = StatePending
			p.r.emit(p.counter(p.i+1) + p.r.Icon(StatePending) + " " + p.names[p.i] +
				"  " + p.r.muted.Render("skipped"))
		}
	} else {
		p.names = append(p.names, name)
		p.states = append(p.states, StatePending)
		p.i = len(p.names) - 1
	}

	idx := p.i
	s := p.r.startStep(name, p.counter(idx+1))
	s.onFinish = func(st State) {
		p.states[idx] = st
		if p.i == idx {
			p.i++
		}
		p.current = nil
	}
	p.current = s
	return s
}

func (p *Progress) indexOf(name string) int {
	for j := p.i; j < len(p.names); j++ {
		if p.names[j] == name {
			return j
		}
	}
	return -1
}

// counter renders the "[2/6] " prefix, padded so every step's glyph lines up
// however many digits the totals need.
func (p *Progress) counter(n int) string {
	digits := len(fmt.Sprint(len(p.names)))
	return p.r.faint.Render(fmt.Sprintf("[%*d/%d]", digits, n, len(p.names))) + " "
}

// Finish closes the run with a bar and a verdict, and reports whether every
// step succeeded.
func (p *Progress) Finish() bool {
	if p.current != nil {
		p.current.Done()
	}
	var ok, failed, skipped int
	for _, s := range p.states {
		switch s {
		case StateReady, StateWarn:
			ok++
		case StateFailed:
			failed++
		default:
			skipped++
		}
	}
	total := len(p.names)
	elapsed := formatDuration(p.r.now().Sub(p.start))

	p.r.emit("")
	p.r.emit(detailIndent + p.Bar(barWidth) + "  " +
		p.r.muted.Render(fmt.Sprintf("%d/%d", ok, total)) + " " +
		p.r.faint.Render("· "+elapsed))

	switch {
	case failed > 0:
		p.r.Error(fmt.Sprintf("%s — %d of %d steps done, %d failed", p.title, ok, total, failed))
	case skipped > 0:
		p.r.Success(fmt.Sprintf("%s — %d of %d steps done, %d skipped  %s",
			p.title, ok, total, skipped, p.r.faint.Render(elapsed)))
	default:
		p.r.Success(fmt.Sprintf("%s — %d steps in %s", p.title, total, elapsed))
	}
	return failed == 0
}

const barWidth = 24

// Bar renders this run's progress bar at the given width.
func (p *Progress) Bar(width int) string {
	done := 0
	for _, s := range p.states {
		if s == StateReady || s == StateWarn {
			done++
		}
	}
	return p.r.bar(done, len(p.names), width)
}

// Bar renders a standalone progress bar. Exported because a bar is useful
// outside a Progress — a wait loop, a rollout — and there must be exactly one
// implementation of what a bar looks like.
func (r *Renderer) Bar(done, total, width int) string { return r.bar(done, total, width) }

func (r *Renderer) bar(done, total, width int) string {
	if width <= 0 {
		return ""
	}
	if total <= 0 {
		return r.faint.Render(strings.Repeat(IconBarVoid, width))
	}
	if done > total {
		done = total
	}
	if done < 0 {
		done = 0
	}
	full := done * width / total
	return r.ok.Render(strings.Repeat(IconBarFull, full)) +
		r.faint.Render(strings.Repeat(IconBarVoid, width-full))
}
