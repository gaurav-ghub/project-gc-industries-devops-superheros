// Package prompt is the keymap every Endurance question answers to.
//
// The output side of this CLI has been one library since Phase 7. The input
// side had not been, and it showed. Each question was built where it was asked,
// so `init` ran six separate one-field forms in a row — and a form with one
// field has nothing to go back to. huh binds "back" to shift+tab and quit to
// ctrl+c alone, which left a user who said yes to AI by accident with no way
// out but killing the run.
//
// So the keys live here, once, and every form in the tool is built through this
// package:
//
//	↑ / shift+tab   the previous question
//	↓ / tab / enter the next one
//	esc             cancel
//
// ctrl+c still works everywhere; esc is the addition, because esc is what
// people press.
package prompt

import (
	"errors"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

// Hint is the one-line reminder to print above a form. The keys are useless if
// nobody knows they are there, and huh's own help line is easy to miss under a
// long description.
const Hint = "↑/↓ move between questions · enter accepts · esc cancels, and nothing is created"

// KeyMap returns huh's defaults with esc bound to quit and the arrow keys bound
// to previous/next.
//
// ↑/↓ are added only to fields that do not already use them. Select and
// MultiSelect move their own cursor with those keys and are deliberately left
// exactly as huh ships them — rebinding those would break the field to fix the
// form.
func KeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()

	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc", "cancel"))

	km.Input.Prev = back("shift+tab", "up")
	km.Input.Next = forward("enter", "tab", "down")

	km.Confirm.Prev = back("shift+tab", "up")
	km.Confirm.Next = forward("enter", "tab", "down")

	km.Note.Prev = back("shift+tab", "up")
	km.Note.Next = forward("enter", "tab", "down")

	return km
}

func back(keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp("↑", "back"))
}

func forward(keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp("↓/enter", "next"))
}

// Form builds a form on the shared keymap. Every huh form in Endurance is built
// here rather than with huh.NewForm, so there is one answer to "which keys
// work" instead of one per call site.
func Form(groups ...*huh.Group) *huh.Form {
	return huh.NewForm(groups...).WithKeyMap(KeyMap())
}

// Run runs a single field as its own form, on the shared keymap. Used where
// there genuinely is only one question — `enable ai`, the uninstall confirm —
// and never as a way to ask several in a row.
func Run(field huh.Field) error {
	return Form(huh.NewGroup(field)).Run()
}

// Cancelled reports whether err is a user pressing esc or ctrl+c. It is not a
// failure and must not be reported as one: the user answered the question, and
// the answer was no.
func Cancelled(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}
