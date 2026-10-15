package codegraph_test

import (
	"context"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/codegraph/internal/graphcheck"
)

// includes asserts that got contains every want.
func includes(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("%s: missing %s in %q", what, w, got)
		}
	}
}

// The whole graph of this repository, every layer at once: the invariants an
// agent relies on when it follows edges without checking each one (shared with
// the fixture's, see graphcheck), and the paths the spec promises.
func TestWholeGraphInvariants(t *testing.T) {
	rep, q, h := indexRepoStore(t, codegraph.Options{Router: miniRouter(), CommandSource: noCommands})
	for _, l := range rep.Layers {
		t.Logf("%-10s %-8s %5d quads %4d ms", l.Name, l.Status, l.Quads, l.DurationMS)
		if l.Status == "error" {
			t.Errorf("layer %s failed: %s", l.Name, l.Error)
		}
		if l.Status == "skipped" {
			t.Errorf("layer %s skipped although a router was given", l.Name)
		}
	}
	// This repository has no migrations and no templates (those belong to an
	// application), so the schema and render edges are exercised on the fixture
	// instead (TestFixtureGraphInvariants).
	res := graphcheck.Check(t, h, graphcheck.Options{
		Layers: []string{"code", "routes", "commands", "templates", "config", "mcp", "db", "docs", "externals", "findings"},
		RequireEdges: []string{"handled_by", "uses_mw", "implemented_by", "uses_config", "reads_env",
			"registers", "has_item", "mentions", "covers", "imports"},
	})
	t.Logf("%d quads in total", len(res.Quads))

	for _, id := range []string{"cfg:database.mariadb.dsn", "env:MARIADB_DSN"} {
		found := false
		for _, qd := range res.Quads {
			if iriOf(qd.Subject) == id && iriOf(qd.Predicate) == "sensitive" && qd.Object == quad.Value(quad.Bool(true)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s should be sensitive", id)
		}
	}

	// The paths the spec promises, on this repository.
	equal(t, "route -> handler", q(`g.V(g.IRI('route:GET:/_govibe/static/htmx.min.js')).Out(g.IRI('handled_by')).All()`), "<func:web.ServeHTMX>")
	equal(t, "tool -> registering function", q(`g.V(g.IRI('tool:graph_query')).In(g.IRI('registers')).All()`), "<func:internal/devmcp.registerGraphTools>")
	// (Prose and wiring change, so these assert inclusion, not exact sets.)
	includes(t, "functions that use a config key", q(`g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).Has(g.IRI('type'), g.IRI('kind:func')).All()`),
		"<func:internal/kernel.serve>", "<func:config.fromTree>")
	// A closure inside a package-level var has no enclosing function, so its use is the package's.
	includes(t, "packages that use a config key", q(`g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).Has(g.IRI('type'), g.IRI('kind:package')).All()`),
		"<pkg:internal/kernel>", "<pkg:config>")
	// The tool reads the project's database setting from its env.yml for the offline graph.
	includes(t, "packages that use the database key", q(`g.V(g.IRI('cfg:database.mariadb.dsn')).In(g.IRI('uses_config')).Has(g.IRI('type'), g.IRI('kind:package')).All()`),
		"<pkg:cmd/govibe>", "<pkg:internal/kernel>")
	includes(t, "docs that mention a route", q(`g.V(g.IRI('route:GET:/_govibe/static/htmx.min.js')).In(g.IRI('mentions')).All()`), "<doc:docs/public/ARCHITECTURE.md>")
	// complete: every row of its status table is implemented or deliberately skipped (it read in_progress until Phase 6).
	equal(t, "this spec's status", q(`g.V(g.IRI('spec:graph-indexer')).Out(g.IRI('status')).All()`), "complete")
	includes(t, "what the dev-db spec skipped",
		q(`g.V(g.IRI('spec:dev-db-explorer')).Out(g.IRI('has_item')).Has(g.IRI('status'), 'skipped').All()`),
		"<item:dev-db-explorer/production-exposure>")
	// The framework owns no migrations: an application's are its own.
	equal(t, "the migrations", q(`g.V().Has(g.IRI('type'), g.IRI('kind:migration')).All()`))

	// Idempotent: a second full run changes nothing.
	again, err := codegraph.New(h.QuadStore, codegraph.Options{Root: "../..", Router: miniRouter(), CommandSource: noCommands}).Run(context.Background())
	if err != nil || again.Added != 0 || again.Removed != 0 {
		t.Errorf("second run: +%d -%d, %v", again.Added, again.Removed, err)
	}
}

func iriOf(v quad.Value) string {
	if i, ok := v.(quad.IRI); ok {
		return string(i)
	}
	return ""
}
