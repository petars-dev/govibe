package codegraph

import (
	"testing"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// Who a route belongs to: the framework when it registered it before the app's Routes
// ran or when it sits in a reserved namespace; the app otherwise, and also under
// /dev/app, where the framework mounts the pages the app adds through App.DevTools.
func TestRouteOriginAndTheAppsDevPages(t *testing.T) {
	facts := BuildFacts{FrameworkRoutes: map[string]bool{"GET /assets/*": true}}
	cases := map[string]string{
		"/assets/*":         OriginFramework,
		"/dev/graph":        OriginFramework,
		"/dev":              OriginFramework,
		"/_govibe/static/*": OriginFramework,
		"/api/hello":        OriginApp,
		"/dev/app":          OriginApp,
		"/dev/app/orders":   OriginApp,
		"/dev/application":  OriginFramework, // a prefix match, not a word match
	}
	for path, want := range cases {
		if got := routeOrigin(facts, routeinfo.Route{Method: "GET", Path: path}); got != want {
			t.Errorf("GET %s: origin %s, want %s", path, got, want)
		}
	}
}
