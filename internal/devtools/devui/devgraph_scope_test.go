package devui

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The graph page is about the app by default; the framework is one switch away, and the
// page, its data, its reports and its links all say which scope they are in.

// withRoutes is the fixture seen by a running app: its routes, one of them under /dev, which
// is the framework's by its path.
func withRoutes() codegraph.Options {
	h := routeinfo.Ref{Symbol: "example.com/fixture/controllers.HelloAPI", File: "controllers/hello.go", Line: 9}
	return codegraph.Options{Root: codegraphFixture, RouteSource: func() ([]routeinfo.Route, error) {
		return []routeinfo.Route{
			{Method: "GET", Path: "/api/hello", Handler: h},
			{Method: "ANY", Path: "/dev/mcp", Handler: routeinfo.Ref{Symbol: "example.com/fixture/devmcp.NewHandler"}},
		}, nil
	}}
}

func TestEveryGraphEndpointTakesTheScope(t *testing.T) {
	h, _, _ := dataRouterWith(t, withRoutes(), false)

	// the default is the app's own: the fixture has no framework nodes, so nothing is hidden,
	// but the page says which scope it is in
	st := getJSON(t, h, "/dev/graph/data/status", 200)
	if st["scope"] != "app" {
		t.Errorf("default scope = %v", st["scope"])
	}
	for _, scope := range []string{"app", "framework", "all"} {
		st := getJSON(t, h, "/dev/graph/data/status?scope="+scope, 200)
		hidden, _ := st["hidden"].(map[string]any)
		if st["scope"] != scope || hidden["scope"] != scope {
			t.Errorf("status?scope=%s: %v %v", scope, st["scope"], hidden)
		}
		v := getJSON(t, h, "/dev/graph/data/view?scope="+scope, 200)
		if v["scope"] != scope {
			t.Errorf("view?scope=%s: %v", scope, v["scope"])
		}
		for _, path := range []string{"search?q=orders&", "findings?", "table?name=orders&"} {
			getJSON(t, h, "/dev/graph/data/"+path+"scope="+scope, 200)
		}
	}

	// a node of the framework (the fixture's /dev/mcp route, by its path) is hidden in the app's scope ...
	const route = "route:ANY:/dev/mcp"
	rec := getJSON(t, h, "/dev/graph/data/node?id="+route, 400)
	if msg, _ := rec["error"].(string); !strings.Contains(msg, "scope app") || !strings.Contains(msg, "scope=all") {
		t.Errorf("a hidden node is said to exist in another scope: %q", msg)
	}
	// ... and there, labelled, in scope all
	node := getJSON(t, h, "/dev/graph/data/node?scope=all&id="+route, 200)
	if n, _ := node["node"].(map[string]any); n["origin"] != "framework" {
		t.Errorf("the route is labelled framework: %v", node["node"])
	}
	v := getJSON(t, h, "/dev/graph/data/view?scope=all&preset=app_map", 200)
	if n := ids(v["nodes"])[route]; n == nil || n["origin"] != "framework" {
		t.Errorf("the app map in scope all draws the framework's route, labelled: %v", n)
	}
	if n := ids(getJSON(t, h, "/dev/graph/data/view?preset=app_map", 200)["nodes"])[route]; n != nil {
		t.Errorf("the app map in the app's scope draws a /dev route: %v", n)
	}

	// a scope that does not exist is a 400 that says what is valid
	bad := getJSON(t, h, "/dev/graph/data/view?scope=everything", 400)
	if msg, _ := bad["error"].(string); !strings.Contains(msg, "app, framework or all") {
		t.Errorf("bad scope: %q", msg)
	}
	getJSON(t, h, "/dev/graph/data/status?scope=nope", 400)
}

func TestTheGraphPageHasTheScopeSwitch(t *testing.T) {
	h, _, _ := dataRouter(t, codegraphFixture, false)
	_, body, _ := get(t, h, "/dev/graph")
	for _, want := range []string{`id="gv-scope"`, `role="radiogroup"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	// the script reads and writes the scope: the URL carries scope=, the choice is remembered, every call passes it
	js, err := fs.ReadFile(Static(), "graph.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"URLSearchParams(location.search).get('scope')", "gv.scope", "q.set('scope', S.scope)", "preset: 'app_map'", "'double'"} {
		if !strings.Contains(string(js), want) {
			t.Errorf("graph.js lacks %q", want)
		}
	}
}

func TestTheConfigReportPointsToTheFrameworksSettings(t *testing.T) {
	h, _ := reportsRouter(t, thisRepo, codegraph.Options{Router: thisRepoRouter()}, true)
	_, app, _ := get(t, h, "/dev/graph/reports/config")
	if !strings.Contains(app, `href="/dev/docs/govibe/configuration"`) || !strings.Contains(app, "switch the scope above") {
		t.Errorf("the app's config report does not point at GoVibe: Configuration:\n%.800s", mainOf(app))
	}
	_, all, _ := get(t, h, "/dev/graph/reports/config?scope=all")
	if !strings.Contains(all, `href="/dev/docs/govibe/configuration"`) || strings.Contains(all, "switch the scope above") {
		t.Error("scope all lists them already: the link stays, the advice goes")
	}
	_, fw, _ := get(t, h, "/dev/graph/reports/config?scope=framework")
	if strings.Contains(fw, "This lists your app's settings") {
		t.Error("the framework scope is the framework's settings")
	}
}

func TestReportsShowTheScopeAndCarryItInTheirLinks(t *testing.T) {
	h, _ := reportsRouter(t, thisRepo, codegraph.Options{Router: thisRepoRouter()}, true)

	_, app, _ := get(t, h, "/dev/graph/reports/routes")
	if !strings.Contains(app, `Showing <strong>My app</strong>`) || !strings.Contains(app, "hides 2 framework nodes") ||
		!strings.Contains(app, `href="/dev/graph/reports/routes?scope=all"`) || !strings.Contains(app, `aria-current="true">My app`) {
		t.Errorf("the strip of the app's report:\n%.900s", mainOf(app))
	}
	if strings.Contains(app, "htmx.min.js") {
		t.Error("the framework's route is in the app's own routes report")
	}

	_, all, _ := get(t, h, "/dev/graph/reports/routes?scope=all")
	if !strings.Contains(all, `Showing <strong>All</strong>`) || strings.Contains(all, "hides ") {
		t.Errorf("the strip of the report on everything:\n%.700s", mainOf(all))
	}
	// a link into the graph from a report that shows the framework's nodes opens the graph in that scope
	if !strings.Contains(all, `href="/dev/graph?scope=all#node=`) {
		t.Errorf("graph links of a scope-all page do not carry the scope:\n%.1200s", mainOf(all))
	}
	if strings.Contains(app, `href="/dev/graph?scope=`) {
		t.Error("the default scope is not spelled out in links")
	}

	if code, _, _ := get(t, h, "/dev/graph/reports/routes?scope=everything"); code != 400 {
		t.Errorf("bad scope: %d", code)
	}
}
