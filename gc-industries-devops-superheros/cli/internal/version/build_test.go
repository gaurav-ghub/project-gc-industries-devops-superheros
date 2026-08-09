package version

import (
	"strings"
	"testing"
)

// restore puts the release stamp back, so one test cannot make the next one
// think it is running inside a release.
func restore(t *testing.T) {
	t.Helper()
	commit, built := Commit, Built
	t.Cleanup(func() { Commit, Built = commit, built })
}

// The tell Phase 13's runbook now sends people to. It has to survive being
// extended with the identity half, because the sentence a runbook quotes is a
// sentence a test has to pin.
func TestADevBuildSaysSo(t *testing.T) {
	restore(t)
	Commit, Built = "", ""

	p := Provenance()
	if !strings.Contains(p, "dev build") {
		t.Errorf("Provenance() = %q; the tell is `dev build`", p)
	}
	if strings.Contains(p, "release build") {
		t.Errorf("Provenance() = %q; a dev build must not call itself a release", p)
	}
	if IsRelease() {
		t.Error("an unstamped binary reported itself as a release")
	}
}

func TestAReleaseBuildCarriesItsCommitAndDate(t *testing.T) {
	restore(t)
	Commit, Built = "2bb846c", "2026-07-26T12:48:47Z"

	p := Provenance()
	for _, want := range []string{"release build", "2bb846c", "2026-07-26T12:48:47Z"} {
		if !strings.Contains(p, want) {
			t.Errorf("Provenance() = %q, missing %q", p, want)
		}
	}
	if strings.Contains(p, "dev build") {
		t.Errorf("Provenance() = %q; a stamped build is not a dev build", p)
	}
}

// The whole of B1 in one assertion.
//
// Two binaries, twelve days and one phase apart, both reporting v0.11.0 — and
// both correct, because Current is a constant and Phase 13 changed no command's
// shape. The version cannot tell them apart and must not be asked to. The build
// line has to.
func TestTwoBuildsOfOneVersionAreDistinguishable(t *testing.T) {
	restore(t)

	Commit, Built = "2bb846c", "2026-07-26T12:48:47Z"
	july := Provenance()

	Commit, Built = "", ""
	today := Provenance()

	if july == today {
		t.Fatalf("the July release and today's working-tree build print the same build line (%q) — "+
			"this is exactly the pair that nearly closed Phase 13 on a fault", july)
	}
	if Current == "" {
		t.Fatal("version.Current is empty")
	}
}

// A release's stamped commit is the one the workflow verified against the tag,
// so it wins over whatever Go embedded — they are the same commit, and only one
// of them was checked.
func TestTheStampedCommitWins(t *testing.T) {
	restore(t)
	Commit = "abcdef1234567890"

	if got := Revision(); got != "abcdef1" {
		t.Errorf("Revision() = %q, want the seven-character form the release workflow cuts", got)
	}
}
