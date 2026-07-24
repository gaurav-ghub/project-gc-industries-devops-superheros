package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Golden-output testing for a terminal UI.
//
// The look is the product here, so "does it still look right" has to be a test
// and not a screenshot someone remembers taking. Every primitive renders into a
// buffer and is compared, byte for byte, against a file in testdata/. A diff
// means the output changed; if the change was intended, `go test ./internal/render
// -update` rewrites the files and the diff shows up in code review instead.
//
// Three things are pinned so the comparison is about the look and nothing else:
//
//   - colour off (WithColor(false)) — golden files hold text, not escape codes,
//     so they stay readable and stop depending on the terminal running the test;
//   - no TTY (WithTTY(false)) — no cursor moves, no spinner goroutine, and live
//     steps take exactly the degraded path a CI log or a pipe would take;
//   - a frozen clock — the only nondeterminism in the output is elapsed time,
//     so tests advance a fake clock by hand and durations become assertions.

var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// clock is a hand-wound time source for tests.
type clock struct{ t time.Time }

func newClock() *clock {
	return &clock{t: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time { return c.t }

func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// fixture returns a renderer writing into buf, with colour, TTY behaviour and
// time all pinned.
func fixture(t *testing.T) (*Renderer, *bytes.Buffer, *clock) {
	t.Helper()
	var buf bytes.Buffer
	c := newClock()
	r := New(&buf, WithColor(false), WithTTY(false), WithClock(c.now))
	return r, &buf, c
}

// golden compares got against testdata/<name>.golden.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file: %v (run `go test ./internal/render -update` to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}
