package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/codegraph/internal/graphcheck"
)

// The findings layer's complete output for the fixture, as N-Quads. The fixture
// breaks the rules on purpose: see the table in docs/public/GRAPH_SCHEMA.md.
func TestFindingsLayerGolden(t *testing.T) {
	h, _ := fixtureStore(t)
	checkGolden(t, "findings", storedLabel(t, h, "findings"))
}

// byRule indexes a model's findings: rule -> finding IDs.
func byRule(m *Model) map[string][]string {
	out := map[string][]string{}
	for _, f := range m.Kind("finding") {
		out[f.Str("rule")] = append(out[f.Str("rule")], f.ID)
	}
	return out
}

// What each rule finds on the fixture, asserted apart from the golden so that a
// wrongly regenerated golden is still caught.
func TestFindingsFixtureFacts(t *testing.T) {
	m := fixtureGraph(t)
	got := byRule(m)
	want := map[string][]string{
		"layering":                  {"finding:layering:pkg:controllers#pkg:storage/orderstore"},
		"docs.stale_ref":            {"finding:docs.stale_ref:doc:docs/public/ARCHITECTURE.md#14:legacy/handler.go"},
		"docs.spec_missing":         {"finding:docs.spec_missing:item:hello/audit-trail#16:business/actions/audit.go"},
		"config.undeclared":         {"finding:config.undeclared:cfg:feature.beta", "finding:config.undeclared:env:FIXTURE_DEBUG"},
		"config.unused":             {"finding:config.unused:cfg:auth.api_key", "finding:config.unused:cfg:database.max_conns", "finding:config.unused:cfg:env", "finding:config.unused:cfg:region", "finding:config.unused:cfg:unused.flag"},
		"db.missing_fk":             {"finding:db.missing_fk:col:orders.user_id#col:users.id"},
		"docs.command_undocumented": {"finding:docs.command_undocumented:command:cache:warm"},
		"docs.route_undocumented":   {"finding:docs.route_undocumented:route:ANY:/assets/", "finding:docs.route_undocumented:route:GET:/", "finding:docs.route_undocumented:route:GET:/api/orders/{id}"},
		"code.unreferenced":         {"finding:code.unreferenced:func:business/actions.Notify"},
	}
	for rule, ids := range want {
		mustHave(t, rule, got[rule], ids...)
		if len(got[rule]) != len(ids) {
			t.Errorf("%s: found %v, want exactly %v", rule, got[rule], ids)
		}
	}
	for rule := range got {
		if _, ok := want[rule]; !ok {
			t.Errorf("unexpected rule %s: %v", rule, got[rule])
		}
	}

	// The layering finding points at where the import is used, and says what to do.
	f := m.Node("finding:layering:pkg:controllers#pkg:storage/orderstore")
	if f.Str("severity") != "error" || f.first("about") != "pkg:controllers" || f.Str("path") != "controllers/orders.go" || f.Int("line") == 0 ||
		!strings.Contains(f.Str("message"), "only business actions and services may call storage") {
		t.Errorf("layering finding: %+v", f)
	}
	// The allow-list: the dev package imports storage and is not reported, nor is the
	// wiring package, nor business calling storage.
	for _, id := range got["layering"] {
		if strings.Contains(id, "devmcp") || strings.Contains(id, "pkg:routes") || strings.Contains(id, "business") {
			t.Errorf("an allowed import is reported: %s", id)
		}
	}
	// A stale reference carries its line; the row claims what is not there.
	if f := m.Node("finding:docs.stale_ref:doc:docs/public/ARCHITECTURE.md#14:legacy/handler.go"); f.Int("line") != 14 || f.Str("path") != "docs/public/ARCHITECTURE.md" {
		t.Errorf("stale ref: %+v", f)
	}
	if f := m.Node("finding:docs.spec_missing:item:hello/audit-trail#16:business/actions/audit.go"); f.Str("severity") != "warning" || f.Str("path") != "docs/specs/SPEC_HELLO.md" || !strings.Contains(f.Str("message"), "marks \"Audit trail\" implemented") {
		t.Errorf("spec row: %+v", f)
	}
	// Dev routes are not asked to be documented; the config reads that name nobody are.
	for _, id := range got["docs.route_undocumented"] {
		if strings.Contains(id, "/dev/") {
			t.Errorf("a dev route is reported: %s", id)
		}
	}
	// A config key that only a group read touches is used.
	for _, id := range got["config.unused"] {
		if strings.Contains(id, "queue.broker") {
			t.Errorf("a key read through its group is reported unused: %s", id)
		}
	}
	// The rules that compare with a live database have nothing to look at here, and
	// say so instead of reporting "no findings"; the layer is still fully built.
	for _, id := range []string{"rule:db.drift", "rule:db.unapplied"} {
		if r := m.Node(id); r == nil || r.Bool("checked") || !strings.Contains(r.Str("note"), "not applicable: no live database") {
			t.Errorf("%s has nothing to compare with: %+v", id, r)
		}
	}
	for _, id := range []string{"rule:layering", "rule:db.no_primary_key", "rule:config.unused"} {
		if r := m.Node(id); r == nil || !r.Bool("checked") {
			t.Errorf("%s ran on the fixture: %+v", id, r)
		}
	}
	if st := m.layerStatus("findings"); st != statusOK {
		t.Errorf("a rule that does not apply does not make the layer partial: %s", st)
	}
	if len(m.Kind("rule")) != len(ruleCatalog()) {
		t.Errorf("%d rule nodes, %d rules", len(m.Kind("rule")), len(ruleCatalog()))
	}
}

// A rule that reads a layer which is not built has not looked: it is recorded as
// not checked, with why, and the layer is partial. "No findings" is then not a clean bill.
func TestRulesWhoseLayersAreMissingAreNotChecked(t *testing.T) {
	h, _ := openStore(t)
	opts := Options{Root: fixtureRoot} // no router: the routes layer is skipped
	ix := New(h.QuadStore, opts)
	rep, err := ix.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var fl LayerReport
	for _, l := range rep.Layers {
		if l.Name == "findings" {
			fl = l
		}
	}
	if fl.Status != statusPartial || !strings.Contains(strings.Join(fl.Warnings, ";"), "rule docs.route_undocumented not checked: the routes layer is not built") {
		t.Errorf("findings layer: %+v", fl)
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Node("rule:docs.route_undocumented")
	if r == nil || r.Bool("checked") || !strings.Contains(r.Str("note"), "routes layer is not built") {
		t.Errorf("rule node: %+v", r)
	}
	res := m.Findings(FindingsQuery{})
	if !res.Available || len(res.uncheckedRules()) != 4 || !strings.Contains(res.Note, "docs.route_undocumented") || !strings.Contains(res.Note, "docs.command_undocumented") || !strings.Contains(res.Note, "did not run") {
		t.Errorf("an agent is told what was not checked: %+v", res)
	}
	if byRule(m)["docs.route_undocumented"] != nil {
		t.Error("an unchecked rule has no findings")
	}
	if len(byRule(m)["layering"]) != 1 {
		t.Errorf("rules whose layers are built still run: %v", byRule(m))
	}
}

// Findings are derived data: a second run changes nothing, and fixing the cause
// removes the finding on the next refresh.
func TestFindingsFollowTheSource(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if again, err := ix.Run(ctx); err != nil || again.Added != 0 || again.Removed != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	stale := `g.V(g.IRI('finding:docs.stale_ref:doc:docs/public/ARCHITECTURE.md#14:legacy/handler.go')).Out(g.IRI('about')).All()`
	wantIDs(t, "the stale reference is a finding", gizmoIDs(t, h, stale), "<doc:docs/public/ARCHITECTURE.md>")

	// Fixing the doc is a doc edit: docs and findings rebuild, and the finding is gone.
	writeFile(t, root, "docs/public/ARCHITECTURE.md", strings.ReplaceAll(readFile(t, root, "docs/public/ARCHITECTURE.md"), "`legacy/handler.go`", "`controllers/hello.go`"))
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil || strings.Join(ran(rep), ",") != "docs,findings" {
		t.Fatalf("refresh: %v %v", ran(rep), err)
	}
	wantIDs(t, "the stale reference is gone", gizmoIDs(t, h, stale))

	// Creating the file a doc names is not a doc edit: the docs layer notices through
	// the paths it looked for, and the finding that the file was missing goes away.
	appendTo(t, root, "docs/public/GUIDE.md", "\nSee `docs/extra/notes.md`.\n")
	if rep, err := ix.Refresh(ctx); err != nil || strings.Join(ran(rep), ",") != "docs,findings" {
		t.Fatalf("refresh after a mention: %v %v", ran(rep), err)
	}
	missing := `g.V().Has(g.IRI('rule'), 'docs.stale_ref').All()`
	if got := gizmoIDs(t, h, missing); len(got) != 1 || !strings.Contains(got[0], "docs/extra/notes.md") {
		t.Fatalf("the new reference is stale: %v", got)
	}
	writeFile(t, root, "docs/extra/notes.md", "# Notes\n")
	if stale, err := ix.Stale(ctx); err != nil || strings.Join(stale, ",") != "docs,findings" {
		t.Fatalf("creating the named file makes docs stale: %v %v", stale, err)
	}
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "the file now exists, so the reference is not stale", gizmoIDs(t, h, missing))
	if stale, _ := ix.Stale(ctx); len(stale) != 0 {
		t.Errorf("stale after refresh: %v", stale)
	}
}

// The layering rule, as data: what the rule says about each pair of layers.
func TestLayerVerdict(t *testing.T) {
	allowed := [][2]string{
		{"controllers", "business"}, {"controllers", "support"}, {"controllers", "views"}, {"controllers", "middleware"},
		{"business", "storage"}, {"business", "support"}, {"business", "business"},
		{"storage", "support"}, {"storage", "storage"},
		{"wiring", "storage"}, {"wiring", "controllers"}, {"dev", "storage"}, {"dev", "business"},
		{"middleware", "support"}, {"views", "views"},
		{"commands", "business"}, {"commands", "support"}, {"wiring", "commands"},
	}
	for _, p := range allowed {
		if why := layerVerdict(p[0], p[1]); why != "" {
			t.Errorf("%s -> %s should be allowed: %s", p[0], p[1], why)
		}
	}
	forbidden := [][2]string{
		{"controllers", "storage"}, {"middleware", "storage"}, {"views", "storage"}, {"support", "storage"},
		{"storage", "business"}, {"storage", "controllers"}, {"storage", "wiring"},
		{"business", "controllers"}, {"business", "middleware"}, {"business", "views"}, {"business", "wiring"}, {"business", "dev"},
		{"commands", "storage"}, {"business", "commands"}, {"storage", "commands"},
	}
	for _, p := range forbidden {
		if layerVerdict(p[0], p[1]) == "" {
			t.Errorf("%s -> %s should be forbidden", p[0], p[1])
		}
	}
}

// The unresolved-reference classifier: what counts as stale, and what must not.
func TestStaleReferenceClassification(t *testing.T) {
	w := fixtureWorld(t)
	ci, err := w.code()
	if err != nil {
		t.Fatal(err)
	}
	rt, _ := w.routeTable()
	ti, _ := w.templates()
	cx, _ := w.configIndex()
	dm, _ := w.dbModel()
	sc, _ := w.docScan()
	rs := newResolver(w.root, mustProfile(t, w), sc, ci, rt, nil, ti, cx, dm)

	for _, tok := range []string{
		"legacy/handler.go",       // a removed directory
		"controllers/gone.go",     // a removed file in a directory that exists
		"controllers/gone.go:12",  // with a line
		"controllers.Gone",        // a symbol the package no longer has
		"storage/orderstore.Nope", // dir.Name that is gone
		"docs/public/NOPE.md",
		"../x/y.sql",
	} {
		if _, ok := rs.staleToken(tok, "docs/public"); !ok {
			t.Errorf("%q should be stale", tok)
		}
	}
	for _, tok := range []string{
		"controllers/orders.go",    // there
		"controllers.GetOrder",     // there
		"controllers.HelloAPI()",   // there, with parens
		"storage/orderstore.Store", // dir.Name that exists
		"net/http",                 // an import path, not a file
		"application/json",         // a MIME type
		"example.com/fixture/x.go", // a module path
		"https://example.com/x.go", // a URL
		"docs/*.md",                // a pattern
		"controllers/<name>.go",    // a placeholder
		"GET /api/hello",           // routes are never judged
		"func:controllers.Gone",    // node IDs are never judged
		"FIXTURE_NOPE",             // nor environment variables
		"cfg.Server.Port",          // nor selector chains
		"http.StatusOK",            // a library's symbol: no package of that name here
		"orders.Service.Gone",      // three parts: not judged
		"config.yaml",              // `config` is a package, but a lower-case member is not a symbol
		"README.md",                // no slash: a bare file name is not judged
	} {
		if s, ok := rs.staleToken(tok, "docs/public"); ok {
			t.Errorf("%q must not be stale (got %q)", tok, s)
		}
	}
	// links
	if _, ok := rs.staleLink("GUIDE.md", "docs/public"); ok {
		t.Error("a link to a page that exists is fine")
	}
	if _, ok := rs.staleLink("MISSING.md#top", "docs/public"); !ok {
		t.Error("a link to a page that does not exist is stale")
	}
	if _, ok := rs.staleLink("/dev/docs/specs/nope", "docs/public"); !ok {
		t.Error("a link to a docs page that does not exist is stale")
	}
	if _, ok := rs.staleLink("https://example.com/a.md", "docs/public"); ok {
		t.Error("an external link is never judged")
	}
	if _, ok := rs.staleLink("/api/anything", "docs/public"); ok {
		t.Error("an absolute link to a route is never judged")
	}
}

// The invariants of the findings nodes themselves, over the fixture with a live
// schema that drifts (see TestFixtureGraphInvariants for the graph as a whole).
func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFindingNodesAreWellFormed(t *testing.T) {
	h, _ := fixtureStore(t)
	graphcheck.Check(t, h, graphcheck.Options{Layers: []string{"code", "routes", "commands", "templates", "config", "mcp", "db", "docs", "externals", "findings"}})
	for _, q := range storedLabel(t, h, "findings") {
		if q.Label != quad.Value(LayerLabel("findings")) {
			t.Errorf("a finding quad outside its layer: %v", q)
		}
	}
}
