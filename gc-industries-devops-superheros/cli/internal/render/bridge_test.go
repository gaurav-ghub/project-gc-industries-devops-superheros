package render

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBashPlumbingRendersInOneVoice is the Phase 8 exit criterion as a test.
//
// The claim is that bash stopped being a second visual system: platform/lib/*
// emits plain facts, the Go renderer frames them, and the result reads as one
// product. The only way to know that is to run the real logger — not a mock of
// it — and look at what comes out the other side.
//
// It also pins the half of the contract bash owns: no colour, no rules, no
// banner, `warning:`/`error:` on the lines that matter. If someone reintroduces
// [INFO] tags or an ==== rule, the golden file moves and this test says so.
func TestBashPlumbingRendersInOneVoice(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	logger, err := filepath.Abs(filepath.Join("..", "..", "..", "platform", "lib", "logger.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(logger); err != nil {
		t.Skipf("platform/lib/logger.sh not found: %v", err)
	}

	script := `. "$1"
print_section "Networking"
log_info "Installing Istio 1.23.0..."
log_success "Istio control plane installed."
log_warn "Kiali is not installed on this cluster"
log_success "Ingress gateway ready."`

	r, buf, c := fixture(t)
	step := r.StartStep("Installing Istio")
	out := step.Stream()

	// Both streams into one writer, and stderr merged by the shell rather than
	// by two pipes, so the transcript order is the script's own.
	cmd := exec.Command(bash, "-c", script+" 2>&1", "bash", logger)
	cmd.Env = append(os.Environ(), "ENDURANCE_FRAMED=1")
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		t.Fatalf("running the platform logger: %v", err)
	}
	out.Close()
	c.advance(41 * time.Second)
	step.DoneWith("4 lines from the module")

	got := buf.String()
	golden(t, "bash-bridge", got)

	// The framed contract, asserted rather than merely eyeballed.
	if strings.Contains(got, "[INFO]") || strings.Contains(got, "[SUCCESS]") {
		t.Error("the bash logger is tagging severities again — that was the second visual system")
	}
	if strings.Contains(got, "====") {
		t.Error("the bash logger is drawing rules again")
	}
	if strings.Contains(got, "\x1b") {
		t.Error("the bash logger emitted colour of its own")
	}
	if !strings.Contains(got, "  warning: Kiali is not installed on this cluster") {
		t.Errorf("the warning did not arrive on the detail channel:\n%s", got)
	}
}
