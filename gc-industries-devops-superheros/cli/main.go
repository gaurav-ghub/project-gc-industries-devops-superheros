// Command launchpad is the developer-facing CLI of the LaunchPad IDP.
//
// A developer runs `launchpad onboard` to register and deploy an application,
// `launchpad release` (later) to promote a new image, and `launchpad list` /
// `status` to see what's running. The CLI only ever writes GitOps files and
// commits — ArgoCD is the only thing that deploys.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/gc-ghub/launchpad/internal/gitops"
	"github.com/gc-ghub/launchpad/internal/onboard"
	"github.com/gc-ghub/launchpad/internal/release"
	"github.com/gc-ghub/launchpad/internal/render"
	"github.com/gc-ghub/launchpad/internal/version"
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
	case "list", "ls":
		err = cmdList(args)
	case "status":
		err = cmdStatus(args)
	case "version", "--version", "-v":
		fmt.Println("launchpad " + version.Current)
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
	_ = fs.Parse(args)
	return onboard.Run(onboard.Options{
		Root: *root, GitopsRepo: *repo, Commit: *commit, From: *from, PathPrefix: *prefix,
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
// Usage: launchpad release <app> --service <svc> --tag <tag>
func cmdRelease(args []string) error {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	root := fs.String("root", ".", "platform repo root")
	service := fs.String("service", "", "the service to promote")
	tag := fs.String("tag", "", "the image tag to promote to")
	commit := fs.Bool("commit", false, "stage and commit the changed files (never pushes)")
	dryRun := fs.Bool("dry-run", false, "report what would change without writing")
	app, err := parsePositional(fs, args, "launchpad release <app> --service <svc> --tag <tag>")
	if err != nil {
		return err
	}
	return release.Run(release.Options{
		Root: *root, App: app, Service: *service, Tag: *tag,
		Commit: *commit, DryRun: *dryRun,
	})
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
		render.Info("none yet — run `launchpad onboard`")
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
	name, err := parsePositional(fs, args, "launchpad status <app>")
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
	fmt.Print(string(out))
	return nil
}

func usage() {
	render.Banner(version.Current)
	fmt.Println(`Usage: launchpad <command> [flags]

Commands:
  onboard            Register and generate GitOps files for an application
  release <app>      Promote one service to a new image tag
  list               List registered applications
  status <app>       Show an application's services and pods
  version            Print version
  help               Show this help

Onboard flags:
  --root <dir>          platform repo root (default ".")
  --gitops-repo <url>   repo URL ArgoCD watches
  --from <file>         non-interactive: load the app spec from YAML
  --path-prefix <p>     repo-relative prefix for ArgoCD paths (default: auto)
  --commit              stage + commit generated files (never pushes)

Release flags:
  --service <name>      the service to promote (required)
  --tag <tag>           the image tag to promote to (required)
  --root <dir>          platform repo root (default ".")
  --dry-run             report what would change without writing
  --commit              stage + commit changed files (never pushes)

Example:
  launchpad release superheros --service catalog --tag v2-abc1234`)
}
