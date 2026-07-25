// Package gitops turns an application spec into the files ArgoCD reconciles.
//
// Endurance never deploys directly — it writes three files into the platform
// repo and lets ArgoCD do the rest:
//
//	apps/<name>/app.yaml         registry entry (human source of truth)
//	apps/<name>/values.yaml      values for the generic charts/app chart
//	apps/<name>/application.yaml ArgoCD Application (multi-source: chart + $values)
package gitops

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

// DefaultRepo is the platform repo ArgoCD watches when nothing better is known.
// It is a last resort: a stranger who forked this repo is served by their own
// origin remote, not by this one, so callers should try OriginURL first.
const DefaultRepo = "https://github.com/gc-ghub/project-gc-industries-devops-superheros.git"

// OriginURL returns the fetch URL of the origin remote of the repository
// enclosing root, or "" if there is no repo, no origin, or no git. ArgoCD
// clones over the network, so a local path is not a usable answer and is
// rejected here rather than written into an Application that cannot sync.
func OriginURL(root string) string {
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	cmd := exec.Command(git, "remote", "get-url", "origin")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if !strings.Contains(url, "://") && !strings.Contains(url, "@") {
		return "" // a local path; ArgoCD cannot clone it
	}
	return url
}

// chartValues is the shape consumed by charts/app.
//
// Route is omitted entirely when the application asked for none, so an
// application that never wanted a URL renders exactly the manifests it always
// did — which is what lets the Phase 1–9 regeneration proof keep working.
type chartValues struct {
	App      map[string]string `yaml:"app"`
	Mesh     spec.Mesh         `yaml:"mesh,omitempty"`
	Route    *spec.Route       `yaml:"route,omitempty"`
	Services []spec.Service    `yaml:"services"`
}

// routeValues returns the route to write into the values file, or nil.
func routeValues(app spec.App) *spec.Route {
	if !app.Route.Enabled {
		return nil
	}
	r := app.Route
	return &r
}

// argoAppTmpl is a multi-source ArgoCD Application: source 1 is the generic
// chart, source 2 is the same repo referenced as $values so the per-app values
// file can live outside the chart directory. Requires ArgoCD >= 2.6.
var argoAppTmpl = template.Must(template.New("argoapp").Parse(
	`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: {{ .Name }}
  namespace: argocd
{{- if .Subscriptions }}
  # Who hears about this application, and when. The stages a developer can ask
  # for are Endurance's; the triggers below are ArgoCD's — because ArgoCD is the
  # only thing that deploys, and so the only thing that knows whether a deploy
  # worked. The onboarded and requested stages have no trigger here on purpose:
  # those are sent by the CLI, which knows intent and nothing more.
  annotations:
{{- range .Subscriptions }}
    notifications.argoproj.io/subscribe.{{ .Trigger }}.{{ .Service }}: {{ printf "%q" .Recipient }}
{{- end }}
{{- end }}
spec:
  project: default
  sources:
    - repoURL: {{ .Repo }}
      targetRevision: HEAD
      path: {{ .Prefix }}charts/app
      helm:
        valueFiles:
          - $values/{{ .Prefix }}apps/{{ .Name }}/values.yaml
    - repoURL: {{ .Repo }}
      targetRevision: HEAD
      ref: values
  destination:
    server: https://kubernetes.default.svc
    namespace: {{ .Namespace }}
  syncPolicy:
{{- if .Mesh }}
    # Sidecar injection is a property of the namespace, and the namespace is
    # ArgoCD's to create — so the label that turns the mesh on belongs here
    # rather than in a kubectl command someone has to remember to run.
    managedNamespaceMetadata:
      labels:
        istio-injection: enabled
{{- end }}
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
`))

// AppDir returns the per-application directory under the platform repo root.
func AppDir(root, name string) string {
	return filepath.Join(root, "apps", name)
}

// RepoPrefix returns the path of root relative to the enclosing git repository
// root, as a forward-slash prefix ending in "/" (or "" when root *is* the repo
// root).
//
// This matters because ArgoCD resolves source paths from the repository root,
// not from wherever Endurance was run. When the platform tree is nested inside
// the repo — as in project-gc-industries-devops-superheros/gc-industries-devops-superheros —
// the chart lives at "<prefix>charts/app", and emitting a bare "charts/app"
// makes ArgoCD fail with "app path does not exist".
func RepoPrefix(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	// Walk up looking for the .git directory (or file, for worktrees).
	dir := abs
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			rel, err := filepath.Rel(dir, abs)
			if err != nil || rel == "." {
				return ""
			}
			return filepath.ToSlash(rel) + "/"
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // no repo found; assume root is the repo root
		}
		dir = parent
	}
}

// Generate writes the registry entry, chart values, and ArgoCD Application for
// app into <root>/apps/<name>/. gitopsRepo is the URL of the platform/GitOps
// repo that ArgoCD watches (not the developer's app source repo). pathPrefix is
// the repo-relative prefix for ArgoCD source paths; pass "" to auto-detect it
// from the enclosing git repository. It returns the written file paths.
func Generate(root string, app spec.App, gitopsRepo, pathPrefix string) ([]string, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	// An empty repo URL renders an Application the API server rejects
	// ("spec.sources[0].repoURL: Required value"), and it does so only at apply
	// time — long after the files were written and committed. Refuse here,
	// where the caller can still be told which knob is missing.
	if strings.TrimSpace(gitopsRepo) == "" {
		return nil, fmt.Errorf("no GitOps repo URL: ArgoCD needs the URL of the repo it watches (pass --gitops-repo)")
	}
	dir := AppDir(root, app.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	// 1 + 2. registry entry and chart values
	written, err := writeAppFiles(dir, &app)
	if err != nil {
		return nil, err
	}
	write := func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		written = append(written, p)
		return nil
	}

	// 3. ArgoCD Application
	if pathPrefix == "" {
		pathPrefix = RepoPrefix(root)
	}
	var argo bytes.Buffer
	if err := argoAppTmpl.Execute(&argo, struct {
		Name, Namespace, Repo, Prefix string
		Mesh                          bool
		Subscriptions                 []Subscription
	}{
		Name:          app.Name,
		Namespace:     app.Namespace,
		Repo:          gitopsRepo,
		Prefix:        pathPrefix,
		Mesh:          app.Mesh.Enabled,
		Subscriptions: Subscriptions(app.Notify),
	}); err != nil {
		return nil, err
	}
	if err := write("application.yaml", argo.Bytes()); err != nil {
		return nil, err
	}

	return written, nil
}

// writeAppFiles renders the two files derived purely from the app spec — the
// registry entry and the chart values — into dir, defaulting empty tags first.
// It deliberately does not touch application.yaml: that file describes *where
// and how* the app is deployed, which onboarding owns and a release must not
// disturb. Shared by Generate and SetServiceTag so the two can never drift.
func writeAppFiles(dir string, app *spec.App) ([]string, error) {
	app.ApplyDefaults()

	reg, err := yaml.Marshal(app)
	if err != nil {
		return nil, err
	}
	vals, err := yaml.Marshal(chartValues{
		App:      map[string]string{"name": app.Name},
		Mesh:     app.Mesh,
		Route:    routeValues(*app),
		Services: app.Services,
	})
	if err != nil {
		return nil, err
	}

	files := []struct {
		name string
		data []byte
	}{
		{"app.yaml", withHeader("Endurance registry entry — do not edit by hand; use `endurance onboard`", reg)},
		{"values.yaml", withHeader("Values for charts/app — rendered by Endurance", vals)},
	}
	var written []string
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			return nil, err
		}
		written = append(written, p)
	}
	return written, nil
}

// Bump records what a release changed, so the caller can report it and decide
// whether anything needs committing.
type Bump struct {
	App     spec.App // the application after the bump
	Service string   // the service whose tag moved
	Version string   // the version whose tag moved ("" for a non-canary service)
	OldTag  string   // tag before the bump
	NewTag  string   // tag after the bump
	Written []string // files rewritten (empty when NoOp)
	NoOp    bool     // true when the target was already at NewTag
}

// Target names what the bump moved, for log lines: "catalog" or "catalog/v2".
func (b Bump) Target() string {
	if b.Version == "" {
		return b.Service
	}
	return b.Service + "/" + b.Version
}

// PlanServiceTag works out what a release would change, without touching disk.
//
// Separating the plan from the write is what makes the Phase 3 policy gate
// possible: the caller gets the post-bump application, hands it to the gate, and
// only calls WriteBump once the policies have passed. It also means --dry-run
// and a real release share one code path, so the two can never disagree about
// what a release does.
// versionName selects which variant of a canary service a release targets; it
// must be empty for a service that declares no versions.
func PlanServiceTag(root, appName, svcName, versionName, tag string) (Bump, error) {
	var b Bump
	if err := spec.ValidateTag(tag); err != nil {
		return b, err
	}
	app, err := Load(root, appName)
	if err != nil {
		return b, fmt.Errorf("no registered app %q — run `endurance list` to see what is registered", appName)
	}
	i := app.FindService(svcName)
	if i < 0 {
		return b, fmt.Errorf("app %q has no service %q — services are: %s",
			appName, svcName, strings.Join(app.ServiceNames(), ", "))
	}
	s := &app.Services[i]

	// Which tag is "the" tag depends on whether the service is a canary. Guessing
	// would be worse than refusing: releasing to a canary service without naming
	// a version could plausibly mean "all of them", and moving three versions at
	// once is not something a developer should get by omission.
	var target *string
	switch {
	case s.IsCanary() && versionName == "":
		return b, fmt.Errorf("service %q is split across versions — pass --version (versions are: %s)",
			svcName, strings.Join(s.VersionNames(), ", "))
	case s.IsCanary():
		j := s.FindVersion(versionName)
		if j < 0 {
			return b, fmt.Errorf("service %q has no version %q — versions are: %s",
				svcName, versionName, strings.Join(s.VersionNames(), ", "))
		}
		b.Version = versionName
		target = &s.Versions[j].Tag
	case versionName != "":
		return b, fmt.Errorf("service %q declares no versions — --version applies only to canary services", svcName)
	default:
		target = &s.Tag
	}

	b.Service, b.OldTag, b.NewTag = svcName, *target, tag
	if b.OldTag == tag {
		b.App, b.NoOp = app, true
		return b, nil
	}

	*target = tag
	// Validate the whole app, not just the tag: the registry entry on disk could
	// have been hand-edited into an invalid state, and a release must not
	// propagate that into the values file ArgoCD consumes.
	if err := app.Validate(); err != nil {
		return b, err
	}
	b.App = app
	return b, nil
}

// WeightChange records a canary traffic shift.
type WeightChange struct {
	App     spec.App
	Service string
	Deltas  []WeightDelta
	Written []string
	NoOp    bool // true when the requested weights are already in force
}

// WeightDelta is one version's share of traffic, before and after.
type WeightDelta struct {
	Version  string
	Old, New int
}

// Changed lists only the versions whose share actually moved.
func (w WeightChange) Changed() []WeightDelta {
	var out []WeightDelta
	for _, d := range w.Deltas {
		if d.Old != d.New {
			out = append(out, d)
		}
	}
	return out
}

// PlanWeights works out what a traffic shift would change, without touching
// disk — the same plan/gate/write shape a release uses.
func PlanWeights(root, appName, svcName string, weights map[string]int) (WeightChange, error) {
	var w WeightChange
	app, err := Load(root, appName)
	if err != nil {
		return w, fmt.Errorf("no registered app %q — run `endurance list` to see what is registered", appName)
	}
	i := app.FindService(svcName)
	if i < 0 {
		return w, fmt.Errorf("app %q has no service %q — services are: %s",
			appName, svcName, strings.Join(app.ServiceNames(), ", "))
	}
	for _, v := range app.Services[i].Versions {
		w.Deltas = append(w.Deltas, WeightDelta{Version: v.Name, Old: v.Weight, New: weights[v.Name]})
	}
	if err := app.SetWeights(svcName, weights); err != nil {
		return w, err
	}
	if err := app.Validate(); err != nil {
		return w, err
	}
	w.App, w.Service = app, svcName
	w.NoOp = len(w.Changed()) == 0
	return w, nil
}

// WriteWeights commits a planned traffic shift to disk. Like a release it
// rewrites only the registry entry and the chart values — and unlike a release
// it changes no pod spec at all, so ArgoCD updates the VirtualService and not a
// single pod restarts.
func WriteWeights(root string, w WeightChange) (WeightChange, error) {
	if w.NoOp {
		return w, nil
	}
	written, err := writeAppFiles(AppDir(root, w.App.Name), &w.App)
	if err != nil {
		return w, err
	}
	w.Written = written
	return w, nil
}

// WriteBump commits a planned bump to disk, rewriting the registry entry and
// chart values. The ArgoCD Application is left untouched — a release changes
// *what image runs*, never where or how the application is deployed.
func WriteBump(root string, b Bump) (Bump, error) {
	if b.NoOp {
		return b, nil
	}
	written, err := writeAppFiles(AppDir(root, b.App.Name), &b.App)
	if err != nil {
		return b, err
	}
	b.Written = written
	return b, nil
}

// SetServiceTag plans and writes a release in one step. Callers that need to
// inspect the result before it reaches disk — the policy gate, --dry-run —
// should use PlanServiceTag and WriteBump instead.
func SetServiceTag(root, appName, svcName, versionName, tag string) (Bump, error) {
	b, err := PlanServiceTag(root, appName, svcName, versionName, tag)
	if err != nil {
		return b, err
	}
	return WriteBump(root, b)
}

func withHeader(comment string, body []byte) []byte {
	return append([]byte(fmt.Sprintf("# %s\n---\n", comment)), body...)
}
