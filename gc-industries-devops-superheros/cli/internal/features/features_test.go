package features

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
	"gopkg.in/yaml.v3"
)

// The sentinels. Structurally impossible as real credentials — angle brackets
// are not valid in either format — for the same reason the committed example
// files use them: a realistic-looking dummy in a test file is something GitHub's
// push protection will reject, and it was right to (Phase 5 learned that one the
// hard way).
const (
	fakeKey  = "sk-<TEST-OPENAI-KEY-DO-NOT-PRINT>"
	fakeHook = "https://hooks.slack.com/services/<TEST>/<HOOK>/<DO-NOT-PRINT>"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// sandbox is a throwaway copy of the two directories this package writes into,
// with the marker files platform.FindRoot looks for. Writing a credentials file
// into the real repo during a test run is not a thing to do even once.
func sandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"platform/scripts", "platform/lib", "platform/ai", "platform/gitops/argocd"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The two files platform.isRoot uses as the marker for a platform tree.
	write(t, filepath.Join(root, "platform/scripts/cluster.sh"), "#!/usr/bin/env bash\n")
	write(t, filepath.Join(root, "platform/lib/version.sh"), "CLUSTER_NAME=\"superheros\"\n")
	return root
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.FromSlash(path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func answers(vals ...string) Asker {
	i := 0
	return func(string, string, bool) (string, error) {
		if i >= len(vals) {
			return "", errors.New("asked for more than the test provided")
		}
		v := vals[i]
		i++
		return v, nil
	}
}

func noKubectl(...string) (string, error) { return "", errors.New("no cluster") }

// TestNothingEverPrintsACapturedCredential.
//
// The rule this package exists to keep, asserted across every command that
// touches one. If this test ever fails, the fix is never to change the test.
func TestNothingEverPrintsACapturedCredential(t *testing.T) {
	root := sandbox(t)

	steps := []struct {
		what string
		run  func(*testing.T)
	}{
		{"enable ai", func(t *testing.T) {
			if err := Enable("ai", Options{
				Root: root, Ask: answers(fakeKey, fakeHook),
				Kubectl: noKubectl, Now: func() string { return "2026-07-25" },
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"enable slack", func(t *testing.T) {
			if err := Enable("slack", Options{
				Root: root, Ask: answers(fakeHook), Kubectl: noKubectl,
				Now: func() string { return "2026-07-25" },
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"config list", func(t *testing.T) {
			if err := ConfigList(ConfigOptions{
				Root: root, Kubectl: noKubectl, Getenv: func(string) string { return "" },
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"disable ai", func(t *testing.T) {
			if err := Disable("ai", Options{Root: root, Yes: true, Kubectl: noKubectl}); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, s := range steps {
		t.Run(s.what, func(t *testing.T) {
			buf := capture(t)
			s.run(t)
			got := buf.String()
			for _, secret := range []string{fakeKey, fakeHook} {
				if strings.Contains(got, secret) {
					t.Fatalf("%s printed a credential back:\n%s", s.what, got)
				}
			}
			// Also not a fragment of one. A "helpfully" truncated key is still
			// the first characters of a live key in a screenshot.
			if strings.Contains(got, "sk-") || strings.Contains(got, "hooks.slack.com/services") {
				t.Fatalf("%s printed part of a credential:\n%s", s.what, got)
			}
		})
	}
}

// TestTheWrittenAISecretIsWhatTheModuleApplies.
//
// platform/ai/install.sh runs `kubectl apply -f secret.yaml` on this file
// directly, so the shape is a contract with a bash script rather than a
// preference. The name, namespace and both key names must match what the
// committed example declares, or `enable ai` writes a file the module applies
// and the enricher cannot read.
func TestTheWrittenAISecretIsWhatTheModuleApplies(t *testing.T) {
	root := sandbox(t)
	capture(t)

	path, err := WriteAI(root, fakeKey, fakeHook, "2026-07-25")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Type       string            `yaml:"type"`
		StringData map[string]string `yaml:"stringData"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("the generated Secret is not valid YAML: %v", err)
	}
	if got.Kind != "Secret" || got.APIVersion != "v1" {
		t.Errorf("got %s/%s, want v1/Secret", got.APIVersion, got.Kind)
	}
	if got.Metadata.Name != AISecretName || got.Metadata.Namespace != AINamespace {
		t.Errorf("got %s/%s, want %s/%s",
			got.Metadata.Namespace, got.Metadata.Name, AINamespace, AISecretName)
	}
	if got.StringData["OPENAI_API_KEY"] != fakeKey {
		t.Error("the OpenAI key did not survive the round trip")
	}
	if got.StringData["SLACK_WEBHOOK_URL"] != fakeHook {
		t.Error("the webhook did not survive the round trip")
	}
	// The header has to say what the file is, because a file full of credentials
	// with no explanation on top of it is how one gets committed.
	if !strings.Contains(string(data), "GIT-IGNORED") {
		t.Errorf("the generated file does not say it is git-ignored:\n%s", data)
	}
}

// TestTheWrittenSlackValuesAreWhatHelmConsumes.
//
// platform/gitops/argocd/install.sh passes this file to `helm upgrade` when it
// exists. The two keys that matter are the secret item and the notifier that
// references it — a notifier pointing at a secret key that is not there is a
// configuration error the notifications controller reports at *delivery* time,
// which the Phase 5 example file calls the hardest failure in the feature to
// debug.
func TestTheWrittenSlackValuesAreWhatHelmConsumes(t *testing.T) {
	root := sandbox(t)
	capture(t)

	path, err := WriteSlack(root, fakeHook, "2026-07-25")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Notifications struct {
			Secret struct {
				Items map[string]string `yaml:"items"`
			} `yaml:"secret"`
			Notifiers map[string]string `yaml:"notifiers"`
		} `yaml:"notifications"`
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("the generated values file is not valid YAML: %v", err)
	}
	if got.Notifications.Secret.Items["slack-incoming-url"] != fakeHook {
		t.Error("the webhook did not survive the round trip")
	}
	notifier, ok := got.Notifications.Notifiers["service.webhook.slack-incoming"]
	if !ok {
		t.Fatalf("no service.webhook.slack-incoming notifier:\n%s", data)
	}
	if !strings.Contains(notifier, "$slack-incoming-url") {
		t.Errorf("the notifier does not reference the secret item it needs:\n%s", notifier)
	}
}

// TestTheCredentialsFilesAreGitIgnored.
//
// The other half of not committing a secret, and the half a test can actually
// check. Reads the real .gitignore rather than asserting the intention: this
// package writes two files whose entire safety rests on those two lines.
func TestTheCredentialsFilesAreGitIgnored(t *testing.T) {
	root := realRoot(t)
	// The .gitignore lives in the enclosing git repo, one level above the
	// platform tree, and names the paths from there.
	data, err := os.ReadFile(filepath.Join(root, "..", ".gitignore"))
	if err != nil {
		t.Skipf("no .gitignore above %s: %v", root, err)
	}
	ignored := string(data)
	for _, f := range []string{AISecretFile, SlackFile} {
		if !strings.Contains(ignored, f) {
			t.Errorf(".gitignore does not cover %s — `endurance enable` would write a "+
				"credential into a tracked file", f)
		}
	}
}

// TestConfigListReportsPresenceAndNotValues — the whole point of the command.
func TestConfigListReportsPresenceAndNotValues(t *testing.T) {
	root := sandbox(t)

	// Nothing configured.
	off := Settings(root, nil, func(string) string { return "" })
	for _, s := range off {
		if s.State == render.StateReady {
			t.Errorf("%q reads as on with nothing configured: %s", s.Name, s.Note)
		}
	}

	capture(t)
	if _, err := WriteAI(root, fakeKey, fakeHook, "2026-07-25"); err != nil {
		t.Fatal(err)
	}
	on := Settings(root, nil, func(string) string { return "" })
	found := false
	for _, s := range on {
		if !strings.Contains(s.Name, "AI") {
			continue
		}
		found = true
		if s.State != render.StateReady {
			t.Errorf("AI reads as off with a filled-in secret file: %s", s.Note)
		}
		if strings.Contains(s.Note, fakeKey) || strings.Contains(s.Note, "sk-") {
			t.Errorf("the note carries the value: %s", s.Note)
		}
	}
	if !found {
		t.Fatal("no AI setting in the list")
	}
}

// TestAPlaceholderCopyIsNotConfigured.
//
// The commonest way this feature is "enabled" and does nothing: someone copies
// the example file and forgets to fill it in. It is a ⚠ rather than a ✓ or a ·,
// because the file being there is real and its contents are not.
func TestAPlaceholderCopyIsNotConfigured(t *testing.T) {
	root := sandbox(t)
	example, err := os.ReadFile(filepath.Join(realRoot(t), filepath.FromSlash(AIExampleFile)))
	if err != nil {
		t.Skipf("cannot read the committed example: %v", err)
	}
	write(t, filepath.Join(root, filepath.FromSlash(AISecretFile)), string(example))

	for _, s := range Settings(root, nil, func(string) string { return "" }) {
		if !strings.Contains(s.Name, "AI") {
			continue
		}
		if s.State != render.StateWarn {
			t.Errorf("a placeholder copy reads as %v: %s", s.State, s.Note)
		}
		if !strings.Contains(s.Note, "placeholder") {
			t.Errorf("the note does not say why: %s", s.Note)
		}
	}
}

// TestTheClusterCanDisagreeWithTheFile.
//
// A Secret applied by hand, or by an earlier `enable ai` on a file since
// deleted, is what is actually enriching alerts. Reporting "off" because the
// file is missing would be wrong in the one direction that matters.
func TestTheClusterCanDisagreeWithTheFile(t *testing.T) {
	root := sandbox(t)
	present := func(args ...string) (string, error) { return "secret/" + AISecretName + "\n", nil }

	for _, s := range Settings(root, present, func(string) string { return "" }) {
		if !strings.Contains(s.Name, "AI") {
			continue
		}
		if s.State != render.StateWarn {
			t.Errorf("a Secret in the cluster with no file reads as %v: %s", s.State, s.Note)
		}
		if !strings.Contains(s.Note, "cluster") {
			t.Errorf("the note does not mention the cluster: %s", s.Note)
		}
	}
}

// TestEnableRefusesSomethingThatIsNotASlackWebhook — and the refusal does not
// repeat what was typed, because an error message is a transcript too.
func TestEnableRefusesSomethingThatIsNotASlackWebhook(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	const wrong = "https://example.invalid/<NOT-A-HOOK>"
	err := Enable("slack", Options{Root: root, Ask: answers(wrong), Kubectl: noKubectl})
	if err == nil {
		t.Fatal("a URL that is not a Slack webhook was accepted")
	}
	if strings.Contains(err.Error(), wrong) || strings.Contains(buf.String(), wrong) {
		t.Errorf("the refusal echoed the value: %v\n%s", err, buf.String())
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say the file was left alone: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(SlackFile))); statErr == nil {
		t.Error("a file was written despite the refusal")
	}
}

// TestDisableRemovesTheFileAndTheClusterSecret — both halves, each reported as
// what actually happened rather than as one claim covering two actions.
func TestDisableRemovesTheFileAndTheClusterSecret(t *testing.T) {
	root := sandbox(t)
	capture(t)
	if _, err := WriteAI(root, fakeKey, fakeHook, "2026-07-25"); err != nil {
		t.Fatal(err)
	}

	buf := capture(t)
	var calls [][]string
	kube := func(args ...string) (string, error) {
		calls = append(calls, args)
		return "", nil
	}
	if err := Disable("ai", Options{Root: root, Yes: true, Kubectl: kube}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(AISecretFile))); err == nil {
		t.Error("the credentials file is still there")
	}
	deleted := false
	for _, c := range calls {
		if len(c) > 2 && c[2] == "delete" {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("the cluster Secret was not deleted: %v", calls)
	}
	if got := buf.String(); !strings.Contains(got, "removed "+AISecretFile) {
		t.Errorf("the file removal was not reported:\n%s", got)
	}
}

// TestDisableSaysNothingWhenThereIsNothing — no file, no Secret, no claim.
func TestDisableSaysNothingWhenThereIsNothing(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	asked := false

	err := Disable("slack", Options{
		Root: root, Confirm: func(string) (bool, error) { asked = true; return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if asked {
		t.Error("it asked permission to delete something that is not there")
	}
	if got := buf.String(); !strings.Contains(got, "nothing to disable") {
		t.Errorf("the outcome was not stated:\n%s", got)
	}
}

// TestOnlyTheseTwoFeaturesExist — the set is closed, and it is closed because
// each entry is a file some bash module already reads. A generic key store here
// would be a place to put a credential no module consumes.
func TestOnlyTheseTwoFeaturesExist(t *testing.T) {
	if got := Names(); len(got) != 2 || got[0] != "ai" || got[1] != "slack" {
		t.Fatalf("features are %v, want [ai slack]", got)
	}
	if _, err := Find("openai"); err == nil {
		t.Error("an unknown feature was accepted")
	} else if !strings.Contains(err.Error(), "ai") {
		t.Errorf("the error does not list what is available: %v", err)
	}
	root := realRoot(t)
	for _, f := range Features {
		// Each feature's file must live where the module reads it from, and its
		// example must actually be committed.
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f.Example))); err != nil {
			t.Errorf("%s: the example file %s is not in the repo: %v", f.Name, f.Example, err)
		}
	}
}

// TestASecretFileIsNotWorldReadable — 0600, because git-ignoring a credential
// and then leaving it readable by every process on the machine is half a job.
// (Windows ignores the bits; the assertion is skipped there rather than
// asserting something the OS does not implement.)
func TestASecretFileIsNotWorldReadable(t *testing.T) {
	root := sandbox(t)
	capture(t)
	path, err := WriteAI(root, fakeKey, fakeHook, "2026-07-25")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0o600 {
		return
	}
	if os.Getenv("OS") == "Windows_NT" || filepath.Separator == '\\' {
		t.Skipf("Windows does not implement the mode bits (got %v)", info.Mode().Perm())
	}
	t.Errorf("mode is %v, want 0600", info.Mode().Perm())
}

// realRoot is the actual platform repo, for the tests that must read committed
// files rather than a sandbox.
func realRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("platform/lib/version.sh"))); err != nil {
		t.Skipf("platform tree not found at %s", root)
	}
	return root
}
