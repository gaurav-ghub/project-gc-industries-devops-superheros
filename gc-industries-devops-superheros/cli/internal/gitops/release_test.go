package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// seed writes a registered application to a temp root and returns the root.
func seed(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := Generate(root, sampleApp(), "https://example.com/platform.git", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return root
}

func readValues(t *testing.T, root string) chartValues {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "apps", "superheros", "values.yaml"))
	if err != nil {
		t.Fatalf("read values.yaml: %v", err)
	}
	var v chartValues
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("values.yaml invalid: %v", err)
	}
	return v
}

// The whole point of the per-service release model: exactly one tag moves.
func TestSetServiceTagBumpsOnlyTheNamedService(t *testing.T) {
	root := seed(t)

	bump, err := SetServiceTag(root, "superheros", "catalog", "", "v2-abc1234")
	if err != nil {
		t.Fatalf("SetServiceTag: %v", err)
	}
	if bump.OldTag != "latest" || bump.NewTag != "v2-abc1234" {
		t.Errorf("bump = %s → %s, want latest → v2-abc1234", bump.OldTag, bump.NewTag)
	}
	if bump.NoOp {
		t.Error("bump reported NoOp for a real change")
	}

	vals := readValues(t, root)
	for _, s := range vals.Services {
		switch s.Name {
		case "catalog":
			if s.Tag != "v2-abc1234" {
				t.Errorf("catalog tag = %q, want v2-abc1234", s.Tag)
			}
		case "frontend":
			if s.Tag != "latest" {
				t.Errorf("frontend tag = %q — a release must not touch other services", s.Tag)
			}
		}
	}

	// The registry entry must agree with the values file, or `launchpad list`
	// and ArgoCD would disagree about what is deployed.
	app, err := Load(root, "superheros")
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Services[app.FindService("catalog")].Tag; got != "v2-abc1234" {
		t.Errorf("app.yaml catalog tag = %q, want v2-abc1234", got)
	}
}

// A release changes what image runs, never where or how the app is deployed —
// so application.yaml must come out byte-identical.
func TestSetServiceTagLeavesApplicationYamlUntouched(t *testing.T) {
	root := seed(t)
	path := filepath.Join(root, "apps", "superheros", "application.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := SetServiceTag(root, "superheros", "catalog", "", "v9"); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("application.yaml changed during a release\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestSetServiceTagWritesOnlyTwoFiles(t *testing.T) {
	root := seed(t)
	bump, err := SetServiceTag(root, "superheros", "catalog", "", "v9")
	if err != nil {
		t.Fatal(err)
	}
	if len(bump.Written) != 2 {
		t.Fatalf("wrote %d files, want 2 (app.yaml + values.yaml): %v", len(bump.Written), bump.Written)
	}
	for _, w := range bump.Written {
		if strings.Contains(filepath.Base(w), "application.yaml") {
			t.Errorf("release reported writing %s", w)
		}
	}
}

// Re-releasing the same tag should be a no-op, not an empty commit.
func TestSetServiceTagIsNoOpWhenAlreadyAtTag(t *testing.T) {
	root := seed(t)
	if _, err := SetServiceTag(root, "superheros", "catalog", "", "v2"); err != nil {
		t.Fatal(err)
	}
	bump, err := SetServiceTag(root, "superheros", "catalog", "", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if !bump.NoOp {
		t.Error("re-releasing the same tag should report NoOp")
	}
	if len(bump.Written) != 0 {
		t.Errorf("NoOp release wrote files: %v", bump.Written)
	}
}

func TestSetServiceTagRejectsUnknownService(t *testing.T) {
	root := seed(t)
	_, err := SetServiceTag(root, "superheros", "nosuchsvc", "", "v1")
	if err == nil {
		t.Fatal("expected an error for an unknown service")
	}
	// The message must name the real services — a developer who typos a service
	// name should not have to go read app.yaml to recover.
	for _, want := range []string{"frontend", "catalog"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not list service %q", err, want)
		}
	}
}

func TestSetServiceTagRejectsUnknownApp(t *testing.T) {
	root := seed(t)
	if _, err := SetServiceTag(root, "nosuchapp", "catalog", "", "v1"); err == nil {
		t.Fatal("expected an error for an unknown app")
	}
}

// A malformed tag must be rejected before any file is written, or ArgoCD would
// faithfully reconcile a typo into an ImagePullBackOff.
func TestSetServiceTagRejectsBadTagWithoutWriting(t *testing.T) {
	root := seed(t)
	path := filepath.Join(root, "apps", "superheros", "values.yaml")
	before, _ := os.ReadFile(path)

	for _, bad := range []string{"", "-leading-dash", "has space", "has/slash"} {
		if _, err := SetServiceTag(root, "superheros", "catalog", "", bad); err == nil {
			t.Errorf("tag %q was accepted, want rejection", bad)
		}
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("values.yaml was modified by a rejected release")
	}
}
