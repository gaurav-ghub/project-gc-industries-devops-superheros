package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/success"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := render.SetDefault(render.New(&buf, render.WithColor(false), render.WithTTY(false)))
	t.Cleanup(func() { render.SetDefault(old) })
	return &buf
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "apps", "superheros", "app.yaml")); err != nil {
		t.Skipf("superheros is not registered under %s", root)
	}
	return root
}

// TestCatalogListIsAnsweredFromFilesAlone.
//
// "What have I onboarded" is a question about this repo, not about kubernetes,
// and it must work on a laptop with no cluster and no kubectl. The proof is that
// nothing in this package can call one: List takes a root and no kubectl at all.
func TestCatalogListIsAnsweredFromFilesAlone(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)

	if err := List(root); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "superheros") {
		t.Errorf("the registered application is missing:\n%s", got)
	}
	// The facts on the line all come from apps/superheros/app.yaml.
	for _, want := range []string{"5 services", "canary catalog", "route /"} {
		if !strings.Contains(got, want) {
			t.Errorf("the row does not carry %q:\n%s", want, got)
		}
	}
}

// TestAnEmptyCatalogPointsAtInit — the first thing a stranger sees on a fresh
// clone, so it names the command that fixes it rather than reporting a void.
func TestAnEmptyCatalogPointsAtInit(t *testing.T) {
	buf := capture(t)
	if err := List(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "endurance init") {
		t.Errorf("an empty catalog does not say what to do next:\n%s", got)
	}
	if strings.Contains(got, "▸") {
		t.Errorf("an empty catalog printed a row:\n%s", got)
	}
}

// TestCatalogGetIsTheSameScreenAsStatus.
//
// The rename kept both names on purpose, and the thing that makes that safe is
// that they are one function. Two renderings of "how is my application doing"
// would be two answers, and the one that mattered would be whichever a
// screenshot came from — the same reasoning that exported
// platform.ParsePodTable in Phase 10.
func TestCatalogGetIsTheSameScreenAsStatus(t *testing.T) {
	root := repoRoot(t)

	viaCatalog := capture(t)
	if err := Get(root, "superheros"); err != nil {
		t.Fatal(err)
	}
	fromCatalog := viaCatalog.String()

	viaStatus := capture(t)
	if err := success.Screen(success.Options{Root: root, App: "superheros"}); err != nil {
		t.Fatal(err)
	}
	if got := viaStatus.String(); got != fromCatalog {
		t.Errorf("catalog get and status <app> render differently:\n--- catalog get ---\n%s\n--- status ---\n%s",
			fromCatalog, got)
	}
}

// TestAnUnknownApplicationIsRefusedByName.
func TestAnUnknownApplicationIsRefusedByName(t *testing.T) {
	capture(t)
	if err := Get(t.TempDir(), "nope"); err == nil {
		t.Fatal("an unregistered application rendered a screen")
	}
}

// TestTheRowsAlignAndCarryNoGlyph — List draws the ▸ with render.Step, so the
// glyph comes from the one place that owns it and a row can never carry a
// second one.
func TestTheRowsAlignAndCarryNoGlyph(t *testing.T) {
	apps := []spec.App{
		{Name: "a", Namespace: "a", Services: []spec.Service{{Name: "a"}}},
		{Name: "longer-name", Namespace: "longer-ns", Owner: "team",
			Services: []spec.Service{{Name: "x"}, {Name: "y"}},
			Route:    spec.Route{Enabled: true, Path: "/x"}},
	}
	lines := Lines(apps)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	for _, l := range lines {
		for _, glyph := range []string{render.IconStep, render.IconOK, render.IconWarn, render.IconError} {
			if strings.Contains(l, glyph) {
				t.Errorf("a row carries a glyph of its own: %q", l)
			}
		}
	}
	// The namespace column starts at the same offset on both rows.
	if strings.Index(lines[0], "a  ") != 0 {
		t.Errorf("the first row is not padded from column zero: %q", lines[0])
	}
	if !strings.Contains(lines[0], "1 service") || !strings.Contains(lines[1], "2 services") {
		t.Errorf("the service count does not pluralise: %q / %q", lines[0], lines[1])
	}
}
