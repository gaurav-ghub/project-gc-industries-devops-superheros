package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/spec"
)

func canaryApp() spec.App {
	app := sampleApp()
	app.Mesh = spec.Mesh{Enabled: true}
	i := app.FindService("catalog")
	app.Services[i].Replicas = 0
	app.Services[i].Versions = []spec.Version{
		{Name: "v1", Tag: "v1-a", Weight: 40, Replicas: 1},
		{Name: "v2", Tag: "v2-a", Weight: 30, Replicas: 1},
		{Name: "v3", Tag: "v3-a", Weight: 30, Replicas: 1},
	}
	return app
}

func seedCanary(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := Generate(root, canaryApp(), "https://example.com/platform.git", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return root
}

// Releasing to a canary service without naming a version must fail rather than
// guess. "All three" is a plausible reading, and moving three versions at once
// is not something a developer should get by omission.
func TestReleaseToCanaryRequiresAVersion(t *testing.T) {
	root := seedCanary(t)
	_, err := PlanServiceTag(root, "superheros", "catalog", "", "v9")
	if err == nil {
		t.Fatal("a versionless release to a canary service was accepted")
	}
	for _, want := range []string{"v1", "v2", "v3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not list version %q", err, want)
		}
	}
}

// And the converse: --version on a service that has none is a mistake worth
// naming, not a flag to quietly ignore.
func TestReleaseRejectsVersionOnPlainService(t *testing.T) {
	root := seedCanary(t)
	if _, err := PlanServiceTag(root, "superheros", "frontend", "v1", "v9"); err == nil {
		t.Fatal("--version was accepted for a service with no versions")
	}
}

func TestReleaseMovesOnlyTheNamedVersion(t *testing.T) {
	root := seedCanary(t)
	bump, err := SetServiceTag(root, "superheros", "catalog", "v2", "v2-b")
	if err != nil {
		t.Fatalf("SetServiceTag: %v", err)
	}
	if bump.OldTag != "v2-a" || bump.NewTag != "v2-b" {
		t.Errorf("bump = %s → %s, want v2-a → v2-b", bump.OldTag, bump.NewTag)
	}
	if bump.Target() != "catalog/v2" {
		t.Errorf("Target() = %q, want catalog/v2", bump.Target())
	}

	app, err := Load(root, "superheros")
	if err != nil {
		t.Fatal(err)
	}
	s := app.Services[app.FindService("catalog")]
	want := map[string]string{"v1": "v1-a", "v2": "v2-b", "v3": "v3-a"}
	for _, v := range s.Versions {
		if v.Tag != want[v.Name] {
			t.Errorf("version %s tag = %q, want %q — a release must not disturb sibling versions",
				v.Name, v.Tag, want[v.Name])
		}
		// Weights are traffic, not image identity. A release must never move them.
		if v.Name == "v1" && v.Weight != 40 {
			t.Errorf("release changed v1's weight to %d", v.Weight)
		}
	}
}

func TestUnknownVersionIsRejected(t *testing.T) {
	root := seedCanary(t)
	if _, err := PlanServiceTag(root, "superheros", "catalog", "v9", "t"); err == nil {
		t.Fatal("an unknown version was accepted")
	}
}

// The claim the whole feature rests on: shifting traffic changes the weights and
// nothing else. If a weight change altered any part of a pod spec, ArgoCD would
// roll the pods and a "safe" traffic shift would become an outage risk.
func TestWeightShiftTouchesNoPodSpec(t *testing.T) {
	root := seedCanary(t)
	valuesPath := filepath.Join(root, "apps", "superheros", "values.yaml")
	before, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}

	change, err := PlanWeights(root, "superheros", "catalog", map[string]int{"v1": 10, "v2": 10, "v3": 80})
	if err != nil {
		t.Fatal(err)
	}
	if change, err = WriteWeights(root, change); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(valuesPath)
	if err != nil {
		t.Fatal(err)
	}

	// Compare line by line: every difference must be a weight line.
	beforeLines := strings.Split(strings.ReplaceAll(string(before), "\r\n", "\n"), "\n")
	afterLines := strings.Split(strings.ReplaceAll(string(after), "\r\n", "\n"), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("a weight shift changed the shape of values.yaml (%d → %d lines)",
			len(beforeLines), len(afterLines))
	}
	changed := 0
	for i := range beforeLines {
		if beforeLines[i] == afterLines[i] {
			continue
		}
		changed++
		if !strings.Contains(afterLines[i], "weight:") {
			t.Errorf("a weight shift changed a non-weight line: %q → %q", beforeLines[i], afterLines[i])
		}
	}
	if changed != 3 {
		t.Errorf("changed %d lines, want 3 (one weight per version)", changed)
	}

	// And, as for a release, placement is untouched.
	if len(change.Written) != 2 {
		t.Errorf("wrote %d files, want 2 (app.yaml + values.yaml): %v", len(change.Written), change.Written)
	}
}

func TestWeightShiftIsNoOpWhenAlreadySplitThatWay(t *testing.T) {
	root := seedCanary(t)
	change, err := PlanWeights(root, "superheros", "catalog", map[string]int{"v1": 40, "v2": 30, "v3": 30})
	if err != nil {
		t.Fatal(err)
	}
	if !change.NoOp {
		t.Error("re-applying the current split should report NoOp")
	}
	if change, _ = WriteWeights(root, change); len(change.Written) != 0 {
		t.Errorf("NoOp shift wrote files: %v", change.Written)
	}
}

func TestWeightShiftReportsEveryVersionNotOnlyTheMoved(t *testing.T) {
	root := seedCanary(t)
	change, err := PlanWeights(root, "superheros", "catalog", map[string]int{"v1": 40, "v2": 0, "v3": 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Deltas) != 3 {
		t.Fatalf("reported %d deltas, want 3 — a split is only meaningful as a whole", len(change.Deltas))
	}
	if len(change.Changed()) != 2 {
		t.Errorf("Changed() = %v, want the two that moved", change.Changed())
	}
}

// Enabling the mesh has to reach the namespace, and the namespace is ArgoCD's to
// create — so the label belongs in the Application, not in a remembered command.
func TestApplicationCarriesInjectionLabelOnlyWithMesh(t *testing.T) {
	root := seedCanary(t)
	data, err := os.ReadFile(filepath.Join(root, "apps", "superheros", "application.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"managedNamespaceMetadata", "istio-injection: enabled"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("application.yaml is missing %q", want)
		}
	}

	plain := t.TempDir()
	if _, err := Generate(plain, sampleApp(), "r", ""); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(plain, "apps", "superheros", "application.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "istio-injection") {
		t.Error("a mesh-less application asked for sidecar injection")
	}
}

// Adopting canary for one service must not disturb the others' rendered values,
// or "only what I changed rolls" stops being true the moment anyone tries it.
func TestAdoptingCanaryLeavesOtherServicesByteIdentical(t *testing.T) {
	plain := t.TempDir()
	if _, err := Generate(plain, sampleApp(), "r", ""); err != nil {
		t.Fatal(err)
	}
	canary := t.TempDir()
	if _, err := Generate(canary, canaryApp(), "r", ""); err != nil {
		t.Fatal(err)
	}

	frontendBlock := func(root string) string {
		data, err := os.ReadFile(filepath.Join(root, "apps", "superheros", "values.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		start := strings.Index(text, "- name: frontend")
		end := strings.Index(text[start:], "- name: catalog")
		return text[start : start+end]
	}
	if frontendBlock(plain) != frontendBlock(canary) {
		t.Errorf("frontend's values changed when catalog adopted canary\nplain:\n%s\ncanary:\n%s",
			frontendBlock(plain), frontendBlock(canary))
	}
}
