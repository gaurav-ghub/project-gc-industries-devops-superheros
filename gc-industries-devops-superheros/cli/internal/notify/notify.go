// Package notify is the CLI half of Endurance's developer notifications.
//
// There are two halves, and the split is deliberate. This one sends *intent*:
// the moment a developer's command rewrote the GitOps files, addressed to
// whoever the application's `notify:` block names. The other half lives in
// apps/<name>/application.yaml as ArgoCD subscription annotations and sends
// *outcome* — deploying, healthy, failed — because ArgoCD is the only thing
// that talks to the cluster and therefore the only thing that can honestly say
// whether a deploy worked.
//
// Everything here is best-effort by construction. A notification is a courtesy,
// and a courtesy that can fail a release is worse than no courtesy at all: the
// files are already written and the git history is already correct, so a Slack
// outage must not turn `endurance release` into a non-zero exit that a
// developer then "fixes" by running it again.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"sort"
	"strings"
	"time"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// Environment variables that configure the CLI's own delivery. They are
// environment rather than repo configuration because they are URLs with
// embedded secrets — an incoming-webhook URL *is* the credential, and a
// credential does not belong in a committed application spec.
const (
	EnvSlackWebhook = "ENDURANCE_SLACK_WEBHOOK"
	EnvWebhook      = "ENDURANCE_NOTIFY_WEBHOOK"
)

// timeout bounds a notification attempt. Short on purpose: the developer is
// waiting at a terminal for a command that has already done its real work.
const timeout = 5 * time.Second

// Event is one thing that happened, as the CLI understands it.
type Event struct {
	Stage     spec.Stage `json:"stage"`
	App       string     `json:"app"`
	Namespace string     `json:"namespace"`
	Service   string     `json:"service,omitempty"`
	Version   string     `json:"version,omitempty"`
	Detail    string     `json:"detail,omitempty"`
	Actor     string     `json:"actor"`
	Source    string     `json:"source"`
	Time      string     `json:"time"`
}

// New builds an event, stamping the actor and the time so callers cannot forget
// to. Source is always "endurance-cli": a receiver must be able to tell a
// message that claims intent from one that reports outcome without reading the
// prose.
func New(stage spec.Stage, app spec.App) Event {
	return Event{
		Stage:     stage,
		App:       app.Name,
		Namespace: app.Namespace,
		Actor:     actor(),
		Source:    "endurance-cli",
		Time:      time.Now().UTC().Format(time.RFC3339),
	}
}

// Text is the human sentence a chat client shows.
//
// Every intent message says what was *requested* and states outright that
// nothing is deployed yet. That sentence is the most important string in the
// package: without it a developer reads "release catalog/v2 → v2-abc1234" as
// "catalog v2 is live", which at that moment is false — the commit may not even
// be pushed.
func (e Event) Text() string {
	subject := e.App
	if e.Service != "" {
		subject += "/" + e.Service
		if e.Version != "" {
			subject += "/" + e.Version
		}
	}

	var headline string
	switch e.Stage {
	case spec.StageOnboarded:
		headline = fmt.Sprintf("onboarded %s", subject)
	case spec.StageRequested:
		headline = fmt.Sprintf("change requested for %s", subject)
	case spec.StageTest:
		headline = fmt.Sprintf("test notification for %s", subject)
	default:
		headline = fmt.Sprintf("%s: %s", e.Stage, subject)
	}
	if e.Detail != "" {
		headline += " — " + e.Detail
	}

	line := fmt.Sprintf("Endurance · %s (by %s)", headline, e.Actor)
	switch e.Stage {
	case spec.StageOnboarded, spec.StageRequested:
		line += "\nGitOps files written. Nothing is deployed yet — ArgoCD reports the outcome once this is pushed."
	case spec.StageTest:
		// A test wrote no files either, so it does not get the sentence about
		// files that were written. It says what it is.
		line += "\nDelivery check only — no files were written and nothing was deployed."
	}
	return line
}

// Sink is one place an event can be delivered.
type Sink struct {
	Name string // for log lines
	URL  string
	Body func(Event) ([]byte, error)
}

// Sinks reads the environment and returns where this CLI can deliver.
//
// Two shapes are supported because they answer different questions. A Slack
// incoming-webhook URL is the two-minute path and needs no Slack app at all; a
// generic webhook receives the event as JSON, which is what a local sink, an
// automation, or a future JIRA adapter wants.
func Sinks() []Sink {
	var out []Sink
	if u := strings.TrimSpace(os.Getenv(EnvSlackWebhook)); u != "" {
		out = append(out, Sink{Name: "slack", URL: u, Body: slackBody})
	}
	if u := strings.TrimSpace(os.Getenv(EnvWebhook)); u != "" {
		out = append(out, Sink{Name: "webhook", URL: u, Body: jsonBody})
	}
	return out
}

func slackBody(e Event) ([]byte, error) {
	return json.Marshal(map[string]string{"text": e.Text()})
}

func jsonBody(e Event) ([]byte, error) {
	type payload struct {
		Event
		Text string `json:"text"`
	}
	return json.Marshal(payload{Event: e, Text: e.Text()})
}

// Send delivers an event if the application asked for this stage and the
// environment says where to.
//
// It reports what it did to the terminal and returns nothing. There is no error
// to return: by the time Send is called the command has already succeeded, and
// nothing that happens here may change that.
func Send(app spec.App, e Event) {
	if !app.Notify.Wants(e.Stage) {
		return
	}
	sinks := Sinks()
	if len(sinks) == 0 {
		render.Info(fmt.Sprintf(
			"notify: %s is subscribed but this shell has no webhook — set %s (or %s) to deliver CLI events",
			app.Name, EnvSlackWebhook, EnvWebhook))
		return
	}
	for _, s := range sinks {
		if err := deliver(s, e); err != nil {
			// A warning, never an error. The release happened.
			render.Warn(fmt.Sprintf("notify: %s delivery failed (%v) — the files are written regardless", s.Name, err))
			continue
		}
		render.Success(fmt.Sprintf("notify: %s → %s", e.Stage, s.Name))
	}
}

func deliver(s Sink, e Event) error {
	body, err := s.Body(e)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// actor is who ran the command — the "by" in every message. Best effort: a
// missing username is not worth failing over, and "unknown" is honest.
func actor() string {
	if v := strings.TrimSpace(os.Getenv("ENDURANCE_ACTOR")); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		// Windows reports DOMAIN\user; the domain is noise in a chat message.
		if _, name, ok := strings.Cut(u.Username, `\`); ok {
			return name
		}
		return u.Username
	}
	return "unknown"
}

// SinkNames lists the configured sinks for display, sorted for stability.
func SinkNames() []string {
	var out []string
	for _, s := range Sinks() {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}
