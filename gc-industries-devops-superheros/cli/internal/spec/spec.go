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

// Service is one independently-deployable component of an application.
type Service struct {
	Name     string `yaml:"name"`
	Image    string `yaml:"image"`
	Tag      string `yaml:"tag"`
	Port     int    `yaml:"port"`
	Replicas int    `yaml:"replicas"`
}

// App is a registered application: a namespace + owner + a set of services.
type App struct {
	Name       string    `yaml:"name"`
	Namespace  string    `yaml:"namespace"`
	Repository string    `yaml:"repository"`
	Owner      string    `yaml:"owner"`
	Services   []Service `yaml:"services"`
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
	}
	return nil
}
