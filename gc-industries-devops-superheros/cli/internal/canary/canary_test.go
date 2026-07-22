package canary

import (
	"testing"

	"github.com/gc-ghub/launchpad/internal/spec"
)

func TestParseWeights(t *testing.T) {
	got, err := ParseWeights("v1=90, v2=10")
	if err != nil {
		t.Fatalf("ParseWeights: %v", err)
	}
	if got["v1"] != 90 || got["v2"] != 10 || len(got) != 2 {
		t.Errorf("ParseWeights = %v", got)
	}

	for _, bad := range []string{"", "v1", "v1=abc", "v1=50,v1=50"} {
		if _, err := ParseWeights(bad); err == nil {
			t.Errorf("ParseWeights(%q) was accepted", bad)
		}
	}
}

// Promotion is expressed as an ordinary complete weight set rather than as a
// special case, so `promote` and `set` write the same thing and the registry
// only ever describes traffic one way.
func TestPromoteWeightsZeroesEveryOtherVersion(t *testing.T) {
	s := spec.Service{Name: "catalog", Versions: []spec.Version{
		{Name: "v1", Weight: 40}, {Name: "v2", Weight: 30}, {Name: "v3", Weight: 30},
	}}
	got, err := PromoteWeights(s, "v3")
	if err != nil {
		t.Fatal(err)
	}
	if got["v1"] != 0 || got["v2"] != 0 || got["v3"] != 100 {
		t.Errorf("PromoteWeights = %v, want v3=100 and the rest 0", got)
	}
	total := 0
	for _, w := range got {
		total += w
	}
	if total != 100 {
		t.Errorf("promotion weights sum to %d, want 100", total)
	}

	if _, err := PromoteWeights(s, "v9"); err == nil {
		t.Error("promoting to an unknown version was accepted")
	}
}
