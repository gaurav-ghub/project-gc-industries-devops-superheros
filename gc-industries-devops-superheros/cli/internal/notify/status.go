package notify

import (
	"fmt"
	"strings"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/version"
)

// Status prints who hears about an application and when.
//
// It exists for the same reason `policy list` does: a mechanism that only ever
// speaks when something happens is impossible to trust before it has happened.
// Being able to ask "who would be told if this broke right now?" without
// breaking anything is what makes the subscription reviewable.
func Status(root, appName string) error {
	app, err := gitops.Load(root, appName)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", appName, err)
	}
	render.Banner(version.Current)
	render.Section("Notifications · " + app.Name)

	if !app.Notify.Enabled {
		render.Info("notifications are off — add a notify block to specs/" + app.Name + ".yaml and re-run onboard")
		render.Detail("notify:\n    enabled: true\n    webhook: endurance-sink")
		return nil
	}

	render.Step("Destinations   " + render.Value(strings.Join(app.Notify.Recipients(), "  ")))
	render.Step("Stages         " + render.Value(strings.Join(app.Notify.StageNames(), ", ")))

	render.Section("Who sends what")
	for _, s := range spec.AllStages {
		if !app.Notify.Wants(s) {
			render.Info(fmt.Sprintf("%-10s not subscribed", s))
			continue
		}
		if s.Intent() {
			render.Step(fmt.Sprintf("%-10s %s", s, "the CLI, when it writes the files (intent)"))
			continue
		}
		var triggers []string
		for _, sub := range gitops.Subscriptions(app.Notify) {
			if sub.Stage == s {
				triggers = append(triggers, sub.Trigger+"."+sub.Service)
			}
		}
		render.Step(fmt.Sprintf("%-10s ArgoCD, from the cluster (outcome)", s))
		render.Detail("annotations: " + strings.Join(dedupe(triggers), ", "))
	}

	render.Section("This shell")
	if names := SinkNames(); len(names) > 0 {
		render.Success("CLI events will be delivered to: " + strings.Join(names, ", "))
	} else {
		render.Warn("no CLI webhook configured — " + EnvSlackWebhook + " and " + EnvWebhook + " are both unset")
		render.Detail("the ArgoCD half still works; only the onboarded/requested messages are skipped")
	}
	for _, w := range app.NotifyWarnings() {
		render.Warn(w)
	}
	return nil
}

// TestSend delivers a test event so a developer can prove the wiring works
// without waiting for a real deploy — and, more usefully, without causing one.
func TestSend(root, appName string) error {
	app, err := gitops.Load(root, appName)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", appName, err)
	}
	render.Banner(version.Current)
	render.Section("Notify test · " + app.Name)

	if !app.Notify.Enabled {
		return fmt.Errorf("app %q has notifications disabled — nothing to test", appName)
	}
	if len(Sinks()) == 0 {
		return fmt.Errorf("no CLI webhook configured — set %s or %s and try again", EnvSlackWebhook, EnvWebhook)
	}

	e := New(spec.StageTest, app)
	e.Detail = "sent by `endurance notify test`"
	render.Info(e.Text())
	Send(app, e)

	render.Info("this only exercised the CLI half; the ArgoCD half fires on a real sync")
	return nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
