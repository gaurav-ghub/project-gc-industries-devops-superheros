package platform

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// These tests read the real platform files, not fixtures.
//
// That is the point of them. `endurance version` claims to report what the
// platform installs, and the only way that claim can be wrong is if the files
// move and the reader keeps answering confidently from memory. If someone
// renames KYVERNO_CHART_VERSION or restructures versions.yaml, the suite says
// so — the same discipline the policy package uses when it loads the real
// ClusterPolicies rather than reimplementing them.

func TestShellVarReadsThePlatformIdentity(t *testing.T) {
	root := repoRoot(t)

	name, err := shellVar(root, versionFile, "CLUSTER_NAME")
	if err != nil {
		t.Fatalf("reading CLUSTER_NAME: %v", err)
	}
	if name == "" {
		t.Fatal("CLUSTER_NAME is empty")
	}
	if got := ClusterName(root); got != name {
		t.Errorf("ClusterName = %q, want %q", got, name)
	}
}

// TestContextNameExpandsTheClusterReference — platform/lib/version.sh writes
// KUBERNETES_CONTEXT="kind-${CLUSTER_NAME}". Read literally, `endurance status`
// announces it is talking to a cluster called `kind-${CLUSTER_NAME}`, which is
// exactly what it did before this test existed.
func TestContextNameExpandsTheClusterReference(t *testing.T) {
	root := repoRoot(t)
	got := ContextName(root)

	if strings.ContainsAny(got, "${}") {
		t.Fatalf("ContextName = %q — the shell reference was not expanded", got)
	}
	if want := "kind-" + ClusterName(root); got != want {
		t.Errorf("ContextName = %q, want %q", got, want)
	}
}

func TestComponentsReadTheRealVersionFiles(t *testing.T) {
	root := repoRoot(t)
	comps := map[string]Component{}
	for _, c := range Components(root) {
		comps[c.Name] = c
	}

	// Every component whose version lives in a file must have found it.
	for _, name := range []string{"platform", "istio", "kube-prometheus-stack", "ai-alertmanager", "kyverno"} {
		c, ok := comps[name]
		if !ok {
			t.Errorf("no component %q", name)
			continue
		}
		if c.Version == "" {
			t.Errorf("%s: no version read from %s", name, c.Source)
		}
	}
	// The CLI is deliberately not a component: `endurance version` opens with a
	// "This build" section that reports it, and reports whether the binary came
	// from a release, which a row in this list cannot.
	if _, ok := comps["endurance CLI"]; ok {
		t.Error("the CLI is listed as a component the platform installs; " +
			"it is reported by buildCheck, and two rows for one version is how a screen disagrees with itself")
	}
	if c := buildCheck(); !strings.Contains(c.Name, version.Current) {
		t.Errorf("the build section does not name the version: %q", c.Name)
	}

	// istio is read twice by two different code paths (here and by doctor);
	// they must agree, because doctor blocks a bootstrap on that number.
	declared, err := declaredIstioVersion(root)
	if err != nil {
		t.Fatal(err)
	}
	if comps["istio"].Version != declared {
		t.Errorf("istio: version %q but doctor compares against %q",
			comps["istio"].Version, declared)
	}
}

// TestADevBuildDoesNotReportItselfAsARelease.
//
// version.Current is a constant, so a working tree and the release cut from it
// print the same number — correct for the release and a claim the working tree
// has not earned. The stamp is what tells them apart, and this asserts both
// halves of it, including that a `go test` binary is honestly a dev build.
func TestADevBuildDoesNotReportItselfAsARelease(t *testing.T) {
	if version.IsRelease() {
		t.Fatal("this test binary carries a release stamp — the dev case cannot be checked")
	}
	dev := buildCheck()
	if dev.State != render.StatePending {
		t.Errorf("a dev build renders %v; it should be `·` — building from the repo is the "+
			"normal thing to do while working on it, and warning about the normal case is noise",
			dev.State)
	}
	if !strings.Contains(dev.Note, "dev build") {
		t.Errorf("a dev build does not say so: %q", dev.Note)
	}

	// The release case, stamped the way the workflow stamps it.
	t.Cleanup(func() { version.Commit, version.Built = "", "" })
	version.Commit, version.Built = "abc1234", "2026-07-26T00:00:00Z"

	rel := buildCheck()
	if rel.State != render.StateReady {
		t.Errorf("a release build renders %v, want ✓", rel.State)
	}
	if !strings.Contains(rel.Note, "abc1234") || !strings.Contains(rel.Note, "2026-07-26") {
		t.Errorf("a release build does not name the commit and date it came from: %q", rel.Note)
	}
	if strings.Contains(rel.Note, "dev build") {
		t.Errorf("a stamped build still calls itself a dev build: %q", rel.Note)
	}
}

// TestVersionShortIsParsableWithNoRepo — the sibling of
// TestVersionShortIsOneLine below, and the case that is new in Phase 12.
//
// install.sh reads `version --short` to decide whether it is upgrading,
// downgrading or reinstalling, and it reads it from a binary sitting in a temp
// directory on a machine that has never seen this project. So it must answer
// without a repo, in exactly one line, and it must not grow a provenance suffix
// that a `cut` would then have to know about — which is why the dev/release
// distinction lives in the long form only.
func TestVersionShortIsParsableWithNoRepo(t *testing.T) {
	buf := capture(t)
	if err := Version(t.TempDir(), true); err != nil {
		t.Fatalf("version --short outside a repo: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "endurance "+version.Current {
		t.Errorf("version --short = %q, want %q", got, "endurance "+version.Current)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("version --short printed more than one line: %q", got)
	}
}

// TestVersionAnswersOnAMachineWithNoRepo. Since Phase 12 this is the first
// command a stranger runs: `curl … | bash` puts one binary on PATH and nothing
// else. It used to print one apologetic line, because the whole command was
// about the repo.
func TestVersionAnswersOnAMachineWithNoRepo(t *testing.T) {
	buf := capture(t)
	if err := Version(t.TempDir(), false); err != nil {
		t.Fatalf("version outside a repo: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, version.Current) {
		t.Errorf("version outside a repo does not report the version:\n%s", out)
	}
	if !strings.Contains(out, "This build") {
		t.Errorf("version outside a repo skipped the part it can always answer:\n%s", out)
	}
	if !strings.Contains(out, "no platform repo here") {
		t.Errorf("version outside a repo does not say the components are missing:\n%s", out)
	}
}

// TestArgoCDIsReportedAsUnpinned records a real gap rather than papering over
// it: the installer runs a bare `helm upgrade --install argo/argo-cd` with no
// --version, and platform/gitops/versions.yaml is an empty file, so a bootstrap
// gets whatever the Helm repo holds that day. When someone pins it, this test
// is the one that has to change — deliberately.
func TestArgoCDIsReportedAsUnpinned(t *testing.T) {
	root := repoRoot(t)
	for _, c := range Components(root) {
		if c.Name != "argo-cd" {
			continue
		}
		if c.Version != "" {
			t.Fatalf("argo-cd now reports version %q — pin it in the installer and update this test", c.Version)
		}
		if !strings.Contains(c.Note, "not pinned") {
			t.Errorf("the gap is not stated: %q", c.Note)
		}
		return
	}
	t.Error("argo-cd is not among the components")
}

// TestKialiIsPinnedByTheModuleThatInstallsIt.
//
// Kiali spent three phases being declared in two files and installed by
// neither: platform/networking/versions.yaml carried an aspirational number
// behind `installed: false`, platform/monitoring/versions.yaml carried a chart
// entry for a module that did not exist, and `endurance version` correctly
// reported `· not installed`. Phase 10 wrote the module, and the version moved
// to the file that module's installer actually reads.
//
// This test asserts the whole of that: one source, and it is the one bash
// consults. Otherwise the platform is back to documenting a version it does not
// install, which is the mistake platform/networking/versions.yaml has a
// paragraph about.
func TestKialiIsPinnedByTheModuleThatInstallsIt(t *testing.T) {
	root := repoRoot(t)
	for _, c := range Components(root) {
		if c.Name != "kiali" {
			continue
		}
		if c.Optional {
			t.Fatal("kiali is still reported as not installed — platform/access installs it")
		}
		if c.Version == "" {
			t.Fatalf("kiali has no version: %q", c.Note)
		}
		if c.Source != accessVersions {
			t.Errorf("kiali's version is read from %s, not from the module that installs it", c.Source)
		}
		if state := c.check().State; state != render.StateReady {
			t.Errorf("a pinned, installed component did not render as ready (state %v)", state)
		}
		// The version the installer will hand to helm is the version reported
		// here, and the installer reads it with sed rather than a YAML parser —
		// so prove the two readers agree on the real file.
		if got := kialiVersionAsBashReadsIt(t, root); got != c.Version {
			t.Errorf("bash reads kiali version %q, Go reads %q — %s has two readers and one number",
				got, c.Version, accessVersions)
		}
		return
	}
	t.Error("kiali is not among the components")
}

// kialiVersionAsBashReadsIt runs the module's own reader against the real file.
func kialiVersionAsBashReadsIt(t *testing.T, root string) string {
	t.Helper()
	bash := requireBash(t)
	cmd := exec.Command(bash, "-c",
		`source platform/access/install.sh >/dev/null 2>&1; declared_kiali_version`)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), EnvFramed+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("reading the kiali version from bash: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestVersionShortIsOneLine(t *testing.T) {
	buf := capture(t)
	if err := Version(repoRoot(t), true); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "endurance "+version.Current {
		t.Errorf("version --short printed %q, want %q", got, "endurance "+version.Current)
	}
}

func TestVersionListsEveryComponent(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	if err := Version(root, false); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, c := range Components(root) {
		if !strings.Contains(got, c.Name) {
			t.Errorf("%s is missing from the component list:\n%s", c.Name, got)
		}
	}
	if !strings.Contains(got, version.Current) {
		t.Errorf("the CLI version is missing:\n%s", got)
	}
}
