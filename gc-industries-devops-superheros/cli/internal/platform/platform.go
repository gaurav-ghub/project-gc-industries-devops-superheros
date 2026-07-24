// Package platform is the operator half of the Endurance CLI: bootstrap,
// doctor, destroy, platform status and the component versions behind
// `endurance version`.
//
// # The binary is the front door, bash is the plumbing
//
// The platform has always installed itself with bash — six modules under
// platform/, each with its own install.sh. Phase 9 does not rewrite them in Go
// and does not restyle them. It runs them as subprocesses, one per step of a
// progress chain, with ENDURANCE_FRAMED=1 in the environment and their combined
// output piped through the renderer's detail channel. The modules keep working
// on their own for an operator debugging one of them; what changes is that no
// developer ever needs to know they exist.
//
// That is the whole architectural claim of this package, and it is the one the
// 2026-07-24 "Go owns all rendering" decision asked for: one visual system, one
// entry point, and the plumbing untouched underneath it.
//
// # Nothing here deploys an application
//
// ArgoCD remains the only deployer. These commands stand up (or tear down) the
// cluster the platform runs on; `onboard` and `release` still only write git.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gc-ghub/endurance/internal/render"
)

// EnvFramed is the entire contract between this package and the bash modules.
//
// Set on every subprocess: it tells platform/lib/* that Endurance is drawing
// the frame, so the modules print no banner and no section headings and emit
// nothing but plain facts. Phase 8 deliberately kept it to one flag — anything
// richer (a step protocol spoken by bash) would be a second interface to keep
// in sync with a second implementation.
const EnvFramed = "ENDURANCE_FRAMED"

// warningPrefix is the other half of that contract: platform/lib/logger.sh
// opens a line with it (lowercase, on stderr) when the line matters more than
// the rest. Kept here as a constant because it is a protocol, not a word.
const warningPrefix = "warning:"

// Paths inside the platform repo. They are relative and joined against the
// discovered root, so every command works from anywhere in the tree.
const (
	scriptsDir    = "platform/scripts"
	clusterScript = scriptsDir + "/cluster.sh"
	destroyScript = scriptsDir + "/destroy-kind.sh"
	versionFile   = "platform/lib/version.sh"
)

// A Module is one link in the bootstrap chain: the step the user sees and the
// bash entry point that performs it.
//
// The step names are not free text. They are the vocabulary the render
// package's golden tests were written against in Phase 8 — the same six strings
// appear in progress.golden — so the transcript a user sees is the transcript
// the suite pins.
type Module struct {
	Step   string // what the user reads while it runs
	Done   string // what the user reads once it has: past tense, not present
	Script string // repo-relative bash entry point
}

// Chain is the bootstrap order: cluster → Istio → monitoring → AI → GitOps →
// security.
//
// Two of those orderings are load-bearing and are not to be shuffled for
// tidiness. AI alert enrichment installs into the monitoring namespace and is
// fed by an Alertmanager route that ships in the monitoring module's values, so
// monitoring goes first. Security registers its ClusterPolicies as an ArgoCD
// Application, so GitOps goes before it. bootstrap-kind.sh calls the same
// modules in the same order for the same reasons.
var Chain = []Module{
	{"Creating the kind cluster", "Kind cluster ready", clusterScript},
	{"Installing Istio", "Istio installed", "platform/networking/install.sh"},
	{"Installing monitoring", "Monitoring installed", "platform/monitoring/install.sh"},
	{"Installing AI alert enrichment", "AI alert enrichment installed", "platform/ai/install.sh"},
	{"Installing ArgoCD", "ArgoCD installed", "platform/gitops/install.sh"},
	{"Installing Kyverno policies", "Kyverno policies installed", "platform/security/install.sh"},
}

// FindRoot returns the platform repo root.
//
// If start names a directory that already looks like the root it is used as-is;
// otherwise the search walks upwards, so `endurance bootstrap` works from
// anywhere inside the tree — including from cli/, which is where anyone who has
// just built the binary is standing.
func FindRoot(start string) (string, error) {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		if isRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"no Endurance platform repo found at or above %s (looked for %s) — "+
					"run this from the platform repo, or pass --root", abs, clusterScript)
		}
		dir = parent
	}
}

// isRoot reports whether dir holds the platform tree. The marker is the module
// plumbing itself rather than a .git directory: the platform is one directory
// inside its repo today and will be its own repo after the Phase 13 split, and
// this check should not care which.
func isRoot(dir string) bool {
	for _, marker := range []string{clusterScript, versionFile} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(marker))); err != nil {
			return false
		}
	}
	return true
}

// A runFunc executes one module and reports what its output contained. Tests
// replace it; nothing else does.
type runFunc func(step *render.LiveStep, root, script string) (moduleOutput, error)

// moduleOutput is what a module said while it ran.
//
// warnings is not decoration. Phase 8 gave the bash logger a `warning:` prefix
// precisely so the Go side could promote those lines, and this is the promotion:
// a module that warned finishes ⚠ rather than ✓, so a bootstrap that quietly
// skipped the AI Secret does not read as a clean run.
type moduleOutput struct {
	lines    int
	warnings int
}

// runScript runs one bash module as a subprocess, framed by step.
//
// Both streams go to one detail channel so the transcript keeps the module's
// own ordering — os/exec gives a single pipe to a Stdout and Stderr that are
// the same writer, where two would interleave by scheduler luck, and a warning
// that lands three lines from the command it is about is a warning about
// nothing.
//
// The named results are load-bearing: the line count is only final once the
// stream has been flushed, which happens in the deferred close below.
func runScript(step *render.LiveStep, root, script string) (out moduleOutput, err error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return out, fmt.Errorf("bash is required to run the platform modules " +
			"(git-bash on Windows) and was not found on PATH")
	}
	path := filepath.Join(root, filepath.FromSlash(script))
	if _, err := os.Stat(path); err != nil {
		return out, fmt.Errorf("module %s not found under %s", script, root)
	}

	stream := step.StreamWith(func(line string) bool {
		if strings.HasPrefix(line, warningPrefix) {
			out.warnings++
		}
		return true // nothing a module says is hidden; the CLI only counts
	})
	defer func() {
		stream.Close()
		out.lines = stream.Lines()
	}()

	cmd := exec.Command(bash, script)
	cmd.Dir = root
	// The framing flag, plus whatever the operator's environment already says:
	// the modules read KUBECONFIG, HELM_* and the AI module's credentials from
	// it, and inheriting is the only way `endurance bootstrap` behaves the same
	// as running the script by hand.
	cmd.Env = append(os.Environ(), EnvFramed+"=1")
	cmd.Stdout, cmd.Stderr = stream, stream
	// No stdin: a module that stops to ask a question would hang behind a
	// spinner with nothing on screen to explain why. They are all
	// non-interactive (`istioctl install -y`, `helm upgrade --install`), and if
	// one ever is not, EOF fails it loudly instead of silently.
	cmd.Stdin = nil

	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("%s: %w", script, err)
	}
	return out, nil
}

// runModule runs one module under its own step and resolves the step.
//
// A step announces itself in the present tense and resolves in the past —
// "Installing Istio" while it runs, "✓ Istio installed" once it has. A failure
// keeps the running label: "Istio installed — failed after 44s" would be a
// sentence arguing with itself.
func runModule(run runFunc, p *render.Progress, root string, m Module) error {
	step := p.Start(m.Step)
	res, err := run(step, root, m.Script)
	if err != nil {
		return step.Fail(err)
	}
	step.Rename(m.Done)
	if res.warnings > 0 {
		step.Warn(plural(res.warnings, "warning") + " from the module")
		return nil
	}
	step.Done()
	return nil
}

// shortPath shortens a filesystem path for a boxed summary.
//
// The box grows to fit its content, by design — a stubby box beside a
// full-width rule reads as a rendering bug. The consequence is that a caller
// who drops a 130-character Windows path into a row gets a 130-character box,
// which is a bug in the caller. Home becomes ~, and anything still too long
// keeps its last two segments.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = "~" + string(filepath.Separator) + rel
		}
	}
	const max = 44
	if len(p) <= max {
		return p
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) < 2 {
		return p
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
