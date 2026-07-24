package policy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/manifest"
	"github.com/gc-ghub/endurance/internal/spec"
)

// realPolicyDir is the repo's own ClusterPolicy directory — the same files the
// kyverno-policies ArgoCD Application syncs into the cluster. The gate is tested
// against the real policies rather than fixtures on purpose: a test that passes
// against a hand-written copy would prove nothing about what the cluster does.
const realPolicyDir = "../../../infra/kyverno_policy"

func loadReal(t *testing.T) []Policy {
	t.Helper()
	policies, err := Load(realPolicyDir)
	if err != nil {
		t.Fatalf("Load(%s): %v", realPolicyDir, err)
	}
	if len(policies) == 0 {
		t.Fatalf("no policies loaded from %s", realPolicyDir)
	}
	return policies
}

// compliant is an app that should pass everything: registry-qualified images,
// real tags, one replica. ApplyDefaults supplies resources and security.
func compliant() spec.App {
	return spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Owner:     "gc-industries",
		Services: []spec.Service{
			{Name: "catalog", Image: "docker.io/dockergc00/superheros-catalog", Tag: "v3-6f11a5d", Port: 8081, Replicas: 1},
			{Name: "frontend", Image: "docker.io/dockergc00/superheros-frontend", Tag: "v1-9c6f729", Port: 80, Replicas: 1},
		},
	}
}

func TestLoadRealPolicies(t *testing.T) {
	policies := loadReal(t)
	byName := map[string]Policy{}
	for _, p := range policies {
		byName[p.Name] = p
	}
	for _, want := range []string{
		"require-resources", "disallow-latest-tag", "generate-resourcequota",
		"add-team-label", "restrict-image-registries", "enforce-security-context",
		"enforce-replica-range",
	} {
		if _, ok := byName[want]; !ok {
			t.Errorf("policy %q was not loaded", want)
		}
	}
	if got := byName["require-resources"].Action; got != Enforce {
		t.Errorf("require-resources action = %q, want Enforce", got)
	}
	// generate-resourcequota sets no validationFailureAction, so it must not be
	// treated as blocking.
	if got := byName["generate-resourcequota"].Action; got != Audit {
		t.Errorf("generate-resourcequota action = %q, want Audit (unset)", got)
	}
}

func TestCompliantAppPasses(t *testing.T) {
	rep := Check(loadReal(t), compliant())
	if n := len(rep.Blocking()); n != 0 {
		t.Fatalf("compliant app produced %d blocking violation(s):\n%s", n, dump(rep))
	}
	if rep.Checked == 0 {
		t.Error("no rules were evaluated — the gate would have passed vacuously")
	}
	if len(rep.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", rep.Warnings)
	}
}

func TestViolationsBlock(t *testing.T) {
	policies := loadReal(t)
	cases := []struct {
		name       string
		mutate     func(*spec.App)
		wantPolicy string
	}{
		{"latest tag", func(a *spec.App) { a.Services[0].Tag = "latest" }, "disallow-latest-tag"},
		{"foreign registry", func(a *spec.App) { a.Services[0].Image = "quay.io/gc/catalog" }, "restrict-image-registries"},
		{"too many replicas", func(a *spec.App) { a.Services[0].Replicas = 3 }, "enforce-replica-range"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := compliant()
			c.mutate(&app)
			rep := Check(policies, app)
			blocking := rep.Blocking()
			if len(blocking) == 0 {
				t.Fatalf("expected a blocking violation\n%s", dump(rep))
			}
			found := false
			for _, v := range blocking {
				if v.Policy == c.wantPolicy {
					found = true
					if v.Message == "" {
						t.Error("violation carries no policy message")
					}
					if v.Detail == "" {
						t.Error("violation does not name the offending field")
					}
				}
			}
			if !found {
				t.Errorf("no violation from %q:\n%s", c.wantPolicy, dump(rep))
			}
		})
	}
}

// The one policy the generated manifests can no longer violate, because
// ApplyDefaults always fills resources in — so it is exercised against a
// hand-built pod instead, to prove the rule itself is wired up and would fire.
func TestRequireResourcesFiresOnBarePod(t *testing.T) {
	bare := manifest.Resource{
		Kind: "Pod", Name: "bare", Namespace: "superheros",
		Object: map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": "bare", "namespace": "superheros"},
			"spec": map[string]any{"containers": []any{
				map[string]any{"name": "c", "image": "docker.io/library/nginx:1.25"},
			}},
		},
	}
	rep := Evaluate(loadReal(t), []manifest.Resource{bare})
	found := false
	for _, v := range rep.Blocking() {
		if v.Policy == "require-resources" {
			found = true
		}
	}
	if !found {
		t.Errorf("require-resources did not fire on a pod with no resources:\n%s", dump(rep))
	}
}

// Mutate and generate rules cannot be judged before admission. They must be
// reported as skipped so nobody reads a clean run as "all seven policies passed".
func TestMutateAndGenerateRulesAreSkippedVisibly(t *testing.T) {
	rep := Check(loadReal(t), compliant())
	skipped := map[string]bool{}
	for _, s := range rep.Skipped {
		skipped[s.Policy] = true
	}
	for _, want := range []string{"generate-resourcequota", "add-team-label"} {
		if !skipped[want] {
			t.Errorf("policy %q should be reported as skipped, got: %+v", want, rep.Skipped)
		}
	}
}

// The repo's policies are all scoped to the superheros namespace. A second
// application would therefore be governed by nothing — the gate has to say so
// rather than report a pass.
func TestUngovernedNamespaceIsWarned(t *testing.T) {
	app := compliant()
	app.Name, app.Namespace = "portfolio", "portfolio"
	rep := Check(loadReal(t), app)
	if rep.Checked != 0 {
		t.Fatalf("expected no rules to match namespace %q, %d were checked", app.Namespace, rep.Checked)
	}
	if len(rep.Blocking()) != 0 {
		t.Error("an unmatched namespace should not produce violations")
	}
	joined := strings.Join(rep.Warnings, " ")
	if !strings.Contains(joined, "ungoverned") {
		t.Errorf("expected an ungoverned-namespace warning, got: %v", rep.Warnings)
	}
}

// An unqualified image runs fine but the registry policy matches the literal
// string, so it fails in the cluster for a reason the manifest does not explain.
func TestUnqualifiedImageIsWarnedAndBlocked(t *testing.T) {
	app := compliant()
	app.Services[0].Image = "dockergc00/superheros-catalog"
	rep := Check(loadReal(t), app)

	joined := strings.Join(rep.Warnings, " ")
	if !strings.Contains(joined, "no registry host") {
		t.Errorf("expected a registry-host warning, got: %v", rep.Warnings)
	}
	if len(rep.Blocking()) == 0 {
		t.Error("restrict-image-registries should reject an unqualified image")
	}
}

func TestLoadMissingDirIsAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error for a missing policy directory")
	}
}

func dump(rep Report) string {
	var b strings.Builder
	for _, v := range rep.Violations {
		b.WriteString("  " + string(v.Action) + " " + v.String() + "\n")
	}
	for _, s := range rep.Skipped {
		b.WriteString("  skipped " + s.Policy + "/" + s.Rule + " — " + s.Reason + "\n")
	}
	for _, w := range rep.Warnings {
		b.WriteString("  warn " + w + "\n")
	}
	return b.String()
}
