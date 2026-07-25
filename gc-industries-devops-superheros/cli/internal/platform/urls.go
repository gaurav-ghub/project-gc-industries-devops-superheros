package platform

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
	"gopkg.in/yaml.v3"
)

// `endurance urls` — where the platform is, and whether it answers.
//
// # Why this command exists at all
//
// Until Phase 10 the answer to "where is ArgoCD" was four `kubectl port-forward`
// commands, each needing its own terminal and each dying with the shell that
// started it. The access layer replaced them with real addresses: kind publishes
// two node ports to the host, the Istio ingress gateway listens on them, and one
// Gateway plus a VirtualService per path puts every dashboard under
// http://localhost:8080/<something>.
//
// # Why it probes
//
// A command that prints a list of URLs is making a claim, and this platform has
// a standing rule about claims: never report an outcome you did not observe.
// Printing six addresses proves the CLI can format a string. So `urls` asks each
// one, and says which answered. That also makes it the detector for the single
// sharp edge in the design — Istio merges every VirtualService bound to the same
// gateway and host into one route table in creation order, so an application
// whose route is a bare `/` could in principle shadow the platform's own. If
// that ever happens, the command that prints the URLs is the command that says
// which of them stopped working.
//
// # One table, three readers
//
// The addresses are defined once, here. `endurance bootstrap` closes with them,
// `endurance urls` reprints them, and the success screen embeds them. Three
// copies of a URL list is how three of them come to be wrong.

// GatewayName is the platform's one ingress Gateway, as
// platform/access/manifests/gateway.yaml names it and as charts/app writes it
// into an application's VirtualService. A test reads the manifest and fails if
// they stop agreeing.
const GatewayName = "endurance-gateway"

// GatewayNamespace is where that Gateway lives.
const GatewayNamespace = "istio-system"

// kindConfig is the file that decides which host port the platform answers on.
// It is read rather than duplicated: kind fixes port mappings at cluster
// creation, so this file is the only thing that can be right about them.
const kindConfig = "kind-config.yaml"

// gatewayOverlay pins the ingress gateway Service to the node ports kindConfig
// publishes. Two files, two numbers, and nothing at runtime can tell you they
// disagree — a mismatch installs perfectly and answers nothing — so a test
// compares them.
const gatewayOverlay = "platform/networking/istio/kind-gateway.yaml"

// ingressNodePort is the node port the platform's HTTP front door listens on.
// It is the containerPort side of the mapping in kindConfig and the nodePort
// side of the http2 port in gatewayOverlay.
const ingressNodePort = 30000

// DefaultHostPort is the fallback when kind-config.yaml cannot be read: the
// number that file has carried since Phase 10. A wrong URL is better than no
// output, as long as nothing pretends the file was read.
const DefaultHostPort = 8080

// DefaultBaseURL is DefaultHostPort as an address, for the help text — which is
// printed before any repo has been found and so cannot read the real file.
const DefaultBaseURL = "http://localhost:8080"

// A dashboard is one thing the platform exposes on its own gateway.
//
// Component is the name `endurance status` uses for the same thing, so the two
// commands can agree about what is installed. Empty means the platform itself
// rather than a component — nothing to be missing.
type dashboard struct {
	Label     string
	Path      string
	Component string
	Note      string
}

// dashboards are the platform's own routes, in the order
// platform/access/manifests/dashboards.yaml declares them.
var dashboards = []dashboard{
	{Label: "ArgoCD", Path: "/argocd", Component: "argocd", Note: "user admin"},
	{Label: "Kiali", Path: "/kiali", Component: "kiali"},
	{Label: "Grafana", Path: "/grafana", Component: "grafana", Note: "user admin"},
	{Label: "Prometheus", Path: "/prometheus", Component: "prometheus"},
	{Label: "Alertmanager", Path: "/alertmanager", Component: "alertmanager"},
}

// CredentialHints are the commands that fetch the two passwords the platform
// generates. The passwords themselves are never printed — Phase 9's rule, and
// the reason it exists is that a bootstrap transcript gets screenshotted.
var CredentialHints = []render.Hint{
	{
		Command: `kubectl -n argocd get secret argocd-initial-admin-secret ` +
			`-o jsonpath="{.data.password}" | base64 -d`,
		Note: "ArgoCD, user admin",
	},
	{
		Command: `kubectl -n monitoring get secret prometheus-grafana ` +
			`-o jsonpath="{.data.admin-password}" | base64 -d`,
		Note: "Grafana, user admin",
	},
}

// kindConfigFile is the shape of kind-config.yaml this package reads. Only the
// port mappings; kind's file carries much more and none of it is ours.
type kindConfigFile struct {
	Nodes []struct {
		Role              string `yaml:"role"`
		ExtraPortMappings []struct {
			ContainerPort int `yaml:"containerPort"`
			HostPort      int `yaml:"hostPort"`
		} `yaml:"extraPortMappings"`
	} `yaml:"nodes"`
}

// HostPort returns the host port the ingress gateway is published on, read from
// kind-config.yaml.
//
// The second result reports whether the file actually said so. A caller that
// prints URLs needs to know the difference between "the platform answers on
// 8080" and "8080 is the number this build was written against", because the
// first is a fact about this repo and the second is a guess.
func HostPort(root string) (int, bool) {
	b, err := os.ReadFile(filepath.Join(root, kindConfig))
	if err != nil {
		return DefaultHostPort, false
	}
	var cfg kindConfigFile
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return DefaultHostPort, false
	}
	for _, n := range cfg.Nodes {
		for _, m := range n.ExtraPortMappings {
			if m.ContainerPort == ingressNodePort && m.HostPort > 0 {
				return m.HostPort, true
			}
		}
	}
	return DefaultHostPort, false
}

// BaseURL is the platform's one host, the prefix under which everything lives.
func BaseURL(root string) string {
	port, _ := HostPort(root)
	return fmt.Sprintf("http://localhost:%d", port)
}

// AccessURLs is the platform's own addresses. Applications are not here: an
// application's route comes from its own spec, and `endurance status <app>`
// prints it on the application's success screen.
func AccessURLs(root string) []render.URL { return AccessURLsFor(BaseURL(root)) }

// AccessURLsFor is AccessURLs against an already-resolved base address.
//
// It exists for callers that have built the base once and must not build it
// twice: the success screen prints the application's URL beside the platform's,
// and two reads of kind-config.yaml inside one screen is two chances for the
// halves of it to disagree about which port the platform is on.
func AccessURLsFor(base string) []render.URL {
	out := make([]render.URL, 0, len(dashboards))
	for _, d := range dashboards {
		out = append(out, render.URL{Label: d.Label, Addr: base + d.Path, Note: d.Note})
	}
	return out
}

// A probeFunc asks one address whether it is there. Tests replace it; nothing
// else does.
type probeFunc func(url string) (int, error)

// probeHTTP is deliberately blunt. It follows no redirects and reads no body:
// the question is "did something on the other end of this port answer for this
// path", and any status code at all answers it. A 302 to a login page is a
// working ArgoCD.
func probeHTTP(url string) (int, error) {
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// probeResult is what one address said.
type probeResult struct {
	code int
	err  error
}

// answered reports whether anything served the path.
//
// 404 is not an answer. The gateway returns it for a path no VirtualService
// claims, which is precisely the failure this command exists to catch — a route
// that was never applied, or one an application's `/` shadowed. 503 is not
// either: that is the gateway saying it has a route and nothing healthy behind
// it. Everything else, including a redirect to a login page, means a real
// backend replied.
func (p probeResult) answered() bool {
	return p.err == nil && p.code > 0 && p.code != http.StatusNotFound &&
		p.code != http.StatusServiceUnavailable
}

func (p probeResult) note() string {
	switch {
	case p.err != nil:
		return "no answer"
	case p.code == http.StatusNotFound:
		return "404 — no route"
	case p.code == http.StatusServiceUnavailable:
		return "503 — nothing behind the route"
	default:
		return fmt.Sprintf("%d", p.code)
	}
}

// probeAll asks every address, retrying the ones that did not answer.
//
// The retry is small and bounded on purpose. Envoy picks up a new route within
// a second or two of it being applied, so a bootstrap that probes the instant
// its last module finished can catch a gateway mid-update — which would report
// a broken platform that is fine. Three attempts covers that; anything longer
// starts hiding a real fault behind a wait.
func probeAll(urls []render.URL, probe probeFunc, attempts int, wait time.Duration) []probeResult {
	results := make([]probeResult, len(urls))
	for attempt := 0; attempt < attempts; attempt++ {
		pending := 0
		for i, u := range urls {
			if attempt > 0 && results[i].answered() {
				continue
			}
			code, err := probe(u.Addr)
			results[i] = probeResult{code: code, err: err}
			if !results[i].answered() {
				pending++
			}
		}
		if pending == 0 {
			return results
		}
		if attempt < attempts-1 {
			time.Sleep(wait)
		}
	}
	return results
}

// UrlsOptions configures `endurance urls`.
type UrlsOptions struct {
	Root  string // platform repo root ("" = discover)
	Check bool   // ask each address whether it answers
	probe probeFunc
}

// Urls prints the platform's addresses.
//
// It returns an error when a check was asked for and something did not answer,
// so a script can gate on it — the same bargain `status` and `doctor` make.
// Without --check it is a lookup and always succeeds: printing where something
// is supposed to be is useful even when the cluster is down.
func Urls(opts UrlsOptions) error {
	render.Banner(version.Current)

	root, err := FindRoot(opts.Root)
	if err != nil {
		return err
	}
	urls := AccessURLs(root)

	if !opts.Check {
		render.URLBlock("Access", urls)
		render.Blank()
		render.Info("`endurance urls --check` asks each one whether it answers")
		printAccessFooter(root)
		return nil
	}

	probe := opts.probe
	if probe == nil {
		probe = probeHTTP
	}
	results := probeAll(urls, probe, 3, 1500*time.Millisecond)

	checked := make([]render.URL, len(urls))
	answered := 0
	for i, u := range urls {
		checked[i] = render.URL{Label: u.Label, Addr: u.Addr, Note: results[i].note()}
		if results[i].answered() {
			answered++
		}
	}
	render.URLBlock("Access", checked)
	render.Blank()

	if answered == len(urls) {
		render.Success(fmt.Sprintf("every address answered — %s", plural(answered, "address")))
		printAccessFooter(root)
		return nil
	}
	printUnreachableHints(root)
	printAccessFooter(root)
	return fmt.Errorf("%d of %d addresses did not answer", len(urls)-answered, len(urls))
}

// printUnreachableHints says the three things that are actually wrong when an
// address does not answer, in the order they are worth checking.
func printUnreachableHints(root string) {
	if _, declared := HostPort(root); !declared {
		render.Warn("kind-config.yaml does not publish node port " +
			fmt.Sprint(ingressNodePort) + " — the addresses above are a guess")
	}
	render.Info("`endurance status` — is the component behind it even running")
	render.Info("a cluster created before the access layer does not publish the host ports;")
	render.Detail("kind fixes them at creation time, so it has to be recreated:")
	render.Detail("endurance destroy   then   endurance bootstrap")
}

// printAccessFooter closes with the two things that are not addresses: how to
// get a password, and where an application's own URL comes from.
func printAccessFooter(root string) {
	render.Blank()
	render.Info("credentials are not printed; ask the cluster for them when you need one:")
	for _, h := range CredentialHints {
		render.Detail(h.Command + "    # " + h.Note)
	}
	render.Blank()
	render.Info("an application's URL comes from its own spec, not from here —")
	render.Detail("route: {enabled: true, path: /, service: <svc>} in specs/<app>.yaml")
	render.Detail("`endurance status <app>` prints it with the application's pods")
	_ = root
}

// AccessBlock closes a bootstrap with the platform's addresses, checked.
//
// Phase 9 ended a bootstrap with four port-forward commands labelled
// "temporary until Phase 10". This is what replaced them, and the difference is
// not only that the addresses are real: the run now proves them before it says
// it is finished. A bootstrap whose last act is an unverified list of URLs is a
// bootstrap that finds out it was wrong in front of an audience.
func AccessBlock(root string, probe probeFunc) {
	if probe == nil {
		probe = probeHTTP
	}
	urls := AccessURLs(root)
	results := probeAll(urls, probe, 3, 1500*time.Millisecond)

	checked := make([]render.URL, len(urls))
	answered := 0
	for i, u := range urls {
		checked[i] = render.URL{Label: u.Label, Addr: u.Addr, Note: results[i].note()}
		if results[i].answered() {
			answered++
		}
	}
	render.URLBlock("Access", checked)
	render.Blank()

	switch {
	case answered == len(urls):
		render.Success("every address answered — no port-forward, no daemon, nothing to keep running")
	case answered == 0:
		render.Warn("none of the addresses answered yet")
		printUnreachableHints(root)
	default:
		render.Warn(fmt.Sprintf("%d of %d addresses answered — `endurance urls --check` re-checks",
			answered, len(urls)))
		render.Info("a route applied seconds ago can take a moment to reach the gateway")
	}

	render.Blank()
	render.Info("credentials are not printed; ask the cluster for them when you need one:")
	for _, h := range CredentialHints {
		render.Detail(h.Command + "    # " + h.Note)
	}
}

// declaredGatewayNodePort reads the http2 nodePort out of the Istio overlay.
//
// Only a test calls this, and that is the point: the two numbers that make the
// platform reachable live in two files that no runtime check can compare,
// because a mismatch produces a cluster that installs cleanly and serves
// nothing. The comparison has to happen where a wrong answer is cheap.
func declaredGatewayNodePort(root string) (int, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(gatewayOverlay)))
	if err != nil {
		return 0, err
	}
	var overlay struct {
		Spec struct {
			Components struct {
				IngressGateways []struct {
					K8s struct {
						Service struct {
							Type  string `yaml:"type"`
							Ports []struct {
								Name     string `yaml:"name"`
								Port     int    `yaml:"port"`
								NodePort int    `yaml:"nodePort"`
							} `yaml:"ports"`
						} `yaml:"service"`
					} `yaml:"k8s"`
				} `yaml:"ingressGateways"`
			} `yaml:"components"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(b, &overlay); err != nil {
		return 0, fmt.Errorf("%s: %w", gatewayOverlay, err)
	}
	for _, gw := range overlay.Spec.Components.IngressGateways {
		if !strings.EqualFold(gw.K8s.Service.Type, "NodePort") {
			return 0, fmt.Errorf("%s: the ingress gateway Service is %q, not NodePort — "+
				"a LoadBalancer never gets an address on kind", gatewayOverlay, gw.K8s.Service.Type)
		}
		for _, p := range gw.K8s.Service.Ports {
			if p.Port == 80 {
				return p.NodePort, nil
			}
		}
	}
	return 0, fmt.Errorf("%s: no ingress gateway port 80", gatewayOverlay)
}
