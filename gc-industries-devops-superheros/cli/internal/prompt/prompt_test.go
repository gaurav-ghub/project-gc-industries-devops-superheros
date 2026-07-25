package prompt

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
)

// TestEscQuits.
//
// The reason this package exists. huh binds quit to ctrl+c alone, so esc did
// nothing at all — a user who wanted out of a question had to kill the run.
func TestEscQuits(t *testing.T) {
	km := KeyMap()
	if !contains(km.Quit.Keys(), "esc") {
		t.Errorf("esc is not bound to quit: %v", km.Quit.Keys())
	}
	if !contains(km.Quit.Keys(), "ctrl+c") {
		t.Errorf("ctrl+c stopped quitting: %v — it must keep working", km.Quit.Keys())
	}
}

// TestTheArrowKeysMoveBetweenQuestions — on the three field types Endurance
// asks with. Up and down are what people reach for; shift+tab is what huh
// documents, and both must work.
func TestTheArrowKeysMoveBetweenQuestions(t *testing.T) {
	km := KeyMap()
	for _, c := range []struct {
		field      string
		prev, next []string
	}{
		{"Input", km.Input.Prev.Keys(), km.Input.Next.Keys()},
		{"Confirm", km.Confirm.Prev.Keys(), km.Confirm.Next.Keys()},
		{"Note", km.Note.Prev.Keys(), km.Note.Next.Keys()},
	} {
		if !contains(c.prev, "up") || !contains(c.prev, "shift+tab") {
			t.Errorf("%s: back is %v, want both up and shift+tab", c.field, c.prev)
		}
		if !contains(c.next, "down") || !contains(c.next, "enter") {
			t.Errorf("%s: next is %v, want both down and enter", c.field, c.next)
		}
	}
}

// TestSelectKeepsItsOwnArrowKeys.
//
// Select and MultiSelect move their cursor with up/down. Rebinding those to
// "previous question" would break the field to fix the form, so this package
// deliberately does not touch them.
func TestSelectKeepsItsOwnArrowKeys(t *testing.T) {
	km := KeyMap()
	if contains(km.Select.Prev.Keys(), "up") {
		t.Error("Select.Prev took over the up arrow — it moves the cursor there")
	}
	if !contains(km.Select.Up.Keys(), "up") {
		t.Error("Select lost its up arrow")
	}
	if contains(km.MultiSelect.Prev.Keys(), "up") {
		t.Error("MultiSelect.Prev took over the up arrow")
	}
}

// TestEveryFormIsBuiltHere.
//
// The bug this package fixes was not one bad form, it was eight forms built in
// three packages with nobody owning the keys. A call to huh.NewForm outside
// this file is a call that will not have esc bound, and it will be found by a
// user, not by a reviewer.
func TestEveryFormIsBuiltHere(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(path), "internal/prompt/") {
			return nil // this package is where huh.NewForm belongs
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "huh.NewForm(") {
				offenders = append(offenders, filepath.ToSlash(path)+":"+strconv.Itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("huh.NewForm called outside internal/prompt — these forms will not "+
			"have esc bound or the arrow keys working:\n  %s\nUse prompt.Form or prompt.Run.",
			strings.Join(offenders, "\n  "))
	}
}

// TestCancelledRecognisesAnAbort — a user pressing esc is an answer, not a
// crash, and the caller has to be able to tell the difference.
func TestCancelledRecognisesAnAbort(t *testing.T) {
	if !Cancelled(huh.ErrUserAborted) {
		t.Error("an aborted form was not recognised as cancelled")
	}
	if Cancelled(nil) {
		t.Error("a successful form was reported as cancelled")
	}
	if Cancelled(os.ErrNotExist) {
		t.Error("an unrelated error was reported as cancelled")
	}
}

func contains(keys []string, want string) bool {
	return slices.Contains(keys, want)
}
