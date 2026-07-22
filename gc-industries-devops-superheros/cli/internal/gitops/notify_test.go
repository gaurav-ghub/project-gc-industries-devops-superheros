package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/launchpad/internal/spec"
	"gopkg.in/yaml.v3"
)

func notifiedApp(n spec.Notify) spec.App {
	app := sampleApp()
	app.Notify = n
	return app
}

func generatedApplication(t *testing.T, app spec.App) string {
	t.Helper()
	root := t.TempDir()
	if _, err := Generate(root, app, "https://example.com/platform.git", ""); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "apps", app.Name, "application.yaml"))
	if err != nil {
		t.Fatalf("reading application.yaml: %v", err)
	}
	return string(data)
}

func TestNoSubscriptionsWithoutNotify(t *testing.T) {
	if subs := Subscriptions(spec.Notify{}); subs != nil {
		t.Fatalf("expected no subscriptions, got %v", subs)
	}
	// And the file must be untouched, not merely subscription-free: every
	// application generated before Phase 5 has to keep rendering byte-identically.
	if got := generatedApplication(t, sampleApp()); strings.Contains(got, "annotations") {
		t.Errorf("an app with no notify block must render no annotations:\n%s", got)
	}
}

func TestIntentStagesGetNoArgoCDTrigger(t *testing.T) {
	// ArgoCD never sees a developer run a command — it only sees a commit
	// arrive. If either of these ever gained a trigger, the same event would be
	// announced twice and the intent/outcome split would be gone.
	for _, s := range []spec.Stage{spec.StageOnboarded, spec.StageRequested} {
		subs := Subscriptions(spec.Notify{Enabled: true, Webhook: "sink", Stages: []spec.Stage{s}})
		if len(subs) != 0 {
			t.Errorf("stage %q should produce no ArgoCD subscription, got %v", s, subs)
		}
	}
}

func TestFailedSubscribesToBothWaysToFail(t *testing.T) {
	// A sync can error, or it can succeed onto a workload that then goes
	// Degraded. Subscribing to only one is how a CrashLoopBackOff goes unheard.
	subs := Subscriptions(spec.Notify{Enabled: true, Webhook: "sink", Stages: []spec.Stage{spec.StageFailed}})
	got := map[string]bool{}
	for _, s := range subs {
		got[s.Trigger] = true
	}
	for _, want := range []string{"on-health-degraded", "on-sync-failed"} {
		if !got[want] {
			t.Errorf("missing trigger %q, got %v", want, got)
		}
	}
}

func TestEveryDestinationGetsEveryTrigger(t *testing.T) {
	subs := Subscriptions(spec.Notify{Enabled: true, Slack: "deploys", Webhook: "launchpad-sink"})
	// 4 outcome triggers x 2 destinations.
	if len(subs) != 8 {
		t.Fatalf("expected 8 subscriptions, got %d: %v", len(subs), subs)
	}
	for _, s := range subs {
		if s.Service == "slack" && s.Recipient != "deploys" {
			t.Errorf("slack subscription should carry the channel, got %q", s.Recipient)
		}
		if s.Service == "launchpad-sink" && s.Recipient != "" {
			t.Errorf("a webhook service carries its URL on the platform, so its recipient must be empty, got %q", s.Recipient)
		}
	}
}

func TestSubscriptionsAreRenderedAsAnnotations(t *testing.T) {
	got := generatedApplication(t, notifiedApp(spec.Notify{
		Enabled: true, Slack: "superheros-deploys", Webhook: "launchpad-sink",
	}))
	for _, want := range []string{
		`notifications.argoproj.io/subscribe.on-deployed.slack: "superheros-deploys"`,
		`notifications.argoproj.io/subscribe.on-deployed.launchpad-sink: ""`,
		`notifications.argoproj.io/subscribe.on-sync-running.slack: "superheros-deploys"`,
		`notifications.argoproj.io/subscribe.on-health-degraded.launchpad-sink: ""`,
		`notifications.argoproj.io/subscribe.on-sync-failed.slack: "superheros-deploys"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing annotation %s in:\n%s", want, got)
		}
	}
}

func TestGeneratedApplicationIsStillValidYAMLWithAnnotations(t *testing.T) {
	got := generatedApplication(t, notifiedApp(spec.Notify{Enabled: true, Webhook: "launchpad-sink"}))
	var obj struct {
		Metadata struct {
			Name        string            `yaml:"name"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("application.yaml is not valid YAML: %v\n%s", err, got)
	}
	if obj.Metadata.Name != "superheros" {
		t.Errorf("name lost, got %q", obj.Metadata.Name)
	}
	if len(obj.Metadata.Annotations) != 4 {
		t.Errorf("expected 4 subscription annotations, got %v", obj.Metadata.Annotations)
	}
}

func TestSubscriptionsAreByteStableAcrossRuns(t *testing.T) {
	// application.yaml is regenerated on every onboard; a map iteration leaking
	// into the output would show up as a spurious diff in every pull request.
	app := notifiedApp(spec.Notify{Enabled: true, Slack: "deploys", Webhook: "launchpad-sink"})
	first := generatedApplication(t, app)
	for i := 0; i < 5; i++ {
		if got := generatedApplication(t, app); got != first {
			t.Fatalf("run %d differs:\n%s\n---\n%s", i, first, got)
		}
	}
}

func TestNarrowedStagesNarrowTheAnnotations(t *testing.T) {
	got := generatedApplication(t, notifiedApp(spec.Notify{
		Enabled: true, Webhook: "launchpad-sink", Stages: []spec.Stage{spec.StageFailed},
	}))
	if strings.Contains(got, "on-deployed") {
		t.Errorf("healthy was not subscribed, so on-deployed must not appear:\n%s", got)
	}
	if !strings.Contains(got, "on-sync-failed") {
		t.Errorf("failed was subscribed, so on-sync-failed must appear:\n%s", got)
	}
}

func TestASubscriptionNeverReachesAWorkload(t *testing.T) {
	// The same rule Phase 4 established for a canary weight: configuration that
	// belongs to the platform must not end up in a pod template, or changing who
	// gets paged would restart the application. values.yaml is the chart's
	// input, so nothing about notifications may appear in it.
	root := t.TempDir()
	app := notifiedApp(spec.Notify{Enabled: true, Slack: "superheros-deploys", Webhook: "launchpad-sink"})
	if _, err := Generate(root, app, "r", ""); err != nil {
		t.Fatal(err)
	}
	values, err := os.ReadFile(filepath.Join(root, "apps", "superheros", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"notify", "notifications", "launchpad-sink", "superheros-deploys"} {
		if strings.Contains(string(values), forbidden) {
			t.Errorf("values.yaml must not mention %q — it would put a subscription in a pod spec:\n%s", forbidden, values)
		}
	}
}

func TestReleaseDoesNotRewriteSubscriptions(t *testing.T) {
	// application.yaml is onboarding's output. Phase 2 asserted a release never
	// touches it; now that it also carries who gets paged, that matters more.
	root := t.TempDir()
	app := notifiedApp(spec.Notify{Enabled: true, Webhook: "launchpad-sink"})
	if _, err := Generate(root, app, "r", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "apps", "superheros", "application.yaml")
	before, _ := os.ReadFile(path)
	if _, err := SetServiceTag(root, "superheros", "frontend", "", "v9"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("release rewrote application.yaml:\n%s\n---\n%s", before, after)
	}
}
