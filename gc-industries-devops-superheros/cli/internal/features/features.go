// Package features implements `endurance enable`, `endurance disable` and
// `endurance config list` — the two optional capabilities of this platform and
// the command that says whether they are on.
//
// # The rule this package is built around
//
// A key is captured, never echoed, and never printed back. Not at the prompt
// (the input is masked), not in a confirmation, not in an error message, not in
// `config list`, and not by any code path here that could ever be added later.
// The only place these values exist is the git-ignored file they are written to
// and the cluster Secret made from it.
//
// That is deliberately stricter than the rest of the CLI, and the difference is
// the point. Phase 10 decided that Endurance hands over ArgoCD's and Grafana's
// logins — those two, and the list is closed — because they belong to a
// single-user kind cluster that is deleted at the end of a demo, and because a
// developer should not have to run kubectl to log in. An OpenAI API key is
// nothing like that: it spends real money on an account that outlives every
// cluster, and a Slack incoming-webhook URL lets whoever holds it post into a
// real workspace. Neither is the platform's to hand out.
//
// So `config list` answers a different question from `endurance urls`: not
// "what is the password" but "is one configured", which is the question a
// developer actually has when enrichment is silently not enriching.
//
// # Nothing here installs anything
//
// enable/disable write (or delete) the two git-ignored files the Phase 5 and
// Phase 6 bash modules already read, and — for the AI Secret, which is a plain
// manifest — apply it so the change takes effect without a ten-minute
// bootstrap. Slack's is a Helm values file, so it takes effect when the gitops
// module next runs, and this says so rather than pretending otherwise.
package features

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/notify"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// The two optional capabilities, and the git-ignored file each one is
// configured by. Both paths are the ones the bash modules already read — this
// package adds a way to write them, not a second place to look.
const (
	AISecretFile   = "platform/ai/secret.yaml"
	AIExampleFile  = "platform/ai/secret.example.yaml"
	AISecretName   = "superhero-ai-secret"
	AINamespace    = "monitoring"
	SlackFile      = "platform/gitops/argocd/values.slack.yaml"
	SlackExample   = "platform/gitops/argocd/values.slack.yaml.example"
	placeholderTag = "<your-" // what the example files use, and what a half-filled copy still has
)

// A Feature is one optional capability.
type Feature struct {
	Name    string // the word a user types: `endurance enable ai`
	Title   string
	File    string // repo-relative, git-ignored
	Example string // repo-relative, committed
	What    string // one line: what turning it on does
	Applies string // one line: when the change takes effect
}

// Features is the closed set. Adding one means adding a module that reads it;
// there is no generic key store here and there should not be.
var Features = []Feature{
	{
		Name: "ai", Title: "AI alert enrichment",
		File: AISecretFile, Example: AIExampleFile,
		What:    "Prometheus alerts are explained by a model before they reach Slack",
		Applies: "applied to the cluster now, if it is reachable",
	},
	{
		Name: "slack", Title: "ArgoCD deploy notifications to Slack",
		File: SlackFile, Example: SlackExample,
		What:    "ArgoCD posts deploying / healthy / failed to a Slack channel",
		Applies: "a Helm values file — it takes effect on the next `endurance bootstrap`",
	},
}

// Find returns the named feature.
func Find(name string) (Feature, error) {
	for _, f := range Features {
		if f.Name == name {
			return f, nil
		}
	}
	return Feature{}, fmt.Errorf("unknown feature %q — enable or disable one of: %s",
		name, strings.Join(Names(), ", "))
}

// Names lists the features, for an error message that tells a user what they
// could have typed.
func Names() []string {
	out := make([]string, 0, len(Features))
	for _, f := range Features {
		out = append(out, f.Name)
	}
	return out
}

// An Asker collects one secret from the user. Tests replace it; the CLI uses
// the masked prompt below and nothing else.
//
// The signature returns only an error alongside the value because a caller must
// never be tempted to log what it got.
type Asker func(title, description string, required bool) (string, error)

// Options configures enable/disable.
type Options struct {
	Root string
	Yes  bool // disable: skip the confirmation

	Ask     Asker
	Confirm func(prompt string) (bool, error)
	Kubectl func(args ...string) (string, error)
	Now     func() string // tests pin the timestamp in the generated header
}

func realKubectl(args ...string) (string, error) {
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	return string(out), err
}

// resolveKubectl returns the kubectl to use, or nil when there is none. nil is
// a legitimate answer: writing the file is useful on a machine with no cluster,
// and the command says which half it did.
func resolveKubectl(k func(args ...string) (string, error)) func(args ...string) (string, error) {
	if k != nil {
		return k
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		return nil
	}
	return realKubectl
}

// Enable captures the credentials for a feature and writes them.
func Enable(name string, opts Options) error {
	render.Banner(version.Current)
	f, err := Find(name)
	if err != nil {
		return err
	}
	root, err := platform.FindRoot(opts.Root)
	if err != nil {
		return err
	}

	render.Section("Enable · " + f.Title)
	render.Info(f.What)
	render.Detail("written to " + f.File + " — git-ignored, and never printed back")
	render.Blank()

	ask := opts.Ask
	if ask == nil {
		ask = askSecret
	}

	switch f.Name {
	case "ai":
		return enableAI(root, f, opts, ask)
	case "slack":
		return enableSlack(root, f, opts, ask)
	}
	return fmt.Errorf("no capture flow for %q", f.Name)
}

func enableAI(root string, f Feature, opts Options, ask Asker) error {
	key, err := ask("OpenAI API key",
		"spends on your OpenAI account · input is masked and the value is never echoed", true)
	if err != nil {
		return err
	}
	hook, err := ask("Slack incoming-webhook URL",
		"where the enriched alert is posted · leave empty to skip and enrich into the logs only", false)
	if err != nil {
		return err
	}
	if err := ValidateWebhook(hook); err != nil {
		return err
	}

	path, err := WriteAI(root, key, hook, stamp(opts))
	if err != nil {
		return err
	}
	render.Success("wrote " + f.File)
	render.Detail(describeKeys(map[string]bool{
		"OPENAI_API_KEY":    key != "",
		"SLACK_WEBHOOK_URL": hook != "",
	}))

	// The Secret is a plain manifest, so it can be applied on its own. Doing so
	// is what makes `enable ai` mean something on a running platform rather than
	// a note to self for the next bootstrap.
	kube := resolveKubectl(opts.Kubectl)
	render.Blank()
	if kube == nil {
		render.Info("kubectl is not on PATH — the Secret is applied by the next `endurance bootstrap`")
		return nil
	}
	if out, err := ApplyAISecret(path, kube); err != nil {
		render.Warn("could not apply it to the cluster: " + firstLine(out))
		render.Detail("the file is written · `endurance bootstrap` applies it, or `kubectl apply -f " + f.File + "`")
		return nil
	}
	render.Success("applied Secret " + AISecretName + " to namespace " + AINamespace)
	render.Info("the enricher reads it at startup — restart it to pick this up:")
	render.Detail("kubectl -n " + AINamespace + " rollout restart deployment/superhero-ai-alertmanager")
	return nil
}

// WriteAI writes the AI module's credentials Secret and returns its path.
//
// Exported for `endurance init`, which captures the same two values during its
// prompt phase and must write them **before** the bootstrap runs —
// platform/ai/install.sh applies this file if it is there, so writing it first
// is what makes "enable AI" part of a first run rather than a follow-up.
func WriteAI(root, key, hook, when string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(AISecretFile))
	return path, writeFile(path, aiSecretYAML(key, hook, when))
}

// WriteSlack writes the ArgoCD notification values and returns its path. Same
// reason as WriteAI: platform/gitops/argocd/install.sh passes this file to helm
// when it exists, so it has to be on disk before the module runs.
func WriteSlack(root, hook, when string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(SlackFile))
	return path, writeFile(path, slackValuesYAML(hook, when))
}

// ApplyAISecret applies an already-written Secret manifest, returning kubectl's
// output so a caller can report the reason rather than the fact.
//
// It applies a file this package wrote and reads nothing back. The distinction
// matters: every other cluster call in this package asks whether a resource
// exists, never what is in it.
func ApplyAISecret(path string, kube func(args ...string) (string, error)) (string, error) {
	if kube == nil {
		return "", fmt.Errorf("kubectl is not available")
	}
	return kube("apply", "-f", path)
}

func enableSlack(root string, f Feature, opts Options, ask Asker) error {
	hook, err := ask("Slack incoming-webhook URL",
		"ArgoCD posts deploy outcomes here · input is masked and the value is never echoed", true)
	if err != nil {
		return err
	}
	if err := ValidateWebhook(hook); err != nil {
		return err
	}

	if _, err := WriteSlack(root, hook, stamp(opts)); err != nil {
		return err
	}
	render.Success("wrote " + f.File)
	render.Detail(describeKeys(map[string]bool{"slack-incoming-url": true}))
	render.Blank()
	// No apply here, and the reason is worth saying: this is Helm values, and
	// half-applying a chart's values by hand is how a cluster ends up in a state
	// no file describes.
	render.Info("this is a Helm values file · " + f.Applies)
	render.Detail("in an application spec: notify: {enabled: true, webhook: slack-incoming}")
	return nil
}

// Disable removes a feature's credentials.
func Disable(name string, opts Options) error {
	render.Banner(version.Current)
	f, err := Find(name)
	if err != nil {
		return err
	}
	root, err := platform.FindRoot(opts.Root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(f.File))

	render.Section("Disable · " + f.Title)

	_, statErr := os.Stat(path)
	onDisk := statErr == nil
	kube := resolveKubectl(opts.Kubectl)
	inCluster := f.Name == "ai" && kube != nil && secretExists(kube)

	switch {
	case !onDisk && !inCluster:
		render.Info(f.File + " is not there — nothing to disable")
		return nil
	case onDisk:
		render.Info("this deletes " + render.Value(f.File))
	}
	if inCluster {
		render.Info("and the Secret " + render.Value(AISecretName) + " in namespace " + AINamespace)
	}
	render.Detail("the credentials themselves are not recoverable from here — you will have to paste them again")
	render.Blank()

	if !opts.Yes {
		confirm := opts.Confirm
		if confirm == nil {
			confirm = askConfirm
		}
		ok, err := confirm("Disable " + f.Title + "?")
		if err != nil {
			return err
		}
		if !ok {
			render.Info("nothing was removed")
			return nil
		}
	}

	if onDisk {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing %s: %w", f.File, err)
		}
		render.Success("removed " + f.File)
	}
	if inCluster {
		if out, err := kube("-n", AINamespace, "delete", "secret", AISecretName); err != nil {
			render.Warn("could not delete the Secret: " + firstLine(out))
			render.Detail("kubectl -n " + AINamespace + " delete secret " + AISecretName)
		} else {
			render.Success("deleted Secret " + AISecretName)
			render.Info("the enricher keeps running and forwards alerts un-enriched — that is by design")
		}
	}
	if f.Name == "slack" {
		render.Blank()
		render.Warn("ArgoCD keeps the notification config it was installed with")
		render.Detail("re-run `endurance bootstrap` to install it without Slack")
	}
	return nil
}

// secretExists reports whether the cluster holds the AI Secret. It asks for the
// resource, never for its contents — this package reads no secret values from
// anywhere, which is the property the tests pin.
func secretExists(kube func(args ...string) (string, error)) bool {
	_, err := kube("-n", AINamespace, "get", "secret", AISecretName, "-o", "name")
	return err == nil
}

// A Setting is one line of `endurance config list`: what it is, whether it is
// on, and how it was decided. Never what it is set to.
type Setting struct {
	Name  string
	State render.State
	Note  string
}

// ConfigOptions configures `endurance config list`.
type ConfigOptions struct {
	Root    string
	Kubectl func(args ...string) (string, error)
	Getenv  func(string) string
}

// ConfigList reports which optional features are configured.
//
// Presence, never values. See the package comment for why this is the one
// place in the CLI that refuses to print what it knows.
func ConfigList(opts ConfigOptions) error {
	render.Banner(version.Current)
	root, err := platform.FindRoot(opts.Root)
	if err != nil {
		return err
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	render.Section("Configuration · " + shortRoot(root))
	settings := Settings(root, resolveKubectl(opts.Kubectl), getenv)
	render.Checks(toChecks(settings))
	render.Blank()

	render.Info("presence, never values — nothing here prints a key, a token or a webhook URL")
	render.Detail("`endurance enable ai` / `enable slack` capture them · `disable` removes them")
	render.Detail("`endurance urls` does print ArgoCD's and Grafana's logins: those two are the")
	render.Detail("platform's own, regenerated with the cluster. An API key is not, and never appears.")
	return nil
}

// Settings builds the list. Exported so a test can assert on the content
// without capturing a screen.
func Settings(root string, kube func(args ...string) (string, error), getenv func(string) string) []Setting {
	var out []Setting
	for _, f := range Features {
		out = append(out, featureSetting(root, f, kube))
	}

	// The CLI half of notifications (Phase 5) lives in the environment rather
	// than in a file, and it is the same question: is one configured.
	out = append(out,
		envSetting(getenv, notify.EnvSlackWebhook, "CLI notifications → Slack"),
		envSetting(getenv, notify.EnvWebhook, "CLI notifications → webhook"),
	)

	// The two switches that change what a transcript contains. Reported here
	// because "why is there no password in my screenshot" is a config question.
	out = append(out, switchSetting(getenv, platform.EnvNoCredentials,
		"suppresses ArgoCD's and Grafana's logins in `urls` and `bootstrap`"))
	out = append(out, switchSetting(getenv, render.EnvNoColor,
		"draws the layout without colour"))
	return out
}

func featureSetting(root string, f Feature, kube func(args ...string) (string, error)) Setting {
	s := Setting{Name: f.Title}
	path := filepath.Join(root, filepath.FromSlash(f.File))
	data, err := os.ReadFile(path)
	if err != nil {
		s.State = render.StatePending
		s.Note = "off · no " + f.File + " (copy " + filepath.Base(f.Example) + " or run `endurance enable " + f.Name + "`)"
		if f.Name == "ai" && kube != nil && secretExists(kube) {
			// The file and the cluster can disagree, and the cluster is what is
			// actually enriching alerts. Saying "off" here would be wrong in the
			// one direction that matters.
			s.State = render.StateWarn
			s.Note = "on in the cluster, but no " + f.File + " — a re-bootstrap leaves the Secret as it is"
		}
		return s
	}
	// A copy of the example with the angle-bracket placeholders still in it is
	// the commonest way this feature is "enabled" and does nothing at all.
	if strings.Contains(string(data), placeholderTag) || strings.Contains(string(data), "<SLACK-") ||
		strings.Contains(string(data), "<WORKSPACE-ID>") {
		s.State = render.StateWarn
		s.Note = f.File + " still has placeholder values — nothing will work until they are replaced"
		return s
	}
	s.State = render.StateReady
	s.Note = "configured · " + f.File
	if f.Name == "ai" && kube != nil {
		if secretExists(kube) {
			s.Note += " · Secret present in " + AINamespace
		} else {
			s.Note += " · not applied to the cluster yet"
		}
	}
	return s
}

func envSetting(getenv func(string) string, name, title string) Setting {
	if strings.TrimSpace(getenv(name)) == "" {
		return Setting{Name: title, State: render.StatePending, Note: "off · " + name + " is not set in this shell"}
	}
	return Setting{Name: title, State: render.StateReady, Note: "on · " + name + " is set in this shell"}
}

func switchSetting(getenv func(string) string, name, what string) Setting {
	if strings.TrimSpace(getenv(name)) == "" {
		return Setting{Name: name, State: render.StatePending, Note: "not set · " + what}
	}
	return Setting{Name: name, State: render.StateReady, Note: "set · " + what}
}

func toChecks(settings []Setting) []render.Check {
	out := make([]render.Check, 0, len(settings))
	for _, s := range settings {
		out = append(out, render.Check{Name: s.Name, State: s.State, Note: s.Note})
	}
	return out
}

// --- writing the files ---

// aiSecretYAML renders the AI module's Secret.
//
// The same shape as secret.example.yaml, because platform/ai/install.sh applies
// this file directly and the example is what documents it. The header says
// where the file came from and that it is not in git — a file full of
// credentials with no explanation on top of it is how one ends up committed.
func aiSecretYAML(key, hook, when string) string {
	var b strings.Builder
	b.WriteString(header("AI alert enrichment credentials", "endurance enable ai", when))
	b.WriteString("apiVersion: v1\nkind: Secret\nmetadata:\n")
	b.WriteString("  name: " + AISecretName + "\n")
	b.WriteString("  namespace: " + AINamespace + "\n")
	b.WriteString("type: Opaque\nstringData:\n")
	b.WriteString("  OPENAI_API_KEY: " + quote(key) + "\n")
	b.WriteString("  SLACK_WEBHOOK_URL: " + quote(hook) + "\n")
	return b.String()
}

// slackValuesYAML renders the ArgoCD notifications values.
//
// Only option A of the example — an incoming webhook. The example documents a
// bot token too, and it is the right choice for a platform with many channels;
// it is also a Slack app, an OAuth scope and an admin approval, which is not
// something a guided first run can walk anybody through. Anyone who wants
// option B has the example file, and this header points at it.
func slackValuesYAML(hook, when string) string {
	var b strings.Builder
	b.WriteString(header("ArgoCD → Slack notifications", "endurance enable slack", when))
	b.WriteString("# This is option A from " + filepath.Base(SlackExample) + ": a Slack incoming webhook.\n")
	b.WriteString("# For option B (a bot token, one channel per application) copy the example\n")
	b.WriteString("# file instead and fill in the `slack-token` half.\n\n")
	b.WriteString("notifications:\n  secret:\n    items:\n")
	b.WriteString("      slack-incoming-url: " + quote(hook) + "\n")
	b.WriteString("  notifiers:\n")
	b.WriteString("    service.webhook.slack-incoming: |\n")
	b.WriteString("      url: $slack-incoming-url\n")
	b.WriteString("      headers:\n")
	b.WriteString("      - name: Content-Type\n")
	b.WriteString("        value: application/json\n")
	return b.String()
}

func header(what, by, when string) string {
	return "" +
		"###############################################################################\n" +
		"# " + what + "\n" +
		"#\n" +
		"# Written by `" + by + "` on " + when + ".\n" +
		"# THIS FILE IS GIT-IGNORED AND EVERY VALUE IN IT IS A CREDENTIAL. Endurance\n" +
		"# never prints these back — not in `config list`, not in `urls`, not anywhere.\n" +
		"# `endurance disable` deletes it.\n" +
		"###############################################################################\n\n"
}

// quote writes a YAML double-quoted scalar. Keys and webhook URLs contain none
// of the characters that would need escaping beyond these two, and a value that
// did would be a value that is not what it claims to be.
func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// writeFile writes a credentials file with an owner-only mode.
//
// 0600 rather than 0644: it is ignored by git, and the other half of not
// committing a secret is not leaving it world-readable. (Windows ignores the
// bits; the file lands under the user's profile there anyway.)
func writeFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

// describeKeys says which fields were filled, by name, never by value.
func describeKeys(present map[string]bool) string {
	var set, empty []string
	for _, k := range sortedKeys(present) {
		if present[k] {
			set = append(set, k)
		} else {
			empty = append(empty, k)
		}
	}
	out := strings.Join(set, ", ") + " set"
	if len(empty) > 0 {
		out += " · " + strings.Join(empty, ", ") + " left empty"
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Sorted so the line is the same every run — a message that reorders itself
	// between two transcripts reads as though something changed.
	sort.Strings(out)
	return out
}

// ValidateWebhook rejects something that is plainly not an incoming-webhook URL
// — and says so without repeating the value, because an error message is a
// transcript too.
func ValidateWebhook(hook string) error {
	if hook == "" {
		return nil
	}
	if !strings.HasPrefix(hook, "https://hooks.slack.com/") {
		return fmt.Errorf("that does not look like a Slack incoming-webhook URL " +
			"(they start https://hooks.slack.com/services/…) — nothing was written")
	}
	return nil
}

// AskSecret is the masked prompt, and the only one this platform captures a
// credential with. The value never reaches the screen: huh renders the mask
// character, and nothing here logs what came back.
//
// Exported so `endurance init` asks for a key exactly the way `enable` does. A
// second prompt would be a second chance to get the echo mode wrong.
func AskSecret(title, description string, required bool) (string, error) {
	return askSecret(title, description, required)
}

func askSecret(title, description string, required bool) (string, error) {
	if !render.Default().IsTTY() {
		return "", fmt.Errorf("%s must be typed at a terminal — this is not one, "+
			"so there is nowhere to hide the input", title)
	}
	value := ""
	input := huh.NewInput().
		Title(title).
		Description(description).
		EchoMode(huh.EchoModePassword).
		Value(&value)
	if required {
		input = input.Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("required")
			}
			return nil
		})
	}
	if err := input.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func askConfirm(prompt string) (bool, error) {
	if !render.Default().IsTTY() {
		return false, fmt.Errorf("this needs confirmation and this is not a terminal — re-run with --yes")
	}
	answer := false
	err := huh.NewConfirm().
		Title(prompt).
		Description("deletes the credentials file · the platform keeps running without it").
		Affirmative("Disable it").
		Negative("Keep it").
		Value(&answer).
		Run()
	return answer, err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func shortRoot(root string) string {
	if base := filepath.Base(root); base != "" && base != "." {
		return base
	}
	return root
}

// stamp dates the generated header. A test pins it, because a file whose
// content changes every second cannot be compared with a golden one.
func stamp(opts Options) string {
	if opts.Now != nil {
		return opts.Now()
	}
	return time.Now().Format("2006-01-02")
}
