// Package gitops turns an application spec into the files ArgoCD reconciles.
//
// LaunchPad never deploys directly — it writes three files into the platform
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
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gc-ghub/launchpad/internal/spec"
	"gopkg.in/yaml.v3"
)

// chartValues is the shape consumed by charts/app.
type chartValues struct {
	App      map[string]string `yaml:"app"`
	Services []spec.Service    `yaml:"services"`
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
// not from wherever LaunchPad was run. When the platform tree is nested inside
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
	if err := argoAppTmpl.Execute(&argo, struct{ Name, Namespace, Repo, Prefix string }{
		Name:      app.Name,
		Namespace: app.Namespace,
		Repo:      gitopsRepo,
		Prefix:    pathPrefix,
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
	for i := range app.Services {
		if app.Services[i].Tag == "" {
			app.Services[i].Tag = "latest"
		}
	}

	reg, err := yaml.Marshal(app)
	if err != nil {
		return nil, err
	}
	vals, err := yaml.Marshal(chartValues{
		App:      map[string]string{"name": app.Name},
		Services: app.Services,
	})
	if err != nil {
		return nil, err
	}

	files := []struct {
		name string
		data []byte
	}{
		{"app.yaml", withHeader("LaunchPad registry entry — do not edit by hand; use `launchpad onboard`", reg)},
		{"values.yaml", withHeader("Values for charts/app — rendered by LaunchPad", vals)},
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
	OldTag  string   // tag before the bump
	NewTag  string   // tag after the bump
	Written []string // files rewritten (empty when NoOp)
	NoOp    bool     // true when the service was already at NewTag
}

// SetServiceTag bumps exactly one service's image tag in a registered
// application and rewrites the registry entry and chart values.
//
// This is the file half of `launchpad release`: one line changes, ArgoCD
// notices, and only that service's Deployment rolls. The ArgoCD Application is
// left untouched — a release changes *what image runs*, never where or how the
// application is deployed.
func SetServiceTag(root, appName, svcName, tag string) (Bump, error) {
	var b Bump
	if err := spec.ValidateTag(tag); err != nil {
		return b, err
	}
	app, err := Load(root, appName)
	if err != nil {
		return b, fmt.Errorf("no registered app %q — run `launchpad list` to see what is registered", appName)
	}
	i := app.FindService(svcName)
	if i < 0 {
		return b, fmt.Errorf("app %q has no service %q — services are: %s",
			appName, svcName, strings.Join(app.ServiceNames(), ", "))
	}

	b.Service, b.OldTag, b.NewTag = svcName, app.Services[i].Tag, tag
	if b.OldTag == tag {
		b.App, b.NoOp = app, true
		return b, nil
	}

	app.Services[i].Tag = tag
	// Validate the whole app, not just the tag: the registry entry on disk could
	// have been hand-edited into an invalid state, and a release must not
	// propagate that into the values file ArgoCD consumes.
	if err := app.Validate(); err != nil {
		return b, err
	}

	written, err := writeAppFiles(AppDir(root, appName), &app)
	if err != nil {
		return b, err
	}
	b.App, b.Written = app, written
	return b, nil
}

func withHeader(comment string, body []byte) []byte {
	return append([]byte(fmt.Sprintf("# %s\n---\n", comment)), body...)
}
