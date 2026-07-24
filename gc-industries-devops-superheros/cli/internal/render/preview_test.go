package render

import (
	"errors"
	"flag"
	"os"
	"testing"
	"time"
)

// TestPreview prints the entire visual language to stdout, in colour, so a
// human can look at it:
//
//	go test ./internal/render -run TestPreview -preview -v
//
// The golden files prove the output did not change; only an eye can say whether
// it is any good. This is the manual verification step for Phase 8 — everything
// below is rendered by the same primitives `endurance bootstrap` (Phase 9) and
// the success screen (Phase 10) will use, so what you see here is what those
// commands will look like.
//
// It is skipped without -preview: it is a demo, not an assertion.
var preview = flag.Bool("preview", false, "print the visual language to stdout instead of skipping")

func TestPreview(t *testing.T) {
	if !*preview {
		t.Skip("pass -preview to see the visual language rendered in colour")
	}
	c := newClock()
	r := New(os.Stdout, WithColor(true), WithTTY(false), WithClock(c.now))

	r.Banner("v0.7.0")

	r.Section("Log lines")
	r.Step("work starting")
	r.Detail("a secondary fact under it")
	r.Success("work that succeeded")
	r.Warn("work that succeeded, with a caveat")
	r.Error("work that failed")
	r.Info("context that is not work at all")

	p := r.NewProgress("Bootstrapping the platform", bootstrapSteps...)
	s := p.Start("Creating the kind cluster")
	c.advance(12 * time.Second)
	s.DoneWith("kind-superheros · 1 node")

	s = p.Start("Installing Istio")
	out := s.Stream()
	out.Write([]byte("✔ Istio core installed\n✔ Istiod installed\n✔ Ingress gateways installed\n"))
	out.Close()
	c.advance(41 * time.Second)
	s.Done()

	s = p.Start("Installing AI alert enrichment")
	s.Skip("no OPENAI_API_KEY configured")

	s = p.Start("Installing ArgoCD")
	c.advance(30 * time.Second)
	s.Done()

	s = p.Start("Installing Kyverno policies")
	c.advance(2 * time.Second)
	_ = s.Fail(errors.New("exit status 1"))
	p.Finish()

	r.Section("Preflight")
	r.Checks([]Check{
		{Name: "docker", State: StateReady, Note: "27.1.1 · daemon running"},
		{Name: "kind", State: StateReady, Note: "v0.23.0"},
		{Name: "helm", State: StateWarn, Note: "v3.12.0 — 3.14+ recommended"},
		{Name: "git", State: StateFailed, Note: "not found — https://git-scm.com/downloads"},
	})

	r.URLBlock("Platform URLs", superherosResult().URLs)
	r.Dashboard("Application onboarded",
		[][2]string{{"App", r.Value("superheros")}, {"Namespace", r.Value("superheros")}},
		[]string{"git push — ArgoCD deploys from the repo"})
	r.SuccessScreen(superherosResult())
}
