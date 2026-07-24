package gitops

import (
	"sort"

	"github.com/gc-ghub/endurance/internal/spec"
)

// Subscription is one ArgoCD notification subscription, rendered into
// application.yaml as a
// `notifications.argoproj.io/subscribe.<trigger>.<service>` annotation.
type Subscription struct {
	Stage     spec.Stage // the Endurance stage this serves
	Trigger   string     // the ArgoCD trigger that fires it
	Service   string     // the ArgoCD notification service ("slack", or a webhook service name)
	Recipient string     // channel for slack; empty for a webhook service
}

// stageTriggers maps Endurance's outcome stages onto ArgoCD's triggers.
//
// Only the outcome stages appear here, and that absence is the point: there is
// no ArgoCD trigger for `onboarded` or `requested` because ArgoCD does not know
// a developer ran a command — it only ever sees a commit arrive. Those two
// stages are the CLI's to send, and these three are ArgoCD's. Nothing sends
// both.
//
// `failed` maps to two triggers because an application has two distinct ways to
// fail and a developer cares about neither more than the other: the sync itself
// can error, or the sync can succeed onto a workload that then goes Degraded.
// Subscribing to only one of them is how a CrashLoopBackOff goes unreported.
var stageTriggers = map[spec.Stage][]string{
	spec.StageDeploying: {"on-sync-running"},
	spec.StageHealthy:   {"on-deployed"},
	spec.StageFailed:    {"on-health-degraded", "on-sync-failed"},
}

// Subscriptions returns the ArgoCD subscriptions an application's notify block
// asks for, sorted so that a regenerated application.yaml is byte-stable.
func Subscriptions(n spec.Notify) []Subscription {
	if !n.Enabled {
		return nil
	}
	type dest struct{ service, recipient string }
	var dests []dest
	if n.Slack != "" {
		dests = append(dests, dest{"slack", n.Slack})
	}
	if n.Webhook != "" {
		// A webhook service carries its own URL in the platform's
		// notifications ConfigMap, so the subscription names the service and
		// leaves the recipient empty.
		dests = append(dests, dest{n.Webhook, ""})
	}

	var out []Subscription
	for _, stage := range spec.AllStages {
		if !n.Wants(stage) {
			continue
		}
		for _, trigger := range stageTriggers[stage] {
			for _, d := range dests {
				out = append(out, Subscription{
					Stage: stage, Trigger: trigger, Service: d.service, Recipient: d.recipient,
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Trigger != out[j].Trigger {
			return out[i].Trigger < out[j].Trigger
		}
		return out[i].Service < out[j].Service
	})
	return out
}
