package success

import (
	"flag"
	"os"
	"testing"

	"github.com/gc-ghub/endurance/internal/platform"
	"github.com/gc-ghub/endurance/internal/render"
	"github.com/gc-ghub/endurance/internal/spec"
)

// TestPreview replays the post-deploy screen to stdout, in colour, with no
// cluster:
//
//	go test ./internal/success -run TestPreview -preview -v
//
// The tests beside it prove the screen says the right things; only an eye can
// say whether it reads well. It is the sibling of the bootstrap preview in
// internal/platform, and it exists for the same reason: the feedback loop for
// "does this look right" used to be a ten-minute cluster build, which is why
// five faults survived into Phase 9's first live run.
//
// The four states are all here on purpose. The healthy one is the screenshot;
// the other three are the ones that decide whether the platform is trusted, and
// they are the ones nobody looks at until a demo.
var preview = flag.Bool("preview", false, "replay the post-deploy screen to stdout, in colour")

func TestPreview(t *testing.T) {
	if !*preview {
		t.Skip("pass -preview to watch the success screen render")
	}
	old := render.SetDefault(render.New(os.Stdout, render.WithColor(true), render.WithTTY(false)))
	defer render.SetDefault(old)

	root := repoRoot(t)

	a := spec.App{
		Name:      "superheros",
		Namespace: "superheros",
		Owner:     "gc-industries",
		Mesh:      spec.MeshOn(),
		Route:     spec.Route{Enabled: true, Path: "/", Service: "frontend"},
		Services: []spec.Service{
			{Name: "frontend", Image: "docker.io/dockergc00/superheros-frontend", Tag: "v1-9c6f729", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/dockergc00/superheros-catalog", Port: 8081,
				Versions: []spec.Version{
					{Name: "v1", Tag: "v1-6f11a5d", Weight: 30, Replicas: 1},
					{Name: "v2", Tag: "v2-6f11a5d", Weight: 30, Replicas: 1},
					{Name: "v3", Tag: "v3-6f11a5d", Weight: 40, Replicas: 1},
				}},
			{Name: "orders", Image: "docker.io/dockergc00/superheros-orders", Tag: "v1-df408cf", Port: 8082, Replicas: 1},
		},
	}
	a.ApplyDefaults()

	healthy := []platform.PodState{
		{Name: "frontend-6d9f7c8b4d-r2x9p", Status: "Running", Ready: true},
		{Name: "catalog-v1-7c5f6d4b88-k4t2m", Status: "Running", Ready: true},
		{Name: "catalog-v2-58c9d7f4a1-p7n2v", Status: "Running", Ready: true},
		{Name: "catalog-v3-6b4c8e9d33-m1x8s", Status: "Running", Ready: true},
		{Name: "orders-59d8b7f6c5-h8w3q", Status: "Running", Ready: true},
	}
	deploying := []platform.PodState{
		{Name: "frontend-6d9f7c8b4d-r2x9p", Status: "Running", Ready: true},
		{Name: "catalog-v1-7c5f6d4b88-k4t2m", Status: "Running", Ready: true},
		{Name: "catalog-v2-58c9d7f4a1-p7n2v", Status: "ContainerCreating"},
		{Name: "catalog-v3-6b4c8e9d33-m1x8s", Status: "Pending"},
		{Name: "orders-59d8b7f6c5-h8w3q", Status: "CrashLoopBackOff"},
	}

	render.Banner("preview")

	render.Section("preview · deployed and healthy")
	render.SuccessScreen(Build(root, a, healthy, true))

	render.Section("preview · still deploying, and one pod that is not coming back")
	render.SuccessScreen(Build(root, a, deploying, true))

	render.Section("preview · onboarded, never pushed")
	render.SuccessScreen(Build(root, a, nil, true))

	render.Section("preview · the cluster is not there")
	render.SuccessScreen(Build(root, a, nil, false))

	// The N=1 case, which is the shape the product mock was drawn for: one
	// service, one image, one replica count.
	one := a
	one.Services = a.Services[:1]
	one.ApplyDefaults()
	render.Section("preview · a single-service application")
	render.SuccessScreen(Build(root, one, healthy[:1], true))
}
