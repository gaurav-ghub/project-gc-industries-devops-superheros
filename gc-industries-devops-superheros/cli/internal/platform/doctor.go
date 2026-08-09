package platform

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gc-ghub/endurance/internal/installer"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// `endurance doctor` — the preflight.
//
// It answers one question: if I run `endurance bootstrap` right now, will it
// get as far as installing something? Every check here is a thing the bootstrap
// genuinely needs; nothing is listed to pad the output. A check that fails
// carries the URL that fixes it, because "docker: not found" is a diagnosis and
// a developer wants a next step.
//
// Doctor is a lookup, not a run: it prints a rule, the aligned check list and a
// verdict, and no box. Only a run earns a box.

// A probe is how doctor learns about the machine. Tests replace it; nothing
// else does.
type probe struct {
	lookPath func(name string) (string, error)
	output   func(name string, args ...string) (string, error)
}

func realProbe() probe {
	return probe{
		lookPath: exec.LookPath,
		output: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).CombinedOutput()
			return string(out), err
		},
	}
}

// A tool is one command the platform shells out to, and how to prove it works.
type tool struct {
	name    string
	args    []string // how to ask it its version
	install string   // where to get it
	// verify optionally turns the version output into a note, or into an error
	// that fails the check even though the binary exists.
	verify func(p probe, root, out string) (note string, err error)
}

// tools is the required set. All of them are required: the bootstrap chain
// shells out to every one, and a preflight that passes with a tool missing is
// a preflight that lied.
//
// istioctl is on this list for a reason that is easy to get wrong. The Istio
// module's download_istioctl is a stub that logs and installs nothing, so a
// machine without istioctl does not auto-recover — it fails four steps into a
// bootstrap, after the cluster exists. Doctor says so before the cluster does.
var tools = []tool{
	{
		name: "docker", args: []string{"version", "--format", "{{.Server.Version}}"},
		install: "https://docs.docker.com/get-docker/",
		verify:  dockerDaemon,
	},
	{name: "kind", args: []string{"version"}, install: "https://kind.sigs.k8s.io/docs/user/quick-start/#installation"},
	{name: "kubectl", args: []string{"version", "--client"}, install: "https://kubernetes.io/docs/tasks/tools/"},
	{name: "helm", args: []string{"version", "--short"}, install: "https://helm.sh/docs/intro/install/"},
	{name: "istioctl", args: []string{"version", "--remote=false", "--short"},
		install: "https://istio.io/latest/docs/setup/getting-started/#download",
		verify:  istioctlMatchesDeclared},
	{name: "git", args: []string{"--version"}, install: "https://git-scm.com/downloads"},
	{name: "bash", args: []string{"--version"}, install: "https://git-scm.com/downloads (git-bash, on Windows)"},
}

// DoctorOptions configures a preflight run.
type DoctorOptions struct {
	Root string // platform repo root ("" = discover)

	// NoBanner suppresses the product banner, for a caller that has already
	// drawn one — `endurance init` runs the preflight as its first phase.
	NoBanner bool
}

// Doctor runs the preflight and prints it. It returns an error when a required
// tool is missing or unusable, so `endurance doctor` exits non-zero and can be
// used as a gate in a script.
func Doctor(opts DoctorOptions) error {
	if !opts.NoBanner {
		render.Banner(version.Current)
	}
	// A missing repo used to end the command here, with one failed check and no
	// idea whether Docker was running. Since Phase 12 the first thing a stranger
	// has is the binary and nothing else — `curl … | bash` installs one file —
	// so `endurance doctor` on a machine with no clone has to be worth running.
	// Every tool check is about the machine and needs no repo; only istioctl's
	// version comparison does, and it already says so when it cannot read the
	// declared version.
	root, _ := FindRoot(opts.Root)

	render.Section("Preflight")
	checks := Preflight(realProbe(), root)
	render.Checks(checks)
	render.Blank()
	// Which endurance is running is a preflight question and not a tool
	// question, so it is reported here rather than added to the list above:
	// Preflight is also bootstrap's gate and must stay answerable from a probe
	// alone. It says nothing at all unless there is something to say, and the
	// something is B1 — a binary on $PATH older than the one this repo built,
	// printing the same version, generating different files. `endurance init`
	// runs this preflight as its first phase, so a guided first run gets it too.
	if root != "" {
		reportBinary(InspectBinary(root))
	}
	return preflightVerdict(checks)
}

// Preflight runs the checks and returns them without printing, so bootstrap can
// gate on the same list it shows.
//
// An empty root means no platform repo was found. That is a failed check rather
// than a reason not to run the others: the tools are a fact about the machine
// either way, and reporting "no repo" while saying nothing about a stopped
// Docker daemon sends somebody to fix one thing and come back to the other.
func Preflight(p probe, root string) []render.Check {
	checks := make([]render.Check, 0, len(tools)+1)
	for _, t := range tools {
		checks = append(checks, t.check(p, root))
	}
	repo := render.Check{Name: "platform repo", State: render.StateReady, Note: root}
	if root == "" {
		// Same shape as every other failed check on this list: what is missing,
		// then the one thing that fixes it. A missing repo is not a different
		// kind of problem from a missing kind, and giving it its own paragraph
		// under the table made the verdict's "install what is missing above"
		// read as though it were about something else.
		repo.State = render.StateFailed
		repo.Note = "not found here — git clone https://github.com/" + installer.Repo + ".git"
	}
	checks = append(checks, repo)
	return checks
}

func (t tool) check(p probe, root string) render.Check {
	if _, err := p.lookPath(t.name); err != nil {
		return render.Check{
			Name: t.name, State: render.StateFailed,
			Note: "not found — " + t.install,
		}
	}
	out, err := p.output(t.name, t.args...)
	if err != nil {
		// The binary is on PATH but would not answer. Docker's daemon being
		// down is the common case and the one worth naming; anything else
		// still reports the tool's own first line, which is usually the reason.
		return render.Check{
			Name: t.name, State: render.StateFailed,
			Note: fmt.Sprintf("found, but `%s %s` failed: %s",
				t.name, strings.Join(t.args, " "), firstLine(out)),
		}
	}
	note := firstLine(out)
	if t.verify != nil {
		n, verr := t.verify(p, root, out)
		if verr != nil {
			return render.Check{Name: t.name, State: render.StateFailed, Note: verr.Error()}
		}
		note = n
	}
	return render.Check{Name: t.name, State: render.StateReady, Note: note}
}

// dockerDaemon reports the server version, which is only printable when the
// daemon is actually running — `docker version --format {{.Server.Version}}`
// exits non-zero with the client alone, so reaching here means it answered.
func dockerDaemon(_ probe, _, out string) (string, error) {
	v := firstLine(out)
	if v == "" {
		return "", fmt.Errorf("the Docker daemon is not responding — start Docker Desktop and re-run")
	}
	return v + " · daemon running", nil
}

// istioctlMatchesDeclared holds istioctl to the version the platform says it
// installs.
//
// This is not doctor being fussy. istioctl installs the control plane it was
// built with, so the Istio module refuses to run against a different one (see
// platform/networking/versions.yaml). Reporting the mismatch as a warning would
// mean a bootstrap that fails four steps in on a machine doctor called healthy.
func istioctlMatchesDeclared(_ probe, root, out string) (string, error) {
	installed := lastField(firstLine(out))
	declared, err := declaredIstioVersion(root)
	if err != nil || declared == "" {
		return installed + " · could not read the declared version", nil
	}
	if installed != declared {
		return "", fmt.Errorf("%s installed, but the platform installs Istio %s — "+
			"curl -L https://istio.io/downloadIstio | ISTIO_VERSION=%s sh -",
			installed, declared, declared)
	}
	return installed + " · matches " + declared, nil
}

// preflightVerdict closes the list the way a run closes: one line that says
// whether the thing the user came to do is possible.
func preflightVerdict(checks []render.Check) error {
	var failed, warned int
	for _, c := range checks {
		switch c.State {
		case render.StateFailed:
			failed++
		case render.StateWarn:
			warned++
		}
	}
	total := len(checks)
	if failed > 0 {
		render.Info("install what is missing above, then run `endurance doctor` again")
		// The verdict is the returned error, rendered once by the caller.
		return fmt.Errorf("preflight failed — %d of %d checks need attention", failed, total)
	}
	if warned > 0 {
		render.Warn(fmt.Sprintf("preflight passed with %s — %d of %d checks clean",
			plural(warned, "caveat"), total-warned, total))
	} else {
		render.Success(fmt.Sprintf("preflight passed — %d of %d checks", total, total))
	}
	render.Info("next: `endurance bootstrap` stands the platform up")
	return nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// lastField takes the version off the end of a line: istioctl prints either
// "1.23.1" or "client version: 1.23.1" depending on the release.
func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}
