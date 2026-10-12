package codegraph

import (
	"strings"
	"testing"
)

func stepsString(steps []PathStep) string {
	var parts []string
	for _, s := range steps {
		arrow := "-" + s.Predicate + "->"
		if s.Reverse {
			arrow = "<-" + s.Predicate + "-"
		}
		parts = append(parts, s.From+" "+arrow+" "+s.To)
	}
	return strings.Join(parts, "\n")
}

func pathBetween(t *testing.T, m *Model, from, to string) *PathResult {
	t.Helper()
	res, err := m.Path(PathQuery{From: from, To: to})
	if err != nil {
		t.Fatalf("path %s -> %s: %v", from, to, err)
	}
	return res
}

// Exact edges first: a path made only of edges the graph holds is reported as
// such, with full confidence.
func TestPathUsesExactEdgesWhenTheyExist(t *testing.T) {
	m := fixtureGraph(t)

	res := pathBetween(t, m, "GET /", "templates.Home")
	if !res.Found || res.Inferred || res.Confidence != 100 || res.Depends != "from_depends_on_to" {
		t.Fatalf("route to its template: %+v", res)
	}
	want := "route:GET:/ -handled_by-> func:controllers.HomePage\nfunc:controllers.HomePage -renders-> tmpl:assets/templates.Home"
	if got := stepsString(res.Steps); got != want {
		t.Errorf("steps:\n%s\nwant:\n%s", got, want)
	}
	if got := ids(res.Nodes); len(got) != 3 || res.Nodes[0].ID != "route:GET:/" || res.Nodes[2].ID != "tmpl:assets/templates.Home" {
		t.Errorf("nodes: %v", got)
	}
	if res.Note != "" {
		t.Errorf("an exact path needs no caveat: %s", res.Note)
	}

	// package to package is exact too: imports are edges
	res = pathBetween(t, m, "pkg:routes", "pkg:storage/orderstore")
	if !res.Found || res.Inferred || len(res.Steps) != 1 || res.Steps[0].Kind != "imports" {
		t.Errorf("package imports: %+v", res)
	}
}

// Asked the wrong way round, the answer comes back with the steps still
// running from `from` to `to`, and says which way the dependency goes.
func TestPathTriesBothOrientations(t *testing.T) {
	m := fixtureGraph(t)
	res := pathBetween(t, m, "templates.Home", "GET /")
	if !res.Found || res.Depends != "to_depends_on_from" || res.Inferred {
		t.Fatalf("%+v", res)
	}
	want := "tmpl:assets/templates.Home <-renders- func:controllers.HomePage\nfunc:controllers.HomePage <-handled_by- route:GET:/"
	if got := stepsString(res.Steps); got != want {
		t.Errorf("steps:\n%s\nwant:\n%s", got, want)
	}
	if res.Nodes[0].ID != "tmpl:assets/templates.Home" || res.Nodes[len(res.Nodes)-1].ID != "route:GET:/" {
		t.Errorf("nodes run from `from` to `to`: %v", ids(res.Nodes))
	}
}

// The DB-backed API: with no call edges the path from a route to a table is the
// package-level one. It is found, it is labelled, and the missing hop is named.
func TestPathFromARouteToATableIsPackageLevelAndSaysSo(t *testing.T) {
	m := fixtureGraph(t)
	res := pathBetween(t, m, "GET /api/orders", "table:orders")
	if !res.Found || !res.Inferred || res.Confidence != 50 || res.Depends != "from_depends_on_to" {
		t.Fatalf("%+v", res)
	}
	got := stepsString(res.Steps)
	for _, want := range []string{
		"route:GET:/api/orders -handled_by-> func:controllers.ListOrders",
		"func:controllers.ListOrders -defined_in-> file:controllers/orders.go",
		"file:controllers/orders.go -in_package-> pkg:controllers",
		"pkg:storage/orderstore",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	last := res.Steps[len(res.Steps)-1]
	if last.To != "table:orders" || (last.Predicate != "reads_table" && last.Predicate != "writes_table") {
		t.Errorf("the last hop: %+v", last)
	}
	// the hops that are inference say what kind they are
	kinds := map[string]bool{}
	for _, s := range res.Steps {
		kinds[s.Kind] = true
	}
	if !kinds["imports"] || !kinds["containment"] || !kinds["edge"] {
		t.Errorf("step kinds: %v", kinds)
	}
	if !strings.Contains(res.Note, "calls") || !strings.Contains(res.Note, "possible, not that one is made") {
		t.Errorf("note: %s", res.Note)
	}
	// every function that could be the one is named
	for _, fn := range []string{"Store.Create (writes)", "Store.Get (reads)", "Store.List (reads)"} {
		if !strings.Contains(res.Note, fn) {
			t.Errorf("note does not name %s: %s", fn, res.Note)
		}
	}
}

// Asked for the table first, the same chain comes back reversed.
func TestPathFromATableToARoute(t *testing.T) {
	m := fixtureGraph(t)
	res := pathBetween(t, m, "orders", "GET /api/orders")
	if !res.Found || !res.Inferred || res.Depends != "to_depends_on_from" {
		t.Fatalf("%+v", res)
	}
	if res.Steps[0].From != "table:orders" || res.Steps[len(res.Steps)-1].To != "route:GET:/api/orders" {
		t.Errorf("steps run from `from` to `to`:\n%s", stepsString(res.Steps))
	}
	if !res.Steps[0].Reverse {
		t.Errorf("the first hop goes against the stored edge: %+v", res.Steps[0])
	}
}

// Membership is not inference: a function and its package are connected for certain.
func TestPathThroughMembershipOnlyIsNotInferred(t *testing.T) {
	m := fixtureGraph(t)
	res := pathBetween(t, m, "controllers.HelloAPI", "pkg:controllers")
	if !res.Found || res.Inferred || res.Confidence != 100 || len(res.Steps) != 2 {
		t.Errorf("%+v", res)
	}
}

// When nothing links two nodes, even by package, the answer is a clear no, with
// what each side does reach.
func TestPathNotFoundSaysWhatEachSideReaches(t *testing.T) {
	m := fixtureGraph(t)
	res := pathBetween(t, m, "GET /", "table:products")
	if res.Found || res.Steps != nil || res.Confidence != 0 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Note, "No chain of dependency edges") {
		t.Errorf("note: %s", res.Note)
	}
	mustHave(t, "what the route depends on", ids(res.FromReaches), "func:controllers.HomePage", "tmpl:assets/templates.Home")
	mustHave(t, "what depends on products", ids(res.ToReachedBy), "table:order_items")
}

func TestPathLimitsAndErrors(t *testing.T) {
	m := fixtureGraph(t)

	// too short a search does not find the package-level chain
	res, err := m.Path(PathQuery{From: "GET /api/orders", To: "table:orders", MaxDepth: 3})
	if err != nil || res.Found {
		t.Errorf("a 3-hop budget: %+v %v", res, err)
	}
	// the same node
	res = pathBetween(t, m, "table:orders", "orders")
	if !res.Found || len(res.Steps) != 0 || len(res.Nodes) != 1 {
		t.Errorf("same node: %+v", res)
	}
	// docs are not part of dependency flow, and the note says what to use instead
	res = pathBetween(t, m, "README.md", "table:orders")
	if res.Found || !strings.Contains(res.Note, "graph_neighbors") {
		t.Errorf("doc: %+v", res)
	}
	// bad references name the argument
	if _, err := m.Path(PathQuery{From: "nope.nope", To: "table:orders"}); err == nil || !strings.HasPrefix(err.Error(), "from: ") {
		t.Errorf("bad from: %v", err)
	}
	if _, err := m.Path(PathQuery{From: "table:orders", To: "List"}); err == nil || !strings.HasPrefix(err.Error(), "to: ") {
		t.Errorf("bad to: %v", err)
	}
}
