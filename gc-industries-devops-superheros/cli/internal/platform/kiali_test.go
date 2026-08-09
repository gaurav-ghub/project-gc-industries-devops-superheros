package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Kiali's traffic graph (14.10, B5) — two independent breaks, neither visible
// from the other: Kiali's own Istio-config check reads over the Kubernetes
// API and stays ✓ throughout, which is exactly what let both survive
// unnoticed. These tests read the same files a live run would have to get
// right, the same way alerts_test.go reads the alerting pipeline's files.

const (
	kialiValuesFile      = "platform/access/kiali/values.yaml"
	prometheusValuesFile = "platform/monitoring/values/kind/prometheus-values.yaml"
	istioPodMonitorFile  = "platform/access/manifests/istio-podmonitor.yaml"
	accessInstallScript  = "platform/access/install.sh"
)

// TestKialisPrometheusURLCarriesTheRoutePrefix — the first break. Prometheus
// is told routePrefix: /prometheus so it can sit behind the platform's single
// localhost:8080 ingress; routePrefix changes what the pod actually *serves*,
// not merely what a browser sees, so a URL without it 404s on every call.
// Read out of both files rather than pinned as a literal, so the two cannot
// silently drift apart the way the release label did in 13.4.
func TestKialisPrometheusURLCarriesTheRoutePrefix(t *testing.T) {
	root := repoRoot(t)

	routePrefix := readPrometheusRoutePrefix(t, root)
	if routePrefix == "" {
		t.Fatal("could not read prometheus.prometheusSpec.routePrefix — nothing to compare Kiali's URL against")
	}

	kialiURL := readKialiPrometheusURL(t, root)
	if kialiURL == "" {
		t.Fatal("could not read external_services.prometheus.url from " + kialiValuesFile)
	}
	if !strings.HasSuffix(strings.TrimRight(kialiURL, "/"), routePrefix) {
		t.Errorf("Kiali's prometheus.url is %q, which does not end in the declared routePrefix %q — "+
			"every Prometheus API call Kiali makes 404s", kialiURL, routePrefix)
	}
}

func readPrometheusRoutePrefix(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(prometheusValuesFile))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var v struct {
		Prometheus struct {
			PrometheusSpec struct {
				RoutePrefix string `yaml:"routePrefix"`
			} `yaml:"prometheusSpec"`
		} `yaml:"prometheus"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return v.Prometheus.PrometheusSpec.RoutePrefix
}

func readKialiPrometheusURL(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(kialiValuesFile))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var v struct {
		ExternalServices struct {
			Prometheus struct {
				URL string `yaml:"url"`
			} `yaml:"prometheus"`
		} `yaml:"external_services"`
	}
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return v.ExternalServices.Prometheus.URL
}

// istioPodMonitor is the shape of manifest kiali_test.go reads.
type istioPodMonitor struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		PodMetricsEndpoints []struct {
			Path string `yaml:"path"`
		} `yaml:"podMetricsEndpoints"`
	} `yaml:"spec"`
}

func loadIstioPodMonitor(t *testing.T, root string) istioPodMonitor {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(istioPodMonitorFile))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v — no PodMonitor scrapes the mesh's sidecars", path, err)
	}
	var pm istioPodMonitor
	if err := yaml.Unmarshal(data, &pm); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return pm
}

// TestTheIstioPodMonitorExistsAndIsShapedToBeSelected — the second break.
// istio_requests_total does not exist until something scrapes istio-proxy;
// nothing did, anywhere in this repo, until this file.
func TestTheIstioPodMonitorExistsAndIsShapedToBeSelected(t *testing.T) {
	root := repoRoot(t)
	pm := loadIstioPodMonitor(t, root)

	if pm.Kind != "PodMonitor" {
		t.Errorf("kind = %q, want PodMonitor", pm.Kind)
	}

	// kube-prometheus-stack only selects a PodMonitor carrying its own Helm
	// release name, unless podMonitorSelector is overridden to {} — the exact
	// selector-label fault 13.4 found for the alert rules
	// (TestTheRuleIsLabelledForTheReleaseThatSelectsIt), one resource kind
	// over. A PodMonitor applied and never selected is indistinguishable, from
	// outside, from one that was never applied.
	release := readPrometheusHelmRelease(t, root)
	if got := pm.Metadata.Labels["release"]; got != release {
		t.Errorf("the PodMonitor is labelled release=%q but Prometheus is installed as the %q "+
			"release — kube-prometheus-stack's podMonitorSelector would not select it", got, release)
	}

	if len(pm.Spec.PodMetricsEndpoints) == 0 {
		t.Fatal("the PodMonitor declares no podMetricsEndpoints — nothing would be scraped")
	}
	if pm.Spec.PodMetricsEndpoints[0].Path != "/stats/prometheus" {
		t.Errorf("scrape path = %q, want /stats/prometheus — Istio's own documented metrics endpoint",
			pm.Spec.PodMetricsEndpoints[0].Path)
	}
}

// readPrometheusHelmRelease mirrors TestTheRuleIsLabelledForTheReleaseThatSelectsIt's
// own lookup, so both tests read the installer's release name the same way.
func readPrometheusHelmRelease(t *testing.T, root string) string {
	t.Helper()
	script := filepath.Join(root, filepath.FromSlash("platform/monitoring/prometheus/install.sh"))
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--install") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				return f[1]
			}
		}
	}
	t.Skip("could not read the helm release name out of the prometheus installer")
	return ""
}

// TestTheIstioPodMonitorIsActuallyApplied — a declared resource that nothing
// applies is not configuration (the exact lesson infra/monitoring/
// pod-restart-alert.yaml cost twelve phases). This one has no CRD until the
// Prometheus Operator installs, so it is a direct kubectl apply from the
// access module — which runs last in the bootstrap chain, after both
// Prometheus and Istio are up — rather than an ArgoCD Application.
func TestTheIstioPodMonitorIsActuallyApplied(t *testing.T) {
	root := repoRoot(t)
	script := filepath.Join(root, filepath.FromSlash(accessInstallScript))
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	if !strings.Contains(string(data), "istio-podmonitor.yaml") {
		t.Errorf("%s never applies %s — the PodMonitor would sit in the repo and never reach a cluster",
			accessInstallScript, istioPodMonitorFile)
	}
}
