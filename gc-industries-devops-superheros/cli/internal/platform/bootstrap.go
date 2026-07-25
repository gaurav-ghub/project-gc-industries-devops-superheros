package platform

import (
	"fmt"

	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/version"
)

// `endurance bootstrap` — the single front door to standing the platform up.
//
// Six bash modules, six steps, one look. The CLI contributes the frame — the
// banner, the preflight, the counter, the spinner, the elapsed times, the bar
// and the verdict — and contributes no installation logic at all. Everything
// that touches a cluster is still the module's, which is what keeps
// bootstrap-kind.sh a working fallback rather than a stale copy.
//
// A failure stops the chain. The modules are ordered by dependency (see Chain),
// so carrying on past a failed step would mean installing ArgoCD into a cluster
// that does not exist and reporting five confident errors about one problem.

// BootstrapOptions configures a bootstrap run.
type BootstrapOptions struct {
	Root          string // platform repo root ("" = discover)
	SkipPreflight bool   // break glass: run the chain without checking the tools
	DryRun        bool   // print the chain and the commands, run nothing

	// run, probe and probeURL are the edges of the operating system this
	// command touches — a subprocess, the tools on PATH, and the network.
	// Tests replace them; the CLI never sets any of them.
	run      runFunc
	probe    *probe
	probeURL probeFunc
}

// Bootstrap stands the platform up and returns an error if any module failed.
func Bootstrap(opts BootstrapOptions) error {
	render.Banner(version.Current)

	root, err := FindRoot(opts.Root)
	if err != nil {
		return err
	}
	run := opts.run
	if run == nil {
		run = runScript
	}

	if !opts.SkipPreflight {
		p := realProbe()
		if opts.probe != nil {
			p = *opts.probe
		}
		render.Section("Preflight")
		checks := Preflight(p, root)
		render.Checks(checks)
		render.Blank()
		if err := preflightVerdict(checks); err != nil {
			// Failing here is the point: a bootstrap that dies four steps in,
			// having already created a cluster, is a worse outcome than one
			// that never starts. --skip-preflight is there for the operator who
			// knows better.
			return err
		}
	}

	if opts.DryRun {
		return dryRun(root)
	}

	p := render.NewProgress("Bootstrapping the platform", steps()...)
	var failed error
	for _, m := range Chain {
		if err := runModule(run, p, root, m); err != nil {
			failed = err
			break
		}
	}
	ok := p.Finish()

	if failed != nil {
		render.Info("the module's own output is above, unmodified — the step that failed is the one to re-run")
		render.Info("re-run `endurance bootstrap` when it is fixed; every module is safe to run twice")
		return failed
	}
	if !ok {
		// Belt and braces: Progress is the authority on the verdict, and if it
		// says the run failed while every module returned nil, the transcript
		// is right and this function is wrong.
		return fmt.Errorf("bootstrap did not complete")
	}

	AccessBlock(root, opts.probeURL)

	render.Dashboard("Platform ready", [][2]string{
		{"Cluster", render.Value(ClusterName(root))},
		{"Context", render.Value(ContextName(root))},
		{"Modules", fmt.Sprintf("%d installed", len(Chain))},
		{"Address", render.Value(BaseURL(root))},
		{"Repo", shortPath(root)},
	}, []string{
		"endurance urls — the addresses above, re-checked",
		"endurance status — is every component healthy",
		"endurance onboard — register an application; ArgoCD deploys it",
		"endurance destroy — delete the cluster when you are done",
	})
	return nil
}

// Where the access details went:
//
// This file used to carry an accessBlock() that printed four `kubectl
// port-forward` commands labelled "temporary until Phase 10". They are gone.
// The block is now [AccessBlock] in urls.go, shared with `endurance urls`,
// printing real addresses — and probing them before it claims they work.
//
// What has not changed is the rule that put the block here in the first place.
// Each bash module used to print its own version of it the moment it finished:
// Grafana's URL and password three minutes before ArgoCD existed, ArgoCD's
// "🎉 Welcome Onboard! Your platform is now ready" while two modules were still
// pending. A second visual system, a readiness claim no single module is in a
// position to make, and live admin passwords in a scrollback that gets
// screenshotted. The modules are quiet under ENDURANCE_FRAMED, this runs once
// when the whole chain has finished, and **no credential is printed** — the
// command that fetches one is, which is the same information with a shelf life.

// steps is the plan the progress counter is drawn from.
func steps() []string {
	out := make([]string, 0, len(Chain))
	for _, m := range Chain {
		out = append(out, m.Step)
	}
	return out
}

// dryRun renders the whole chain without touching anything, so the shape of a
// bootstrap can be read (and screenshotted, and reviewed) on a machine with no
// Docker at all. Every step is a skip, because a skip is what actually
// happened — a dry run must not draw six ✓s for work nobody did.
func dryRun(root string) error {
	p := render.NewProgress("Bootstrapping the platform (dry run)", steps()...)
	for _, m := range Chain {
		step := p.Start(m.Step)
		step.Detail("bash " + m.Script)
		step.Skip("dry run")
	}
	p.Finish()
	render.Info("nothing was run · " + EnvFramed + "=1 is set on each module, and its output is streamed here")
	render.Info("drop --dry-run to stand the platform up in " + root)
	return nil
}
