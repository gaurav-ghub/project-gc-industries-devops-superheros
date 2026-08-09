package spec

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 14.7 — the two repo fields, named apart.
//
// `repository` (the application's own source, recorded, never cloned) and
// `--gitops-repo` (the repo ArgoCD watches) are both GitHub URLs, and until
// v0.12.0 the first was a line away from the second in the same file with a name
// that described neither. The tester in the first outside run edited
// specs/portfolio.yaml four times trying both and settled on the wrong one.

const legacySpec = `
name: portfolio
namespace: portfolio
repository: https://github.com/somebody/portfolio.git
owner: somebody
services:
  - name: backend
    image: docker.io/somebody/portfolio-backend
    tag: v1
    port: 8080
`

const renamedSpec = `
name: portfolio
namespace: portfolio
sourceRepo: https://github.com/somebody/portfolio.git
owner: somebody
services:
  - name: backend
    image: docker.io/somebody/portfolio-backend
    tag: v1
    port: 8080
`

func decode(t *testing.T, doc string) App {
	t.Helper()
	var a App
	if err := yaml.Unmarshal([]byte(doc), &a); err != nil {
		t.Fatalf("parsing the spec: %v", err)
	}
	return a
}

// The alias exists because specs are files people have already written, and a
// rename that breaks every one of them is a rename that gets reverted.
func TestTheOldRepositoryKeyStillParses(t *testing.T) {
	a := decode(t, legacySpec)
	if a.SourceRepo != "https://github.com/somebody/portfolio.git" {
		t.Fatalf("SourceRepo = %q; `repository:` must still be read", a.SourceRepo)
	}
	if len(a.Deprecated()) == 0 {
		t.Error("a spec using the old key produced no deprecation notice — " +
			"an alias nobody is told about is a rename that never happens")
	}
	if !strings.Contains(strings.Join(a.Deprecated(), " "), "sourceRepo") {
		t.Error("the notice does not name the new spelling, which is the only thing it is for")
	}
}

func TestTheNewKeyIsQuiet(t *testing.T) {
	a := decode(t, renamedSpec)
	if a.SourceRepo != "https://github.com/somebody/portfolio.git" {
		t.Fatalf("SourceRepo = %q", a.SourceRepo)
	}
	if got := a.Deprecated(); len(got) != 0 {
		t.Errorf("a spec using the current spelling was warned about: %v", got)
	}
}

// The alias is one-way. A format that accepts two spellings and emits whichever
// it was handed has two answers to what a field is called, which is the fault
// being removed rather than a compatibility feature.
func TestEnduranceOnlyEverWritesTheNewKey(t *testing.T) {
	out, err := yaml.Marshal(decode(t, legacySpec))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "repository:") {
		t.Errorf("a spec read under the old key was written back under it:\n%s", out)
	}
	if !strings.Contains(string(out), "sourceRepo:") {
		t.Errorf("the source repo was not written at all:\n%s", out)
	}
}

// An application with no source repo writes no line for one. Before the rename
// the field had no omitempty, so every generated registry entry in this repo
// carries a literal `repository: ""` — nine of them, on applications nobody
// onboarded here.
func TestAnAbsentSourceRepoWritesNothing(t *testing.T) {
	out, err := yaml.Marshal(App{
		Name: "x", Namespace: "x",
		Services: []Service{{Name: "x", Image: "i", Port: 1, Replicas: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "sourceRepo") {
		t.Errorf("an application with no source repo still declared one:\n%s", out)
	}
}

// Two names for one field, disagreeing, is a file with two answers. Resolving it
// silently would make an application's recorded source depend on which line its
// author edited last.
func TestBothSpellingsWithDifferentValuesIsRefused(t *testing.T) {
	var a App
	err := yaml.Unmarshal([]byte(`
name: portfolio
namespace: portfolio
sourceRepo: https://github.com/somebody/portfolio.git
repository: https://github.com/gc-ghub/project-gc-industries-devops-superheros.git
services:
  - name: backend
    image: i
    port: 8080
`), &a)
	if err == nil {
		t.Fatal("a spec declaring both spellings with different values was accepted")
	}
	if !strings.Contains(err.Error(), "sourceRepo") {
		t.Errorf("the refusal does not name the field to keep: %v", err)
	}
}

// The same value under both names is somebody mid-migration, and there is
// nothing to resolve.
func TestBothSpellingsAgreeingIsFine(t *testing.T) {
	var a App
	err := yaml.Unmarshal([]byte(`
name: portfolio
namespace: portfolio
sourceRepo: https://github.com/somebody/portfolio.git
repository: https://github.com/somebody/portfolio.git
services:
  - name: backend
    image: i
    port: 8080
`), &a)
	if err != nil {
		t.Fatalf("both spellings agreeing was refused: %v", err)
	}
	if len(a.Deprecated()) == 0 {
		t.Error("a file still carrying the old key was not told about the rename")
	}
}

// UnmarshalYAML is a place a decoder can silently lose the rest of the document.
// Everything else in the file has to survive the hook that reads one field.
func TestTheRestOfTheSpecSurvivesTheAliasHook(t *testing.T) {
	a := decode(t, `
name: superheros
namespace: superheros
sourceRepo: https://example.com/x.git
owner: gc-industries
mesh:
  enabled: true
routes:
  - path: /api/catalog
    service: catalog
  - path: /
    service: frontend
services:
  - name: frontend
    image: docker.io/x/frontend
    tag: v1
    port: 8080
    replicas: 2
  - name: catalog
    image: docker.io/x/catalog
    port: 8081
    versions:
      - name: v1
        tag: v1
        weight: 100
`)
	if a.Owner != "gc-industries" {
		t.Errorf("owner = %q", a.Owner)
	}
	if !a.Mesh.On() || a.Mesh.Enabled == nil {
		t.Error("the explicit mesh answer was lost")
	}
	if len(a.Routes) != 2 {
		t.Errorf("routes = %d, want 2", len(a.Routes))
	}
	if len(a.Services) != 2 || !a.Services[1].IsCanary() {
		t.Errorf("services did not survive: %+v", a.Services)
	}
}
