package platform

import (
	"errors"
	"strings"
	"testing"

	"github.com/gc-ghub/endurance/internal/render"
)

// running is a machine where the platform's cluster exists.
func running(root string) func() ([]string, error) {
	return func() ([]string, error) { return []string{"kind", ClusterName(root)}, nil }
}

// noClusters is a machine where kind has nothing.
func noClusters() ([]string, error) { return nil, nil }

// TestDestroyAsksFirst — the only command in the CLI that removes something a
// user waited ten minutes for. Answering no must leave the cluster alone.
func TestDestroyAsksFirst(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}
	asked := ""

	err := Destroy(DestroyOptions{
		Root: root, run: f.run, clusters: running(root),
		Confirm: func(prompt string) (bool, error) { asked = prompt; return false, nil },
	})
	if err != nil {
		t.Fatalf("declining the prompt returned an error: %v", err)
	}
	if len(f.ran) != 0 {
		t.Fatalf("the teardown ran anyway: %v", f.ran)
	}
	if !strings.Contains(asked, ClusterName(root)) {
		t.Errorf("the prompt does not name the cluster: %q", asked)
	}
	if got := buf.String(); !strings.Contains(got, "nothing was deleted") {
		t.Errorf("the outcome was not stated:\n%s", got)
	}
}

// TestDestroySaysWhatSurvives — the reason destroy is safe to reach for is that
// git is untouched, and the command has to say so before it asks.
func TestDestroySaysWhatSurvives(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}

	if err := Destroy(DestroyOptions{
		Root: root, Yes: true, run: f.run, clusters: running(root),
	}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	got := buf.String()

	if !strings.Contains(got, "nothing in git is touched") {
		t.Errorf("destroy does not say what survives:\n%s", got)
	}
	if len(f.ran) != 1 || f.ran[0] != destroyScript {
		t.Errorf("ran %v, want [%s]", f.ran, destroyScript)
	}
	if !strings.Contains(got, "Platform destroyed") {
		t.Errorf("no closing summary:\n%s", got)
	}
}

// TestDestroyDoesNotClaimToHaveDeletedNothing.
//
// With no cluster there, the run still happens — it clears a kubectl context
// kind may have left — but the summary must not say "deleted". The transcript
// underneath says the cluster did not exist, and a box that contradicts its own
// transcript is worse than no box.
func TestDestroyDoesNotClaimToHaveDeletedNothing(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}
	asked := false

	err := Destroy(DestroyOptions{
		Root: root, run: f.run, clusters: noClusters,
		Confirm: func(string) (bool, error) { asked = true; return true, nil },
	})
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if asked {
		t.Error("destroy asked permission to delete a cluster that does not exist")
	}
	got := buf.String()
	if !strings.Contains(got, "Nothing to destroy") {
		t.Errorf("the summary claims a deletion that did not happen:\n%s", got)
	}
	if strings.Contains(got, "(deleted)") {
		t.Errorf("the summary says deleted:\n%s", got)
	}
	// The context cleanup is still worth running.
	if len(f.ran) != 1 {
		t.Errorf("the teardown did not run: %v", f.ran)
	}
}

// TestDestroyProceedsWhenKindCannotBeAsked — if `kind get clusters` fails, the
// CLI does not know, so it says nothing it cannot support and lets the module
// decide (it refuses without kind).
func TestDestroyProceedsWhenKindCannotBeAsked(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{}

	err := Destroy(DestroyOptions{
		Root: root, Yes: true, run: f.run,
		clusters: func() ([]string, error) { return nil, errors.New("kind: not found") },
	})
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if len(f.ran) != 1 {
		t.Errorf("the teardown did not run: %v", f.ran)
	}
	if got := buf.String(); strings.Contains(got, "Nothing to destroy") {
		t.Errorf("destroy claimed to know a cluster was absent when it could not ask:\n%s", got)
	}
}

func TestDestroyReportsAFailedTeardown(t *testing.T) {
	root := repoRoot(t)
	buf := capture(t)
	f := &fakeRun{fail: map[string]error{destroyScript: errors.New("exit status 1")}}

	if err := Destroy(DestroyOptions{
		Root: root, Yes: true, run: f.run, clusters: running(root),
	}); err == nil {
		t.Fatal("a failed teardown was reported as success")
	}
	got := buf.String()
	if !strings.Contains(got, "✗ Deleting the kind cluster") {
		t.Errorf("the failure is not marked:\n%s", got)
	}
	if strings.Contains(got, "Platform destroyed") {
		t.Errorf("a failed teardown printed the success box:\n%s", got)
	}
}

// TestDestroyRefusesToGuessWhenItCannotAsk — piped or scripted, a confirmation
// that cannot be shown must not be assumed. It names the flag that says yes.
func TestDestroyRefusesToGuessWhenItCannotAsk(t *testing.T) {
	capture(t) // not a TTY
	ok, err := askConfirm("Delete the kind cluster?")
	if ok {
		t.Fatal("a prompt that could not be shown was answered yes")
	}
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the error does not name the flag: %v", err)
	}
	_ = render.Default()
}
