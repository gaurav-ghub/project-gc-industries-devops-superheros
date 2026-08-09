package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gc-ghub/endurance/internal/render"
)

// Which `endurance` is running — the check Phase 13's live run needed and did
// not have.
//
// # The fault this exists for
//
// `~/.local/bin/endurance.exe` was a release build from twelve days and one
// phase earlier. `cli/endurance.exe` was the build of the phase under test.
// **Both printed `v0.11.0`, and both were right**, because Current is a constant
// and Phase 13 changed no command's shape. The runbook said `endurance init` in
// one step and `./cli/endurance.exe` in another, and on any machine that has
// ever run this project's own release installer those are two different
// programs. The better the installer works, the more reliably the trap is armed.
//
// version.Provenance names *what* a binary is. This file answers the question
// underneath it — is the endurance you just ran the one this repo built? — by
// comparing the running executable against whatever `go build` has left in
// <root>/cli. It reads modification times, not versions, because the whole point
// is that the versions agreed.
//
// # Why the newest, and why the count is reported
//
// `cli/` accumulates a binary from every phase that ever built one, all
// git-ignored: `launchpad.exe`, `endurance`, `endurance.exe`. A runbook that
// names `./cli/endurance` in one step and `./cli/endurance.exe` in another runs
// two different CLIs in one pass, and that has already cost this project two
// runs. So the comparison is against the newest, and the others are named rather
// than silently ignored — an old binary sitting beside a new one is the trap,
// and the only useful thing to do with it is say it is there.

// BinaryNames are the file names a build of this CLI leaves in <root>/cli.
//
// `launchpad` is on the list because the binary was called that through Phase 6
// and a machine that has been used since then still has one — it is the same
// program under an earlier name, it will happily generate applications, and it
// predates every decision made since.
var BinaryNames = []string{
	"endurance", "endurance.exe",
	"launchpad", "launchpad.exe",
}

// BuildDir is where a working tree's build lands, relative to the platform repo
// root. It is `go build`'s default output directory for the main package, which
// is the whole reason binaries collect there.
const BuildDir = "cli"

// A Binary is one endurance executable on this machine.
type Binary struct {
	Path    string
	ModTime time.Time
	Size    int64
}

// Name is the file name, for a message that does not need 90 columns of path.
func (b Binary) Name() string { return filepath.Base(b.Path) }

// When is the build time as a person reads it. Minute precision: the fault this
// catches is measured in hours and days, and seconds would only make two lines
// harder to compare.
func (b Binary) When() string { return b.ModTime.Format("2006-01-02 15:04") }

// A BinaryReport says which endurance is running and how it relates to what this
// repo has built.
type BinaryReport struct {
	// Running is the executable this process was started from. Zero when it
	// could not be resolved, which is not a failure — it is a fact about the
	// platform the CLI happens to be on, and every check below simply declines
	// to make a claim.
	Running Binary
	// Builds is every endurance binary found in <root>/cli, newest first.
	Builds []Binary
	// Stale is the one conclusion worth acting on: the running binary is not the
	// newest build in <root>/cli, and the newest build is newer than it.
	Stale bool
}

// Newest is the most recently built binary in <root>/cli, or the zero Binary.
func (r BinaryReport) Newest() (Binary, bool) {
	if len(r.Builds) == 0 {
		return Binary{}, false
	}
	return r.Builds[0], true
}

// RunningIsABuild reports whether the process was started from <root>/cli — the
// ordinary case while working on a phase, and the case where nothing can be
// stale.
func (r BinaryReport) RunningIsABuild() bool {
	if r.Running.Path == "" {
		return false
	}
	for _, b := range r.Builds {
		if sameFile(b.Path, r.Running.Path) {
			return true
		}
	}
	return false
}

// Warnings is what to print, and it is empty in every case but the one that
// matters.
//
// Two situations earn a line. The running binary being older than this repo's
// build is the B1 fault itself. Several binaries sitting in cli/ is the trap one
// level up — nothing is wrong yet, and the next runbook step that names the
// other one makes it wrong.
func (r BinaryReport) Warnings() []string {
	var out []string
	newest, ok := r.Newest()
	if ok && r.Stale {
		out = append(out, fmt.Sprintf(
			"the endurance you are running is older than the one this repo has built — %s (%s) vs %s (%s)",
			r.Running.Path, r.Running.When(), newest.Path, newest.When()))
		out = append(out, "two builds can print the same version and generate different files · "+
			"copy the build over it before a live run:")
		out = append(out, "  cp "+filepath.ToSlash(newest.Path)+` "$HOME/.local/bin/"`)
	}
	if len(r.Builds) > 1 {
		var names []string
		for _, b := range r.Builds {
			names = append(names, b.Name()+" ("+b.When()+")")
		}
		out = append(out, "cli/ holds "+fmt.Sprint(len(r.Builds))+" endurance binaries — "+
			strings.Join(names, ", ")+" · `rm` the ones you are not testing")
	}
	return out
}

// InspectBinary builds the report for the platform repo at root.
func InspectBinary(root string) BinaryReport {
	return inspectBinary(root, os.Executable)
}

// reportBinary prints what the report found, and prints nothing when there is
// nothing to say — which is the usual case and the one this must not clutter.
func reportBinary(r BinaryReport) {
	warnings := r.Warnings()
	if len(warnings) == 0 {
		return
	}
	render.Blank()
	render.Warn(warnings[0])
	for _, w := range warnings[1:] {
		render.Detail(w)
	}
	render.Blank()
}

// inspectBinary is the testable half: the one question it asks the host — which
// executable am I? — is the parameter.
//
// That separation is not decoration. Phase 12 shipped a broken release because
// sixteen tests asked the host whether docker, kind and kubectl were installed,
// and Phase 13 found one more of the class still in the tree. A test for this
// file that called os.Executable would be asking where the test binary happens
// to live, which is a fact about the machine running the suite.
func inspectBinary(root string, executable func() (string, error)) BinaryReport {
	var r BinaryReport

	if exe, err := executable(); err == nil && exe != "" {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if abs, err := filepath.Abs(exe); err == nil {
			exe = abs
		}
		if fi, err := os.Stat(exe); err == nil {
			r.Running = Binary{Path: exe, ModTime: fi.ModTime(), Size: fi.Size()}
		}
	}

	dir := filepath.Join(root, BuildDir)
	for _, name := range BinaryNames {
		p := filepath.Join(dir, name)
		fi, err := os.Stat(p)
		if err != nil || fi.IsDir() {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		r.Builds = append(r.Builds, Binary{Path: p, ModTime: fi.ModTime(), Size: fi.Size()})
	}
	sort.SliceStable(r.Builds, func(i, j int) bool {
		return r.Builds[i].ModTime.After(r.Builds[j].ModTime)
	})

	newest, ok := r.Newest()
	// Nothing to compare against, no running binary to compare, or the running
	// binary *is* one of the builds: in all three there is no claim to make. The
	// last one is the ordinary case while a phase is being built, and warning
	// about it would be the Phase 9 fault of warning about the normal thing.
	if ok && r.Running.Path != "" && !r.RunningIsABuild() {
		r.Stale = newest.ModTime.After(r.Running.ModTime)
	}
	return r
}

// sameFile compares two paths for identity, case-insensitively on Windows where
// `C:\Users\me\cli\endurance.exe` and `c:\users\me\cli\endurance.exe` are one
// file and a byte comparison says they are two.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}
