package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// B1, as a test.
//
// Phase 13's live run was nearly closed on a fault because two binaries one
// phase apart both printed `v0.11.0` and both were right. Every test here is
// about the fact underneath the version, and none of them asks the host which
// executable it is running — that is the parameter, for the reason
// inspectBinary's comment gives.

// aBinary writes a file standing in for an endurance build, with the
// modification time it is meant to have. Real files, in a temp dir: the code
// under test reads the filesystem, and a fake filesystem would be testing the
// fake.
func aBinary(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("not really a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	return p
}

func startedFrom(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}

// The fault itself: the binary on $PATH is older than the one this repo built,
// and it is not in cli/ so nothing else would notice.
func TestAnOlderBinaryOnPathIsReported(t *testing.T) {
	root := t.TempDir()
	build := aBinary(t, filepath.Join(root, BuildDir), "endurance.exe", 0)
	installed := aBinary(t, filepath.Join(t.TempDir(), "bin"), "endurance.exe", 12*24*time.Hour)

	r := inspectBinary(root, startedFrom(installed))
	if !r.Stale {
		t.Fatalf("a %s binary beside a build from today was not reported stale", "twelve-day-old")
	}
	warnings := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(warnings, installed) || !strings.Contains(warnings, build) {
		t.Errorf("the warning names neither binary — it has to name both, "+
			"because the fault is that they are indistinguishable:\n%s", warnings)
	}
	if !strings.Contains(warnings, "same version") {
		t.Errorf("the warning does not say why this matters — that the two print the "+
			"same version is the whole of B1:\n%s", warnings)
	}
}

// The other direction, and the one that must stay silent: running the build
// itself is what anybody working on a phase does, and Phase 9's rule is that
// warning about the normal case is how a warning becomes noise.
func TestRunningTheRepoBuildSaysNothing(t *testing.T) {
	root := t.TempDir()
	build := aBinary(t, filepath.Join(root, BuildDir), "endurance.exe", 0)

	r := inspectBinary(root, startedFrom(build))
	if r.Stale {
		t.Error("running cli/endurance.exe itself was reported stale")
	}
	if !r.RunningIsABuild() {
		t.Error("the running binary is cli/endurance.exe and was not recognised as a build")
	}
	if got := r.Warnings(); len(got) != 0 {
		t.Errorf("running the build warned about itself: %v", got)
	}
}

// A binary newer than this repo's build is not stale. Somebody who installed a
// later release and is reading an older checkout has the newer tool, and telling
// them to overwrite it with the old one would be actively wrong.
func TestANewerBinaryOnPathIsNotStale(t *testing.T) {
	root := t.TempDir()
	aBinary(t, filepath.Join(root, BuildDir), "endurance.exe", 30*24*time.Hour)
	installed := aBinary(t, filepath.Join(t.TempDir(), "bin"), "endurance.exe", 0)

	if r := inspectBinary(root, startedFrom(installed)); r.Stale {
		t.Error("a binary newer than the repo's build was reported stale")
	}
}

// Memory item #15, one level down: cli/ collects a binary from every phase that
// ever built one, and a runbook naming `./cli/endurance` in one step and
// `./cli/endurance.exe` in another runs two different CLIs in one pass. That has
// cost this project two runs, so the pair is named rather than silently ignored.
func TestSeveralBinariesInCliAreNamed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, BuildDir)
	aBinary(t, dir, "endurance", 16*24*time.Hour) // the v0.9.0 one that stripped routes:
	build := aBinary(t, dir, "endurance.exe", 0)

	r := inspectBinary(root, startedFrom(build))
	if len(r.Builds) != 2 {
		t.Fatalf("found %d binaries in cli/, want 2", len(r.Builds))
	}
	if newest, _ := r.Newest(); newest.Path != build {
		t.Errorf("newest is %s, want %s — the comparison must be against the newest", newest.Path, build)
	}
	warnings := strings.Join(r.Warnings(), "\n")
	if !strings.Contains(warnings, "endurance") || !strings.Contains(warnings, "2 endurance binaries") {
		t.Errorf("two binaries in cli/ produced no warning naming them:\n%s", warnings)
	}
}

// `launchpad` is the same program under the name it had through Phase 6. A
// machine that has been used since then still has one, it will happily generate
// applications, and it predates every decision made since — so it counts.
func TestTheOldLaunchpadNameCounts(t *testing.T) {
	root := t.TempDir()
	old := aBinary(t, filepath.Join(root, BuildDir), "launchpad.exe", 380*24*time.Hour)
	found := false
	for _, b := range inspectBinary(root, startedFrom(old)).Builds {
		if b.Path == old {
			found = true
		}
	}
	if !found {
		t.Error("a launchpad binary in cli/ was not recognised — it is this CLI under its first name")
	}
}

// Nothing to compare against is not a warning. A stranger who installed the
// binary and has no clone must not be told their tool is out of date by a check
// that found no build to compare it with.
func TestNoBuildInTheRepoIsSilent(t *testing.T) {
	root := t.TempDir()
	installed := aBinary(t, filepath.Join(t.TempDir(), "bin"), "endurance", 0)

	r := inspectBinary(root, startedFrom(installed))
	if r.Stale {
		t.Error("a repo with no build in cli/ made the installed binary stale")
	}
	if got := r.Warnings(); len(got) != 0 {
		t.Errorf("nothing to compare against still warned: %v", got)
	}
}

// The host may decline to say which executable is running, and on that machine
// the check simply makes no claim rather than guessing.
func TestAnUnknowableExecutableMakesNoClaim(t *testing.T) {
	root := t.TempDir()
	aBinary(t, filepath.Join(root, BuildDir), "endurance", 0)

	r := inspectBinary(root, func() (string, error) { return "", os.ErrNotExist })
	if r.Stale {
		t.Error("an unresolvable executable was reported stale")
	}
}
