// Package policy is LaunchPad's deploy-time Kyverno gate.
//
// The cluster already enforces the ClusterPolicies in infra/kyverno_policy/ at
// admission time. That is the right place for the final word, but it is the
// wrong place to *learn* you got it wrong: by then the bad values are committed,
// pushed, and ArgoCD is reporting a sync error that names a webhook rather than
// the line the developer typed. This package reads those same policy files and
// evaluates them against the manifests LaunchPad is about to write, so a
// violation is refused before it can reach a commit.
//
// The policies are read from disk rather than reimplemented, so the gate and the
// cluster cannot disagree about what the rules are — editing a policy file
// changes both at once.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gc-ghub/launchpad/internal/manifest"
	"github.com/gc-ghub/launchpad/internal/spec"
	"gopkg.in/yaml.v3"
)

// DefaultDir is where the platform repo keeps its ClusterPolicies — the same
// directory the kyverno-policies ArgoCD Application syncs to the cluster.
const DefaultDir = "infra/kyverno_policy"

// Action mirrors Kyverno's validationFailureAction.
type Action string

const (
	Enforce Action = "Enforce"
	Audit   Action = "Audit"
)

// Rule is one rule of a ClusterPolicy, reduced to what a deploy-time gate can
// act on.
type Rule struct {
	Name       string
	Kinds      []string
	Namespaces []string // empty means "any namespace"
	Exclude    []string
	Message    string
	Pattern    any
	Kind       string // "validate", "mutate" or "generate"
}

// Policy is a Kyverno ClusterPolicy as the gate understands it.
type Policy struct {
	Name   string
	Action Action
	Rules  []Rule
	Source string // file it was loaded from, for error messages
}

// clusterPolicy mirrors the on-disk YAML. Kyverno accepts both the modern
// match.any/resources form and the legacy match.resources form; the policies in
// this repo use the legacy one, and both are read here so a policy rewritten in
// the newer style does not silently stop being enforced by the gate.
type clusterPolicy struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		ValidationFailureAction string `yaml:"validationFailureAction"`
		Rules                   []struct {
			Name     string        `yaml:"name"`
			Match    resourceBlock `yaml:"match"`
			Exclude  resourceBlock `yaml:"exclude"`
			Validate struct {
				Message string `yaml:"message"`
				Pattern any    `yaml:"pattern"`
			} `yaml:"validate"`
			Mutate   map[string]any `yaml:"mutate"`
			Generate map[string]any `yaml:"generate"`
		} `yaml:"rules"`
	} `yaml:"spec"`
}

type resourceBlock struct {
	Resources resourceFilter   `yaml:"resources"`
	Any       []resourceFilter `yaml:"any"`
	All       []resourceFilter `yaml:"all"`
}

type resourceFilter struct {
	Kinds      []string `yaml:"kinds"`
	Namespaces []string `yaml:"namespaces"`
	Resources  *struct {
		Kinds      []string `yaml:"kinds"`
		Namespaces []string `yaml:"namespaces"`
	} `yaml:"resources"`
}

// flatten merges the legacy and any/all forms into one kinds+namespaces pair.
func (b resourceBlock) flatten() (kinds, namespaces []string) {
	add := func(f resourceFilter) {
		kinds = append(kinds, f.Kinds...)
		namespaces = append(namespaces, f.Namespaces...)
		if f.Resources != nil {
			kinds = append(kinds, f.Resources.Kinds...)
			namespaces = append(namespaces, f.Resources.Namespaces...)
		}
	}
	add(b.Resources)
	for _, f := range b.Any {
		add(f)
	}
	for _, f := range b.All {
		add(f)
	}
	return kinds, namespaces
}

// Load reads every ClusterPolicy under dir. Non-policy YAML is skipped, so the
// directory can hold a kustomization or a README without breaking the gate.
func Load(dir string) ([]Policy, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading policies from %s: %w", dir, err)
	}
	var out []Policy
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		// A file may hold several documents.
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		for {
			var cp clusterPolicy
			if err := dec.Decode(&cp); err != nil {
				break // EOF, or a document this gate does not model
			}
			if cp.Kind != "ClusterPolicy" && cp.Kind != "Policy" {
				continue
			}
			out = append(out, convert(cp, path))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out, nil
}

func convert(cp clusterPolicy, source string) Policy {
	p := Policy{Name: cp.Metadata.Name, Action: Audit, Source: source}
	if strings.EqualFold(cp.Spec.ValidationFailureAction, string(Enforce)) {
		p.Action = Enforce
	}
	for _, r := range cp.Spec.Rules {
		kinds, namespaces := r.Match.flatten()
		_, excluded := r.Exclude.flatten()
		rule := Rule{
			Name:       r.Name,
			Kinds:      kinds,
			Namespaces: namespaces,
			Exclude:    excluded,
			Message:    r.Validate.Message,
			Pattern:    normalize(r.Validate.Pattern),
			Kind:       "validate",
		}
		switch {
		case r.Validate.Pattern != nil:
		case r.Mutate != nil:
			rule.Kind = "mutate"
		case r.Generate != nil:
			rule.Kind = "generate"
		default:
			rule.Kind = "unknown"
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
}

// normalize converts yaml.v3's map[any]any (produced for `any` targets in some
// shapes) into map[string]any so the matcher only has one map type to handle.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[k] = normalize(val)
		}
		return m
	case map[any]any:
		m := make(map[string]any, len(t))
		for k, val := range t {
			m[fmt.Sprint(k)] = normalize(val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, val := range t {
			l[i] = normalize(val)
		}
		return l
	default:
		return v
	}
}

// applies reports whether a rule is scoped to this resource.
func (r Rule) applies(res manifest.Resource) bool {
	if !contains(r.Kinds, res.Kind) {
		return false
	}
	if contains(r.Exclude, res.Namespace) {
		return false
	}
	if len(r.Namespaces) > 0 && !contains(r.Namespaces, res.Namespace) {
		return false
	}
	return true
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want || wildcard(v, want) {
			return true
		}
	}
	return false
}

// Violation is one resource failing one rule.
type Violation struct {
	Policy   string
	Rule     string
	Action   Action
	Kind     string
	Resource string
	Message  string // the policy author's message
	Detail   string // the specific field that failed
}

func (v Violation) String() string {
	s := fmt.Sprintf("%s/%s  %s %s — %s", v.Policy, v.Rule, v.Kind, v.Resource, v.Message)
	if v.Detail != "" {
		s += " (" + v.Detail + ")"
	}
	return s
}

// Skipped records a rule the gate could not evaluate, so an unevaluated rule is
// visible rather than mistaken for a pass.
type Skipped struct {
	Policy string
	Rule   string
	Reason string
}

// Report is the outcome of evaluating a policy set against a set of manifests.
type Report struct {
	Policies   int
	Checked    int // rule/resource pairs actually evaluated
	Violations []Violation
	Skipped    []Skipped
	Warnings   []string
}

// Blocking returns the violations that must stop a commit — those from policies
// whose validationFailureAction is Enforce. Audit-mode violations are reported
// but do not block, exactly as they would not block admission in the cluster.
func (r Report) Blocking() []Violation {
	var out []Violation
	for _, v := range r.Violations {
		if v.Action == Enforce {
			out = append(out, v)
		}
	}
	return out
}

// Evaluate checks every resource against every applicable rule.
func Evaluate(policies []Policy, resources []manifest.Resource) Report {
	rep := Report{Policies: len(policies)}
	seenSkip := map[string]bool{}

	for _, p := range policies {
		for _, rule := range p.Rules {
			if rule.Kind != "validate" {
				key := p.Name + "/" + rule.Name
				if !seenSkip[key] {
					seenSkip[key] = true
					rep.Skipped = append(rep.Skipped, Skipped{p.Name, rule.Name,
						rule.Kind + " rules act at admission time and have nothing to validate here"})
				}
				continue
			}
			for _, res := range resources {
				if !rule.applies(res) {
					continue
				}
				rep.Checked++
				mm, err := match("", rule.Pattern, res.Object)
				if err != nil {
					key := p.Name + "/" + rule.Name
					if !seenSkip[key] {
						seenSkip[key] = true
						rep.Skipped = append(rep.Skipped, Skipped{p.Name, rule.Name, err.Error()})
					}
					continue
				}
				if mm != nil {
					rep.Violations = append(rep.Violations, Violation{
						Policy: p.Name, Rule: rule.Name, Action: p.Action,
						Kind: res.Kind, Resource: res.Namespace + "/" + res.Name,
						Message: rule.Message, Detail: mm.String(),
					})
				}
			}
		}
	}
	return rep
}

// Check renders app and evaluates it, adding the advisory warnings that are not
// policy violations but reliably become one later.
func Check(policies []Policy, app spec.App) Report {
	rep := Evaluate(policies, manifest.Render(app))
	rep.Warnings = append(rep.Warnings, imageWarnings(app)...)

	// Every ClusterPolicy in this repo is scoped to `namespaces: [superheros]`,
	// so onboarding a second application into its own namespace would be checked
	// by nothing at all — and would report a clean pass. A gate that says "all
	// policies satisfied" after evaluating zero rules is actively misleading, so
	// the emptiness is reported instead.
	if rep.Checked == 0 && len(policies) > 0 {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf(
			"no policy rule matched namespace %q — this application is currently ungoverned; "+
				"widen the `namespaces` selector in the ClusterPolicies to cover it", app.Namespace))
	}
	return rep
}

// imageWarnings flags image references that carry no registry host.
//
// Kubernetes resolves a bare "dockergc00/superheros-catalog" against Docker Hub
// and the pod runs fine, but restrict-image-registries matches the literal
// string in the manifest against "docker.io/*" and rejects it. The image works
// and the policy refuses it at the same time, which is a confusing pair of facts
// to discover from a cluster, so the gate names it here.
//
// This holds because platform/security/kyverno/values.yaml sets
// enableDefaultRegistryMutation=false. Kyverno enables that mutation by default,
// and with it on, admission would silently rewrite the reference to
// "docker.io/..." before validating and let the unqualified form through — the
// gate would then be stricter than the cluster. Turning it off is what keeps the
// two halves of the policy story saying the same thing.
func imageWarnings(app spec.App) []string {
	var out []string
	for _, s := range app.Services {
		if registryOf(s.Image) == "" {
			out = append(out, fmt.Sprintf(
				"service %q image %q has no registry host — write it as docker.io/%s so registry policies can match it",
				s.Name, s.Image, s.Image))
		}
	}
	return out
}

// registryOf returns the registry host of an image reference, or "" when the
// reference is unqualified. The first path segment is a host only if it looks
// like one (contains a dot or a colon, or is "localhost").
func registryOf(image string) string {
	i := strings.Index(image, "/")
	if i < 0 {
		return ""
	}
	head := image[:i]
	if head == "localhost" || strings.ContainsAny(head, ".:") {
		return head
	}
	return ""
}
