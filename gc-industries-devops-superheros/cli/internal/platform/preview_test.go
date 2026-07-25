package platform

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gc-ghub/endurance/internal/render"
)

// TestPreview replays a bootstrap and a teardown to stdout, in colour, with no
// Docker and no cluster:
//
//	go test ./internal/platform -run TestPreview -preview -v
//
// The tests either side of this file prove the transcript says the right
// things; only an eye can say whether it reads well. A ten-minute live
// bootstrap is a slow way to look at a colour, and this is the same code path —
// the same Progress, the same steps, the same detail channel — with the
// subprocess replaced by a script of what the real modules say.
//
// Skipped without -preview: it is a demo, not an assertion.
var preview = flag.Bool("preview", false, "replay a bootstrap and a teardown to stdout, in colour")

func TestPreview(t *testing.T) {
	if !*preview {
		t.Skip("pass -preview to watch a bootstrap and a teardown render")
	}
	old := render.SetDefault(render.New(os.Stdout, render.WithColor(true), render.WithTTY(false)))
	defer render.SetDefault(old)

	root, err := FindRoot(".")
	if err != nil {
		t.Skip(err)
	}

	// What each module actually says, trimmed to a few lines each.
	says := map[string][]string{
		Chain[0].Script: {"Creating Kind cluster superheros...", "All Kubernetes nodes are Ready."},
		Chain[1].Script: {"Installing platform networking...", "✔ Istiod installed", "Istio control plane installed."},
		Chain[2].Script: {"Installing metrics platform...", "Release \"prometheus\" does not exist. Installing it now."},
		Chain[3].Script: {"Applying AI alertmanager manifests...", "warning: No superhero-ai-secret Secret found and no secret.yaml to apply."},
		Chain[4].Script: {"Installing ArgoCD...", "ArgoCD components installed."},
		Chain[5].Script: {"Installing Kyverno chart (3.8.2)...", "Kyverno installed."},
		Chain[6].Script: {
			"Ingress gateway is a NodePort on 30000/30001.",
			"Installing Kiali 2.17.0...",
			"Ingress Gateway and platform routes applied.",
			"host 8080 maps to node port 30000 (0.0.0.0:8080).",
		},
	}

	render.Banner("preview")
	p := render.NewProgress("Bootstrapping the platform", steps()...)
	for _, m := range Chain {
		step := p.Start(m.Step)
		out := step.StreamWith(nil)
		warnings := 0
		for _, line := range says[m.Script] {
			out.Write([]byte(line + "\n"))
			if len(line) > 8 && line[:8] == warningPrefix {
				warnings++
			}
			time.Sleep(120 * time.Millisecond)
		}
		out.Close()
		step.Rename(m.Done)
		if warnings > 0 {
			step.Warn(plural(warnings, "warning") + " from the module")
		} else {
			step.Done()
		}
	}
	p.Finish()
	AccessBlock(root, func(string) (int, error) { return 200, nil },
		// The credentials a real cluster would hand back, scripted — the block
		// is half of what closes a bootstrap now, and a preview that skipped it
		// would be previewing the wrong screen.
		func(args ...string) (string, error) {
			if len(args) > 4 && args[4] == "argocd-initial-admin-secret" {
				return base64.StdEncoding.EncodeToString([]byte("Xk7pQ2mR9tLw")), nil
			}
			if len(args) > 6 && strings.HasSuffix(args[6], "admin-user}") {
				return base64.StdEncoding.EncodeToString([]byte("admin")), nil
			}
			return base64.StdEncoding.EncodeToString([]byte("prom-operator")), nil
		})
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

	// The teardown, in the other colour.
	d := render.NewProgress("Destroying the platform", teardown.Step).Danger()
	step := d.Start(teardown.Step)
	out := step.StreamWith(nil)
	out.Write([]byte("Deleting cluster \"superheros\" ...\n"))
	out.Close()
	step.Rename(teardown.Done)
	step.Done()
	d.Finish()

	// And a failure, since that is the shape nobody looks at until it happens.
	f := render.NewProgress("Bootstrapping the platform", steps()...)
	f.Start(Chain[0].Step).Done()
	fs := f.Start(Chain[1].Step)
	o := fs.StreamWith(nil)
	o.Write([]byte("error: Failed to install Istio control plane.\n"))
	o.Close()
	_ = fs.Fail(errors.New("platform/networking/install.sh: exit status 1"))
	f.Finish()

	// `endurance urls --check`, both ways round. The second is the shape that
	// matters and the one nobody would otherwise look at until a demo: a
	// cluster created before the access layer, where every module installed
	// perfectly and nothing on the host can reach any of it.
	render.Section("preview · endurance urls --check, everything answering")
	_ = Urls(UrlsOptions{Root: root, Check: true, probe: func(string) (int, error) { return 200, nil }})

	render.Section("preview · endurance urls --check, a cluster with no port mappings")
	_ = Urls(UrlsOptions{Root: root, Check: true, probe: func(string) (int, error) {
		return 0, errors.New("connection refused")
	}})

	// And one route missing rather than all of them — an application's `/` that
	// shadowed a dashboard, or a route that never applied. It has to read
	// differently from "the platform is down", because the fix is different.
	render.Section("preview · endurance urls --check, one route gone")
	_ = Urls(UrlsOptions{Root: root, Check: true, probe: func(u string) (int, error) {
		if strings.HasSuffix(u, "/kiali") {
			return 404, nil
		}
		return 200, nil
	}})
}
