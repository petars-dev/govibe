package codegraph_test

import (
	"context"
	"testing"

	"github.com/cayleygraph/quad"

	"petars-dev/govibe/business/services/codegraph"
	"petars-dev/govibe/business/services/codegraph/internal/graphcheck"
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
	rep, q, h := indexRepoStore(t, codegraph.Options{Router: miniRouter()})
	for _, l := range rep.Layers {
		t.Logf("%-10s %-8s %5d quads %4d ms", l.Name, l.Status, l.Quads, l.DurationMS)
		if l.Status == "error" {
			t.Errorf("layer %s failed: %s", l.Name, l.Error)
		}
		if l.Status == "skipped" {
			t.Errorf("layer %s skipped although a router was given", l.Name)
		}
	}
	// This repository's own migration creates no tables, so the schema edges are
	// exercised on the fixture instead (TestFixtureGraphInvariants).
	res := graphcheck.Check(t, h, graphcheck.Options{
		Layers: []string{"code", "routes", "templates", "config", "mcp", "db", "docs", "findings"},
		RequireEdges: []string{"handled_by", "uses_mw", "implemented_by", "renders", "uses_config", "reads_env",
			"registers", "has_item", "mentions", "covers", "imports"},
	})
	t.Logf("%d quads in total", len(res.Quads))

	for _, id := range []string{"cfg:database.mariadb.dsn", "cfg:workos.api_key", "env:MARIADB_DSN", "env:WORKOS_API_KEY"} {
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
	equal(t, "route -> handler -> template", q(`g.V(g.IRI('route:GET:/')).Out(g.IRI('handled_by')).Out(g.IRI('renders')).All()`), "<tmpl:assets/templates.Home>")
	equal(t, "tool -> registering function", q(`g.V(g.IRI('tool:graph_query')).In(g.IRI('registers')).All()`), "<func:devmcp.registerGraphTools>")
	// (Prose and wiring change, so these assert inclusion, not exact sets.)
	includes(t, "functions that use a config key", q(`g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).Has(g.IRI('type'), g.IRI('kind:func')).All()`),
		"<func:..main>", "<func:config.Load>")
	// A closure inside a package-level var has no enclosing function, so its use is the package's.
	includes(t, "packages that use a config key", q(`g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).Has(g.IRI('type'), g.IRI('kind:package')).All()`),
		"<pkg:.>", "<pkg:cmd/govibe>", "<pkg:config>")
	includes(t, "docs that mention a route", q(`g.V(g.IRI('route:GET:/api/hello')).In(g.IRI('mentions')).All()`), "<doc:SKILL.md>")
	equal(t, "this spec's status", q(`g.V(g.IRI('spec:graph-indexer')).Out(g.IRI('status')).All()`), "in_progress")
	includes(t, "what the dev-db spec skipped",
		q(`g.V(g.IRI('spec:dev-db-explorer')).Out(g.IRI('has_item')).Has(g.IRI('status'), 'skipped').All()`),
		"<item:dev-db-explorer/production-exposure>")
	// The one migration is a node even though it creates nothing.
	equal(t, "the migrations", q(`g.V().Has(g.IRI('type'), g.IRI('kind:migration')).All()`), "<mig:0001_init>")

	// Idempotent: a second full run changes nothing.
	again, err := codegraph.New(h.QuadStore, codegraph.Options{Root: "../../..", Router: miniRouter()}).Run(context.Background())
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
