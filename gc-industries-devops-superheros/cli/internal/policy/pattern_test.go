package policy

import "testing"

func TestWildcard(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"?*", "50m", true},
		{"?*", "", false}, // "?" needs at least one character
		{"*", "", true},   // "*" alone accepts empty
		{"*:latest", "nginx:latest", true},
		{"*:latest", "nginx:v1", false},
		{"docker.io/*", "docker.io/dockergc00/superheros-catalog:v3", true},
		{"docker.io/*", "quay.io/dockergc00/superheros-catalog:v3", false},
		{"docker.io/*", "dockergc00/superheros-catalog:v3", false}, // unqualified fails
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
	}
	for _, c := range cases {
		if got := wildcard(c.pattern, c.s); got != c.want {
			t.Errorf("wildcard(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestMatchScalarConditions(t *testing.T) {
	cases := []struct {
		name    string
		pattern any
		value   any
		ok      bool
	}{
		{"required present", "?*", "100m", true},
		{"required empty", "?*", "", false},
		{"negated latest fails", "!*:latest", "nginx:latest", false},
		{"negated latest passes", "!*:latest", "nginx:v1-abc", true},
		{"bool literal", true, true, true},
		{"bool literal mismatch", true, false, false},
		{"replica range in", ">=1 & <=1", 1, true},
		{"replica range over", ">=1 & <=1", 2, false},
		{"replica range under", ">=1 & <=1", 0, false},
		{"disjunction", "1 | 3", 3, true},
		{"disjunction miss", "1 | 3", 2, false},
		{"quantity suffix", ">=64Mi", "128Mi", true},
		{"quantity suffix under", ">=64Mi", "32Mi", false},
		{"millicpu", ">=50m", "100m", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mm, err := match("f", c.pattern, c.value)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := mm == nil; got != c.ok {
				t.Errorf("match(%v, %v) ok=%v, want %v (%v)", c.pattern, c.value, got, c.ok, mm)
			}
		})
	}
}

// A missing key is a violation, not a silent pass — this is the whole basis of
// the require-resources policy.
func TestMatchReportsMissingKeys(t *testing.T) {
	pattern := map[string]any{
		"spec": map[string]any{"containers": []any{
			map[string]any{"resources": map[string]any{
				"limits": map[string]any{"cpu": "?*"},
			}},
		}},
	}
	value := map[string]any{
		"spec": map[string]any{"containers": []any{
			map[string]any{"name": "c"},
		}},
	}
	mm, err := match("", pattern, value)
	if err != nil {
		t.Fatal(err)
	}
	if mm == nil {
		t.Fatal("expected a violation for a container with no resources")
	}
	if mm.path != "spec.containers[0].resources" {
		t.Errorf("path = %q, want spec.containers[0].resources", mm.path)
	}
}

// Every element of a list must satisfy the pattern. Kyverno's documented default
// is "at least one", which would let a single compliant sidecar vouch for a
// non-compliant app container; Endurance takes the stricter reading on purpose.
func TestMatchListRequiresEveryElement(t *testing.T) {
	pattern := []any{map[string]any{"image": "!*:latest"}}
	value := []any{
		map[string]any{"image": "app:v1"},
		map[string]any{"image": "sidecar:latest"},
	}
	mm, err := match("containers", pattern, value)
	if err != nil {
		t.Fatal(err)
	}
	if mm == nil {
		t.Fatal("a non-compliant second element must fail the list")
	}
}

// An anchor the matcher does not implement must be reported, never assumed to
// pass — evidence of compliance that was never checked is worse than none.
func TestUnsupportedAnchorIsReportedNotPassed(t *testing.T) {
	pattern := map[string]any{"(name)": "x"}
	_, err := match("", pattern, map[string]any{"name": "y"})
	if err == nil {
		t.Fatal("expected an unsupported-construct error for a conditional anchor")
	}
	if _, ok := err.(errUnsupported); !ok {
		t.Errorf("err = %T, want errUnsupported", err)
	}
}
