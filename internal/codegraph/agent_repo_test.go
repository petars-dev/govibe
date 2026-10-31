package codegraph_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
)

var repoModel struct {
	once sync.Once
	m    *codegraph.Model
	err  error
}

// loadRepoModel is the model the agent tools answer from over this repository (real
// code, a real chi router). Indexing the repository is the slowest setup in the package,
// so it is done once and shared, like fixtureGraph: a test that gets it only reads it.
func loadRepoModel(t *testing.T) *codegraph.Model {
	t.Helper()
	repoModel.once.Do(func() {
		dir, err := os.MkdirTemp("", "codegraph-repo-")
		if err != nil {
			repoModel.err = err
			return
		}
		defer os.RemoveAll(dir)
		h, err := graphdb.OpenScratch(dir)
		if err != nil {
			repoModel.err = err
			return
		}
		defer h.Close()
		opts := codegraph.Options{Root: "../..", Router: miniRouter(), CommandSource: noCommands}
		if _, err := codegraph.New(h.QuadStore, opts).Run(context.Background()); err != nil {
			repoModel.err = fmt.Errorf("index the repository: %w", err)
			return
		}
		repoModel.m, repoModel.err = codegraph.Load(context.Background(), h.QuadStore)
	})
	if repoModel.err != nil {
		t.Fatal(repoModel.err)
	}
	return repoModel.m
}

// The spec's acceptance questions, on this repository's own graph.
func TestAgentSurfaceAnswersTheAcceptanceQuestionsOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)
	const route = "route:GET:/_govibe/static/htmx.min.js"

	// 1. the htmx route: the route, its handler and file, and an explicit "no table is reached".
	c, err := m.Context(codegraph.ContextQuery{Route: "GET /_govibe/static/htmx.min.js"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Node.ID != route || c.Handler == nil || c.Handler.ID != "func:web.ServeHTMX" ||
		c.Handler.Path != "web/web.go" || c.Handler.Line == 0 {
		t.Fatalf("route briefing: %+v", c)
	}
	if c.Tables != nil || c.Reach == nil || len(c.Reach.Tables) != 0 || !strings.Contains(c.Reach.Note, "No table is reached") {
		t.Errorf("no table is reached, and the briefing says so: tables=%+v reach=%+v", c.Tables, c.Reach)
	}
	if c.Package == nil || c.Package.ID != "pkg:web" {
		t.Errorf("the handler's package: %+v", c.Package)
	}
	if len(c.Middleware) == 0 {
		t.Error("middleware of the route")
	}

	// what breaks if I change file Y: one call
	res, err := m.Impact(codegraph.ImpactQuery{Node: "web/web.go"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range res.Affected {
		if a.ID == route && a.Confidence == 100 && a.Method == "edge" {
			found = true
		}
		// the file declares one handler: no other route is in doubt
		if a.Kind == "route" && a.ID != route {
			t.Errorf("a route beside the handler is named: %+v", a)
		}
	}
	if !found || len(res.Docs) == 0 {
		t.Errorf("impact of web/web.go: route=%v docs=%d", found, len(res.Docs))
	}

	// ... and of a package
	res, err = m.Impact(codegraph.ImpactQuery{Node: "web"})
	if err != nil {
		t.Fatal(err)
	}
	var importer bool
	for _, a := range res.Affected {
		importer = importer || a.ID == "pkg:internal/kernel" && a.Confidence == 100
	}
	if !importer {
		t.Errorf("the kernel imports web: %+v", res.Affected)
	}

	// 4. a package: direct imports and reverse dependents
	c, err = m.Context(codegraph.ContextQuery{Path: "web"})
	if err != nil || !containsID(c.ImportedBy, "pkg:internal/kernel") || c.ArchLayer != "support" {
		t.Errorf("package briefing: %+v %v", c, err)
	}

	// a path from a route to its handler, exactly
	p, err := m.Path(codegraph.PathQuery{From: "GET /_govibe/static/htmx.min.js", To: "web.ServeHTMX"})
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
		"/assets/static/theme.css":            "route:ANY:/assets/*", // a concrete URL finds its pattern
		"GET /dev/db/api/tables/users":        "route:GET:/dev/db/api/tables/{table}",
		"POST /dev/mcp":                       "route:ANY:/dev/mcp",
		"web/web.go":                          "file:web/web.go",
		"internal/codegraph":                  "pkg:internal/codegraph",
		"codegraph.Indexer.Run":               "func:internal/codegraph.Indexer.Run",
		"dbexplorer.Explorer.Snapshot":        "func:internal/dbexplorer.Explorer.Snapshot",
		"server.port":                         "cfg:server.port",
		"graph_query":                         "tool:graph_query",
		"docs/specs/SPEC_GRAPH_INDEXER.md":    "spec:graph-indexer",
		"SKILL.md":                            "doc:SKILL.md",
		"github.com/petars-dev/govibe/web":    "pkg:web",
		"internal/devtools/devui/devdb.go:25": "file:internal/devtools/devui/devdb.go",
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
	rep, q, h := indexRepoStore(t, codegraph.Options{Router: miniRouter(), CommandSource: noCommands})
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
	// The first example names the Hello demo's route, which an application has and
	// this repository does not (the fixture checks it); the config example is this repo's.
	if got := q(s.Examples[3].Query); len(got) == 0 {
		t.Errorf("%q: nothing uses cfg:server.port", s.Examples[3].Question)
	}
}

func TestOverviewAndExplainChangeOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)
	md := m.Overview(nil)
	for _, want := range []string{"## Architecture layers", "**wiring**", "## Routes", "`/_govibe/static/htmx.min.js`", "`web.ServeHTMX` (web/web.go:", "## MCP tools", "`graph_query`", "No tables"} {
		if !strings.Contains(md, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// The framework has no layering violation to name.
	if strings.Contains(md, "**error**") {
		t.Errorf("the overview names an error finding:\n%s", md)
	}
	if n := strings.Count(md, "\n"); n > 150 {
		t.Errorf("overview is %d lines", n)
	}

	// A change to the indexer or to the code under the framework's directories says what to update.
	idx, err := m.ExplainChange("internal/codegraph/layer_routes.go\ninternal/kernel/kernel.go", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`docs/public/GRAPH_SCHEMA.md` and `docs/specs/SPEC_GRAPH_INDEXER.md`: the indexer or its vocabulary changed", "(code under the layered directories changed; check)"} {
		if !strings.Contains(idx, want) {
			t.Errorf("the brief for a change to the indexer lacks %q:\n%s", want, idx)
		}
	}
	if plain, err := m.ExplainChange("README.md", ""); err != nil || strings.Contains(plain, "the indexer or its vocabulary changed") {
		t.Errorf("a README edit does not touch the indexer: %v\n%s", err, plain)
	}

	brief, err := m.ExplainChange("web/web.go\ninternal/kernel/kernel.go", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web/web.go", "internal/kernel/kernel.go", "GET /_govibe/static/htmx.min.js", "## Docs to update"} {
		if !strings.Contains(brief, want) {
			t.Errorf("briefing lacks %q:\n%s", want, brief)
		}
	}
}

// A hand-written reference to something that does not exist gets a pointer to
// what does, never a guess.
func TestRepoNearMisses(t *testing.T) {
	m := loadRepoModel(t)
	_, err := m.Context(codegraph.ContextQuery{Symbol: "ServeHTMX2"})
	if err == nil || !strings.Contains(err.Error(), "no node matches") {
		t.Errorf("%v", err)
	}
	// `Snapshot` is a type and a method of dbexplorer: ambiguous, and the error lists both
	_, err = m.Context(codegraph.ContextQuery{Symbol: "Snapshot"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "func:internal/dbexplorer.Explorer.Snapshot") {
		t.Errorf("%v", err)
	}
}
