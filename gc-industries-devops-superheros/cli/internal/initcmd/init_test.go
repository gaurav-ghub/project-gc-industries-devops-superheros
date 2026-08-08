package initcmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gc-ghub/endurance/internal/onboard"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/policy"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

// The sentinels for the credential tests. Angle brackets, so nothing here can
// ever be mistaken for a real key by a secret scanner — the rule the committed
// example files were rewritten to follow in Phase 5.
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

// sandbox is a throwaway platform tree: enough for FindRoot, kind-config.yaml
// and the two directories init writes into. Nothing in these tests may write a
// spec, a credential or a commit into the real repo.
func sandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		"platform/scripts", "platform/lib", "platform/ai", "platform/gitops/argocd",
		"specs", "apps",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "platform/scripts/cluster.sh"), "#!/usr/bin/env bash\n")
	writeFile(t, filepath.Join(root, "platform/lib/version.sh"),
		"CLUSTER_NAME=\"endurance\"\nKUBERNETES_CONTEXT=\"kind-${CLUSTER_NAME}\"\n")
	// The real host-port mapping, so BaseURL reads a file rather than guessing.
	writeFile(t, filepath.Join(root, "kind-config.yaml"), ""+
		"kind: Cluster\nnodes:\n  - role: control-plane\n    extraPortMappings:\n"+
		"      - containerPort: 30000\n        hostPort: 8080\n")
	return root
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.FromSlash(path), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spy records which of the phases init actually reached.
type spy struct {
	bootstrapped int
	onboarded    []onboard.Options
	kubectl      [][]string
}

func (s *spy) bootstrap(o platform.BootstrapOptions) error {
	s.bootstrapped++
	return nil
}

// onboard stands in for the real generator, and writes the two files init goes
// on to use: the registry entry the success screen reads, and the ArgoCD
// Application the deploy step applies. A fake that recorded the call and wrote
// nothing would let init pass a test it would fail on a real machine.
func (s *spy) onboard(o onboard.Options) error {
	s.onboarded = append(s.onboarded, o)

	data, err := os.ReadFile(o.From)
	if err != nil {
		return err
	}
	var app spec.App
	if err := yaml.Unmarshal(data, &app); err != nil {
		return err
	}
	dir := filepath.Join(o.Root, "apps", app.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	out, err := yaml.Marshal(app)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), out, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "application.yaml"),
		[]byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: "+
			app.Name+"\n  namespace: argocd\n"), 0o644)
}

// kube answers as a cluster with the application synced and one pod up.
func (s *spy) kube(args ...string) (string, error) {
	s.kubectl = append(s.kubectl, args)
	switch {
	case args[0] == "apply":
		return "application.argoproj.io/demo created\n", nil
	case len(args) > 3 && args[3] == "application":
		return `{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`, nil
	case args[0] == "get" && args[1] == "pods":
		return "demo-6d9f7c8b4d-r2x9p   1/1   Running   0   30s\n", nil
	}
	return "", errors.New("unexpected: " + strings.Join(args, " "))
}

// noCluster is a machine with nothing on it.
func noCluster(root string) platform.ClusterState {
	return platform.ClusterState{Name: "superheros", Known: true, HostPort: 8080}
}

// healthyCluster is one that already has everything.
func healthyCluster(root string) platform.ClusterState {
	return platform.ClusterState{Name: "superheros", Exists: true, Known: true, Published: true, HostPort: 8080}
}

func noHealth(string) platform.Health  { return platform.Health{Total: 10} }
func allHealth(string) platform.Health { return platform.Health{Reachable: true, Ready: 10, Total: 10} }

// readyMachine is the machine check answered without asking the machine.
//
// Every test below is about what init does once the tools are there, and the
// real platform.Doctor looks for docker, kind, kubectl, helm and istioctl on
// whatever host is running the test. Leaving it real made this suite a report
// on the developer's laptop: green here, red on a CI runner, and red for
// anybody who cloned the repo before installing the toolchain. The gate itself
// is not skipped by this — it has its own test below, and platform's own suite
// covers what Doctor reports.
func readyMachine(platform.DoctorOptions) error { return nil }

// unreadyMachine is the same edge answering the way a bare machine does.
func unreadyMachine(platform.DoctorOptions) error {
	return errors.New("preflight failed — 5 of 8 checks need attention")
}

// base is a run with every prompt already answered, which is the form the
// runbook and these tests use.
func base(root string, s *spy) Options {
	return Options{
		Root: root, Name: "demo", Yes: true,
		Doctor:    readyMachine,
		Bootstrap: s.bootstrap, Onboard: s.onboard, Kubectl: s.kube,
		Inspect: noCluster, Health: noHealth,
		Timeout: time.Second,
		Now:     func() time.Time { return time.Unix(0, 0) },
		sleep:   func(time.Duration) {},
	}
}

// TestThePlanNamesEverythingBeforeAnythingHappens.
//
// The load-bearing screen. This command creates a Docker container that outlives
// it and writes and commits files, and a user who has not been shown that list
// cannot consent to it. Every item is checked by name.
func TestThePlanNamesEverythingBeforeAnythingHappens(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.DryRun = true
	if err := Run(opts); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	got := buf.String()

	for _, want := range []string{
		"kind cluster superheros",                       // what gets created
		"a Docker container that stays on this machine", // and that it survives
		"specs/demo.yaml",                               // what gets written
		"apps/demo/{app,application,values}.yaml",
		"Commit those files",                       // and committed
		"Endurance never pushes",                   // and what it will not do
		"application.yaml, the ArgoCD Application", // how it deploys
		"nothing here applies a workload",
		"skipping: AI alert enrichment", // and what it is not doing
		"skipping: Slack notifications",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan does not mention %q:\n%s", want, got)
		}
	}
	// A plan has observed nothing, so it may not carry the glyph that means "it
	// worked". The preflight above it legitimately does, so the check is scoped
	// to the plan itself.
	plan := got[strings.Index(got, "This is what will happen"):]
	if strings.Contains(plan, render.IconOK) {
		t.Errorf("the plan claims something succeeded before it happened:\n%s", plan)
	}
}

// TestNothingHappensBeforeTheConfirmation — declining leaves the machine and the
// repo exactly as they were.
func TestNothingHappensBeforeTheConfirmation(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.Yes = false
	opts.Confirm = func(string) (Decision, error) { return Cancel, nil }

	if err := Run(opts); err != nil {
		t.Fatalf("declining returned an error: %v", err)
	}
	if s.bootstrapped != 0 || len(s.onboarded) != 0 || len(s.kubectl) != 0 {
		t.Fatalf("work happened after the user said no: bootstrap=%d onboard=%d kubectl=%v",
			s.bootstrapped, len(s.onboarded), s.kubectl)
	}
	if _, err := os.Stat(filepath.Join(root, "specs", "demo.yaml")); err == nil {
		t.Error("a spec file was written after the user said no")
	}
	if got := buf.String(); !strings.Contains(got, "nothing was created") {
		t.Errorf("the outcome was not stated:\n%s", got)
	}
}

// TestADryRunWritesNothing — the same guarantee, without a prompt at all.
func TestADryRunWritesNothing(t *testing.T) {
	root := sandbox(t)
	capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.DryRun = true
	opts.Ask = func(a *Answers, d Answers) error {
		*a = d
		a.AI, a.OpenAIKey = true, fakeKey
		a.Slack, a.SlackHook = true, fakeHook
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if s.bootstrapped != 0 || len(s.onboarded) != 0 {
		t.Fatal("--dry-run ran something")
	}
	for _, f := range []string{"specs/demo.yaml", "platform/ai/secret.yaml", "platform/gitops/argocd/values.slack.yaml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			t.Errorf("--dry-run wrote %s", f)
		}
	}
}

// TestInitRefusesAClusterThatPredatesTheAccessLayer.
//
// kind fixes extraPortMappings at cluster-creation time. A cluster from before
// Phase 10 installs all seven modules perfectly, reports every component
// healthy, and cannot be reached from the host at all — so a first run that
// carried on would spend ten minutes and then hand a stranger five dead links
// under a success screen. There is no fix but a recreate, and this is the
// command that must not discover that at the end.
func TestInitRefusesAClusterThatPredatesTheAccessLayer(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.Inspect = func(string) platform.ClusterState {
		return platform.ClusterState{Name: "superheros", Exists: true, Known: true, HostPort: 8080}
	}
	err := Run(opts)
	if err == nil {
		t.Fatal("init proceeded against a cluster nothing can reach")
	}
	if s.bootstrapped != 0 {
		t.Error("it started a bootstrap anyway")
	}
	got := buf.String()
	if !strings.Contains(got, "endurance destroy") {
		t.Errorf("the refusal does not name the fix:\n%s", got)
	}
	if !strings.Contains(got, "kind fixes port mappings") {
		t.Errorf("the refusal does not say why a recreate is the only fix:\n%s", got)
	}
}

// TestABootstrapIsSkippedWhenThePlatformIsAlreadyThere.
//
// Ten minutes is a long time to spend re-running seven idempotent modules, and
// "is your platform already installed?" is a question the tool can answer by
// asking the cluster. A skip is stated, never silent.
func TestABootstrapIsSkippedWhenThePlatformIsAlreadyThere(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.Inspect = healthyCluster
	opts.Health = allHealth
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if s.bootstrapped != 0 {
		t.Error("a healthy platform was reinstalled")
	}
	got := buf.String()
	if !strings.Contains(got, "skipping: installing the platform") {
		t.Errorf("the plan does not say the bootstrap is being skipped:\n%s", got)
	}
	if !strings.Contains(got, "already installed and healthy") {
		t.Errorf("the run does not say why it skipped:\n%s", got)
	}
	if len(s.onboarded) != 1 {
		t.Errorf("it did not go on to onboard: %v", s.onboarded)
	}
}

// TestTheSpecFileIsTheInputOnboardReads.
//
// Init generates the *input*, not the output, and then composes `onboard`
// instead of reimplementing the generator. That is what makes the run repeatable
// — apps/<app>/ is regenerated from specs/<app>.yaml every time — and it is what
// keeps exactly one generator in this codebase.
func TestTheSpecFileIsTheInputOnboardReads(t *testing.T) {
	root := sandbox(t)
	capture(t)
	s := &spy{}

	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "specs", "demo.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no spec file was written: %v", err)
	}
	var app spec.App
	if err := yaml.Unmarshal(data, &app); err != nil {
		t.Fatalf("the generated spec is not valid YAML: %v", err)
	}
	app.ApplyDefaults()
	if err := app.Validate(); err != nil {
		t.Fatalf("init generated a spec its own validator refuses: %v", err)
	}
	if len(s.onboarded) != 1 {
		t.Fatalf("onboard ran %d times", len(s.onboarded))
	}
	if got := s.onboarded[0].From; got != path {
		t.Errorf("onboard read %q, want %q", got, path)
	}
	if !s.onboarded[0].Commit {
		t.Error("init did not ask onboard to commit — the plan said it would")
	}
	if s.onboarded[0].NoBanner != true {
		t.Error("onboard drew a second banner inside one run")
	}
}

// TestAnExistingSpecIsNotOverwritten.
//
// Re-running init for an application that already has a spec must not throw away
// what the user edited into it. The generated tree is regenerated; the input is
// theirs.
func TestAnExistingSpecIsNotOverwritten(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	mine := "name: demo\nnamespace: demo\nservices:\n  - name: demo\n    image: docker.io/me/demo\n    tag: v9\n    port: 9000\n"
	writeFile(t, filepath.Join(root, "specs", "demo.yaml"), mine)

	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, "specs", "demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != mine {
		t.Errorf("init overwrote a spec the user already had:\n%s", after)
	}
	if got := buf.String(); !strings.Contains(got, "already exists") {
		t.Errorf("it did not say it was leaving the file alone:\n%s", got)
	}
}

// TestThePlanDescribesTheSpecThatWillActuallyBeUsed.
//
// An application that already has a spec file is onboarded from that file —
// writeSpec leaves it alone, because the input is the user's. So a plan built
// from the answers would describe a different run from the one about to happen:
// "1 service · nginx-unprivileged" above a re-run of a five-service application
// is precisely the untruth this screen exists to prevent, and it is worse than
// the others because it is the screen consent is given on.
func TestThePlanDescribesTheSpecThatWillActuallyBeUsed(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	writeFile(t, filepath.Join(root, "specs", "demo.yaml"), ""+
		"name: demo\nnamespace: demo-ns\nowner: someone-else\n"+
		"route:\n  enabled: true\n  path: /shop\n  service: web\n"+
		"services:\n"+
		"  - name: web\n    image: docker.io/me/web\n    tag: v2\n    port: 3000\n"+
		"  - name: api\n    image: docker.io/me/api\n    tag: v2\n    port: 4000\n")

	opts := base(root, s)
	opts.DryRun = true
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"Re-register demo from the spec it already has",
		"2 services · web, api",
		"namespace demo-ns · owner someone-else",
		"http://localhost:8080/shop via web",
		"already exists and is left alone",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan does not describe the existing spec (%q):\n%s", want, got)
		}
	}
	// And it must not describe the defaults it is not going to use.
	for _, wrong := range []string{DefaultImage, "1 service ·", "namespace demo ·"} {
		if strings.Contains(got, wrong) {
			t.Errorf("the plan describes the answers rather than the spec (%q):\n%s", wrong, got)
		}
	}
}

// TestTheDefaultsAStrangerGetsPassThePolicyGate.
//
// The single most important assertion about the defaults. A first run whose
// out-of-the-box image is rejected by the platform's own Kyverno policies three
// screens later makes the gate look like an obstacle instead of the point — and
// the ordinary `nginx` image is exactly such an image, because it binds port 80
// and writes to /var/cache as root. This reads the real ClusterPolicies.
func TestTheDefaultsAStrangerGetsPassThePolicyGate(t *testing.T) {
	root := realRoot(t)
	dir := filepath.Join(root, policy.DefaultDir)
	policies, err := policy.Load(dir)
	if err != nil {
		t.Skipf("cannot read the policies at %s: %v", dir, err)
	}
	if len(policies) == 0 {
		t.Skipf("no policies under %s", dir)
	}

	a := Answers{
		Name: DefaultName, Image: DefaultImage, Tag: DefaultTag, Port: DefaultPort,
		Route: true, Path: "/",
	}
	app := toSpec(a)
	app.ApplyDefaults()
	if err := app.Validate(); err != nil {
		t.Fatalf("the default application does not validate: %v", err)
	}
	rep := policy.Check(policies, app)
	if n := len(rep.Blocking()); n > 0 {
		t.Fatalf("the default application a stranger gets is blocked by %d enforced policy "+
			"violation(s):\n%+v", n, rep.Blocking())
	}
}

// TestTheDefaultTagIsNotLatest — `:latest` is refused by this platform's own
// restrict-image-tags policy, so a default of `latest` would be a default that
// cannot deploy. Belt and braces beside the gate test above, because this one
// names the reason.
func TestTheDefaultTagIsNotLatest(t *testing.T) {
	if DefaultTag == "latest" || DefaultTag == "" {
		t.Fatalf("the default tag is %q — the policy gate refuses it", DefaultTag)
	}
	if err := validTag(DefaultTag); err != nil {
		t.Fatalf("the default tag does not pass init's own validator: %v", err)
	}
	if err := validTag("latest"); err == nil {
		t.Error("the prompt would accept `latest`, which the gate then refuses")
	}
}

// TestTheDefaultPathAvoidsOneAnotherApplicationAlreadyHas.
//
// Istio merges every VirtualService bound to one gateway into a single route
// table, so two applications both claiming `/` is not an error anywhere — it is
// one of them silently never being reached, in creation order. Defaulting around
// it costs nothing.
func TestTheDefaultPathAvoidsOneAnotherApplicationAlreadyHas(t *testing.T) {
	root := sandbox(t)
	if got := freePath(root, "demo"); got != "/" {
		t.Errorf("with nothing registered the default path is %q, want /", got)
	}

	dir := filepath.Join(root, "apps", "other")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "app.yaml"),
		"name: other\nnamespace: other\nroute:\n  enabled: true\n  path: /\n  service: other\n"+
			"services:\n  - name: other\n    image: i\n    tag: v1\n    port: 80\n")

	if got := freePath(root, "demo"); got != "/demo" {
		t.Errorf("with / taken the default path is %q, want /demo", got)
	}
	// Its own path is not a collision with itself, which matters on a re-run.
	if got := freePath(root, "other"); got != "/" {
		t.Errorf("an application collided with its own route: %q", got)
	}
}

// TestTheReservedPathsAreRefusedAtThePrompt.
//
// The gate would refuse them anyway, three screens later, with the run half
// done. A prompt that accepts an answer the tool is about to reject is a prompt
// that wasted somebody's time.
func TestTheReservedPathsAreRefusedAtThePrompt(t *testing.T) {
	for _, p := range spec.ReservedPaths {
		if err := validPath(p); err == nil {
			t.Errorf("the prompt accepts %s, which belongs to a platform dashboard", p)
		}
		if err := validPath(p + "/sub"); err == nil {
			t.Errorf("the prompt accepts %s/sub", p)
		}
	}
	if err := validPath("/"); err != nil {
		t.Errorf("the root path was refused: %v", err)
	}
	if err := validPath("nope"); err == nil {
		t.Error("a path that is not a path was accepted")
	}
}

// TestInitNeverPrintsACredentialItCaptured.
//
// Init is where a stranger types an OpenAI key for the first time. It goes to a
// git-ignored file and nowhere else — not to the plan, not to a confirmation,
// not to the closing screen. See internal/features for why this is stricter than
// the rule `endurance urls` follows for ArgoCD's and Grafana's own logins.
func TestInitNeverPrintsACredentialItCaptured(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.Name = "" // the interactive path: the answers arrive from the form
	opts.Yes = false
	opts.Confirm = func(string) (Decision, error) { return Create, nil }
	opts.Ask = func(a *Answers, d Answers) error {
		*a = d
		a.Name = "demo"
		a.AI, a.OpenAIKey, a.AISlackHook = true, fakeKey, fakeHook
		a.Slack, a.SlackHook = true, fakeHook
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}

	got := buf.String()
	for _, secret := range []string{fakeKey, fakeHook, "sk-", "hooks.slack.com/services"} {
		if strings.Contains(got, secret) {
			t.Fatalf("init printed a credential:\n%s", got)
		}
	}
	// And it did write them, where the modules read them from — a test that
	// passes because nothing was captured would prove nothing.
	for _, f := range []string{"platform/ai/secret.yaml", "platform/gitops/argocd/values.slack.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("%s was not written: %v", f, err)
		}
		if !strings.Contains(string(data), fakeKey) && !strings.Contains(string(data), fakeHook) {
			t.Errorf("%s does not carry what was captured", f)
		}
	}
	if !strings.Contains(got, "never printed back") {
		t.Errorf("init does not tell the user where the key went:\n%s", got)
	}
}

// TestTheCredentialsAreWrittenBeforeTheBootstrap.
//
// Ordering, and it is not cosmetic: platform/ai/install.sh applies secret.yaml
// if it finds one, and platform/gitops/argocd/install.sh passes values.slack.yaml
// to helm if it finds one. Written after the chain, both would be ignored until
// the next bootstrap, and "enable AI" during a first run would quietly mean
// "enable AI eventually".
func TestTheCredentialsAreWrittenBeforeTheBootstrap(t *testing.T) {
	root := sandbox(t)
	capture(t)

	var order []string
	s := &spy{}
	opts := base(root, s)
	opts.Name = ""
	opts.Ask = func(a *Answers, d Answers) error {
		*a = d
		a.Name = "demo"
		a.AI, a.OpenAIKey = true, fakeKey
		return nil
	}
	opts.Yes = false
	opts.Confirm = func(string) (Decision, error) { return Create, nil }
	opts.Bootstrap = func(o platform.BootstrapOptions) error {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("platform/ai/secret.yaml"))); err == nil {
			order = append(order, "secret-already-there")
		} else {
			order = append(order, "secret-missing")
		}
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "secret-already-there" {
		t.Errorf("the bootstrap ran before the credentials were written: %v", order)
	}
}

// TestInitEndsOnTheRealSuccessScreen.
//
// The rule this project keeps re-learning: never claim an outcome you did not
// observe. Init's last screen is the one `endurance status <app>` draws, from
// live pod state, and its title comes from what the cluster said.
func TestInitEndsOnTheRealSuccessScreen(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "demo is deployed and healthy") {
		t.Errorf("the run does not end on the success screen:\n%s", got)
	}
	if !strings.Contains(got, "1 of 1 pods ready") {
		t.Errorf("the footer does not report what was observed:\n%s", got)
	}
	if !strings.Contains(got, "http://localhost:8080/") {
		t.Errorf("the application's own address is missing:\n%s", got)
	}
}

// TestTheApplicationIsRegisteredWithArgoCDAndNothingElseIsApplied.
//
// ArgoCD is the only deployer, and that invariant is exactly as intact after
// this phase as before it. Init applies one file — the ArgoCD Application, which
// is a registration — and never a Deployment, a Service or a namespace.
func TestTheApplicationIsRegisteredWithArgoCDAndNothingElseIsApplied(t *testing.T) {
	root := sandbox(t)
	capture(t)
	s := &spy{}

	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	applies := 0
	for _, c := range s.kubectl {
		if c[0] != "apply" {
			continue
		}
		applies++
		if !strings.HasSuffix(filepath.ToSlash(c[2]), "apps/demo/application.yaml") {
			t.Errorf("init applied something other than the ArgoCD Application: %v", c)
		}
	}
	if applies != 1 {
		t.Errorf("kubectl apply ran %d times, want once: %v", applies, s.kubectl)
	}
	// And no verb that could create a workload.
	for _, c := range s.kubectl {
		switch c[0] {
		case "create", "run", "expose", "scale", "delete", "patch":
			t.Errorf("init used a mutating kubectl verb: %v", c)
		}
	}
}

// TestADeployThatCannotBeSeenSaysSoAndNamesThePush.
//
// "Endurance never pushes" is a decision from Phase 5 that this project made
// structural, and a guided first run is not the place to quietly reverse it.
// ArgoCD only ever sees pushed state, so when the commit is not on the branch it
// watches, the wait says which commit, which remote, and the one command — and
// the closing screen still reports what is actually running, which is nothing.
func TestADeployThatCannotBeSeenSaysSoAndNamesThePush(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	s := &spy{}
	opts := base(root, s)
	// ArgoCD has the Application but has nothing to sync from.
	opts.Kubectl = func(args ...string) (string, error) {
		s.kubectl = append(s.kubectl, args)
		switch {
		case args[0] == "apply":
			return "application.argoproj.io/demo created\n", nil
		case len(args) > 3 && args[3] == "application":
			return `{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Missing"}}}`, nil
		}
		return "", errors.New("No resources found")
	}
	if err := Run(opts); err != nil {
		t.Fatalf("a deploy still waiting is not an error: %v", err)
	}
	got := buf.String()

	// The sandbox is not a git repository at all, which is the strongest form of
	// "nothing has been pushed anywhere".
	if !strings.Contains(got, "git push") {
		t.Errorf("the run does not name the command it will not run itself:\n%s", got)
	}
	if !strings.Contains(got, "never pushes") {
		t.Errorf("the run does not say why it stopped there:\n%s", got)
	}
	if strings.Contains(got, "deployed and healthy") {
		t.Errorf("it claimed a deploy it never observed:\n%s", got)
	}
}

// TestPushStateDistinguishesItsThreeAnswers — no upstream, ahead of one, and
// nothing to do are three different sentences because they have three different
// next steps.
func TestPushStateDistinguishesItsThreeAnswers(t *testing.T) {
	// A directory that is not a git repository: every git call fails, which is
	// the "nothing has been pushed anywhere" case.
	needs, detail := pushState(t.TempDir())
	if !needs {
		t.Error("a non-repository was reported as fully pushed")
	}
	if !strings.Contains(detail, "no upstream") {
		t.Errorf("the reason is not stated: %q", detail)
	}
}

// TestTheWaitReportsEachStateOnceAndNotEveryPoll — a step that prints the same
// line every five seconds is a step nobody reads.
func TestTheWaitReportsEachStateOnceAndNotEveryPoll(t *testing.T) {
	buf := capture(t)
	root := sandbox(t)

	polls := 0
	kube := func(args ...string) (string, error) {
		polls++
		if polls < 3 {
			return `{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Progressing"}}}`, nil
		}
		return `{"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}`, nil
	}
	p := render.NewProgress("t", "wait")
	step := p.Start("wait")
	st := waitForSync(step, root, "demo", kube, Options{
		Timeout: time.Minute, Now: func() time.Time { return time.Unix(0, 0) },
		sleep: func(time.Duration) {},
	})
	step.Done()
	p.Finish()

	if !st.synced {
		t.Fatal("a Synced/Healthy application was not recognised")
	}
	if got := strings.Count(buf.String(), "sync OutOfSync · health Progressing"); got != 1 {
		t.Errorf("the same state was reported %d times, want 1:\n%s", got, buf.String())
	}
}

// TestNoQuestionIsAskedThatTheToolCanAnswer.
//
// The rule that decides which prompts exist at all. These four are answerable
// from the machine, so none of them is a question.
func TestNoQuestionIsAskedThatTheToolCanAnswer(t *testing.T) {
	root := sandbox(t)
	capture(t)

	asked := false
	s := &spy{}
	opts := base(root, s)
	opts.Ask = func(*Answers, Answers) error { asked = true; return nil }
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if asked {
		t.Error("init asked questions despite --name and --yes")
	}

	a, err := collect(base(root, s), root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Name == "" {
		t.Error("the name was not defaulted")
	}
	app := toSpec(a)
	// The namespace is the name; it is never a question.
	if app.Namespace != a.Name {
		t.Errorf("namespace %q, want %q", app.Namespace, a.Name)
	}
	// Replicas is never asked either: one, which is what a first run wants.
	if app.Services[0].Replicas != 1 {
		t.Errorf("replicas %d, want 1", app.Services[0].Replicas)
	}
	// The route's port comes from the service rather than being asked twice.
	app.ApplyDefaults()
	if app.Route.Enabled && app.Route.Port != a.Port {
		t.Errorf("route port %d, want the service's %d", app.Route.Port, a.Port)
	}
}

// TestTheGeneratedSpecExplainsItself.
//
// specs/<app>.yaml is the file the user comes back to when they want a second
// service or a different tag, and a bare four-line document teaches them nothing
// about what else it could say.
func TestTheGeneratedSpecExplainsItself(t *testing.T) {
	yamlText := specYAML(Answers{
		Name: "demo", Image: DefaultImage, Tag: DefaultTag, Port: DefaultPort,
		Owner: "me", Route: true, Path: "/",
	})
	for _, want := range []string{
		"endurance onboard --root . --from specs/demo.yaml", // how to re-run it
		"overwritten on every onboard",                      // why to edit here
		"the N=1 case",                                      // how to grow it
		"platform defaults",                                 // why security is absent
	} {
		if !strings.Contains(yamlText, want) {
			t.Errorf("the generated spec does not explain %q:\n%s", want, yamlText)
		}
	}
	var app spec.App
	if err := yaml.Unmarshal([]byte(yamlText), &app); err != nil {
		t.Fatalf("the generated spec is not valid YAML: %v", err)
	}
	if app.Route.Path != "/" || app.Route.Service != "demo" {
		t.Errorf("the route did not round-trip: %+v", app.Route)
	}
}

// TestAnOwnerThatYAMLWouldMisreadIsQuoted — `git config user.name` is arbitrary
// text, and an owner called "Yes" or one containing a colon is silently wrong
// rather than loudly wrong.
func TestAnOwnerThatYAMLWouldMisreadIsQuoted(t *testing.T) {
	for _, owner := range []string{"Yes", "team: platform", "*star", "#hash"} {
		text := specYAML(Answers{Name: "demo", Image: "i", Tag: "v1", Port: 80, Owner: owner})
		var app spec.App
		if err := yaml.Unmarshal([]byte(text), &app); err != nil {
			t.Fatalf("owner %q produced invalid YAML: %v\n%s", owner, err, text)
		}
		if app.Owner != owner {
			t.Errorf("owner %q round-tripped as %q", owner, app.Owner)
		}
	}
}

// TestTheHeadlessFormNeedsNoTerminal — the runbook, the tests and any future CI
// use it, and it must not be the interactive path with the prompts disabled.
func TestTheHeadlessFormNeedsNoTerminal(t *testing.T) {
	root := sandbox(t)
	capture(t) // not a TTY

	s := &spy{}
	opts := base(root, s)
	opts.Image = "docker.io/library/redis"
	opts.Tag = "7-alpine"
	opts.Port = 6379
	opts.NoRoute = true

	if err := Run(opts); err != nil {
		t.Fatalf("the headless form failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "specs", "demo.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var app spec.App
	if err := yaml.Unmarshal(data, &app); err != nil {
		t.Fatal(err)
	}
	if app.Services[0].Image != "docker.io/library/redis" || app.Services[0].Port != 6379 {
		t.Errorf("the flags did not reach the spec: %+v", app.Services[0])
	}
	if app.Route.Enabled {
		t.Error("--no-route still produced a route")
	}
}

// TestInitWithoutFlagsRefusesToGuessOffATerminal — the other half: no flags, no
// terminal, no invented answers.
func TestInitWithoutFlagsRefusesToGuessOffATerminal(t *testing.T) {
	capture(t)
	err := askApplication(&Answers{}, Answers{})
	if err == nil {
		t.Fatal("a form that could not be shown returned answers")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the error does not name the way to run it headless: %v", err)
	}
}

// TestThePhasesRunInSequenceAndNotNested.
//
// Written down because it was the decision this phase had to make first. A
// render.Progress owns a counter, a bar and a verdict; nesting one inside
// another means two counters on one line and a bar meaning two things at once.
// What ties init together instead is one titled rule per phase — a rule opens a
// phase of work, which is the Phase 8 grammar. This asserts the rules are there
// and that no second chain opens inside the first.
func TestThePhasesRunInSequenceAndNotNested(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, phase := range []string{"1 of 4", "2 of 4", "3 of 4", "4 of 4"} {
		if !strings.Contains(got, phase) {
			t.Errorf("no rule for phase %s:\n%s", phase, got)
		}
	}
	// The deploy chain's counter is [1/2] and [2/2]. It is the only chain in
	// this run (the bootstrap is faked), and its steps are numbered from one —
	// which is what "sequential" looks like in the transcript.
	if !strings.Contains(got, "[1/2]") || !strings.Contains(got, "[2/2]") {
		t.Errorf("the deploy chain did not number its own steps:\n%s", got)
	}
	if strings.Contains(got, "[1/9]") {
		t.Errorf("a chain was renumbered to include another chain's steps:\n%s", got)
	}
}

// TestInitStopsOnAMachineThatIsNotReady.
//
// The gate that phase 1 exists for, and it had no test until the release
// workflow found out why: the machine check was wired straight to the host, so
// there was no way to write this one. A missing tool has to stop the run before
// a single question is asked and before anything is written — finding out that
// kind is not installed after answering six questions is the failure this
// ordering was chosen to avoid.
func TestInitStopsOnAMachineThatIsNotReady(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.Doctor = unreadyMachine

	err := Run(opts)
	if err == nil {
		t.Fatal("init ran on a machine that failed its preflight")
	}
	if !strings.Contains(buf.String(), "fix what is named above") {
		t.Errorf("the run stopped without saying what to do about it:\n%s", buf.String())
	}
	if s.bootstrapped != 0 || len(s.onboarded) != 0 || len(s.kubectl) != 0 {
		t.Errorf("something ran anyway: bootstrap=%d onboard=%d kubectl=%d",
			s.bootstrapped, len(s.onboarded), len(s.kubectl))
	}
	if _, err := os.Stat(filepath.Join(root, "specs", "demo.yaml")); !os.IsNotExist(err) {
		t.Error("a spec was written for a run that never got past the machine check")
	}
}

// TestNoWaitEndsOnWhatIsRunningRatherThanWaiting.
func TestNoWaitEndsOnWhatIsRunningRatherThanWaiting(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)
	s := &spy{}

	opts := base(root, s)
	opts.NoWait = true
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "skipped · --no-wait") {
		t.Errorf("the skip is not stated:\n%s", got)
	}
	for _, c := range s.kubectl {
		if len(c) > 3 && c[3] == "application" {
			t.Errorf("--no-wait polled ArgoCD anyway: %v", c)
		}
	}
}

// TestAFailedApplyStopsAndSaysWhy — an ArgoCD Application that will not apply is
// a failure, and the step carries it rather than the run carrying on to wait for
// something that was never registered.
func TestAFailedApplyStopsAndSaysWhy(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Kubectl = func(args ...string) (string, error) {
		return "error: the server could not find the requested resource", fmt.Errorf("exit status 1")
	}
	if err := Run(opts); err == nil {
		t.Fatal("a failed apply was reported as success")
	}
	got := buf.String()
	if !strings.Contains(got, "✗ Registering demo with ArgoCD") {
		t.Errorf("the failing step is not marked:\n%s", got)
	}
	if strings.Contains(got, "deployed and healthy") {
		t.Errorf("the success screen printed after a failed apply:\n%s", got)
	}
}

// TestOnboardIsToldWhichRepoArgoCDWatches.
//
// A regression, and an expensive one: init called onboard without a GitopsRepo,
// so every Application it generated rendered `repoURL:` empty. Nothing noticed
// until `kubectl apply` at the very end — after the files were written and
// committed — and the API server's answer ("spec.sources[0].repoURL: Required
// value") never reached the screen. The whole run failed on its last step.
func TestOnboardIsToldWhichRepoArgoCDWatches(t *testing.T) {
	root := sandbox(t)
	capture(t)

	s := &spy{}
	if err := Run(base(root, s)); err != nil {
		t.Fatal(err)
	}
	if len(s.onboarded) != 1 {
		t.Fatalf("onboard was called %d times, want 1", len(s.onboarded))
	}
	if got := s.onboarded[0].GitopsRepo; got == "" {
		t.Fatal("init onboarded without a GitOps repo URL — the generated " +
			"Application would have an empty repoURL and be refused by the API server")
	}
}

// TestTheGitopsRepoFlagWins — an explicit --gitops-repo is not second-guessed.
func TestTheGitopsRepoFlagWins(t *testing.T) {
	root := sandbox(t)
	capture(t)

	const want = "https://github.com/someone-else/their-platform.git"
	s := &spy{}
	opts := base(root, s)
	opts.GitopsRepo = want
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if got := s.onboarded[0].GitopsRepo; got != want {
		t.Errorf("GitopsRepo = %q, want %q", got, want)
	}
}

// TestAnInvalidApplicationReportsTheFieldsAtFault.
//
// kubectl puts the headline on the first line and the diagnosis on the ones
// after it. Reporting only the first line turns a fixable fault into "The
// Application "demo" is invalid:" — a sentence that names nothing.
func TestAnInvalidApplicationReportsTheFieldsAtFault(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Kubectl = func(args ...string) (string, error) {
		return "The Application \"demo\" is invalid: \n" +
			"* spec.sources[0].repoURL: Required value\n" +
			"* spec.sources[1].repoURL: Required value\n", fmt.Errorf("exit status 1")
	}
	err := Run(opts)
	if err == nil {
		t.Fatal("an invalid Application was reported as success")
	}
	if !strings.Contains(err.Error(), "spec.sources[0].repoURL: Required value") {
		t.Errorf("the returned error names no field at fault: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "spec.sources[1].repoURL: Required value") {
		t.Errorf("the diagnosis lines are not on screen:\n%s", got)
	}
}

func TestReasonFoldsAComplaintIntoOneSentence(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", "no output"},
		{"one line", "error: no such file", "error: no such file"},
		{
			"headline plus fields",
			"The Application \"demo\" is invalid: \n* spec.project: Required value\n",
			`The Application "demo" is invalid: spec.project: Required value`,
		},
		{
			"more than can be shown",
			"invalid:\n* a\n* b\n* c\n* d\n* e\n",
			"invalid: a; b; c; d, …",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reason(c.in); got != c.want {
				t.Errorf("reason() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestGoingBackAndSayingSkipLeavesNoCredential.
//
// The fault a user hit on the first live run: they answered Yes to AI
// enrichment, then wanted out. Each question used to be its own one-field form,
// so "back" had nothing to go back to and ctrl+c — killing the run — was the
// only exit.
//
// The questions are one form now, so ↑ returns to the confirm and Skip closes
// the group. huh keeps a hidden group's values, so the answers have to be
// cleared: a plan that says "skipping AI enrichment" while a key sits in the
// answers would write that key to disk.
func TestGoingBackAndSayingSkipLeavesNoCredential(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Name = ""
	opts.Yes = false
	opts.Confirm = func(string) (Decision, error) { return Create, nil }
	// Typed a key, went back, chose Skip — which is what the form leaves behind.
	opts.Ask = func(a *Answers, d Answers) error {
		*a = d
		a.Name = "demo"
		a.AI, a.OpenAIKey = false, ""
		a.Slack, a.SlackHook = false, ""
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"platform/ai/secret.yaml", "platform/gitops/argocd/values.slack.yaml"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err == nil {
			t.Errorf("%s was written for a capability that was skipped", f)
		}
	}
	if got := buf.String(); !strings.Contains(got, "skipping") {
		t.Errorf("the plan does not say the capabilities were skipped:\n%s", got)
	}
}

// TestTheQuestionsAreOneForm — asserted through the seam, because a form cannot
// be driven from a test without a terminal: Options has one Ask covering every
// question, credentials included. A separate AskSecret seam would mean a
// separate form, which is what could not be navigated back through.
func TestTheQuestionsAreOneForm(t *testing.T) {
	root := sandbox(t)
	capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Name = ""
	opts.Yes = false
	opts.Confirm = func(string) (Decision, error) { return Create, nil }

	asked := 0
	opts.Ask = func(a *Answers, d Answers) error {
		asked++
		*a = d
		a.Name = "demo"
		a.AI, a.OpenAIKey = true, fakeKey
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if asked != 1 {
		t.Errorf("the questions were asked in %d passes, want 1 — they must be one form", asked)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("platform/ai/secret.yaml"))); err != nil {
		t.Error("the key captured by the one form was not written")
	}
}

// TestEditReopensTheQuestionsAndTheSecondAnswerWins.
//
// The confirmation screen is the first time the answers are visible together,
// which makes it the first place a wrong one is obvious. Until v0.10.3 the only
// responses were "create it" and "cancel", so noticing a typo cost every other
// answer — and the user who found this had typed six.
func TestEditReopensTheQuestionsAndTheSecondAnswerWins(t *testing.T) {
	root := sandbox(t)
	capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Name = ""
	opts.Yes = false

	// Read the plan, ask to edit, then accept the second one.
	answered := 0
	opts.Confirm = func(string) (Decision, error) {
		answered++
		if answered == 1 {
			return Edit, nil
		}
		return Create, nil
	}
	asked := 0
	opts.Ask = func(a *Answers, d Answers) error {
		asked++
		*a = d
		if asked == 1 {
			a.Name, a.Tag = "typo", "v1"
			return nil
		}
		// The second pass arrives holding the first pass's answers, which is the
		// whole point of Edit: change one line, keep the rest.
		if d.Name != "typo" {
			t.Errorf("the form reopened with name %q, want the answer it was given", d.Name)
		}
		a.Name = "demo"
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}

	if asked != 2 {
		t.Errorf("the questions were asked %d times, want 2", asked)
	}
	if len(s.onboarded) != 1 {
		t.Fatalf("onboard ran %d times, want once", len(s.onboarded))
	}
	if !strings.Contains(s.onboarded[0].From, "demo") {
		t.Errorf("onboarded from %q — the edited name did not win", s.onboarded[0].From)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("specs/typo.yaml"))); err == nil {
		t.Error("the abandoned first answer was written to disk")
	}
}

// TestEditCanStillBeCancelled — the loop has to have an exit that creates
// nothing, or Edit becomes a trap of its own.
func TestEditCanStillBeCancelled(t *testing.T) {
	root := sandbox(t)
	buf := capture(t)

	s := &spy{}
	opts := base(root, s)
	opts.Name = ""
	opts.Yes = false

	answered := 0
	opts.Confirm = func(string) (Decision, error) {
		answered++
		if answered == 1 {
			return Edit, nil
		}
		return Cancel, nil
	}
	opts.Ask = func(a *Answers, d Answers) error {
		*a = d
		a.Name = "demo"
		return nil
	}
	if err := Run(opts); err != nil {
		t.Fatal(err)
	}
	if s.bootstrapped != 0 {
		t.Error("a cancelled run bootstrapped a cluster")
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash("specs/demo.yaml"))); err == nil {
		t.Error("a cancelled run wrote a spec")
	}
	if got := buf.String(); !strings.Contains(got, "nothing was created") {
		t.Errorf("a cancelled run did not say so:\n%s", got)
	}
}

// TestAnEmptyCredentialTurnsItsCapabilityOff.
//
// The rule that lets ↑ work at all. huh will not leave a group backwards while a
// field in it has a validation error, so a required-and-empty first field is a
// prompt with no exit — which is exactly what the OpenAI key prompt was for a
// user who answered Yes by accident. Neither secret is required now, and an
// empty one means the capability was declined.
func TestAnEmptyCredentialTurnsItsCapabilityOff(t *testing.T) {
	cases := []struct {
		name string
		in   Answers
		want Answers
	}{
		{
			"said yes, gave nothing",
			Answers{AI: true, Slack: true},
			Answers{},
		},
		{
			"whitespace is nothing",
			Answers{AI: true, OpenAIKey: "   ", Slack: true, SlackHook: "\t"},
			Answers{},
		},
		{
			"went back and chose skip, key still in the hidden group",
			Answers{AI: false, OpenAIKey: fakeKey, AISlackHook: fakeHook},
			Answers{},
		},
		{
			"a key given is a key kept",
			Answers{AI: true, OpenAIKey: fakeKey},
			Answers{AI: true, OpenAIKey: fakeKey},
		},
		{
			"the AI webhook is optional and survives without turning anything off",
			Answers{AI: true, OpenAIKey: fakeKey, AISlackHook: fakeHook},
			Answers{AI: true, OpenAIKey: fakeKey, AISlackHook: fakeHook},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in
			settle(&got)
			if got != c.want {
				t.Errorf("settle(%+v)\n = %+v\nwant %+v", c.in, got, c.want)
			}
		})
	}
}
