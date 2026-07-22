package spec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// slackChannel matches a Slack channel name as Slack itself accepts it —
// lowercase, up to 80 characters, and rather more permissive than DNS-1123.
var slackChannel = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

// Stage is one point in an application's life that is worth telling a developer
// about.
//
// The five stages fall into two halves, and which half a stage belongs to is
// the whole design of Phase 5:
//
//	onboarded, requested   — INTENT.   Emitted by the CLI, at the moment a
//	                                   developer's command rewrote the GitOps
//	                                   files. Nothing is deployed yet; the
//	                                   commit may not even be pushed.
//	deploying, healthy, failed — OUTCOME. Emitted by ArgoCD, which is the only
//	                                   thing that talks to the cluster and
//	                                   therefore the only thing that knows.
//
// LaunchPad refuses to blur the line. A CLI that printed "deployed ✅" after
// writing a YAML file would be reporting a fact it cannot possibly have, and
// that specific lie is the entire failure mode of hand-rolled deploy
// notifications — the message arrives, the deploy fails, and nobody finds out
// from the tool that claimed success.
type Stage string

const (
	StageOnboarded Stage = "onboarded"
	StageRequested Stage = "requested"
	StageDeploying Stage = "deploying"
	StageHealthy   Stage = "healthy"
	StageFailed    Stage = "failed"

	// StageTest is `launchpad notify test`. It is never subscribable — it
	// exists so the delivery path can be exercised without waiting for a real
	// deploy, and it deliberately bypasses the event filter so that "did my
	// webhook work?" and "am I subscribed to this stage?" stay separate
	// questions.
	StageTest Stage = "test"
)

// Intent reports whether this stage is something the CLI may claim. Only the
// two intent stages are; everything else is ArgoCD's to report.
func (s Stage) Intent() bool { return s == StageOnboarded || s == StageRequested || s == StageTest }

// AllStages is the subscribable set, in life-cycle order.
var AllStages = []Stage{StageOnboarded, StageRequested, StageDeploying, StageHealthy, StageFailed}

// Notify is an application's declaration of who should hear about it.
//
// Like mesh.enabled it is per-application and opt-in: notifications are a
// property of an application's operations, not of the platform, and the team
// that owns the app is the team that knows which channel it should land in.
type Notify struct {
	Enabled bool `yaml:"enabled"`

	// Slack is a channel name (no leading '#'), delivered through ArgoCD's
	// `slack` notification service — which needs a bot token in the platform's
	// notifications secret.
	Slack string `yaml:"slack,omitempty"`

	// Webhook is the name of a webhook notification service the platform has
	// configured (e.g. `launchpad-sink`, or `slack-incoming` for a Slack
	// incoming-webhook URL, which needs no Slack app at all). Named rather than
	// a URL on purpose: a URL in a committed application spec is a credential
	// in git.
	Webhook string `yaml:"webhook,omitempty"`

	// Stages narrows what is delivered. Empty means every stage — the useful
	// default, because an application that asked to be notified almost never
	// wants to be told about only some of its own deploys.
	Stages []Stage `yaml:"stages,omitempty"`
}

// Wants reports whether this application asked to hear about a stage.
func (n Notify) Wants(s Stage) bool {
	if !n.Enabled {
		return false
	}
	if s == StageTest {
		return true
	}
	if len(n.Stages) == 0 {
		return true
	}
	for _, want := range n.Stages {
		if want == s {
			return true
		}
	}
	return false
}

// Recipients lists the configured destinations as "<service>:<recipient>"
// pairs, for display. Order is stable so a dashboard does not shuffle.
func (n Notify) Recipients() []string {
	var out []string
	if n.Slack != "" {
		out = append(out, "slack:"+n.Slack)
	}
	if n.Webhook != "" {
		out = append(out, "webhook:"+n.Webhook)
	}
	sort.Strings(out)
	return out
}

// StageNames lists the stages this application subscribes to, expanding the
// empty-means-all default so that what is printed is what is delivered.
func (n Notify) StageNames() []string {
	stages := n.Stages
	if len(stages) == 0 {
		stages = AllStages
	}
	out := make([]string, 0, len(stages))
	for _, s := range stages {
		out = append(out, string(s))
	}
	return out
}

// validateNotify checks the notification block is internally consistent.
//
// The one that matters is "enabled with no destination": it is trivially easy
// to write, it is silently inert, and the developer who wrote it believes they
// will be paged. Everything else here is a typo check.
func validateNotify(n Notify) error {
	if !n.Enabled {
		if n.Slack != "" || n.Webhook != "" || len(n.Stages) > 0 {
			return fmt.Errorf("notify is configured but notify.enabled is false — set enabled: true or remove the block")
		}
		return nil
	}
	if n.Slack == "" && n.Webhook == "" {
		return fmt.Errorf("notify.enabled is true but no destination is set — give it a `slack:` channel or a `webhook:` service name")
	}
	if strings.HasPrefix(n.Slack, "#") {
		return fmt.Errorf("notify.slack %q must not start with '#' — ArgoCD takes the bare channel name", n.Slack)
	}
	if n.Slack != "" && !slackChannel.MatchString(n.Slack) {
		return fmt.Errorf("notify.slack %q is not a valid Slack channel name (lowercase alphanumerics, '-', '_' and '.')", n.Slack)
	}
	if n.Webhook != "" && !dns1123.MatchString(n.Webhook) {
		return fmt.Errorf("notify.webhook %q must be the DNS-safe name of a webhook service configured on the platform, not a URL", n.Webhook)
	}
	seen := map[Stage]bool{}
	for _, s := range n.Stages {
		if s == StageTest {
			return fmt.Errorf("notify.stages: %q is not subscribable — it is what `launchpad notify test` sends", s)
		}
		known := false
		for _, k := range AllStages {
			if s == k {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("notify.stages: unknown stage %q — the stages are: %s", s, stageList())
		}
		if seen[s] {
			return fmt.Errorf("notify.stages: duplicate stage %q", s)
		}
		seen[s] = true
	}
	return nil
}

func stageList() string {
	names := make([]string, 0, len(AllStages))
	for _, s := range AllStages {
		names = append(names, string(s))
	}
	return strings.Join(names, ", ")
}

// NotifyWarnings reports notification setups that are valid but will not do
// what the developer meant.
func (a App) NotifyWarnings() []string {
	var out []string
	if !a.Notify.Enabled {
		return nil
	}
	// A Slack channel needs a bot token in the platform's notifications secret,
	// which is an operator action in a different repo — so a developer can
	// commit a perfectly correct subscription and hear nothing, forever, with
	// no error anywhere they would think to look.
	if a.Notify.Slack != "" && a.Notify.Webhook == "" {
		out = append(out, fmt.Sprintf(
			"notify.slack is set to %q — delivery needs a Slack bot token in the platform's argocd-notifications-secret; "+
				"if you only have an incoming-webhook URL, use notify.webhook instead", a.Notify.Slack))
	}
	if len(a.Notify.Stages) > 0 {
		var missing []string
		for _, s := range AllStages {
			if !a.Notify.Wants(s) {
				missing = append(missing, string(s))
			}
		}
		if len(missing) > 0 {
			out = append(out, "notify.stages excludes "+strings.Join(missing, ", ")+
				" — you will not be told when those happen")
		}
	}
	return out
}
