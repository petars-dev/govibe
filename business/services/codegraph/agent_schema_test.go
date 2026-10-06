package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

func TestSchemaDescribesTheGraphAsItIs(t *testing.T) {
	m := fixtureGraph(t)
	s := m.Schema([]string{"docs"})

	if s.AsOf == "" || !s.Freshness.Indexed || len(s.Freshness.Layers) < 7 || len(s.Freshness.StaleLayers) != 1 {
		t.Errorf("freshness: %+v", s.Freshness)
	}
	kinds := map[string]KindInfo{}
	for _, k := range s.Kinds {
		kinds[k.Kind] = k
		if k.What == "" || k.Layer == "" || k.ID == "" || k.Example == "" || m.nodes[k.Example] == nil || k.Count != len(m.byKind[k.Kind]) {
			t.Errorf("kind %s: %+v", k.Kind, k)
		}
	}
	for _, want := range []string{"package", "file", "func", "route", "table", "column", "spec_item", "config_key", "annotation"} {
		if kinds[want].Count == 0 {
			t.Errorf("kind %s missing", want)
		}
	}
	if kinds["route"].Example != "route:GET:/api/hello" || kinds["func"].Example != "func:controllers.HelloAPI" {
		t.Errorf("examples are real nodes: %+v %+v", kinds["route"], kinds["func"])
	}
	if _, ok := kinds["finding"]; ok {
		t.Error("a kind with no nodes is not listed")
	}

	preds := map[string]PredicateInfo{}
	for _, p := range s.Predicates {
		preds[p.Predicate] = p
		if p.What == "" {
			t.Errorf("predicate %q has no description: add it to predicateDocs", p.Predicate)
		}
	}
	h := preds["handled_by"]
	if !h.Structure || h.Count != 5 || strings.Join(h.From, ",") != "route" || strings.Join(h.To, ",") != "func" {
		t.Errorf("handled_by: %+v", h)
	}
	if n := preds["name"]; n.Structure || n.Count < 50 {
		t.Errorf("name is an attribute: %+v", n)
	}
	if !preds["type"].Structure || preds["type"].Count < 100 {
		t.Errorf("type: %+v", preds["type"])
	}
	// structural predicates come first
	seenAttr := false
	for _, p := range s.Predicates {
		if !p.Structure {
			seenAttr = true
		} else if seenAttr {
			t.Errorf("structural predicate %s after an attribute", p.Predicate)
		}
	}

	notes := strings.Join(s.Notes, "\n")
	for _, want := range []string{"findings layer is not built", "calls layer is not built", "git layer is not built", "docs layer is stale"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if len(s.Conventions) < 5 || len(s.Questions) < 6 || len(s.Examples) != len(exampleQueries) {
		t.Errorf("guidance: %d conventions %d questions %d examples", len(s.Conventions), len(s.Questions), len(s.Examples))
	}
	for _, q := range s.Questions {
		if !strings.HasPrefix(q.Use, "graph_") {
			t.Errorf("question routes to %q", q.Use)
		}
	}
}

// Every kind a layer can emit is described. The graph of the fixture has every
// kind but finding; the vocabulary table is checked against the constants.
func TestEveryKindConstantIsDescribed(t *testing.T) {
	described := map[string]bool{}
	for _, kd := range kindDocs {
		described[kd.kind] = true
	}
	for _, k := range []quad.IRI{KindPackage, KindFile, KindFunc, KindType, KindBuild, KindLayer, KindRoute, KindMiddleware, KindTemplate, KindMCPTool,
		KindConfigKey, KindEnvVar, KindDoc, KindSpec, KindSpecItem, KindTable, KindColumn, KindIndex, KindMigration, KindAnnotation} {
		if name := strings.TrimPrefix(string(k), "kind:"); !described[name] {
			t.Errorf("kind %s has no entry in kindDocs", name)
		}
	}
	for _, p := range structuralPredicates {
		if predicateDocs[string(p)] == "" {
			t.Errorf("structural predicate %s has no entry in predicateDocs", p)
		}
	}
}

// The example queries are real: each runs against a graph and the ones whose
// answer exists on the fixture return it.
func TestExampleQueriesRun(t *testing.T) {
	h, _ := fixtureStore(t)
	for _, ex := range exampleQueries {
		if _, err := gizmoRun(t, h, ex.Query); err != nil {
			t.Errorf("%q: %v", ex.Question, err)
		}
	}
	for _, q := range exampleQueries {
		if strings.Contains(q.Query, "reads_table") || strings.Contains(q.Query, "Emit") {
			continue
		}
		res, _ := gizmoRun(t, h, q.Query)
		if len(res) == 0 {
			t.Errorf("%q returns nothing on the fixture", q.Question)
		}
	}
	res, _ := gizmoRun(t, h, exampleQueries[2].Query)
	if len(res) == 0 {
		t.Errorf("functions that read a table: nothing")
	}
}

func TestOverviewRendersTheArchitecture(t *testing.T) {
	m := fixtureGraph(t)
	md := m.Overview(nil)
	for _, want := range []string{
		"# Project graph overview",
		"## Architecture layers", "**controllers**", "`controllers` (", "**storage**", "`storage/orderstore`",
		"## Imports between layers", "controllers → business (2), storage (1)",
		"`controllers` imports `storage/orderstore`", // the fixture's deliberate violation
		"## Routes", "| GET | `/api/hello` | `controllers.HelloAPI` (controllers/hello.go:10) | 2 |", "| ANY | `/dev/mcp` (dev) |", "`middleware` (1 func)",
		"## MCP tools", "`fixture_ping`", "registered by `devmcp.registerTools`",
		"## Database", "`orders` (4 columns) → users (implicit, 95%)", "read by 2, written by 1 functions",
		"## Config", "## Specs", "`docs/specs/SPEC_HELLO.md` (in_progress)",
		"## Not covered", "findings layer is not built",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if strings.Contains(md, "Stale layers") {
		t.Error("no stale banner when nothing is stale")
	}
	if !strings.Contains(m.Overview([]string{"db"}), "Stale layers: db") {
		t.Error("stale layers are shown")
	}
	if lines := strings.Count(md, "\n"); lines > 150 {
		t.Errorf("an overview is a page, not a dump: %d lines", lines)
	}

	h, _ := openStore(t)
	empty, _ := Load(context.Background(), h.QuadStore)
	if got := empty.Overview(nil); !strings.Contains(got, "has not been indexed yet") {
		t.Errorf("empty graph: %s", got)
	}
}

func TestChangedPathsFromPathsAndADiff(t *testing.T) {
	diff := "diff --git a/controllers/api.go b/controllers/api.go\nindex 1..2 100644\n--- a/controllers/api.go\n+++ b/controllers/api.go\n@@ -1 +1 @@\n" +
		"diff --git a/old/gone.go b/old/gone.go\ndeleted file mode 100644\n--- a/old/gone.go\n+++ /dev/null\n" +
		"diff --git a/new.go b/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/new.go\n"
	got := ChangedPaths("README.md, ./routes/routes.go\ncontrollers/api.go", diff)
	want := []string{"README.md", "controllers/api.go", "new.go", "old/gone.go", "routes/routes.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v want %v", got, want)
	}
	if got := ChangedPaths("", ""); len(got) != 0 {
		t.Errorf("nothing: %v", got)
	}
}

func TestExplainChangeBriefsOnImpactAndDocsToUpdate(t *testing.T) {
	m := fixtureGraph(t)
	md, err := m.ExplainChange("controllers/orders.go, storage/migrations/sql/0001_init.sql, controllers/orders_test.go, brand/new.go", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Explain this change", "4 changed path(s)",
		"### `controllers/orders.go` — file `file:controllers/orders.go` in `controllers` (controllers)",
		"- Defines: GetOrder, ListOrders, writeJSON",
		"Affected for certain (stored edges, confidence ≥ 90): route: GET /api/orders, GET /api/orders/{id}",
		"Possibly affected (package-level estimate, confidence ≤ 60, not proven)",
		"### `storage/migrations/sql/0001_init.sql` — migration `mig:0001_init`",
		"Store.Create", // the migration's tables are read by code
		"Not in the graph:", "`controllers/orders_test.go`: test files are not indexed", "`brand/new.go`: a new or deleted Go file",
		"## Docs and specs that mention the changed code", "`doc:docs/public/ARCHITECTURE.md`",
		"## Docs to update", "SKILL.md",
		"- [ ] `docs/public/ARCHITECTURE.md`: if the change adds, moves or removes a package, route, layer or dev tool (code under the layered directories changed; check)",
		"a migration or table is involved", "likely",
		"## Your task", "graph_impact",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("briefing lacks %q\n---\n%s", want, md)
		}
	}
	if strings.Contains(md, "GRAPH_SCHEMA.md` and") {
		t.Error("the graph docs are only asked for when the indexer changed")
	}

	// a change to the indexer asks for the graph docs; a spec row shows up as a spec to update
	md, err = m.ExplainChange("", "diff --git a/business/services/codegraph/vocab.go b/business/services/codegraph/vocab.go\n--- a/business/services/codegraph/vocab.go\n+++ b/business/services/codegraph/vocab.go\n")
	if err != nil || !strings.Contains(md, "`docs/public/GRAPH_SCHEMA.md` and `docs/specs/SPEC_GRAPH_INDEXER.md`") {
		t.Errorf("%v\n%s", err, md)
	}
	md, _ = m.ExplainChange("controllers/hello.go", "")
	if !strings.Contains(md, "Spec status table: `docs/specs/SPEC_HELLO.md` rows “GET /api/hello endpoint”") {
		t.Errorf("a spec row that covers the file:\n%s", md)
	}

	if _, err := m.ExplainChange("  ", ""); err == nil {
		t.Error("nothing to explain")
	}
	// bounded
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, "pkg/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go")
	}
	md, _ = m.ExplainChange(strings.Join(many, ","), "")
	if !strings.Contains(md, "(the first 15 analysed)") {
		t.Errorf("a long change list is capped:\n%s", md[:300])
	}
}
