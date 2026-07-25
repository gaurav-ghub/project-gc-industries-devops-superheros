// Package success renders the post-deploy screen: what was deployed, where it
// is, whether it is actually up yet, and what to type next.
//
// # The screen Phase 8 drew and Phase 10 filled in
//
// render.SuccessScreen has existed since Phase 8 and until now had only ever
// rendered from test data — there were no URLs to put in it and no cluster to
// ask about pods. The access layer supplied the first and kubectl the second.
//
// # The one rule it exists to keep
//
// A success screen that claims health it has not observed is the fastest way to
// make a platform untrusted, and this is the screen most likely to be
// screenshotted. So: a pod earns a ✓ only when the cluster said it is Running
// with every container ready. Anything else — pending, creating, crash-looping,
// or simply not asked about because the cluster is unreachable — renders as a ·
// with whatever the cluster actually said next to it, and the footer states the
// real count. "5 of 8 pods ready — ArgoCD is still syncing" is a more useful
// sentence than a checkmark, and it is true.
//
// The same rule covers the case where there is nothing to observe at all. An
// application that has been onboarded but never pushed has no pods, and the
// screen says so and points at the git push — rather than drawing an empty Pods
// section that reads like a failure.
package success

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/gc-ghub/endurance/internal/gitops"
	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// A KubectlFunc runs kubectl and returns its combined output. Tests replace it;
// the CLI never does.
type KubectlFunc func(args ...string) (string, error)

func realKubectl(args ...string) (string, error) {
	out, err := exec.Command("kubectl", args...).CombinedOutput()
	return string(out), err
}

// Options configures the screen.
type Options struct {
	Root    string // platform repo root
	App     string // the registered application to report on
	Kubectl KubectlFunc
}

// Screen prints the post-deploy screen for one application.
//
// It returns an error only when the application is not registered. A cluster
// that cannot be reached, or an application whose pods have not appeared, are
// both things to *report* — the developer asking "did my deploy work" is owed
// the answer "not yet, and here is why", not a stack trace.
func Screen(opts Options) error {
	app, err := gitops.Load(opts.Root, opts.App)
	if err != nil {
		return fmt.Errorf("no registered app %q (%v)", opts.App, err)
	}
	app.ApplyDefaults()

	kube := opts.Kubectl
	if kube == nil {
		if _, err := exec.LookPath("kubectl"); err != nil {
			kube = nil
		} else {
			kube = realKubectl
		}
	}

	root, rootErr := platform.FindRoot(opts.Root)
	if rootErr != nil {
		// status <app> is a developer command and must work from a checkout the
		// platform tree is not under. The addresses then come from the defaults,
		// which the URL block is honest about below.
		root = opts.Root
	}

	pods, observed := observePods(kube, app)
	res := Build(root, app, pods, observed)
	render.SuccessScreen(res)
	return nil
}

// observePods asks the cluster which pods this application has.
//
// The second result is whether the cluster answered at all. Nil pods and "the
// cluster said there are none" are different facts and the screen says different
// things about them, so they must not collapse into one empty slice.
func observePods(kube KubectlFunc, app spec.App) ([]platform.PodState, bool) {
	if kube == nil {
		return nil, false
	}
	out, err := kube("get", "pods", "-n", app.Namespace,
		"-l", "app.kubernetes.io/part-of="+app.Name, "--no-headers")
	if err != nil {
		return nil, false
	}
	// kubectl says "No resources found in <ns> namespace." on stderr and exits
	// 0, so an empty namespace arrives as output rather than as nothing.
	if strings.HasPrefix(strings.TrimSpace(out), "No resources found") {
		return nil, true
	}
	return platform.ParsePodTable(out), true
}

// Build assembles the screen. Separated from printing it so the tests can
// assert on the content rather than on the frame — the frame is the render
// package's, and it has golden files of its own.
func Build(root string, app spec.App, pods []platform.PodState, observed bool) render.Result {
	base := platform.BaseURL(root)

	res := render.Result{
		Title: title(app, pods, observed),
		State: state(pods, observed),
		Rows:  rows(root, app),
		Pods:  podRows(pods),
		URLs:  urls(base, app),
		Hints: hints(app),
	}
	res.Footer = footer(app, pods, observed)
	return res
}

// title says what happened, and never more than was seen.
func title(app spec.App, pods []platform.PodState, observed bool) string {
	switch {
	case !observed:
		return app.Name + " — cluster not reached"
	case len(pods) == 0:
		return app.Name + " — nothing running yet"
	case readyCount(pods) == len(pods):
		return app.Name + " is deployed and healthy"
	default:
		return app.Name + " is deploying"
	}
}

// state is the glyph beside the title, and it earns a ✓ exactly once: when the
// cluster answered and every pod it named was ready. Everything else is pending
// — including a failing pod, because the application is not broken, one of its
// pods is, and the pod list below says which.
func state(pods []platform.PodState, observed bool) render.State {
	if observed && len(pods) > 0 && readyCount(pods) == len(pods) {
		return render.StateReady
	}
	return render.StatePending
}

func rows(root string, app spec.App) [][2]string {
	out := [][2]string{
		{"App", render.Value(app.Name)},
		{"Namespace", app.Namespace},
		{"Cluster", platform.ContextName(root)},
	}
	if app.Owner != "" {
		out = append(out, [2]string{"Owner", app.Owner})
	}

	// The PDF mock is a single-service application: namespace, cluster, image,
	// replicas. That is the N=1 case of this platform's model, so it renders
	// exactly that — and a multi-service application, where one image and one
	// replica count would be a lie, gets the list instead.
	if len(app.Services) == 1 {
		s := app.Services[0]
		out = append(out,
			[2]string{"Image", s.Ref()},
			[2]string{"Replicas", fmt.Sprint(desiredReplicas(app))})
		return out
	}
	out = append(out,
		[2]string{"Services", fmt.Sprintf("%d · %s", len(app.Services),
			strings.Join(app.ServiceNames(), ", "))},
		[2]string{"Replicas", fmt.Sprintf("%d desired", desiredReplicas(app))})
	if canaries := app.CanaryServices(); len(canaries) > 0 {
		out = append(out, [2]string{"Canary", strings.Join(canaries, ", ")})
	}
	return out
}

// desiredReplicas is what the spec asked for, across every service and every
// canary version of one. It is deliberately the *desired* number and labelled
// as such: what is running is the pod list below it.
func desiredReplicas(app spec.App) int {
	total := 0
	for _, s := range app.Services {
		for _, v := range s.Rollout() {
			n := v.Replicas
			if n == 0 {
				n = 1
			}
			total += n
		}
	}
	return total
}

// podRows maps the cluster's answer onto the screen's states.
//
// This is the whole honesty rule in four lines: Running-and-all-containers-ready
// is the only thing that becomes StateReady. A CrashLoopBackOff is a failure, and
// everything else — Pending, ContainerCreating, Terminating — is pending, which
// renders as · and not as a ✗, because a pod that is still being created is not
// a broken one.
func podRows(pods []platform.PodState) []render.Pod {
	out := make([]render.Pod, 0, len(pods))
	for _, p := range pods {
		state := render.StatePending
		switch {
		case p.Ready:
			state = render.StateReady
		case isFailing(p.Status):
			state = render.StateFailed
		}
		out = append(out, render.Pod{Name: p.Name, Status: p.Status, State: state})
	}
	return out
}

// isFailing lists the statuses that are a problem rather than a stage. Anything
// not named here is treated as "not yet", which is the safer default: a status
// this list has never heard of should not be reported as broken.
func isFailing(status string) bool {
	switch status {
	case "CrashLoopBackOff", "Error", "ImagePullBackOff", "ErrImagePull",
		"CreateContainerConfigError", "Evicted", "OOMKilled":
		return true
	}
	return false
}

func readyCount(pods []platform.PodState) int {
	n := 0
	for _, p := range pods {
		if p.Ready {
			n++
		}
	}
	return n
}

// urls puts the application's own address first, then the platform's.
//
// The application's comes from its spec — the platform does not decide what an
// application's URL looks like — and is absent entirely when it asked for none,
// with a line in the footer saying how to ask.
func urls(base string, app spec.App) []render.URL {
	var out []render.URL
	if u := app.URL(base); u != "" {
		out = append(out, render.URL{
			Label: "App", Addr: u,
			Note: "via " + app.Route.Service,
		})
	}
	// The platform's own, built from the same base this screen already
	// resolved: reading kind-config.yaml twice inside one screen is two chances
	// for its halves to disagree about which port the platform is on.
	return append(out, platform.AccessURLsFor(base)...)
}

// hints are the "useful commands" block from the PDF mock, in both dialects.
//
// Both on purpose. `endurance` is what a developer on this platform should
// type, and kubectl is what they will type anyway when something is wrong at
// 2am — pretending otherwise makes the tool feel like it is hiding the cluster.
func hints(app spec.App) []render.Hint {
	out := []render.Hint{
		{Command: "endurance status " + app.Name, Note: "this screen, again"},
		{Command: "endurance urls --check", Note: "are the addresses answering"},
	}
	if len(app.CanaryServices()) > 0 {
		out = append(out, render.Hint{
			Command: "endurance canary status " + app.Name,
			Note:    "how traffic is split",
		})
	}
	svc := app.Services[0].Name
	return append(out,
		// Phase 11 gave the platform its own verb for this, so it is offered
		// first — and the kubectl form stays underneath, because that is what a
		// developer types at 2am and a tool that hides the cluster is a tool
		// people work around.
		render.Hint{
			Command: "endurance logs " + app.Name + " --service " + svc,
			Note:    "its logs",
		},
		render.Hint{
			Command: "endurance release " + app.Name + " --service " + svc + " --tag <tag>",
			Note:    "promote one service",
		},
		render.Hint{
			Command: "kubectl get pods -n " + app.Namespace,
			Note:    "the raw truth",
		},
		render.Hint{
			Command: "kubectl logs -n " + app.Namespace + " -l app.kubernetes.io/name=" + svc + " -f",
			Note:    "follow one service",
		},
	)
}

// footer says the quiet part: how much of this screen was observed.
//
// Kept to one short line. The box grows to fit its content by design (Phase 8),
// so a paragraph here produces a 130-column box — which Phase 9 already
// established is a bug in the caller and not in the box.
func footer(app spec.App, pods []platform.PodState, observed bool) string {
	switch {
	case !observed:
		return "no pods were observed — only the rows above were read from files"
	case len(pods) == 0:
		return "no pods yet — ArgoCD deploys pushed state, so check the commit landed"
	}
	ready := readyCount(pods)
	switch {
	case ready < len(pods):
		return fmt.Sprintf("%d of %d pods ready — ArgoCD is still syncing", ready, len(pods))
	case !app.Route.Enabled:
		return fmt.Sprintf("%d of %d pods ready · no route declared, so no URL of its own",
			ready, len(pods))
	default:
		return fmt.Sprintf("%d of %d pods ready", ready, len(pods))
	}
}
