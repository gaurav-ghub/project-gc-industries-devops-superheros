// Package catalog is the registry, as a developer asks about it: which
// applications does this platform know, and what is one of them doing.
//
// # Why the verb is `catalog` and the old names still work
//
// Phase 1 shipped `endurance list` and `endurance status <app>`, and both were
// right at the time: there was one noun and one question. There are several
// nouns now — `endurance status` with no argument is the *platform's* health,
// and `endurance status <app>` is an application's post-deploy screen — and one
// verb answering about two different things is how a CLI becomes hard to
// explain. `catalog list` and `catalog get <app>` name the thing being asked
// about.
//
// The old names are kept, deliberately and permanently, as aliases:
// `endurance list` is `catalog list`, and `endurance status <app>` is
// `catalog get <app>`. Removing them would break every transcript in
// test-evidence/, every step in MANUAL-TESTING.md and the muscle memory of the
// one person who has been using this platform since Phase 1 — for the sake of a
// name. The help text names `catalog` first and marks the other two as what
// they are.
//
// # It reads files, and asks the cluster only for `get`
//
// `list` is answered entirely from apps/*/app.yaml. That is on purpose: it must
// work on a laptop with no cluster, because "what have I onboarded" is a
// question about this repo and not about kubernetes.
package catalog

import (
	"fmt"
	"strings"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/success"
)

// List prints every registered application.
func List(root string) error {
	apps, err := gitops.List(root)
	if err != nil {
		return err
	}
	render.Section("Catalog")
	if len(apps) == 0 {
		render.Info("no applications registered yet")
		render.Detail("`endurance init` walks you through the first one")
		render.Detail("`endurance onboard` is the full form, for a multi-service application")
		return nil
	}
	for _, l := range Lines(apps) {
		render.Step(l)
	}
	render.Blank()
	render.Info(fmt.Sprintf("%s registered · `endurance catalog get <app>` shows one in detail",
		plural(len(apps), "application")))
	return nil
}

// Lines renders one catalog row per application, aligned, without the glyph —
// List draws that with render.Step, so the ▸ comes from the one place that owns
// it. Separated from printing so a test can assert on the content rather than
// on the frame.
//
// Deliberately not kubectl's shape: this is a registry listing, and every
// column comes from a file in this repo. Whether any of it is *running* is a
// different question, and `catalog get` is where it gets asked.
func Lines(apps []spec.App) []string {
	nameW, nsW := 0, 0
	for _, a := range apps {
		if len(a.Name) > nameW {
			nameW = len(a.Name)
		}
		if len(a.Namespace) > nsW {
			nsW = len(a.Namespace)
		}
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		line := render.Value(pad(a.Name, nameW)) +
			"  " + render.Muted(pad(a.Namespace, nsW)) +
			"  " + render.Muted(plural(len(a.Services), "service"))
		var tags []string
		if len(a.CanaryServices()) > 0 {
			tags = append(tags, "canary "+strings.Join(a.CanaryServices(), ","))
		}
		if a.Route.Enabled {
			tags = append(tags, "route "+a.Route.Path)
		}
		if a.Owner != "" {
			tags = append(tags, a.Owner)
		}
		if len(tags) > 0 {
			line += "  " + render.Muted("· "+strings.Join(tags, " · "))
		}
		out = append(out, line)
	}
	return out
}

// Get prints one application's post-deploy screen.
//
// It is the same function `endurance status <app>` calls, not a second
// rendering of the same facts — there is one success screen on this platform
// and two ways to ask for it.
func Get(root, app string) error {
	return success.Screen(success.Options{Root: root, App: app})
}

func pad(s string, n int) string {
	if len(s) < n {
		return s + strings.Repeat(" ", n-len(s))
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
