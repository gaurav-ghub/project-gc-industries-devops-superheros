package spec

import (
	"strings"
	"testing"
)

func notifyApp(n Notify) App {
	return App{
		Name:      "superheros",
		Namespace: "superheros",
		Notify:    n,
		Services: []Service{
			{Name: "catalog", Image: "docker.io/dockergc00/superheros-catalog", Tag: "v1", Port: 8081, Replicas: 1},
		},
	}
}

func TestNotifyEnabledWithoutADestinationIsRejected(t *testing.T) {
	// The failure this guards against is silent: a developer writes
	// `enabled: true`, believes they are subscribed, and hears nothing forever.
	err := notifyApp(Notify{Enabled: true}).Validate()
	if err == nil {
		t.Fatal("expected an error for notify.enabled with no destination")
	}
	if !strings.Contains(err.Error(), "no destination") {
		t.Errorf("error should name the problem, got: %v", err)
	}
}

func TestNotifyConfiguredButDisabledIsRejected(t *testing.T) {
	err := notifyApp(Notify{Slack: "superheros-deploys"}).Validate()
	if err == nil {
		t.Fatal("expected an error for a configured-but-disabled notify block")
	}
}

func TestNotifyAcceptsEitherDestination(t *testing.T) {
	for _, n := range []Notify{
		{Enabled: true, Slack: "superheros-deploys"},
		{Enabled: true, Webhook: "endurance-sink"},
		{Enabled: true, Slack: "superheros-deploys", Webhook: "endurance-sink"},
	} {
		if err := notifyApp(n).Validate(); err != nil {
			t.Errorf("%+v should be valid: %v", n, err)
		}
	}
}

func TestNotifyRejectsAWebhookURL(t *testing.T) {
	// A URL here would be a credential in git — the whole reason webhooks are
	// referenced by name and configured on the platform. (The host is a
	// deliberately unroutable one: even a fake credential-shaped string in a
	// committed file is something secret scanners are right to reject.)
	err := notifyApp(Notify{Enabled: true, Webhook: "https://hooks.slack.example/services/AAA/BBB/ccc"}).Validate()
	if err == nil {
		t.Fatal("expected a URL to be rejected as a webhook service name")
	}
	if !strings.Contains(err.Error(), "not a URL") {
		t.Errorf("error should explain why, got: %v", err)
	}
}

func TestNotifyRejectsHashPrefixedChannel(t *testing.T) {
	if err := notifyApp(Notify{Enabled: true, Slack: "#deploys"}).Validate(); err == nil {
		t.Fatal("expected '#deploys' to be rejected")
	}
}

func TestNotifyRejectsUnknownAndDuplicateStages(t *testing.T) {
	bad := notifyApp(Notify{Enabled: true, Webhook: "sink", Stages: []Stage{"deployed"}})
	if err := bad.Validate(); err == nil {
		t.Error("expected unknown stage to be rejected")
	}
	dup := notifyApp(Notify{Enabled: true, Webhook: "sink", Stages: []Stage{StageHealthy, StageHealthy}})
	if err := dup.Validate(); err == nil {
		t.Error("expected duplicate stage to be rejected")
	}
	test := notifyApp(Notify{Enabled: true, Webhook: "sink", Stages: []Stage{StageTest}})
	if err := test.Validate(); err == nil {
		t.Error("expected the test stage to be unsubscribable")
	}
}

func TestWantsDefaultsToEveryStage(t *testing.T) {
	n := Notify{Enabled: true, Webhook: "sink"}
	for _, s := range AllStages {
		if !n.Wants(s) {
			t.Errorf("an empty stage list should mean every stage; %q was excluded", s)
		}
	}
	if len(n.StageNames()) != len(AllStages) {
		t.Errorf("StageNames should expand the default, got %v", n.StageNames())
	}
}

func TestWantsNarrowsToTheNamedStages(t *testing.T) {
	n := Notify{Enabled: true, Webhook: "sink", Stages: []Stage{StageFailed}}
	if !n.Wants(StageFailed) {
		t.Error("failed should be wanted")
	}
	if n.Wants(StageHealthy) {
		t.Error("healthy should not be wanted")
	}
	// The test stage is a delivery probe, not a subscription — it must survive
	// any narrowing, or `notify test` would answer a different question than
	// the one it was asked.
	if !n.Wants(StageTest) {
		t.Error("the test stage must bypass the stage filter")
	}
}

func TestDisabledNotifyWantsNothing(t *testing.T) {
	n := Notify{}
	for _, s := range append(AllStages, StageTest) {
		if n.Wants(s) {
			t.Errorf("disabled notify should want nothing; wanted %q", s)
		}
	}
}

func TestOnlyIntentStagesAreTheCLIs(t *testing.T) {
	// This is the phase's central rule: the CLI may claim what was asked for,
	// never what happened. If this test ever goes green for `healthy` the CLI
	// has started reporting a fact it cannot possibly have.
	for _, s := range []Stage{StageOnboarded, StageRequested, StageTest} {
		if !s.Intent() {
			t.Errorf("%q should be an intent stage", s)
		}
	}
	for _, s := range []Stage{StageDeploying, StageHealthy, StageFailed} {
		if s.Intent() {
			t.Errorf("%q is an outcome — only ArgoCD may report it", s)
		}
	}
}

func TestNotifyWarnsAboutSlackOnlyDelivery(t *testing.T) {
	app := notifyApp(Notify{Enabled: true, Slack: "superheros-deploys"})
	warnings := app.NotifyWarnings()
	if len(warnings) == 0 || !strings.Contains(warnings[0], "bot token") {
		t.Errorf("a Slack-only setup should warn about the token it needs, got %v", warnings)
	}
}

func TestNotifyWarnsAboutExcludedStages(t *testing.T) {
	app := notifyApp(Notify{Enabled: true, Webhook: "sink", Stages: []Stage{StageHealthy}})
	warnings := app.NotifyWarnings()
	joined := strings.Join(warnings, " ")
	if !strings.Contains(joined, "failed") {
		t.Errorf("excluding `failed` is worth saying out loud, got %v", warnings)
	}
}

func TestRecipientsAreStable(t *testing.T) {
	n := Notify{Enabled: true, Slack: "deploys", Webhook: "endurance-sink"}
	got := strings.Join(n.Recipients(), ",")
	if got != "slack:deploys,webhook:endurance-sink" {
		t.Errorf("recipients should be stable and sorted, got %q", got)
	}
}
