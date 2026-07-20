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
      path: charts/app
      helm:
        valueFiles:
          - $values/apps/{{ .Name }}/values.yaml
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

// Generate writes the registry entry, chart values, and ArgoCD Application for
// app into <root>/apps/<name>/. gitopsRepo is the URL of the platform/GitOps
// repo that ArgoCD watches (not the developer's app source repo). It returns the
// list of written file paths.
func Generate(root string, app spec.App, gitopsRepo string) ([]string, error) {
	if err := app.Validate(); err != nil {
		return nil, err
	}
	dir := AppDir(root, app.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	var written []string
	write := func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		written = append(written, p)
		return nil
	}

	// Default tags so values are always renderable.
	for i := range app.Services {
		if app.Services[i].Tag == "" {
			app.Services[i].Tag = "latest"
		}
	}

	// 1. registry entry
	reg, err := yaml.Marshal(app)
	if err != nil {
		return nil, err
	}
	if err := write("app.yaml", withHeader("LaunchPad registry entry — do not edit by hand; use `launchpad onboard`", reg)); err != nil {
		return nil, err
	}

	// 2. chart values
	vals, err := yaml.Marshal(chartValues{
		App:      map[string]string{"name": app.Name},
		Services: app.Services,
	})
	if err != nil {
		return nil, err
	}
	if err := write("values.yaml", withHeader("Values for charts/app — rendered by LaunchPad", vals)); err != nil {
		return nil, err
	}

	// 3. ArgoCD Application
	var argo bytes.Buffer
	if err := argoAppTmpl.Execute(&argo, struct{ Name, Namespace, Repo string }{
		Name:      app.Name,
		Namespace: app.Namespace,
		Repo:      gitopsRepo,
	}); err != nil {
		return nil, err
	}
	if err := write("application.yaml", argo.Bytes()); err != nil {
		return nil, err
	}

	return written, nil
}

func withHeader(comment string, body []byte) []byte {
	return append([]byte(fmt.Sprintf("# %s\n---\n", comment)), body...)
}
