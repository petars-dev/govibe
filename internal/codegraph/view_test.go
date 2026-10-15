package codegraph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/quad"
)

func viewOf(t *testing.T, m *Model, q ViewQuery) *ViewResult {
	t.Helper()
	v, err := m.View(q)
	if err != nil {
		t.Fatalf("view %+v: %v", q, err)
	}
	// Whatever the view, its edges join nodes it holds, and every node counts once.
	have := map[string]ViewNode{}
	for _, n := range v.Nodes {
		if _, dup := have[n.ID]; dup {
			t.Errorf("%s: %s twice", v.Preset, n.ID)
		}
		have[n.ID] = n
	}
	for _, e := range v.Edges {
		if _, ok := have[e.Source]; !ok {
			t.Errorf("%s: edge from a node the view lacks: %+v", v.Preset, e)
		}
		if _, ok := have[e.Target]; !ok {
			t.Errorf("%s: edge to a node the view lacks: %+v", v.Preset, e)
		}
	}
	if len(v.Nodes) > v.Limit {
		t.Errorf("%s: %d nodes over the limit %d", v.Preset, len(v.Nodes), v.Limit)
	}
	counted := 0
	for _, k := range v.Kinds {
		counted += k.Count
	}
	if counted != len(v.Nodes) {
		t.Errorf("%s: kinds count %d nodes, the view has %d", v.Preset, counted, len(v.Nodes))
	}
	return v
}

func (v *ViewResult) node(id string) *ViewNode {
	for i := range v.Nodes {
		if v.Nodes[i].ID == id {
			return &v.Nodes[i]
		}
	}
	return nil
}

func (v *ViewResult) edge(from, pred, to string) *ViewEdge {
	for i := range v.Edges {
		if e := &v.Edges[i]; e.Source == from && e.Predicate == pred && e.Target == to {
			return e
		}
	}
	return nil
}

func TestPresetViewsOfTheFixture(t *testing.T) {
	m := fixtureGraph(t)

	arch := viewOf(t, m, ViewQuery{Preset: "architecture"})
	if arch.Preset != "architecture" || strings.Join(arch.Columns, ",") != "wiring,dev,controllers,commands,middleware,business,storage,support" || len(arch.Nodes) != 11 {
		t.Fatalf("architecture: %s %v %d", arch.Preset, arch.Columns, len(arch.Nodes))
	}
	if e := arch.edge("pkg:controllers", "imports", "pkg:storage/orderstore"); e == nil || !e.Violation {
		t.Errorf("the controllers -> storage import is drawn as a violation: %+v", e)
	}
	for _, ok := range [][2]string{{"pkg:devmcp", "pkg:storage/orderstore"}, {"pkg:routes", "pkg:storage/orderstore"}, {"pkg:business/services/orders", "pkg:storage/orderstore"}} {
		if e := arch.edge(ok[0], "imports", ok[1]); e == nil || e.Violation {
			t.Errorf("%v is allowed: %+v", ok, e)
		}
	}
	if n := arch.node("pkg:controllers"); n == nil || n.Findings != 1 || n.Worst != "error" || n.Group != "controllers" || n.Col != 2 {
		t.Errorf("the package carries its finding and its layer's column: %+v", n)
	}

	flow := viewOf(t, m, ViewQuery{Preset: "request_flow"})
	if flow.edge("route:GET:/api/hello", "handled_by", "func:controllers.HelloAPI") == nil ||
		flow.edge("func:controllers.HomePage", "renders", "tmpl:assets/templates.Home") == nil ||
		flow.edge("mw:middleware.RequestLogger", "implemented_by", "func:middleware.RequestLogger") == nil ||
		flow.edge("route:GET:/api/hello", "uses_mw", "mw:middleware.RequestLogger") == nil {
		t.Errorf("request flow edges: %+v", flow.Edges)
	}
	if n := flow.node("route:ANY:/dev/mcp"); n == nil || n.Flag != "dev" {
		t.Errorf("a dev route says so: %+v", n)
	}
	// application routes come before dev routes, so a cap drops dev ones first
	var seenDev bool
	for _, n := range flow.Nodes {
		if n.Kind == "route" {
			if n.Flag == "dev" {
				seenDev = true
			} else if seenDev {
				t.Errorf("app route %s after a dev route", n.ID)
			}
		}
	}

	er := viewOf(t, m, ViewQuery{Preset: "db_er"})
	if len(er.Nodes) != 4 || er.edge("table:order_items", "fk_to", "table:orders") == nil || len(er.Columns) != 0 {
		t.Errorf("er: %d nodes %+v", len(er.Nodes), er.Edges)
	}
	if e := er.edge("table:orders", "implicit_ref", "table:users"); e == nil || !e.Inferred || e.Confidence != 95 {
		t.Errorf("an inferred relation is marked, with how sure: %+v", e)
	}
	if e := er.edge("table:orders", "many_to_many", "table:products"); e == nil || !e.Inferred || er.edge("table:products", "many_to_many", "table:orders") != nil {
		t.Errorf("many-to-many is drawn once: %+v", e)
	}

	cov := viewOf(t, m, ViewQuery{Preset: "docs_coverage"})
	for id, want := range map[string]string{"pkg:config": "uncovered", "pkg:middleware": "uncovered", "route:GET:/": "uncovered", "pkg:controllers": "", "table:orders": ""} {
		if n := cov.node(id); n == nil || n.Flag != want {
			t.Errorf("coverage of %s: %+v, want flag %q", id, n, want)
		}
	}
	if cov.edge("doc:docs/public/ARCHITECTURE.md", "mentions", "pkg:storage/orderstore") == nil {
		t.Error("a doc that names a file covers its package")
	}
	if cov.node("route:ANY:/dev/mcp") != nil {
		t.Error("dev routes are not judged route by route")
	}
	if first := cov.Nodes[len(cov.Nodes)-len(cov.Nodes)+4]; first.Flag != "uncovered" {
		t.Errorf("what nothing mentions comes first (after the docs): %+v", first)
	}

	cfg := viewOf(t, m, ViewQuery{Preset: "config_usage"})
	for id, want := range map[string]string{"cfg:database.dsn": "sensitive", "env:FIXTURE_DSN": "sensitive", "cfg:feature.beta": "undeclared", "env:FIXTURE_DEBUG": "undeclared", "cfg:server.port": ""} {
		if n := cfg.node(id); n == nil || n.Flag != want {
			t.Errorf("config %s: %+v, want flag %q", id, n, want)
		}
	}
	if cfg.edge("pkg:.", "uses_config", "cfg:server.port") == nil || cfg.edge("cfg:server.port", "reads_env", "env:PORT") == nil || cfg.edge("pkg:.", "reads_env", "env:FIXTURE_DEBUG") == nil {
		t.Errorf("config edges: %+v", cfg.Edges)
	}

	fs := viewOf(t, m, ViewQuery{Preset: "findings"})
	if fs.Nodes[0].Kind != "finding" || fs.Nodes[0].Group != "error" || fs.edge(fs.Nodes[0].ID, "about", "pkg:controllers") == nil {
		t.Errorf("the worst finding leads and points at its node: %+v", fs.Nodes[0])
	}
	if len(fs.Columns) != 2 || fs.Total != len(fs.Nodes) {
		t.Errorf("findings view: %v %d/%d", fs.Columns, len(fs.Nodes), fs.Total)
	}
}

func TestFocusViewIsABoundedNeighbourhood(t *testing.T) {
	m := fixtureGraph(t)
	v := viewOf(t, m, ViewQuery{Focus: "controllers/orders.go"})
	if v.Preset != "focus" || v.Focus == nil || v.Focus.ID != "file:controllers/orders.go" || v.Node("file:controllers/orders.go").Dist != 0 {
		t.Fatalf("focus: %+v", v.Focus)
	}
	if v.edge("func:controllers.GetOrder", "defined_in", "file:controllers/orders.go") == nil || v.edge("file:controllers/orders.go", "in_package", "pkg:controllers") == nil {
		t.Errorf("a file's neighbours: %+v", v.Edges)
	}
	if n := v.node("pkg:controllers"); n == nil || n.Dist != 1 {
		t.Errorf("distance: %+v", n)
	}
	// deeper reaches further, and the focus node survives a kind filter
	deep := viewOf(t, m, ViewQuery{Focus: "controllers/orders.go", Depth: 2})
	if len(deep.Nodes) <= len(v.Nodes) || deep.node("pkg:storage/orderstore") == nil {
		t.Errorf("depth 2: %d nodes vs %d", len(deep.Nodes), len(v.Nodes))
	}
	only := viewOf(t, m, ViewQuery{Focus: "controllers/orders.go", Depth: 2, Kinds: []string{"func"}})
	if only.node("file:controllers/orders.go") == nil || only.node("pkg:controllers") != nil || only.node("func:controllers.GetOrder") == nil {
		t.Errorf("kind filter keeps the focus: %+v", only.Nodes)
	}
	// a hub shows a few of its edges and says how many it left out
	hub := viewOf(t, m, ViewQuery{Focus: "route:GET:/api/hello", Depth: 1})
	if hub.node("mw:middleware.RequestLogger") == nil {
		t.Errorf("a route's middleware: %+v", hub.Nodes)
	}
	// references work the way they do for graph_context
	if v := viewOf(t, m, ViewQuery{Focus: "GET /api/hello"}); v.Focus.ID != "route:GET:/api/hello" {
		t.Errorf("a route reference: %+v", v.Focus)
	}
	if v := viewOf(t, m, ViewQuery{Focus: "orders"}); v.Focus.ID != "table:orders" {
		t.Errorf("a bare table name: %+v", v.Focus)
	}
	// bookkeeping stays out unless it is what you asked about
	for _, n := range viewOf(t, m, ViewQuery{Focus: "table:orders", Depth: 2}).Nodes {
		if n.Kind == "annotation" {
			t.Errorf("an annotation is not part of the picture: %+v", n)
		}
	}
	if v := viewOf(t, m, ViewQuery{Focus: "build:latest"}); len(v.Nodes) < 2 {
		t.Errorf("the build node shows its layer runs: %+v", v.Nodes)
	}
}

func (v *ViewResult) Node(id string) *ViewNode { return v.node(id) }

func TestViewErrorsAreHelpful(t *testing.T) {
	m := fixtureGraph(t)
	if _, err := m.View(ViewQuery{Preset: "pie_chart"}); err == nil || !strings.Contains(err.Error(), "presets: app_map, architecture, request_flow") {
		t.Errorf("unknown preset: %v", err)
	}
	if _, err := m.View(ViewQuery{Preset: "manual"}); err == nil {
		t.Error("the manual preset is not drawn from the model")
	}
	if _, err := m.View(ViewQuery{Focus: "nope.Nope"}); err == nil || !strings.Contains(err.Error(), "no node matches") {
		t.Errorf("unknown focus: %v", err)
	}
	if _, err := m.View(ViewQuery{Preset: "architecture", Kinds: []string{"banana"}}); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("unknown kind: %v", err)
	}
	// an empty graph is an answer, not an error
	h, _ := openStore(t)
	empty, _ := Load(context.Background(), h.QuadStore)
	if v := viewOf(t, empty, ViewQuery{}); len(v.Nodes) != 0 || len(v.Notes) == 0 || len(v.Presets) != len(ViewPresets) {
		t.Errorf("empty graph: %+v", v)
	}
	// a layer that is not built says so instead of drawing nothing
	noRoutes := modelWithout(t, "routes")
	if v := viewOf(t, noRoutes, ViewQuery{Preset: "request_flow"}); len(v.Nodes) != 0 || !strings.Contains(strings.Join(v.Notes, " "), "routes layer is not built") {
		t.Errorf("request flow without routes: %+v", v.Notes)
	}
	if v := viewOf(t, modelWithout(t, "db"), ViewQuery{Preset: "db_er"}); len(v.Nodes) != 0 || len(v.Notes) == 0 {
		t.Errorf("er without tables: %+v", v.Notes)
	}
	if v := viewOf(t, modelWithout(t, "findings"), ViewQuery{Preset: "findings"}); len(v.Nodes) != 0 || !strings.Contains(strings.Join(v.Notes, " "), "nothing has been checked") {
		t.Errorf("findings without the layer: %+v", v.Notes)
	}
}

func TestViewLimitAndKindFilter(t *testing.T) {
	m := fixtureGraph(t)
	v := viewOf(t, m, ViewQuery{Preset: "config_usage", Limit: 5})
	if len(v.Nodes) != 5 || !v.Truncated || v.Total <= 5 || !strings.Contains(strings.Join(v.Notes, " "), "Showing 5 of") {
		t.Errorf("a cap says so: %d nodes, total %d, %v", len(v.Nodes), v.Total, v.Notes)
	}
	k := viewOf(t, m, ViewQuery{Preset: "config_usage", Kinds: []string{"cfg", "env"}})
	for _, n := range k.Nodes {
		if n.Kind != "config_key" && n.Kind != "env_var" {
			t.Errorf("kind filter (aliases work): %+v", n)
		}
	}
	if len(k.Edges) == 0 || k.edge("cfg:server.port", "reads_env", "env:PORT") == nil {
		t.Error("edges between the kept kinds stay")
	}
	if viewOf(t, m, ViewQuery{Limit: 100000}).Limit != maxViewLimit {
		t.Error("the limit is clamped")
	}
}

// ManualView shows the quads the indexer does not own.
func TestManualViewShowsOnlyHandAddedQuads(t *testing.T) {
	h, _ := fixtureStore(t)
	empty, err := ManualView(context.Background(), h.QuadStore, 0)
	if err != nil || len(empty.Nodes) != 0 || len(empty.Notes) != 1 || empty.Total != 0 {
		t.Fatalf("nothing added yet: %+v %v", empty, err)
	}
	for _, q := range []quad.Quad{
		quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil),
		quad.Make(quad.String("bob"), quad.String("knows"), quad.String("carol"), quad.String("layer:code")), // a string label is not the indexer's
		quad.Make(quad.IRI("pkg:controllers"), quad.IRI("note"), quad.String("hand-written"), quad.IRI("mine")),
	} {
		if err := h.AddQuad(q); err != nil {
			t.Fatal(err)
		}
	}
	v, err := ManualView(context.Background(), h.QuadStore, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v.Total != 3 || len(v.Nodes) != 5 || len(v.Edges) != 3 || v.Preset != "manual" {
		t.Errorf("manual view: %d quads, %d nodes, %d edges", v.Total, len(v.Nodes), len(v.Edges))
	}
	if v.edge("alice", "knows", "bob") == nil {
		t.Errorf("edges: %+v", v.Edges)
	}
	if capped, _ := ManualView(context.Background(), h.QuadStore, 1); len(capped.Edges) != 1 || !capped.Truncated || capped.Total != 3 {
		t.Errorf("a cap on quads: %+v", capped)
	}
}

func TestInspectorOfRoutesTablesAndSpecs(t *testing.T) {
	m := fixtureGraph(t)
	o := LinkOptions{Root: "/work/fixture", DB: true}

	r, err := m.Inspect("GET /api/hello", o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Node.ID != "route:GET:/api/hello" || r.Label != "GET /api/hello" {
		t.Errorf("route: %+v", r.Node)
	}
	attrs := map[string]any{}
	for _, a := range r.Attrs {
		attrs[a.Name] = a.Value
	}
	if attrs["method"] != "GET" || attrs["dev_only"] != false || r.Attrs[0].Name != "name" {
		t.Errorf("attributes, name first: %+v", r.Attrs)
	}
	var handled *EdgeGroup
	for i, g := range r.Out {
		if g.Predicate == "handled_by" {
			handled = &r.Out[i]
		}
	}
	if handled == nil || handled.Nodes[0].ID != "func:controllers.HelloAPI" || handled.Nodes[0].Path != "controllers/hello.go" {
		t.Errorf("handled_by: %+v", r.Out)
	}
	links := map[string]string{}
	for _, l := range r.Links {
		links[l.Kind] = l.URL
	}
	if links["editor"] != "vscode://file/work/fixture/controllers/hello.go:10" || links["graph"] != "/dev/graph#node=route%3AGET%3A%2Fapi%2Fhello" ||
		!strings.HasPrefix(links["report"], "/dev/graph/reports/routes#") {
		t.Errorf("links: %v", links)
	}
	if r.Links[0].Kind != "graph" {
		t.Errorf("the graph link leads: %+v", r.Links)
	}

	tb, err := m.Inspect("table:orders", o)
	if err != nil {
		t.Fatal(err)
	}
	var db, report bool
	for _, l := range tb.Links {
		db = db || (l.Kind == "db" && l.URL == "/dev/db#table=orders")
		report = report || (l.Kind == "report" && strings.HasPrefix(l.URL, "/dev/graph/reports/schema#"))
	}
	if !db || !report {
		t.Errorf("table links: %+v", tb.Links)
	}
	if noDB, _ := m.Inspect("table:orders", LinkOptions{}); func() bool {
		for _, l := range noDB.Links {
			if l.Kind == "db" || l.Kind == "editor" {
				return true
			}
		}
		return false
	}() {
		t.Error("no database explorer and no root: no db or editor links")
	}
	// an inferred edge's bookkeeping node is not listed among a table's relations
	for _, g := range append(tb.Out, tb.In...) {
		for _, n := range g.Nodes {
			if n.Kind == "annotation" {
				t.Errorf("annotation listed: %+v", g)
			}
		}
	}

	sp, _ := m.Inspect("spec:hello", o)
	var specPage string
	for _, l := range sp.Links {
		if l.Kind == "docs" {
			specPage = l.URL
		}
	}
	if specPage != "/dev/docs/specs/hello" {
		t.Errorf("spec page: %q", specPage)
	}
	doc, _ := m.Inspect("docs/public/ARCHITECTURE.md", o)
	if len(doc.Findings) != 1 || doc.Findings[0].Rule != "docs.stale_ref" {
		t.Errorf("findings about the doc: %+v", doc.Findings)
	}
	pkg, _ := m.Inspect("controllers", o)
	if len(pkg.Findings) != 1 || pkg.Findings[0].Severity != "error" {
		t.Errorf("findings about the package: %+v", pkg.Findings)
	}
	// the dsn key carries no default, so there is nothing to leak
	dsn, _ := m.Inspect("cfg:database.dsn", o)
	for _, a := range dsn.Attrs {
		if a.Name == "default" || strings.Contains(fmt.Sprint(a.Value), "user:pass") {
			t.Errorf("a sensitive key shows a default: %+v", a)
		}
	}
	if _, err := m.Inspect("nope.Nope", o); err == nil {
		t.Error("an unknown node is an error")
	}
	// a group is cut, and says so
	tools, _ := m.Inspect("build:latest", o)
	if len(tools.Out) == 0 {
		t.Errorf("the build lists its layer runs: %+v", tools)
	}
}

func TestStatusSummarisesFreshnessAndFindings(t *testing.T) {
	m := fixtureGraph(t)
	s := m.Status([]string{"docs"})
	if !s.Freshness.Indexed || s.Freshness.Quads == 0 || s.Freshness.Added == 0 || s.Freshness.IndexerVersion != IndexerVersion || len(s.Freshness.StaleLayers) != 1 {
		t.Errorf("freshness: %+v", s.Freshness)
	}
	f := s.Findings
	if !f.Available || f.Error != 1 || f.Warning != 4 || f.Info != 11 || f.Total != 16 || strings.Join(f.NotRun, ",") != "db.drift,db.unapplied" {
		t.Errorf("finding counts: %+v", f)
	}
	if len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "Stale layers: docs") {
		t.Errorf("notes: %v", s.Notes)
	}
	if s := modelWithout(t, "findings").Status(nil); s.Findings.Available {
		t.Errorf("no findings layer: %+v", s.Findings)
	}
	h, _ := openStore(t)
	empty, _ := Load(context.Background(), h.QuadStore)
	if s := empty.Status(nil); s.Freshness.Indexed || len(s.Notes) == 0 {
		t.Errorf("empty: %+v", s)
	}
}

// --- the visualizer stays responsive however big the graph gets ---

// bigModel is a graph with many thousands of quads: packages that import each
// other, files and functions in them, and a finding on a share of the functions.
func bigModel(packages, filesPer, funcsPer int) *Model {
	label := LayerLabel("code")
	var qs []quad.Quad
	add := func(s, p string, o quad.Value) {
		qs = append(qs, quad.Quad{Subject: quad.IRI(s), Predicate: quad.IRI(p), Object: o, Label: label})
	}
	layers := []string{"controllers", "business", "storage", "support", "middleware"}
	for p := 0; p < packages; p++ {
		pid := fmt.Sprintf("pkg:p%03d", p)
		add(pid, "type", KindPackage)
		add(pid, "name", quad.String(fmt.Sprintf("p%03d", p)))
		add(pid, "path", quad.String(fmt.Sprintf("p%03d", p)))
		add(pid, "arch_layer", quad.String(layers[p%len(layers)]))
		for k := 1; k <= 3; k++ {
			add(pid, "imports", quad.IRI(fmt.Sprintf("pkg:p%03d", (p+k)%packages)))
		}
		for f := 0; f < filesPer; f++ {
			fid := fmt.Sprintf("file:p%03d/f%03d.go", p, f)
			add(fid, "type", KindFile)
			add(fid, "name", quad.String(fmt.Sprintf("f%03d.go", f)))
			add(fid, "path", quad.String(fmt.Sprintf("p%03d/f%03d.go", p, f)))
			add(fid, "in_package", quad.IRI(pid))
			for n := 0; n < funcsPer; n++ {
				id := fmt.Sprintf("func:p%03d.F%03d_%03d", p, f, n)
				add(id, "type", KindFunc)
				add(id, "name", quad.String(fmt.Sprintf("F%03d_%03d", f, n)))
				add(id, "defined_in", quad.IRI(fid))
				add(id, "line", quad.Int(n+1))
				add(id, "exported", quad.Bool(true))
				if n%3 == 0 {
					fnd := "finding:code.unreferenced:" + id
					add(fnd, "type", KindFinding)
					add(fnd, "rule", quad.String("code.unreferenced"))
					add(fnd, "severity", quad.String("info"))
					add(fnd, "message", quad.String("nothing references it"))
					add(fnd, "about", quad.IRI(id))
				}
			}
		}
	}
	// the findings layer ran, so an empty findings view would mean "none"
	for _, q := range []quad.Quad{
		quad.Make(quad.IRI("layer:findings"), quad.IRI("type"), KindLayer, LayerLabel(metaLayer)),
		quad.Make(quad.IRI("layer:findings"), quad.IRI("status"), quad.String(statusOK), LayerLabel(metaLayer)),
	} {
		qs = append(qs, q)
	}
	return modelOf(qs)
}

func TestViewsStayBoundedOnABigGraph(t *testing.T) {
	m := bigModel(60, 20, 12) // ~ 90,000 quads
	if m.Quads() < 50000 {
		t.Fatalf("only %d quads", m.Quads())
	}
	for _, q := range []ViewQuery{
		{Preset: "architecture"}, {Preset: "findings"}, {Preset: "config_usage"},
		{Focus: "pkg:p000", Depth: 3}, {Focus: "file:p007/f003.go", Depth: 3}, {Focus: "pkg:p012", Depth: 2, Limit: 1500},
	} {
		start := time.Now()
		v := viewOf(t, m, q)
		took := time.Since(start)
		t.Logf("%-14s focus=%-18s %4d nodes %5d edges  total %5d  %s", v.Preset, q.Focus, len(v.Nodes), len(v.Edges), v.Total, took.Round(time.Millisecond))
		if len(v.Nodes) > v.Limit || len(v.Edges) > 4*v.Limit {
			t.Errorf("unbounded: %d nodes, %d edges", len(v.Nodes), len(v.Edges))
		}
		if took > 2*time.Second {
			t.Errorf("a view of %d quads took %s", m.Quads(), took)
		}
	}
	// the findings view is cut at the limit, and says what it left out
	v := viewOf(t, m, ViewQuery{Preset: "findings"})
	if !v.Truncated || v.Total <= v.Limit {
		t.Errorf("findings cut: total %d, shown %d", v.Total, len(v.Nodes))
	}
	if v := viewOf(t, m, ViewQuery{Focus: "pkg:p000", Depth: 1}); v.node("pkg:p000").More == 0 {
		t.Errorf("a hub says how many edges it left out: %+v", v.node("pkg:p000"))
	}
}

func TestTheAppMapIsTheDefaultViewAndShowsTheAppsOwnConnections(t *testing.T) {
	m := fixtureGraph(t).scoped(ScopeApp)
	v := viewOf(t, m, ViewQuery{})
	if v.Preset != "app_map" || v.Title != "App map" || v.Scope != ScopeApp || v.Hidden.Nodes == 0 {
		t.Fatalf("default view: %s %q scope=%s hidden=%+v", v.Preset, v.Title, v.Scope, v.Hidden)
	}
	if strings.Join(v.Columns, ",") != "route, command,handler,actions, services, stores,tables,templates, settings, docs" {
		t.Errorf("columns = %q", v.Columns)
	}
	if ViewPresets[0].ID != "app_map" {
		t.Errorf("the app map is the first preset: %v", ViewPresets[0])
	}

	// a route, its handler, the packages it reaches (estimated from imports: dashed), the table
	const route, handler = "route:GET:/api/orders", "func:controllers.ListOrders"
	for _, id := range []string{route, handler, "pkg:business/services/orders", "pkg:storage/orderstore", "table:orders"} {
		if v.node(id) == nil {
			t.Errorf("the app map lacks %s", id)
		}
	}
	if e := v.edge(route, "handled_by", handler); e == nil {
		t.Error("a route is linked to its handler")
	}
	if e := v.edge(handler, "imports", "pkg:business/services/orders"); e == nil || !e.Inferred {
		t.Errorf("without the calls layer, reach is an estimate from imports, drawn dashed: %+v", e)
	}
	if e := v.edge("pkg:storage/orderstore", "reads_table", "table:orders"); e == nil || !e.Inferred {
		t.Errorf("the table is reached through the store: %+v", e)
	}
	for _, n := range v.Nodes {
		if n.Origin != OriginApp {
			t.Errorf("%s is %s in the app's own scope", n.ID, n.Origin)
		}
		if n.Kind == "route" && strings.HasPrefix(n.Label, "ANY /dev") {
			t.Errorf("a /dev route in the app map: %s", n.ID)
		}
		if n.Kind == "middleware" || n.Kind == "mcp_tool" {
			t.Errorf("the app map is not the request flow: %s", n.ID)
		}
	}

	// with both scopes the framework's routes appear too, labelled, after the app's own
	all := viewOf(t, fixtureGraph(t), ViewQuery{})
	var fw []string
	for _, n := range all.Nodes {
		if n.Kind == "route" && n.Origin == OriginFramework {
			fw = append(fw, n.ID)
		}
	}
	if len(fw) == 0 {
		t.Errorf("scope all labels the framework's routes: %v", all.Nodes)
	}
}
