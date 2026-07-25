package platform

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// capture points the process-wide renderer at a buffer, with colour and TTY
// behaviour off, and restores it afterwards.
//
// The look itself is pinned by the golden files in internal/render. What these
// tests assert is what the *commands* say — the order of the steps, which glyph
// a module earned, what a failure leaves behind — so they match on substrings
// and never on the whole frame. Elapsed times are real here, which is the other
// reason not to golden-compare.
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// repoRoot is the real platform repo this CLI lives in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if !isRoot(root) {
		t.Skipf("platform tree not found at %s", root)
	}
	return root
}

func TestFindRootWalksUp(t *testing.T) {
	root := repoRoot(t)

	// From the repo root, from a directory inside it, and from a directory
	// several levels down: all the same answer. Anyone who has just run
	// `go build` is standing in cli/.
	for _, start := range []string{".", "..", filepath.Join(root, "cli", "internal", "platform")} {
		got, err := FindRoot(start)
		if err != nil {
			t.Fatalf("FindRoot(%q): %v", start, err)
		}
		if got != root {
			t.Errorf("FindRoot(%q) = %q, want %q", start, got, root)
		}
	}
}

func TestFindRootFailsOutsideThePlatform(t *testing.T) {
	dir := t.TempDir()
	_, err := FindRoot(dir)
	if err == nil {
		t.Fatal("an empty directory was accepted as a platform repo")
	}
	// The error has to name the way out, not just the problem.
	if !strings.Contains(err.Error(), "--root") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

// TestChainMatchesTheBashModules keeps the CLI's idea of the platform and the
// platform itself from drifting: every script the chain names must exist, and
// bootstrap-kind.sh must still install the same modules in the same order.
//
// The two are separate code paths on purpose — the operator's fallback and the
// CLI's front door — and separate code paths are exactly what quietly disagree.
func TestChainMatchesTheBashModules(t *testing.T) {
	root := repoRoot(t)

	for _, m := range Chain {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(m.Script))); err != nil {
			t.Errorf("chain step %q names a module that does not exist: %v", m.Step, err)
		}
	}

	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(scriptsDir+"/bootstrap-kind.sh")))
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	calls := []string{
		"bootstrap_cluster",
		"install_networking",
		"install_monitoring",
		"install_ai",
		"install_gitops",
		"install_security",
		"install_access",
	}
	last := -1
	for _, call := range calls {
		i := strings.Index(script, "\n    "+call)
		if i < 0 {
			t.Errorf("bootstrap-kind.sh no longer calls %s — the chain and the script have diverged", call)
			continue
		}
		if i < last {
			t.Errorf("%s is called out of the chain's order in bootstrap-kind.sh", call)
		}
		last = i
	}
}

// TestEveryModuleStandsAlone is the assumption this whole phase rests on.
//
// bootstrap-kind.sh sources the six modules into one shell, so a module that
// never sourced utils.sh still had log_info — the parent had already defined
// it. Run as a subprocess, that module dies on its first line of output. This
// test sources each chain script in a fresh shell and checks the plumbing it
// depends on is there, which is the cheapest possible version of "does running
// it on its own work".
func TestEveryModuleStandsAlone(t *testing.T) {
	bash := requireBash(t)
	root := repoRoot(t)

	for _, m := range Chain {
		t.Run(m.Script, func(t *testing.T) {
			cmd := exec.Command(bash, "-c",
				`source "$1" >/dev/null 2>&1; type -t log_info; type -t print_banner`, "bash", m.Script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), EnvFramed+"=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("sourcing %s standalone failed: %v\n%s", m.Script, err, out)
			}
			if got := strings.Fields(string(out)); len(got) != 2 || got[0] != "function" || got[1] != "function" {
				t.Errorf("%s does not reach platform/lib on its own (got %q) — "+
					"it would die on its first log_info as a subprocess", m.Script, out)
			}
		})
	}
}

// TestModuleSummariesAreSilentWhenFramed.
//
// Each module ends by displaying what it installed — URLs, admin passwords, pod
// tables, "Status : READY ✅", "🎉 Welcome Onboard!". Fine when an operator runs
// that module by hand. In a framed bootstrap it was three separate problems:
// a third visual system after the one Phase 8 removed; a claim that the
// platform was ready, printed by ArgoCD while two modules were still pending;
// and live credentials in a scrollback that gets screenshotted.
//
// They are quiet under ENDURANCE_FRAMED now, and Endurance prints the access
// details once, at the end. This test runs the real functions and fails if any
// of that comes back.
func TestModuleSummariesAreSilentWhenFramed(t *testing.T) {
	bash := requireBash(t)
	root := repoRoot(t)

	summaries := []struct{ file, fn string }{
		{"platform/security/verify.sh", "display_security_summary"},
		{"platform/ai/verify.sh", "display_ai_summary"},
		{"platform/gitops/argocd/install.sh", "display_gitops_summary"},
		{"platform/gitops/argocd/install.sh", "display_argocd_information"},
		{"platform/monitoring/prometheus/install.sh", "display_monitoring_information"},
		{"platform/security/kyverno/install.sh", "display_kyverno_policies"},
		// The sharpest case of the rule: this one prints the platform's URLs,
		// and a module printing URLs mid-run is exactly what Phase 9's live
		// bootstrap exposed. Endurance prints one Access block, at the end, and
		// probes it first.
		{"platform/access/verify.sh", "display_access_summary"},
	}

	for _, s := range summaries {
		t.Run(s.fn, func(t *testing.T) {
			cmd := exec.Command(bash, "-c",
				`source platform/scripts/utils.sh; source "$1"; type -t "$2" >/dev/null || exit 3; "$2"`,
				"bash", s.file, s.fn)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), EnvFramed+"=1")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s: %v\n%s", s.fn, err, out)
			}
			if len(bytes.TrimSpace(out)) != 0 {
				t.Errorf("%s speaks in a framed run — Endurance is drawing here:\n%s", s.fn, out)
			}
		})
	}
}

// TestRunScriptFramesASubprocess is this phase's exit criterion as a test, the
// way the bash bridge was Phase 8's: run a real subprocess through the real
// runner and prove the three things the framing depends on.
func TestRunScriptFramesASubprocess(t *testing.T) {
	bash := requireBash(t)
	dir := t.TempDir()
	write(t, dir, "module.sh", `#!/usr/bin/env bash
echo "framed=${ENDURANCE_FRAMED:-unset}"
echo "cwd-has-module=$(test -f module.sh && echo yes || echo no)"
echo "warning: no secret.yaml — enrichment will be un-enriched" >&2
echo "done"
`)
	_ = bash

	buf := capture(t)
	step := render.StartStep("Installing something")
	out, err := runScript(step, dir, "module.sh")
	if err != nil {
		t.Fatalf("running the module: %v", err)
	}
	step.Done()
	got := buf.String()

	// 1. The framing flag reaches the child — this is the whole contract with
	//    platform/lib/*, and if it is lost the modules draw their own banners.
	if !strings.Contains(got, "framed=1") {
		t.Errorf("%s did not reach the subprocess:\n%s", EnvFramed, got)
	}
	// 2. The module runs in the platform repo, so its relative paths resolve.
	if !strings.Contains(got, "cwd-has-module=yes") {
		t.Errorf("the subprocess did not run in the repo root:\n%s", got)
	}
	// 3. Its output arrived on the detail channel, indented under the step.
	if !strings.Contains(got, "  warning: no secret.yaml") {
		t.Errorf("stderr did not arrive on the detail channel:\n%s", got)
	}
	if out.warnings != 1 {
		t.Errorf("warnings = %d, want 1 — the `warning:` prefix is how bash raises its voice", out.warnings)
	}
	if out.lines != 4 {
		t.Errorf("lines = %d, want 4", out.lines)
	}
}

// TestRunScriptReportsAFailingModule — a module that exits non-zero must fail
// its step with the script named, and must not swallow what it printed on the
// way down.
func TestRunScriptReportsAFailingModule(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	write(t, dir, "module.sh", `#!/usr/bin/env bash
echo "error: helm upgrade failed" >&2
exit 1
`)

	buf := capture(t)
	step := render.StartStep("Installing ArgoCD")
	_, err := runScript(step, dir, "module.sh")
	if err == nil {
		t.Fatal("a module that exited 1 was reported as a success")
	}
	if !strings.Contains(err.Error(), "module.sh") {
		t.Errorf("the error does not name the module: %v", err)
	}
	_ = step.Fail(err)

	got := buf.String()
	if !strings.Contains(got, "  error: helm upgrade failed") {
		t.Errorf("the module's last words were lost:\n%s", got)
	}
	if !strings.Contains(got, "✗ Installing ArgoCD") {
		t.Errorf("the step did not resolve as failed:\n%s", got)
	}
}

// TestRunScriptRejectsAMissingModule — a typo in the chain must be a clear
// error before anything runs, not a shell's "No such file or directory".
func TestRunScriptRejectsAMissingModule(t *testing.T) {
	requireBash(t)
	capture(t)
	step := render.StartStep("Installing nothing")
	_, err := runScript(step, t.TempDir(), "platform/nope/install.sh")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
	step.Done()
}

// TestRunModulePromotesAWarning is the Phase 8 hook in use: a module that
// warned finishes ⚠, not ✓. A bootstrap that skipped the AI credentials should
// not read as a clean run.
func TestRunModulePromotesAWarning(t *testing.T) {
	buf := capture(t)
	p := render.NewProgress("Bootstrapping the platform", "Installing AI alert enrichment")

	err := runModule(func(*render.LiveStep, string, string) (moduleOutput, error) {
		return moduleOutput{lines: 9, warnings: 2}, nil
	}, p, "/repo", Chain[3])
	if err != nil {
		t.Fatal(err)
	}
	p.Finish()

	got := buf.String()
	if !strings.Contains(got, "⚠ "+Chain[3].Done) {
		t.Errorf("a module that warned did not finish with ⚠:\n%s", got)
	}
	if !strings.Contains(got, "2 warnings from the module") {
		t.Errorf("the warning count was not reported:\n%s", got)
	}
	// A warning is still a step that ran: the progress verdict must not call
	// the run incomplete because of it.
	if !strings.Contains(got, "1 step in") {
		t.Errorf("a warned step was not counted as done:\n%s", got)
	}
}

// TestStepsResolveInThePastTense — a step says what it is doing while it runs
// and what it did once it has. A failure keeps the running label, because
// "Istio installed — failed after 44s" is a sentence arguing with itself.
func TestStepsResolveInThePastTense(t *testing.T) {
	for _, m := range Chain {
		if m.Done == "" {
			t.Errorf("%s has no past-tense label", m.Step)
		}
		if m.Done == m.Step {
			t.Errorf("%s resolves with the same words it started with", m.Step)
		}
	}

	t.Run("success renames", func(t *testing.T) {
		buf := capture(t)
		p := render.NewProgress("Bootstrapping the platform", Chain[1].Step)
		if err := runModule(func(*render.LiveStep, string, string) (moduleOutput, error) {
			return moduleOutput{lines: 4}, nil
		}, p, "/repo", Chain[1]); err != nil {
			t.Fatal(err)
		}
		if got := buf.String(); !strings.Contains(got, "✓ Istio installed") {
			t.Errorf("the step did not resolve in the past tense:\n%s", got)
		}
	})

	t.Run("failure keeps the running label", func(t *testing.T) {
		buf := capture(t)
		p := render.NewProgress("Bootstrapping the platform", Chain[1].Step)
		err := runModule(func(*render.LiveStep, string, string) (moduleOutput, error) {
			return moduleOutput{}, errors.New("exit status 1")
		}, p, "/repo", Chain[1])
		if err == nil {
			t.Fatal("the failure was swallowed")
		}
		got := buf.String()
		if strings.Contains(got, "Istio installed") {
			t.Errorf("a failed step claimed Istio was installed:\n%s", got)
		}
		if !strings.Contains(got, "✗ Installing Istio") {
			t.Errorf("the failed step lost its label:\n%s", got)
		}
	})
}

func requireBash(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	return bash
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
