// Package render is Endurance's single output library.
//
// Everything the user sees comes from here. That is the whole point of the
// package: before Phase 8 the platform had two visual systems — this one and the
// bash `lib/*` logger with its `====` rules and `[INFO]` tags — and a tool that
// looks like two products is one product less. The decision (see the 2026-07-24
// "Go owns all rendering" entry in decisions.md) is that Go owns rendering and
// bash is plumbing whose output arrives here as a muted detail stream.
//
// The look is restrained and Claude/Vercel-flavoured: a boxless product banner,
// one semantic glyph per meaning (▸ ✓ ⚠ ✗ ·), a small palette, titled rules to
// open a phase of work and a box to close one.
//
// # Writing to a writer, not to stdout
//
// Every primitive is a method on a [Renderer] bound to an io.Writer, with
// package-level shorthands that delegate to the process-wide [Default]
// renderer. Nothing calls fmt.Println into the void. This is what makes the
// output testable: a test renders into a bytes.Buffer with colour forced off
// and a frozen clock, and compares against a golden file in testdata/.
//
// # Live output degrades honestly
//
// [Renderer.StartStep], [Renderer.NewProgress] and [Renderer.Stream] are "live"
// primitives: on a terminal a step spins in place and is rewritten with its ✓
// when it finishes. When the writer is not a terminal — a pipe, a CI log, a
// golden test — there is no cursor to move, so the step prints its start line
// and later its completion line. Same information, no escape codes.
package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Tagline is the product's one-line positioning, shown under the wordmark.
// Single source — nothing else may spell it out.
const Tagline = "Mission Control for Every Application"

// The glyph vocabulary. One glyph per meaning, everywhere, forever: a reader
// should be able to learn five characters once. Commands must use these
// constants rather than typing the runes, so the set can only grow deliberately.
const (
	IconStep    = "▸" // work starting
	IconOK      = "✓" // it worked
	IconWarn    = "⚠" // it worked, but read this
	IconError   = "✗" // it did not work
	IconInfo    = "·" // context; also "pending", "skipped", "nothing happened"
	IconNext    = "→" // something for the user to do next
	IconRule    = "─" // rules and separators
	IconBarFull = "█" // progress, done
	IconBarVoid = "░" // progress, not yet
)

// DefaultWidth is the column the rules, boxes and bars are drawn to. Narrow on
// purpose: 58 columns fits every terminal anyone demos in, including a laptop
// screen shared over a call.
const DefaultWidth = 58

// EnvNoColor disables colour without disabling the layout. NO_COLOR and
// CLICOLOR are honoured too (termenv reads them); this one exists so a user can
// turn off Endurance's colour without turning off everything else's.
const EnvNoColor = "ENDURANCE_NO_COLOR"

// The palette — deliberately small. A colour that means two things means
// nothing, so each one has exactly one job.
var (
	cBrand  = lipgloss.Color("99")  // purple — the product itself
	cAccent = lipgloss.Color("45")  // cyan   — headings, active work, values
	cOK     = lipgloss.Color("42")  // green  — success
	cWarn   = lipgloss.Color("214") // yellow — warning
	cBad    = lipgloss.Color("196") // red    — error
	cMuted  = lipgloss.Color("245") // gray   — secondary text, subprocess output
	cFaint  = lipgloss.Color("240") // dim    — rules, hints, borders
)

// A Renderer draws Endurance's output to one writer.
//
// It is safe for concurrent use: the live primitives run a spinner on their own
// goroutine, and it and the caller both write through the same mutex.
type Renderer struct {
	mu    sync.Mutex
	w     io.Writer
	lip   *lipgloss.Renderer
	width int
	now   func() time.Time
	tty   bool
	spin  bool

	// parked reports that the cursor is sitting at the end of a live step's
	// start line, with no newline written yet. Anything that wants to write
	// must break the line first; the step itself may instead rewrite it in
	// place. This one bit is what lets a step upgrade from ▸ to ✓ without
	// leaving a duplicate behind, and what makes interleaved subprocess
	// output safe.
	parked bool

	brand, head, ok, warn, bad, muted, faint lipgloss.Style
}

// An Option configures a Renderer at construction.
type Option func(*Renderer)

// WithColor forces colour on or off instead of detecting it from the writer.
// Tests pass false so golden files hold text, not escape sequences.
func WithColor(on bool) Option {
	return func(r *Renderer) {
		if on {
			if r.lip.ColorProfile() == termenv.Ascii {
				r.lip.SetColorProfile(termenv.ANSI256)
			}
			return
		}
		r.lip.SetColorProfile(termenv.Ascii)
	}
}

// WithWidth sets the column rules, boxes and bars are drawn to.
func WithWidth(n int) Option {
	return func(r *Renderer) {
		if n > 0 {
			r.width = n
		}
	}
}

// WithClock replaces time.Now. Step durations are the only nondeterminism in
// the output, so a test that wants a stable golden file freezes the clock here.
func WithClock(now func() time.Time) Option {
	return func(r *Renderer) {
		if now != nil {
			r.now = now
		}
	}
}

// WithTTY forces terminal behaviour (in-place rewriting, spinners) on or off.
// Detection is right in every case that matters; this exists for tests and for
// a caller who knows better.
func WithTTY(on bool) Option {
	return func(r *Renderer) {
		r.tty = on
		r.spin = on
	}
}

// New returns a Renderer writing to w.
//
// Colour and terminal behaviour are detected from w: a file that is a terminal
// gets both, a pipe or a buffer gets neither. That single detection is why the
// same code path serves a demo, a CI log and a golden test.
func New(w io.Writer, opts ...Option) *Renderer {
	lr := lipgloss.NewRenderer(w)
	isTTY := isTerminal(w)
	r := &Renderer{
		w:     w,
		lip:   lr,
		width: DefaultWidth,
		now:   time.Now,
		tty:   isTTY,
		spin:  isTTY,
	}
	if os.Getenv(EnvNoColor) != "" {
		lr.SetColorProfile(termenv.Ascii)
	}
	r.restyle()
	for _, o := range opts {
		o(r)
	}
	return r
}

func (r *Renderer) restyle() {
	s := r.lip.NewStyle
	r.brand = s().Foreground(cBrand).Bold(true)
	r.head = s().Foreground(cAccent).Bold(true)
	r.ok = s().Foreground(cOK)
	r.warn = s().Foreground(cWarn)
	r.bad = s().Foreground(cBad)
	r.muted = s().Foreground(cMuted)
	r.faint = s().Foreground(cFaint)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// Width is the column this renderer draws to.
func (r *Renderer) Width() int { return r.width }

// IsTTY reports whether this renderer is drawing to a terminal — i.e. whether
// live primitives will animate. Callers should not need it; it is here so a
// command can decide how chatty to be.
func (r *Renderer) IsTTY() bool { return r.tty }

// ---------------------------------------------------------------------------
// The write path. Every byte this package emits goes through emit or park.
// ---------------------------------------------------------------------------

// emit writes one finished line, breaking a parked live-step line first.
func (r *Renderer) emit(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emitLocked(line)
}

func (r *Renderer) emitLocked(line string) {
	r.unparkLocked()
	fmt.Fprintln(r.w, line)
}

// unparkLocked ends a live step's in-progress line so other output can follow.
func (r *Renderer) unparkLocked() {
	if r.parked {
		fmt.Fprintln(r.w)
		r.parked = false
	}
}

// park writes a line that the cursor stays on, so it can be rewritten later.
// On a non-terminal there is no cursor to keep, so it is an ordinary line.
func (r *Renderer) parkLocked(line string) {
	r.unparkLocked()
	if !r.tty {
		fmt.Fprintln(r.w, line)
		return
	}
	fmt.Fprint(r.w, line)
	r.parked = true
}

// repark rewrites the parked line in place (\r, erase to end of line).
func (r *Renderer) reparkLocked(line string) {
	if !r.parked {
		return
	}
	fmt.Fprint(r.w, "\r\x1b[2K"+line)
}

// resolveLocked finishes a parked line with its final text: rewritten in place
// on a terminal, printed as its own line anywhere else.
func (r *Renderer) resolveLocked(line string) {
	if r.parked {
		fmt.Fprint(r.w, "\r\x1b[2K"+line+"\n")
		r.parked = false
		return
	}
	fmt.Fprintln(r.w, line)
}

// ---------------------------------------------------------------------------
// Static primitives.
// ---------------------------------------------------------------------------

// Print writes raw text as a line, unstyled. It exists so that output which is
// genuinely not ours — kubectl's table, a usage block — still goes through the
// renderer's writer and cannot land in the middle of a live step.
func (r *Renderer) Print(a ...any) { r.emit(strings.TrimRight(fmt.Sprint(a...), "\n")) }

// Printf is Print with formatting. A trailing newline is optional; the line is
// terminated either way.
func (r *Renderer) Printf(format string, a ...any) {
	r.emit(strings.TrimRight(fmt.Sprintf(format, a...), "\n"))
}

// Blank writes one empty line.
func (r *Renderer) Blank() { r.emit("") }

// Section opens a phase of work with a titled rule. A rule opens work; a box
// (see Dashboard, SuccessScreen) closes it.
func (r *Renderer) Section(title string) {
	r.emit("\n" + r.sectionLine(title))
}

func (r *Renderer) sectionLine(title string) string {
	head := r.head.Render(IconRule + IconRule + " " + title + " ")
	pad := r.width - lipgloss.Width(head)
	if pad < 0 {
		pad = 0
	}
	return head + r.faint.Render(strings.Repeat(IconRule, pad))
}

// Rule writes an untitled horizontal rule.
func (r *Renderer) Rule() { r.emit(r.faint.Render(strings.Repeat(IconRule, r.width))) }

// Step, Success, Warn, Error and Info are the semantic log lines: work
// starting, work that succeeded, work that succeeded with a caveat, work that
// failed, and context that is not work at all.
func (r *Renderer) Step(msg string)    { r.emit(r.head.Render(IconStep) + " " + msg) }
func (r *Renderer) Success(msg string) { r.emit(r.ok.Render(IconOK) + " " + msg) }
func (r *Renderer) Warn(msg string)    { r.emit(r.warn.Render(IconWarn) + " " + msg) }
func (r *Renderer) Error(msg string)   { r.emit(r.bad.Render(IconError) + " " + msg) }
func (r *Renderer) Info(msg string)    { r.emit(r.muted.Render(IconInfo) + " " + msg) }

// Detail prints an indented, muted line under whatever came before it —
// the secondary channel. Subprocess output arrives the same way (see Stream),
// which is what keeps a helm install from shouting over the CLI's own voice.
func (r *Renderer) Detail(msg string) { r.emit(detailIndent + r.muted.Render(msg)) }

const detailIndent = "  "

// Value styles a value for inline use: cyan, the same colour headings use, so
// the eye can find the changing part of a line.
func (r *Renderer) Value(s string) string { return r.lip.NewStyle().Foreground(cAccent).Render(s) }

// Muted styles secondary text for inline use.
func (r *Renderer) Muted(s string) string { return r.muted.Render(s) }

// Icon returns the glyph for a State, in its colour.
func (r *Renderer) Icon(s State) string {
	switch s {
	case StateReady:
		return r.ok.Render(IconOK)
	case StateWarn:
		return r.warn.Render(IconWarn)
	case StateFailed:
		return r.bad.Render(IconError)
	default:
		return r.muted.Render(IconInfo)
	}
}

// A State is how one thing in a list of things is doing — a pod, a check, a
// component. Pending is the honest default: a pod that is not Running yet is
// not a failure, and the success screen must not claim otherwise.
type State int

const (
	StatePending State = iota
	StateReady
	StateWarn
	StateFailed
)

// formatDuration renders an elapsed time the way a human reads one: exact
// enough to compare runs, never more precise than it is useful.
func formatDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "0ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
}

// pad right-pads plain text to n visible columns.
func pad(s string, n int) string {
	if w := lipgloss.Width(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
