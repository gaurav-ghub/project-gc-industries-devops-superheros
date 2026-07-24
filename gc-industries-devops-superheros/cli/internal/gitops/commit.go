package gitops

import (
	"fmt"
	"os/exec"
	"strings"
)

// Commit stages exactly the files Endurance wrote and commits them with msg.
//
// It never pushes, and it never stages anything it was not handed. `git add -A`
// would be one character shorter and would sweep a developer's unrelated work
// into a commit whose message claims to be a release — so the file list is
// always explicit.
//
// This is the single commit path behind `--commit` on onboard, release and
// canary. It used to be three near-identical private functions, which is three
// places for the flag to be subtly wrong and, until Phase 5, zero places it was
// covered by a test.
func Commit(root string, files []string, msg string) error {
	if len(files) == 0 {
		return fmt.Errorf("nothing to commit")
	}
	args := append([]string{"-C", root, "add", "--"}, files...)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("git", "-C", root, "commit", "-m", msg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// HeadSubject returns the subject line of the repo's current commit, or "" if
// root is not a git repository with any commits. Used to show a developer what
// `--commit` actually produced rather than asserting it worked.
func HeadSubject(root string) string {
	out, err := exec.Command("git", "-C", root, "log", "-1", "--pretty=%h %s").CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
