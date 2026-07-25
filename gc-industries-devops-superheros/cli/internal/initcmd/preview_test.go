package initcmd

import (
	"errors"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
)

// TestPreview replays the guided first run to stdout, in colour, with no Docker
// and no cluster:
//
//	go test ./internal/initcmd -run TestPreview -preview -v
//
// The tests beside it prove the run says the right things; only an eye can say
// whether a stranger would follow it. It is the third of these — internal/platform
// replays a bootstrap, internal/success replays the post-deploy screen — and it
// exists for the same reason all three do: the feedback loop for "does this read
// well" used to be a ten-minute cluster build, which is how five faults survived
// into Phase 9's first live run.
//
// Four runs, because the happy one is the screenshot and the other three are the
// ones that decide whether the tool is trusted:
//
//  1. the plan, which is the screen the whole command turns on;
//  2. a platform that is already up, so the bootstrap is skipped;
//  3. a deploy ArgoCD cannot see, because nothing was pushed;
//  4. a cluster that predates the access layer, which is refused.
var preview = flag.Bool("preview", false, "replay the guided first run to stdout, in colour")

func TestPreview(t *testing.T) {
	if !*preview {
		t.Skip("pass -preview to watch `endurance init` render")
	}
	old := render.SetDefault(render.New(os.Stdout, render.WithColor(true), render.WithTTY(false)))
	defer render.SetDefault(old)

	root := sandbox(t)
	frozen := func() time.Time { return time.Unix(0, 0) }

	// 1 · the plan, for an application with both optional capabilities on. The
	// keys are sentinels and the point of the screen is that they do not appear
	// anywhere on it.
	render.Section("preview · init --dry-run, everything switched on")
	s := &spy{}
	_ = Run(Options{
		Root: root, Yes: true, DryRun: true,
		Bootstrap: s.bootstrap, Onboard: s.onboard, Kubectl: s.kube,
		Inspect: noCluster, Health: noHealth, Now: frozen, sleep: func(time.Duration) {},
		Ask: func(a *Answers, d Answers) error {
			*a = d
			a.Name, a.Owner = "shortener", "platform-team"
			a.AI, a.OpenAIKey, a.AISlackHook = true, fakeKey, fakeHook
			a.Slack, a.SlackHook = true, fakeHook
			return nil
		},
	})

	// 2 · the whole run, against a platform that is already installed. This is
	// what a second `init` looks like, and the bootstrap skip is the reason it
	// takes seconds rather than ten minutes.
	render.Section("preview · a full run, platform already up, ArgoCD syncs")
	s2 := &spy{}
	_ = Run(Options{
		Root: sandbox(t), Name: "shortener", Yes: true,
		Bootstrap: s2.bootstrap, Onboard: s2.onboard, Kubectl: s2.kube,
		Inspect: healthyCluster, Health: allHealth,
		Timeout: time.Second, Now: frozen, sleep: func(time.Duration) {},
	})

	// 3 · the run that stops short, and the one worth reading most carefully:
	// Endurance never pushes, so a commit ArgoCD cannot see is where the
	// automation ends and a sentence has to do the rest.
	render.Section("preview · a deploy ArgoCD cannot see — nothing was pushed")
	s3 := &spy{}
	_ = Run(Options{
		Root: sandbox(t), Name: "shortener", Yes: true,
		Bootstrap: s3.bootstrap, Onboard: s3.onboard,
		Kubectl: func(args ...string) (string, error) {
			switch {
			case args[0] == "apply":
				return "application.argoproj.io/shortener created\n", nil
			case len(args) > 3 && args[3] == "application":
				return `{"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Missing"}}}`, nil
			}
			return "No resources found in shortener namespace.\n", nil
		},
		Inspect: healthyCluster, Health: allHealth,
		Timeout: 10 * time.Second, Now: frozen, sleep: func(time.Duration) {},
	})

	// 4 · the refusal. A cluster created before Phase 10 installs every module
	// perfectly and answers nothing, and this is the one screen that has to stop
	// a run rather than decorate it.
	render.Section("preview · a cluster that predates the access layer")
	s4 := &spy{}
	_ = Run(Options{
		Root: sandbox(t), Name: "shortener", Yes: true,
		Bootstrap: s4.bootstrap, Onboard: s4.onboard, Kubectl: s4.kube,
		Inspect: func(string) platform.ClusterState {
			return platform.ClusterState{Name: "superheros", Exists: true, Known: true, HostPort: 8080}
		},
		Health: noHealth, Now: frozen, sleep: func(time.Duration) {},
	})

	// 5 · and a failed apply, since that is the shape nobody looks at until it
	// happens in front of somebody.
	render.Section("preview · the ArgoCD Application would not apply")
	s5 := &spy{}
	_ = Run(Options{
		Root: sandbox(t), Name: "shortener", Yes: true,
		Bootstrap: s5.bootstrap, Onboard: s5.onboard,
		Kubectl: func(args ...string) (string, error) {
			return "error: unable to recognize \"application.yaml\": no matches for kind " +
				"\"Application\" in version \"argoproj.io/v1alpha1\"", errors.New("exit status 1")
		},
		Inspect: healthyCluster, Health: allHealth,
		Timeout: time.Second, Now: frozen, sleep: func(time.Duration) {},
	})
}
