package image

import (
	"errors"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/spec"
)

// 14.3 and 14.4 — the two refusals, and the rule about how they are tested.
//
// **Nothing here calls `docker manifest inspect`.** A test that did would pass
// on the laptop this was written on, hang in CI and fail on a machine with no
// network — the exact class of fault that shipped a broken v0.11.0 release, when
// sixteen tests asked the host whether docker, kind and kubectl were installed.
// Every registry answer below is a fake, and the one real implementation
// (DockerInspector) is exercised only through its parser, on captured bytes.

const amd64Nodes = "linux/amd64"

func nodes() Platform { return Platform{OS: "linux", Arch: "amd64"} }

// app builds a defaulted single-service application, which is what the gate
// actually sees: the privileged-port rule is about the security block
// ApplyDefaults materialises, so checking a raw spec would check a posture the
// generated files do not have.
func app(name, image, tag string, port int) spec.App {
	a := spec.App{
		Name: name, Namespace: name,
		Services: []spec.Service{{Name: name, Image: image, Tag: tag, Port: port, Replicas: 1}},
	}
	a.ApplyDefaults()
	return a
}

// publishes returns an Inspector that answers with exactly these platforms.
func publishes(platforms ...Platform) Inspector {
	return func(ref string) (Manifest, error) {
		return Manifest{Ref: ref, Platforms: platforms}, nil
	}
}

func arm64Only() Inspector { return publishes(Platform{OS: "linux", Arch: "arm64"}) }
func multiArch() Inspector {
	return publishes(Platform{OS: "linux", Arch: "amd64"}, Platform{OS: "linux", Arch: "arm64"})
}
func unreachable() Inspector {
	return func(string) (Manifest, error) { return Manifest{}, errors.New("dial tcp: i/o timeout") }
}

// --- 14.3 · an image with no manifest for this cluster's architecture ---

// bad-app, exactly: built on an Apple Silicon MacBook without --platform, so it
// publishes arm64 only, against amd64 kind nodes. The platform took ~10 minutes
// and 41 backoff events to say so.
func TestAnArm64OnlyImageIsRefusedOnAmd64Nodes(t *testing.T) {
	rep := Check(app("bad-app", "docker.io/dockergc00/bad-app", "v2", 8080),
		Options{Node: nodes(), Inspect: arm64Only()})

	if rep.OK() {
		t.Fatal("an arm64-only image was accepted against amd64 nodes — this is the failure that " +
			"cost the first outside run ten minutes and forty-one backoff events")
	}
	f := rep.Findings[0]
	if !strings.Contains(f.Reason, "linux/arm64") || !strings.Contains(f.Reason, amd64Nodes) {
		t.Errorf("the refusal does not say which architectures are involved: %q", f.Reason)
	}
	if !strings.Contains(f.Reason, "no match for platform in manifest") {
		t.Errorf("the refusal does not name the event the cluster would have produced, "+
			"which is the sentence a developer will search for: %q", f.Reason)
	}
	if !strings.Contains(f.Fix, "--platform") {
		t.Errorf("the refusal says what is wrong and not what to change: %q", f.Fix)
	}
}

func TestAMultiArchImageIsAccepted(t *testing.T) {
	rep := Check(app("ok", "docker.io/library/nginx", "1.27", 8080),
		Options{Node: nodes(), Inspect: multiArch()})
	if !rep.OK() {
		t.Fatalf("a multi-arch image was refused: %v", rep.Findings)
	}
}

// The other half of "does this exist": the v3 rebuild that never happened. The
// buildx run was started from the platform repo, which has no Dockerfile, so it
// failed and v3 was never pushed — while `endurance status` cheerfully reported
// the spec's `bad-app:v3` beside a pod still running v2.
func TestATagThatWasNeverPushedIsRefused(t *testing.T) {
	missing := func(ref string) (Manifest, error) {
		return Manifest{}, NotFoundError{Ref: ref, Detail: "manifest unknown"}
	}
	rep := Check(app("bad-app", "docker.io/dockergc00/bad-app", "v3", 8080),
		Options{Node: nodes(), Inspect: missing})
	if rep.OK() {
		t.Fatal("a tag the registry has never seen was accepted")
	}
	if !strings.Contains(rep.Findings[0].Reason, "no such image or tag") {
		t.Errorf("the refusal does not say the image is not there: %q", rep.Findings[0].Reason)
	}
}

// A registry that did not answer is not a registry that said yes.
func TestAnUnreachableRegistryIsSkippedNotPassed(t *testing.T) {
	rep := Check(app("x", "docker.io/x/y", "v1", 8080),
		Options{Node: nodes(), Inspect: unreachable()})
	if len(rep.Skipped) != 1 {
		t.Fatalf("an unreachable registry produced %d skips, want 1", len(rep.Skipped))
	}
	if !rep.OK() {
		t.Error("an unreachable registry blocked the run — a laptop on a train must still onboard")
	}
	if !strings.Contains(rep.Skipped[0].Reason, "could not ask the registry") {
		t.Errorf("the skip does not say what could not be done: %q", rep.Skipped[0].Reason)
	}
}

// No inspector at all — a machine with no docker. The check is not made and says
// so; it does not quietly become a pass.
func TestNoInspectorSkipsAndSaysSo(t *testing.T) {
	rep := Check(app("x", "docker.io/x/y", "v1", 8080), Options{Node: nodes()})
	if len(rep.Skipped) != 1 {
		t.Fatalf("skips = %d, want 1", len(rep.Skipped))
	}
	if !strings.Contains(rep.Skipped[0].Reason, "docker manifest inspect") {
		t.Errorf("the skip does not name the command it would have run: %q", rep.Skipped[0].Reason)
	}
}

// A canary service has one image reference per version, and each of them can be
// the wrong architecture on its own.
func TestEveryCanaryVersionIsChecked(t *testing.T) {
	a := spec.App{
		Name: "superheros", Namespace: "superheros",
		Services: []spec.Service{{
			Name: "catalog", Image: "docker.io/x/catalog", Port: 8081,
			Versions: []spec.Version{
				{Name: "v1", Tag: "v1", Weight: 50},
				{Name: "v2", Tag: "v2", Weight: 50},
			},
		}},
	}
	a.ApplyDefaults()

	seen := map[string]bool{}
	rep := Check(a, Options{Node: nodes(), Inspect: func(ref string) (Manifest, error) {
		seen[ref] = true
		return Manifest{Ref: ref, Platforms: []Platform{{OS: "linux", Arch: "amd64"}}}, nil
	}})
	if !seen["docker.io/x/catalog:v1"] || !seen["docker.io/x/catalog:v2"] {
		t.Errorf("not every canary version's image was looked up: %v", seen)
	}
	if !rep.OK() {
		t.Errorf("a correct canary was refused: %v", rep.Findings)
	}
}

// --- 14.4 · an image the security defaults make unrunnable ---

// portfolio-frontend, exactly: stock nginx on port 80, under the UID 10001 and
// dropped capabilities every Endurance values file mandates.
func TestStockNginxOnPort80IsRefused(t *testing.T) {
	rep := Check(app("frontend", "docker.io/library/nginx", "1.27-alpine", 80),
		Options{Node: nodes(), Inspect: multiArch()})

	if rep.OK() {
		t.Fatal("port 80 under runAsNonRoot with all capabilities dropped was accepted — " +
			"this container cannot start on any machine")
	}
	f := rep.Findings[0]
	for _, want := range []string{"80", "10001", "NET_BIND_SERVICE"} {
		if !strings.Contains(f.Reason, want) {
			t.Errorf("the refusal does not mention %q: %q", want, f.Reason)
		}
	}
	if !strings.Contains(f.Fix, "nginx-unprivileged") {
		t.Errorf("the refusal does not name the image that works, which this repo has "+
			"used as its own default since Phase 11: %q", f.Fix)
	}
}

// The point of the whole item: this is a fact about the image, not about the
// manifest, so it is caught with no registry at all. The Kyverno gate said
// `✓ all policies satisfied` about this same application and was right.
func TestThePortCheckNeedsNoRegistry(t *testing.T) {
	rep := Check(app("frontend", "docker.io/library/nginx", "1.27-alpine", 80),
		Options{Node: nodes()})
	if rep.OK() {
		t.Fatal("a privileged port was accepted when there was no registry to ask — " +
			"the port rule is structural and must not depend on the network")
	}
}

func TestAnUnprivilegedPortIsFine(t *testing.T) {
	rep := Check(app("frontend", "docker.io/nginxinc/nginx-unprivileged", "stable-alpine", 8080),
		Options{Node: nodes(), Inspect: multiArch()})
	if !rep.OK() {
		t.Fatalf("8080 under the platform defaults was refused: %v", rep.Findings)
	}
}

// The boundary. 1023 is privileged and 1024 is not, and getting this off by one
// would either refuse a working service or accept a doomed one.
func TestThePrivilegedBoundary(t *testing.T) {
	for port, wantRefused := range map[int]bool{1: true, 80: true, 443: true, 1023: true, 1024: false, 8080: false} {
		rep := Check(app("x", "docker.io/x/y", "v1", port), Options{Node: nodes()})
		if refused := !rep.OK(); refused != wantRefused {
			t.Errorf("port %d refused = %v, want %v", port, refused, wantRefused)
		}
	}
}

// A service that is genuinely allowed to be root — nothing generates one today,
// and the rule must be about the posture rather than about the number.
func TestAPrivilegedPortIsFineWithoutRunAsNonRoot(t *testing.T) {
	a := app("x", "docker.io/x/y", "v1", 80)
	a.Services[0].Security.RunAsNonRoot = false
	if rep := Check(a, Options{Node: nodes()}); !rep.OK() {
		t.Error("port 80 was refused for a container that is not required to be non-root")
	}
}

// --- the gate ---

func TestTheGateRefusesAndSaysNothingWasWritten(t *testing.T) {
	err := Gate(app("frontend", "docker.io/library/nginx", "1.27-alpine", 80),
		Options{Node: nodes(), Inspect: multiArch()})
	if err == nil {
		t.Fatal("the gate passed an image that cannot start")
	}
	if !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("the refusal does not say the run wrote nothing, which is the "+
			"only thing that makes a refusal a refusal: %v", err)
	}
}

func TestBreakGlassReportsAndDoesNotBlock(t *testing.T) {
	err := Gate(app("frontend", "docker.io/library/nginx", "1.27-alpine", 80),
		Options{Node: nodes(), Inspect: multiArch(), Skip: true})
	if err != nil {
		t.Fatalf("--skip-image-check still blocked: %v", err)
	}
}

// --- the node platform ---

// linux, not runtime.GOOS. kind nodes are Linux containers whatever the host is,
// and reading GOOS here would refuse every image in existence on the Windows
// laptop this platform is developed on.
func TestTheNodePlatformIsAlwaysLinux(t *testing.T) {
	if got := NodePlatform(); got.OS != "linux" {
		t.Errorf("NodePlatform() = %s; kind nodes are Linux containers on every host", got)
	}
	if NodePlatform().Arch == "" {
		t.Error("NodePlatform() has no architecture")
	}
}

// --- the parser, on captured bytes ---

// `docker manifest inspect --verbose` returns an array for a multi-arch
// reference and a bare object for a single-arch one. The single-arch case is
// exactly what this check exists for, so the parser has to handle the shape that
// only appears in the failure.
func TestTheParserReadsBothVerboseShapes(t *testing.T) {
	list := []byte(`[
	  {"Ref":"docker.io/library/nginx:1.27","Descriptor":{"platform":{"architecture":"amd64","os":"linux"}}},
	  {"Ref":"docker.io/library/nginx:1.27","Descriptor":{"platform":{"architecture":"arm64","os":"linux","variant":"v8"}}}
	]`)
	m, err := parseManifest("docker.io/library/nginx:1.27", list)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Publishes(nodes()) || len(m.Platforms) != 2 {
		t.Errorf("a two-platform list parsed as %v", m.Platforms)
	}

	single := []byte(`{"Ref":"docker.io/dockergc00/bad-app:v2","Descriptor":{"platform":{"architecture":"arm64","os":"linux"}}}`)
	m, err = parseManifest("docker.io/dockergc00/bad-app:v2", single)
	if err != nil {
		t.Fatal(err)
	}
	if m.Publishes(nodes()) {
		t.Error("a single arm64 manifest was read as publishing amd64 — this is bad-app, " +
			"and the single-object shape is the one the failure comes in")
	}
	if m.List() != "linux/arm64" {
		t.Errorf("List() = %q, want linux/arm64", m.List())
	}
}

// A registry saying "not there" and a registry not answering are different
// facts, and only the first one blocks.
func TestNotFoundIsToldApartFromCouldNotAsk(t *testing.T) {
	missing := classify("x:v1", "manifest unknown: manifest unknown", errors.New("exit 1"))
	if _, ok := missing.(NotFoundError); !ok {
		t.Errorf("`manifest unknown` was not classified as not-found: %T", missing)
	}
	unauthorised := classify("x:v1", "unauthorized: authentication required", errors.New("exit 1"))
	if _, ok := unauthorised.(NotFoundError); ok {
		t.Error("an auth failure was classified as not-found — a private image would be refused " +
			"for not existing, which is both wrong and unfixable by its owner")
	}
	down := classify("x:v1", "Cannot connect to the Docker daemon", errors.New("exit 1"))
	if _, ok := down.(NotFoundError); ok {
		t.Error("a stopped Docker daemon was classified as not-found")
	}
}

// A variant mismatch is not a reason a pod fails to start, and refusing on one
// would refuse images that work.
func TestVariantsDoNotDecideTheMatch(t *testing.T) {
	m := Manifest{Platforms: []Platform{{OS: "linux", Arch: "arm64"}}}
	if !m.Publishes(Platform{OS: "linux", Arch: "arm64"}) {
		t.Error("linux/arm64 did not match linux/arm64")
	}
}
