package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBannerGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.Banner("v0.7.0")
	golden(t, "banner", buf.String())
}

func TestLogLinesGolden(t *testing.T) {
	r, buf, _ := fixture(t)
	r.Section("Onboard an application")
	r.Step("Rendering manifests")
	r.Detail("charts/app · 5 services")
	r.Success("wrote apps/superheros/app.yaml")
	r.Warn("commit skipped: nothing staged")
	r.Error("2 enforced violation(s)")
	r.Info("not committed — review the files, then commit when ready")
	r.Blank()
	r.Print("raw output that is not ours")
	r.Printf("%d services, %d ready", 5, 4)
	r.Rule()
	golden(t, "log-lines", buf.String())
}

// TestOneGlyphPerMeaning pins the vocabulary itself. Phases 9–11 add commands,
// and the cheapest way to end up with two visual systems again is for one of
// them to invent a sixth glyph. The set changes here or not at all.
func TestOneGlyphPerMeaning(t *testing.T) {
	want := map[string]string{
		"step": "▸", "ok": "✓", "warn": "⚠", "error": "✗", "info": "·", "next": "→",
	}
	got := map[string]string{
		"step": IconStep, "ok": IconOK, "warn": IconWarn,
		"error": IconError, "info": IconInfo, "next": IconNext,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("glyph %q = %q, want %q", k, got[k], v)
		}
	}
	r, buf, _ := fixture(t)
	r.Step("a")
	r.Success("b")
	r.Warn("c")
	r.Error("d")
	r.Info("e")
	r.Dashboard("f", nil, []string{"g"}) // → lives on next-step lines
	for _, g := range want {
		if !strings.Contains(buf.String(), g+" ") {
			t.Errorf("no line rendered with glyph %q", g)
		}
	}
}

// TestColorOffIsPlainText is the contract the golden files rest on: with colour
// disabled the output contains no escape sequences at all.
func TestColorOffIsPlainText(t *testing.T) {
	r, buf, _ := fixture(t)
	r.Banner("v0.7.0")
	r.Section("Whatever")
	r.Success("done")
	r.Dashboard("Summary", [][2]string{{"App", r.Value("superheros")}}, []string{"do a thing"})
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatalf("colour is off but the output contains escape sequences:\n%q", buf.String())
	}
}

// TestColorOnStylesLines is the other half: forcing colour on really does emit
// SGR sequences, so "colour off" is a decision and not a broken palette.
func TestColorOnStylesLines(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf, WithColor(true), WithTTY(false))
	r.Success("done")
	if !strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("colour is on but nothing was styled: %q", buf.String())
	}
	// Styling must not change the visible width — every alignment in this
	// package depends on that.
	if w := len(stripANSITest(buf.String())); w != len("✓ done\n") {
		t.Errorf("styled line has %d visible bytes, want %d", w, len("✓ done\n"))
	}
}

func stripANSITest(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{-time.Second, "0ms"},
		{340 * time.Millisecond, "340ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{1500 * time.Millisecond, "1.5s"},
		{59500 * time.Millisecond, "59.5s"},
		{time.Minute, "1m00s"},
		{4*time.Minute + 12*time.Second, "4m12s"},
		{90 * time.Minute, "90m00s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestSectionWidth pins the rule to the renderer's width, whatever the title.
func TestSectionWidth(t *testing.T) {
	for _, title := range []string{"A", "Onboard an application", strings.Repeat("x", 80)} {
		r, buf, _ := fixture(t)
		r.Section(title)
		line := strings.TrimSpace(buf.String())
		if len(title) < DefaultWidth-4 && len([]rune(line)) != DefaultWidth {
			t.Errorf("section %q rendered %d columns, want %d", title, len([]rune(line)), DefaultWidth)
		}
		if !strings.HasPrefix(line, "── "+title) {
			t.Errorf("section %q did not open with its title: %q", title, line)
		}
	}
}

// TestDefaultRendererIsRedirectable — the package-level shorthands must be
// capturable, or no other package can ever test its own output.
func TestDefaultRendererIsRedirectable(t *testing.T) {
	var buf bytes.Buffer
	old := SetDefault(New(&buf, WithColor(false), WithTTY(false)))
	defer SetDefault(old)

	Success("captured")
	if got := buf.String(); got != "✓ captured\n" {
		t.Errorf("package-level Success wrote %q", got)
	}
}
