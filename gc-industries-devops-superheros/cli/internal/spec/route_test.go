package spec

import (
	"strings"
	"testing"
)

func routedApp() App {
	return App{
		Name:      "superheros",
		Namespace: "superheros",
		Route:     Route{Enabled: true, Service: "frontend"},
		Services: []Service{
			{Name: "frontend", Image: "docker.io/x/frontend", Tag: "v1", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/x/catalog", Tag: "v1", Port: 8081, Replicas: 1},
		},
	}
}

// TestRouteTakesItsPortFromTheServiceItNames.
//
// A route that repeats the port its service already declares is two statements
// of one fact, and the second goes stale the day the service moves. So the port
// is read from the service, and `route.port` is a field almost nobody sets.
func TestRouteTakesItsPortFromTheServiceItNames(t *testing.T) {
	app := routedApp()
	app.ApplyDefaults()

	if app.Route.Port != 80 {
		t.Errorf("route port = %d, want frontend's 80", app.Route.Port)
	}
	if app.Route.Path != DefaultRoutePath {
		t.Errorf("route path = %q, want %q", app.Route.Path, DefaultRoutePath)
	}
	if app.Route.Gateway != DefaultGateway {
		t.Errorf("route gateway = %q, want the platform's %q", app.Route.Gateway, DefaultGateway)
	}

	// A route naming the other service takes the other service's port.
	other := routedApp()
	other.Route.Service = "catalog"
	other.ApplyDefaults()
	if other.Route.Port != 8081 {
		t.Errorf("route port = %d, want catalog's 8081", other.Route.Port)
	}

	// An explicit port is left alone: a service can serve its UI on a different
	// port from the one it registers.
	explicit := routedApp()
	explicit.Route.Port = 3000
	explicit.ApplyDefaults()
	if explicit.Route.Port != 3000 {
		t.Errorf("an explicit route port was overwritten with %d", explicit.Route.Port)
	}
}

// TestApplyDefaultsIsIdempotentForRoutes — every write applies the defaults
// again, so a second application of them must change nothing. This is what
// makes the byte-identical regeneration proof possible at all.
func TestApplyDefaultsIsIdempotentForRoutes(t *testing.T) {
	app := routedApp()
	app.ApplyDefaults()
	once := app.Route
	app.ApplyDefaults()
	if app.Route != once {
		t.Errorf("a second ApplyDefaults changed the route: %+v -> %+v", once, app.Route)
	}
}

// TestADisabledRouteKeepsNoFields.
//
// A registry entry that carries `enabled: false` next to a service and a path
// is a fossil of a route somebody turned off, and the next reader cannot tell
// whether the application is exposed. Blanking it means the file says one thing.
func TestADisabledRouteKeepsNoFields(t *testing.T) {
	app := routedApp()
	app.Route.Enabled = false
	app.ApplyDefaults()

	if app.Route != (Route{}) {
		t.Errorf("a disabled route kept fields: %+v", app.Route)
	}
	if app.URL("http://localhost:8080") != "" {
		t.Error("an application with no route reported a URL")
	}
}

// TestRouteRejectsWhatWouldSilentlyRouteNowhere.
//
// Every case here is accepted by Istio and produces a VirtualService that looks
// configured and serves 503 — or, for a reserved path, something stranger: the
// application and a platform dashboard both claim a prefix and which one wins
// depends on which VirtualService Istio created first. The failure then reads
// as "ArgoCD is down".
func TestRouteRejectsWhatWouldSilentlyRouteNowhere(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mutol func(*App)
		want  string
	}{
		{"no service named", func(a *App) { a.Route.Service = "" }, "which of"},
		{"a service that does not exist", func(a *App) { a.Route.Service = "nope" }, "does not declare"},
		{"a path that is not a path", func(a *App) { a.Route.Path = "superheros.local" }, "must start with '/'"},
		{"a reserved path", func(a *App) { a.Route.Path = "/grafana" }, "reserved"},
		{"under a reserved path", func(a *App) { a.Route.Path = "/argocd/apps" }, "reserved"},
		{"declared but switched off", func(a *App) { a.Route.Enabled = false }, "enabled is false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := routedApp()
			tc.mutol(&app)
			err := app.Validate()
			if err == nil {
				t.Fatalf("accepted a route that routes nowhere: %+v", app.Route)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not explain the problem (want %q): %v", tc.want, err)
			}
		})
	}
}

// A path that merely starts with the same letters as a reserved one is fine:
// /grafana-dashboards is not /grafana, and refusing it would be the check being
// clever rather than correct.
func TestRouteAllowsAPathThatOnlyLooksReserved(t *testing.T) {
	app := routedApp()
	app.Route.Path = "/grafana-clone"
	if err := app.Validate(); err != nil {
		t.Errorf("a path that is not reserved was refused: %v", err)
	}
}

func TestURLIsThePlatformsHostPlusThePath(t *testing.T) {
	app := routedApp()
	app.ApplyDefaults()
	if got := app.URL("http://localhost:8080"); got != "http://localhost:8080/" {
		t.Errorf("URL = %q", got)
	}

	app.Route.Path = "/shop"
	if got := app.URL("http://localhost:9999"); got != "http://localhost:9999/shop" {
		t.Errorf("URL = %q — the base address is the cluster's, not this package's", got)
	}
}

// An application with no route is the ordinary case and must stay valid — the
// platform is not an ingress controller, and most applications on it are
// something the frontend calls.
func TestAnApplicationNeedsNoRoute(t *testing.T) {
	app := routedApp()
	app.Route = Route{}
	if err := app.Validate(); err != nil {
		t.Fatalf("an application without a route was rejected: %v", err)
	}
	app.ApplyDefaults()
	if app.Route.Gateway != "" {
		t.Error("an application that asked for no route was given a gateway")
	}
}
