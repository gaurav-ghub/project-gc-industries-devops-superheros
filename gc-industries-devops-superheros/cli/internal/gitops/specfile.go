package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gc-ghub/launchpad/internal/spec"
	"gopkg.in/yaml.v3"
)

// SpecPath is where an application's human input lives: specs/<app>.yaml, the
// file `onboard --from` reads.
func SpecPath(root, app string) string {
	return filepath.Join(root, "specs", app+".yaml")
}

// weightLine matches the value of a `weight:` key, capturing what surrounds it
// so an inline comment survives the edit.
var weightLine = regexp.MustCompile(`^(\s*weight:\s*)(\d+)(\s*(?:#.*)?)$`)

// SyncSpecWeights writes a canary service's new traffic weights back into
// specs/<app>.yaml.
//
// Why this exists at all. Phase 4's live run left apps/superheros/values.yaml
// saying 10/10/80 while specs/superheros.yaml still said 40/30/30, so the next
// `onboard --from` would have silently reset the split — the same failure class
// that reverted the namespace fix in Phase 1. The rule "generated files are
// regenerated" is what makes LaunchPad predictable, and it is only safe while
// the input file is actually the truth. A weight is the one field that drifts,
// because a weight is operational state a human moves at 2am rather than a
// stated intention.
//
// So `canary set` closes the loop by editing the input too. The alternative —
// teaching `onboard` to preserve weights it finds in the generated files —
// would have made generated output take precedence over its own input, which is
// a much larger thing to be true of a GitOps platform.
//
// The edit is deliberately line-surgical rather than a YAML round-trip.
// specs/<app>.yaml is a hand-written file with a long explanatory comment on
// nearly every block, and marshalling a parsed document back out would silently
// delete all of it. Only the digits after `weight:` on the lines yaml.v3 tells
// us the versions live on are touched; every other byte of the file is
// preserved exactly.
//
// It returns the path written, or "" when there was nothing to do (no spec file
// on disk, or the file already agrees).
func SyncSpecWeights(root, app, svcName string, weights map[string]int) (string, error) {
	path := SpecPath(root, app)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil // an app onboarded interactively has no spec file; that is allowed
	}
	if err != nil {
		return "", err
	}

	lines, err := planWeightEdits(data, svcName, weights)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if len(lines) == 0 {
		return "", nil
	}

	out := strings.Split(string(data), "\n")
	changed := false
	for lineNo, want := range lines {
		i := lineNo - 1 // yaml.Node lines are 1-based
		if i < 0 || i >= len(out) {
			return "", fmt.Errorf("%s: weight is on line %d but the file has %d lines", path, lineNo, len(out))
		}
		// Tolerate a CRLF checkout: split off the carriage return, edit, put it back.
		body, cr := strings.CutSuffix(out[i], "\r")
		m := weightLine.FindStringSubmatch(body)
		if m == nil {
			return "", fmt.Errorf("%s line %d is not a plain weight line (%q) — edit it by hand", path, lineNo, body)
		}
		if m[2] == strconv.Itoa(want) {
			continue
		}
		body = m[1] + strconv.Itoa(want) + m[3]
		if cr {
			body += "\r"
		}
		out[i] = body
		changed = true
	}
	if !changed {
		return "", nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// planWeightEdits locates the weight lines of one service's versions, returning
// line number -> wanted value. It uses yaml.v3's node line numbers rather than
// scanning for `weight:`, so a weight belonging to a different service — or to
// something that merely looks like one inside a comment — is never touched.
func planWeightEdits(data []byte, svcName string, weights map[string]int) (map[int]int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.SequenceNode {
		return nil, nil
	}
	for _, svc := range services.Content {
		if scalar(mapValue(svc, "name")) != svcName {
			continue
		}
		versions := mapValue(svc, "versions")
		if versions == nil || versions.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("service %q declares no versions", svcName)
		}
		out := map[int]int{}
		for _, v := range versions.Content {
			name := scalar(mapValue(v, "name"))
			want, ok := weights[name]
			if !ok {
				return nil, fmt.Errorf("service %q version %q has no new weight", svcName, name)
			}
			w := mapValue(v, "weight")
			if w == nil {
				return nil, fmt.Errorf("service %q version %q has no weight field", svcName, name)
			}
			out[w.Line] = want
		}
		return out, nil
	}
	// The spec file does not describe this service. Not an error: the registry
	// is the authority on what exists, and a spec that has moved on is the
	// developer's business.
	return nil, nil
}

// WeightDrift compares the traffic weights in an incoming spec against the ones
// already in the registry, and describes any disagreement.
//
// This is the second half of the drift fix, and it covers the case
// SyncSpecWeights cannot: weights changed by editing apps/<name>/values.yaml
// directly, or by a `canary set --no-spec`, or on a checkout where the spec
// write-back never ran. Onboarding will reset those splits, silently, because
// regenerating is exactly what onboarding is for — so it has to say so first.
func WeightDrift(root string, incoming spec.App) []string {
	current, err := Load(root, incoming.Name)
	if err != nil {
		return nil // not registered yet; there is nothing to disagree with
	}
	var out []string
	for _, want := range incoming.Services {
		if !want.IsCanary() {
			continue
		}
		i := current.FindService(want.Name)
		if i < 0 {
			continue
		}
		have := current.Services[i]
		if !have.IsCanary() {
			continue
		}
		var diffs []string
		for _, v := range want.Versions {
			j := have.FindVersion(v.Name)
			if j < 0 {
				continue
			}
			if have.Versions[j].Weight != v.Weight {
				diffs = append(diffs, fmt.Sprintf("%s %d%%→%d%%", v.Name, have.Versions[j].Weight, v.Weight))
			}
		}
		if len(diffs) > 0 {
			out = append(out, fmt.Sprintf(
				"%s: onboarding will reset the live traffic split — %s. The spec is the input; if the deployed split is the one you want, copy it back into specs/%s.yaml first",
				want.Name, strings.Join(diffs, ", "), incoming.Name))
		}
	}
	return out
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}
