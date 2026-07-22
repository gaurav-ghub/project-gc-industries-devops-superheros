package manifest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gc-ghub/launchpad/internal/spec"
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
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed — skipping chart conformance check")
	}

	app := sample()
	app.ApplyDefaults()

	values := map[string]any{
		"app":      map[string]any{"name": app.Name},
		"services": app.Services,
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
