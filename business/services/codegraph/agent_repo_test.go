package codegraph_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"petars-dev/govibe/business/services/codegraph"
)

// loadRepoModel indexes this repository (real code, a real chi router) and loads
// the model the agent tools answer from.
func loadRepoModel(t *testing.T) *codegraph.Model {
	t.Helper()
	_, _, h := indexRepoStore(t, codegraph.Options{Router: miniRouter()})
	start := time.Now()
	m, err := codegraph.Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded %d quads in %s", m.Quads(), time.Since(start))
	return m
}

// The spec's acceptance questions, on this repository's own graph.
func TestAgentSurfaceAnswersTheAcceptanceQuestionsOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)

	// 1. GET /api/hello: the route, its handler and file, and an explicit "no table is reached".
	c, err := m.Context(codegraph.ContextQuery{Route: "GET /api/hello"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Node.ID != "route:GET:/api/hello" || c.Handler == nil || c.Handler.ID != "func:controllers.HelloAPI" ||
		c.Handler.Path != "controllers/api.go" || c.Handler.Line == 0 {
		t.Fatalf("route briefing: %+v", c)
	}
	if c.Tables != nil || c.Reach == nil || len(c.Reach.Tables) != 0 || !strings.Contains(c.Reach.Note, "No table is reached") {
		t.Errorf("no table is reached, and the briefing says so: tables=%+v reach=%+v", c.Tables, c.Reach)
	}
	if c.Package == nil || c.Package.ID != "pkg:controllers" || !containsID(c.Imports, "pkg:business/actions") {
		t.Errorf("the handler's package and what it imports (the call to actions.Hello is a package import until the calls layer): %+v", c.Imports)
	}
	if len(c.Middleware) == 0 {
		t.Error("middleware of the route")
	}

	// what breaks if I change file Y: one call
	res, err := m.Impact(codegraph.ImpactQuery{Node: "controllers/api.go"})
	if err != nil {
		t.Fatal(err)
	}
	var route bool
	for _, a := range res.Affected {
		if a.ID == "route:GET:/api/hello" && a.Confidence == 100 && a.Method == "edge" {
			route = true
		}
		// the file declares a handler and a struct: its package's other routes are not in doubt
		if a.Kind == "route" && a.ID != "route:GET:/api/hello" {
			t.Errorf("a route beside the handler is named: %+v", a)
		}
	}
	if !route || len(res.Docs) == 0 {
		t.Errorf("impact of controllers/api.go: route=%v docs=%d", route, len(res.Docs))
	}

	// ... and of a package
	res, err = m.Impact(codegraph.ImpactQuery{Node: "business/actions"})
	if err != nil {
		t.Fatal(err)
	}
	var importer bool
	for _, a := range res.Affected {
		importer = importer || a.ID == "pkg:controllers" && a.Confidence == 100
	}
	if !importer {
		t.Errorf("controllers imports business/actions: %+v", res.Affected)
	}

	// 4. a package: direct imports and reverse dependents
	c, err = m.Context(codegraph.ContextQuery{Path: "business/actions"})
	if err != nil || !containsID(c.ImportedBy, "pkg:controllers") || c.ArchLayer != "business" {
		t.Errorf("package briefing: %+v %v", c, err)
	}

	// a path from a route to its handler, exactly
	p, err := m.Path(codegraph.PathQuery{From: "GET /api/hello", To: "controllers.HelloAPI"})
	if err != nil || !p.Found || p.Inferred || len(p.Steps) != 1 || p.Steps[0].Predicate != "handled_by" {
		t.Errorf("path: %+v %v", p, err)
	}
}

func containsID(refs []codegraph.NodeRef, id string) bool {
	for _, r := range refs {
		if r.ID == id {
			return true
		}
	}
	return false
}

func TestRepoReferencesResolve(t *testing.T) {
	m := loadRepoModel(t)
	for ref, want := range map[string]string{
		"/assets/static/theme.css":         "route:ANY:/assets/*", // a concrete URL finds its pattern
		"GET /dev/db/api/tables/users":     "route:GET:/dev/db/api/tables/{table}",
		"POST /dev/mcp":                    "route:ANY:/dev/mcp",
		"controllers/api.go":               "file:controllers/api.go",
		"business/services/codegraph":      "pkg:business/services/codegraph",
		"codegraph.Indexer.Run":            "func:business/services/codegraph.Indexer.Run",
		"dbexplorer.Explorer.Snapshot":     "func:storage/dbexplorer.Explorer.Snapshot",
		"server.port":                      "cfg:server.port",
		"graph_query":                      "tool:graph_query",
		"docs/specs/SPEC_GRAPH_INDEXER.md": "spec:graph-indexer",
		"SKILL.md":                         "doc:SKILL.md",
		"templates.Home":                   "tmpl:assets/templates.Home",
		"petars-dev/govibe/controllers":    "pkg:controllers",
		"controllers/devdb.go:25":          "file:controllers/devdb.go",
	} {
		got := m.Resolve(ref)
		if len(got) != 1 || got[0].ID != want {
			var ids []string
			for _, n := range got {
				ids = append(ids, n.ID)
			}
			t.Errorf("Resolve(%q) = %v, want %s", ref, ids, want)
		}
	}
}

// graph_schema on the real graph: every predicate that occurs is described, and
// the example queries it offers run against the live store.
func TestSchemaOfThisRepoIsFullyDescribedAndItsExamplesRun(t *testing.T) {
	rep, q, h := indexRepoStore(t, codegraph.Options{Router: miniRouter()})
	_ = rep
	m, err := codegraph.Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	s := m.Schema(nil)
	for _, p := range s.Predicates {
		if p.What == "" {
			t.Errorf("predicate %q occurs in the graph but has no description in predicateDocs", p.Predicate)
		}
	}
	for _, k := range s.Kinds {
		if k.What == "" || k.Layer == "" {
			t.Errorf("kind %q occurs in the graph but has no entry in kindDocs: %+v", k.Kind, k)
		}
	}
	for _, ex := range s.Examples {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%q: %v", ex.Question, r)
				}
			}()
			q(ex.Query)
		}()
	}
	// the one example whose answer this repo has is checked
	if got := q(s.Examples[0].Query); len(got) != 1 || got[0] != "<func:controllers.HelloAPI>" {
		t.Errorf("%q: %v", s.Examples[0].Question, got)
	}
}

func TestOverviewAndExplainChangeOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)
	md := m.Overview(nil)
	for _, want := range []string{"## Architecture layers", "`controllers`", "## Routes", "`/api/hello`", "`controllers.HelloAPI` (controllers/api.go:", "## MCP tools", "`graph_query`", "No tables"} {
		if !strings.Contains(md, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// The one known layering violation of this repo (SPEC_GRAPH_INDEXER, Phase 5): controllers/devdb.go imports storage/dbexplorer.
	if !strings.Contains(md, "`controllers` imports `storage/dbexplorer`") {
		t.Errorf("the controllers -> storage import should be named:\n%s", md)
	}
	if n := strings.Count(md, "\n"); n > 150 {
		t.Errorf("overview is %d lines", n)
	}

	brief, err := m.ExplainChange("controllers/api.go\nbusiness/actions/hello.go", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"controllers/api.go", "business/actions/hello.go", "GET /api/hello", "## Docs to update"} {
		if !strings.Contains(brief, want) {
			t.Errorf("briefing lacks %q:\n%s", want, brief)
		}
	}
}

// A hand-written reference to something that does not exist gets a pointer to
// what does, never a guess.
func TestRepoNearMisses(t *testing.T) {
	m := loadRepoModel(t)
	_, err := m.Context(codegraph.ContextQuery{Symbol: "HelloApi2"})
	if err == nil || !strings.Contains(err.Error(), "no node matches") {
		t.Errorf("%v", err)
	}
	// `Snapshot` is a type and a method of dbexplorer: ambiguous, and the error lists both
	_, err = m.Context(codegraph.ContextQuery{Symbol: "Snapshot"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "func:storage/dbexplorer.Explorer.Snapshot") {
		t.Errorf("%v", err)
	}
}
