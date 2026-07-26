package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// What these tests can and cannot prove, stated once at the top because the
// distinction is the whole reason Phase 11 took five passes.
//
// They can prove: that the three files which have to agree about asset names do
// agree; that the script's os/arch detection maps a real `uname` onto a real
// asset; that a checksum mismatch stops the install and leaves nothing behind;
// and — the one that matters — that the whole script, run for real against a
// served release, downloads a genuine endurance binary, verifies it, puts it
// somewhere, and that the thing it put there answers `version --short` with the
// version it claimed to install.
//
// They cannot prove that GitHub serves the release, that the workflow ran, or
// that a machine which has never seen this project can reach any of it. The
// server here is on 127.0.0.1 and the binary is compiled locally. That half is
// Part B of the runbook and it is a person's job.

// ---------------------------------------------------------------------------
// The three files that have to agree
// ---------------------------------------------------------------------------

func repoRoot(t *testing.T) string {
	t.Helper()
	// cli/internal/installer → cli → the platform tree.
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func scriptPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), "install.sh")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("install.sh not found at %s: %v", p, err)
	}
	return p
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// TestTheScriptAndTheGoTableNameTheSameAssets is the Phase 10 port-numbers test
// in a different costume: two files hold the same list, nothing at runtime
// compares them, and a disagreement is a 404 on somebody else's machine rather
// than a failure here.
//
// It runs the real asset_name function out of the real script, so a typo in the
// case statement fails this rather than a stranger's install.
func TestTheScriptAndTheGoTableNameTheSameAssets(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)

	for _, target := range Targets {
		got := sourceAndCall(t, bash, script, "asset_name "+target.OS+" "+target.Arch)
		if got != target.Asset {
			t.Errorf("install.sh names %s/%s %q; installer.Targets says %q",
				target.OS, target.Arch, got, target.Asset)
		}
	}

	// And the other direction: the script must not publish a name Go has never
	// heard of, because nothing would build it.
	body := readFile(t, script)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "endurance_") && !strings.Contains(line, ") echo endurance_") {
			continue
		}
		name := line[strings.Index(line, "endurance_"):]
		name = strings.Fields(name)[0]
		name = strings.TrimSuffix(name, ";;")
		if !knownAsset(name) {
			t.Errorf("install.sh can ask for %q, which no target in installer.Targets builds", name)
		}
	}
}

// TestAssetForAnswersOnlyForPublishedPairs — an empty answer is what makes a
// caller say which platform it asked about, and a wrong non-empty answer is a
// URL that 404s.
func TestAssetForAnswersOnlyForPublishedPairs(t *testing.T) {
	for _, target := range Targets {
		if got := AssetFor(target.OS, target.Arch); got != target.Asset {
			t.Errorf("AssetFor(%q, %q) = %q, want %q", target.OS, target.Arch, got, target.Asset)
		}
	}
	for _, pair := range [][2]string{{"linux", "386"}, {"windows", "arm64"}, {"", ""}, {"freebsd", "amd64"}} {
		if got := AssetFor(pair[0], pair[1]); got != "" {
			t.Errorf("AssetFor(%q, %q) = %q; nothing is published for it", pair[0], pair[1], got)
		}
	}
	if AssetForThisMachine() != AssetFor(runtime.GOOS, runtime.GOARCH) {
		t.Error("AssetForThisMachine disagrees with AssetFor about this machine")
	}
}

func knownAsset(name string) bool {
	for _, t := range Targets {
		if t.Asset == name {
			return true
		}
	}
	return false
}

// TestTheWorkflowBuildsEveryPublishedTarget reads the real release workflow.
//
// The failure this prevents is asymmetric and quiet: a target in installer.go
// and install.sh that the workflow does not build means the release is missing
// an asset, and only somebody on that OS ever finds out. The reverse — a target
// built and never downloaded — is a wasted minute of CI and worth knowing about
// too, so both directions fail.
func TestTheWorkflowBuildsEveryPublishedTarget(t *testing.T) {
	wf := filepath.Join(repoRoot(t), "..", ".github", "workflows", "release-endurance.yml")
	if _, err := os.Stat(wf); err != nil {
		t.Skipf("release workflow not found at %s: %v", wf, err)
	}
	body := readFile(t, wf)

	for _, target := range Targets {
		if !strings.Contains(body, target.Asset) {
			t.Errorf("the release workflow never builds %s (%s/%s)",
				target.Asset, target.OS, target.Arch)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || !strings.HasPrefix(fields[2], "endurance_") {
			continue
		}
		if !knownAsset(fields[2]) {
			t.Errorf("the release workflow builds %q, which install.sh will never ask for", fields[2])
		}
	}

	// The stamp is what lets `endurance version` tell a release from a working
	// tree, and it is a string in a YAML file that nothing else checks.
	for _, want := range []string{
		"internal/version.Commit=",
		"internal/version.Built=",
		ChecksumsAsset,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the release workflow does not mention %q", want)
		}
	}
}

// TestTheInstallCommandPointsAtTheScriptThatExists — the curl line the CLI
// prints, the path the script is committed at, and the raw URL are three
// spellings of one fact.
func TestTheInstallCommandPointsAtTheScriptThatExists(t *testing.T) {
	if _, err := os.Stat(filepath.Join(repoRoot(t), "install.sh")); err != nil {
		t.Fatalf("ScriptPath is %s but there is no install.sh in the platform tree: %v", ScriptPath, err)
	}
	if !strings.HasSuffix(ScriptPath, "/install.sh") {
		t.Errorf("ScriptPath %q does not end in install.sh", ScriptPath)
	}
	if !strings.Contains(ScriptURL, ScriptPath) {
		t.Errorf("ScriptURL %q does not carry ScriptPath %q", ScriptURL, ScriptPath)
	}
	if !strings.Contains(Command, ScriptURL) || !strings.HasSuffix(Command, "| bash") {
		t.Errorf("Command %q is not `curl … <ScriptURL> | bash`", Command)
	}
	// The repository the script downloads from has to be the one the script
	// itself is served from, or the curl line installs somebody else's binary.
	if !strings.Contains(readFile(t, filepath.Join(repoRoot(t), "install.sh")), Repo) {
		t.Errorf("install.sh does not name %s", Repo)
	}
}

// TestTheLatestURLNeedsNoVersionInTheName. /releases/latest/download only works
// when the asset name is knowable before the version is, so a version in an
// asset name would silently force the installer onto the API — which needs a
// token to avoid a rate limit, and this project does not have one.
func TestTheLatestURLNeedsNoVersionInTheName(t *testing.T) {
	for _, target := range Targets {
		if strings.Contains(target.Asset, "v0.") || strings.Contains(target.Asset, "{{") {
			t.Errorf("asset %q carries a version; /releases/latest/download cannot name it", target.Asset)
		}
	}
	if got := DownloadURL("", "endurance_linux_amd64"); got != ReleasesURL+"/latest/download/endurance_linux_amd64" {
		t.Errorf("latest URL = %q", got)
	}
	if got := DownloadURL("v0.11.0", "endurance_linux_amd64"); got != ReleasesURL+"/download/v0.11.0/endurance_linux_amd64" {
		t.Errorf("pinned URL = %q", got)
	}
}

// ---------------------------------------------------------------------------
// The script's own functions, run out of the real script
// ---------------------------------------------------------------------------

// TestTheScriptRecognisesThisMachine. The detection is three case statements
// and it is the part that cannot be reasoned about from a desk: `uname -s` on
// git-bash prints MINGW64_NT-10.0-26200, which matches none of the obvious
// patterns. So this asks the script about the machine it is running on and
// checks the answer against what Go says the machine is.
func TestTheScriptRecognisesThisMachine(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)

	os_ := sourceAndCall(t, bash, script, "detect_os")
	arch := sourceAndCall(t, bash, script, "detect_arch")
	if os_ == "" || arch == "" {
		t.Fatalf("install.sh does not recognise this machine: detect_os=%q detect_arch=%q", os_, arch)
	}
	if os_ != runtime.GOOS || arch != runtime.GOARCH {
		t.Errorf("install.sh says %s/%s; Go says %s/%s", os_, arch, runtime.GOOS, runtime.GOARCH)
	}
	if got, want := sourceAndCall(t, bash, script, "asset_name "+os_+" "+arch), AssetForThisMachine(); got != want {
		t.Errorf("install.sh would download %q for this machine; the release publishes %q", got, want)
	}
}

// TestTheScriptRefusesAMachineItHasNoBinaryFor — a platform with no asset must
// produce nothing rather than an empty URL, which would download GitHub's 404
// page and hand somebody an HTML file called endurance.
func TestTheScriptRefusesAMachineItHasNoBinaryFor(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	for _, pair := range []string{"linux 386", "freebsd amd64", "windows arm64", "plan9 amd64"} {
		if got := sourceAndCall(t, bash, script, "asset_name "+pair); got != "" {
			t.Errorf("asset_name %s = %q; nothing is published for it", pair, got)
		}
	}
}

// TestTheScriptOrdersVersionsTheWayAnUpgradeNeeds. The comparison decides
// whether the run says "upgrading", "downgrading" or "reinstalling", and the
// case it must not get wrong is 0.10.3 → 0.11.0: string order says 11 < 3.
func TestTheScriptOrdersVersionsTheWayAnUpgradeNeeds(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	cases := []struct{ a, b, want string }{
		{"v0.10.3", "v0.11.0", "-1"},
		{"v0.11.0", "v0.10.3", "1"},
		{"v0.11.0", "v0.11.0", "0"},
		{"v0.9.1", "v0.10.0", "-1"},
		{"v1.0.0", "v0.11.0", "1"},
		{"0.11.0", "v0.11.0", "0"},
		{"v0.11.0-rc1", "v0.11.0", "different"},
		{"nonsense", "v0.11.0", "different"},
	}
	for _, c := range cases {
		if got := sourceAndCall(t, bash, script, "compare_versions "+c.a+" "+c.b); got != c.want {
			t.Errorf("compare_versions %s %s = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// TestTheScriptInstallsWhereItWasToldAndSaysSoOtherwise. The PATH decision is
// the one place this script could quietly do the wrong thing: installing into a
// directory nothing searches produces a successful run and a `command not
// found`, and that is a worse outcome than refusing.
func TestTheScriptInstallsWhereItWasToldAndSaysSoOtherwise(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)

	dir := t.TempDir()
	if got := sourceAndCallEnv(t, bash, script, "choose_dir", "ENDURANCE_INSTALL_DIR="+dir); got != dir {
		t.Errorf("ENDURANCE_INSTALL_DIR was ignored: choose_dir = %q, want %q", got, dir)
	}

	// A trailing slash is what a user types; it must not become a double slash
	// in a path the script then chmods.
	if got := sourceAndCallEnv(t, bash, script, "choose_dir", "ENDURANCE_INSTALL_DIR="+dir+"/"); got != dir {
		t.Errorf("a trailing slash survived: choose_dir = %q, want %q", got, dir)
	}

	// on_path is what decides whether the run ends with advice or without it.
	if out := sourceAndCallEnv(t, bash, script,
		`if on_path /opt/nowhere; then echo yes; else echo no; fi`, "PATH=/usr/bin:/bin"); out != "no" {
		t.Errorf("on_path claimed /opt/nowhere is on PATH=/usr/bin:/bin")
	}
	if out := sourceAndCallEnv(t, bash, script,
		`if on_path /usr/bin; then echo yes; else echo no; fi`, "PATH=/usr/bin:/bin"); out != "yes" {
		t.Errorf("on_path did not find /usr/bin on PATH=/usr/bin:/bin")
	}
}

// TestTheScriptNeverReachesForSudo. Not a style rule: a one-line curl that
// escalates is a thing people should have to decide to do rather than have
// happen to them, and once it is in the file somebody will paste it into a
// machine they do not own.
func TestTheScriptNeverReachesForSudo(t *testing.T) {
	body := readFile(t, scriptPath(t))
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue // the comment explaining why there is no sudo
		}
		if strings.Contains(trimmed, "sudo ") {
			t.Errorf("install.sh runs sudo: %s", trimmed)
		}
	}
}

// TestTheScriptInstallsOnlyTheBinary. `endurance doctor` reports on Docker,
// kind, kubectl, helm and istioctl, in the tool's own voice and with the reason
// each is needed. An installer that also installed them would be a second,
// worse doctor — and it would be the thing that makes `curl … | bash` a
// dangerous line rather than a convenient one.
func TestTheScriptInstallsOnlyTheBinary(t *testing.T) {
	body := readFile(t, scriptPath(t))
	for _, forbidden := range []string{
		"apt-get install", "apt install", "brew install", "yum install",
		"dnf install", "choco install", "winget install", "go install",
		"kind create", "kubectl apply", "helm install", "docker run",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("install.sh runs %q — the installer's job is one binary", forbidden)
		}
	}
}

// ---------------------------------------------------------------------------
// The whole script, run for real
// ---------------------------------------------------------------------------

// TestTheInstallerInstallsARunnableBinary is the offline half of the phase's
// exit criterion, and it is as close as a test can get.
//
// A local server plays the part of the release: it serves a real endurance
// binary — cross-compiled here, stamped as though the workflow had built it —
// under the asset name this machine will ask for, plus a checksums.txt over it.
// The script is then run with nothing faked inside it: it detects the platform,
// downloads, hashes, compares, installs, and runs what it installed.
//
// The assertion at the end is the one that matters. It is not "the file
// exists": it is that the installed file, executed from where it was put,
// reports the version the run said it was installing. Every previous phase that
// shipped a fault shipped one where a screen claimed an outcome nobody had
// asked the system about.
func TestTheInstallerInstallsARunnableBinary(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binary := buildStampedCLI(t)
	srv := serveRelease(t, map[string][]byte{asset: binary})
	dir := filepath.Join(t.TempDir(), "bin")

	out := runInstaller(t, bash, script, srv.URL, dir, nil)

	installed := filepath.Join(dir, installedName())
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("the installer reported success and there is no binary at %s\n%s", installed, out)
	}

	// The claim, checked against the thing itself rather than against the
	// transcript that made it.
	got, err := exec.Command(installed, "version", "--short").Output()
	if err != nil {
		t.Fatalf("the installed binary does not run: %v\n%s", err, out)
	}
	if want := "endurance " + currentVersion(t); strings.TrimSpace(string(got)) != want {
		t.Errorf("the installed binary reports %q, want %q\n%s", strings.TrimSpace(string(got)), want, out)
	}
	if !strings.Contains(out, "is installed at") {
		t.Errorf("the run never said where it installed anything:\n%s", out)
	}
	if !strings.Contains(out, "verified") {
		t.Errorf("the run never mentioned verifying the download:\n%s", out)
	}

	// A stamped build has to be legible as one, or `endurance version` cannot
	// tell a release from somebody's working tree.
	long, err := exec.Command(installed, "version").Output()
	if err == nil && !strings.Contains(string(long), "release build") {
		t.Errorf("a stamped binary does not report itself as a release build:\n%s", long)
	}
}

// TestACorruptedDownloadInstallsNothing. The checksum is the only reason
// `curl … | bash` is defensible at all, so the failure path is worth more than
// the success one: a mismatch must stop, say both digests, and leave the
// directory it was going to install into exactly as it found it.
func TestACorruptedDownloadInstallsNothing(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binary := buildStampedCLI(t)
	srv := serveRelease(t, map[string][]byte{asset: binary})
	srv.corrupt(asset) // the checksums file now describes a different file
	dir := filepath.Join(t.TempDir(), "bin")

	out, err := tryInstaller(t, bash, script, srv.URL, dir, nil)
	if err == nil {
		t.Fatalf("a corrupted download installed successfully:\n%s", out)
	}
	if !strings.Contains(out, "checksum mismatch") {
		t.Errorf("the failure did not name the checksum:\n%s", out)
	}
	if !strings.Contains(out, "nothing was installed") {
		t.Errorf("the failure did not say that nothing was installed:\n%s", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused install left %d files in %s", len(entries), dir)
	}
}

// TestAReleaseWithNoChecksumsInstallsNothing. The other half: an installer that
// skips verification when the manifest is missing verifies nothing, because a
// release that lost its checksums file and a server that is lying look
// identical from here.
func TestAReleaseWithNoChecksumsInstallsNothing(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	srv := serveRelease(t, map[string][]byte{asset: buildStampedCLI(t)})
	srv.withhold(ChecksumsAsset)
	dir := filepath.Join(t.TempDir(), "bin")

	out, err := tryInstaller(t, bash, script, srv.URL, dir, nil)
	if err == nil {
		t.Fatalf("a release with no %s installed successfully:\n%s", ChecksumsAsset, out)
	}
	if !strings.Contains(out, "nothing was installed") {
		t.Errorf("the failure did not say that nothing was installed:\n%s", out)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused install left %d files in %s", len(entries), dir)
	}
}

// TestReinstallingSaysWhatItReplaced. Running the line twice is what everybody
// does, and the second run has to be legible: an upgrade names both versions, a
// same-version run says it is a reinstall rather than pretending to be new.
func TestReinstallingSaysWhatItReplaced(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	binary := buildStampedCLI(t)
	srv := serveRelease(t, map[string][]byte{asset: binary})
	dir := filepath.Join(t.TempDir(), "bin")

	// The version is part of every phrase asserted here, and not for tidiness:
	// t.TempDir() names the directory after the test, so the transcript carries
	// "TestReinstallingSaysWhatItReplaced" in every path it prints and a bare
	// search for "Reinstalling" matches the fixture rather than the message.
	ver := currentVersion(t)

	if out := runInstaller(t, bash, script, srv.URL, dir, nil); strings.Contains(out, "Reinstalling "+ver) {
		t.Errorf("the first install called itself a reinstall:\n%s", out)
	}

	// Second run, with the first install visible on PATH — which is what makes
	// it a reinstall rather than a fresh one.
	shellDir := posixPath(t, bash, dir)
	out := runInstaller(t, bash, script, srv.URL, dir, []string{"PATH=" + shellDir + ":" + shellPath(t, bash)})
	if !strings.Contains(out, "Reinstalling "+ver) {
		t.Errorf("re-running the installer over the same version did not say so:\n%s", out)
	}
	if !strings.Contains(out, "replacing "+shellDir+"/endurance") {
		t.Errorf("the reinstall did not name the file it replaced:\n%s", out)
	}
	// Two warnings that must not fire on a run that replaced its own install.
	// The first is the directory being off PATH, which it is not; the second is
	// a rival endurance elsewhere, which on git-bash is the file the installer
	// wrote a second earlier — `command -v endurance` answers without the .exe
	// the file actually carries, and a literal comparison makes the run end by
	// warning about a conflict with itself.
	if strings.Contains(out, "is not on PATH") {
		t.Errorf("a directory that is on PATH was reported as not on it:\n%s", out)
	}
	if strings.Contains(out, "another endurance is still on PATH") {
		t.Errorf("the installer warned about a conflict with the file it just wrote:\n%s", out)
	}
}

// TestAnUpgradeNamesBothVersions. The version being replaced comes out of the
// binary that is there, not out of a file, so this stands a deliberately older
// build on PATH and checks the sentence.
func TestAnUpgradeNamesBothVersions(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	old := filepath.Join(t.TempDir(), "old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCLI(t, filepath.Join(old, installedName()), "v0.0.1")

	srv := serveRelease(t, map[string][]byte{asset: buildStampedCLI(t)})
	dir := filepath.Join(t.TempDir(), "bin")

	out := runInstaller(t, bash, script, srv.URL, dir,
		[]string{"PATH=" + old + string(os.PathListSeparator) + os.Getenv("PATH")})
	if !strings.Contains(out, "Upgrading v0.0.1 to "+currentVersion(t)) {
		t.Errorf("the upgrade did not name both versions:\n%s", out)
	}
}

// TestInstallingSomewhereOffPathSaysSo. The run "succeeded" and `endurance`
// does not exist — the exact shape of untruth this project has fixed in a
// success screen, a bootstrap and an uninstall already.
func TestInstallingSomewhereOffPathSaysSo(t *testing.T) {
	bash := requireBash(t)
	script := scriptPath(t)
	requireCurl(t)
	asset := AssetForThisMachine()
	if asset == "" {
		t.Skipf("no release asset is published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	srv := serveRelease(t, map[string][]byte{asset: buildStampedCLI(t)})
	// A fresh temp directory is not on anybody's PATH, which is the condition
	// under test. PATH itself is left alone on purpose: stripping it to prove
	// a point also strips curl, and the script would then fail for the wrong
	// reason and still pass this test.
	dir := filepath.Join(t.TempDir(), "nowhere")

	out := runInstaller(t, bash, script, srv.URL, dir, nil)
	if !strings.Contains(out, "is not on PATH") {
		t.Errorf("installing outside PATH was not reported:\n%s", out)
	}
	if !strings.Contains(out, "export PATH=") {
		t.Errorf("the run did not print the line that fixes it:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------------

func requireBash(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	return bash
}

func requireCurl(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not on PATH — install.sh cannot download anything without it")
	}
}

// posixPath asks the shell what it calls a directory.
//
// On git-bash `$PATH` holds /c/Users/me/bin while Go hands out
// C:\Users\me\bin, and a test that builds a PATH out of Go's spelling is
// testing a shell that would never see it. The script canonicalises the same
// way, so this is the shell's own answer rather than a second conversion.
func posixPath(t *testing.T, bash, dir string) string {
	t.Helper()
	out, err := exec.Command(bash, "-c", `cd "$1" && pwd`, "bash", dir).Output()
	if err != nil {
		t.Fatalf("asking the shell for %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// shellPath is $PATH as the shell has it, which on Windows is not os.Getenv.
func shellPath(t *testing.T, bash string) string {
	t.Helper()
	out, err := exec.Command(bash, "-c", `printf '%s' "$PATH"`).Output()
	if err != nil {
		t.Fatalf("asking the shell for PATH: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func installedName() string {
	if runtime.GOOS == "windows" {
		return "endurance.exe"
	}
	return "endurance"
}

// sourceAndCall sources install.sh with ENDURANCE_LIB=1 — which defines its
// functions and installs nothing — and evaluates one expression in it.
func sourceAndCall(t *testing.T, bash, script, expr string) string {
	t.Helper()
	return sourceAndCallEnv(t, bash, script, expr)
}

func sourceAndCallEnv(t *testing.T, bash, script, expr string, env ...string) string {
	t.Helper()
	cmd := exec.Command(bash, "-c", ". \"$1\"; "+expr, "bash", script)
	cmd.Env = append(append(os.Environ(), "ENDURANCE_LIB=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash %s: %v\n%s", expr, err, out)
	}
	return strings.TrimSpace(string(out))
}

func runInstaller(t *testing.T, bash, script, base, dir string, env []string) string {
	t.Helper()
	out, err := tryInstaller(t, bash, script, base, dir, env)
	if err != nil {
		t.Fatalf("install.sh failed: %v\n%s", err, out)
	}
	return out
}

func tryInstaller(t *testing.T, bash, script, base, dir string, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command(bash, script)
	cmd.Env = append(os.Environ(),
		"ENDURANCE_BASE_URL="+base,
		"ENDURANCE_INSTALL_DIR="+dir,
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// currentVersion reads the version the CLI declares, out of the source, so this
// package's tests do not import the version package and quietly compare a
// constant to itself.
func currentVersion(t *testing.T) string {
	t.Helper()
	body := readFile(t, filepath.Join(repoRoot(t), "cli", "internal", "version", "version.go"))
	const marker = `const Current = "`
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatal("could not find const Current in internal/version/version.go")
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, `"`)]
}

var (
	builtOnce sync.Once
	builtPath string
	builtErr  error
)

// buildStampedCLI compiles the real CLI with the same ldflags the release
// workflow uses, so what the fake release serves is what a real one would.
//
// Compiled once per test binary: five tests download it and go build is the
// slowest thing in this package by an order of magnitude.
func buildStampedCLI(t *testing.T) []byte {
	t.Helper()
	builtOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			builtErr = fmt.Errorf("go not on PATH")
			return
		}
		dir, err := os.MkdirTemp("", "endurance-release-*")
		if err != nil {
			builtErr = err
			return
		}
		out := filepath.Join(dir, installedName())
		const mod = "github.com/gc-ghub/endurance"
		ldflags := "-X " + mod + "/internal/version.Commit=abc1234" +
			" -X " + mod + "/internal/version.Built=2026-07-26T00:00:00Z"
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", out, ".")
		cmd.Dir = filepath.Join(repoRoot(t), "cli")
		if b, err := cmd.CombinedOutput(); err != nil {
			builtErr = fmt.Errorf("go build: %v\n%s", err, b)
			return
		}
		builtPath = out
	})
	if builtErr != nil {
		t.Skipf("cannot build the CLI to serve as a release artefact: %v", builtErr)
	}
	b, err := os.ReadFile(builtPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeFakeCLI writes something that answers `version --short` and nothing
// else, for the cases that only need a version already on PATH.
func writeFakeCLI(t *testing.T, path, version string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows will not execute a shell script named .exe, so the older
		// build has to be a real one. It is the same binary with a different
		// version stamped in, which is exactly what an older release is.
		const mod = "github.com/gc-ghub/endurance"
		if _, err := exec.LookPath("go"); err != nil {
			t.Skip("go not on PATH")
		}
		ldflags := "-X " + mod + "/internal/version.Commit=old0000" +
			" -X " + mod + "/internal/version.Built=2026-01-01T00:00:00Z"
		cmd := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", path, ".")
		cmd.Dir = filepath.Join(repoRoot(t), "cli")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, b)
		}
		// The stamp cannot change a constant, so patch the answer by name: the
		// test needs a *different* version on PATH, and the only honest way to
		// get one on Windows is a wrapper the shell will run. bash finds
		// `endurance` before `endurance.exe` when both are present.
		script := filepath.Join(filepath.Dir(path), "endurance")
		writeShellCLI(t, script, version)
		return
	}
	writeShellCLI(t, path, version)
}

func writeShellCLI(t *testing.T, path, version string) {
	t.Helper()
	body := "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"endurance " + version + "\"; fi\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// release is a stand-in for the GitHub release: the assets, a checksums.txt
// over them, and the two ways it can be wrong.
type release struct {
	URL   string
	files map[string][]byte
	mu    sync.Mutex
}

func serveRelease(t *testing.T, assets map[string][]byte) *release {
	t.Helper()
	r := &release{files: map[string][]byte{}}
	var sums strings.Builder
	for name, body := range assets {
		r.files[name] = body
		sum := sha256.Sum256(body)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	r.files[ChecksumsAsset] = []byte(sums.String())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		body, ok := r.files[strings.TrimPrefix(req.URL.Path, "/")]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	r.URL = srv.URL
	return r
}

// corrupt changes an asset after its digest was recorded — a truncated
// download, a proxy that rewrote it, or a server that is lying. From the
// installer's side these are the same event and it must refuse all three.
func (r *release) corrupt(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.files[name] = append([]byte("tampered"), r.files[name]...)
}

func (r *release) withhold(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.files, name)
}
