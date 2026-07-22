package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gc-ghub/launchpad/internal/spec"
)

func subscribedApp() spec.App {
	return spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Notify:    spec.Notify{Enabled: true, Webhook: "launchpad-sink"},
	}
}

// recorder is a webhook that remembers what it was sent.
type recorder struct {
	server *httptest.Server
	bodies chan string
	status int
	hits   int32
}

func newRecorder(status int) *recorder {
	r := &recorder{bodies: make(chan string, 8), status: status}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&r.hits, 1)
		body, _ := io.ReadAll(req.Body)
		select {
		case r.bodies <- string(body):
		default:
		}
		w.WriteHeader(r.status)
	}))
	return r
}

func (r *recorder) close() { r.server.Close() }

func (r *recorder) body(t *testing.T) string {
	t.Helper()
	select {
	case b := <-r.bodies:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived")
		return ""
	}
}

func TestIntentMessagesSayNothingIsDeployedYet(t *testing.T) {
	// The most important string in the package. Without it, "release
	// catalog/v2 → v2-abc1234" reads as "catalog v2 is live", which at the
	// moment the CLI sends it is false — the commit may not even be pushed.
	for _, stage := range []spec.Stage{spec.StageOnboarded, spec.StageRequested} {
		e := New(stage, subscribedApp())
		if !strings.Contains(e.Text(), "Nothing is deployed yet") {
			t.Errorf("stage %q must not imply a deploy happened:\n%s", stage, e.Text())
		}
		if !strings.Contains(e.Text(), "ArgoCD reports the outcome") {
			t.Errorf("stage %q should say who will report the outcome:\n%s", stage, e.Text())
		}
	}
}

func TestATestNotificationDoesNotClaimFilesWereWritten(t *testing.T) {
	// `notify test` writes nothing, so it must not borrow the release
	// disclaimer — the whole discipline here is not saying things that are not
	// true, and "GitOps files written" would be one of them.
	got := New(spec.StageTest, subscribedApp()).Text()
	if strings.Contains(got, "GitOps files written") {
		t.Errorf("a test notification wrote no files:\n%s", got)
	}
	if !strings.Contains(got, "Delivery check only") {
		t.Errorf("a test notification should say what it is:\n%s", got)
	}
}

func TestOutcomeMessagesDoNotCarryTheIntentDisclaimer(t *testing.T) {
	// If ArgoCD's own message ever went through here, appending "nothing is
	// deployed yet" to a healthy notification would be its own kind of lie.
	e := New(spec.StageHealthy, subscribedApp())
	if strings.Contains(e.Text(), "Nothing is deployed yet") {
		t.Errorf("an outcome stage must not carry the intent disclaimer:\n%s", e.Text())
	}
}

func TestTextNamesTheServiceAndVersion(t *testing.T) {
	e := New(spec.StageRequested, subscribedApp())
	e.Service, e.Version, e.Detail = "catalog", "v2", "v2-old → v2-new"
	got := e.Text()
	for _, want := range []string{"superheros/catalog/v2", "v2-old → v2-new"} {
		if !strings.Contains(got, want) {
			t.Errorf("message should contain %q:\n%s", want, got)
		}
	}
}

func TestSendDeliversToASlackWebhookAsText(t *testing.T) {
	rec := newRecorder(http.StatusOK)
	defer rec.close()
	t.Setenv(EnvSlackWebhook, rec.server.URL)
	t.Setenv(EnvWebhook, "")

	Send(subscribedApp(), New(spec.StageOnboarded, subscribedApp()))

	var payload map[string]any
	if err := json.Unmarshal([]byte(rec.body(t)), &payload); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	// Slack's incoming-webhook contract is a single `text` field and nothing else.
	if len(payload) != 1 {
		t.Errorf("a Slack webhook body should be exactly {text}, got %v", payload)
	}
	if s, _ := payload["text"].(string); !strings.Contains(s, "onboarded superheros") {
		t.Errorf("text = %q", s)
	}
}

func TestSendDeliversToAWebhookAsStructuredJSON(t *testing.T) {
	rec := newRecorder(http.StatusOK)
	defer rec.close()
	t.Setenv(EnvSlackWebhook, "")
	t.Setenv(EnvWebhook, rec.server.URL)

	app := subscribedApp()
	e := New(spec.StageRequested, app)
	e.Service, e.Detail = "catalog", "v1 → v2"
	Send(app, e)

	var got Event
	if err := json.Unmarshal([]byte(rec.body(t)), &got); err != nil {
		t.Fatalf("body is not an Event: %v", err)
	}
	if got.Stage != spec.StageRequested || got.App != "superheros" || got.Service != "catalog" {
		t.Errorf("event lost fields in transit: %+v", got)
	}
	// A receiver must be able to tell intent from outcome without reading prose.
	if got.Source != "launchpad-cli" {
		t.Errorf("source = %q, want launchpad-cli", got.Source)
	}
	if got.Time == "" || got.Actor == "" {
		t.Errorf("time and actor should always be stamped: %+v", got)
	}
}

func TestSendReachesEveryConfiguredSink(t *testing.T) {
	slack, hook := newRecorder(http.StatusOK), newRecorder(http.StatusOK)
	defer slack.close()
	defer hook.close()
	t.Setenv(EnvSlackWebhook, slack.server.URL)
	t.Setenv(EnvWebhook, hook.server.URL)

	Send(subscribedApp(), New(spec.StageOnboarded, subscribedApp()))
	slack.body(t)
	hook.body(t)
}

func TestSendSkipsAStageTheAppDidNotSubscribeTo(t *testing.T) {
	rec := newRecorder(http.StatusOK)
	defer rec.close()
	t.Setenv(EnvWebhook, rec.server.URL)
	t.Setenv(EnvSlackWebhook, "")

	app := subscribedApp()
	app.Notify.Stages = []spec.Stage{spec.StageFailed}
	Send(app, New(spec.StageOnboarded, app))

	if n := atomic.LoadInt32(&rec.hits); n != 0 {
		t.Errorf("expected no delivery, got %d", n)
	}
}

func TestSendSkipsWhenNotificationsAreOff(t *testing.T) {
	rec := newRecorder(http.StatusOK)
	defer rec.close()
	t.Setenv(EnvWebhook, rec.server.URL)

	app := spec.App{Name: "superheros", Namespace: "superheros"}
	Send(app, New(spec.StageOnboarded, app))
	if n := atomic.LoadInt32(&rec.hits); n != 0 {
		t.Errorf("expected no delivery, got %d", n)
	}
}

func TestAFailingWebhookIsNotFatal(t *testing.T) {
	// By the time Send is called the files are written and the commit is made.
	// A Slack outage must not be able to fail a release — a developer who sees
	// a non-zero exit will run the command again.
	rec := newRecorder(http.StatusInternalServerError)
	defer rec.close()
	t.Setenv(EnvWebhook, rec.server.URL)
	t.Setenv(EnvSlackWebhook, "")

	Send(subscribedApp(), New(spec.StageOnboarded, subscribedApp())) // must not panic
	rec.body(t)
}

func TestAnUnreachableWebhookIsNotFatal(t *testing.T) {
	rec := newRecorder(http.StatusOK)
	rec.close() // nothing is listening now
	t.Setenv(EnvWebhook, rec.server.URL)
	t.Setenv(EnvSlackWebhook, "")

	Send(subscribedApp(), New(spec.StageOnboarded, subscribedApp())) // must not panic or block
}

func TestSinksAreDrivenByTheEnvironmentOnly(t *testing.T) {
	// Webhook URLs are credentials; they are read from the environment and are
	// deliberately not a field of the committed application spec.
	t.Setenv(EnvSlackWebhook, "")
	t.Setenv(EnvWebhook, "")
	if got := Sinks(); len(got) != 0 {
		t.Fatalf("expected no sinks, got %v", got)
	}
	t.Setenv(EnvSlackWebhook, "  https://hooks.example.invalid/x  ")
	sinks := Sinks()
	if len(sinks) != 1 || sinks[0].Name != "slack" {
		t.Fatalf("expected one slack sink, got %+v", sinks)
	}
	if sinks[0].URL != "https://hooks.example.invalid/x" {
		t.Errorf("URL should be trimmed, got %q", sinks[0].URL)
	}
}

func TestActorIsOverridable(t *testing.T) {
	t.Setenv("LAUNCHPAD_ACTOR", "ci-bot")
	if got := New(spec.StageOnboarded, subscribedApp()).Actor; got != "ci-bot" {
		t.Errorf("actor = %q", got)
	}
}
