package onboard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/deploy"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// aValidApp is a minimal application that clears Validate, the image gate and
// the (absent) policy gate, so tests below are about the deploy wiring and not
// about getting past the earlier checks.
func aValidApp(name string) spec.App {
	return spec.App{
		Name: name, Namespace: name,
		Services: []spec.Service{
			{Name: name, Image: "docker.io/nginxinc/nginx-unprivileged", Tag: "stable-alpine", Port: 8080, Replicas: 1},
		},
	}
}

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

// TestARejectedOnboardSaysSoOnce.
//
// `finish` used to call render.Error and *also* return the error, which main
// renders — so a refusal printed the same sentence twice in two voices. That is
// the duplicated-verdict fault Phase 9 recorded and fixed for `doctor` and
// `status`; it survived here because nothing tripped it routinely until Phase
// 10's route validation started refusing reserved paths.
//
// The rule: the verdict is the returned error, rendered once, by the caller.
func TestARejectedOnboardSaysSoOnce(t *testing.T) {
	dir := t.TempDir()
	buf := capture(t)

	// A route onto one of the platform's own dashboard paths — the refusal a
	// developer is most likely to see, and the one that exposed the bug.
	app := spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Route:     spec.Route{Enabled: true, Path: "/grafana", Service: "frontend"},
		Services: []spec.Service{
			{Name: "frontend", Image: "docker.io/x/frontend", Tag: "v1", Port: 80, Replicas: 1},
		},
	}

	err := finish(Options{Root: dir, SkipPolicy: true}, app)
	if err == nil {
		t.Fatal("a route onto a reserved path was accepted")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the error does not explain the refusal: %v", err)
	}

	got := buf.String()
	if n := strings.Count(got, render.IconError); n != 0 {
		t.Errorf("finish rendered the verdict itself %d time(s) — main renders it, "+
			"and two voices saying one thing is the Phase 9 duplicated-verdict bug:\n%s", n, got)
	}

	// And nothing was written: a refused onboard leaves no half-generated app.
	if _, err := os.Stat(filepath.Join(dir, "apps")); !os.IsNotExist(err) {
		t.Error("a refused onboard created apps/")
	}
}

// 14.2 — the missing verb, and onboard's half of "have both doors use it".
//
// onboard is also the automation entry point (CI, --from, the byte-identical
// regeneration proof), so unlike init it must not reach for a cluster nobody
// asked about by default. Deploy is opt-in here; what changes unconditionally
// is what the dashboard tells a person to type when it did not run.

func TestOnboardDoesNotDeployByDefault(t *testing.T) {
	dir := t.TempDir()
	capture(t)

	called := false
	err := finish(Options{
		Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true,
		DeployFunc: func(deploy.Options) (bool, error) { called = true; return true, nil },
	}, aValidApp("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("onboard deployed without --deploy — it must not touch a cluster nobody asked about")
	}
}

// A tool that knows the exact command and hands it to a person has not
// finished (memory item #17) — and until 14.2 onboard's own next-steps line
// was `kubectl apply -f apps/<app>/application.yaml`. It is now the verb.
func TestUndeployedOnboardPointsAtTheVerbNotKubectl(t *testing.T) {
	dir := t.TempDir()
	buf := capture(t)

	if err := finish(Options{Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true}, aValidApp("demo")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "kubectl apply") {
		t.Errorf("onboard's next steps still hand a kubectl command to a person:\n%s", got)
	}
	if !strings.Contains(got, "endurance deploy demo") {
		t.Errorf("onboard's next steps do not name the verb that registers it with ArgoCD:\n%s", got)
	}
}

// This held even without the mesh — the pre-14.2 line only appeared for a
// meshed application, framed as being about the namespace label, when applying
// the Application is what makes ArgoCD aware of the app at all, mesh or not.
func TestUndeployedOnboardPointsAtTheVerbWithoutMeshToo(t *testing.T) {
	dir := t.TempDir()
	buf := capture(t)

	app := aValidApp("demo")
	app.Mesh = spec.MeshOff()
	if err := finish(Options{Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true}, app); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "endurance deploy demo") {
		t.Errorf("an unmeshed application's next steps do not mention deploy:\n%s", buf.String())
	}
}

// --deploy makes onboard call the same verb, passing what a live cluster
// actually needs — assert on what it passes, not just that it called the next
// thing, which is the rule v0.10.1's missing GitopsRepo cost a release over.
func TestOnboardDeployForwardsRootAndApp(t *testing.T) {
	dir := t.TempDir()
	capture(t)

	var got deploy.Options
	err := finish(Options{
		Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true, Deploy: true,
		DeployFunc: func(o deploy.Options) (bool, error) { got = o; return true, nil },
	}, aValidApp("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != dir {
		t.Errorf("Root = %q, want %q", got.Root, dir)
	}
	if got.App != "demo" {
		t.Errorf("App = %q, want %q", got.App, "demo")
	}
}

// Once deploy actually converges, the dashboard has nothing left to tell a
// person to type — the run already earned the claim.
func TestASyncedDeployLeavesNoOutstandingStep(t *testing.T) {
	dir := t.TempDir()
	buf := capture(t)

	err := finish(Options{
		Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true, Deploy: true,
		DeployFunc: func(deploy.Options) (bool, error) { return true, nil },
	}, aValidApp("demo"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "endurance deploy demo") {
		t.Errorf("a synced deploy still told the user to run deploy:\n%s", buf.String())
	}
}

// A deploy that was asked for and failed is a real failure — the caller
// explicitly asked for the cluster to be touched, so a broken apply must not
// be swallowed the way "no --deploy at all" is.
func TestAFailedRequestedDeployIsAnError(t *testing.T) {
	dir := t.TempDir()
	capture(t)

	err := finish(Options{
		Root: dir, GitopsRepo: "https://example.com/x.git", SkipPolicy: true, Deploy: true,
		DeployFunc: func(deploy.Options) (bool, error) {
			return false, errors.New("kubectl apply: exit status 1")
		},
	}, aValidApp("demo"))
	if err == nil {
		t.Fatal("a failed --deploy was reported as success")
	}
}
