package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/quad"
)

func ctxOf(t *testing.T, m *Model, q ContextQuery) *Context {
	t.Helper()
	c, err := m.Context(q)
	if err != nil {
		t.Fatalf("context %+v: %v", q, err)
	}
	return c
}

func routeIDs(rs []RouteInfo) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestContextOfAFile(t *testing.T) {
	m := fixtureGraph(t)
	c := ctxOf(t, m, ContextQuery{Path: "controllers/orders.go"})

	if c.Node.ID != "file:controllers/orders.go" || c.Package == nil || c.Package.ID != "pkg:controllers" || c.ArchLayer != "controllers" {
		t.Fatalf("where it lives: %+v %+v %q", c.Node, c.Package, c.ArchLayer)
	}
	var syms []string
	for _, s := range c.Symbols {
		syms = append(syms, s.Name)
		if s.Signature == "" || s.Line == 0 {
			t.Errorf("symbol without a signature or line: %+v", s)
		}
	}
	if strings.Join(syms, ",") != "ListOrders,GetOrder,writeJSON" {
		t.Errorf("symbols in source order: %v", syms)
	}
	mustHave(t, "routes it serves", routeIDs(c.Routes), "route:GET:/api/orders", "route:GET:/api/orders/{id}")
	for _, r := range c.Routes {
		if len(r.Middleware) != 2 || r.Handler == "" || r.Method != "GET" {
			t.Errorf("route info: %+v", r)
		}
	}
	mustHave(t, "imports of its package", ids(c.Imports), "pkg:business/services/orders", "pkg:storage/orderstore")
	mustHave(t, "importers of its package", ids(c.ImportedBy), "pkg:routes")
	if len(c.Docs) == 0 || c.Docs[0].ID != "doc:docs/public/ARCHITECTURE.md" {
		t.Errorf("docs: %+v", c.Docs)
	}
	if c.Connections["in:defined_in"] != 3 {
		t.Errorf("connections: %v", c.Connections)
	}
	// the briefing does not say what a function calls: nothing records it
	if c.Reach != nil {
		t.Errorf("a file has no reach: %+v", c.Reach)
	}
}

func TestContextOfAPackageListsWhatItHoldsAndWhoUsesIt(t *testing.T) {
	m := fixtureGraph(t)
	c := ctxOf(t, m, ContextQuery{Path: "storage/orderstore"})
	if c.Node.ID != "pkg:storage/orderstore" || c.ArchLayer != "storage" || c.Package != nil {
		t.Fatalf("%+v", c)
	}
	mustHave(t, "functions", func() []string {
		var out []string
		for _, s := range c.Symbols {
			out = append(out, s.ID)
		}
		return out
	}(), "func:storage/orderstore.Store.List", "typ:storage/orderstore.Order")
	if c.Tables == nil || len(c.Tables.Reads) != 1 || len(c.Tables.Writes) != 1 || len(c.Tables.MapsTo) != 1 {
		t.Errorf("tables it touches: %+v", c.Tables)
	}
	mustHave(t, "who imports it", ids(c.ImportedBy), "pkg:business/services/orders", "pkg:controllers", "pkg:devmcp", "pkg:routes")
}

func TestContextOfAFunctionSaysWhatItReachesAndWhatItDoesNot(t *testing.T) {
	m := fixtureGraph(t)
	c := ctxOf(t, m, ContextQuery{Symbol: "controllers.HelloAPI"})
	if c.Node.ID != "func:controllers.HelloAPI" || c.File == nil || c.File.Path != "controllers/hello.go" || c.Package.ID != "pkg:controllers" {
		t.Fatalf("%+v", c)
	}
	if got := routeIDs(c.Routes); len(got) != 1 || got[0] != "route:GET:/api/hello" {
		t.Errorf("routes: %v", got)
	}
	if c.Tables != nil {
		t.Errorf("HelloAPI touches no table itself: %+v", c.Tables)
	}
	// ... but its package imports one that does, and the briefing shows it, as a possibility
	if c.Reach == nil || len(c.Reach.Tables) != 1 {
		t.Fatalf("reach: %+v", c.Reach)
	}
	rt := c.Reach.Tables[0]
	if rt.Table.ID != "table:orders" || rt.Confidence != 50 || rt.Access != "read+write" ||
		strings.Join(rt.Via, ">") != "pkg:controllers>pkg:storage/orderstore" || len(rt.Functions) != 3 {
		t.Errorf("reach table: %+v", rt)
	}
	if !strings.Contains(c.Reach.Note, "touches no table itself") || !strings.Contains(c.Reach.Note, "possible, not proven") {
		t.Errorf("reach note: %s", c.Reach.Note)
	}
}

func TestContextOfARoute(t *testing.T) {
	m := fixtureGraph(t)

	c := ctxOf(t, m, ContextQuery{Route: "GET /"})
	if c.Handler == nil || c.Handler.ID != "func:controllers.HomePage" || c.Handler.Line == 0 || c.File == nil {
		t.Fatalf("handler: %+v", c)
	}
	if len(c.Middleware) != 2 {
		t.Errorf("middleware: %v", ids(c.Middleware))
	}
	if got := ids(c.Renders); len(got) != 1 || got[0] != "tmpl:assets/templates.Home" {
		t.Errorf("renders: %v", got)
	}

	// a library handler: the briefing says it stops there
	c = ctxOf(t, m, ContextQuery{Route: "/assets/"})
	if c.Handler != nil || c.Attrs["handler"] == nil || !strings.Contains(strings.Join(c.Notes, " "), "not application code") {
		t.Errorf("a library handler: %+v", c)
	}

	// a route whose handler goes to the database directly: exact tables, and no
	// possibility is repeated as an exact fact
	c = ctxOf(t, m, ContextQuery{Route: "GET /api/orders/{id}"})
	if c.Handler.ID != "func:controllers.GetOrder" {
		t.Fatalf("%+v", c.Handler)
	}
}

func TestContextOfATable(t *testing.T) {
	m := fixtureGraph(t)
	c := ctxOf(t, m, ContextQuery{Table: "orders"})
	tb := c.Table
	if tb == nil || c.Node.ID != "table:orders" || tb.Engine != "InnoDB" || tb.Source != "migration" {
		t.Fatalf("%+v", c)
	}
	var cols []string
	for _, col := range tb.Columns {
		cols = append(cols, col.Name)
	}
	if strings.Join(cols, ",") != "id,user_id,total,status" {
		t.Errorf("columns in table order: %v", cols)
	}
	userID := tb.Columns[1]
	if userID.Type != "bigint unsigned" || userID.Nullable || userID.Key != "MUL" || userID.Implicit != "col:users.id" || userID.ImplicitPct != 95 {
		t.Errorf("user_id: %+v", userID)
	}
	if tb.Columns[0].Extra != "auto_increment" || tb.Columns[3].Default != "'new'" {
		t.Errorf("columns: %+v", tb.Columns)
	}
	if len(tb.Indexes) != 2 || !tb.Indexes[0].Primary && !tb.Indexes[1].Primary {
		t.Errorf("indexes: %+v", tb.Indexes)
	}
	// relations in both directions, with what each rests on
	if len(tb.References) != 1 || tb.References[0].Kind != "implicit_ref" || tb.References[0].Method != "name_match" || tb.References[0].Confidence != 95 ||
		tb.References[0].Columns != "orders.user_id -> users.id" {
		t.Errorf("references: %+v", tb.References)
	}
	if len(tb.ReferencedBy) != 1 || tb.ReferencedBy[0].Table.ID != "table:order_items" || tb.ReferencedBy[0].Kind != "foreign_key" ||
		tb.ReferencedBy[0].Columns != "order_items.order_id -> orders.id" {
		t.Errorf("referenced by: %+v", tb.ReferencedBy)
	}
	if len(tb.ManyToMany) != 1 || tb.ManyToMany[0].Table.ID != "table:products" || tb.ManyToMany[0].Via != "table:order_items" || tb.ManyToMany[0].Method != "junction_pk" {
		t.Errorf("many to many: %+v", tb.ManyToMany)
	}
	if len(tb.Readers) != 2 || len(tb.Writers) != 1 || len(tb.MappedBy) != 1 || len(tb.Migrations) != 1 {
		t.Errorf("code and history: %d readers %d writers %d mapped %d migrations", len(tb.Readers), len(tb.Writers), len(tb.MappedBy), len(tb.Migrations))
	}
	if !strings.Contains(strings.Join(c.Notes, " "), "graph_impact") {
		t.Errorf("notes: %v", c.Notes)
	}
}

func TestContextOfASpecAndOtherKinds(t *testing.T) {
	m := fixtureGraph(t)

	c := ctxOf(t, m, ContextQuery{Path: "docs/specs/SPEC_HELLO.md"})
	if c.Node.ID != "spec:hello" || len(c.Items) != 6 {
		t.Fatalf("%+v", c)
	}
	byStatus := map[string]int{}
	for _, it := range c.Items {
		byStatus[it.Status]++
		if it.Status == "skipped" && it.WhySkipped == "" {
			t.Errorf("a skipped row says why: %+v", it)
		}
	}
	if byStatus["skipped"] == 0 || byStatus["implemented"] == 0 || byStatus["planned"] == 0 {
		t.Errorf("statuses: %v", byStatus)
	}
	if _, dup := c.Related["out:has_item"]; dup {
		t.Error("items are listed once")
	}

	// a kind with no section of its own gets its neighbours
	c = ctxOf(t, m, ContextQuery{Node: "server.port"})
	if c.Node.ID != "cfg:server.port" || c.Attrs["default"] != "8080" || c.Attrs["declared"] != true {
		t.Fatalf("%+v", c)
	}
	mustHave(t, "who uses it", ids(c.Related["in:uses_config"]), "func:..main", "pkg:.")
	mustHave(t, "what backs it", ids(c.Related["out:reads_env"]), "env:PORT")

	// a template
	c = ctxOf(t, m, ContextQuery{Symbol: "templates.Home"})
	if got := ids(c.RenderedBy); len(got) != 1 || got[0] != "func:controllers.HomePage" {
		t.Errorf("rendered by: %v", got)
	}
	mustHave(t, "renders", ids(c.Renders), "tmpl:assets/templates.Footer", "tmpl:assets/templates.Layout")
	if got := routeIDs(c.Routes); len(got) != 1 || got[0] != "route:GET:/" {
		t.Errorf("routes through the renderer: %v", got)
	}
}

func TestContextArgumentsAndErrors(t *testing.T) {
	m := fixtureGraph(t)
	for _, q := range []ContextQuery{{}, {Path: "a", Symbol: "b"}, {Table: "orders", Route: "GET /"}} {
		if _, err := m.Context(q); err == nil || !strings.Contains(err.Error(), "exactly one of") {
			t.Errorf("%+v: %v", q, err)
		}
	}
	// each argument resolves only its own kind of thing
	if _, err := m.Context(ContextQuery{Table: "controllers.HelloAPI"}); err == nil {
		t.Error("a symbol is not a table")
	}
	if _, err := m.Context(ContextQuery{Route: "controllers/hello.go"}); err == nil {
		t.Error("a path is not a route")
	}
	// an unindexed file says what is indexed
	_, err := m.Context(ContextQuery{Path: "controllers/hello_test.go"})
	if err == nil || !strings.Contains(err.Error(), "not tests") {
		t.Errorf("a test file: %v", err)
	}
	// an ambiguous symbol lists the candidates
	_, err = m.Context(ContextQuery{Symbol: "List"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous: %v", err)
	}
	// the same briefing whichever way the node is named
	a := ctxOf(t, m, ContextQuery{Symbol: "controllers.HelloAPI"})
	b := ctxOf(t, m, ContextQuery{Node: "func:controllers.HelloAPI"})
	c := ctxOf(t, m, ContextQuery{Path: "controllers/hello.go"})
	if a.Node.ID != b.Node.ID || a.Node.ID == c.Node.ID {
		t.Errorf("%s %s %s", a.Node.ID, b.Node.ID, c.Node.ID)
	}
}

func TestContextBoundsAHugePackage(t *testing.T) {
	m := freshGraph(t) // this test changes the model
	// pad a package with functions: the symbol list is capped, and the counts say how many there are
	pkg := m.nodes["pkg:controllers"]
	file := m.nodes["file:controllers/hello.go"]
	for i := 0; i < 100; i++ {
		id := "func:controllers.Pad" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		n := m.node(id)
		n.Kind = "func"
		n.Attrs["name"] = []any{id}
		n.Out["defined_in"] = []string{file.ID}
		file.In["defined_in"] = append(file.In["defined_in"], id)
	}
	m.pkgFuncs = nil
	c := ctxOf(t, m, ContextQuery{Node: pkg.ID})
	if len(c.Symbols) != maxSymbols || c.SymbolCounts["func"] < 100 {
		t.Errorf("%d symbols, counts %v", len(c.Symbols), c.SymbolCounts)
	}
}

// --- findings: the Phase 5 contract, exercised with findings written by hand ---

// addFindingsLayer writes what a findings layer would: kind:finding nodes with
// rule, severity, message and an `about` edge, and the layer's run node.
func addFindingsLayer(t *testing.T, h *cayley.Handle, fs ...[4]string) {
	t.Helper()
	label := quad.IRI("layer:findings")
	add := func(s, p string, o quad.Value, l quad.IRI) {
		if err := h.AddQuad(quad.Make(quad.IRI(s), quad.IRI(p), o, l)); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range fs { // id, rule/severity packed as "rule|severity", message, about
		parts := strings.SplitN(f[1], "|", 2)
		add(f[0], "type", quad.IRI("kind:finding"), label)
		add(f[0], "rule", quad.String(parts[0]), label)
		add(f[0], "severity", quad.String(parts[1]), label)
		add(f[0], "message", quad.String(f[2]), label)
		add(f[0], "about", quad.IRI(f[3]), label)
	}
	add("layer:findings", "type", quad.IRI("kind:layer"), "layer:meta")
	add("layer:findings", "status", quad.String("ok"), "layer:meta")
	add("layer:findings", "name", quad.String("findings"), "layer:meta")
}

func TestFindingsAreUnavailableUntilTheLayerExists(t *testing.T) {
	m := fixtureGraph(t)
	res := m.Findings(FindingsQuery{})
	if res.Available || len(res.Findings) != 0 || res.Findings == nil || !strings.Contains(res.Note, "not built yet") || !strings.Contains(res.Note, "does not mean the code is clean") {
		t.Errorf("%+v", res)
	}
	// and a context says so too
	c := ctxOf(t, m, ContextQuery{Symbol: "controllers.GetOrder"})
	if !strings.Contains(strings.Join(c.Notes, " "), "findings layer is not built") {
		t.Errorf("notes: %v", c.Notes)
	}
}

func TestFindingsAreFilteredRankedAndAttachedToTheirNodes(t *testing.T) {
	h, _ := fixtureStore(t)
	addFindingsLayer(t, h,
		[4]string{"finding:layering:func:controllers.GetOrder", "layering|error", "controller calls storage directly", "func:controllers.GetOrder"},
		[4]string{"finding:stale-doc:doc:README.md", "stale-doc|warning", "mentions a path that is gone", "doc:README.md"},
		[4]string{"finding:unused-config:cfg:mail.smtp.host", "unused-config|info", "declared, never read", "cfg:mail.smtp.host"},
		[4]string{"finding:layering:func:controllers.HomePage", "layering|error", "controller calls storage directly", "func:controllers.HomePage"},
	)
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}

	res := m.Findings(FindingsQuery{})
	if !res.Available || res.Total != 4 || res.Note != "" {
		t.Fatalf("%+v", res)
	}
	var order []string
	for _, f := range res.Findings {
		order = append(order, f.Severity)
	}
	if strings.Join(order, ",") != "error,error,warning,info" {
		t.Errorf("worst first: %v", order)
	}
	if res.BySeverity["error"] != 2 || res.ByRule["layering"] != 2 {
		t.Errorf("counts: %v %v", res.BySeverity, res.ByRule)
	}
	if f := res.Findings[0]; f.Rule != "layering" || f.About != "func:controllers.GetOrder" || f.Message == "" {
		t.Errorf("a finding: %+v", f)
	}

	if got := m.Findings(FindingsQuery{Severity: "ERROR"}); got.Total != 2 || got.BySeverity["info"] != 1 {
		t.Errorf("by severity: %+v", got)
	}
	if got := m.Findings(FindingsQuery{Rule: "stale-doc"}); got.Total != 1 {
		t.Errorf("by rule: %+v", got)
	}
	if got := m.Findings(FindingsQuery{Rule: "un*"}); got.Total != 1 || got.Findings[0].Rule != "unused-config" {
		t.Errorf("by rule prefix: %+v", got)
	}
	if got := m.Findings(FindingsQuery{Severity: "error", Rule: "stale-doc"}); got.Total != 0 || got.Findings == nil {
		t.Errorf("no match: %+v", got)
	}
	if got := m.Findings(FindingsQuery{Limit: 1}); len(got.Findings) != 1 || !got.Truncated || got.Total != 4 {
		t.Errorf("limit: %+v", got)
	}

	// the briefing of a node carries the findings about it
	c := ctxOf(t, m, ContextQuery{Symbol: "controllers.GetOrder"})
	if len(c.Findings) != 1 || c.Findings[0].Rule != "layering" {
		t.Errorf("findings in a context: %+v", c.Findings)
	}
	if strings.Contains(strings.Join(c.Notes, " "), "findings layer") {
		t.Errorf("no caveat once the layer exists: %v", c.Notes)
	}
	// and so does the file or package that holds it
	c = ctxOf(t, m, ContextQuery{Path: "controllers"})
	if len(c.Findings) != 2 {
		t.Errorf("findings about a package's members: %+v", c.Findings)
	}
}
