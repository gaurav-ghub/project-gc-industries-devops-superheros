package manifest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

const chartDir = "../../../charts/app"

func sample() spec.App {
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

// canarySample is the shape Phase 4 added: one service split across weighted
// versions, with the mesh on.
func canarySample() spec.App {
	app := sample()
	app.Mesh = spec.Mesh{Enabled: true}
	i := app.FindService("catalog")
	app.Services[i].Tag = ""
	app.Services[i].Replicas = 0
	app.Services[i].Versions = []spec.Version{
		{Name: "v1", Tag: "v1-6f11a5d", Weight: 40, Replicas: 1,
			Env: []spec.EnvVar{{Name: "CATALOG_VERSION", Value: "v1"}}},
		{Name: "v2", Tag: "v2-6f11a5d", Weight: 30, Replicas: 1,
			Env: []spec.EnvVar{{Name: "CATALOG_VERSION", Value: "v2"}}},
		{Name: "v3", Tag: "v3-6f11a5d", Weight: 30, Replicas: 1,
			Env: []spec.EnvVar{{Name: "CATALOG_VERSION", Value: "v3"}}},
	}
	return app
}

// routedSample is the shape Phase 10 added: an application that asked for a
// public address, which is the only thing charts/app renders that binds to the
// platform's ingress Gateway.
func routedSample() spec.App {
	app := sample()
	app.Route = spec.Route{Enabled: true, Path: "/", Service: "frontend"}
	return app
}

// A canary service renders one Deployment per version but still exactly one
// Service — if it rendered one Service per version there would be nothing for
// Istio to split, since each version would have its own address.
func TestCanaryRendersOneWorkloadPerVersionAndOneService(t *testing.T) {
	counts := map[string]int{}
	names := map[string]bool{}
	for _, r := range Render(canarySample()) {
		counts[r.Kind]++
		names[r.Kind+"/"+r.Name] = true
	}
	for kind, want := range map[string]int{
		"Deployment": 4, "Pod": 4, "Service": 2, "DestinationRule": 1, "VirtualService": 1,
	} {
		if counts[kind] != want {
			t.Errorf("rendered %d %s, want %d", counts[kind], kind, want)
		}
	}
	for _, want := range []string{
		"Deployment/catalog-v1", "Deployment/catalog-v2", "Deployment/catalog-v3",
		"Deployment/frontend", "Service/catalog", "VirtualService/catalog",
	} {
		if !names[want] {
			t.Errorf("missing %s", want)
		}
	}
	if names["Deployment/catalog"] {
		t.Error("rendered a bare catalog Deployment alongside its versions")
	}
	if names["Service/catalog-v1"] {
		t.Error("rendered a per-version Service — all versions must share one address")
	}
}

// The Service must select on name+part-of only. Including the version label
// would give each version its own endpoint set and make the DestinationRule
// subsets meaningless.
func TestCanaryServiceSelectorIgnoresVersion(t *testing.T) {
	for _, r := range Render(canarySample()) {
		if r.Kind != "Service" || r.Name != "catalog" {
			continue
		}
		sel := r.Object["spec"].(map[string]any)["selector"].(map[string]any)
		if _, ok := sel["app.kubernetes.io/version"]; ok {
			t.Fatalf("Service selector pins a version: %v", sel)
		}
		return
	}
	t.Fatal("no catalog Service rendered")
}

// The mesh objects exist only when the application asked for the mesh. Without
// the sidecars they would be inert configuration that looks like it works.
func TestMeshObjectsRequireMeshEnabled(t *testing.T) {
	app := canarySample()
	app.Mesh.Enabled = false
	for _, r := range Render(app) {
		if r.Kind == "VirtualService" || r.Kind == "DestinationRule" {
			t.Errorf("rendered %s/%s with mesh disabled", r.Kind, r.Name)
		}
		if r.Kind != "Deployment" {
			continue
		}
		lbl := r.Object["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
		if _, ok := lbl["sidecar.istio.io/inject"]; ok {
			t.Errorf("%s carries the sidecar opt-in with mesh disabled", r.Name)
		}
	}
}

// The weights are what a canary shift changes, and they must live only in the
// VirtualService — a weight that reached a pod spec would restart the pod on
// every shift, which is precisely what routing at the mesh layer avoids.
func TestWeightsAppearOnlyInTheVirtualService(t *testing.T) {
	res := Render(canarySample())
	for _, r := range res {
		if r.Kind == "VirtualService" {
			continue
		}
		if strings.Contains(dump(t, r.Object), "weight") {
			t.Errorf("%s/%s mentions a weight — a weight change would restart it", r.Kind, r.Name)
		}
	}
	for _, r := range res {
		if r.Kind != "VirtualService" {
			continue
		}
		routes := r.Object["spec"].(map[string]any)["http"].([]any)[0].(map[string]any)["route"].([]any)
		total := 0
		for _, rt := range routes {
			total += rt.(map[string]any)["weight"].(int)
		}
		if total != 100 {
			t.Errorf("route weights sum to %d, want 100", total)
		}
		return
	}
	t.Fatal("no VirtualService rendered")
}

func dump(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderCoversEveryService(t *testing.T) {
	res := Render(sample())
	counts := map[string]int{}
	for _, r := range res {
		counts[r.Kind]++
		if r.Namespace != "superheros" {
			t.Errorf("%s/%s has namespace %q — policy namespace matching depends on this",
				r.Kind, r.Name, r.Namespace)
		}
	}
	for kind, want := range map[string]int{"Deployment": 2, "Pod": 2, "Service": 2} {
		if counts[kind] != want {
			t.Errorf("rendered %d %s, want %d", counts[kind], kind, want)
		}
	}
}

// Defaults must be materialized before rendering, or every Pod-matching policy
// would judge a manifest that is missing the fields it asks about.
func TestRenderMaterializesDefaults(t *testing.T) {
	app := sample()
	app.Services[0].Resources = spec.Resources{} // explicitly unset
	res := Render(app)

	for _, r := range res {
		if r.Kind != "Pod" || r.Name != "catalog" {
			continue
		}
		c := r.Object["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
		req := c["resources"].(map[string]any)["requests"].(map[string]any)
		if req["cpu"] != spec.DefaultRequestsCPU {
			t.Errorf("requests.cpu = %v, want %s", req["cpu"], spec.DefaultRequestsCPU)
		}
		sc := r.Object["spec"].(map[string]any)["securityContext"].(map[string]any)
		if sc["runAsNonRoot"] != true {
			t.Error("runAsNonRoot was not defaulted to true")
		}
		return
	}
	t.Fatal("no catalog Pod was rendered")
}

// The projection exists so the gate does not have to shell out to helm. That is
// only safe while the two agree, so this test renders the real chart and asserts
// they do. It is skipped where helm is unavailable rather than failing, since
// the CLI itself never needs helm.
func TestProjectionMatchesHelmTemplate(t *testing.T) {
	// Both shapes, because canary is where the two renderers could most easily
	// diverge: the chart builds its per-version fallback in template language and
	// the projection builds it in Go, and nothing but this test connects them.
	for _, tc := range []struct {
		name string
		app  spec.App
	}{
		{"plain", sample()},
		{"canary", canarySample()},
		{"routed", routedSample()},
	} {
		t.Run(tc.name, func(t *testing.T) { assertChartConformance(t, tc.app) })
	}
}

func assertChartConformance(t *testing.T, app spec.App) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed — skipping chart conformance check")
	}

	app.ApplyDefaults()

	values := map[string]any{
		"app":      map[string]any{"name": app.Name},
		"mesh":     map[string]any{"enabled": app.Mesh.Enabled},
		"services": app.Services,
	}
	if app.Route.Enabled {
		values["route"] = app.Route
	}
	data, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	valuesPath := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(valuesPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	cmd := exec.Command(helm, "template", app.Name, chartDir, "-f", valuesPath)
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, stderr.String())
	}

	rendered := map[string]map[string]any{} // "Kind/name" -> object
	dec := yaml.NewDecoder(strings.NewReader(out.String()))
	for {
		var doc map[string]any
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if len(doc) == 0 {
			continue
		}
		kind, _ := doc["kind"].(string)
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		rendered[kind+"/"+name] = doc
	}
	if len(rendered) == 0 {
		t.Fatalf("helm rendered nothing:\n%s", out.String())
	}

	for _, r := range Render(app) {
		if r.Kind == "Pod" {
			continue // synthesized for Kyverno autogen; the chart never emits it
		}
		want, ok := rendered[r.Kind+"/"+r.Name]
		if !ok {
			t.Errorf("chart did not render %s/%s", r.Kind, r.Name)
			continue
		}
		// The chart omits metadata.namespace and lets ArgoCD's destination place
		// the object; the projection carries it so policies can match on it.
		got := deepCopy(r.Object).(map[string]any)
		delete(got["metadata"].(map[string]any), "namespace")

		if !reflect.DeepEqual(normalizeNumbers(got), normalizeNumbers(want)) {
			t.Errorf("%s/%s differs between the Go projection and the chart\nprojection: %#v\nchart:      %#v",
				r.Kind, r.Name, normalizeNumbers(got), normalizeNumbers(want))
		}
	}
}

// normalizeNumbers makes int/float differences between the YAML decoder and the
// Go projection irrelevant, so the comparison is about structure and values.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = normalizeNumbers(val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = normalizeNumbers(val)
		}
		return l
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		return v
	}
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = deepCopy(val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = deepCopy(val)
		}
		return l
	default:
		return v
	}
}
