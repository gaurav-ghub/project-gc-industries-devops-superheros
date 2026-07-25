package success

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// repoRoot is the real platform repo this CLI lives in — the success screen
// reads kind-config.yaml for the host port and platform/lib/version.sh for the
// cluster name, and reading the real ones is the point.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.FindRoot(root); err != nil {
		t.Skipf("platform tree not found at %s", root)
	}
	return root
}

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

func app() spec.App {
	a := spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Owner:     "gc-industries",
		Route:     spec.Route{Enabled: true, Path: "/", Service: "frontend"},
		Services: []spec.Service{
			{Name: "frontend", Image: "docker.io/x/frontend", Tag: "v1", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/x/catalog", Tag: "v1", Port: 8081, Replicas: 2},
		},
	}
	a.ApplyDefaults()
	return a
}

// TestOnlyAnObservedRunningPodEarnsATick.
//
// The rule this screen exists to keep, and the one place a pretty screen could
// lie. Asserted at both levels it applies: the per-pod glyph, and the glyph
// beside the title.
func TestOnlyAnObservedRunningPodEarnsATick(t *testing.T) {
	root := repoRoot(t)
	observed := []platform.PodState{
		{Name: "frontend-abc", Status: "Running", Ready: true},
		{Name: "catalog-def", Status: "ContainerCreating", Ready: false},
		{Name: "catalog-ghi", Status: "CrashLoopBackOff", Ready: false},
		// Running, but 1/2 containers — the pod is up and serving nothing.
		{Name: "catalog-jkl", Status: "Running", Ready: false},
	}
	res := Build(root, app(), observed, true)

	want := []render.State{
		render.StateReady, render.StatePending, render.StateFailed, render.StatePending,
	}
	if len(res.Pods) != len(want) {
		t.Fatalf("got %d pod rows, want %d", len(res.Pods), len(want))
	}
	for i, p := range res.Pods {
		if p.State != want[i] {
			t.Errorf("%s (%s): state %v, want %v", p.Name, p.Status, p.State, want[i])
		}
	}
	if res.State == render.StateReady {
		t.Error("the screen claimed the application is healthy with three pods that are not")
	}
	if !strings.Contains(res.Footer, "1 of 4 pods ready") {
		t.Errorf("the footer does not carry the honest count: %q", res.Footer)
	}
}

// A CrashLoopBackOff is a failure; a ContainerCreating is not. Treating every
// non-ready pod as broken would mean every deploy looks broken for its first
// thirty seconds, which teaches people to ignore the screen.
func TestAPodBeingCreatedIsNotAFailure(t *testing.T) {
	res := Build(repoRoot(t), app(), []platform.PodState{
		{Name: "frontend-abc", Status: "Pending"},
		{Name: "frontend-def", Status: "ContainerCreating"},
		{Name: "frontend-ghi", Status: "Terminating"},
	}, true)
	for _, p := range res.Pods {
		if p.State == render.StateFailed {
			t.Errorf("%s (%s) was reported as failed", p.Name, p.Status)
		}
	}
}

func TestEveryPodReadyEarnsTheTick(t *testing.T) {
	res := Build(repoRoot(t), app(), []platform.PodState{
		{Name: "frontend-abc", Status: "Running", Ready: true},
		{Name: "catalog-def", Status: "Running", Ready: true},
	}, true)
	if res.State != render.StateReady {
		t.Errorf("a fully-ready application did not earn a ✓ (state %v)", res.State)
	}
	if !strings.Contains(res.Title, "healthy") {
		t.Errorf("title = %q", res.Title)
	}
	if res.Footer != "2 of 2 pods ready" {
		t.Errorf("footer = %q", res.Footer)
	}
}

// TestTheThreeWaysThereAreNoPods.
//
// "The cluster said there are none" and "the cluster did not answer" are
// different facts, and collapsing them into one empty list is how a screen
// comes to say "nothing is running" about a cluster it never reached.
func TestTheThreeWaysThereAreNoPods(t *testing.T) {
	root := repoRoot(t)

	unreachable := Build(root, app(), nil, false)
	if unreachable.State == render.StateReady {
		t.Error("an unreachable cluster produced a ✓")
	}
	if !strings.Contains(unreachable.Title, "cluster not reached") {
		t.Errorf("title = %q", unreachable.Title)
	}
	if !strings.Contains(unreachable.Footer, "no pods were observed") {
		t.Errorf("footer = %q", unreachable.Footer)
	}

	empty := Build(root, app(), nil, true)
	if !strings.Contains(empty.Title, "nothing running yet") {
		t.Errorf("title = %q", empty.Title)
	}
	if !strings.Contains(empty.Footer, "ArgoCD deploys pushed state") {
		t.Errorf("an un-pushed application is not told to push: %q", empty.Footer)
	}
	if len(empty.Pods) != 0 {
		t.Error("pods appeared from nowhere")
	}
	// Both cases still print the URLs: where the application will be is useful
	// before it is there, and it is the only part of this screen that is not an
	// observation.
	if len(empty.URLs) == 0 || len(unreachable.URLs) == 0 {
		t.Error("the URLs were dropped when there were no pods")
	}
}

// TestTheApplicationsURLComesFirstAndFromItsOwnSpec.
//
// The platform does not decide what an application's URL looks like — the
// pre-Endurance chart had /api/catalog baked into a gateway route and that is
// the mistake not to repeat. So the App row is built from the app's spec, and
// an application that asked for no route gets no App row at all rather than a
// plausible-looking guess.
func TestTheApplicationsURLComesFirstAndFromItsOwnSpec(t *testing.T) {
	root := repoRoot(t)
	base := platform.BaseURL(root)

	res := Build(root, app(), nil, true)
	if len(res.URLs) == 0 || res.URLs[0].Label != "App" {
		t.Fatalf("the application's own URL is not first: %+v", res.URLs)
	}
	if res.URLs[0].Addr != base+"/" {
		t.Errorf("App URL = %q, want %q", res.URLs[0].Addr, base+"/")
	}
	if !strings.Contains(res.URLs[0].Note, "frontend") {
		t.Errorf("the App URL does not say which service answers it: %q", res.URLs[0].Note)
	}

	// A non-root path is the application's to choose.
	other := app()
	other.Route.Path = "/shop"
	if got := Build(root, other, nil, true).URLs[0].Addr; got != base+"/shop" {
		t.Errorf("App URL = %q, want %q", got, base+"/shop")
	}

	// No route, no App URL — and the footer says how to ask for one.
	none := app()
	none.Route = spec.Route{}
	res = Build(root, none, []platform.PodState{{Name: "frontend-abc", Status: "Running", Ready: true}}, true)
	for _, u := range res.URLs {
		if u.Label == "App" {
			t.Error("an application that asked for no route was given a URL")
		}
	}
	if !strings.Contains(res.Footer, "no route declared") {
		t.Errorf("the footer does not mention the missing route: %q", res.Footer)
	}
	// The platform's own addresses are still there: they are the platform's,
	// not the application's, and they are useful either way.
	if len(res.URLs) != len(platform.AccessURLs(root)) {
		t.Errorf("got %d URLs, want the platform's %d", len(res.URLs), len(platform.AccessURLs(root)))
	}
}

// TestTheScreenSpeaksBothDialects — the "useful commands" block carries both an
// endurance form and a kubectl form. Both, because kubectl is what a developer
// will reach for when something is wrong at 2am, and a tool that hides the
// cluster is a tool people work around.
func TestTheScreenSpeaksBothDialects(t *testing.T) {
	res := Build(repoRoot(t), app(), nil, true)
	var endurance, kubectl int
	for _, h := range res.Hints {
		switch {
		case strings.HasPrefix(h.Command, "endurance "):
			endurance++
		case strings.HasPrefix(h.Command, "kubectl "):
			kubectl++
		default:
			t.Errorf("a hint is in neither dialect: %q", h.Command)
		}
	}
	if endurance == 0 || kubectl == 0 {
		t.Errorf("hints: %d endurance, %d kubectl — the mock asks for both", endurance, kubectl)
	}
}

// A canary application is offered the canary command; a plain one is not.
func TestHintsFollowWhatTheApplicationActuallyIs(t *testing.T) {
	root := repoRoot(t)

	plain := Build(root, app(), nil, true)
	for _, h := range plain.Hints {
		if strings.Contains(h.Command, "canary") {
			t.Errorf("a plain application was offered %q", h.Command)
		}
	}

	c := app()
	c.Services[1].Tag, c.Services[1].Replicas = "", 0
	c.Services[1].Versions = []spec.Version{
		{Name: "v1", Tag: "v1", Weight: 50, Replicas: 1},
		{Name: "v2", Tag: "v2", Weight: 50, Replicas: 1},
	}
	c.ApplyDefaults()

	found := false
	for _, h := range Build(root, c, nil, true).Hints {
		if strings.Contains(h.Command, "canary status") {
			found = true
		}
	}
	if !found {
		t.Error("a canary application was not offered `endurance canary status`")
	}
	// And its replica count includes every version, not just the services.
	res := Build(root, c, nil, true)
	if !rowsContain(res.Rows, "Replicas", "3 desired") {
		t.Errorf("replicas do not count the canary versions: %+v", res.Rows)
	}
	if !rowsContain(res.Rows, "Canary", "catalog") {
		t.Errorf("the canary service is not named: %+v", res.Rows)
	}
}

// TestASingleServiceApplicationGetsThePDFsRows — the mock is the N=1 case:
// namespace, cluster, image, replicas. A five-service application cannot
// honestly report one image, so it reports the list instead.
func TestASingleServiceApplicationGetsThePDFsRows(t *testing.T) {
	root := repoRoot(t)

	one := app()
	one.Services = one.Services[:1]
	one.ApplyDefaults()
	res := Build(root, one, nil, true)
	if !rowsContain(res.Rows, "Image", "docker.io/x/frontend:v1") {
		t.Errorf("a single-service application does not show its image: %+v", res.Rows)
	}

	many := Build(root, app(), nil, true)
	if rowsContain(many.Rows, "Image", "") {
		t.Error("a multi-service application claimed to have one image")
	}
	if !rowsContain(many.Rows, "Services", "frontend, catalog") {
		t.Errorf("the services are not listed: %+v", many.Rows)
	}
}

func rowsContain(rows [][2]string, key, substr string) bool {
	for _, r := range rows {
		if r[0] == key && strings.Contains(r[1], substr) {
			return true
		}
	}
	return false
}

// TestScreenRendersFromTheRealRegistry — the end-to-end path, with kubectl
// scripted. apps/superheros is generated and committed, so this reads what the
// platform actually has on disk.
func TestScreenRendersFromTheRealRegistry(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	err := Screen(Options{Root: root, App: "superheros", Kubectl: func(...string) (string, error) {
		return "frontend-6d9f7c8b4d-r2x9p   1/1   Running             0   2m\n" +
			"catalog-v1-7c5f6d4b88-k4t2m   0/2   ContainerCreating   0   5s\n", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"superheros is deploying",
		"frontend-6d9f7c8b4d-r2x9p",
		"1 of 2 pods ready",
		platform.BaseURL(root) + "/",
		platform.BaseURL(root) + "/argocd",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The pod that is still being created must not carry a ✓ on its own line.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "ContainerCreating") && strings.Contains(line, render.IconOK) {
			t.Errorf("a pod that is not Running was marked done: %q", line)
		}
	}
}

// An unreachable cluster is reported, not returned as an error: the developer
// asking "did my deploy work" is owed "not yet, and here is why".
func TestAnUnreachableClusterIsReportedNotThrown(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	err := Screen(Options{Root: root, App: "superheros", Kubectl: func(...string) (string, error) {
		return "", errors.New("connection refused")
	}})
	if err != nil {
		t.Fatalf("an unreachable cluster failed the command: %v", err)
	}
	if !strings.Contains(buf.String(), "cluster not reached") {
		t.Errorf("the screen did not say the cluster was unreachable:\n%s", buf.String())
	}
}

// An unregistered application is the one case that *is* an error: there is
// nothing to draw and no cluster question worth asking.
func TestAnUnregisteredApplicationIsAnError(t *testing.T) {
	capture(t)
	err := Screen(Options{Root: repoRoot(t), App: "nope"})
	if err == nil {
		t.Fatal("an unregistered application rendered a success screen")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("the error does not name it: %v", err)
	}
}

// kubectl's "No resources found" arrives on stdout with exit 0, so an empty
// namespace looks like output. Reading it as a pod row would print a pod called
// "No".
func TestNoResourcesFoundIsNotAPod(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	if err := Screen(Options{Root: root, App: "superheros", Kubectl: func(...string) (string, error) {
		return "No resources found in superheros namespace.\n", nil
	}}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "No resources") {
		t.Errorf("kubectl's empty-namespace message was rendered as a pod:\n%s", got)
	}
	if !strings.Contains(got, "nothing running yet") {
		t.Errorf("an empty namespace was not reported:\n%s", got)
	}
}
