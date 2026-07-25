package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

func sampleApp() spec.App {
	return spec.App{
		Name:       "superheros",
		Namespace:  "superheros",
		Repository: "https://github.com/gc-ghub/project-gc-industries-devops-superheros.git",
		Owner:      "gc-industries",
		Services: []spec.Service{
			{Name: "frontend", Image: "dockergc00/superheros-frontend", Tag: "latest", Port: 8080, Replicas: 2},
			{Name: "catalog", Image: "dockergc00/superheros-catalog", Port: 8081, Replicas: 1},
		},
	}
}

func TestGenerateWritesThreeFiles(t *testing.T) {
	root := t.TempDir()
	written, err := Generate(root, sampleApp(), "https://example.com/platform.git", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(written) != 3 {
		t.Fatalf("expected 3 files, got %d: %v", len(written), written)
	}
	for _, f := range []string{"app.yaml", "values.yaml", "application.yaml"} {
		if _, err := os.Stat(filepath.Join(root, "apps", "superheros", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

func TestGenerateDefaultsTag(t *testing.T) {
	root := t.TempDir()
	if _, err := Generate(root, sampleApp(), "r", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "apps", "superheros", "values.yaml"))
	var vals chartValues
	if err := yaml.Unmarshal(data, &vals); err != nil {
		t.Fatalf("values.yaml invalid: %v", err)
	}
	if len(vals.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(vals.Services))
	}
	for _, s := range vals.Services {
		if s.Tag == "" {
			t.Errorf("service %q tag not defaulted", s.Name)
		}
	}
	if vals.App["name"] != "superheros" {
		t.Errorf("app.name = %q, want superheros", vals.App["name"])
	}
}

func TestApplicationIsMultiSource(t *testing.T) {
	root := t.TempDir()
	if _, err := Generate(root, sampleApp(), "https://example.com/platform.git", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "apps", "superheros", "application.yaml"))
	s := string(data)
	for _, want := range []string{
		"kind: Application",
		"path: charts/app",
		"$values/apps/superheros/values.yaml",
		"ref: values",
		"namespace: superheros",
		"CreateNamespace=true",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("application.yaml missing %q\n---\n%s", want, s)
		}
	}
}

// The platform tree can be nested inside the git repo (as it is in
// project-gc-industries-devops-superheros/gc-industries-devops-superheros).
// ArgoCD resolves source paths from the REPO root, so the generated Application
// must carry that prefix — otherwise ArgoCD fails with "app path does not exist".
func TestApplicationPathsAreRepoRelativeWhenNested(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(repoRoot, "gc-industries-devops-superheros")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := RepoPrefix(nested); got != "gc-industries-devops-superheros/" {
		t.Fatalf("RepoPrefix = %q, want %q", got, "gc-industries-devops-superheros/")
	}

	if _, err := Generate(nested, sampleApp(), "https://example.com/platform.git", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(nested, "apps", "superheros", "application.yaml"))
	s := string(data)
	for _, want := range []string{
		"path: gc-industries-devops-superheros/charts/app",
		"$values/gc-industries-devops-superheros/apps/superheros/values.yaml",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("application.yaml missing %q\n---\n%s", want, s)
		}
	}
}

// When root IS the repo root, no prefix should be emitted.
func TestRepoPrefixEmptyAtRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoPrefix(repoRoot); got != "" {
		t.Errorf("RepoPrefix at repo root = %q, want empty", got)
	}
}

func TestGenerateRejectsInvalidApp(t *testing.T) {
	root := t.TempDir()
	bad := sampleApp()
	bad.Services = nil // no services
	if _, err := Generate(root, bad, "r", ""); err == nil {
		t.Fatal("expected validation error for app with no services")
	}
}

func TestListRoundTrip(t *testing.T) {
	root := t.TempDir()
	if _, err := Generate(root, sampleApp(), "r", ""); err != nil {
		t.Fatal(err)
	}
	apps, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "superheros" {
		t.Fatalf("List round-trip failed: %+v", apps)
	}
}

// TestGenerateRefusesAnEmptyRepoURL.
//
// The Application template interpolates the repo URL into two sources. With an
// empty string it renders valid YAML that the API server rejects at apply time
// — long after the files were written and committed, which is the worst moment
// to find out. Refuse while the caller can still be told which knob is missing.
func TestGenerateRefusesAnEmptyRepoURL(t *testing.T) {
	root := t.TempDir()
	app := spec.App{
		Name: "demo", Namespace: "demo", Owner: "team",
		Services: []spec.Service{{
			Name: "web", Image: "docker.io/library/nginx", Tag: "1.27", Port: 8080, Replicas: 1,
		}},
	}
	for _, repo := range []string{"", "   "} {
		written, err := Generate(root, app, repo, "")
		if err == nil {
			t.Fatalf("Generate(%q) wrote %v; want a refusal", repo, written)
		}
		if !strings.Contains(err.Error(), "repo") {
			t.Errorf("the refusal does not name the repo URL: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(AppDir(root, "demo"), "application.yaml")); err == nil {
		t.Error("an Application with no repoURL was written to disk anyway")
	}
}

// TestOriginURLRejectsAPathArgoCDCannotClone — ArgoCD clones over the network
// from its own pod, so a local filesystem remote is not an answer, it is a
// silent failure that only shows up as an unsyncable Application.
func TestOriginURLRejectsAPathArgoCDCannotClone(t *testing.T) {
	if got := OriginURL(t.TempDir()); got != "" {
		t.Errorf("OriginURL on a directory with no repo = %q, want %q", got, "")
	}
}
