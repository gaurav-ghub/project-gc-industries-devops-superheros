package render

import (
	"io"
	"os"
	"sync"
)

// The process-wide renderer, and the package-level shorthands that use it.
//
// Commands call render.Step("…") rather than threading a *Renderer through
// every function, because there is exactly one terminal and pretending
// otherwise buys nothing. Tests — and any future command that needs to capture
// its own output — construct a Renderer with New instead.

var (
	defaultMu sync.RWMutex
	def       = New(os.Stdout)
)

// Default returns the process-wide renderer.
func Default() *Renderer {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return def
}

// SetDefault replaces the process-wide renderer and returns the previous one,
// so a caller can restore it:
//
//	old := render.SetDefault(render.New(&buf, render.WithColor(false)))
//	defer render.SetDefault(old)
func SetDefault(r *Renderer) *Renderer {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	old := def
	def = r
	return old
}

// SetOutput points the process-wide renderer at a different writer, detecting
// colour and terminal behaviour afresh.
func SetOutput(w io.Writer, opts ...Option) { SetDefault(New(w, opts...)) }

// Static primitives.
func Banner(version string)                                   { Default().Banner(version) }
func Section(title string)                                    { Default().Section(title) }
func Rule()                                                   { Default().Rule() }
func Blank()                                                  { Default().Blank() }
func Print(a ...any)                                          { Default().Print(a...) }
func Printf(format string, a ...any)                          { Default().Printf(format, a...) }
func Step(msg string)                                         { Default().Step(msg) }
func Success(msg string)                                      { Default().Success(msg) }
func Warn(msg string)                                         { Default().Warn(msg) }
func Error(msg string)                                        { Default().Error(msg) }
func Info(msg string)                                         { Default().Info(msg) }
func Detail(msg string)                                       { Default().Detail(msg) }
func Value(s string) string                                   { return Default().Value(s) }
func Muted(s string) string                                   { return Default().Muted(s) }
func Icon(s State) string                                     { return Default().Icon(s) }
func Checks(checks []Check)                                   { Default().Checks(checks) }
func Bar(done, total, width int) string                       { return Default().Bar(done, total, width) }
func Dashboard(title string, rows [][2]string, next []string) { Default().Dashboard(title, rows, next) }
func SuccessScreen(res Result)                                { Default().SuccessScreen(res) }
func URLBlock(title string, urls []URL)                       { Default().URLBlock(title, urls) }
func CredentialBlock(title string, creds []Credential)        { Default().CredentialBlock(title, creds) }

// Live primitives.
func StartStep(msg string) *LiveStep { return Default().StartStep(msg) }
func NewProgress(title string, steps ...string) *Progress {
	return Default().NewProgress(title, steps...)
}

// NewStream and NewStreamWith are named for the type they return, not for the
// method they delegate to: the package already has a Stream type, and a
// package-level func Stream would shadow it.
func NewStream() *Stream { return Default().Stream() }
func NewStreamWith(indent string, filter func(string) bool) *Stream {
	return Default().StreamWith(indent, filter)
}
