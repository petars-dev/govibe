package codegraph

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/graphdb"
)

var deepFixture struct {
	once sync.Once
	m    *Model
	err  error
}

// fixtureDeepGraph is the loaded model of the fixture with the opt-in layers on:
// the call graph, and (in a throw-away repository, so the history is known) git.
// Like fixtureGraph it is built once, shared, and must not be changed.
func fixtureDeepGraph(t *testing.T) *Model {
	t.Helper()
	deepFixture.once.Do(func() {
		root, err := os.MkdirTemp("", "codegraph-deep-")
		if err != nil {
			deepFixture.err = err
			return
		}
		defer os.RemoveAll(root)
		if err := copyDir(fixtureRoot, root); err != nil {
			deepFixture.err = err
			return
		}
		haveGit := false
		if _, err := exec.LookPath("git"); err == nil {
			haveGit = true
			r := &testRepo{t: t, root: root}
			r.git(nil, "init", "-q", "-b", "main")
			for i := 1; i <= 4; i++ {
				b, _ := os.ReadFile(root + "/business/actions/hello.go")
				g, _ := os.ReadFile(root + "/docs/public/GUIDE.md")
				r.commit("alice", "Edit the greeting", map[string]string{
					"business/actions/hello.go": strings.Replace(string(b), "Fixture!", "Fixture"+strings.Repeat("!", i+1), 1),
					"docs/public/GUIDE.md":      string(g) + "\nedit\n",
				})
			}
		}
		dir, err := os.MkdirTemp("", "codegraph-deep-store-")
		if err != nil {
			deepFixture.err = err
			return
		}
		defer os.RemoveAll(dir)
		h, err := graphdb.Open(dir)
		if err != nil {
			deepFixture.err = err
			return
		}
		defer h.Close()
		opts := fixtureOptions()
		opts.Root, opts.Calls, opts.Git = root, true, haveGit
		rep, err := New(h.QuadStore, opts).Run(context.Background())
		if err != nil {
			deepFixture.err = err
			return
		}
		for _, l := range rep.Layers {
			if l.Status == statusError {
				deepFixture.err = &layerError{l}
				return
			}
		}
		deepFixture.m, deepFixture.err = Load(context.Background(), h.QuadStore)
	})
	if deepFixture.err != nil {
		t.Fatalf("index the fixture with calls: %v", deepFixture.err)
	}
	return deepFixture.m
}

type layerError struct{ l LayerReport }

func (e *layerError) Error() string { return "layer " + e.l.Name + " failed: " + e.l.Error }

func copyDir(src, dst string) error {
	cmd := exec.Command("cp", "-R", src+"/.", dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		return &execError{err, string(out)}
	}
	return nil
}

type execError struct {
	err error
	out string
}

func (e *execError) Error() string { return e.err.Error() + ": " + e.out }

func affectedByID(res *ImpactResult) map[string]Affected {
	out := map[string]Affected{}
	for _, a := range res.Affected {
		out[a.ID] = a
	}
	return out
}

func TestImpactOfATableFollowsCalls(t *testing.T) {
	m := fixtureDeepGraph(t)
	if !m.hasCalls() {
		t.Fatal("the deep fixture has call edges")
	}
	res, err := m.Impact(ImpactQuery{Node: "table:orders"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Depth != defaultCallsDepth {
		t.Errorf("with call edges the default depth is %d, got %d", defaultCallsDepth, res.Depth)
	}
	got := affectedByID(res)

	// Acceptance 3: the routes whose handler call closure reaches a function that
	// reads or writes the table, and how.
	list, ok := got["route:GET:/api/orders"]
	if !ok {
		t.Fatalf("GET /api/orders reaches orders through ListOrders -> Service.List -> Store.List: %v", ids2(res))
	}
	if list.Access != "read" || list.Method != methodDispatch || list.Confidence != 70 {
		t.Errorf("GET /api/orders: %+v (the call through Lister is an interface call between two candidates)", list)
	}
	wantChain := []string{"route:GET:/api/orders", "func:controllers.ListOrders", "func:business/services/orders.Service.List", "func:storage/orderstore.Store.List", "table:orders"}
	if strings.Join(list.Through, ",") != strings.Join(wantChain, ",") {
		t.Errorf("chain %v, want %v", list.Through, wantChain)
	}
	one, ok := got["route:GET:/api/orders/{id}"]
	if !ok || one.Access != "read" || one.Confidence != 90 || one.Method != methodSQL {
		t.Errorf("GET /api/orders/{id}: %+v (a direct call to the store; the table hop is read from SQL: 90)", one)
	}
	if _, bad := got["route:GET:/api/hello"]; bad {
		t.Error("GET /api/hello touches no table: the package-level estimate that put it here is gone")
	}
	// The composition root is listed (it calls the handler factories) but not followed.
	if _, ok := got["func:routes.New"]; !ok {
		t.Error("the wiring function that calls an affected handler factory is listed")
	}
	if _, followed := got["func:..main"]; followed {
		t.Error("wiring is not followed: main reaches everything")
	}
	if res.ByMethod[methodPkgImport] != 0 || res.ByMethod[methodPkgScope] != 0 {
		t.Errorf("no package-level estimate when calls are indexed: %v", res.ByMethod)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "Callers come from the `calls` layer") {
		t.Errorf("notes: %v", res.Notes)
	}

	// Filtering to entry points is how an agent asks "which routes".
	only, err := m.Impact(ImpactQuery{Node: "table:orders", Kinds: []string{"route"}})
	if err != nil || len(only.Affected) != 2 {
		t.Errorf("routes only: %v %v", ids2(only), err)
	}
}

func ids2(res *ImpactResult) []string {
	var out []string
	if res == nil {
		return out
	}
	for _, a := range res.Affected {
		out = append(out, a.ID)
	}
	return out
}

func TestImpactOfAFunctionFindsItsRoute(t *testing.T) {
	m := fixtureDeepGraph(t)
	res, err := m.Impact(ImpactQuery{Node: "orderstore.Store.List"})
	if err != nil {
		t.Fatal(err)
	}
	got := affectedByID(res)
	mustHave(t, "callers of Store.List", ids2(res), "func:business/services/orders.Service.List", "func:controllers.ListOrders", "route:GET:/api/orders")
	if a := got["func:business/services/orders.Service.List"]; a.Depth != 1 || a.Confidence != 70 || a.Method != methodDispatch {
		t.Errorf("Service.List: %+v", a)
	}
	// Nothing reaches GetOrder's route through List.
	mustNotHave(t, "callers of Store.List", ids2(res), "route:GET:/api/orders/{id}", "route:GET:/api/hello")

	// An unexported helper that two handlers share: both routes, through the call
	// edges, which the package-level estimate could only guess at.
	w, err := m.Impact(ImpactQuery{Node: "controllers.writeJSON"})
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, "writeJSON's routes", ids2(w), "route:GET:/api/hello", "route:GET:/api/orders", "route:GET:/api/orders/{id}")
	mustNotHave(t, "writeJSON's routes", ids2(w), "route:GET:/")
}

// A type's methods are part of it: its callers are the callers of its methods.
func TestImpactOfATypeFollowsItsMethods(t *testing.T) {
	m := fixtureDeepGraph(t)
	res, err := m.Impact(ImpactQuery{Node: "typ:storage/orderstore.Store"})
	if err != nil {
		t.Fatal(err)
	}
	mustHave(t, "routes using the store's methods", ids2(res), "route:GET:/api/orders", "route:GET:/api/orders/{id}")
	if !strings.Contains(strings.Join(res.Notes, " "), "A type is used without being called") {
		t.Errorf("notes: %v", res.Notes)
	}
}

// The first path found is not the whole story: a route that reaches a table by a
// read in one handler's call and a write in another reads and writes it.
func TestImpactAccessIsTheUnionOverEveryPath(t *testing.T) {
	label := LayerLabel("calls")
	var quads []quad.Quad
	add := func(s, p string, o quad.Value) { quads = append(quads, quad.Make(quad.IRI(s), quad.IRI(p), o, label)) }
	for id, kind := range map[string]string{
		"func:a.H": "func", "func:a.A": "func", "func:a.X": "func", "func:a.B": "func",
		"route:GET:/r": "route", "table:t": "table", "layer:calls": "layer",
	} {
		add(id, "type", quad.IRI("kind:"+kind))
		add(id, "name", quad.String(id))
	}
	add("layer:calls", "status", quad.String("ok"))
	add("route:GET:/r", "handled_by", quad.IRI("func:a.H"))
	add("func:a.H", "calls", quad.IRI("func:a.A")) // H reads through A, one hop from the table...
	add("func:a.H", "calls", quad.IRI("func:a.X")) // ...and writes through X, which is a hop further
	add("func:a.X", "calls", quad.IRI("func:a.B"))
	add("func:a.A", "reads_table", quad.IRI("table:t"))
	add("func:a.B", "writes_table", quad.IRI("table:t"))
	m := modelOf(quads)

	res, err := m.Impact(ImpactQuery{Node: "table:t"})
	if err != nil {
		t.Fatal(err)
	}
	got := affectedByID(res)
	if a := got["func:a.H"]; a.Access != "read+write" {
		t.Errorf("H reads through A and writes through X: %+v", a)
	}
	if a := got["route:GET:/r"]; a.Access != "read+write" {
		t.Errorf("the route inherits both: %+v", a)
	}
	if a := got["func:a.A"]; a.Access != "read" {
		t.Errorf("A only reads: %+v", a)
	}
}

func TestPathFromARouteToATableIsAChainOfCalls(t *testing.T) {
	m := fixtureDeepGraph(t)

	res := pathBetween(t, m, "GET /api/orders", "table:orders")
	if !res.Found || res.Inferred || res.Depends != "from_depends_on_to" {
		t.Fatalf("%+v", res)
	}
	want := "route:GET:/api/orders -handled_by-> func:controllers.ListOrders\n" +
		"func:controllers.ListOrders -calls-> func:business/services/orders.Service.List\n" +
		"func:business/services/orders.Service.List -calls-> func:storage/orderstore.Store.List\n" +
		"func:storage/orderstore.Store.List -reads_table-> table:orders"
	if got := stepsString(res.Steps); got != want {
		t.Errorf("steps:\n%s\nwant:\n%s", got, want)
	}
	if res.Confidence != 70 {
		t.Errorf("the path is as sure as its weakest hop, the interface call: %d", res.Confidence)
	}
	hop := res.Steps[2]
	if hop.Method != methodDispatch || hop.Confidence != 70 || hop.Via != "typ:business/services/orders.Lister" {
		t.Errorf("the interface hop: %+v", hop)
	}
	if res.Steps[3].Confidence != 90 || res.Steps[3].Method != methodSQL {
		t.Errorf("the table hop: %+v", res.Steps[3])
	}
	if !strings.Contains(res.Note, "Not every hop is certain") || !strings.Contains(res.Note, "orders.Lister") || !strings.Contains(res.Note, "SQL strings") {
		t.Errorf("note: %s", res.Note)
	}

	// The reverse question gives the same chain, from the table to the route.
	back := pathBetween(t, m, "table:orders", "GET /api/orders")
	if !back.Found || back.Depends != "to_depends_on_from" || len(back.Steps) != 4 || back.Steps[0].From != "table:orders" {
		t.Errorf("reverse: %+v", back)
	}

	// A handler that reaches no table has no path to one, and says it is sure.
	none := pathBetween(t, m, "GET /api/hello", "table:orders")
	if none.Found || none.Inferred {
		t.Fatalf("GET /api/hello reaches no table: %+v", none)
	}
	if !strings.Contains(none.Note, "No chain of calls") || strings.Contains(none.Note, "package imports") {
		t.Errorf("note: %s", none.Note)
	}
	mustHave(t, "what the route does reach", ids(none.FromReaches), "func:controllers.HelloAPI", "func:business/actions.Hello")

	// A direct call is surer than going through the service.
	direct := pathBetween(t, m, "GET /api/orders/{id}", "table:orders")
	if !direct.Found || direct.Confidence != 90 || len(direct.Steps) != 3 {
		t.Errorf("direct: %+v", direct)
	}
}

func TestContextOfARouteListsWhatItReaches(t *testing.T) {
	m := fixtureDeepGraph(t)

	// Acceptance 1: the route, its handler, the call to the action, and no table.
	c, err := m.Context(ContextQuery{Route: "GET /api/hello"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Handler == nil || c.Handler.ID != "func:controllers.HelloAPI" {
		t.Fatalf("handler: %+v", c.Handler)
	}
	calls := map[string]CallRef{}
	for _, r := range c.Calls {
		calls[r.ID] = r
	}
	if r, ok := calls["func:business/actions.Hello"]; !ok || r.Confidence != 100 {
		t.Errorf("the handler's call to the action: %+v", c.Calls)
	}
	if c.Reach == nil || c.Reach.Method != "calls" || len(c.Reach.Tables) != 0 || !strings.Contains(c.Reach.Note, "No table is reached") {
		t.Fatalf("reach: %+v", c.Reach)
	}
	if c.Reach.Functions != 2 {
		t.Errorf("HelloAPI reaches Hello and writeJSON: %d", c.Reach.Functions)
	}

	// Acceptance 2: a DB-backed route reaches the table through the call chain.
	c, err = m.Context(ContextQuery{Route: "GET /api/orders"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Reach == nil || len(c.Reach.Tables) != 1 {
		t.Fatalf("reach: %+v", c.Reach)
	}
	r := c.Reach.Tables[0]
	if r.Table.ID != "table:orders" || r.Access != "read" || r.Confidence != 70 ||
		strings.Join(r.Via, ",") != "func:controllers.ListOrders,func:business/services/orders.Service.List,func:storage/orderstore.Store.List" {
		t.Errorf("reach table: %+v", r)
	}
	if !strings.Contains(c.Reach.Note, "call chains") {
		t.Errorf("note: %s", c.Reach.Note)
	}

	// A function's briefing says who calls it and whom it calls.
	f, err := m.Context(ContextQuery{Symbol: "orders.Service.List"})
	if err != nil {
		t.Fatal(err)
	}
	var out, in []string
	for _, r := range f.Calls {
		out = append(out, r.ID)
		if r.ID == "func:storage/orderstore.Store.List" && (r.Method != methodDispatch || r.Via != "typ:business/services/orders.Lister") {
			t.Errorf("the interface call: %+v", r)
		}
	}
	for _, r := range f.CalledBy {
		in = append(in, r.ID)
	}
	mustHave(t, "calls", out, "func:storage/orderstore.Store.List")
	mustHave(t, "called by", in, "func:controllers.ListOrders")
	if f.Reach == nil || len(f.Reach.Tables) != 1 || f.Reach.Tables[0].Table.ID != "table:orders" {
		t.Errorf("Service.List reaches the orders table through the store: %+v", f.Reach)
	}
}

// Without the call graph the answers are the package-level estimates they have
// always been, and say that the layer is off.
func TestWithoutCallsTheEstimateIsLabelled(t *testing.T) {
	m := fixtureGraph(t)
	if m.hasCalls() {
		t.Fatal("the shared fixture graph has no call edges")
	}
	c, err := m.Context(ContextQuery{Route: "GET /api/hello"})
	if err != nil || c.Reach == nil || c.Reach.Method != methodPkgImport || len(c.Calls) != 0 {
		t.Fatalf("%+v %v", c.Reach, err)
	}
	res, err := m.Impact(ImpactQuery{Node: "table:orders"})
	if err != nil || res.Depth != defaultImpactDepth {
		t.Fatalf("%+v %v", res, err)
	}
	if notes := strings.Join(res.Notes, " "); !strings.Contains(notes, "calls` layer is off") || !strings.Contains(notes, "GRAPH_CALLS") {
		t.Errorf("notes: %v", res.Notes)
	}
}

func TestGitHistoryInTheBriefings(t *testing.T) {
	m := fixtureDeepGraph(t)
	if !m.layerBuilt(gitLayerName) {
		t.Skip("git is not installed")
	}
	c, err := m.Context(ContextQuery{Path: "business/actions/hello.go"})
	if err != nil {
		t.Fatal(err)
	}
	h := c.History
	if h == nil || h.Commits != 4 || h.Authors != 1 || h.LastSubject != "Edit the greeting" || len(h.LastCommit) != 12 || h.LastCommitAt == "" {
		t.Fatalf("history: %+v", h)
	}
	if len(h.CoChanged) != 1 || h.CoChanged[0].ID != "doc:docs/public/GUIDE.md" || h.CoChanged[0].Percent != 100 || h.CoChanged[0].Together != 4 {
		t.Errorf("co-changed: %+v", h.CoChanged)
	}
	// A function takes the history of its file.
	f, err := m.Context(ContextQuery{Symbol: "actions.Hello"})
	if err != nil || f.History == nil || f.History.Commits != 4 {
		t.Errorf("function history: %+v %v", f.History, err)
	}
	// A change to the function tells what usually changes with its file.
	res, err := m.Impact(ImpactQuery{Node: "actions.Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CoChanged) != 1 || res.CoChanged[0].ID != "doc:docs/public/GUIDE.md" {
		t.Errorf("co_changed: %+v", res.CoChanged)
	}
	// The doc, in turn, knows the file.
	d, err := m.Context(ContextQuery{Path: "docs/public/GUIDE.md"})
	if err != nil || d.History == nil || len(d.History.CoChanged) != 1 || d.History.CoChanged[0].ID != "file:business/actions/hello.go" {
		t.Errorf("doc history: %+v %v", d.History, err)
	}
	// A file one commit created has a history of one, and nothing changed with it.
	if n, err := m.Context(ContextQuery{Path: "controllers/orders.go"}); err != nil || n.History == nil || n.History.Commits != 1 || len(n.History.CoChanged) != 0 {
		t.Errorf("%+v %v", n.History, err)
	}
}

func TestSchemaSaysHowReachIsKnown(t *testing.T) {
	deep := strings.Join(fixtureDeepGraph(t).Schema(nil).Conventions, "\n")
	if !strings.Contains(deep, "Call edges are indexed") || strings.Contains(deep, "No call edges are indexed") {
		t.Errorf("conventions with calls:\n%s", deep)
	}
	plain := strings.Join(fixtureGraph(t).Schema(nil).Conventions, "\n")
	if !strings.Contains(plain, "No call edges are indexed") {
		t.Errorf("conventions without calls:\n%s", plain)
	}
	notes := strings.Join(fixtureDeepGraph(t).Schema(nil).Notes, "\n")
	if strings.Contains(notes, "calls layer is off") {
		t.Errorf("the calls layer is on:\n%s", notes)
	}
}

// With the call graph the request-flow view runs down to the tables, and draws a
// call through an interface dashed, with its confidence.
func TestRequestFlowViewShowsTheCallChain(t *testing.T) {
	m := fixtureDeepGraph(t)
	v, err := m.View(ViewQuery{Preset: "request_flow"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(v.Columns, "|"); got != "route|middleware|handler|calls|tables, renders, tools" {
		t.Errorf("columns: %s", got)
	}
	nodes := map[string]ViewNode{}
	for _, n := range v.Nodes {
		nodes[n.ID] = n
	}
	for id, col := range map[string]int{
		"route:GET:/api/orders": 0, "func:controllers.ListOrders": 2,
		"func:business/services/orders.Service.List": 3, "func:storage/orderstore.Store.List": 3, "table:orders": 4,
	} {
		if n, ok := nodes[id]; !ok || n.Col != col {
			t.Errorf("%s: %+v (want column %d)", id, n, col)
		}
	}
	if _, bad := nodes["func:controllers.writeJSON"]; bad {
		t.Error("a helper that touches no table is not on the way to one")
	}
	var dispatch, read bool
	for _, e := range v.Edges {
		switch {
		case e.Source == "func:business/services/orders.Service.List" && e.Target == "func:storage/orderstore.Store.List" && e.Predicate == "calls":
			dispatch = e.Inferred && e.Confidence == 70
		case e.Source == "func:storage/orderstore.Store.List" && e.Target == "table:orders" && e.Predicate == "reads_table":
			read = true
		}
	}
	if !dispatch || !read {
		t.Errorf("edges: dispatch dashed=%v, table read=%v: %+v", dispatch, read, v.Edges)
	}

	// Without the call graph the view is what it was.
	plain, err := fixtureGraph(t).View(ViewQuery{Preset: "request_flow"})
	if err != nil || strings.Join(plain.Columns, "|") != "route|middleware|handler|renders / registers" {
		t.Errorf("%v %v", plain.Columns, err)
	}
}

func TestRoutesReportListsTheTablesARouteReaches(t *testing.T) {
	rep := fixtureDeepGraph(t).RoutesReport(LinkOptions{})
	if !rep.Calls {
		t.Fatal("the call graph is indexed")
	}
	rows := map[string]RouteRow{}
	for _, r := range rep.Routes {
		rows[r.Route.ID] = r
	}
	if r := rows["route:GET:/api/orders"]; len(r.Tables) != 1 || r.Tables[0].Table.Label == "" || r.Tables[0].Access != "read" || r.Tables[0].Confidence != 70 {
		t.Errorf("GET /api/orders: %+v", r.Tables)
	}
	if r := rows["route:GET:/api/orders/{id}"]; len(r.Tables) != 1 || r.Tables[0].Confidence != 90 {
		t.Errorf("GET /api/orders/{id}: %+v", r.Tables)
	}
	if r := rows["route:GET:/api/hello"]; len(r.Tables) != 0 {
		t.Errorf("GET /api/hello reaches no table: %+v", r.Tables)
	}
	if plain := fixtureGraph(t).RoutesReport(LinkOptions{}); plain.Calls {
		t.Error("no call graph, no tables column")
	}
}

func TestFocusViewDrawsUnsureEdgesDashed(t *testing.T) {
	m := fixtureDeepGraph(t)
	v, err := m.View(ViewQuery{Focus: "func:business/services/orders.Service.List"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range v.Edges {
		if e.Predicate == "calls" && e.Target == "func:storage/orderstore.Store.List" {
			found = e.Inferred && e.Confidence == 70
		}
	}
	if !found {
		t.Errorf("edges: %+v", v.Edges)
	}
}
