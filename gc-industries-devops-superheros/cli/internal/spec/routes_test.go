package spec

import (
	"strings"
	"testing"
)

// multiRoutedApp is the shape the superhero application actually has: a browser
// SPA at the root and JSON APIs beneath it, each served by a different service.
func multiRoutedApp() App {
	return App{
		Name:      "superheros",
		Namespace: "superheros",
		Routes: []Route{
			{Path: "/", Service: "frontend"},
			{Path: "/api/catalog", Service: "catalog"},
			{Path: "/api/pay", Service: "payment", Rewrite: "/pay"},
		},
		Services: []Service{
			{Name: "frontend", Image: "docker.io/x/frontend", Tag: "v1", Port: 80, Replicas: 1},
			{Name: "catalog", Image: "docker.io/x/catalog", Tag: "v1", Port: 8081, Replicas: 1},
			{Name: "payment", Image: "docker.io/x/payment", Tag: "v1", Port: 8084, Replicas: 1},
		},
	}
}

// TestTheRootRouteIsSortedLast.
//
// The one that matters. Istio takes the first prefix that matches, so a `/` rule
// above `/api/catalog` swallows it — and the failure is invisible from the
// platform's side: a valid VirtualService, a Synced application, and an API that
// answers with the SPA's index.html. Authors write routes in the order they
// think of them, which is never the safe order, so the order is ours.
func TestTheRootRouteIsSortedLast(t *testing.T) {
	app := multiRoutedApp()
	app.ApplyDefaults()

	routes := app.RouteList()
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	if got := routes[len(routes)-1].Path; got != "/" {
		t.Errorf("last route is %q, want the root — everything below it is unreachable", got)
	}
	for i := 1; i < len(routes); i++ {
		if len(routes[i-1].Path) < len(routes[i].Path) {
			t.Errorf("route %q precedes the longer %q", routes[i-1].Path, routes[i].Path)
		}
	}

	// ApplyDefaults writes the sorted order back, so the generated values file a
	// reviewer reads is the order Istio will use.
	if app.Routes[len(app.Routes)-1].Path != "/" {
		t.Error("ApplyDefaults left the stored order unsorted")
	}
}

// TestEveryRouteInAListGetsTheDefaults — the port comes from the service named,
// for the fifth route exactly as for the first.
func TestEveryRouteInAListGetsTheDefaults(t *testing.T) {
	app := multiRoutedApp()
	app.ApplyDefaults()

	want := map[string]int{"/": 80, "/api/catalog": 8081, "/api/pay": 8084}
	for _, r := range app.RouteList() {
		if r.Port != want[r.Path] {
			t.Errorf("route %s port = %d, want %d", r.Path, r.Port, want[r.Path])
		}
		if r.Gateway != DefaultGateway {
			t.Errorf("route %s gateway = %q, want %q", r.Path, r.Gateway, DefaultGateway)
		}
		if !r.Enabled {
			t.Errorf("route %s is not enabled — being in the list is what publishes it", r.Path)
		}
	}
}

// TestApplyDefaultsIsIdempotentForARouteList — sorting runs on every write, so
// running it twice must not shuffle anything. Byte-identical regeneration is
// what proves a generated tree was not hand-edited.
func TestApplyDefaultsIsIdempotentForARouteList(t *testing.T) {
	app := multiRoutedApp()
	app.ApplyDefaults()
	first := app.RouteList()

	app.ApplyDefaults()
	second := app.RouteList()

	if len(first) != len(second) {
		t.Fatalf("route count changed: %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("route %d changed on the second pass: %+v then %+v", i, first[i], second[i])
		}
	}
}

// TestRouteAndRoutesTogetherAreRefused. Two spellings of one fact, and no way to
// tell which the author meant.
func TestRouteAndRoutesTogetherAreRefused(t *testing.T) {
	app := multiRoutedApp()
	app.Route = Route{Enabled: true, Path: "/", Service: "frontend"}

	err := app.Validate()
	if err == nil {
		t.Fatal("an app declaring both route: and routes: was accepted")
	}
	if !strings.Contains(err.Error(), "routes:") {
		t.Errorf("error does not name the conflict: %v", err)
	}
}

// TestTheSamePathTwiceIsRefused — Istio takes the first and ignores the second
// in silence, so the second service never sees a request and nothing says so.
func TestTheSamePathTwiceIsRefused(t *testing.T) {
	app := multiRoutedApp()
	app.Routes = append(app.Routes, Route{Path: "/api/catalog", Service: "payment"})

	err := app.Validate()
	if err == nil {
		t.Fatal("a duplicated route path was accepted")
	}
	if !strings.Contains(err.Error(), "/api/catalog") {
		t.Errorf("error does not name the path: %v", err)
	}
}

// TestARouteListIsCheckedEntryByEntry — the checks that guard a single route
// guard every entry, not just the first. A reserved path in position four is the
// one nobody would notice.
func TestARouteListIsCheckedEntryByEntry(t *testing.T) {
	cases := []struct {
		name  string
		route Route
		want  string
	}{
		{"unknown service", Route{Path: "/api/x", Service: "nosuch"}, "nosuch"},
		{"reserved path", Route{Path: "/grafana", Service: "catalog"}, "reserved"},
		{"relative path", Route{Path: "api/x", Service: "catalog"}, "must start with '/'"},
		{"rewrite without a slash", Route{Path: "/api/x", Service: "catalog", Rewrite: "pay"}, "rewrite"},
		{"no service", Route{Path: "/api/x"}, "no service is named"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := multiRoutedApp()
			app.Routes = append(app.Routes, c.route)

			err := app.Validate()
			if err == nil {
				t.Fatalf("%+v was accepted", c.route)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// TestRoutesShareOneGateway. One VirtualService carries all of an application's
// rules and binds to one set of gateways, so two entries naming different ones
// cannot both be honoured — and picking one silently would publish a path on a
// gateway nobody named.
func TestRoutesShareOneGateway(t *testing.T) {
	app := multiRoutedApp()
	app.Routes[1].Gateway = "istio-system/some-other-gateway"

	err := app.Validate()
	if err == nil {
		t.Fatal("routes naming two gateways were accepted")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error does not name the problem: %v", err)
	}
}

// TestTheURLOfAMultiRoutedAppIsItsFrontDoor.
//
// "The address" of an application serving a page at / and APIs beneath it is the
// one a person can open, not the longest prefix in the file.
func TestTheURLOfAMultiRoutedAppIsItsFrontDoor(t *testing.T) {
	app := multiRoutedApp()
	app.ApplyDefaults()

	if got := app.URL("http://localhost:8080"); got != "http://localhost:8080/" {
		t.Errorf("URL = %q, want the root", got)
	}

	// An application whose shallowest route is not the root is addressed there.
	sub := multiRoutedApp()
	sub.Routes = []Route{
		{Path: "/shop", Service: "frontend"},
		{Path: "/shop/api/catalog", Service: "catalog"},
	}
	sub.ApplyDefaults()
	if got := sub.URL("http://localhost:8080"); got != "http://localhost:8080/shop" {
		t.Errorf("URL = %q, want /shop", got)
	}
}

// TestASingleRouteStillWorks — `route:` is a `routes:` list of one, and the
// applications already written against it must not notice this change.
func TestASingleRouteStillWorks(t *testing.T) {
	app := routedApp()
	app.ApplyDefaults()

	routes := app.RouteList()
	if len(routes) != 1 {
		t.Fatalf("got %d routes, want 1", len(routes))
	}
	if routes[0].Service != "frontend" || routes[0].Port != 80 {
		t.Errorf("single route came through as %+v", routes[0])
	}
	if len(app.Routes) != 0 {
		t.Error("a single-route app grew a routes list — its values file would change shape")
	}
}

// TestAnApplicationWithNoRoutesHasNoList — nothing published, nothing to sort.
func TestAnApplicationWithNoRoutesHasNoList(t *testing.T) {
	app := routedApp()
	app.Route = Route{}
	app.ApplyDefaults()

	if got := app.RouteList(); len(got) != 0 {
		t.Errorf("RouteList = %+v, want none", got)
	}
	if got := app.URL("http://localhost:8080"); got != "" {
		t.Errorf("URL = %q, want empty", got)
	}
}
