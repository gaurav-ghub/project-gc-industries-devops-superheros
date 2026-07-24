package platform

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// fakeProbe answers for a machine the test describes: which tools are on PATH,
// and what each one says when asked its version.
func fakeProbe(missing []string, says map[string]string, broken map[string]error) *probe {
	gone := map[string]bool{}
	for _, m := range missing {
		gone[m] = true
	}
	p := probe{
		lookPath: func(name string) (string, error) {
			if gone[name] {
				return "", errors.New("executable file not found in $PATH")
			}
			return "/usr/bin/" + name, nil
		},
		output: func(name string, _ ...string) (string, error) {
			if err, ok := broken[name]; ok {
				return says[name], err
			}
			if out, ok := says[name]; ok {
				return out, nil
			}
			return name + " 1.0.0", nil
		},
	}
	return &p
}

// healthy is a machine everything is installed on, with the istioctl the
// platform actually declares.
func healthy(t *testing.T) *probe {
	t.Helper()
	root := repoRoot(t)
	declared, err := declaredIstioVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	return fakeProbe(nil, map[string]string{
		"docker":   "27.1.1",
		"istioctl": "client version: " + declared,
	}, nil)
}

func missingTool(name string) *probe {
	return fakeProbe([]string{name}, map[string]string{"docker": "27.1.1"}, nil)
}

func find(t *testing.T, checks []render.Check, name string) render.Check {
	t.Helper()
	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %v", name, checks)
	return render.Check{}
}

func TestPreflightPassesOnAHealthyMachine(t *testing.T) {
	root := repoRoot(t)
	checks := Preflight(*healthy(t), root)

	for _, c := range checks {
		if c.State != render.StateReady {
			t.Errorf("%s = %v (%s), want ready", c.Name, c.State, c.Note)
		}
	}
	if find(t, checks, "platform repo").Note != root {
		t.Error("the preflight does not report which repo it found")
	}
	if err := preflightVerdict(checks); err != nil {
		t.Errorf("a healthy machine failed the preflight: %v", err)
	}
}

// TestPreflightReportsAMissingTool is the phase's stated verification: doctor
// correctly reports a missing tool — and, just as importantly, says where to
// get it. "kind: not found" is a diagnosis; a developer wants a next step.
func TestPreflightReportsAMissingTool(t *testing.T) {
	root := repoRoot(t)
	checks := Preflight(*missingTool("kind"), root)

	kind := find(t, checks, "kind")
	if kind.State != render.StateFailed {
		t.Fatalf("a missing kind was reported as %v", kind.State)
	}
	if !strings.Contains(kind.Note, "not found") || !strings.Contains(kind.Note, "https://") {
		t.Errorf("the note does not carry an install link: %q", kind.Note)
	}
	// Nothing else may be dragged down with it.
	if find(t, checks, "helm").State != render.StateReady {
		t.Error("one missing tool marked the others as broken")
	}
	if err := preflightVerdict(checks); err == nil {
		t.Error("the preflight passed with a required tool missing")
	}
}

// TestPreflightCatchesADockerDaemonThatIsNotRunning — the most common failure
// on a laptop, and the one where "docker: found" would be a lie. `docker
// version --format {{.Server.Version}}` exits non-zero without a daemon.
func TestPreflightCatchesADockerDaemonThatIsNotRunning(t *testing.T) {
	root := repoRoot(t)
	p := fakeProbe(nil,
		map[string]string{"docker": "error during connect: this error may indicate that the docker daemon is not running"},
		map[string]error{"docker": errors.New("exit status 1")})

	docker := find(t, Preflight(*p, root), "docker")
	if docker.State != render.StateFailed {
		t.Fatalf("docker with no daemon was reported as %v", docker.State)
	}
	if !strings.Contains(docker.Note, "daemon") {
		t.Errorf("the note does not say the daemon is the problem: %q", docker.Note)
	}
}

// TestPreflightFailsOnAnIstioctlMismatch.
//
// istioctl installs the control plane it was built with, so the Istio module
// refuses to run against a different one. Reporting this as a warning would
// mean a bootstrap that dies four steps in on a machine doctor called healthy —
// which is exactly the outcome the preflight exists to prevent.
func TestPreflightFailsOnAnIstioctlMismatch(t *testing.T) {
	root := repoRoot(t)
	declared, err := declaredIstioVersion(root)
	if err != nil || declared == "" {
		t.Skip("no declared istio version to compare against")
	}
	p := fakeProbe(nil, map[string]string{
		"docker":   "27.1.1",
		"istioctl": "client version: 1.28.0",
	}, nil)

	istio := find(t, Preflight(*p, root), "istioctl")
	if istio.State != render.StateFailed {
		t.Fatalf("an istioctl the platform cannot use was reported as %v", istio.State)
	}
	if !strings.Contains(istio.Note, declared) || !strings.Contains(istio.Note, "1.28.0") {
		t.Errorf("the note does not name both versions: %q", istio.Note)
	}
	if !strings.Contains(istio.Note, "ISTIO_VERSION=") {
		t.Errorf("the note does not carry the command that fixes it: %q", istio.Note)
	}
}

// TestPreflightAcceptsEitherIstioctlVersionFormat — istioctl prints "1.23.1" or
// "client version: 1.23.1" depending on the release.
func TestPreflightAcceptsEitherIstioctlVersionFormat(t *testing.T) {
	root := repoRoot(t)
	declared, err := declaredIstioVersion(root)
	if err != nil || declared == "" {
		t.Skip("no declared istio version to compare against")
	}
	for _, format := range []string{declared, "client version: " + declared} {
		p := fakeProbe(nil, map[string]string{"docker": "27.1.1", "istioctl": format}, nil)
		if c := find(t, Preflight(*p, root), "istioctl"); c.State != render.StateReady {
			t.Errorf("istioctl reporting %q was read as %v (%s)", format, c.State, c.Note)
		}
	}
}

func TestDoctorPrintsEveryToolItNeeds(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	checks := Preflight(*healthy(t), root)
	render.Checks(checks)
	got := buf.String()

	// Every tool the chain shells out to has to appear, including istioctl,
	// whose absence does not self-heal: the Istio module's download_istioctl is
	// a stub that installs nothing.
	for _, name := range []string{"docker", "kind", "kubectl", "helm", "istioctl", "git", "bash"} {
		if !strings.Contains(got, name) {
			t.Errorf("doctor does not check %s:\n%s", name, got)
		}
	}
	if len(checks) != len(tools)+1 {
		t.Errorf("checks = %d, want %d (%d tools + the repo)", len(checks), len(tools)+1, len(tools))
	}
	fmt.Fprint(buf)
}
