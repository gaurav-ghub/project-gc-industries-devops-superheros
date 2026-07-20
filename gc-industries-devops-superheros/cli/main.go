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
	"github.com/gc-ghub/launchpad/internal/render"
)

const version = "v0.1.0"

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
	case "list", "ls":
		err = cmdList(args)
	case "status":
		err = cmdStatus(args)
	case "version", "--version", "-v":
		fmt.Println("launchpad " + version)
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
	_ = fs.Parse(args)
	return onboard.Run(onboard.Options{Root: *root, GitopsRepo: *repo, Commit: *commit, From: *from})
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
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: launchpad status <app>")
	}
	name := fs.Arg(0)

	app, err := gitops.Load(*root, name)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", name, err)
	}
	render.Banner(version)
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
	render.Banner(version)
	fmt.Println(`Usage: launchpad <command> [flags]

Commands:
  onboard            Register and generate GitOps files for an application
  list               List registered applications
  status <app>       Show an application's services and pods
  version            Print version
  help               Show this help

Onboard flags:
  --root <dir>          platform repo root (default ".")
  --gitops-repo <url>   repo URL ArgoCD watches
  --commit              stage + commit generated files (never pushes)`)
}
