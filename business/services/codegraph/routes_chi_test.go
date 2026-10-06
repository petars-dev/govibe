package codegraph_test

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"petars-dev/govibe/business/services/codegraph"
	"petars-dev/govibe/controllers"
	"petars-dev/govibe/devmcp"
	appmw "petars-dev/govibe/middleware"
	"petars-dev/govibe/storage/graphdb"
)

// miniRouter mirrors routes/routes.go with the application's real handlers, so
// the routes layer is exercised on a runtime router that resolves to this
// repository's source. (routes.NewRouter itself opens the dev graph store and
// starts an indexer, so a test cannot call it.)
func miniRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(appmw.RequestLogger)

	r.Group(func(g chi.Router) {
		g.Use(appmw.DevNoCache)
		g.Handle("/assets/*", http.StripPrefix("/assets", http.FileServer(http.Dir("assets"))))
	})
	r.Get("/", controllers.Home)
	r.Route("/api", func(api chi.Router) {
		api.Get("/hello", controllers.HelloAPI)
	})
	r.Get("/hello", controllers.Hello)
	r.Route("/dev/docs", func(d chi.Router) {
		d.Get("/app", controllers.DevDocsAppIndex)
		d.Get("/app/{slug}", controllers.DevDocsAppFile)
	})
	r.Route("/dev/db/api", func(api chi.Router) {
		api.Get("/meta", controllers.DevDBMeta(nil))
		api.Get("/tables/{table}", controllers.DevDBDescribeTable(nil))
		api.Post("/tables/{table}/rows", controllers.DevDBInsertRow(nil))
		api.Delete("/tables/{table}/rows", controllers.DevDBResetTable(nil))
	})
	r.Handle("/dev/mcp", devmcp.NewHandler(nil, nil, nil))
	r.Post("/dev/graph/reindex", controllers.DevGraphReindex(nil))
	return r
}

func indexRepo(t *testing.T, opts codegraph.Options) (*codegraph.Report, func(string) []string) {
	t.Helper()
	rep, q, _ := indexRepoStore(t, opts)
	return rep, q
}

// indexRepoStore indexes this repository into a fresh store and also returns
// the store, for tests that read raw quads.
func indexRepoStore(t *testing.T, opts codegraph.Options) (*codegraph.Report, func(string) []string, *cayley.Handle) {
	t.Helper()
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	opts.Root = "../../.."
	rep, err := codegraph.New(h.QuadStore, opts).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return rep, func(q string) []string {
		t.Helper()
		it, err := query.Execute(context.Background(), h.QuadStore, gizmo.Name, q, query.Options{Collation: query.JSON, Limit: 1000})
		if err != nil {
			t.Fatalf("gizmo %s: %v", q, err)
		}
		defer it.Close()
		var ids []string
		for it.Next(context.Background()) {
			m, _ := it.Result().(map[string]any)
			ids = append(ids, fmt.Sprint(m["id"]))
		}
		sort.Strings(ids)
		return ids
	}, h
}

func equal(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s:\n got  %q\n want %q", what, got, want)
			return
		}
	}
}

// A route resolves to its handler's declaration through the Go runtime: file and
// line map onto the code layer's functions, and a closure resolves to the
// function that contains it.
func TestRoutesLayerOnARealChiRouter(t *testing.T) {
	rep, q := indexRepo(t, codegraph.Options{Router: miniRouter()})
	var routes codegraph.LayerReport
	for _, l := range rep.Layers {
		if l.Name == "routes" {
			routes = l
		}
	}
	if routes.Status != "ok" || routes.Quads == 0 {
		t.Fatalf("routes layer: %+v", routes)
	}

	hello := `g.V(g.IRI('route:GET:/api/hello'))`
	equal(t, "handler of GET /api/hello", q(hello+`.Out(g.IRI('handled_by')).All()`), "<func:controllers.HelloAPI>")
	equal(t, "...which is defined in", q(hello+`.Out(g.IRI('handled_by')).Out(g.IRI('defined_in')).All()`), "<file:controllers/api.go>")
	equal(t, "path and method", q(hello+`.Out(g.IRI('path')).All()`), "/api/hello")
	equal(t, "name", q(hello+`.Out(g.IRI('name')).All()`), "GET /api/hello")
	equal(t, "not dev only", q(hello+`.Out(g.IRI('dev_only')).All()`), "false")

	// Middleware: the root stack applies to every route; a group's inline
	// middleware only to its own.
	equal(t, "middleware of GET /api/hello", q(hello+`.Out(g.IRI('uses_mw')).All()`),
		"<mw:github.com/go-chi/chi/v5/middleware.RequestID>", "<mw:middleware.RequestLogger>")
	equal(t, "assets also get DevNoCache",
		q(`g.V(g.IRI('route:ANY:/assets/*')).Out(g.IRI('uses_mw')).All()`),
		"<mw:github.com/go-chi/chi/v5/middleware.RequestID>", "<mw:middleware.RequestLogger>", "<mw:middleware.DevNoCache>")
	equal(t, "an application middleware is traced to its code",
		q(`g.V(g.IRI('mw:middleware.RequestLogger')).Out(g.IRI('implemented_by')).All()`), "<func:middleware.RequestLogger>")
	equal(t, "a library middleware has no code node",
		q(`g.V(g.IRI('mw:github.com/go-chi/chi/v5/middleware.RequestID')).Out(g.IRI('implemented_by')).All()`))
	equal(t, "library middleware name",
		q(`g.V(g.IRI('mw:github.com/go-chi/chi/v5/middleware.RequestID')).Out(g.IRI('name')).All()`), "RequestID")

	// Sub-routers join their patterns; a handler factory's closure resolves to
	// the factory.
	equal(t, "mounted route", q(`g.V(g.IRI('route:GET:/dev/docs/app')).Out(g.IRI('handled_by')).All()`), "<func:controllers.DevDocsAppIndex>")
	equal(t, "chi parameters are kept", q(`g.V(g.IRI('route:GET:/dev/db/api/tables/{table}')).Out(g.IRI('handled_by')).All()`),
		"<func:controllers.DevDBDescribeTable>")
	equal(t, "closure -> enclosing factory", q(`g.V(g.IRI('route:GET:/dev/db/api/meta')).Out(g.IRI('handled_by')).All()`), "<func:controllers.DevDBMeta>")
	equal(t, "one path, several methods",
		q(`g.V().Has(g.IRI('path'), '/dev/db/api/tables/{table}/rows').All()`),
		"<route:DELETE:/dev/db/api/tables/{table}/rows>", "<route:POST:/dev/db/api/tables/{table}/rows>")
	equal(t, "dev only", q(`g.V(g.IRI('route:GET:/dev/db/api/meta')).Out(g.IRI('dev_only')).All()`), "true")

	// A catch-all registration is one route, not nine.
	equal(t, "catch-all routes",
		q(`g.V().Has(g.IRI('method'), 'ANY').All()`), "<route:ANY:/assets/*>", "<route:ANY:/dev/mcp>")

	// A handler that is not application code is labelled, not invented.
	equal(t, "no invented link for a library handler", q(`g.V(g.IRI('route:ANY:/dev/mcp')).Out(g.IRI('handled_by')).All()`))
	if got := q(`g.V(g.IRI('route:ANY:/dev/mcp')).Out(g.IRI('handler')).All()`); len(got) != 1 {
		t.Errorf("an unresolved handler should say what the runtime sees, got %q", got)
	} else {
		t.Logf("unresolved /dev/mcp handler: %s", got[0])
	}
	if got := q(`g.V(g.IRI('route:ANY:/assets/*')).Out(g.IRI('handler')).All()`); len(got) != 1 {
		t.Errorf("assets handler label: %q", got)
	} else {
		t.Logf("unresolved /assets handler: %s", got[0])
	}
}
