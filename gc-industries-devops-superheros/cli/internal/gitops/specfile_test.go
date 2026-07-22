package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/launchpad/internal/spec"
)

// The real specs/superheros.yaml is dense with explanatory comments, which is
// exactly what a YAML round-trip would delete. The fixture keeps them.
const specFixture = `# LaunchPad application spec — the INPUT to onboard --from.
name: superheros
namespace: superheros
mesh:
  enabled: true

services:
  - name: frontend
    image: docker.io/dockergc00/superheros-frontend
    tag: v1-9c6f729
    port: 80
    replicas: 1
  # catalog is the canary.
  - name: catalog
    image: docker.io/dockergc00/superheros-catalog
    port: 8081
    versions:
      - name: v1
        tag: v1-6f11a5d
        weight: 40      # the baseline
        replicas: 1
      - name: v2
        tag: v2-6f11a5d
        weight: 30
        replicas: 1
      - name: v3
        tag: v3-6f11a5d
        weight: 30
        replicas: 1
`

func writeSpec(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "specs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "superheros.yaml")
	if err := os.WriteFile(path, []byte(specFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSyncSpecWeightsRewritesOnlyTheWeights(t *testing.T) {
	root := t.TempDir()
	path := writeSpec(t, root)

	written, err := SyncSpecWeights(root, "superheros", "catalog", map[string]int{"v1": 10, "v2": 10, "v3": 80})
	if err != nil {
		t.Fatalf("SyncSpecWeights: %v", err)
	}
	if written != path {
		t.Fatalf("expected %s to be written, got %q", path, written)
	}

	before := strings.Split(specFixture, "\n")
	data, _ := os.ReadFile(path)
	after := strings.Split(string(data), "\n")
	if len(before) != len(after) {
		t.Fatalf("line count changed: %d → %d", len(before), len(after))
	}
	var changed []string
	for i := range before {
		if before[i] != after[i] {
			changed = append(changed, after[i])
		}
	}
	// Three weights moved, so exactly three lines may differ. This is the same
	// shape of proof Phase 4 used for the rendered manifest.
	if len(changed) != 3 {
		t.Fatalf("expected exactly 3 changed lines, got %d: %v", len(changed), changed)
	}
	for _, line := range changed {
		if !strings.Contains(line, "weight:") {
			t.Errorf("a non-weight line changed: %q", line)
		}
	}
}

func TestSyncSpecWeightsKeepsCommentsAndInlineComments(t *testing.T) {
	root := t.TempDir()
	path := writeSpec(t, root)
	if _, err := SyncSpecWeights(root, "superheros", "catalog", map[string]int{"v1": 0, "v2": 0, "v3": 100}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	if !strings.Contains(got, "# LaunchPad application spec") {
		t.Error("the file header comment was lost")
	}
	if !strings.Contains(got, "# catalog is the canary.") {
		t.Error("a block comment was lost")
	}
	if !strings.Contains(got, "weight: 0      # the baseline") {
		t.Errorf("the inline comment on the edited line was lost:\n%s", got)
	}
}

func TestSyncSpecWeightsIsANoOpWhenAlreadyInAgreement(t *testing.T) {
	root := t.TempDir()
	path := writeSpec(t, root)
	before, _ := os.ReadFile(path)
	written, err := SyncSpecWeights(root, "superheros", "catalog", map[string]int{"v1": 40, "v2": 30, "v3": 30})
	if err != nil {
		t.Fatal(err)
	}
	if written != "" {
		t.Errorf("nothing changed, so nothing should be reported written; got %q", written)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the file was rewritten despite no change")
	}
}

func TestSyncSpecWeightsToleratesAMissingSpecFile(t *testing.T) {
	// An application onboarded through the interactive form has no specs/ file,
	// and that is allowed — it must not turn a successful shift into an error.
	root := t.TempDir()
	written, err := SyncSpecWeights(root, "superheros", "catalog", map[string]int{"v1": 100})
	if err != nil {
		t.Fatalf("a missing spec file is not an error: %v", err)
	}
	if written != "" {
		t.Errorf("nothing to write, got %q", written)
	}
}

func TestSyncSpecWeightsIgnoresAServiceTheSpecDoesNotDescribe(t *testing.T) {
	root := t.TempDir()
	if _, err := SyncSpecWeights(root, "superheros", "ghost", map[string]int{"v1": 100}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writeSpec(t, root)
	if _, err := SyncSpecWeights(root, "superheros", "ghost", map[string]int{"v1": 100}); err != nil {
		t.Fatalf("a service the spec does not mention is not an error: %v", err)
	}
}

func TestSyncSpecWeightsRefusesANonCanaryService(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root)
	if _, err := SyncSpecWeights(root, "superheros", "frontend", map[string]int{"v1": 100}); err == nil {
		t.Fatal("expected an error for a service with no versions")
	}
}

func TestSyncSpecWeightsTargetsOneServiceOnly(t *testing.T) {
	// A regex over the whole file would happily rewrite another service's
	// weights. The line numbers come from the parsed document for this reason.
	root := t.TempDir()
	path := writeSpec(t, root)
	two := strings.Replace(specFixture, "  - name: frontend", `  - name: orders
    image: docker.io/dockergc00/superheros-orders
    port: 8082
    versions:
      - name: v1
        tag: v1-df408cf
        weight: 100
        replicas: 1
  - name: frontend`, 1)
	if err := os.WriteFile(path, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncSpecWeights(root, "superheros", "catalog", map[string]int{"v1": 0, "v2": 0, "v3": 100}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "weight: 100\n        replicas: 1\n  - name: frontend") {
		t.Errorf("orders' weight should be untouched:\n%s", data)
	}
}

func TestWeightDriftNamesTheSplitThatWouldBeReset(t *testing.T) {
	// The Phase 4 finding, turned into a warning: apps/ says 10/10/80 while
	// specs/ still says 40/30/30, and onboarding is about to regenerate.
	root := t.TempDir()
	deployed := canarySpecApp()
	if _, err := Generate(root, deployed, "r", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWeightsOnDisk(root, "superheros", "catalog", map[string]int{"v1": 10, "v2": 10, "v3": 80}); err != nil {
		t.Fatal(err)
	}

	drift := WeightDrift(root, deployed)
	if len(drift) != 1 {
		t.Fatalf("expected one drift warning, got %v", drift)
	}
	for _, want := range []string{"catalog", "10%→40%", "80%→30%"} {
		if !strings.Contains(drift[0], want) {
			t.Errorf("warning should contain %q, got: %s", want, drift[0])
		}
	}
}

func TestWeightDriftIsSilentWhenTheyAgree(t *testing.T) {
	root := t.TempDir()
	app := canarySpecApp()
	if _, err := Generate(root, app, "r", ""); err != nil {
		t.Fatal(err)
	}
	if drift := WeightDrift(root, app); len(drift) != 0 {
		t.Errorf("expected no drift, got %v", drift)
	}
}

func TestWeightDriftIsSilentForAnUnregisteredApp(t *testing.T) {
	if drift := WeightDrift(t.TempDir(), canarySpecApp()); len(drift) != 0 {
		t.Errorf("nothing to disagree with yet, got %v", drift)
	}
}

// SetWeightsOnDisk is the plan+write pair used by `canary set`, wrapped for the
// tests above.
func SetWeightsOnDisk(root, app, svc string, weights map[string]int) (WeightChange, error) {
	c, err := PlanWeights(root, app, svc, weights)
	if err != nil {
		return c, err
	}
	return WriteWeights(root, c)
}

func canarySpecApp() spec.App {
	return spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Mesh:      spec.Mesh{Enabled: true},
		Services: []spec.Service{
			{Name: "frontend", Image: "docker.io/dockergc00/superheros-frontend", Tag: "v1", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/dockergc00/superheros-catalog", Port: 8081, Versions: []spec.Version{
				{Name: "v1", Tag: "v1-a", Weight: 40, Replicas: 1},
				{Name: "v2", Tag: "v2-a", Weight: 30, Replicas: 1},
				{Name: "v3", Tag: "v3-a", Weight: 30, Replicas: 1},
			}},
		},
	}
}
