// Package installer holds the handful of facts that install.sh, the release
// workflow and the CLI all have to agree about, and the one line a user types
// to get Endurance.
//
// # Why a package rather than three copies
//
// Three files decide whether `curl … | bash` works: the workflow that
// cross-compiles and names the assets, the shell script that guesses an asset
// name from `uname` and downloads it, and the CLI, which prints the install
// command in its own uninstall output. Nothing at runtime compares them — a
// disagreement produces a 404 on somebody else's machine, which is the same
// shape of failure as Phase 10's two port numbers: everything builds, every
// test passes, and the thing answers nothing.
//
// So the names live here once, and installer_test.go reads the real workflow
// and the real script and fails when either drifts.
//
// # What is deliberately not here
//
// No download logic. The CLI does not install itself and does not update
// itself: the installer is bash, it runs before this binary exists, and a
// second implementation in Go would be a second answer to the same question.
// `endurance uninstall` removes the binary (Phase 11) and names this command as
// the way back.
package installer

import "runtime"

const (
	// Repo is the GitHub repository that publishes Endurance releases.
	//
	// Its releases are Endurance CLI releases and nothing else — install.sh
	// resolves "latest" through /releases/latest/download, which is a redirect
	// rather than an API call, so it needs no token and has no rate limit, and
	// in exchange it takes whatever release is newest. Phase 13 makes that
	// structurally true by moving the platform to its own repository; until
	// then it is a rule, written down here because the failure mode is an
	// installer that silently fetches an application's release.
	Repo = "gc-ghub/project-gc-industries-devops-superheros"

	// ScriptPath is where install.sh lives in that repository. The platform is
	// a subdirectory of this repo until Phase 13 splits it out, and the script
	// belongs with the platform it installs rather than at a root it is about
	// to leave.
	ScriptPath = "gc-industries-devops-superheros/install.sh"

	// ScriptURL is the address in the curl line. raw.githubusercontent serves
	// the branch, so what somebody pipes into bash is the file in main —
	// reviewable in the repository, not a build artefact. Each release attaches
	// a copy of the same file for anyone who wants the script and the binaries
	// pinned together.
	ScriptURL = "https://raw.githubusercontent.com/" + Repo + "/main/" + ScriptPath

	// Command is the whole of the installation instructions.
	Command = "curl -fsSL " + ScriptURL + " | bash"

	// ChecksumsAsset is the sha256sum-format manifest published beside the
	// binaries. install.sh refuses to install anything it cannot check against
	// this file.
	ChecksumsAsset = "checksums.txt"

	// ReleasesURL is where the artefacts live.
	ReleasesURL = "https://github.com/" + Repo + "/releases"
)

// A Target is one binary a release publishes.
//
// Asset names carry no version, on purpose: /releases/latest/download/<asset>
// only works when the name is knowable before the version is, and an installer
// that has to learn the version before it can build a URL needs the API, a
// JSON parser and a rate limit.
type Target struct {
	OS, Arch, Asset string
}

// Targets is what a release ships. Windows/amd64 is git-bash, which is the
// machine this platform is actually developed on; linux/arm64 is an Apple
// Silicon Mac running the verification container.
var Targets = []Target{
	{OS: "windows", Arch: "amd64", Asset: "endurance_windows_amd64.exe"},
	{OS: "darwin", Arch: "arm64", Asset: "endurance_darwin_arm64"},
	{OS: "darwin", Arch: "amd64", Asset: "endurance_darwin_amd64"},
	{OS: "linux", Arch: "amd64", Asset: "endurance_linux_amd64"},
	{OS: "linux", Arch: "arm64", Asset: "endurance_linux_arm64"},
}

// AssetFor names the published binary for a GOOS/GOARCH pair, or "" when the
// release does not build one. A caller that gets "" must say which pair it
// asked about — "unsupported platform" with no platform in it is not a report.
func AssetFor(goos, goarch string) string {
	for _, t := range Targets {
		if t.OS == goos && t.Arch == goarch {
			return t.Asset
		}
	}
	return ""
}

// AssetForThisMachine names the binary this build would have been published as.
func AssetForThisMachine() string { return AssetFor(runtime.GOOS, runtime.GOARCH) }

// DownloadURL is where an asset of a given release lives. An empty tag means
// the latest release, which GitHub serves from a fixed path by redirecting.
func DownloadURL(tag, asset string) string {
	if tag == "" {
		return ReleasesURL + "/latest/download/" + asset
	}
	return ReleasesURL + "/download/" + tag + "/" + asset
}
