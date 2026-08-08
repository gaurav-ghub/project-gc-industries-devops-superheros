package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func superherosResult() Result {
	return Result{
		// Deploying, not deployed, and State left at its zero value — because
		// one of the three pods below is still being created. The title, the
		// glyph and the footer have to agree with the pod list or the screen is
		// arguing with itself.
		Title: "superheros is deploying",
		Rows: [][2]string{
			{"Namespace", "superheros"},
			{"Cluster", "kind-endurance"},
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
			{Label: "ArgoCD", Addr: "http://localhost:8080/argocd", Note: "user admin"},
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

// TestSuccessScreenGlyphFollowsTheResult.
//
// The title's glyph was a hardcoded ✓ until Phase 10, which meant a screen
// headed "cluster not reached" opened with a checkmark — the untruth the rest
// of this type is careful about, in the largest text on the screen. It now
// follows Result.State, and the zero value is pending: a caller that says
// nothing gets `·` rather than a claim.
func TestSuccessScreenGlyphFollowsTheResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		want  string
	}{
		{"unstated", StatePending, IconInfo},
		{"healthy", StateReady, IconOK},
		{"degraded", StateFailed, IconError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, buf, _ := fixture(t)
			res := superherosResult()
			res.State = tc.state
			r.SuccessScreen(res)

			title := ""
			for _, line := range strings.Split(buf.String(), "\n") {
				if strings.Contains(line, res.Title) {
					title = line
					break
				}
			}
			if title == "" {
				t.Fatalf("no title line in:\n%s", buf.String())
			}
			if !strings.Contains(title, tc.want) {
				t.Errorf("title glyph = %q, want %q", title, tc.want)
			}
			if tc.state != StateReady && strings.Contains(title, IconOK) {
				t.Errorf("a result that is not ready opened with a checkmark: %q", title)
			}
		})
	}
}

func credentialSample() []Credential {
	return []Credential{
		{Label: "ArgoCD", Username: "admin", Password: "Xk7pQ2mR9tLw"},
		{Label: "Grafana", Username: "admin", Password: "prom-operator"},
	}
}

func TestCredentialBlockGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.CredentialBlock("Credentials", credentialSample())
	golden(t, "credential-block", buf.String())
}

// TestACredentialWithNoPasswordSaysWhy.
//
// The rule this type carries, and the reason Password and Note are separate
// fields: a password that could not be fetched must not render as an empty
// column, which reads as a blank password and sends someone hunting for a bug
// in ArgoCD. It renders the reason, where the password would have been.
func TestACredentialWithNoPasswordSaysWhy(t *testing.T) {
	r, buf, _ := fixture(t)
	r.CredentialBlock("Credentials", []Credential{
		{Label: "ArgoCD", Username: "admin", Password: "Xk7pQ2mR9tLw"},
		{Label: "Grafana", Username: "admin",
			Note: "no prometheus-grafana secret — is monitoring installed?"},
	})
	got := buf.String()

	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "Grafana") {
			continue
		}
		if !strings.Contains(line, IconWarn) {
			t.Errorf("an unfetchable credential is not marked: %q", line)
		}
		if !strings.Contains(line, "is monitoring installed?") {
			t.Errorf("the reason is missing: %q", line)
		}
		// The giveaway for the bug this guards against: a separator with
		// nothing after it.
		if strings.HasSuffix(strings.TrimRight(line, " "), "/") {
			t.Errorf("a credential rendered an empty password: %q", line)
		}
	}
	// The one that did work is unaffected.
	if !strings.Contains(got, "Xk7pQ2mR9tLw") {
		t.Errorf("the fetched credential was lost:\n%s", got)
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

// TestABoxNeverOutgrowsTheTerminal.
//
// A box is the one shape here a terminal can destroy. Everything else wraps and
// still reads; a box whose border sits past the right edge has every border
// character wrapped onto the following line, and what is left is not a wider box
// but a shredded one — content and borders interleaved. That is what an absolute
// Windows path in an onboard summary did on a real machine.
func TestABoxNeverOutgrowsTheTerminal(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, WithColor(false))

	long := "kubectl apply -f " + strings.Repeat("C:/a/very/long/windows/path", 8) + "/application.yaml"
	r.Dashboard("Application onboarded", [][2]string{{"App", "superheros"}}, []string{long})

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := lipgloss.Width(line); w > MaxBoxWidth {
			t.Errorf("line is %d columns, over the %d limit:\n%s", w, MaxBoxWidth, line)
		}
	}
}

// TestABoxStaysRectangular — the wrapped halves of a long line have to be padded
// like every other line, or the right border staircases inward.
func TestABoxStaysRectangular(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, WithColor(false))

	r.Dashboard("Onboarded", [][2]string{{"App", "x"}},
		[]string{strings.Repeat("word ", 60)})

	var widths []int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		widths = append(widths, lipgloss.Width(line))
	}
	for i, w := range widths {
		if w != widths[0] {
			t.Errorf("line %d is %d columns, the first is %d — the box is not rectangular", i, w, widths[0])
		}
	}
}

// TestTheSuccessScreenHandsOverTheLogins.
//
// The screen used to end with "ArgoCD  http://localhost:8080/argocd  user admin"
// and stop, so the first thing a developer did after a ten-minute install was
// open a second terminal and paste a kubectl/base64 pipeline out of the docs.
// The password was one API call away the whole time.
func TestTheSuccessScreenHandsOverTheLogins(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, WithColor(false))

	r.SuccessScreen(Result{
		Title: "superheros is deployed and healthy",
		State: StateReady,
		URLs:  []URL{{Label: "ArgoCD", Addr: "http://localhost:8080/argocd"}},
		Logins: []Credential{
			{Label: "ArgoCD", Username: "admin", Password: "mQiZu-MdIGGbDycv"},
			{Label: "Grafana", Username: "admin", Password: "prom-operator"},
		},
	})

	got := buf.String()
	for _, want := range []string{"Logins", "mQiZu-MdIGGbDycv", "prom-operator"} {
		if !strings.Contains(got, want) {
			t.Errorf("the screen does not carry %q:\n%s", want, got)
		}
	}
}

// TestNoLoginsMeansNoSection — a caller that could not reach the cluster prints
// no block rather than a block full of apologies, and a run recorded with
// ENDURANCE_NO_CREDENTIALS set prints none either.
func TestNoLoginsMeansNoSection(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, WithColor(false))

	r.SuccessScreen(Result{Title: "superheros — cluster not reached", State: StateWarn})

	if strings.Contains(buf.String(), "Logins") {
		t.Errorf("an empty login list still drew a section:\n%s", buf.String())
	}
}
