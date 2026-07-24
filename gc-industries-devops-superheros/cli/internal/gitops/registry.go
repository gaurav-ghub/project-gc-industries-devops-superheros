package gitops

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/gc-ghub/endurance/internal/spec"
	"gopkg.in/yaml.v3"
)

// Load reads a single registered application's spec.
func Load(root, name string) (spec.App, error) {
	var app spec.App
	data, err := os.ReadFile(filepath.Join(AppDir(root, name), "app.yaml"))
	if err != nil {
		return app, err
	}
	err = yaml.Unmarshal(data, &app)
	return app, err
}

// List returns all registered applications under <root>/apps, sorted by name.
func List(root string) ([]spec.App, error) {
	dir := filepath.Join(root, "apps")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var apps []spec.App
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		app, err := Load(root, e.Name())
		if err != nil {
			continue // skip dirs without a valid registry entry
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, nil
}
