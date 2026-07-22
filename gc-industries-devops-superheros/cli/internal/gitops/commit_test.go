package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tempRepo makes a real git repository in a temp dir. `--commit` has been
// carried forward as "never exercised" since Phase 2 precisely because there was
// nowhere safe to run it: the CLI's own repo is the user's, and Claude does not
// commit to it. A throwaway repo is that missing place.
func tempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@launchpad.invalid"},
		{"config", "user.name", "LaunchPad Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v (%s)", args, err, out)
		}
	}
	return root
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitCommitsTheGeneratedFiles(t *testing.T) {
	root := tempRepo(t)
	written, err := Generate(root, sampleApp(), "https://example.com/platform.git", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if status := git(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("working tree should be clean after --commit, got:\n%s", status)
	}
	if subject := git(t, root, "log", "-1", "--pretty=%s"); subject != "launchpad: onboard superheros" {
		t.Errorf("commit subject = %q", subject)
	}
	files := git(t, root, "show", "--name-only", "--pretty=format:")
	for _, want := range []string{"app.yaml", "values.yaml", "application.yaml"} {
		if !strings.Contains(files, want) {
			t.Errorf("commit is missing %s; it contains:\n%s", want, files)
		}
	}
}

func TestCommitStagesNothingItWasNotGiven(t *testing.T) {
	// `git add -A` would be shorter and would sweep a developer's unrelated
	// work-in-progress into a commit whose message claims to be a release.
	root := tempRepo(t)
	written, err := Generate(root, sampleApp(), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(stray, []byte("work in progress\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err != nil {
		t.Fatal(err)
	}
	if files := git(t, root, "show", "--name-only", "--pretty=format:"); strings.Contains(files, "notes.txt") {
		t.Errorf("an unrelated file was swept into the commit:\n%s", files)
	}
	if status := git(t, root, "status", "--porcelain"); !strings.Contains(status, "notes.txt") {
		t.Errorf("the unrelated file should still be uncommitted, got:\n%s", status)
	}
}

func TestReleaseCommitCarriesOnlyTheTwoChangedFiles(t *testing.T) {
	root := tempRepo(t)
	written, err := Generate(root, sampleApp(), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err != nil {
		t.Fatal(err)
	}

	bump, err := SetServiceTag(root, "superheros", "frontend", "", "v2-abc1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, bump.Written, "launchpad: release superheros/frontend v2-abc1234"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	files := strings.Fields(git(t, root, "show", "--name-only", "--pretty=format:"))
	if len(files) != 2 {
		t.Fatalf("a release commit should carry exactly two files, got %v", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "application.yaml") {
			t.Errorf("application.yaml must never be in a release commit: %v", files)
		}
	}
	if status := git(t, root, "status", "--porcelain"); status != "" {
		t.Errorf("working tree should be clean, got:\n%s", status)
	}
}

func TestCommitNeverPushes(t *testing.T) {
	// The rule since Phase 1: LaunchPad writes and commits; a human pushes. A
	// repo with no remote proves it by construction — a push would error, and
	// Commit returns nil.
	root := tempRepo(t)
	written, err := Generate(root, sampleApp(), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err != nil {
		t.Fatalf("Commit must succeed without a remote: %v", err)
	}
	if remotes := git(t, root, "remote"); remotes != "" {
		t.Fatalf("test repo unexpectedly has a remote: %q", remotes)
	}
}

func TestCommitReportsGitFailureRatherThanSwallowingIt(t *testing.T) {
	// Not a git repository at all — the mistake the Phase 2 runbook warns about,
	// running the CLI from the outer folder instead of the repo.
	root := t.TempDir()
	written, err := Generate(root, sampleApp(), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err == nil {
		t.Fatal("expected an error when root is not a git repository")
	}
}

func TestCommitRefusesAnEmptyFileList(t *testing.T) {
	root := tempRepo(t)
	if err := Commit(root, nil, "launchpad: nothing"); err == nil {
		t.Fatal("expected an error committing no files")
	}
}

func TestHeadSubjectReportsTheCommitThatWasMade(t *testing.T) {
	root := tempRepo(t)
	written, err := Generate(root, sampleApp(), "r", "")
	if err != nil {
		t.Fatal(err)
	}
	if s := HeadSubject(root); s != "" {
		t.Errorf("a repo with no commits has no HEAD subject, got %q", s)
	}
	if err := Commit(root, written, "launchpad: onboard superheros"); err != nil {
		t.Fatal(err)
	}
	if s := HeadSubject(root); !strings.Contains(s, "launchpad: onboard superheros") {
		t.Errorf("HeadSubject = %q", s)
	}
}
