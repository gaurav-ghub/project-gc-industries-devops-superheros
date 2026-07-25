package platform

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

// TestTheTwoPortNumbersAgree is the most important test in this file, and it is
// the one that cannot be written any other way.
//
// Reaching the platform from the host depends on two numbers in two files:
// kind-config.yaml publishes a node port to the host, and the Istio overlay
// pins the ingress gateway Service to that node port. Nothing at runtime can
// tell you they disagree — a mismatch produces a cluster that creates cleanly,
// installs every module, passes every health check, and answers nothing. The
// only cheap moment to compare them is here.
func TestTheTwoPortNumbersAgree(t *testing.T) {
	root := repoRoot(t)

	gatewayPort, err := declaredGatewayNodePort(root)
	if err != nil {
		t.Fatalf("reading %s: %v", gatewayOverlay, err)
	}
	if gatewayPort != ingressNodePort {
		t.Errorf("%s pins the gateway to node port %d, but this package routes %d",
			gatewayOverlay, gatewayPort, ingressNodePort)
	}

	host, declared := HostPort(root)
	if !declared {
		t.Fatalf("%s does not publish container port %d to the host — "+
			"every address `endurance urls` prints would be unreachable", kindConfig, ingressNodePort)
	}
	if host <= 0 || host > 65535 {
		t.Errorf("host port %d is not a port", host)
	}
}

// TestHostPortSaysWhetherItReadTheFile.
//
// HostPort falls back to 8080 so that `urls` prints something useful on a
// machine with no repo. The fallback is only safe because the caller can tell
// the two cases apart: printing a guessed address as though it were read is the
// same class of untruth as a success screen claiming health it never observed.
func TestHostPortSaysWhetherItReadTheFile(t *testing.T) {
	port, declared := HostPort(t.TempDir())
	if declared {
		t.Error("HostPort claimed it read a kind-config.yaml that does not exist")
	}
	if port != DefaultHostPort {
		t.Errorf("fallback port = %d, want %d", port, DefaultHostPort)
	}

	dir := t.TempDir()
	write(t, dir, kindConfig, fmt.Sprintf(`kind: Cluster
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: %d
        hostPort: 9999
`, ingressNodePort))
	port, declared = HostPort(dir)
	if !declared || port != 9999 {
		t.Errorf("HostPort = %d (declared %v), want 9999 read from the file", port, declared)
	}
	if got := BaseURL(dir); got != "http://localhost:9999" {
		t.Errorf("BaseURL = %q — the addresses do not follow the file", got)
	}
}

// TestAccessURLsCoverEveryDashboardRoute — the Go table and the VirtualService
// the access module applies are two lists of the same paths, maintained by
// hand, in two languages. This reads the real manifest.
func TestAccessURLsCoverEveryDashboardRoute(t *testing.T) {
	root := repoRoot(t)

	b, err := os.ReadFile(filepath.Join(root, "platform", "access", "manifests", "dashboards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var vs struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Gateways []string `yaml:"gateways"`
			HTTP     []struct {
				Match []struct {
					URI struct {
						Prefix string `yaml:"prefix"`
					} `yaml:"uri"`
				} `yaml:"match"`
			} `yaml:"http"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(b, &vs); err != nil {
		t.Fatal(err)
	}

	if len(vs.Spec.Gateways) != 1 || vs.Spec.Gateways[0] != GatewayName {
		t.Errorf("the platform routes bind to %v, not to %s", vs.Spec.Gateways, GatewayName)
	}

	routed := map[string]bool{}
	for _, h := range vs.Spec.HTTP {
		for _, m := range h.Match {
			routed[m.URI.Prefix] = true
		}
	}
	for _, d := range dashboards {
		if !routed[d.Path] {
			t.Errorf("`endurance urls` prints %s but no VirtualService route claims it", d.Path)
		}
		delete(routed, d.Path)
	}
	for path := range routed {
		t.Errorf("the platform routes %s and nothing prints it — an address nobody can find", path)
	}
}

// TestUrlsAndStatusAgreeAboutWhatExists.
//
// `endurance urls` prints an address per component and `endurance status`
// reports whether that component is running. They are two hand-kept lists, and
// a name in one that is missing from the other is how a platform comes to print
// an address for something it never installs, or to install something nobody
// can reach. Kiali arrived in Phase 10 and had to be added to both.
func TestUrlsAndStatusAgreeAboutWhatExists(t *testing.T) {
	known := map[string]bool{}
	for _, c := range components {
		known[c.name] = true
	}
	for _, d := range dashboards {
		if d.Component == "" {
			continue // the platform itself, not a component
		}
		if !known[d.Component] {
			t.Errorf("`urls` prints %s for component %q, which `status` never reports on",
				d.Path, d.Component)
		}
	}
}

// TestReservedPathsMatchThePlatformsOwn.
//
// spec.ReservedPaths is what stops an application asking for /grafana. It is a
// second copy of the list in this package, and it is a second copy on purpose:
// the dependency runs spec <- platform, never the other way, or the application
// model would know about bootstrap and could not be split out in Phase 13.
//
// A copy needs a test. If a dashboard were added here and not there, an
// application could claim its path — and which one won would depend on which
// VirtualService Istio created first, so the symptom would be "ArgoCD is down".
func TestReservedPathsMatchThePlatformsOwn(t *testing.T) {
	reserved := map[string]bool{}
	for _, p := range spec.ReservedPaths {
		reserved[p] = true
	}
	for _, d := range dashboards {
		if !reserved[d.Path] {
			t.Errorf("the platform routes %s but spec.ReservedPaths does not protect it — "+
				"an application could claim the same prefix", d.Path)
		}
		delete(reserved, d.Path)
	}
	for p := range reserved {
		t.Errorf("spec.ReservedPaths protects %s, which the platform does not use — "+
			"an application is being refused a path for no reason", p)
	}
}

// TestTheGatewayIsNamedTheSameByBothHalves — the platform creates the Gateway
// and the application model writes its name into every route that binds to it.
func TestTheGatewayIsNamedTheSameByBothHalves(t *testing.T) {
	want := GatewayNamespace + "/" + GatewayName
	if spec.DefaultGateway != want {
		t.Errorf("applications bind to %q, the platform creates %q",
			spec.DefaultGateway, want)
	}
}

// TestTheGatewayIsNamedOnce — three places spell the Gateway: the manifest that
// creates it, this package, and charts/app, which writes it into every
// application's VirtualService. The manifest is the one that is real.
func TestTheGatewayIsNamedOnce(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(gatewayFile)))
	if err != nil {
		t.Fatal(err)
	}
	var gw struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal(b, &gw); err != nil {
		t.Fatal(err)
	}
	if gw.Kind != "Gateway" {
		t.Fatalf("%s is a %s", gatewayFile, gw.Kind)
	}
	if gw.Metadata.Name != GatewayName {
		t.Errorf("the manifest creates Gateway %q, the CLI routes to %q", gw.Metadata.Name, GatewayName)
	}
	if gw.Metadata.Namespace != GatewayNamespace {
		t.Errorf("the Gateway lives in %q, the CLI says %q", gw.Metadata.Namespace, GatewayNamespace)
	}
}

// TestEveryDeclaredAddressUsesTheMappedHostPort.
//
// Several component values files have to spell the platform's public address
// out in full — Grafana's root_url, Prometheus's and Alertmanager's externalUrl,
// ArgoCD's argocdUrl, Kiali's link to Grafana. Each is a copy of a number that
// kind-config.yaml owns, and a copy that drifts produces a UI that loads and
// then links to nowhere.
func TestEveryDeclaredAddressUsesTheMappedHostPort(t *testing.T) {
	root := repoRoot(t)
	port, declared := HostPort(root)
	if !declared {
		t.Skip("kind-config.yaml publishes no ingress mapping")
	}
	want := fmt.Sprintf("http://localhost:%d", port)

	files := []string{
		"platform/gitops/argocd/values.yaml",
		"platform/monitoring/values/kind/prometheus-values.yaml",
		"platform/access/kiali/values.yaml",
		"platform/access/verify.sh",
	}
	for _, rel := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			i := strings.Index(line, "http://localhost:")
			if i < 0 {
				continue
			}
			if !strings.HasPrefix(line[i:], want) {
				t.Errorf("%s declares an address on the wrong port (want %s):\n  %s",
					rel, want, strings.TrimSpace(line))
			}
		}
	}
}

// TestUrlsReportsWhatAnswered — the command's whole job.
func TestUrlsReportsWhatAnswered(t *testing.T) {
	root := repoRoot(t)

	t.Run("everything answers", func(t *testing.T) {
		buf := capture(t)
		err := Urls(UrlsOptions{Root: root, Check: true, probe: allAnswer})
		if err != nil {
			t.Fatalf("urls: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "every address answered") {
			t.Errorf("no verdict:\n%s", got)
		}
		for _, d := range dashboards {
			if !strings.Contains(got, BaseURL(root)+d.Path) {
				t.Errorf("%s is missing:\n%s", d.Path, got)
			}
		}
	})

	t.Run("a 404 is not an answer", func(t *testing.T) {
		buf := capture(t)
		// The gateway returns 404 for a path no VirtualService claims — a route
		// that was never applied, or one an application's `/` shadowed. That is
		// exactly the failure this command exists to catch, so it must not be
		// rounded up into "it responded".
		err := Urls(UrlsOptions{Root: root, Check: true,
			probe: func(string) (int, error) { return http.StatusNotFound, nil }})
		if err == nil {
			t.Fatal("a platform answering 404 on every path was reported as healthy")
		}
		got := buf.String()
		if !strings.Contains(got, "404 — no route") {
			t.Errorf("the 404 is not explained:\n%s", got)
		}
	})

	t.Run("503 means the route exists and the backend does not", func(t *testing.T) {
		capture(t)
		err := Urls(UrlsOptions{Root: root, Check: true,
			probe: func(string) (int, error) { return http.StatusServiceUnavailable, nil }})
		if err == nil {
			t.Fatal("503 from every address was reported as healthy")
		}
	})

	t.Run("a redirect to a login page is an answer", func(t *testing.T) {
		capture(t)
		// ArgoCD answers / with a 307 to its login page. Treating that as a
		// failure would make the check useless on the one component most likely
		// to redirect.
		if err := Urls(UrlsOptions{Root: root, Check: true,
			probe: func(string) (int, error) { return http.StatusTemporaryRedirect, nil }}); err != nil {
			t.Errorf("a redirect was treated as unreachable: %v", err)
		}
	})

	t.Run("without --check it is a lookup and cannot fail", func(t *testing.T) {
		buf := capture(t)
		if err := Urls(UrlsOptions{Root: root, probe: noneAnswer}); err != nil {
			t.Errorf("a lookup failed: %v", err)
		}
		if strings.Contains(buf.String(), "no answer") {
			t.Error("a lookup probed anyway")
		}
	})
}

// TestUrlsNeverPrintsACredential — the Phase 9 rule, applied to the command
// that inherited the access block.
func TestUrlsNeverPrintsACredential(t *testing.T) {
	buf := capture(t)
	if err := Urls(UrlsOptions{Root: repoRoot(t), Check: true, probe: allAnswer}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, forbidden := range []string{"Password:", "password:", "Username:"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("urls printed something shaped like a credential (%q):\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "argocd-initial-admin-secret") {
		t.Errorf("it does not say how to fetch the ArgoCD password:\n%s", got)
	}
}

// TestProbeRetriesOnlyWhatFailed.
//
// Envoy picks up a new route a second or two after it is applied, so a
// bootstrap that probes the instant its last module finished can catch the
// gateway mid-update. The retry covers that and nothing more: an address that
// answered is never asked twice, because a bounded wait that hides a real fault
// is worse than a first attempt that reports one.
func TestProbeRetriesOnlyWhatFailed(t *testing.T) {
	urls := AccessURLs(repoRoot(t))
	calls := map[string]int{}
	// The first address needs two attempts; the rest answer immediately.
	probe := func(u string) (int, error) {
		calls[u]++
		if u == urls[0].Addr && calls[u] == 1 {
			return 0, errors.New("connection refused")
		}
		return 200, nil
	}

	results := probeAll(urls, probe, 3, time.Millisecond)
	for i, r := range results {
		if !r.answered() {
			t.Errorf("%s did not answer: %v", urls[i].Addr, r.note())
		}
	}
	if calls[urls[0].Addr] != 2 {
		t.Errorf("the failing address was asked %d times, want 2", calls[urls[0].Addr])
	}
	for _, u := range urls[1:] {
		if calls[u.Addr] != 1 {
			t.Errorf("%s answered first time and was asked %d times", u.Addr, calls[u.Addr])
		}
	}
}
