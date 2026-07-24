// Package onboard implements `endurance onboard` — the interactive form that
// turns a developer's answers into an application spec and the GitOps files
// ArgoCD needs. It never deploys; it writes files (and optionally commits).
package onboard

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/gc-ghub/endurance/internal/gitops"
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
}

// Run drives onboarding and writes the GitOps files. If opts.From is set it
// skips the interactive form and loads the spec from a YAML file (used for
// migration and automation); otherwise it prompts via huh.
func Run(opts Options) error {
	render.Banner(version.Current)
	render.Section("Onboard an application")

	if opts.From != "" {
		return runFromFile(opts)
	}

	app := spec.App{}
	appForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Application name").
				Description("lowercase, DNS-safe (e.g. superheros)").
				Value(&app.Name).
				Validate(dns("application name")),
			huh.NewInput().Title("Namespace").
				Description("Kubernetes namespace to deploy into").
				Value(&app.Namespace).
				Validate(dns("namespace")),
			huh.NewInput().Title("Repository URL").
				Description("the developer's application source repo").
				Value(&app.Repository),
			huh.NewInput().Title("Owner / team").
				Value(&app.Owner),
		),
	)
	if err := appForm.Run(); err != nil {
		return err
	}
	if app.Namespace == "" {
		app.Namespace = app.Name
	}

	// Collect one or more services.
	for {
		svc, err := serviceForm(len(app.Services) + 1)
		if err != nil {
			return err
		}
		app.Services = append(app.Services, svc)

		more := false
		if err := huh.NewConfirm().
			Title("Add another service?").
			Description(fmt.Sprintf("%d service(s) so far", len(app.Services))).
			Value(&more).Run(); err != nil {
			return err
		}
		if !more {
			break
		}
	}

	return finish(opts, app)
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
	return finish(opts, app)
}

// finish validates the app, writes the GitOps files, optionally commits, and
// prints the end-of-run dashboard. Shared by the interactive and file paths.
func finish(opts Options, app spec.App) error {
	if err := app.Validate(); err != nil {
		render.Error(err.Error())
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
		render.Error(err.Error())
		return err
	}
	for _, w := range written {
		render.Success("wrote " + w)
	}
	if prefix != "" {
		render.Info("ArgoCD source paths prefixed with " + prefix + " (platform tree is nested in the repo)")
	}

	if opts.Commit {
		if err := gitops.Commit(opts.Root, written, "endurance: onboard "+app.Name); err != nil {
			render.Warn("commit skipped: " + err.Error())
		} else {
			render.Success("staged and committed, not pushed — " + gitops.HeadSubject(opts.Root))
		}
	} else {
		render.Info("not committed — review the files, then commit when ready")
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
	next := []string{
		"git push the platform repo so ArgoCD can see it",
		"endurance status " + app.Name + "   to watch it come up",
	}
	if canaries := app.CanaryServices(); len(canaries) > 0 {
		rows = append(rows, [2]string{"Canary", render.Value(strings.Join(canaries, ", "))})
		next = append(next, "endurance canary status "+app.Name+"   to see the traffic split")
	}
	if app.Notify.Enabled {
		rows = append(rows, [2]string{"Notify", render.Value(strings.Join(app.Notify.Recipients(), "  ")) +
			"  (" + strings.Join(app.Notify.StageNames(), ", ") + ")"})
		next = append(next, "endurance notify status "+app.Name+"   to see who hears about it and when")
	}
	if app.Mesh.Enabled {
		rows = append(rows, [2]string{"Mesh", "istio — namespace gets istio-injection=enabled"})
		// Applying the Application by hand is already the documented step, but
		// it is easy to skip on a re-onboard, and the namespace label is the one
		// change ArgoCD cannot make for itself until it has been told to.
		next = append(next, "kubectl apply -f "+
			gitops.AppDir(opts.Root, app.Name)+"/application.yaml   (the namespace label lives here)")
	}
	render.Dashboard("Application onboarded", rows, next)
	return nil
}

func serviceForm(n int) (spec.Service, error) {
	var s spec.Service
	portStr, tagStr, replStr := "", "latest", "1"
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().Title(fmt.Sprintf("Service #%d", n)),
			huh.NewInput().Title("Service name").
				Value(&s.Name).Validate(dns("service name")),
			huh.NewInput().Title("Image").
				Description("repository without tag, e.g. dockergc00/superheros-frontend").
				Value(&s.Image).Validate(nonEmpty("image")),
			huh.NewInput().Title("Tag").Value(&tagStr),
			huh.NewInput().Title("Container port").
				Value(&portStr).Validate(isPort),
			huh.NewInput().Title("Replicas").Value(&replStr).Validate(isPositiveInt),
		),
	)
	if err := form.Run(); err != nil {
		return s, err
	}
	s.Tag = tagStr
	s.Port, _ = strconv.Atoi(portStr)
	s.Replicas, _ = strconv.Atoi(replStr)
	return s, nil
}

// --- validators ---

func dns(field string) func(string) error {
	return func(v string) error {
		s := spec.App{Name: "x", Namespace: "x", Services: []spec.Service{{Name: v, Image: "i", Port: 1, Replicas: 1}}}
		if field == "service name" {
			if err := s.Validate(); err != nil {
				return fmt.Errorf("%s must be lowercase DNS-safe", field)
			}
			return nil
		}
		if v == "" {
			return nil // namespace may default from name
		}
		t := spec.App{Name: v, Namespace: v, Services: []spec.Service{{Name: "s", Image: "i", Port: 1, Replicas: 1}}}
		if err := t.Validate(); err != nil {
			return fmt.Errorf("%s must be lowercase DNS-safe", field)
		}
		return nil
	}
}

func nonEmpty(field string) func(string) error {
	return func(v string) error {
		if v == "" {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
}

func isPort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("port must be 1-65535")
	}
	return nil
}

func isPositiveInt(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return fmt.Errorf("must be a positive integer")
	}
	return nil
}
