package platform

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// This file exists because of one fault, and it is worth naming precisely.
//
// `infra/monitoring/pod-restart-alert.yaml` was a correct PrometheusRule. It had
// the right apiVersion, the right selector label, a sensible expression, and it
// had been reviewed. It was applied by no install script and watched by no
// ArgoCD Application, so it had never been in a cluster — in any run of this
// platform, since Phase 6. Everything downstream of it was wired correctly and
// was verified green during the first outside run: the Alertmanager route, the
// enricher deployment, `enable ai`'s Secret, `config list`'s confirmation.
//
// Being green at every link except the first is indistinguishable, from inside,
// from working. So the check that was missing is not "is the file correct" —
// every reviewer answered that one — but "is there anything that would ever put
// this in a cluster".
//
// The scope is deliberately narrow. infra/ also holds the pre-Endurance
// manifests, a Korion sample and a superseded AlertmanagerConfig, none of which
// anything applies and none of which claims to be live. What must be applied is
// what the platform's own behaviour depends on: the policies that govern
// applications and the rules that alert on them.

// appliedKinds are the resource kinds whose whole purpose is to be in the
// cluster. A file declaring one of these and reachable by nothing is the bug
// this file tests for.
var appliedKinds = map[string]bool{
	"PrometheusRule": true,
	"ClusterPolicy":  true,
}

// TestEveryPolicyAndAlertRuleIsSyncedByArgoCD walks infra/ for the kinds above
// and requires each one to sit inside a directory that an ArgoCD Application
// syncs — where that Application is itself applied by a platform install script,
// because an Application manifest nobody applies is the same bug one level up.
func TestEveryPolicyAndAlertRuleIsSyncedByArgoCD(t *testing.T) {
	root := repoRoot(t)
	synced := syncedPaths(t, root)
	if len(synced) == 0 {
		t.Fatal("no ArgoCD Application in infra/argocd/ is applied by any platform script — " +
			"nothing in this repo would reach the cluster")
	}

	infra := filepath.Join(root, "infra")
	found := 0
	err := filepath.WalkDir(infra, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		kind := kindsIn(t, path)
		for _, k := range kind {
			if !appliedKinds[k] {
				continue
			}
			found++
			dir := repoRel(t, root, filepath.Dir(path))
			if !coveredBy(synced, dir) {
				t.Errorf("%s declares a %s and lives in %s, which no ArgoCD Application syncs — "+
					"a declared resource that nothing applies is not configuration.\n"+
					"  synced paths: %v", repoRel(t, root, path), k, dir, synced)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", infra, err)
	}
	// A guard against the test passing because it found nothing to check — which
	// is exactly the shape of the fault it is here to prevent.
	if found == 0 {
		t.Fatalf("no PrometheusRule or ClusterPolicy found under %s", infra)
	}
}

// TestTheAlertRulesDirectoryHoldsOnlyRules. ArgoCD syncs a directory, not a
// file, so anything sharing that directory is applied with the rules whether it
// was meant to be or not. infra/monitoring/ is exactly why this matters: it also
// holds a superseded AlertmanagerConfig scoped to a namespace that may not exist
// and a Tempo ConfigMap, and pointing the Application at the parent directory
// would have applied both.
func TestTheAlertRulesDirectoryHoldsOnlyRules(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "infra", "monitoring", "rules")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("%s/%s is a directory — ArgoCD would sync it too", dir, e.Name())
			continue
		}
		for _, k := range kindsIn(t, filepath.Join(dir, e.Name())) {
			if k != "PrometheusRule" {
				t.Errorf("infra/monitoring/rules/%s declares a %s — this directory is synced "+
					"wholesale by the platform-alert-rules Application and holds rules only",
					e.Name(), k)
			}
		}
	}
}

// syncedPaths returns the repo-relative source paths of every ArgoCD Application
// under infra/argocd/ that some script in platform/ actually applies.
func syncedPaths(t *testing.T, root string) []string {
	t.Helper()
	scripts := allScripts(t, root)

	dir := filepath.Join(root, "infra", "argocd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// Applied by name: the install scripts reference these files literally,
		// so the filename appearing in a script is the link being asserted.
		if !strings.Contains(scripts, e.Name()) {
			continue
		}
		var app struct {
			Kind string `yaml:"kind"`
			Spec struct {
				Source struct {
					Path string `yaml:"path"`
				} `yaml:"source"`
			} `yaml:"spec"`
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(data, &app); err != nil || app.Kind != "Application" {
			continue
		}
		if p := strings.Trim(app.Spec.Source.Path, "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allScripts is every shell script under platform/, concatenated. Crude on
// purpose: the question is only whether a filename is named anywhere that runs.
func allScripts(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(filepath.Join(root, "platform"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".sh" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.Write(data)
		return nil
	})
	if err != nil {
		t.Fatalf("reading platform scripts: %v", err)
	}
	return b.String()
}

// coveredBy reports whether dir is one of the synced Application paths.
//
// Suffix rather than equality because an Application's path is resolved from the
// *repository* root and the platform tree is one directory inside its repo —
// "gc-industries-devops-superheros/infra/monitoring/rules" and
// "infra/monitoring/rules" are the same place, and which spelling is correct
// changes at the Phase 15 repo split.
func coveredBy(synced []string, dir string) bool {
	for _, p := range synced {
		if p == dir || strings.HasSuffix(p, "/"+dir) {
			return true
		}
	}
	return false
}

// kindsIn returns the `kind` of every YAML document in a file.
func kindsIn(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var out []string
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var doc struct {
			Kind string `yaml:"kind"`
		}
		if err := dec.Decode(&doc); err != nil {
			break
		}
		if doc.Kind != "" {
			out = append(out, doc.Kind)
		}
	}
	return out
}

func repoRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("relativising %s: %v", path, err)
	}
	return filepath.ToSlash(rel)
}
