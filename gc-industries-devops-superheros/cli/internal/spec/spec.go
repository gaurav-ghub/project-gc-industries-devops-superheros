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

// Service is one independently-deployable component of an application.
type Service struct {
	Name      string    `yaml:"name"`
	Image     string    `yaml:"image"`
	Tag       string    `yaml:"tag"`
	Port      int       `yaml:"port"`
	Replicas  int       `yaml:"replicas"`
	Resources Resources `yaml:"resources"`
	Security  Security  `yaml:"security"`
}

// Ref is the full image reference the chart will render for this service.
func (s Service) Ref() string {
	tag := s.Tag
	if tag == "" {
		tag = "latest"
	}
	return s.Image + ":" + tag
}

// ApplyDefaults fills in every field the platform is willing to decide on the
// developer's behalf. It is idempotent, and is applied on every write so that a
// registry entry written before these fields existed gains them on its next
// release rather than silently rendering a policy-violating manifest.
func (a *App) ApplyDefaults() {
	for i := range a.Services {
		s := &a.Services[i]
		if s.Tag == "" {
			s.Tag = "latest"
		}
		if s.Replicas == 0 {
			s.Replicas = 1
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

// App is a registered application: a namespace + owner + a set of services.
type App struct {
	Name       string    `yaml:"name"`
	Namespace  string    `yaml:"namespace"`
	Repository string    `yaml:"repository"`
	Owner      string    `yaml:"owner"`
	Services   []Service `yaml:"services"`
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
		if s.Replicas < 1 {
			return fmt.Errorf("service %q replicas must be >= 1", s.Name)
		}
		// An empty tag is allowed here — Generate defaults it to "latest".
		if s.Tag != "" {
			if err := ValidateTag(s.Tag); err != nil {
				return fmt.Errorf("service %q: %w", s.Name, err)
			}
		}
	}
	return nil
}
