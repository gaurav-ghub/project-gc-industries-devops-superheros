package spec

import (
	"strings"
	"testing"
)

func canaryApp() App {
	return App{
		Name:      "superheros",
		Namespace: "superheros",
		Mesh:      Mesh{Enabled: true},
		Services: []Service{
			{Name: "frontend", Image: "docker.io/x/frontend", Tag: "v1", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/x/catalog", Port: 8081, Versions: []Version{
				{Name: "v1", Tag: "v1-a", Weight: 40, Replicas: 1},
				{Name: "v2", Tag: "v2-a", Weight: 30, Replicas: 1},
				{Name: "v3", Tag: "v3-a", Weight: 30, Replicas: 1},
			}},
		},
	}
}

func TestCanaryAppIsValid(t *testing.T) {
	if err := canaryApp().Validate(); err != nil {
		t.Fatalf("valid canary app rejected: %v", err)
	}
}

// Istio requires a route's weights to sum to 100. Catching it here means the
// developer hears it from their own terminal rather than from a VirtualService
// that silently never takes effect.
func TestWeightsMustSumTo100(t *testing.T) {
	app := canaryApp()
	app.Services[1].Versions[0].Weight = 41
	err := app.Validate()
	if err == nil {
		t.Fatal("weights summing to 101 were accepted")
	}
	// The message must show the actual split, or the developer has to add the
	// numbers up by hand to find which one is wrong.
	for _, want := range []string{"101", "v1=41", "v2=30", "v3=30"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// A registry entry that carries both a top-level tag and versions gives two
// different answers to "what is running", and only one of them is true.
func TestCanaryServiceRejectsTopLevelTagAndReplicas(t *testing.T) {
	app := canaryApp()
	app.Services[1].Tag = "v9"
	if err := app.Validate(); err == nil {
		t.Error("a canary service with a top-level tag was accepted")
	}

	app = canaryApp()
	app.Services[1].Replicas = 2
	if err := app.Validate(); err == nil {
		t.Error("a canary service with top-level replicas was accepted")
	}
}

func TestCanaryVersionNamesMustBeLabelSafeAndUnique(t *testing.T) {
	app := canaryApp()
	app.Services[1].Versions[0].Name = "V1" // becomes an Istio subset + a Deployment suffix
	if err := app.Validate(); err == nil {
		t.Error("an upper-case version name was accepted")
	}

	app = canaryApp()
	app.Services[1].Versions[1].Name = "v1"
	if err := app.Validate(); err == nil {
		t.Error("a duplicate version name was accepted")
	}
}

// ApplyDefaults must clear the service-level tag and replicas for a canary, so
// the registry entry never claims a tag nothing is running.
func TestApplyDefaultsClearsServiceLevelTagForCanary(t *testing.T) {
	app := canaryApp()
	app.Services[1].Tag, app.Services[1].Replicas = "", 0
	app.ApplyDefaults()

	s := app.Services[1]
	if s.Tag != "" || s.Replicas != 0 {
		t.Errorf("canary service defaulted to tag=%q replicas=%d, want both empty", s.Tag, s.Replicas)
	}
	for _, v := range s.Versions {
		if v.Replicas != 1 {
			t.Errorf("version %s replicas = %d, want 1", v.Name, v.Replicas)
		}
	}
	// A plain service must still be defaulted exactly as before.
	if app.Services[0].Tag != "v1" || app.Services[0].Replicas != 1 {
		t.Errorf("plain service was disturbed: %+v", app.Services[0])
	}
}

func TestWorkloadNames(t *testing.T) {
	app := canaryApp()
	plain, canary := app.Services[0], app.Services[1]

	if got := plain.WorkloadName(plain.Rollout()[0]); got != "frontend" {
		t.Errorf("plain workload name = %q, want frontend", got)
	}
	if got := canary.WorkloadName(canary.Versions[1]); got != "catalog-v2" {
		t.Errorf("canary workload name = %q, want catalog-v2", got)
	}
}

func TestSetWeightsRequiresACompleteSplit(t *testing.T) {
	app := canaryApp()

	if err := app.SetWeights("catalog", map[string]int{"v1": 100}); err == nil {
		t.Error("a partial split was accepted — every version must be named")
	}
	if err := app.SetWeights("catalog", map[string]int{"v1": 50, "v2": 30, "v3": 30}); err == nil {
		t.Error("weights summing to 110 were accepted")
	}
	if err := app.SetWeights("catalog", map[string]int{"v1": 0, "v2": 0, "v3": 100, "v4": 0}); err == nil {
		t.Error("an unknown version was accepted")
	}
	if err := app.SetWeights("frontend", map[string]int{"v1": 100}); err == nil {
		t.Error("weights on a non-canary service were accepted")
	}

	if err := app.SetWeights("catalog", map[string]int{"v1": 10, "v2": 10, "v3": 80}); err != nil {
		t.Fatalf("a complete, valid split was rejected: %v", err)
	}
	got := map[string]int{}
	for _, v := range app.Services[1].Versions {
		got[v.Name] = v.Weight
	}
	if got["v1"] != 10 || got["v2"] != 10 || got["v3"] != 80 {
		t.Errorf("weights after SetWeights = %v", got)
	}
}

// Declaring weights without the mesh is valid YAML that silently does the wrong
// thing — kube-proxy splits evenly regardless. Nothing rejects it, so the CLI
// has to say it.
func TestMeshWarningWhenCanaryWithoutMesh(t *testing.T) {
	app := canaryApp()
	if w := app.MeshWarnings(); len(w) != 0 {
		t.Errorf("warned about a correctly configured canary: %v", w)
	}
	app.Mesh.Enabled = false
	w := app.MeshWarnings()
	if len(w) != 1 || !strings.Contains(w[0], "catalog") {
		t.Errorf("MeshWarnings = %v, want one warning naming catalog", w)
	}
}

// A version's env replaces the service's rather than merging, matching how the
// chart already treats resources and security.
func TestEnvForPrefersTheVersion(t *testing.T) {
	s := Service{
		Env:      []EnvVar{{Name: "A", Value: "svc"}},
		Versions: []Version{{Name: "v1"}, {Name: "v2", Env: []EnvVar{{Name: "A", Value: "ver"}}}},
	}
	if got := s.EnvFor(s.Versions[0]); len(got) != 1 || got[0].Value != "svc" {
		t.Errorf("v1 env = %v, want the service's", got)
	}
	if got := s.EnvFor(s.Versions[1]); len(got) != 1 || got[0].Value != "ver" {
		t.Errorf("v2 env = %v, want the version's", got)
	}
}
