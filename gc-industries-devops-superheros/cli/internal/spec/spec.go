// Package spec defines the Endurance application model.
//
// The core idea of the platform: an *application* is a set of one or more
// *services*, each with its own container image. A single-image app is simply
// the N=1 case of a multi-service app. This is what makes Endurance
// application-agnostic — the platform never needs to know a service's language.
package spec

import (
	"fmt"
	"regexp"
	"sort"
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

// Security is Endurance's single per-service security block. The chart splits it
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
// the weight into the pod spec — as the pre-Endurance superhero chart did —
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
// what Endurance produced before canary existed.
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
	a.applyRouteDefaults()
}

// applyRouteDefaults fills in the parts of a route an author should not have to
// write: where the platform's Gateway is, that a route with no path means the
// root, and which port the named service listens on.
//
// The port matters most. A route that names a service and repeats its port is
// two statements of one fact, and the second one goes stale the day the service
// moves. Reading it from the service is why `route.port` is almost never set by
// hand.
func (a *App) applyRouteDefaults() {
	if len(a.Routes) > 0 {
		a.Routes = a.RouteList() // sorted once, here, so the file records the order
		for i := range a.Routes {
			// An entry in a list is published by being in the list. Setting the
			// flag rather than teaching the chart about two shapes keeps
			// route.yaml a single loop.
			a.Routes[i].Enabled = true
			a.fillRoute(&a.Routes[i])
		}
		a.Route = Route{}
		return
	}
	if !a.Route.Enabled {
		// A disabled route keeps no fields at all, so a file that turned one
		// off does not carry the fossil of the one it used to have.
		a.Route = Route{}
		return
	}
	a.fillRoute(&a.Route)
}

// fillRoute completes one route: the platform's Gateway, the root path, and the
// port of the service it names.
func (a *App) fillRoute(r *Route) {
	if r.Gateway == "" {
		r.Gateway = DefaultGateway
	}
	if r.Path == "" {
		r.Path = DefaultRoutePath
	}
	if r.Port == 0 {
		if i := a.FindService(r.Service); i >= 0 {
			r.Port = a.Services[i].Port
		}
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

// DefaultGateway is the platform's one ingress Gateway, in the form Istio wants
// a cross-namespace reference: <namespace>/<name>.
//
// It is written into an application's values rather than hardcoded in the
// chart, so the chart carries no platform knowledge and the reference is
// visible in the generated file a reviewer reads. The same string is spelled in
// platform/access/manifests/gateway.yaml and in cli/internal/platform; tests
// read the manifest and fail if the three drift.
const DefaultGateway = "istio-system/endurance-gateway"

// DefaultRoutePath is where an application lands when it asks for a route and
// says nothing about where: the root of the platform's one host.
const DefaultRoutePath = "/"

// Route is an application's request for a URL.
//
// # Why this lives in the application's spec and not in the platform
//
// The platform owns the front door — one Istio Gateway, one host, path-based —
// and it deliberately knows nothing about what is behind any given path. The
// pre-Endurance superhero chart had `/api/catalog` baked into a gateway route,
// which meant the platform could only ever serve that one application. So an
// application that wants an address says so here, in its own spec, and
// charts/app renders a VirtualService bound to the platform's Gateway. The
// platform provides the door; the application says which room it is.
//
// # It is opt-in
//
// Most services in a multi-service application are internal — catalog, orders
// and payment are called by the frontend and by nothing outside the cluster.
// Exposing all of them would be a security decision made by omission. Every
// route is written down by name, and nothing is published that was not.
//
// # One route, or several
//
// Until v0.10.3 an application could have exactly one, and that was wrong for a
// reason worth recording. The superhero application it was modelled on serves a
// browser SPA at `/` and four JSON APIs at `/api/catalog`, `/api/orders`,
// `/api/inventory` and `/api/payment`; before Endurance those five lived in the
// application's own hand-written gateway route. Onboarding it through Endurance
// generated the one rule the model allowed — `/` to the frontend — and the four
// APIs stopped existing. The frontend loaded, the catalog page hung, and
// nothing in the platform said why, because from the platform's side a
// perfectly valid VirtualService had been created exactly as asked.
//
// So an application declares a list, in App.Routes. The single App.Route stays
// for the applications that have one and the `init` flow that writes them, and
// the two are mutually exclusive — see [App.RouteList].
type Route struct {
	// Enabled is the single-route form's on switch. Inside a Routes list it is
	// meaningless — an entry is there or it is not — and ApplyDefaults sets it
	// so the two forms render through one code path in the chart.
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path,omitempty"`
	Service string `yaml:"service,omitempty"`
	// Rewrite replaces the matched prefix before the request is forwarded, for
	// the common case of a service that was written without knowing what it
	// would be mounted under. superheros' payment service serves POST /pay and
	// its browser calls POST /api/pay; without this the platform's only options
	// are to change the service, change the caller, or route nothing.
	Rewrite string `yaml:"rewrite,omitempty"`
	// Port is the Service port traffic is sent to. Left empty it is filled in
	// from the named service, which is what anyone would mean.
	Port int `yaml:"port,omitempty"`
	// Gateway is the platform Gateway to bind to. Set by ApplyDefaults, and
	// overridable only because a platform that hardcodes its own name into an
	// application's file is a platform that cannot be renamed.
	Gateway string `yaml:"gateway,omitempty"`
}

// App is a registered application: a namespace + owner + a set of services.
type App struct {
	Name       string    `yaml:"name"`
	Namespace  string    `yaml:"namespace"`
	Repository string    `yaml:"repository"`
	Owner      string    `yaml:"owner"`
	Mesh       Mesh      `yaml:"mesh,omitempty"`
	Route      Route     `yaml:"route,omitempty"`
	Routes     []Route   `yaml:"routes,omitempty"`
	Notify     Notify    `yaml:"notify,omitempty"`
	Services   []Service `yaml:"services"`
}

// RouteList is every route this application publishes, most specific first.
//
// One accessor for both spellings, so nothing downstream has to ask which form
// the author used. `route:` is the same thing as a `routes:` list of one, and
// Validate refuses a file that writes both — an application with two answers to
// "where do I answer" is a file nobody can review.
//
// # The order is ours, and it matters
//
// Istio evaluates a VirtualService's http rules top to bottom and takes the
// first prefix that matches, so a `/` rule written above `/api/catalog` silently
// swallows it — the API returns the SPA's index.html, and the browser reports a
// JSON parse error somewhere far away from the cause. Authors write routes in
// the order they think of them, which is never that order, so the list is sorted
// here: longest path first, and ties left in the order they were written. The
// sorted list is what gets written into the generated values file, so the order
// a reviewer reads is the order Istio will use.
func (a App) RouteList() []Route {
	var out []Route
	if len(a.Routes) > 0 {
		out = append(out, a.Routes...)
	} else if a.Route.Enabled {
		out = append(out, a.Route)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].Path) > len(out[j].Path)
	})
	return out
}

// URL is the address this application answers on, given the platform's base
// address, or "" when it asked for no route.
//
// base is passed in rather than known here: which host port the platform is
// published on is a fact about the cluster (kind-config.yaml), and the spec
// package must not grow an opinion about it.
func (a App) URL(base string) string {
	routes := a.RouteList()
	if len(routes) == 0 {
		return ""
	}
	// The front door is the least specific route: for an application serving a
	// browser at `/` and four APIs beneath it, "the address" is the one a person
	// can open. RouteList sorts longest-first, so that is the last of them.
	path := routes[len(routes)-1].Path
	if path == "" || path == DefaultRoutePath {
		return base + "/"
	}
	return base + path
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
	if err := validateNotify(a.Notify); err != nil {
		return err
	}
	if err := a.validateRoute(); err != nil {
		return err
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

// validateRoute checks an application's request for a URL.
//
// The rejections are all of one kind: a route that is declared, looks
// configured, and silently routes nowhere. Istio accepts a VirtualService
// pointing at a service that does not exist — it simply returns 503 to every
// request — so the check has to happen before the file is written, not when
// somebody opens a browser.
func (a App) validateRoute() error {
	if len(a.Routes) > 0 {
		if a.Route.Enabled || a.Route.Service != "" || a.Route.Path != "" {
			return fmt.Errorf("app %q declares both `route:` and `routes:` — "+
				"they are the same thing and only one can be the answer; "+
				"move the single route into the list", a.Name)
		}
		seen := make(map[string]bool, len(a.Routes))
		for _, r := range a.Routes {
			path := r.Path
			if path == "" {
				path = DefaultRoutePath
			}
			if seen[path] {
				// Istio takes the first and ignores the second, so the second
				// service never sees a request and nothing anywhere says so.
				return fmt.Errorf("app %q routes %q twice — one path answers from one service", a.Name, path)
			}
			seen[path] = true
			// One VirtualService carries all of an application's rules, and a
			// VirtualService binds to one set of gateways. Two entries naming
			// different ones cannot both be honoured, and picking one silently
			// would publish a path on a gateway its author did not name.
			if r.Gateway != "" && r.Gateway != a.Routes[0].Gateway {
				return fmt.Errorf("app %q routes %q through gateway %q and %q through %q — "+
					"an application's routes share one gateway",
					a.Name, path, r.Gateway, orRoot(a.Routes[0].Path), a.Routes[0].Gateway)
			}
			// Being in the list is what publishes an entry, and Validate must
			// give the same answer whether or not ApplyDefaults has run.
			r.Enabled = true
			if err := a.validateOneRoute(r); err != nil {
				return err
			}
		}
		return nil
	}
	return a.validateOneRoute(a.Route)
}

// orRoot names a path for an error message, including the one nobody wrote down.
func orRoot(path string) string {
	if path == "" {
		return DefaultRoutePath
	}
	return path
}

// validateOneRoute checks a single route, whether it came from `route:` or from
// one entry of `routes:`.
func (a App) validateOneRoute(r Route) error {
	if !r.Enabled {
		// A disabled route with fields set is a route somebody meant to turn
		// on. Saying so beats deploying an application with no address and no
		// explanation.
		if r.Service != "" || r.Path != "" {
			return fmt.Errorf("route declares service/path but enabled is false — "+
				"set `enabled: true` to publish %s, or delete the block", a.Name)
		}
		return nil
	}
	if r.Service == "" {
		return fmt.Errorf("route.enabled is true but no service is named — "+
			"which of %s should answer the URL?", strings.Join(a.ServiceNames(), ", "))
	}
	if a.FindService(r.Service) < 0 {
		return fmt.Errorf("route names service %q, which app %q does not declare — services are: %s",
			r.Service, a.Name, strings.Join(a.ServiceNames(), ", "))
	}
	if r.Path != "" && !strings.HasPrefix(r.Path, "/") {
		return fmt.Errorf("route path %q must start with '/' — it is a path on the platform's host, not a hostname", r.Path)
	}
	// The platform's own dashboards live under these. An application that
	// claimed one would either be shadowed by them or shadow them, depending on
	// which VirtualService Istio happened to create first — and the failure
	// would look like "ArgoCD is down".
	for _, reserved := range ReservedPaths {
		if r.Path == reserved || strings.HasPrefix(r.Path, reserved+"/") {
			return fmt.Errorf("route path %q is reserved for the platform's own dashboards "+
				"(%s) — pick another path", r.Path, strings.Join(ReservedPaths, ", "))
		}
	}
	if r.Rewrite != "" && !strings.HasPrefix(r.Rewrite, "/") {
		return fmt.Errorf("route rewrite %q must start with '/' — it is the path the service will see", r.Rewrite)
	}
	if r.Port < 0 || r.Port > 65535 {
		return fmt.Errorf("route port %d out of range", r.Port)
	}
	return nil
}

// ReservedPaths are the prefixes the platform routes to its own dashboards. An
// application may not take one.
//
// Duplicated from the platform package on purpose: spec must not import it —
// the dependency runs the other way, and an application model that knows about
// bootstrap is an application model that cannot be split out in Phase 13. A
// test in the platform package compares the two lists.
var ReservedPaths = []string{"/argocd", "/kiali", "/grafana", "/prometheus", "/alertmanager"}

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
