package render

import (
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStreamGolden — the detail channel Phase 9 pipes the bash modules through.
// Whatever a subprocess writes arrives indented and muted, in one voice.
func TestStreamGolden(t *testing.T) {
	r, buf, c := fixture(t)

	s := r.StartStep("Installing Istio")
	out := r.Stream()
	out.Write([]byte("✔ Istio core installed\n✔ Istiod installed\n"))
	out.Write([]byte("✔ Ingress gateways installed\n"))
	out.Close()
	c.advance(41 * time.Second)
	s.DoneWith("3 components")

	golden(t, "stream", buf.String())
}

func TestStreamBuffersPartialLines(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.Stream()
	out.Write([]byte("Creating clus"))
	if buf.Len() != 0 {
		t.Errorf("a partial line was printed early: %q", buf.String())
	}
	out.Write([]byte("ter \"superheros\" ...\n"))
	if got, want := buf.String(), "  Creating cluster \"superheros\" ...\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStreamCloseFlushesFinalLine — a process that dies mid-line still says
// what it was saying.
func TestStreamCloseFlushesFinalLine(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.Stream()
	out.Write([]byte("panic: something went wrong"))
	out.Close()
	if !strings.Contains(buf.String(), "panic: something went wrong") {
		t.Errorf("the last line was lost: %q", buf.String())
	}
}

// TestStreamStripsSubprocessColor — helm, kind and kubectl each have their own
// palette. The detail channel has one colour, and the subprocess does not vote.
func TestStreamStripsSubprocessColor(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.Stream()
	out.Write([]byte("\x1b[32m✔ Istio core installed\x1b[0m\n\x1b]0;title\x07plain\n"))
	out.Close()
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("subprocess escape sequences survived: %q", buf.String())
	}
	if want := "  ✔ Istio core installed\n  plain\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestStreamTreatsCarriageReturnsAsLines — kind and docker draw progress with
// \r. Printed literally it would overwrite Endurance's own output.
func TestStreamTreatsCarriageReturnsAsLines(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.Stream()
	out.Write([]byte("Pulling 10%\rPulling 60%\rPulling 100%\r\n"))
	out.Close()
	want := "  Pulling 10%\n  Pulling 60%\n  Pulling 100%\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestStreamCollapsesBlankLines — bash scripts echo blank lines generously;
// three in a row are noise, one is punctuation, and a leading one is neither.
func TestStreamCollapsesBlankLines(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.Stream()
	out.Write([]byte("\n\n\nfirst\n\n\n\nsecond\n"))
	out.Close()
	if want := "  first\n\n  second\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
	if out.Lines() != 3 {
		t.Errorf("Lines() = %d, want 3", out.Lines())
	}
}

func TestStreamFilter(t *testing.T) {
	r, buf, _ := fixture(t)
	out := r.StreamWith("    ", func(line string) bool {
		return !strings.HasPrefix(line, "Waiting for")
	})
	out.Write([]byte("Waiting for deployment\nWaiting for deployment\ndeployment rolled out\n"))
	out.Close()
	if want := "    deployment rolled out\n"; buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

// TestStreamFromRealSubprocess — the point of the type is exec.Cmd, so wire one
// up for real. This is the exact shape Phase 9 will use for the bash modules.
func TestStreamFromRealSubprocess(t *testing.T) {
	shell, flag := "sh", "-c"
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			shell, flag = "cmd", "/c"
		}
	}
	script := "echo one; echo two 1>&2"
	if shell == "cmd" {
		script = "echo one & echo two 1>&2"
	}

	r, buf, _ := fixture(t)
	step := r.StartStep("Running a module")
	out := step.Stream()
	cmd := exec.Command(shell, flag, script)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		t.Fatalf("subprocess failed: %v", err)
	}
	out.Close()
	step.Done()

	got := buf.String()
	for _, want := range []string{"  one", "  two", "✓ Running a module"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
