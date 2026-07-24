package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func superherosResult() Result {
	return Result{
		Title: "superheros is deployed",
		Rows: [][2]string{
			{"Namespace", "superheros"},
			{"Cluster", "kind-superheros"},
			{"Services", "5  (frontend, catalog, orders, inventory, payment)"},
			{"Image", "docker.io/gcghub/superhero-frontend:v1.4.2"},
			{"Replicas", "1 per service"},
		},
		Pods: []Pod{
			{Name: "frontend-6d9f7c8b4d-r2x9p", Status: "Running", State: StateReady},
			{Name: "catalog-7c5f6d4b88-k4t2m", Status: "Running", State: StateReady},
			{Name: "orders-59d8b7f6c5-h8w3q", Status: "ContainerCreating", State: StatePending},
		},
		URLs: []URL{
			{Label: "App", Addr: "http://localhost:8080/"},
			{Label: "ArgoCD", Addr: "http://localhost:8080/argocd", Note: "admin · endurance urls --show-password"},
			{Label: "Grafana", Addr: "http://localhost:8080/grafana"},
		},
		Hints: []Hint{
			{Command: "endurance status superheros", Note: "services and pods"},
			{Command: "endurance release superheros --service catalog --tag v2", Note: "promote an image"},
			{Command: "kubectl get pods -n superheros", Note: "the raw truth"},
		},
		Footer: "2 of 3 pods ready — ArgoCD is still syncing",
	}
}

func TestSuccessScreenGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.SuccessScreen(superherosResult())
	golden(t, "success-screen", buf.String())
}

// TestSuccessScreenIsHonestAboutPendingPods — the rule this screen exists to
// obey. A pod that is not Running yet renders as pending; only an observed
// Running pod earns a ✓.
func TestSuccessScreenIsHonestAboutPendingPods(t *testing.T) {
	r, buf, _ := fixture(t)
	r.SuccessScreen(superherosResult())
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "ContainerCreating") {
			if strings.Contains(line, IconOK) {
				t.Errorf("a pod that is not Running was marked done: %q", line)
			}
			if !strings.Contains(line, IconInfo) {
				t.Errorf("a pending pod is missing its %q glyph: %q", IconInfo, line)
			}
		}
	}
	if !strings.Contains(buf.String(), "2 of 3 pods ready") {
		t.Error("the footer did not carry the honest count")
	}
}

func TestURLBlockGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.URLBlock("Platform URLs", []URL{
		{Label: "App", Addr: "http://localhost:8080/"},
		{Label: "ArgoCD", Addr: "http://localhost:8080/argocd", Note: "admin"},
		{Label: "Kiali", Addr: "http://localhost:8080/kiali"},
		{Label: "Grafana", Addr: "http://localhost:8080/grafana"},
		{Label: "Prometheus", Addr: "http://localhost:8080/prometheus"},
	})
	golden(t, "url-block", buf.String())
}

func TestURLBlockEmpty(t *testing.T) {
	r, buf, _ := fixture(t)
	r.URLBlock("Platform URLs", nil)
	if !strings.Contains(buf.String(), "endurance bootstrap") {
		t.Errorf("an empty URL block should say what to run:\n%s", buf.String())
	}
}

func TestDashboardGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.Dashboard("Application onboarded",
		[][2]string{
			{"App", r.Value("superheros")},
			{"Namespace", r.Value("superheros")},
			{"Services", "5  (frontend, catalog, orders, inventory, payment)"},
			{"Notify", "webhook:endurance-sink"},
		},
		[]string{
			"git push — ArgoCD deploys from the repo",
			"endurance status superheros",
		})
	golden(t, "dashboard", buf.String())
}

// TestBoxedScreensAreRectangular — alignment is the whole job of a box, and a
// row that overflows the border is the classic way it breaks.
func TestBoxedScreensAreRectangular(t *testing.T) {
	for name, render := range map[string]func(*Renderer){
		"success-screen": func(r *Renderer) { r.SuccessScreen(superherosResult()) },
		"dashboard": func(r *Renderer) {
			r.Dashboard("T", [][2]string{{"k", "v"}, {"longer-key", "value"}}, []string{"next"})
		},
	} {
		r, buf, _ := fixture(t)
		render(r)
		lines := strings.Split(strings.Trim(buf.String(), "\n"), "\n")
		w := lipgloss.Width(lines[0])
		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Errorf("%s: line %d is %d columns, want %d:\n%s",
					name, i, lipgloss.Width(l), w, buf.String())
			}
		}
	}
}
