// Package spec defines the LaunchPad application model.
//
// The core idea of the platform: an *application* is a set of one or more
// *services*, each with its own container image. A single-image app is simply
// the N=1 case of a multi-service app. This is what makes LaunchPad
// application-agnostic — the platform never needs to know a service's language.
package spec

import (
	"fmt"
	"regexp"
	"strings"
)

// dns1123 matches a Kubernetes-safe name (lowercase alphanumerics and '-').
var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// dockerTag matches an OCI image tag: an alphanumeric or '_' first character,
// then up to 127 more of alphanumerics, '.', '_' or '-'.
var dockerTag = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

// ValidateTag reports whether tag is a usable OCI image tag. Release rejects a
// malformed tag before touching any file, so a typo can never reach a commit
// and become an ImagePullBackOff that ArgoCD faithfully reconciles.
func ValidateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("tag is required")
	}
	if !dockerTag.MatchString(tag) {
		return fmt.Errorf("tag %q is not a valid image tag (alphanumerics, '.', '_', '-'; max 128 chars)", tag)
	}
	return nil
}

// Platform defaults materialized into every service that does not set its own.
//
// These are deliberately written into apps/<name>/values.yaml rather than left
// implicit in the chart: the policy gate validates what is actually declared,
// and a reviewer reading a pull request can see the security posture in the
// diff instead of having to know the chart's fallbacks.
const (
	DefaultRunAsUser  int64 = 10001
	DefaultRunAsGroup int64 = 10001
	DefaultFSGroup    int64 = 10001

	DefaultRequestsCPU    = "50m"
	DefaultRequestsMemory = "64Mi"
	DefaultLimitsCPU      = "500m"
	DefaultLimitsMemory   = "512Mi"
)

// ResourceList is one half of a container's resource block.
type ResourceList struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// Resources mirrors the Kubernetes container resources shape so the chart can
// pass it straight through with toYaml.
type Resources struct {
	Requests ResourceList `yaml:"requests"`
	Limits   ResourceList `yaml:"limits"`
}

// Security is LaunchPad's single per-service security block. The chart splits it
// into the two places Kubernetes wants it: the pod-level securityContext
// (runAsNonRoot / runAsUser / runAsGroup / fsGroup) and the container-level one
// (allowPrivilegeEscalation / capabilities).
//
// RunAsNonRoot and AllowPrivilegeEscalation are platform-mandated rather than
// per-app knobs — ApplyDefaults always sets them, because an app that needs root
// is a policy-exception conversation, not a values field.
type Security struct {
	RunAsNonRoot             bool     `yaml:"runAsNonRoot"`
	RunAsUser                int64    `yaml:"runAsUser"`
	RunAsGroup               int64    `yaml:"runAsGroup"`
	FSGroup                  int64    `yaml:"fsGroup"`
	AllowPrivilegeEscalation bool     `yaml:"allowPrivilegeEscalation"`
	DropCapabilities         []string `yaml:"dropCapabilities"`
}

// EnvVar is a static environment variable set on a service's container.
//
// Deliberately static only — no downward API, no secret refs. The one thing a
// canary demo needs is for each version to be able to say which version it is,
// and anything richer belongs to the application's own configuration story.
type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// Version is one deployed variant of a service, and the unit Istio splits
// traffic between.
//
// A service either has no versions — the ordinary case, one Deployment named
// after the service — or two or more, in which case it renders one Deployment
// per version (`<service>-<version>`) behind a single Service, with a
// DestinationRule subset and a VirtualService route weight per version.
//
// Weight is the *only* place a version's share of traffic is recorded. It is
// deliberately not passed to the container as an environment variable: baking
// the weight into the pod spec — as the pre-LaunchPad superhero chart did —
// means every weight change restarts every pod, which defeats the entire point
// of shifting traffic at the mesh layer.
type Version struct {
	Name     string   `yaml:"name"`
	Tag      string   `yaml:"tag"`
	Weight   int      `yaml:"weight"`
	Replicas int      `yaml:"replicas"`
	Env      []EnvVar `yaml:"env,omitempty"`
}

// Service is one independently-deployable component of an application.
type Service struct {
	Name      string    `yaml:"name"`
	Image     string    `yaml:"image"`
	Tag       string    `yaml:"tag,omitempty"`
	Port      int       `yaml:"port"`
	Replicas  int       `yaml:"replicas,omitempty"`
	Resources Resources `yaml:"resources"`
	Security  Security  `yaml:"security"`
	Env       []EnvVar  `yaml:"env,omitempty"`
	Versions  []Version `yaml:"versions,omitempty"`
}

// IsCanary reports whether this service is split across weighted versions.
func (s Service) IsCanary() bool { return len(s.Versions) > 0 }

// Ref is the full image reference the chart will render for this service.
// Only meaningful for a service with no versions; a canary service has one
// reference per version (see VersionRef).
func (s Service) Ref() string {
	tag := s.Tag
	if tag == "" {
		tag = "latest"
	}
	return s.Image + ":" + tag
}

// VersionRef is the image reference for one version of a canary service. Every
// version shares the service's image repository and differs only by tag — a
// canary is the same program at two points in its history, not two programs.
func (s Service) VersionRef(v Version) string {
	tag := v.Tag
	if tag == "" {
		tag = "latest"
	}
	return s.Image + ":" + tag
}

// WorkloadName is the Deployment name for a version of this service: the bare
// service name when there are no versions, so a service that never adopts
// canary renders exactly as it always has.
func (s Service) WorkloadName(v Version) string {
	if v.Name == "" {
		return s.Name
	}
	return s.Name + "-" + v.Name
}

// EnvFor returns the environment a version's container runs with. A version's
// env replaces the service's wholesale rather than merging with it, matching
// how the chart already treats resources and security — one place to look, no
// per-key precedence to remember.
func (s Service) EnvFor(v Version) []EnvVar {
	if len(v.Env) > 0 {
		return v.Env
	}
	return s.Env
}

// Rollout is the list of variants to render for a service. A service without
// versions yields exactly one unnamed variant carrying the service's own tag
// and replicas, which is what keeps the non-canary render byte-identical to
// what LaunchPad produced before canary existed.
func (s Service) Rollout() []Version {
	if len(s.Versions) > 0 {
		return s.Versions
	}
	return []Version{{Tag: s.Tag, Replicas: s.Replicas, Env: s.Env}}
}

// FindVersion returns the index of the named version, or -1.
func (s Service) FindVersion(name string) int {
	for i, v := range s.Versions {
		if v.Name == name {
			return i
		}
	}
	return -1
}

// VersionNames lists the version names in declaration order, for error messages
// that tell a developer what they could have typed.
func (s Service) VersionNames() []string {
	names := make([]string, 0, len(s.Versions))
	for _, v := range s.Versions {
		names = append(names, v.Name)
	}
	return names
}

// ApplyDefaults fills in every field the platform is willing to decide on the
// developer's behalf. It is idempotent, and is applied on every write so that a
// registry entry written before these fields existed gains them on its next
// release rather than silently rendering a policy-violating manifest.
func (a *App) ApplyDefaults() {
	for i := range a.Services {
		s := &a.Services[i]
		if s.IsCanary() {
			// A canary service's tag and replicas live on its versions. Blanking
			// them here — rather than leaving a stale value behind — is what stops
			// the registry entry from claiming a tag that nothing is running.
			s.Tag, s.Replicas = "", 0
			for j := range s.Versions {
				v := &s.Versions[j]
				if v.Tag == "" {
					v.Tag = "latest"
				}
				if v.Replicas == 0 {
					v.Replicas = 1
				}
			}
		} else {
			if s.Tag == "" {
				s.Tag = "latest"
			}
			if s.Replicas == 0 {
				s.Replicas = 1
			}
		}
		if s.Resources.Requests.CPU == "" {
			s.Resources.Requests.CPU = DefaultRequestsCPU
		}
		if s.Resources.Requests.Memory == "" {
			s.Resources.Requests.Memory = DefaultRequestsMemory
		}
		if s.Resources.Limits.CPU == "" {
			s.Resources.Limits.CPU = DefaultLimitsCPU
		}
		if s.Resources.Limits.Memory == "" {
			s.Resources.Limits.Memory = DefaultLimitsMemory
		}
		if s.Security.RunAsUser == 0 {
			s.Security.RunAsUser = DefaultRunAsUser
		}
		if s.Security.RunAsGroup == 0 {
			s.Security.RunAsGroup = DefaultRunAsGroup
		}
		if s.Security.FSGroup == 0 {
			s.Security.FSGroup = DefaultFSGroup
		}
		if len(s.Security.DropCapabilities) == 0 {
			s.Security.DropCapabilities = []string{"ALL"}
		}
		// Platform-mandated, never inherited from the input.
		s.Security.RunAsNonRoot = true
		s.Security.AllowPrivilegeEscalation = false
	}
}

// Mesh is the application's opt-in to Istio.
//
// It is opt-in rather than always-on because turning it on is not free: every
// pod gains a sidecar and therefore rolls, and an application that declares no
// versions gains nothing in return. Weighted canary routing is the capability
// that needs it, so the application that wants canary is the application that
// asks for the mesh.
type Mesh struct {
	Enabled bool `yaml:"enabled"`
}

// App is a registered application: a namespace + owner + a set of services.
type App struct {
	Name       string    `yaml:"name"`
	Namespace  string    `yaml:"namespace"`
	Repository string    `yaml:"repository"`
	Owner      string    `yaml:"owner"`
	Mesh       Mesh      `yaml:"mesh,omitempty"`
	Services   []Service `yaml:"services"`
}

// CanaryServices lists the services split across weighted versions.
func (a App) CanaryServices() []string {
	var out []string
	for _, s := range a.Services {
		if s.IsCanary() {
			out = append(out, s.Name)
		}
	}
	return out
}

// MeshWarnings reports configurations that are valid but will not do what the
// developer meant.
//
// Declaring weights without the mesh is the one that matters: the Deployments
// come up, the app looks healthy, and kube-proxy load-balances evenly across
// every version's pods — so a 90/10 split silently behaves as 50/50. Nothing
// rejects it, which is exactly why the CLI has to say it out loud.
func (a App) MeshWarnings() []string {
	var out []string
	if canaries := a.CanaryServices(); len(canaries) > 0 && !a.Mesh.Enabled {
		out = append(out, fmt.Sprintf(
			"service(s) %s declare weighted versions but mesh.enabled is false — "+
				"without Istio the weights are inert and traffic is split evenly by kube-proxy",
			strings.Join(canaries, ", ")))
	}
	return out
}

// SetWeights replaces the traffic weights of one canary service. The new set
// must name every declared version and sum to 100, so a weight change is always
// a complete statement of where traffic goes rather than a partial edit whose
// effect depends on what was there before.
func (a *App) SetWeights(svcName string, weights map[string]int) error {
	i := a.FindService(svcName)
	if i < 0 {
		return fmt.Errorf("app %q has no service %q — services are: %s",
			a.Name, svcName, strings.Join(a.ServiceNames(), ", "))
	}
	s := &a.Services[i]
	if !s.IsCanary() {
		return fmt.Errorf("service %q declares no versions — nothing to split traffic between", svcName)
	}
	for name := range weights {
		if s.FindVersion(name) < 0 {
			return fmt.Errorf("service %q has no version %q — versions are: %s",
				svcName, name, strings.Join(s.VersionNames(), ", "))
		}
	}
	total := 0
	for _, v := range s.Versions {
		w, ok := weights[v.Name]
		if !ok {
			return fmt.Errorf("no weight given for version %q — every version must be named, and the weights must sum to 100 (versions are: %s)",
				v.Name, strings.Join(s.VersionNames(), ", "))
		}
		total += w
	}
	if total != 100 {
		return fmt.Errorf("weights sum to %d, want 100", total)
	}
	for j := range s.Versions {
		s.Versions[j].Weight = weights[s.Versions[j].Name]
	}
	return nil
}

// FindService returns the index of the named service, or -1 if the application
// does not declare it.
func (a App) FindService(name string) int {
	for i, s := range a.Services {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// ServiceNames lists the service names in declaration order, for error messages
// that tell a developer what they *could* have typed.
func (a App) ServiceNames() []string {
	names := make([]string, 0, len(a.Services))
	for _, s := range a.Services {
		names = append(names, s.Name)
	}
	return names
}

// Validate checks the app is internally consistent and Kubernetes-safe.
func (a App) Validate() error {
	if !dns1123.MatchString(a.Name) {
		return fmt.Errorf("app name %q must be a DNS-1123 label (lowercase alphanumerics and '-')", a.Name)
	}
	if !dns1123.MatchString(a.Namespace) {
		return fmt.Errorf("namespace %q must be a DNS-1123 label", a.Namespace)
	}
	if len(a.Services) == 0 {
		return fmt.Errorf("app %q must declare at least one service", a.Name)
	}
	seen := map[string]bool{}
	for _, s := range a.Services {
		if !dns1123.MatchString(s.Name) {
			return fmt.Errorf("service name %q must be a DNS-1123 label", s.Name)
		}
		if seen[s.Name] {
			return fmt.Errorf("duplicate service name %q", s.Name)
		}
		seen[s.Name] = true
		if s.Image == "" {
			return fmt.Errorf("service %q must have an image", s.Name)
		}
		if s.Port < 1 || s.Port > 65535 {
			return fmt.Errorf("service %q port %d out of range", s.Name, s.Port)
		}
		// An empty tag is allowed here — ApplyDefaults defaults it to "latest".
		if s.Tag != "" {
			if err := ValidateTag(s.Tag); err != nil {
				return fmt.Errorf("service %q: %w", s.Name, err)
			}
		}
		if !s.IsCanary() {
			if s.Replicas < 0 {
				return fmt.Errorf("service %q replicas must be >= 1", s.Name)
			}
			continue
		}
		if err := validateVersions(s); err != nil {
			return err
		}
	}
	return nil
}

// validateVersions checks a canary service.
//
// The top-level tag and replicas are rejected rather than ignored: a registry
// entry that lists both `tag: v3` and three weighted versions gives two
// different answers to "what is running", and only one of them is true.
func validateVersions(s Service) error {
	if s.Tag != "" {
		return fmt.Errorf("service %q declares versions, so its top-level tag %q is unused — set the tag on each version instead",
			s.Name, s.Tag)
	}
	if s.Replicas != 0 {
		return fmt.Errorf("service %q declares versions, so its top-level replicas is unused — set replicas on each version instead",
			s.Name)
	}
	seen := map[string]bool{}
	total := 0
	for _, v := range s.Versions {
		if !dns1123.MatchString(v.Name) {
			return fmt.Errorf("service %q: version name %q must be a DNS-1123 label (it becomes an Istio subset and a Deployment name suffix)",
				s.Name, v.Name)
		}
		if seen[v.Name] {
			return fmt.Errorf("service %q: duplicate version %q", s.Name, v.Name)
		}
		seen[v.Name] = true
		if v.Tag != "" {
			if err := ValidateTag(v.Tag); err != nil {
				return fmt.Errorf("service %q version %q: %w", s.Name, v.Name, err)
			}
		}
		if v.Replicas < 0 {
			return fmt.Errorf("service %q version %q: replicas must be >= 1", s.Name, v.Name)
		}
		if v.Weight < 0 || v.Weight > 100 {
			return fmt.Errorf("service %q version %q: weight %d must be between 0 and 100", s.Name, v.Name, v.Weight)
		}
		total += v.Weight
	}
	// Istio requires the weights of a single route to sum to 100. Rejecting it
	// here rather than letting the cluster do it means a developer learns from
	// their own terminal instead of from a VirtualService that silently never
	// becomes ready.
	if total != 100 {
		parts := make([]string, 0, len(s.Versions))
		for _, v := range s.Versions {
			parts = append(parts, fmt.Sprintf("%s=%d", v.Name, v.Weight))
		}
		return fmt.Errorf("service %q: traffic weights sum to %d, want 100 (%s)",
			s.Name, total, strings.Join(parts, ", "))
	}
	return nil
}
