// Command endurance is the developer-facing CLI of the Endurance IDP.
//
// A developer runs `endurance onboard` to register and deploy an application,
// `endurance release` (later) to promote a new image, and `endurance list` /
// `status` to see what's running. The CLI only ever writes GitOps files and
// commits — ArgoCD is the only thing that deploys.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gc-ghub/endurance/internal/canary"
	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/notify"
	"github.com/gc-ghub/endurance/internal/onboard"
	"github.com/gc-ghub/endurance/internal/policy"
	"github.com/gc-ghub/endurance/internal/release"
	"github.com/gc-ghub/endurance/internal/render"
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
	case "onboard":
		err = cmdOnboard(args)
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
		render.Print("endurance " + version.Current)
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

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	_ = fs.Parse(args)

	apps, err := gitops.List(*root)
	if err != nil {
		return err
	}
	render.Section("Registered applications")
	if len(apps) == 0 {
		render.Info("none yet — run `endurance onboard`")
		return nil
	}
	for _, a := range apps {
		render.Step(render.Value(a.Name) + "  " + fmt.Sprintf("ns=%s services=%d owner=%s", a.Namespace, len(a.Services), a.Owner))
	}
	return nil
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	name, err := parsePositional(fs, args, "endurance status <app>")
	if err != nil {
		return err
	}

	app, err := gitops.Load(*root, name)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", name, err)
	}
	render.Banner(version.Current)
	render.Section("Status · " + app.Name)
	render.Info(fmt.Sprintf("namespace=%s  services=%d", app.Namespace, len(app.Services)))

	if _, err := exec.LookPath("kubectl"); err != nil {
		render.Warn("kubectl not found — showing registry only")
		for _, s := range app.Services {
			render.Step(render.Value(s.Name) + "  " + s.Image + ":" + s.Tag)
		}
		return nil
	}
	out, kerr := exec.Command("kubectl", "get", "pods", "-n", app.Namespace,
		"-l", "app.kubernetes.io/part-of="+app.Name, "--no-headers").CombinedOutput()
	if kerr != nil {
		render.Warn("could not query cluster: " + string(out))
		return nil
	}
	if len(out) == 0 {
		render.Warn("no pods yet — ArgoCD may still be syncing")
		return nil
	}
	// kubectl's table is not ours to restyle, but it still goes through the
	// renderer so it cannot land in the middle of a live step's line.
	render.Print(string(out))
	return nil
}

func usage() {
	render.Banner(version.Current)
	render.Print(`Usage: endurance <command> [flags]

Commands:
  onboard             Register and generate GitOps files for an application
  release <app>       Promote one service (or one canary version) to a new tag
  canary status <app> Show how each service's traffic is split
  canary set <app>    Change a canary service's traffic weights
  canary promote <app>Send all of a canary service's traffic to one version
  notify status <app> Show who hears about an application, and when
  notify test <app>   Send a test notification through this shell's webhook
  policy list         Show the Kyverno policies the platform enforces
  policy check <app>  Evaluate a registered application against them
  list                List registered applications
  status <app>        Show an application's services and pods
  version             Print version
  help                Show this help

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
  endurance release superheros --service catalog --version v2 --tag v2-abc1234
  endurance canary set superheros --service catalog --weights v1=80,v2=10,v3=10
  endurance canary promote superheros --service catalog --version v3`)
}
