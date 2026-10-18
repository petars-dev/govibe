package codegraph

import (
	"encoding/json"
	"strings"
	"testing"
)

// byID indexes an impact result for assertions.
func byID(res *ImpactResult) map[string]Affected {
	out := map[string]Affected{}
	for _, a := range res.Affected {
		out[a.ID] = a
	}
	return out
}

func affectedIDs(res *ImpactResult) []string {
	var out []string
	for _, a := range res.Affected {
		out = append(out, a.ID)
	}
	return out
}

func impact(t *testing.T, m *Model, q ImpactQuery) *ImpactResult {
	t.Helper()
	res, err := m.Impact(q)
	if err != nil {
		t.Fatalf("impact %+v: %v", q, err)
	}
	return res
}

// "What breaks if I change table X": one call gives the code that reads and
// writes it (exact), the tables that reference it, and the routes and tools
// that can reach that code through package imports (estimated, and labelled so).
func TestImpactOfATable(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "orders"})
	got := byID(res)

	if res.Target.ID != "table:orders" || res.SeedCount != 1 {
		t.Fatalf("target: %+v", res)
	}
	// exact: the SQL that touches the table, its row type, and who references it
	for id, want := range map[string]struct {
		via, method, access string
		conf                int
	}{
		"func:storage/orderstore.Store.List":   {"reads_table", methodSQL, "read", 90},
		"func:storage/orderstore.Store.Get":    {"reads_table", methodSQL, "read", 90},
		"func:storage/orderstore.Store.Create": {"writes_table", methodSQL, "write", 90},
		"typ:storage/orderstore.Order":         {"maps_to", methodDBTag, "", 90},
		"table:order_items":                    {"fk_to", methodEdge, "", 100},
	} {
		a, ok := got[id]
		if !ok {
			t.Errorf("%s is not affected", id)
			continue
		}
		if a.Via != want.via || a.Method != want.method || a.Access != want.access || a.Confidence != want.conf || a.Depth != 1 || a.From != "table:orders" {
			t.Errorf("%s: %+v, want %+v", id, a, want)
		}
	}
	// a table that references orders by name only is not affected by orders; orders
	// references users, so a change to orders does not reach users
	mustNotHave(t, "dependents of orders", affectedIDs(res), "table:users", "table:products", "func:business/services/orders.Service.List")

	// estimated: importers of the package that touches it, then the routes and tools there
	for id, via := range map[string]string{
		"pkg:business/services/orders": "imports", "pkg:controllers": "imports", "pkg:devmcp": "imports", "pkg:routes": "imports", "pkg:.": "imports",
		"route:GET:/api/orders": "handled_by", "route:GET:/api/orders/{id}": "handled_by", "route:ANY:/dev/mcp": "handled_by",
		"tool:fixture_echo": "registers", "tool:fixture_ping": "registers",
	} {
		a, ok := got[id]
		if !ok {
			t.Errorf("%s is not reached through package imports", id)
			continue
		}
		wantConf := 50
		if a.Kind == "package" {
			wantConf = 60
		}
		if a.Via != via || a.Method != methodPkgImport || a.Confidence != wantConf {
			t.Errorf("%s: %+v", id, a)
		}
		// the chain that justifies it ends at the package that touches the table
		if len(a.Through) < 2 || a.Through[len(a.Through)-1] != "pkg:storage/orderstore" {
			t.Errorf("%s through = %v", id, a.Through)
		}
		// package-level reach cannot tell a reader from a writer: orderstore does both
		if a.Access != "read+write" {
			t.Errorf("%s access = %q", id, a.Access)
		}
	}
	if got["pkg:."].Depth != 2 || strings.Join(got["pkg:."].Through, ">") != "pkg:.>pkg:routes>pkg:storage/orderstore" {
		t.Errorf("a chain through routes: %+v", got["pkg:."])
	}

	// strongest evidence first
	for i := 1; i < len(res.Affected); i++ {
		if res.Affected[i-1].Confidence < res.Affected[i].Confidence {
			t.Errorf("not sorted by confidence at %d: %s then %s", i, res.Affected[i-1].ID, res.Affected[i].ID)
		}
	}
	if res.ByMethod[methodSQL] != 3 || res.ByKind["route"] < 4 || res.Total != len(res.Affected) || res.Truncated {
		t.Errorf("summary: %+v %+v total=%d", res.ByKind, res.ByMethod, res.Total)
	}

	// docs to update, and what the answer cannot see
	if len(res.Docs) == 0 || res.Docs[0].ID != "doc:docs/public/ARCHITECTURE.md" || len(res.Docs[0].Mentions) < 2 {
		t.Errorf("docs: %+v", res.Docs)
	}
	notes := strings.Join(res.Notes, "\n")
	if !strings.Contains(notes, "calls") || !strings.Contains(notes, "SQL built at run time") {
		t.Errorf("notes: %s", notes)
	}
}

func TestImpactKindsAndLimit(t *testing.T) {
	m := fixtureGraph(t)

	// Five routes and four tools: the two the fixture's own MCP server registers, and the
	// two it adds to the framework's dev server through App.DevTools (routes/devtools_dev.go).
	res := impact(t, m, ImpactQuery{Node: "table:orders", Kinds: []string{"route", "tool"}})
	if len(res.Affected) != 9 {
		t.Errorf("routes and tools: %v", affectedIDs(res))
	}
	for _, a := range res.Affected {
		if a.Kind != "route" && a.Kind != "mcp_tool" {
			t.Errorf("kind filter let %s through", a.ID)
		}
	}
	// the summary still describes everything, so the caller sees what it filtered out
	// Three commands too: the two that go through the orders service, and (without call
	// edges) the one in a package that imports it, as a possible, not proven, reach.
	if res.ByKind["func"] != 3 || res.ByKind["package"] != 6 || res.ByKind["command"] != 3 {
		t.Errorf("by_kind after filtering: %v", res.ByKind)
	}

	res = impact(t, m, ImpactQuery{Node: "table:orders", Limit: 4})
	if len(res.Affected) != 4 || !res.Truncated || res.Total != 23 {
		t.Errorf("limit: %d affected, truncated=%v, total=%d", len(res.Affected), res.Truncated, res.Total)
	}
	// truncation drops the weakest evidence, never the strongest
	if res.Affected[0].Confidence != 100 || res.Affected[3].Confidence < 90 {
		t.Errorf("kept: %v", res.Affected)
	}
}

// A handler is called by the router, so changing it does not put the routes
// beside it in doubt; its importers are the code that wires it, and (a possible reach,
// through the wiring package) the dev tools that package registers.
func TestImpactOfAHandler(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "controllers.HelloAPI"})
	want := []string{"route:GET:/api/hello", "pkg:routes", "pkg:.", "tool:fixture_cache_flush", "tool:fixture_orders_count"}
	if got := affectedIDs(res); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("affected = %v, want %v", got, want)
	}
	a := res.Affected[0]
	if a.Confidence != 100 || a.Method != methodEdge || a.Via != "handled_by" || a.Depth != 1 {
		t.Errorf("the route: %+v", a)
	}
	if res.Docs[0].ID != "doc:docs/public/ARCHITECTURE.md" {
		t.Errorf("docs: %+v", res.Docs)
	}
	var item *DocHit
	for i := range res.Docs {
		if res.Docs[i].ID == "item:hello/get-api-hello-endpoint" {
			item = &res.Docs[i]
		}
	}
	if item == nil || item.Via != "covers" || item.Spec != "spec:hello" || item.Status == "" {
		t.Errorf("the spec row that covers it: %+v", item)
	}
}

// A file is its functions: the exact routes of its handlers, and, because it
// holds an unexported helper that its siblings may call, their routes too, at
// the lowest confidence.
func TestImpactOfAFile(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "controllers/orders.go"})
	got := byID(res)
	if res.Target.ID != "file:controllers/orders.go" || res.SeedCount != 4 {
		t.Fatalf("target: %+v seeds %d", res.Target, res.SeedCount)
	}
	for _, id := range []string{"route:GET:/api/orders", "route:GET:/api/orders/{id}"} {
		if a := got[id]; a.Confidence != 100 || a.Method != methodEdge {
			t.Errorf("%s: %+v", id, a)
		}
	}
	for _, id := range []string{"route:GET:/", "route:GET:/api/hello"} {
		if a := got[id]; a.Confidence != 30 || a.Method != methodPkgScope || len(a.Through) != 1 {
			t.Errorf("%s: %+v", id, a)
		}
	}
	if a := got["pkg:routes"]; a.Confidence != 60 || a.Method != methodPkgImport {
		t.Errorf("an importer of an exported function: %+v", a)
	}
}

// A struct is not a helper: changing one does not put every route of its package
// in doubt, only what imports the package.
func TestImpactOfATypeDoesNotPutItsPackagesRoutesInDoubt(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "typ:business/services/orders.Service"})
	for _, a := range res.Affected {
		if a.Method == methodPkgScope {
			t.Errorf("%s: a type change should not list same-package code: %+v", a.ID, a)
		}
	}
	mustHave(t, "importers of the package", affectedIDs(res), "pkg:controllers", "pkg:routes")

	// a file that declares only a handler and a struct names its route, not its neighbours
	res = impact(t, m, ImpactQuery{Node: "controllers/hello.go"})
	if got := affectedIDs(res); got[0] != "route:GET:/api/hello" {
		t.Errorf("%v", got)
	}
	for _, a := range res.Affected {
		if a.Kind == "route" && a.ID != "route:GET:/api/hello" {
			t.Errorf("another route in the package: %+v", a)
		}
	}
}

func TestImpactOfAnUnexportedHelperStaysInItsPackage(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "controllers.writeJSON"})
	for _, a := range res.Affected {
		if a.Kind == "package" {
			t.Errorf("an unexported function cannot be imported: %s", a.ID)
		}
		if a.Method != methodPkgScope {
			t.Errorf("%s: %+v", a.ID, a)
		}
	}
	mustHave(t, "routes beside the helper", affectedIDs(res), "route:GET:/", "route:GET:/api/hello", "route:GET:/api/orders", "route:GET:/api/orders/{id}")
	if !strings.Contains(strings.Join(res.Notes, " "), "unexported") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestImpactOfAPackage(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "storage/orderstore"})
	got := byID(res)
	// importers are exact: they depend on the package as a whole
	for id, depth := range map[string]int{"pkg:business/services/orders": 1, "pkg:controllers": 1, "pkg:devmcp": 1, "pkg:routes": 1, "pkg:.": 2} {
		if a := got[id]; a.Depth != depth || a.Confidence != 100 || a.Method != methodEdge {
			t.Errorf("%s: %+v", id, a)
		}
	}
	// the routes and tools registered in them are not
	if a := got["route:GET:/api/orders"]; a.Confidence != 50 || a.Method != methodPkgImport {
		t.Errorf("route: %+v", a)
	}

	// with a depth of one, the second-level importer is still named, as an estimate
	res = impact(t, m, ImpactQuery{Node: "storage/orderstore", Depth: 1})
	if a := byID(res)["pkg:."]; a.Confidence != 60 || a.Method != methodPkgImport {
		t.Errorf("beyond the depth: %+v", a)
	}
}

func TestImpactOfConfigTemplatesColumnsAndMigrations(t *testing.T) {
	m := fixtureGraph(t)

	res := impact(t, m, ImpactQuery{Node: "server.port"})
	if got := affectedIDs(res); strings.Join(got, ",") != "func:..main,pkg:." && strings.Join(got, ",") != "pkg:.,func:..main" {
		t.Errorf("who uses server.port: %v", got)
	}

	// a template reaches the handler that renders it, and that handler's route
	res = impact(t, m, ImpactQuery{Node: "templates.Home"})
	got := byID(res)
	if a := got["func:controllers.HomePage"]; a.Via != "renders" || a.Depth != 1 || a.Confidence != 100 {
		t.Errorf("renderer: %+v", a)
	}
	if a := got["route:GET:/"]; a.Via != "handled_by" || a.Depth != 2 || a.Confidence != 100 || a.Method != methodEdge {
		t.Errorf("route of the renderer: %+v", a)
	}

	// an implicit reference is followed, with the annotation's own confidence
	res = impact(t, m, ImpactQuery{Node: "users.id"})
	got = byID(res)
	if a := got["col:orders.user_id"]; a.Via != "implicit_ref" || a.Confidence != 95 || a.Method != methodInferred {
		t.Errorf("implicit reference: %+v", a)
	}
	// the dependent column's table is read by code that may or may not use it
	if a := got["func:storage/orderstore.Store.List"]; a.Method != methodTable || a.Confidence != 70 || a.Depth != 2 {
		t.Errorf("readers of the dependent column's table: %+v", a)
	}

	// a migration is the tables it introduced
	res = impact(t, m, ImpactQuery{Node: "mig:0001_init"})
	if res.SeedCount != 5 {
		t.Errorf("migration seeds: %d", res.SeedCount)
	}
	mustHave(t, "migration impact", affectedIDs(res), "func:storage/orderstore.Store.List", "route:GET:/api/orders")
}

func TestImpactErrorsAndDeterminism(t *testing.T) {
	m := fixtureGraph(t)
	if _, err := m.Impact(ImpactQuery{Node: "no.such.thing"}); err == nil || !strings.Contains(err.Error(), "no node matches") {
		t.Errorf("unknown node: %v", err)
	}
	if _, err := m.Impact(ImpactQuery{Node: "List"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("ambiguous node: %v", err)
	}
	if _, err := m.Impact(ImpactQuery{Node: "table:orders", Kinds: []string{"banana"}}); err == nil {
		t.Error("unknown kind")
	}
	// a node nothing depends on is an empty list with a note, not an error
	res := impact(t, m, ImpactQuery{Node: "route:GET:/api/hello"})
	if len(res.Affected) != 0 || res.Affected == nil {
		t.Errorf("a route has no dependents: %v", res.Affected)
	}

	a, _ := json.Marshal(impact(t, m, ImpactQuery{Node: "table:orders"}))
	b, _ := json.Marshal(impact(t, m, ImpactQuery{Node: "table:orders"}))
	if string(a) != string(b) {
		t.Error("two runs over the same model differ")
	}
}
