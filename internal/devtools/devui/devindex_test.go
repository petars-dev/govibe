package devui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

func indexed(t *testing.T, opts codegraph.Options) *codegraph.Indexer {
	t.Helper()
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ix := codegraph.New(h.QuadStore, opts)
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ix
}

func devIndexBody(t *testing.T, info DevInfo, ix *codegraph.Indexer) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/dev", nil)
	DevIndex(info, ix).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	return rec.Body.String()
}

func TestTheDevIndexIsTheFrontDoor(t *testing.T) {
	ix := indexed(t, withRoutes())
	body := devIndexBody(t, DevInfo{
		AppName: "shop", Module: "example.com/shop", FrameworkVersion: "v0.3.1", Env: "local",
		DB: true, Graph: true, MCP: true, FrameworkDocs: true,
		Commands: []DevCommand{{Name: "orders:export", Short: "Export yesterday's orders"}, {Name: "a:first"}},
	}, ix)

	for _, want := range []string{
		"<h1>shop</h1>", "example.com/shop", "GoVibe <code>v0.3.1</code>", "environment <code>local</code>",
		`href="/dev/graph"`, `href="/dev/docs/app"`, `href="/dev/db"`, `href="/dev/graph/reports"`, "http://localhost:8080/dev/mcp", "graph_schema",
		"orders:export", "Export yesterday&#39;s orders", "<code>GoVibe:</code>",
		"Counted in the app's own scope",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the dev index lacks %q", want)
		}
	}
	// the app's own routes and commands are counted: one app route (the /dev one is the framework's, so left out)
	if !strings.Contains(body, "<b>1</b><span>route</span>") || !strings.Contains(body, "<b>2</b><span>commands</span>") {
		t.Errorf("counts:\n%s", between(body, `<ul class="counts"`, "</ul>"))
	}
	if strings.Contains(body, "registers no routes yet") {
		t.Error("an app with routes is told to add one")
	}
	// commands are listed in order
	if strings.Index(body, "a:first") > strings.Index(body, "orders:export") {
		t.Error("commands are sorted")
	}
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], b)
	if j < 0 {
		return s[i:]
	}
	return s[i : i+j]
}

// The handler serves many requests with one DevInfo: it sorts its own copy of the commands.
func TestTheDevIndexDoesNotSortTheSharedCommandList(t *testing.T) {
	info := DevInfo{AppName: "shop", Commands: []DevCommand{{Name: "z:last"}, {Name: "a:first"}}}
	h := DevIndex(info, nil)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dev", nil))
		if !strings.Contains(rec.Body.String(), "a:first") {
			t.Fatal("no commands")
		}
	}
	if info.Commands[0].Name != "z:last" {
		t.Errorf("the shared list was sorted in place: %v", info.Commands)
	}
}

func TestTheDevIndexSaysWhatToDoFirstWhenThereAreNoRoutes(t *testing.T) {
	// the demo was deleted: the app registers nothing, and the framework's routes are not the app's
	ix := indexed(t, codegraph.Options{Root: codegraphFixture, RouteSource: func() ([]routeinfo.Route, error) {
		return []routeinfo.Route{{Method: "GET", Path: "/_govibe/static/htmx.min.js"}, {Method: "ANY", Path: "/dev/mcp"}}, nil
	}})
	body := devIndexBody(t, DevInfo{AppName: "shop", Graph: true}, ix)
	for _, want := range []string{"This app registers no routes yet", "routes/routes.go", `r.Get("/", controllers.Home)`, "<b>0</b><span>routes</span>"} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q", want)
		}
	}
}

func TestTheDevIndexExplainsWhatIsNotMounted(t *testing.T) {
	// no graph, no database: nothing is linked that would 404, and each says why
	body := devIndexBody(t, DevInfo{AppName: "shop"}, nil)
	for _, want := range []string{"No database is configured", "MARIADB_DSN", "Not mounted: the graph store could not be opened"} {
		if !strings.Contains(body, want) {
			t.Errorf("lacks %q", want)
		}
	}
	for _, bad := range []string{`href="/dev/graph"`, `href="/dev/db"`, `href="/dev/mcp"`, "<ul class=\"counts\""} {
		if strings.Contains(body, bad) {
			t.Errorf("links or counts something that is not there: %s", bad)
		}
	}
	// no name given: the page still has a heading
	if !strings.Contains(devIndexBody(t, DevInfo{}, nil), "<h1>this app</h1>") {
		t.Error("no heading for an unnamed app")
	}
}
