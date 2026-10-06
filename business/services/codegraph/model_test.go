package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

func TestLoadReadsEveryNodeOfTheFixture(t *testing.T) {
	m := fixtureGraph(t)

	if m.Empty() || m.Quads() < 200 {
		t.Fatalf("a model of the fixture should hold hundreds of quads, got %d", m.Quads())
	}
	// kinds, attributes and edges arrive typed
	hello := m.Node("func:controllers.HelloAPI")
	if hello == nil || hello.Kind != "func" {
		t.Fatalf("HelloAPI: %+v", hello)
	}
	if hello.Str("name") != "HelloAPI" || !hello.Bool("exported") || hello.Int("line") == 0 {
		t.Errorf("attributes: %v", hello.Attrs)
	}
	if got := hello.first("defined_in"); got != "file:controllers/hello.go" {
		t.Errorf("defined_in = %q", got)
	}
	if ref := m.Ref(hello); ref.Path != "controllers/hello.go" || ref.Line != hello.Int("line") {
		t.Errorf("ref: %+v", ref)
	}
	// reverse edges are filled in
	route := m.Node("route:GET:/api/hello")
	if route == nil || route.Kind != "route" || route.first("handled_by") != hello.ID {
		t.Fatalf("route: %+v", route)
	}
	if got := hello.In["handled_by"]; len(got) != 1 || got[0] != route.ID {
		t.Errorf("handled_by in-edges of HelloAPI = %v", got)
	}
	// a route's path attribute is its URL, not a file
	if ref := m.Ref(route); ref.Path != "" || ref.Name != "GET /api/hello" {
		t.Errorf("route ref: %+v", ref)
	}
	// typed literals keep their types
	if m.Node("build:latest").Time("indexed_at").IsZero() {
		t.Error("indexed_at lost its type")
	}
	if tbl := m.Node("table:orders"); tbl == nil || tbl.Kind != "table" {
		t.Errorf("the fixture's migration should give a table:orders, got %+v", tbl)
	}
}

// What a hand-added quad is, the model ignores: only the indexer's labels count.
func TestLoadIgnoresHandAddedQuads(t *testing.T) {
	h, _ := fixtureStore(t)
	for _, q := range []quad.Quad{
		quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil),
		quad.Make(quad.IRI("pkg:controllers"), quad.IRI("imports"), quad.IRI("pkg:hand-added"), quad.String("my-label")),
		quad.Make(quad.IRI("pkg:controllers"), quad.IRI("name"), quad.String("not controllers"), nil),
	} {
		if err := h.AddQuad(q); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if m.Node("alice") != nil || m.Node("pkg:hand-added") != nil {
		t.Error("a hand-added quad leaked into the model")
	}
	if got := m.Node("pkg:controllers").Str("name"); got != "controllers" {
		t.Errorf("name = %q", got)
	}
}

// A string that looks like an IRI is stored in backticks (F4); the model hands
// out the original text. The encoding is not one-to-one for text that is itself
// a backticked magic string (lit("") and lit("“") are both "“"), which the
// indexer never writes; everything else round-trips.
func TestUnlitInvertsLit(t *testing.T) {
	for _, s := range []string{"plain", "<x>", "_:b", `"x"^^<t>`, `"x"@en`, "`", "`quoted`", "a `b` c", "route:GET:/x"} {
		if got := unlit(string(lit(s))); got != s {
			t.Errorf("unlit(lit(%q)) = %q", s, got)
		}
	}
	// text that merely starts and ends with a backtick is left alone
	if got := unlit("`not magic`"); got != "`not magic`" {
		t.Errorf("unlit of ordinary backticked text = %q", got)
	}
}

func TestFreshnessAndWarnings(t *testing.T) {
	m := fixtureGraph(t)
	f := m.Freshness()
	if !f.Indexed || f.IndexedAt.IsZero() || f.IndexerVersion != IndexerVersion || len(f.Layers) < 7 {
		t.Errorf("freshness: %+v", f)
	}
	if !strings.Contains(m.AsOf(), "T") {
		t.Errorf("as of: %q", m.AsOf())
	}
	if got := m.warnings("code", "routes"); len(got) != 0 {
		t.Errorf("warnings on a healthy graph: %v", got)
	}
	got := m.warnings("nosuch")
	if len(got) != 1 || !strings.Contains(got[0], "nosuch layer is not built") {
		t.Errorf("a layer that never ran: %v", got)
	}
	// An opt-in layer that is off says so, and how to switch it on.
	got = m.warnings("git")
	if len(got) != 1 || !strings.Contains(got[0], "git layer is off") || !strings.Contains(got[0], "GRAPH_GIT") {
		t.Errorf("a layer that is off: %v", got)
	}
}

func TestEmptyStoreGivesAnEmptyModel(t *testing.T) {
	h, _ := openStore(t)
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Empty() || m.Freshness().Indexed || m.AsOf() != "not indexed yet" {
		t.Errorf("empty model: %+v %q", m.Freshness(), m.AsOf())
	}
}
