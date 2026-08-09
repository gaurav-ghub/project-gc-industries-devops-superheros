// Package onboard implements `endurance onboard` — the non-interactive door
// that turns an application spec into the GitOps files ArgoCD needs. It
// writes files (and optionally commits and deploys); `endurance init` is
// where the questions are asked, since Phase 14 (14.1).
package onboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gc-ghub/endurance/internal/deploy"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/image"
	"github.com/gc-ghub/endurance/internal/notify"
	"github.com/gc-ghub/endurance/internal/policy"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
	"github.com/gc-ghub/endurance/internal/version"
	"gopkg.in/yaml.v3"
)

// Options configures a run of the onboarding flow.
type Options struct {
	Root       string // platform repo root
	GitopsRepo string // repo URL ArgoCD watches
	Commit     bool   // stage + commit generated files (default off)
	From       string // non-interactive: load the app spec from this YAML file
	PathPrefix string // repo-relative prefix for ArgoCD paths ("" = auto-detect)
	PolicyDir  string // override the Kyverno policy directory
	SkipPolicy bool   // break glass: report policy violations but do not block
	NoNotify   bool   // do not send the CLI notification

	// SkipImageCheck is break glass for the image preflight, the same shape as
	// SkipPolicy: report everything, block nothing.
	SkipImageCheck bool

	// Inspect is the registry lookup the image preflight uses. Nil means the
	// real one when docker is on PATH — see image.DefaultInspector. It is an
	// option rather than a package-level default so that **no test ever makes
	// the call**: `docker manifest inspect` on a test runner would pass here,
	// hang in CI and fail on a machine with no network, which is the class of
	// fault that shipped a broken v0.11.0.
	Inspect image.Inspector

	// Deploy asks onboard to apply the generated Application, using the same
	// verb `endurance deploy <app>` calls on its own (14.2). Off by default,
	// the same posture as Commit: `onboard` is also the automation entry point
	// — CI, `--from`, the byte-identical regeneration proof — and those callers
	// must not have a kubeconfig they did not ask about touched. A person who
	// wants the deploy runs `endurance onboard --deploy` or, more simply,
	// `endurance init`. Either way, what onboard offers instead when this is
	// off is `endurance deploy <app>` — never a raw kubectl line.
	Deploy  bool
	NoWait  bool // deploy: do not wait for ArgoCD; end on whatever is running now
	Timeout time.Duration
	Kubectl func(args ...string) (string, error)

	// DeployFunc is the verb itself. Nil means deploy.Run. Injectable so a test
	// can assert on what onboard passes it without touching a real cluster —
	// the same rule that caught v0.10.1's missing GitopsRepo.
	DeployFunc func(deploy.Options) (bool, error)

	// NoMesh sets the default the mesh question opens on. It only reaches the
	// interactive path: a spec file passed with --from has already answered the
	// question, and a flag must not silently overrule a file.
	NoMesh bool

	// NoBanner suppresses the product banner, for a caller that has already
	// drawn one. `endurance init` onboards as one phase of a longer run.
	NoBanner bool
}

// Run drives onboarding and writes the GitOps files from a spec file.
//
// # It is the non-interactive door, and only that, since Phase 14 (14.1)
//
// This command used to also ask its own questions — application name,
// namespace, source repo, owner, mesh, then a loop of per-service prompts —
// which meant a developer with a multi-service application had two different
// interactive experiences to choose between, split by a rule ("one service →
// `init`, more than one → `onboard`") that was nowhere in the help text and
// was not even the real distinction. The distinction was accidental: `init`
// bootstrapped and deployed, `onboard` did neither, and both happened to write
// the same files.
//
// `endurance init` is the only guided form now, and its form asks for N
// services, routes and per-service env — everything this command's old form
// asked for and the two things it never did. `init` writes what it asks as
// `specs/<app>.yaml` and calls this function with `--from` pointed at that
// file, which is the same thing CI, `--from` and the byte-identical
// regeneration proof already did. Nobody hand-writes a spec file: it is
// always either `init`'s output or a previous onboard's input, edited.
func Run(opts Options) error {
	if !opts.NoBanner {
		render.Banner(version.Current)
	}
	render.Section("Onboard an application")

	if opts.From == "" {
		return fmt.Errorf("onboard needs --from <specs/app.yaml> — " +
			"`endurance init` asks the questions and writes one; " +
			"onboard is the door for that same file (CI, automation, re-registering an app already onboarded)")
	}
	return runFromFile(opts)
}

// runFromFile loads an app spec from YAML and generates without prompting.
func runFromFile(opts Options) error {
	data, err := os.ReadFile(opts.From)
	if err != nil {
		return err
	}
	var app spec.App
	if err := yaml.Unmarshal(data, &app); err != nil {
		return fmt.Errorf("parsing %s: %w", opts.From, err)
	}
	if app.Namespace == "" {
		app.Namespace = app.Name
	}
	render.Info("loaded spec from " + opts.From)
	// A renamed field that is silently accepted is a rename nobody hears about,
	// and the whole argument for renaming this one is that its old name misled
	// somebody for four edits. Said once, here, where the file was read.
	for _, d := range app.Deprecated() {
		render.Warn(d)
	}
	return finish(opts, app)
}

// finish validates the app, writes the GitOps files, optionally commits, and
// prints the end-of-run dashboard. Shared by the interactive and file paths.
func finish(opts Options, app spec.App) error {
	// The verdict is the returned error, rendered once, by main. Printing it
	// here as well said the same sentence twice in two voices — the fault Phase
	// 9 recorded and fixed for `doctor` and `status`, still here because nothing
	// tripped it routinely until route validation started refusing paths.
	if err := app.Validate(); err != nil {
		return err
	}

	// The gate runs against the defaulted spec — the same one Generate is about
	// to render — so what is judged is exactly what would be written. An app that
	// cannot pass the platform's policies is never onboarded in a half state:
	// either all three files are written or none are.
	gated := app
	gated.ApplyDefaults()
	for _, w := range gated.MeshWarnings() {
		render.Warn(w)
	}
	for _, w := range gated.NotifyWarnings() {
		render.Warn(w)
	}
	// Onboarding regenerates, which is the whole contract — but a live traffic
	// split is the one thing worth being told about before it is overwritten,
	// because unlike a tag nobody wrote it down in the spec on purpose.
	for _, w := range gitops.WeightDrift(opts.Root, gated) {
		render.Warn(w)
	}

	// The image gate runs *above* the policy gate, and the order is the lesson.
	//
	// In the first outside run the Kyverno gate printed `✓ all policies
	// satisfied` about a container that could not start, and it was right: the
	// manifest declares non-root and dropped capabilities, which is what the
	// policy asks. Whether the image can live inside that posture is a fact
	// about the image, which no static check of the YAML can reach. Printing the
	// tick first and the refusal after would reproduce exactly the pair of facts
	// that made that run hard to read.
	if err := image.Gate(gated, image.Options{
		Inspect: opts.Inspect,
		Skip:    opts.SkipImageCheck,
	}); err != nil {
		return err
	}

	if err := policy.Gate(policy.Options{
		Root: opts.Root, Dir: opts.PolicyDir, Skip: opts.SkipPolicy,
	}, gated); err != nil {
		return err
	}

	render.Section("Generating GitOps files")
	prefix := opts.PathPrefix
	if prefix == "" {
		prefix = gitops.RepoPrefix(opts.Root)
	}
	written, err := gitops.Generate(opts.Root, app, opts.GitopsRepo, prefix)
	if err != nil {
		return err
	}
	for _, w := range written {
		render.Success("wrote " + w)
	}
	if prefix != "" {
		render.Info("ArgoCD source paths prefixed with " + prefix + " (platform tree is nested in the repo)")
	}

	if opts.Commit {
		// The spec that produced this run rides in the same commit as the
		// files generated from it (B2, Phase 13's Part B). `init` used to
		// print `✓ wrote specs/<app>.yaml` and then commit only the files
		// under apps/, leaving the spec untracked — harmless while ArgoCD
		// reads apps/ and never specs/, and it stops being harmless the
		// moment 14.1 makes the spec the only way one is ever written. An
		// output the tool does not commit is an output the next person does
		// not have.
		toCommit := written
		if opts.From != "" {
			if _, err := os.Stat(opts.From); err == nil {
				toCommit = append(append([]string{}, written...), opts.From)
			}
		}
		if err := gitops.Commit(opts.Root, toCommit, "endurance: onboard "+app.Name); err != nil {
			render.Warn("commit skipped: " + err.Error())
		} else {
			render.Success("staged and committed, not pushed — " + gitops.HeadSubject(opts.Root))
		}
	} else {
		render.Info("not committed — review the files, then commit when ready")
	}

	// Deploying is the verb 14.2 added, and this is the second of the two doors
	// that use it — init calls it unconditionally, onboard only when asked,
	// because onboard is also the automation entry point and must not reach
	// for a kubeconfig a CI run never mentioned.
	deployed := false
	if opts.Deploy {
		run := opts.DeployFunc
		if run == nil {
			run = deploy.Run
		}
		synced, err := run(deploy.Options{
			Root: opts.Root, App: app.Name,
			NoWait: opts.NoWait, Timeout: opts.Timeout, Kubectl: opts.Kubectl,
		})
		if err != nil {
			return err
		}
		deployed = synced
	}

	if !opts.NoNotify {
		e := notify.New(spec.StageOnboarded, gated)
		e.Detail = fmt.Sprintf("%d service(s) registered", len(app.Services))
		notify.Send(gated, e)
	}

	svcNames := ""
	for i, s := range app.Services {
		if i > 0 {
			svcNames += ", "
		}
		svcNames += s.Name
	}
	rows := [][2]string{
		{"App", render.Value(app.Name)},
		{"Namespace", render.Value(app.Namespace)},
		{"Services", render.Value(strconv.Itoa(len(app.Services))) + "  (" + svcNames + ")"},
		{"Owner", app.Owner},
	}
	next := []string{"git push the platform repo so ArgoCD can see it"}
	switch {
	case deployed:
		// Deploy ran and ArgoCD already reports Synced/Healthy — the run has
		// earned the claim, so there is nothing left to tell them to type.
	case opts.Deploy:
		// Deploy ran and did not converge yet (still syncing, or waiting for
		// the push named above); `status` is the thing to watch, not a second
		// deploy.
	default:
		// Deploy did not run at all. Until 14.2 this line was a raw kubectl
		// command — a tool that knows the exact command and hands it to a
		// person has not finished — and it only appeared when the mesh was on,
		// which was its own fault: the Application has to be applied whether
		// or not there is a sidecar, or nothing ArgoCD watches ever hears this
		// application exists.
		next = append(next, "endurance deploy "+app.Name+"   to register it with ArgoCD")
	}
	next = append(next, "endurance status "+app.Name+"   to watch it come up")
	if canaries := app.CanaryServices(); len(canaries) > 0 {
		rows = append(rows, [2]string{"Canary", render.Value(strings.Join(canaries, ", "))})
		next = append(next, "endurance canary status "+app.Name+"   to see the traffic split")
	}
	if app.Notify.Enabled {
		rows = append(rows, [2]string{"Notify", render.Value(strings.Join(app.Notify.Recipients(), "  ")) +
			"  (" + strings.Join(app.Notify.StageNames(), ", ") + ")"})
		next = append(next, "endurance notify status "+app.Name+"   to see who hears about it and when")
	}
	if !app.Mesh.On() {
		// Said out loud rather than left off the screen. Being outside the mesh
		// is now a choice somebody made at a prompt, and the closing box is
		// where they find out it took.
		rows = append(rows, [2]string{"Mesh", "none — pods run without a sidecar, and Kiali will not see this application"})
	} else {
		rows = append(rows, [2]string{"Mesh", "istio — namespace gets istio-injection=enabled"})
	}
	render.Dashboard("Application onboarded", rows, next)
	return nil
}
