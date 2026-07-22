package release

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gc-ghub/launchpad/internal/gitops"
	"github.com/gc-ghub/launchpad/internal/spec"
)

const realPolicyDir = "../../../infra/kyverno_policy"

func seed(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	app := spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Owner:     "gc-industries",
		Services: []spec.Service{
			{Name: "catalog", Image: "docker.io/dockergc00/superheros-catalog", Tag: "v3-6f11a5d", Port: 8081, Replicas: 1},
			{Name: "frontend", Image: "docker.io/dockergc00/superheros-frontend", Tag: "v1-9c6f729", Port: 80, Replicas: 1},
		},
	}
	if _, err := gitops.Generate(root, app, "https://example.com/platform.git", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return root
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	dir := filepath.Join(root, "apps", "superheros")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(data)
	}
	return files
}

func opts(root string) Options {
	return Options{
		Root: root, App: "superheros", Service: "catalog",
		PolicyDir: realPolicyDir,
	}
}

// The Phase 3 guarantee: a release that would violate an enforced policy is
// refused *before* anything reaches the repo. If this ever regresses, the CLI
// would leave a dirty working tree the developer has to notice and revert.
func TestBlockedReleaseWritesNothing(t *testing.T) {
	root := seed(t)
	before := snapshot(t, root)

	o := opts(root)
	o.Tag = "latest" // violates disallow-latest-tag (Enforce)
	if err := Run(o); err == nil {
		t.Fatal("expected the policy gate to reject a `latest` release")
	}

	if after := snapshot(t, root); !equal(before, after) {
		t.Error("a rejected release modified files on disk")
	}
}

func TestCompliantReleaseWrites(t *testing.T) {
	root := seed(t)
	o := opts(root)
	o.Tag = "v4-abc1234"
	if err := Run(o); err != nil {
		t.Fatalf("compliant release was rejected: %v", err)
	}

	app, err := gitops.Load(root, "superheros")
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Services[app.FindService("catalog")].Tag; got != "v4-abc1234" {
		t.Errorf("catalog tag = %q, want v4-abc1234", got)
	}
}

// --dry-run must still run the gate — its whole purpose is to answer "would this
// be accepted?", and a dry run that skips the check answers the wrong question.
func TestDryRunGatesButWritesNothing(t *testing.T) {
	root := seed(t)
	before := snapshot(t, root)

	o := opts(root)
	o.Tag, o.DryRun = "latest", true
	if err := Run(o); err == nil {
		t.Error("--dry-run should still report a policy violation")
	}

	o.Tag = "v4-abc1234"
	if err := Run(o); err != nil {
		t.Fatalf("compliant dry run failed: %v", err)
	}
	if after := snapshot(t, root); !equal(before, after) {
		t.Error("--dry-run wrote files")
	}
}

// The break-glass flag has to actually let a release through, or an operator
// facing a broken policy has no way to ship a fix.
func TestSkipPolicyAllowsAViolation(t *testing.T) {
	root := seed(t)
	o := opts(root)
	o.Tag, o.SkipPolicy = "latest", true
	if err := Run(o); err != nil {
		t.Fatalf("--skip-policy should allow the release: %v", err)
	}
	app, _ := gitops.Load(root, "superheros")
	if got := app.Services[app.FindService("catalog")].Tag; got != "latest" {
		t.Errorf("catalog tag = %q, want latest", got)
	}
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
