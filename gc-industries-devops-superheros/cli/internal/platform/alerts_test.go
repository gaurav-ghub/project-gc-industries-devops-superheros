package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The alerting pipeline is four files that have to agree and no runtime check
// that they do.
//
//	infra/monitoring/rules/application-alerts.yaml       produces the alerts
//	platform/monitoring/prometheus/install.sh            names the helm release
//	platform/monitoring/values/kind/prometheus-values.yaml  routes them
//	platform/ai/install.sh                               receives them
//
// Every way this has gone wrong so far has been a disagreement between two of
// them that no single file was wrong about: a rule labelled `release: monitoring`
// after the release was renamed to `prometheus`, an AlertmanagerConfig nothing
// selected, and a rule nothing applied. Each time the file under review was
// correct and the pipeline was inert.
//
// These tests read both ends and compare them, in the same spirit as the
// kind-config port test — two files hold one fact, so something has to make them
// say it together.

const alertRulesFile = "infra/monitoring/rules/application-alerts.yaml"

type prometheusRule struct {
	Metadata struct {
		Name      string            `yaml:"name"`
		Namespace string            `yaml:"namespace"`
		Labels    map[string]string `yaml:"labels"`
	} `yaml:"metadata"`
	Spec struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expr        string            `yaml:"expr"`
				For         string            `yaml:"for"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	} `yaml:"spec"`
}

func loadAlertRules(t *testing.T, root string) prometheusRule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(alertRulesFile)))
	if err != nil {
		t.Fatalf("reading %s: %v", alertRulesFile, err)
	}
	var pr prometheusRule
	if err := yaml.Unmarshal(data, &pr); err != nil {
		t.Fatalf("parsing %s: %v", alertRulesFile, err)
	}
	if len(pr.Spec.Groups) == 0 {
		t.Fatalf("%s declares no rule groups", alertRulesFile)
	}
	return pr
}

// TestEveryAlertCarriesTheLabelsAlertmanagerRoutesOn is the link that decides
// whether an alert reaches a human or the null receiver.
//
// The matchers are read out of the Alertmanager config rather than written down
// here, so adding a matcher to the route without labelling the rules fails this
// rather than silently sending every alert to `null`.
func TestEveryAlertCarriesTheLabelsAlertmanagerRoutesOn(t *testing.T) {
	root := repoRoot(t)
	want := aiWebhookMatchers(t, root)
	if len(want) == 0 {
		t.Fatal("no matchers found on the ai-webhook route — the enricher would receive nothing")
	}

	pr := loadAlertRules(t, root)
	for _, g := range pr.Spec.Groups {
		for _, r := range g.Rules {
			for k, v := range want {
				if r.Labels[k] != v {
					t.Errorf("alert %s has %s=%q; the ai-webhook route matches %s=%q, so this "+
						"alert would go to the null receiver", r.Alert, k, r.Labels[k], k, v)
				}
			}
		}
	}
}

// aiWebhookMatchers reads the matchers of the route that reaches the AI
// enricher, as {label: value}. The matchers are written `severity="warning"`.
func aiWebhookMatchers(t *testing.T, root string) map[string]string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash("platform/monitoring/values/kind/prometheus-values.yaml"))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var values struct {
		Alertmanager struct {
			Config struct {
				Route struct {
					Routes []struct {
						Receiver string   `yaml:"receiver"`
						Matchers []string `yaml:"matchers"`
					} `yaml:"routes"`
				} `yaml:"route"`
			} `yaml:"config"`
		} `yaml:"alertmanager"`
	}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	matcher := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)="([^"]*)"$`)
	out := map[string]string{}
	for _, r := range values.Alertmanager.Config.Route.Routes {
		if r.Receiver != "ai-webhook" {
			continue
		}
		for _, m := range r.Matchers {
			if sub := matcher.FindStringSubmatch(strings.TrimSpace(m)); sub != nil {
				out[sub[1]] = sub[2]
			}
		}
	}
	return out
}

// TestTheRuleIsLabelledForTheReleaseThatSelectsIt. kube-prometheus-stack's
// default ruleSelector is release=<helm release name>. A rule with the wrong
// value here is applied to the cluster, visible in ArgoCD, readable with
// kubectl — and loaded into no Prometheus. This label was "monitoring" once,
// left behind by a rename, and the alert simply did not exist.
func TestTheRuleIsLabelledForTheReleaseThatSelectsIt(t *testing.T) {
	root := repoRoot(t)
	pr := loadAlertRules(t, root)

	script := filepath.Join(root, filepath.FromSlash("platform/monitoring/prometheus/install.sh"))
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}
	release := regexp.MustCompile(`--install\s+(\S+)\s+\\?\s*\n?\s*prometheus-community/kube-prometheus-stack`)
	sub := release.FindStringSubmatch(string(data))
	if sub == nil {
		t.Skip("could not read the helm release name out of the prometheus installer")
	}
	if got := pr.Metadata.Labels["release"]; got != sub[1] {
		t.Errorf("the rule is labelled release=%q but the stack is installed as the %q release — "+
			"kube-prometheus-stack's ruleSelector would not select it", got, sub[1])
	}
}

// TestNoAlertIsPinnedToOneApplication is 13.4's second half, and the half that
// was reported as the whole problem.
//
// The original rule read `namespace="superheros"` — the reference application —
// so even loaded it would have watched one application on a platform that hosts
// any number. An exclusion (`namespace!~"..."`) is the legitimate form and is
// what the rules use to stay off the platform's own namespaces.
func TestNoAlertIsPinnedToOneApplication(t *testing.T) {
	pinned := regexp.MustCompile(`namespace\s*=\s*"`)
	pr := loadAlertRules(t, repoRoot(t))
	for _, g := range pr.Spec.Groups {
		for _, r := range g.Rules {
			if pinned.MatchString(r.Expr) {
				t.Errorf("alert %s pins a single namespace in its expression — the platform "+
					"alerts on every application namespace and narrows with `namespace!~`:\n%s",
					r.Alert, r.Expr)
			}
		}
	}
}

// TestTheAlertsCoverHowDeploymentsActuallyFail is 13.5.
//
// `PodRestartingFrequently` over a [5m] increase window was the platform's
// entire alerting surface, and `bad-app` sat in ImagePullBackOff for seventeen
// minutes with restart count 0 — the one alert there was could not see the one
// failure there was. These are the reasons the first outside run actually
// produced, plus the memory limit and a backstop.
func TestTheAlertsCoverHowDeploymentsActuallyFail(t *testing.T) {
	pr := loadAlertRules(t, repoRoot(t))

	var exprs, names []string
	for _, g := range pr.Spec.Groups {
		for _, r := range g.Rules {
			names = append(names, r.Alert)
			exprs = append(exprs, r.Expr)
			if r.For == "" {
				t.Errorf("alert %s has no `for:` — it fires on a single scrape", r.Alert)
			}
			if r.Annotations["summary"] == "" || r.Annotations["description"] == "" {
				t.Errorf("alert %s carries no summary/description — those annotations are the "+
					"whole of what the AI enricher has to explain it with", r.Alert)
			}
		}
	}
	all := strings.Join(exprs, "\n")
	for _, reason := range []string{
		"ImagePullBackOff", "ErrImagePull", "CrashLoopBackOff", "OOMKilled",
	} {
		if !strings.Contains(all, reason) {
			t.Errorf("no alert mentions %s — it is one of the ways a deploy fails here "+
				"without ever moving the restart counter. Alerts: %v", reason, names)
		}
	}
	if !strings.Contains(all, "kube_pod_container_status_ready") {
		t.Errorf("no backstop alert on a container that never became ready. Alerts: %v", names)
	}
	if !strings.Contains(all, "kube_pod_container_status_restarts_total") {
		t.Errorf("the restart alert was dropped rather than widened. Alerts: %v", names)
	}
}
