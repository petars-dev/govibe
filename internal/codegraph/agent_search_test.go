package codegraph

import (
	"strings"
	"testing"
)

func resolved(m *Model, ref string) []string { return sortedIDs(m.Resolve(ref)) }

func TestResolveFindsWhatAnAgentHasInHand(t *testing.T) {
	m := fixtureGraph(t)
	for _, tc := range []struct {
		ref  string
		want []string
	}{
		// an ID, plain or in angle brackets
		{"func:controllers.HelloAPI", []string{"func:controllers.HelloAPI"}},
		{"<func:controllers.HelloAPI>", []string{"func:controllers.HelloAPI"}},
		// routes: method and path, in the spellings people use
		{"GET /api/hello", []string{"route:GET:/api/hello"}},
		{"get /api/hello", []string{"route:GET:/api/hello"}},
		{"GET:/api/hello", []string{"route:GET:/api/hello"}},
		{"/api/hello", []string{"route:GET:/api/hello"}},
		{"POST /api/hello", nil},
		// a concrete URL finds the pattern that serves it
		{"GET /api/orders/42", []string{"route:GET:/api/orders/{id}"}},
		{"/api/orders/42?expand=items", []string{"route:GET:/api/orders/{id}"}},
		// a route registered for every method answers any method
		{"POST /dev/mcp", []string{"route:ANY:/dev/mcp"}},
		// repository paths: file, directory, with a line, with ./, absolute
		{"controllers/hello.go", []string{"file:controllers/hello.go"}},
		{"./controllers/hello.go", []string{"file:controllers/hello.go"}},
		{"controllers/hello.go:14", []string{"file:controllers/hello.go"}},
		{"controllers/hello.go:14:3", []string{"file:controllers/hello.go"}},
		{"controllers", []string{"pkg:controllers"}},
		{"controllers/", []string{"pkg:controllers"}},
		{"/home/dev/work/fixture/controllers/hello.go", []string{"file:controllers/hello.go"}},
		{"example.com/fixture/controllers", []string{"pkg:controllers"}},
		{"assets/templates/home.templ", []string{"file:assets/templates/home.templ"}},
		{"docs/specs/SPEC_HELLO.md", []string{"spec:hello"}},
		{"README.md", []string{"doc:README.md"}},
		{".", []string{"pkg:."}},
		// bare names with an implied prefix
		{"orders", []string{"table:orders"}},
		{"Orders", []string{"table:orders"}},
		{"orders.user_id", []string{"col:orders.user_id"}},
		{"server.port", []string{"cfg:server.port"}},
		{"PORT", []string{"env:PORT"}},
		{"fixture_ping", []string{"tool:fixture_ping"}},
		{"hello", []string{"spec:hello"}},
		// symbols
		{"controllers.HelloAPI", []string{"func:controllers.HelloAPI"}},
		{"HelloAPI", []string{"func:controllers.HelloAPI"}},
		{"helloapi", []string{"func:controllers.HelloAPI"}},
		{"orderstore.Store.List", []string{"func:storage/orderstore.Store.List"}},
		{"storage/orderstore.Store.List", []string{"func:storage/orderstore.Store.List"}},
		{"Store.List", []string{"func:storage/orderstore.Store.List"}},
		{"orders.Service", []string{"typ:business/services/orders.Service"}},
		{"templates.Home", []string{"tmpl:assets/templates.Home"}},
		// not there
		{"nothing.Here", nil},
		{"", nil},
	} {
		if got := resolved(m, tc.ref); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("Resolve(%q) = %v, want %v", tc.ref, got, tc.want)
		}
	}
}

func TestResolveOneReportsAmbiguityAndNearMisses(t *testing.T) {
	m := freshGraph(t) // this test changes the model

	// A method name two types share is ambiguous; the error lists them.
	_, err := m.resolveOne("List")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") ||
		!strings.Contains(err.Error(), "func:business/services/orders.Service.List") ||
		!strings.Contains(err.Error(), "func:storage/orderstore.Store.List") {
		t.Errorf("a bare name two functions share: %v", err)
	}

	// Nothing matches: say what is close.
	_, err = m.resolveOne("HelloAPIs")
	if err == nil || !strings.Contains(err.Error(), "no node matches") {
		t.Fatalf("a miss: %v", err)
	}
	_, err = m.resolveOne("hello api")
	if err == nil || !strings.Contains(err.Error(), "did you mean") || !strings.Contains(err.Error(), "func:controllers.HelloAPI") {
		t.Errorf("near misses: %v", err)
	}

	// With the routes layer unavailable the message says why a route is missing.
	m.nodes["layer:routes"].Attrs["status"] = []any{statusSkipped}
	m.nodes["layer:routes"].Attrs["note"] = []any{"no router wired"}
	_, err = m.resolveOne("GET /nope")
	if err == nil || !strings.Contains(err.Error(), "routes layer was skipped (no router wired)") {
		t.Errorf("a route miss without the routes layer: %v", err)
	}
}

func TestSearchRanksByHowWellItMatches(t *testing.T) {
	m := fixtureGraph(t)

	res, err := m.SearchNodes(SearchQuery{Text: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total < 5 || len(res.Hits) == 0 {
		t.Fatalf("hits: %+v", res)
	}
	// the exact name beats everything that merely contains it
	if res.Hits[0].ID != "table:orders" || res.Hits[0].Match != "id" {
		t.Errorf("first hit for \"orders\": %+v", res.Hits[0])
	}
	if res.Hits[1].ID != "pkg:business/services/orders" {
		t.Errorf("second hit for \"orders\": %+v", res.Hits[1])
	}

	// every word must match, in any field
	res, _ = m.SearchNodes(SearchQuery{Text: "returns every order newest"})
	if got := ids(refsOf(res.Hits)); len(got) != 1 || got[0] != "func:storage/orderstore.Store.List" {
		t.Errorf("a phrase from a doc comment: %v", got)
	}

	// a kind filter, spelled however the agent likes
	for _, kinds := range [][]string{{"table"}, {"kind:table"}, {"Table"}} {
		res, err = m.SearchNodes(SearchQuery{Text: "order", Kinds: kinds})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Hits {
			if h.Kind != "table" {
				t.Errorf("kinds %v returned %s", kinds, h.ID)
			}
		}
		mustHave(t, "tables matching order", ids(refsOf(res.Hits)), "table:orders", "table:order_items")
	}
	res, _ = m.SearchNodes(SearchQuery{Text: "hello", Kinds: []string{"function", "endpoint"}})
	for _, h := range res.Hits {
		if h.Kind != "func" && h.Kind != "route" {
			t.Errorf("aliases: %s", h.ID)
		}
	}
	mustHave(t, "hello", ids(refsOf(res.Hits)), "func:controllers.HelloAPI", "route:GET:/api/hello")

	// bookkeeping nodes are found only when asked for
	res, _ = m.SearchNodes(SearchQuery{Text: "ann:"})
	if len(res.Hits) != 0 {
		t.Errorf("annotation nodes should not be searched by default: %+v", res.Hits)
	}
	res, _ = m.SearchNodes(SearchQuery{Text: "ann:", Kinds: []string{"annotation"}})
	if len(res.Hits) != 4 {
		t.Errorf("annotation nodes should be searchable on request: %+v", res.Hits)
	}

	// bounded
	res, _ = m.SearchNodes(SearchQuery{Text: "e", Limit: 3})
	if len(res.Hits) != 3 || !res.Truncated || res.Total <= 3 {
		t.Errorf("limit: %d hits, truncated=%v, total=%d", len(res.Hits), res.Truncated, res.Total)
	}

	// no match is an empty list, not an error; bad input is an error
	res, err = m.SearchNodes(SearchQuery{Text: "zzzz-nothing"})
	if err != nil || res.Hits == nil || len(res.Hits) != 0 || res.Total != 0 {
		t.Errorf("no match: %+v %v", res, err)
	}
	if _, err = m.SearchNodes(SearchQuery{Text: "  "}); err == nil {
		t.Error("empty text should be an error")
	}
	if _, err = m.SearchNodes(SearchQuery{Text: "x", Kinds: []string{"banana"}}); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("unknown kind: %v", err)
	}
}

func refsOf(hits []SearchHit) []NodeRef {
	out := make([]NodeRef, len(hits))
	for i, h := range hits {
		out[i] = h.NodeRef
	}
	return out
}

func TestNeighborsWalksTheGraphWithinBounds(t *testing.T) {
	m := fixtureGraph(t)

	res, err := m.Neighbors(NeighborsQuery{Node: "pkg:controllers", Predicates: []string{"imports"}, Direction: "out"})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0)
	for _, n := range res.Nodes {
		got = append(got, n.ID)
		if n.Depth != 1 {
			t.Errorf("%s at depth %d", n.ID, n.Depth)
		}
	}
	mustHave(t, "controllers imports", got, "pkg:business/actions", "pkg:business/services/orders", "pkg:storage/orderstore")
	if len(res.Nodes) != 3 || len(res.Edges) != 3 {
		t.Errorf("nodes %d edges %d", len(res.Nodes), len(res.Edges))
	}
	for _, e := range res.Edges {
		if e.From != "pkg:controllers" || e.Predicate != "imports" {
			t.Errorf("edge %+v", e)
		}
	}

	// direction matters: who imports a package
	res, _ = m.Neighbors(NeighborsQuery{Node: "controllers", Predicates: []string{"<imports>"}, Direction: "in"})
	if len(res.Nodes) != 1 || res.Nodes[0].ID != "pkg:routes" {
		t.Errorf("importers of controllers: %+v", res.Nodes)
	}
	if e := res.Edges[0]; e.From != "pkg:routes" || e.To != "pkg:controllers" {
		t.Errorf("an edge keeps its stored direction: %+v", e)
	}

	// depth walks further; every node is reported once, nearest first
	res, _ = m.Neighbors(NeighborsQuery{Node: "pkg:routes", Predicates: []string{"imports"}, Direction: "out", Depth: 2})
	depths := map[string]int{}
	for _, n := range res.Nodes {
		if _, dup := depths[n.ID]; dup {
			t.Errorf("%s twice", n.ID)
		}
		depths[n.ID] = n.Depth
	}
	if depths["pkg:controllers"] != 1 || depths["pkg:business/actions"] != 2 {
		t.Errorf("depths: %v", depths)
	}

	// a limit truncates and says so, and the degree still tells how big the node is
	res, _ = m.Neighbors(NeighborsQuery{Node: "pkg:controllers", Limit: 2})
	if len(res.Nodes) != 2 || !res.Truncated {
		t.Errorf("limit: %d nodes, truncated=%v", len(res.Nodes), res.Truncated)
	}
	if res.Degree["out:imports"] != 3 || res.Degree["in:in_package"] != 3 {
		t.Errorf("degree: %v", res.Degree)
	}
	for _, e := range res.Edges {
		in := func(id string) bool {
			if id == res.Node.ID {
				return true
			}
			for _, n := range res.Nodes {
				if n.ID == id {
					return true
				}
			}
			return false
		}
		if !in(e.From) || !in(e.To) {
			t.Errorf("an edge to a node that was cut off: %+v", e)
		}
	}

	// bookkeeping stays out of a table's neighbours, unless asked for
	res, _ = m.Neighbors(NeighborsQuery{Node: "table:orders", Direction: "in"})
	for _, n := range res.Nodes {
		if n.Kind == "annotation" {
			t.Errorf("annotation among neighbours: %s", n.ID)
		}
	}
	res, _ = m.Neighbors(NeighborsQuery{Node: "table:orders", Predicates: []string{"subject"}, Direction: "in"})
	if len(res.Nodes) == 0 {
		t.Error("annotations should show when their predicate is asked for")
	}

	// errors
	if _, err = m.Neighbors(NeighborsQuery{Node: "pkg:controllers", Direction: "sideways"}); err == nil {
		t.Error("bad direction")
	}
	if _, err = m.Neighbors(NeighborsQuery{Node: "pkg:controllers", Predicates: []string{"befriends"}}); err == nil || !strings.Contains(err.Error(), "imports") {
		t.Errorf("unknown predicate should list the known ones: %v", err)
	}
	if _, err = m.Neighbors(NeighborsQuery{Node: "no.such.thing"}); err == nil {
		t.Error("unknown node")
	}
}
