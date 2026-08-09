package version

import (
	"runtime/debug"
	"strings"
)

// Build identity — what this binary *is*, as opposed to what it declares.
//
// # Why this file exists
//
// Phase 13's live run was very nearly closed on a fault because two `endurance`
// binaries twelve days and one phase apart both printed `v0.11.0`, and both were
// right: Current is a constant in the source, Phase 13 changed no command's
// shape, and so it correctly earned no bump. The one identity check the runbook
// asked for was therefore structurally incapable of catching the one
// substitution that mattered — the binary on $PATH generated applications with
// no `mesh:` block and no `managedNamespaceMetadata`, and the pods came up 2/2
// anyway through the chart's other injection path, so the missing half was
// invisible behind the working half.
//
// The lesson is that **release identity and build identity are different facts**.
// A version string answers "which release is this"; it cannot answer "which
// build is this", and the more disciplined the versioning the less it can tell
// you. So the number stays where it is and this file adds the fact underneath
// it, which is different for every build even when the version is not.
//
// # Where it comes from
//
// Two independent sources, in order of trustworthiness:
//
//   - Commit and Built, stamped by the release workflow's -ldflags. Present only
//     in a release, which is exactly what makes them worth checking first.
//   - The VCS stamp Go embeds by itself. `go build` records vcs.revision,
//     vcs.time and vcs.modified into the build info of any main package built
//     inside a git work tree, with no flags and no cooperation from this file —
//     so a build somebody made by hand carries its own identity whether or not
//     they meant it to. This is the half that would have named B1's two binaries
//     apart on its own.
//
// The path and modification time of the executable itself are the third source
// and live in internal/platform, because reading the filesystem is that
// package's job and this one is meant to stay pure.

// The VCS facts Go embedded at build time, read once. Empty in a binary built
// outside a work tree, or with -buildvcs=false, and empty is an honest answer
// rather than a failure.
var (
	vcsRevision string
	vcsTime     string
	vcsModified bool
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			vcsRevision = s.Value
		case "vcs.time":
			vcsTime = s.Value
		case "vcs.modified":
			vcsModified = s.Value == "true"
		}
	}
}

// shortSHA is how long a commit is quoted in a build line. Seven characters,
// matching the release workflow's own `cut -c1-7`, so a release build and a dev
// build of the same commit print the same string.
const shortSHA = 7

// Revision is the commit this binary was built from, short, or "" when nothing
// recorded one. A release's stamped Commit wins over the VCS stamp: they are the
// same commit, and the stamped one is the one the workflow verified.
func Revision() string {
	if Commit != "" {
		return short(Commit)
	}
	return short(vcsRevision)
}

// Dirty reports whether the work tree had uncommitted changes at build time.
// Only ever true for a dev build — the release workflow builds from a clean
// checkout, and a dirty release would be a bug in the workflow rather than a
// fact about this binary.
func Dirty() bool { return vcsModified }

// Stamp is when this binary was built, as a date. The release workflow's, or
// Go's own VCS commit time, or "".
func Stamp() string {
	if Built != "" {
		return Built
	}
	return vcsTime
}

// IsRelease reports whether this binary came out of the release workflow.
func IsRelease() bool { return Commit != "" }

// Provenance is the one phrase `endurance version` prints about this binary, and
// it is the line a runbook should send somebody to read.
//
// It says the kind of build first, because that is the coarse question — "is
// this the thing I installed, or the thing I built?" — and then the identity,
// because two builds of the same kind and the same version are exactly the case
// that cost Phase 13's live run an hour. `dev build` is still the tell it has
// always been; what is new is that two dev builds no longer read identically.
func Provenance() string {
	var b strings.Builder
	if IsRelease() {
		b.WriteString("release build")
	} else {
		b.WriteString("dev build — not installed from a release")
	}
	if rev := Revision(); rev != "" {
		b.WriteString(" · ")
		b.WriteString(rev)
		if Dirty() {
			// A build made over uncommitted edits is not the commit it names,
			// and during a phase that is the normal state of cli/'s binary.
			b.WriteString("-dirty")
		}
	} else if !IsRelease() {
		b.WriteString(" · no commit recorded")
	}
	if s := Stamp(); s != "" {
		b.WriteString(" · ")
		b.WriteString(s)
	}
	return b.String()
}

func short(sha string) string {
	if len(sha) > shortSHA {
		return sha[:shortSHA]
	}
	return sha
}
