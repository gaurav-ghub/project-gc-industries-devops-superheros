package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

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
