package render

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
)

// ansiSeq matches the escape sequences a child process emits when it thinks it
// is talking to a terminal. Endurance strips them.
//
// This is the consistency fix at its smallest scale: helm, kind and kubectl all
// have opinions about colour, and if their opinions survive into our output the
// user is reading three products again. The detail channel has one colour —
// muted — and the subprocess does not get a vote.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// A Stream is the streamed-subprocess detail channel: an io.WriteCloser that
// turns everything a child process writes into indented, muted lines.
//
// Phase 9 wires the M1–M4 bash modules through it —
//
//	step := r.StartStep("Installing Istio")
//	out := step.Stream()
//	cmd.Stdout, cmd.Stderr = out, out
//	err := cmd.Run()
//	out.Close()
//
// — which is the whole mechanism by which bash stops being a second visual
// system: its output becomes content inside Endurance's frame rather than a
// competing frame of its own.
//
// Partial writes are buffered until a line ends, so a process that writes a
// line in three syscalls still produces one line here. Carriage returns are
// treated as line breaks (that is how kind and docker draw progress), runs of
// blank lines collapse to one, and trailing whitespace goes.
type Stream struct {
	r      *Renderer
	indent string
	filter func(string) bool

	mu       sync.Mutex
	buf      bytes.Buffer
	lines    int
	lastVoid bool // the last line emitted was blank
	skipLF   bool // the previous line ended in \r; a \n now is the same break
}

// Stream returns a detail channel with the standard indent.
func (r *Renderer) Stream() *Stream { return r.StreamWith(detailIndent, nil) }

// StreamWith returns a detail channel with a custom indent and an optional
// filter. The filter reports whether a line should be shown; it is how a
// command drops the noise a tool insists on ("Waiting for deployment ..."
// forty times) without hiding the tool's real output.
func (r *Renderer) StreamWith(indent string, filter func(line string) bool) *Stream {
	return &Stream{r: r, indent: indent, filter: filter}
}

// Write implements io.Writer.
func (s *Stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Write(p)
	for {
		data := s.buf.Bytes()
		// A \r\n pair is one break, not two — otherwise every line of
		// Windows-authored output would be followed by a blank one.
		if s.skipLF && len(data) > 0 && data[0] == '\n' {
			s.buf.Next(1)
			s.skipLF = false
			continue
		}
		i := bytes.IndexAny(data, "\n\r")
		if i < 0 {
			return len(p), nil
		}
		line := string(data[:i])
		s.skipLF = data[i] == '\r'
		s.buf.Next(i + 1)
		s.emitLine(line)
	}
}

// Close flushes a final line that never got its newline. A subprocess that dies
// mid-line still says what it was saying.
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipLF = false
	if s.buf.Len() > 0 {
		line := s.buf.String()
		s.buf.Reset()
		s.emitLine(line)
	}
	return nil
}

// Lines is how many lines this stream has shown. Phase 9 uses it to say
// "42 lines of output hidden" when a step succeeds quietly.
func (s *Stream) Lines() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lines
}

func (s *Stream) emitLine(line string) {
	line = strings.TrimRight(ansiSeq.ReplaceAllString(line, ""), " \t\r")
	if line == "" {
		if s.lastVoid || s.lines == 0 {
			return // never open with, or repeat, an empty line
		}
		s.lastVoid = true
		s.lines++
		s.r.emit("")
		return
	}
	if s.filter != nil && !s.filter(line) {
		return
	}
	s.lastVoid = false
	s.lines++
	s.r.emit(s.indent + s.r.muted.Render(line))
}
