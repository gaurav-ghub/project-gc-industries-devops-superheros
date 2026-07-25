// Command endurance is the CLI of the Endurance IDP — and, from Phase 9, its
// single front door.
//
// An operator runs `endurance doctor` / `bootstrap` / `status` / `destroy` to
// stand the platform up and take it down; a developer runs `endurance onboard`
// to register an application, `release` to promote an image, and `list` /
// `status <app>` to see what is running. The two halves are different jobs and
// one binary: the operator verbs drive the bash modules under platform/ as
// subprocesses, and the developer verbs only ever write GitOps files and
// commit — ArgoCD is still the only thing that deploys.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gc-ghub/endurance/internal/canary"
	"github.com/gc-ghub/endurance/internal/catalog"
	"github.com/gc-ghub/endurance/internal/features"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/initcmd"
	"github.com/gc-ghub/endurance/internal/notify"
	"github.com/gc-ghub/endurance/internal/observe"
	"github.com/gc-ghub/endurance/internal/onboard"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/policy"
	"github.com/gc-ghub/endurance/internal/release"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/success"
	"github.com/gc-ghub/endurance/internal/version"
)

// defaultGitopsRepo is the platform repo ArgoCD watches. Overridable per command.
const defaultGitopsRepo = "https://github.com/gc-ghub/project-gc-industries-devops-superheros.git"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	// The guided first run, which is a sequence of the two halves below.
	case "init":
		err = cmdInit(args)

	// The platform half: these drive the bash modules under platform/.
	case "bootstrap":
		err = cmdBootstrap(args)
	case "doctor":
		err = cmdDoctor(args)
	case "destroy":
		err = cmdDestroy(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "urls":
		err = cmdUrls(args)
	case "enable":
		err = cmdFeature(args, true)
	case "disable":
		err = cmdFeature(args, false)
	case "config":
		err = cmdConfig(args)

	// The application half: these only write git.
	case "onboard":
		err = cmdOnboard(args)
	case "catalog":
		err = cmdCatalog(args)
	case "logs":
		err = cmdLogs(args)
	case "metrics":
		err = cmdMetrics(args)
	case "release":
		err = cmdRelease(args)
	case "canary":
		err = cmdCanary(args)
	case "notify":
		err = cmdNotify(args)
	case "policy":
		err = cmdPolicy(args)
	case "list", "ls":
		err = cmdList(args)
	case "status":
		err = cmdStatus(args)
	case "version", "--version", "-v":
		err = cmdVersion(args)
	case "help", "--help", "-h":
		usage()
	default:
		render.Error("unknown command: " + cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		render.Error(err.Error())
		os.Exit(1)
	}
}

// cmdInit is the guided first run: welcome → doctor → questions → the
// confirmation screen → bootstrap → onboard → deploy → success screen.
//
// Every flag here is a question it does not have to ask. Supplying --name and
// --yes is the non-interactive form, which is what the runbook and the tests
// use; --dry-run prints the plan and stops, which is the cheapest way to see
// what a run would do.
func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	name := fs.String("name", "", "application name (default: asked, or "+initcmd.DefaultName+")")
	image := fs.String("image", "", "container image, with its registry (default: "+initcmd.DefaultImage+")")
	tag := fs.String("tag", "", "image tag (default: "+initcmd.DefaultTag+"; `latest` is refused by the policy gate)")
	owner := fs.String("owner", "", "owning team (default: git config user.name)")
	repo := fs.String("app-repo", "", "the application's own source repo, recorded in the registry")
	path := fs.String("path", "", "URL path on the platform's host (default: / if free, else /<app>)")
	port := fs.Int("port", 0, "container port (default: "+fmt.Sprint(initcmd.DefaultPort)+")")
	noRoute := fs.Bool("no-route", false, "do not give the application a URL")
	yes := fs.Bool("yes", false, "accept the plan without confirming")
	dryRun := fs.Bool("dry-run", false, "print the plan and stop")
	skipBootstrap := fs.Bool("skip-bootstrap", false, "the platform is already up; do not check")
	noWait := fs.Bool("no-wait", false, "do not wait for ArgoCD to sync")
	timeout := fs.Duration("timeout", initcmd.DefaultTimeout, "how long to wait for ArgoCD")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return initcmd.Run(initcmd.Options{
		Root: *root, Name: *name, Image: *image, Tag: *tag, Owner: *owner,
		Repo: *repo, Path: *path, Port: *port, NoRoute: *noRoute,
		Yes: *yes, DryRun: *dryRun, SkipBootstrap: *skipBootstrap,
		NoWait: *noWait, Timeout: *timeout,
	})
}

// The platform commands. --root is optional everywhere: they find the platform
// repo by walking up from the working directory, so they behave the same run
// from the repo root, from cli/, or from wherever the binary was copied to.

// cmdBootstrap stands the whole platform up: six bash modules run as
// subprocesses, framed as one progress chain by the renderer.
func cmdBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	skipPreflight := fs.Bool("skip-preflight", false, "break glass: run the modules without checking the tools first")
	dryRun := fs.Bool("dry-run", false, "print the chain and the commands, run nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Bootstrap(platform.BootstrapOptions{
		Root: *root, SkipPreflight: *skipPreflight, DryRun: *dryRun,
	})
}

// cmdDoctor is the preflight: can a bootstrap succeed on this machine?
func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Doctor(platform.DoctorOptions{Root: *root})
}

// cmdDestroy deletes the kind cluster the platform runs on.
func cmdDestroy(args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Destroy(platform.DestroyOptions{Root: *root, Yes: *yes})
}

// cmdUninstall removes the CLI binary. Distinct from destroy, which removes the
// cluster — the two are easy to confuse and expensive both ways round, so each
// command's output names the other.
func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Uninstall(platform.UninstallOptions{Root: *root, Yes: *yes})
}

// cmdFeature is `endurance enable ai|slack` and `endurance disable ai|slack`.
//
// Both capture credentials, and neither ever prints one back — not at the
// prompt, not in a confirmation and not in an error. See internal/features for
// why that is stricter than the rule `endurance urls` follows for ArgoCD's and
// Grafana's own logins.
func cmdFeature(args []string, enable bool) error {
	verb := "disable"
	if enable {
		verb = "enable"
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance %s %s", verb, strings.Join(features.Names(), "|"))
	}
	name, rest := args[0], args[1:]

	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	yes := fs.Bool("yes", false, "disable: do not ask for confirmation")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	opts := features.Options{Root: *root, Yes: *yes}
	if enable {
		return features.Enable(name, opts)
	}
	return features.Disable(name, opts)
}

// cmdConfig is `endurance config list`: which optional features are on, and
// whether a credential is present. Presence, never values.
func cmdConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance config list")
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("config", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	switch sub {
	case "list", "ls":
		return features.ConfigList(features.ConfigOptions{Root: *root})
	default:
		return fmt.Errorf("unknown config subcommand %q — use list", sub)
	}
}

// cmdUrls prints where the platform is — and with --check, whether it is
// actually there.
//
// The addresses are real ones served by the Istio ingress gateway through the
// ports kind publishes to the host, so there is nothing to keep running in a
// terminal. --check exists because printing a list of URLs proves only that the
// CLI can format a string, and this platform has a rule about claiming outcomes
// it has not observed.
func cmdUrls(args []string) error {
	fs := flag.NewFlagSet("urls", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	check := fs.Bool("check", false, "ask each address whether it answers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Urls(platform.UrlsOptions{Root: *root, Check: *check})
}

// cmdVersion prints the CLI version and, unless --short, the version of every
// component the platform installs.
func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	root := fs.String("root", "", "platform repo root (default: auto-detect)")
	short := fs.Bool("short", false, "print only `endurance <version>`")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return platform.Version(*root, *short)
}

func cmdOnboard(args []string) error {
	fs := flag.NewFlagSet("onboard", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	repo := fs.String("gitops-repo", defaultGitopsRepo, "repo URL ArgoCD watches")
	commit := fs.Bool("commit", false, "stage and commit the generated files (never pushes)")
	from := fs.String("from", "", "non-interactive: load the app spec from this YAML file")
	prefix := fs.String("path-prefix", "", "repo-relative prefix for ArgoCD source paths (default: auto-detect)")
	policyDir := fs.String("policy-dir", "", "Kyverno policy directory (default <root>/"+policy.DefaultDir+")")
	skipPolicy := fs.Bool("skip-policy", false, "break glass: report policy violations but do not block")
	noNotify := fs.Bool("no-notify", false, "do not send the CLI notification for this run")
	_ = fs.Parse(args)
	return onboard.Run(onboard.Options{
		Root: *root, GitopsRepo: *repo, Commit: *commit, From: *from, PathPrefix: *prefix,
		PolicyDir: *policyDir, SkipPolicy: *skipPolicy, NoNotify: *noNotify,
	})
}

// parsePositional parses a flag set that takes one leading positional argument
// and returns it.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `release superheros --service catalog` would otherwise leave --service unset
// and silently do the wrong thing. Parsing once to reach the positional, then
// again from just past it, lets flags appear on either side of the app name —
// which is how anyone would expect a CLI to behave.
func parsePositional(fs *flag.FlagSet, args []string, usage string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() < 1 {
		return "", fmt.Errorf("usage: %s", usage)
	}
	positional := fs.Arg(0)
	if err := fs.Parse(fs.Args()[1:]); err != nil {
		return "", err
	}
	return positional, nil
}

// cmdRelease promotes one service of one application to a new image tag.
// Usage: endurance release <app> --service <svc> --tag <tag>
func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	service := fs.String("service", "", "the service to promote")
	ver := fs.String("version", "", "the canary version to promote (required for a service with versions)")
	tag := fs.String("tag", "", "the image tag to promote to")
	commit := fs.Bool("commit", false, "stage and commit the changed files (never pushes)")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	policyDir := fs.String("policy-dir", "", "Kyverno policy directory (default <root>/"+policy.DefaultDir+")")
	skipPolicy := fs.Bool("skip-policy", false, "break glass: report policy violations but do not block")
	noNotify := fs.Bool("no-notify", false, "do not send the CLI notification for this run")
	app, err := parsePositional(fs, args, "endurance release <app> --service <svc> --tag <tag>")
	if err != nil {
		return err
	}
	return release.Run(release.Options{
		Root: *root, App: app, Service: *service, Version: *ver, Tag: *tag,
		Commit: *commit, DryRun: *dryRun,
		PolicyDir: *policyDir, SkipPolicy: *skipPolicy, NoNotify: *noNotify,
	})
}

// cmdNotify is the developer-facing view of Phase 5: who hears about this
// application, when, and which half of the platform tells them.
func cmdNotify(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance notify status <app> | endurance notify test <app>")
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("notify", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")

	switch sub {
	case "status":
		app, err := parsePositional(fs, rest, "endurance notify status <app>")
		if err != nil {
			return err
		}
		return notify.Status(*root, app)
	case "test":
		app, err := parsePositional(fs, rest, "endurance notify test <app>")
		if err != nil {
			return err
		}
		return notify.TestSend(*root, app)
	default:
		return fmt.Errorf("unknown notify subcommand %q — use status or test", sub)
	}
}

// cmdCanary is the traffic half of the platform: `canary status` shows how an
// application's traffic is currently split, `canary set` changes the split, and
// `canary promote` is the shorthand for sending all of it to one version.
//
// It is a separate command from `release` on purpose. Releasing changes what
// runs and restarts pods; shifting traffic changes who is served and restarts
// nothing. Collapsing the two into one command would hide exactly the
// distinction that makes a canary safe.
func cmdCanary(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance canary status <app> | canary set <app> --service <svc> --weights v1=90,v2=10 | canary promote <app> --service <svc> --version <v>")
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("canary", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	service := fs.String("service", "", "the canary service")
	weights := fs.String("weights", "", "traffic split, e.g. v1=90,v2=10 (must sum to 100)")
	to := fs.String("version", "", "the version to send all traffic to (promote)")
	commit := fs.Bool("commit", false, "stage and commit the changed files (never pushes)")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	policyDir := fs.String("policy-dir", "", "Kyverno policy directory (default <root>/"+policy.DefaultDir+")")
	skipPolicy := fs.Bool("skip-policy", false, "break glass: report policy violations but do not block")
	noSpec := fs.Bool("no-spec", false, "do not write the new weights back into specs/<app>.yaml")
	noNotify := fs.Bool("no-notify", false, "do not send the CLI notification for this run")

	switch sub {
	case "status":
		app, err := parsePositional(fs, rest, "endurance canary status <app>")
		if err != nil {
			return err
		}
		return canary.Status(*root, app)

	case "set":
		app, err := parsePositional(fs, rest, "endurance canary set <app> --service <svc> --weights v1=90,v2=10")
		if err != nil {
			return err
		}
		if *weights == "" {
			return fmt.Errorf("--weights is required, e.g. --weights v1=90,v2=10")
		}
		w, err := canary.ParseWeights(*weights)
		if err != nil {
			return err
		}
		return canary.Shift(canary.Options{
			Root: *root, App: app, Service: *service, Weights: w,
			Commit: *commit, DryRun: *dryRun,
			PolicyDir: *policyDir, SkipPolicy: *skipPolicy,
			NoSpec: *noSpec, NoNotify: *noNotify,
		})

	case "promote":
		app, err := parsePositional(fs, rest, "endurance canary promote <app> --service <svc> --version <v>")
		if err != nil {
			return err
		}
		if *to == "" {
			return fmt.Errorf("--version is required (which version should receive all the traffic?)")
		}
		reg, err := gitops.Load(*root, app)
		if err != nil {
			return fmt.Errorf("no registered app %q (%v)", app, err)
		}
		i := reg.FindService(*service)
		if i < 0 {
			return fmt.Errorf("app %q has no service %q — services are: %s",
				app, *service, strings.Join(reg.ServiceNames(), ", "))
		}
		w, err := canary.PromoteWeights(reg.Services[i], *to)
		if err != nil {
			return err
		}
		return canary.Shift(canary.Options{
			Root: *root, App: app, Service: *service, Weights: w,
			Commit: *commit, DryRun: *dryRun,
			PolicyDir: *policyDir, SkipPolicy: *skipPolicy,
			NoSpec: *noSpec, NoNotify: *noNotify,
		})

	default:
		return fmt.Errorf("unknown canary subcommand %q — use status, set or promote", sub)
	}
}

// cmdPolicy is the standalone view of the gate: `policy list` shows what the
// platform enforces, `policy check <app>` runs the same evaluation onboard and
// release run, against an already-registered application.
//
// It exists because a gate that only ever speaks when it blocks you is hard to
// trust. Being able to ask "what would you say about superheros right now?"
// without staging a release is what makes the rules reviewable.
func cmdPolicy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance policy list | endurance policy check <app>")
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("policy", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	dir := fs.String("policy-dir", "", "Kyverno policy directory (default <root>/"+policy.DefaultDir+")")

	switch sub {
	case "list":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return policyList(*root, *dir)
	case "check":
		app, err := parsePositional(fs, rest, "endurance policy check <app>")
		if err != nil {
			return err
		}
		return policyCheck(*root, *dir, app)
	default:
		return fmt.Errorf("unknown policy subcommand %q — use list or check", sub)
	}
}

func policyDir(root, dir string) string {
	if dir != "" {
		return dir
	}
	return filepath.Join(root, policy.DefaultDir)
}

func policyList(root, dir string) error {
	d := policyDir(root, dir)
	policies, err := policy.Load(d)
	if err != nil {
		return err
	}
	render.Banner(version.Current)
	render.Section("Policies · " + d)
	if len(policies) == 0 {
		render.Warn("no ClusterPolicies found")
		return nil
	}
	for _, p := range policies {
		render.Step(render.Value(p.Name) + "  " + string(p.Action) +
			fmt.Sprintf("  (%d rule(s), %s)", len(p.Rules), filepath.Base(p.Source)))
		for _, r := range p.Rules {
			render.Detail(fmt.Sprintf("%s [%s] kinds=%s ns=%s",
				r.Name, r.Kind, strings.Join(r.Kinds, ","), nsList(r.Namespaces)))
		}
	}
	return nil
}

func nsList(ns []string) string {
	if len(ns) == 0 {
		return "*"
	}
	return strings.Join(ns, ",")
}

func policyCheck(root, dir, name string) error {
	d := policyDir(root, dir)
	app, err := gitops.Load(root, name)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", name, err)
	}
	policies, err := policy.Load(d)
	if err != nil {
		return err
	}
	render.Banner(version.Current)
	render.Section("Policy check · " + app.Name)

	app.ApplyDefaults()
	rep := policy.Check(policies, app)
	policy.Print(rep, d)

	if n := len(rep.Blocking()); n > 0 {
		return fmt.Errorf("%d enforced violation(s)", n)
	}
	return nil
}

// cmdCatalog is the registry as a developer asks about it: `catalog list` is
// every registered application, `catalog get <app>` is one of them in detail.
//
// It is the rename of `list` and `status <app>`, and both of those still work —
// see internal/catalog for why they were kept rather than retired. `catalog get`
// and `status <app>` call the same function; there is one success screen on this
// platform.
func cmdCatalog(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: endurance catalog list | endurance catalog get <app>")
	}
	sub, rest := args[0], args[1:]

	fs := flag.NewFlagSet("catalog", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")

	switch sub {
	case "list", "ls":
		if err := fs.Parse(rest); err != nil {
			return err
		}
		return catalog.List(*root)
	case "get":
		app, err := parsePositional(fs, rest, "endurance catalog get <app>")
		if err != nil {
			return err
		}
		render.Banner(version.Current)
		return catalog.Get(*root, app)
	default:
		return fmt.Errorf("unknown catalog subcommand %q — use list or get", sub)
	}
}

// cmdList is `catalog list` under its original name. Kept because every
// transcript in test-evidence/ and every step in the manual runbook uses it.
func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	_ = fs.Parse(args)
	return catalog.List(*root)
}

// cmdLogs and cmdMetrics are thin wrappers around kubectl that know what an
// application is. They add the selector and print what kubectl printed.
func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	service := fs.String("service", "", "one service (default: every service in the application)")
	follow := fs.Bool("f", false, "follow the log stream")
	tail := fs.Int("tail", 200, "lines of recent log to show (0 = all)")
	since := fs.String("since", "", "only logs newer than this, e.g. 10m or 1h")
	app, err := parsePositional(fs, args, "endurance logs <app> [--service <svc>] [-f]")
	if err != nil {
		return err
	}
	return observe.Logs(observe.LogOptions{
		Root: *root, App: app, Service: *service,
		Follow: *follow, Tail: *tail, Since: *since,
	})
}

func cmdMetrics(args []string) error {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	service := fs.String("service", "", "one service (default: every service in the application)")
	app, err := parsePositional(fs, args, "endurance metrics <app> [--service <svc>]")
	if err != nil {
		return err
	}
	return observe.Metrics(observe.MetricOptions{Root: *root, App: app, Service: *service})
}

// cmdStatus answers "is it up?" about whichever thing was named.
//
// With nothing, it is the platform itself: the cluster and every component the
// bootstrap installed. With an application, it is the post-deploy success
// screen — what was deployed, where it is, whether it is actually up yet, and
// what to type next. One verb, because a developer asking whether things are
// working should not have to know which half of the system they are asking
// about.
//
// Until Phase 10 the application half printed kubectl's own pod table, because
// there was nothing else honest to show: the success screen wanted URLs and
// there were none. There are now.
func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		// Platform status searches upward from --root, so the same default
		// serves both: run from anywhere in the tree and it finds the repo.
		return platform.Status(platform.StatusOptions{Root: *root})
	}
	name, err := parsePositional(fs, args, "endurance status [<app>]")
	if err != nil {
		return err
	}
	render.Banner(version.Current)
	return success.Screen(success.Options{Root: *root, App: name})
}

func usage() {
	render.Banner(version.Current)
	render.Print(`Usage: endurance <command> [flags]

Start here:
  init                Guided first run — stands the platform up, onboards an
                      application and deploys it. Asks before it creates
                      anything; --dry-run prints the plan and stops

Platform commands (operator):
  doctor              Check this machine can stand the platform up
  bootstrap           Install the platform: cluster, Istio, monitoring, AI,
                      ArgoCD, Kyverno, access — one framed run
  status              Show whether the cluster and every component is healthy
  urls                Show where the platform is; --check asks each address
  enable ai|slack     Capture the credentials for an optional capability
  disable ai|slack    Remove them
  config list         Which capabilities are on, and whether a key is present
                      (presence, never values)
  destroy             Delete the kind CLUSTER and everything installed in it
  uninstall           Remove the endurance BINARY. The cluster survives —
                      destroy and uninstall are different removals

Application commands (developer):
  onboard             Register and generate GitOps files for an application
  catalog list        List registered applications
  catalog get <app>   One application: services, pods, URLs, what to type next
  logs <app>          Its logs, via kubectl; --service narrows to one
  metrics <app>       Its CPU and memory, via kubectl top
  release <app>       Promote one service (or one canary version) to a new tag
  canary status <app> Show how each service's traffic is split
  canary set <app>    Change a canary service's traffic weights
  canary promote <app>Send all of a canary service's traffic to one version
  notify status <app> Show who hears about an application, and when
  notify test <app>   Send a test notification through this shell's webhook
  policy list         Show the Kyverno policies the platform enforces
  policy check <app>  Evaluate a registered application against them
  version             Print the CLI version and every component's version
  help                Show this help

Retained aliases, from before the catalog verb existed. They are not deprecated
and they are not going away — they are the older way to say the same thing:
  list                = catalog list
  status <app>        = catalog get <app>

Init flags:
  --name <app>          application name (asked for if not given)
  --image <ref>         container image, with its registry
  --tag <tag>           image tag (` + "`latest`" + ` is refused by the policy gate)
  --port <n>            container port
  --path </p>           URL path on the platform's host
  --app-repo <url>      the application's own source repo, recorded only
  --owner <team>        default: git config user.name
  --no-route            do not give the application a URL
  --yes                 accept the plan without confirming
  --dry-run             print the plan and stop
  --skip-bootstrap      the platform is already up; do not check
  --no-wait             do not wait for ArgoCD to sync
  --timeout <dur>       how long to wait for ArgoCD (default 6m)

Platform flags (doctor, bootstrap, status, urls, destroy, uninstall, version):
  --root <dir>          platform repo root (default: found by walking up)
  --dry-run             bootstrap: print the chain and the commands, run nothing
  --skip-preflight      bootstrap: run the modules without checking the tools
  --check               urls: ask each address whether it answers
  --yes                 destroy / uninstall / disable: skip the confirmation
  --short               version: print only ` + "`endurance <version>`" + `

Logs and metrics flags:
  --service <name>      one service instead of the whole application
  -f                    logs: follow the stream
  --tail <n>            logs: lines of history (default 200, 0 = all)
  --since <dur>         logs: only lines newer than this

A credential Endurance captured is never printed back. ` + "`config list`" + ` reports
whether one is present and nothing else. The two exceptions are deliberate and
closed: ArgoCD's and Grafana's own admin logins, which the platform generates
for itself and ` + "`urls`" + ` hands over — set ` + platform.EnvNoCredentials + `=1
to keep them out of a transcript you are recording.

Everything the platform exposes is on one host, path-based, through the Istio
ingress gateway on the port kind-config.yaml publishes:
  ` + platform.DefaultBaseURL + `/argocd   /kiali   /grafana   /prometheus   /alertmanager
No port-forward, no daemon, no /etc/hosts. An application's own URL comes from
a route: block in its spec, never from the platform.

The platform commands run the bash modules under platform/ as subprocesses,
with ` + platform.EnvFramed + `=1 in their environment, and stream their output
through this CLI's renderer. The modules still work on their own — bootstrap
is a front door, not a rewrite.

Onboard flags:
  --root <dir>          platform repo root (default ".")
  --gitops-repo <url>   repo URL ArgoCD watches
  --from <file>         non-interactive: load the app spec from YAML
  --path-prefix <p>     repo-relative prefix for ArgoCD paths (default: auto)
  --commit              stage + commit generated files (never pushes)

Release flags:
  --service <name>      the service to promote (required)
  --version <name>      the canary version to promote (required if the service
                        declares versions; rejected if it does not)
  --tag <tag>           the image tag to promote to (required)
  --root <dir>          platform repo root (default ".")
  --dry-run             report what would change without writing
  --commit              stage + commit changed files (never pushes)

Canary flags:
  --service <name>      the canary service (required for set and promote)
  --weights v1=90,v2=10 the new split; must name every version and sum to 100
  --version <name>      promote: the version that receives all the traffic
  --root <dir>          platform repo root (default ".")
  --dry-run             report what would change without writing
  --commit              stage + commit changed files (never pushes)
  --no-spec             leave specs/<app>.yaml alone (a re-onboard will then
                        reset the split back to what the spec still says)

Policy flags (onboard, release, canary and policy):
  --policy-dir <dir>    Kyverno policy directory (default <root>/` + policy.DefaultDir + `)
  --skip-policy         break glass: report violations but do not block

Notification flags (onboard, release, canary):
  --no-notify           do not send this run's CLI notification

Onboard, release and canary refuse to write anything when an Enforce-mode
Kyverno policy would reject the manifests they generate.

A release replaces pods; a canary shift replaces none — it only rewrites the
Istio VirtualService weights, so traffic moves without a restart.

Notifications come from two places and never from one. The CLI reports intent —
onboarded, requested — the moment it writes the files; ArgoCD reports outcome —
deploying, healthy, failed — because it is the only thing that deploys. Set
` + notify.EnvSlackWebhook + ` (a Slack incoming-webhook URL) or
` + notify.EnvWebhook + ` (any JSON receiver) to enable the CLI half.

Examples:
  endurance doctor
  endurance bootstrap
  endurance release superheros --service catalog --version v2 --tag v2-abc1234
  endurance canary set superheros --service catalog --weights v1=80,v2=10,v3=10
  endurance canary promote superheros --service catalog --version v3`)
}
