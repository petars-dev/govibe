package codegraph

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

// moduleWorld writes a module of the given files to a temp dir and returns a world
// over it, as an explicit reindex would see it.
func moduleWorld(t *testing.T, files map[string]string) *world {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n\ngo 1.25.0\n")
	for p, c := range files {
		writeFile(t, root, p, c)
	}
	return newTestWorld(t, root, Options{})
}

// callEdges returns the `calls` edges of a built layer as "from -> to".
func callEdges(out *layerOutput) []string {
	var edges []string
	for _, q := range out.quads {
		if q.Predicate == quad.Value(PredCalls) {
			edges = append(edges, fmt.Sprintf("%s -> %s", q.Subject, q.Object))
		}
	}
	return edges
}

func hasEdge(out *layerOutput, from, to string) bool {
	for _, e := range callEdges(out) {
		if e == "<"+from+"> -> <"+to+">" {
			return true
		}
	}
	return false
}

// annotationOf returns the annotation facts of a calls edge, or nil.
func annotationFacts(out *layerOutput, from, to string) facts {
	aid := annID(quad.IRI(from), PredCalls, quad.IRI(to))
	f := facts{out.quads}
	if !f.exists(string(aid)) {
		return facts{}
	}
	return f
}

func TestCallsLayerGolden(t *testing.T) {
	checkGolden(t, "calls", buildLayer(t, fixtureWorld(t), callsLayer{}).quads)
}

// The fixture is written so that each question the calls layer exists for has an
// answer: a handler that reaches no table, one that reaches orders through a
// service and an interface, one that skips the service.
func TestCallsLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), callsLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	// Acceptance 1: HelloAPI calls actions.Hello and the unexported helper, and
	// nothing else. A plain call has no annotation: it is certain.
	for _, to := range []string{"func:business/actions.Hello", "func:controllers.writeJSON"} {
		if !hasEdge(out, "func:controllers.HelloAPI", to) {
			t.Errorf("HelloAPI should call %s; edges: %v", to, callEdges(out))
		}
	}
	if got := f.out("func:controllers.HelloAPI", "calls"); len(got) != 2 {
		t.Errorf("HelloAPI calls %v, want exactly Hello and writeJSON", got)
	}
	if annotationFacts(out, "func:controllers.HelloAPI", "func:business/actions.Hello").quads != nil {
		t.Error("a plain call carries no annotation")
	}

	// Acceptance 2: route handler -> service -> (interface) -> store. The call
	// inside the handler's closure belongs to the function that declares it.
	if !hasEdge(out, "func:controllers.ListOrders", "func:business/services/orders.Service.List") {
		t.Errorf("a call inside a closure belongs to the enclosing function: %v", callEdges(out))
	}
	if !hasEdge(out, "func:business/services/orders.Service.List", "func:storage/orderstore.Store.List") {
		t.Fatalf("Service.List calls the store through the Lister interface: %v", callEdges(out))
	}
	if hasEdge(out, "func:business/services/orders.Service.List", "func:business/services/orders.Service.List") {
		t.Error("a function that could call itself through an interface has no edge to itself")
	}
	ann := annotationFacts(out, "func:business/services/orders.Service.List", "func:storage/orderstore.Store.List")
	if ann.quads == nil {
		t.Fatal("a call through an interface carries an annotation")
	}
	aid := string(annID("func:business/services/orders.Service.List", PredCalls, "func:storage/orderstore.Store.List"))
	if got := ann.one(t, aid, "method"); got != str(methodDispatch) {
		t.Errorf("method %v", got)
	}
	// Two types fit Lister (the store, and the service itself): 70, not 80.
	if got := ann.one(t, aid, "confidence"); got != quad.Int(70) {
		t.Errorf("confidence %v, want 70 for two candidates", got)
	}
	if got := ann.one(t, aid, "via"); got != iri("typ:business/services/orders.Lister") {
		t.Errorf("via %v", got)
	}
	if got := ann.one(t, aid, "predicate"); got != str("calls") {
		t.Errorf("predicate %v", got)
	}

	// The layering violation: GetOrder calls the store's method directly.
	if !hasEdge(out, "func:controllers.GetOrder", "func:storage/orderstore.Store.Get") {
		t.Errorf("GetOrder should call Store.Get: %v", callEdges(out))
	}

	// Generated templ output has no node, so a call into it is not an edge.
	if got := f.out("func:controllers.HomePage", "calls"); len(got) != 0 {
		t.Errorf("HomePage calls generated code only: %v", got)
	}
	// Wiring: the router builder calls what it wires.
	for _, to := range []string{"func:storage/orderstore.New", "func:business/services/orders.New", "func:controllers.ListOrders", "func:controllers.GetOrder", "func:devmcp.NewHandler", "func:middleware.RequestLogger"} {
		if !hasEdge(out, "func:routes.New", to) {
			t.Errorf("routes.New should call %s", to)
		}
	}
	if !hasEdge(out, "func:..main", "func:routes.New") {
		t.Error("main calls routes.New")
	}
	// Tool registration closures belong to the registering function.
	if !hasEdge(out, "func:devmcp.NewHandler", "func:devmcp.registerTools") {
		t.Error("NewHandler calls registerTools")
	}
}

// Everything in a call index must point at nodes the code layer wrote.
func TestCallsEdgesJoinCodeNodes(t *testing.T) {
	w := fixtureWorld(t)
	code := buildLayer(t, w, codeLayer{})
	calls := buildLayer(t, w, callsLayer{})
	known := map[string]bool{}
	for _, q := range code.quads {
		if q.Predicate == quad.Value(PredType) && q.Object == quad.Value(KindFunc) {
			known[string(q.Subject.(quad.IRI))] = true
		}
	}
	for _, q := range calls.quads {
		if q.Predicate != quad.Value(PredCalls) {
			continue
		}
		for _, end := range []quad.Value{q.Subject, q.Object} {
			if !known[string(end.(quad.IRI))] {
				t.Errorf("%v: %v is no function of the code layer", q, end)
			}
		}
	}
}

func TestCallsResolution(t *testing.T) {
	out := buildLayer(t, moduleWorld(t, map[string]string{
		"a/a.go": `package a

import "example.com/m/b"

type T struct{ b.Base }
type V int

func (t *T) Ptr()    { t.Helper() }
func (v V) Val()     {}
func (t *T) Helper() {}
func Plain()         {}

func Generic[X any](x X) X { return x }

func Methods() {
	t := &T{}
	t.Ptr()            // a method on a concrete type
	t.Promoted()       // a method promoted from an embedded struct of another package
	(*T).Helper(t)     // a method expression
	V(1).Val()         // a conversion, then a value-receiver method
	Plain()
	Generic[int](3)    // an explicit instantiation
	Generic("x")       // an inferred one
}

func Concurrent() {
	go Plain()
	defer Plain()
	f := Plain
	f() // a function value: not an edge
	func() { Plain() }() // a closure: the call is the enclosing function's
}

func Recursive(n int) {
	if n > 0 {
		Recursive(n - 1)
	}
}

var initialised = compute() // a package-level initializer has no caller

func compute() int { return 1 }

func init() { Plain() }
func init() { compute() }
`,
		"b/b.go": `package b

type Base struct{}

func (Base) Promoted() {}
`,
	}), callsLayer{})

	for _, tc := range []struct{ from, to string }{
		{"func:a.T.Ptr", "func:a.T.Helper"},
		{"func:a.Methods", "func:a.T.Ptr"},
		{"func:a.Methods", "func:b.Base.Promoted"},
		{"func:a.Methods", "func:a.T.Helper"},
		{"func:a.Methods", "func:a.V.Val"},
		{"func:a.Methods", "func:a.Plain"},
		{"func:a.Methods", "func:a.Generic"},
		{"func:a.Concurrent", "func:a.Plain"},
		{"func:a.init", "func:a.Plain"},
		{"func:a.init#2", "func:a.compute"},
	} {
		if !hasEdge(out, tc.from, tc.to) {
			t.Errorf("missing %s -> %s", tc.from, tc.to)
		}
	}
	for _, e := range callEdges(out) {
		switch {
		case strings.HasPrefix(e, "<func:a.Recursive> ->"):
			t.Errorf("a recursive call is not an edge: %s", e)
		case strings.HasSuffix(e, "-> <func:a.compute>") && !strings.HasPrefix(e, "<func:a.init#2>"):
			t.Errorf("only init#2 calls compute (a package variable's initializer has no caller): %s", e)
		}
	}
	// Each `init` keeps its own identity.
	if hasEdge(out, "func:a.init", "func:a.compute") || hasEdge(out, "func:a.init#2", "func:a.Plain") {
		t.Error("the two init functions were confused")
	}
	// The three plain edges of Concurrent collapse to one; the function value call is none.
	n := 0
	for _, e := range callEdges(out) {
		if strings.HasPrefix(e, "<func:a.Concurrent> ->") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Concurrent has %d edges, want 1 (go, defer and a closure call Plain; a function value is not seen)", n)
	}
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
}

// How many candidates a call through an interface has decides how sure each edge is.
func TestCallsInterfaceDispatch(t *testing.T) {
	impls := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "type Impl%d struct{}\n\nfunc (Impl%d) Run() {}\n\n", i, i)
		}
		return b.String()
	}
	build := func(n int, extra string) *layerOutput {
		return buildLayer(t, moduleWorld(t, map[string]string{
			"a/a.go": "package a\n\ntype Runner interface{ Run() }\n\nfunc Use(r Runner) { r.Run() }\n\n" + impls(n) + extra,
		}), callsLayer{})
	}

	for _, tc := range []struct {
		impls, conf int
	}{{1, 80}, {2, 70}, {3, 60}, {4, 60}, {5, 50}, {8, 50}, {9, 30}, {maxImplementers, 30}} {
		out := build(tc.impls, "")
		got := 0
		for i := 0; i < tc.impls; i++ {
			to := fmt.Sprintf("func:a.Impl%d.Run", i)
			if !hasEdge(out, "func:a.Use", to) {
				t.Errorf("%d implementers: no edge to %s", tc.impls, to)
				continue
			}
			got++
			aid := string(annID("func:a.Use", PredCalls, quad.IRI(to)))
			if c := (facts{out.quads}).one(t, aid, "confidence"); c != quad.Int(tc.conf) {
				t.Errorf("%d implementers: confidence %v, want %d", tc.impls, c, tc.conf)
			}
			if v := (facts{out.quads}).one(t, aid, "via"); v != iri("typ:a.Runner") {
				t.Errorf("via %v", v)
			}
		}
		if got != tc.impls {
			t.Errorf("%d implementers, %d edges", tc.impls, got)
		}
	}

	// Past the limit nothing is linked, and the layer says so.
	out := build(maxImplementers+1, "")
	for _, e := range callEdges(out) {
		if strings.HasPrefix(e, "<func:a.Use>") {
			t.Errorf("a call through a very wide interface is not expanded: %s", e)
		}
	}
	if want := fmt.Sprintf("%d implementers", maxImplementers+1); len(out.warnings) != 1 || !strings.Contains(out.warnings[0], want) {
		t.Errorf("warnings: %v", out.warnings)
	}

	// A plain call that is also a possible interface call stays certain.
	out = build(1, "func Direct() { Use(Impl0{}); Impl0{}.Run() }\n\nfunc Both(r Runner) { r.Run(); Impl0{}.Run() }\n")
	if aid := annID("func:a.Both", PredCalls, "func:a.Impl0.Run"); (facts{out.quads}).exists(string(aid)) {
		t.Error("the surer evidence for a pair wins: a direct call of Impl0.Run is not an interface call")
	}
	if !hasEdge(out, "func:a.Both", "func:a.Impl0.Run") {
		t.Error("Both calls Impl0.Run")
	}
}

// Only interfaces the module declares are expanded: a call through error or
// io.Reader has nothing in the module worth linking to.
func TestCallsIgnoresInterfacesOutsideTheModule(t *testing.T) {
	out := buildLayer(t, moduleWorld(t, map[string]string{
		"a/a.go": `package a

import (
	"fmt"
	"io"
)

type Err struct{}

func (Err) Error() string { return "e" }

type R struct{}

func (R) Read(p []byte) (int, error) { return 0, nil }
func (R) String() string             { return "r" }

func Use(err error, r io.Reader, s fmt.Stringer) {
	_ = err.Error()
	_, _ = r.Read(nil)
	_ = s.String()
}
`,
	}), callsLayer{})
	if edges := callEdges(out); len(edges) != 0 {
		t.Errorf("calls through error, io.Reader and fmt.Stringer are not expanded: %v", edges)
	}
}

// A package that does not compile is named, its calls are missing and the layer
// is partial; the rest of the module is unaffected.
func TestCallsPartialWhenAPackageDoesNotTypeCheck(t *testing.T) {
	out := buildLayer(t, moduleWorld(t, map[string]string{
		"good/good.go": "package good\n\nfunc A() { B() }\n\nfunc B() {}\n",
		"bad/bad.go":   "package bad\n\nfunc C() { missing() }\n",
	}), callsLayer{})
	if !hasEdge(out, "func:good.A", "func:good.B") {
		t.Errorf("the good package still has its edges: %v", callEdges(out))
	}
	if len(out.warnings) != 1 || !strings.Contains(out.warnings[0], "example.com/m/bad did not type-check cleanly") {
		t.Errorf("warnings: %v", out.warnings)
	}
}

func TestCallsFailsWhenNothingTypeChecks(t *testing.T) {
	w := moduleWorld(t, map[string]string{"a/a.go": "package a\n\nfunc A() { missing() }\n"})
	if _, err := (callsLayer{}).build(context.Background(), w); err == nil || !strings.Contains(err.Error(), "none of the 1 packages type-checked") {
		t.Errorf("err = %v", err)
	}
}

func TestCallsHonoursCancellation(t *testing.T) {
	w := moduleWorld(t, map[string]string{"a/a.go": "package a\n\nfunc A() {}\n"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.ctx = ctx
	if _, err := (callsLayer{}).build(ctx, w); err == nil {
		t.Error("a cancelled context must not produce an index")
	}
}

// A file the code layer does not index (generated templ output, a test) is not a
// caller, whatever it calls.
func TestCallsSkipsCodeTheCodeLayerSkips(t *testing.T) {
	out := buildLayer(t, moduleWorld(t, map[string]string{
		"a/a.go":         "package a\n\nfunc A() { gen() }\n",
		"a/gen_templ.go": "package a\n\nfunc gen() { A() }\n",
		"a/a_test.go":    "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { A() }\n",
	}), callsLayer{})
	if edges := callEdges(out); len(edges) != 0 {
		t.Errorf("calls into and out of skipped files are not edges: %v", edges)
	}
}

func TestCallsLayerOnThisRepository(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the whole module")
	}
	w := newTestWorld(t, repoRoot, Options{})
	out := buildLayer(t, w, callsLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	// The public entry point calls the kernel's, and a dev handler calls the indexer.
	if !hasEdge(out, "func:..Main", "func:internal/kernel.Main") {
		t.Errorf("govibe.Main -> kernel.Main missing; %d edges", len(callEdges(out)))
	}
	if !hasEdge(out, "func:internal/devtools/devui.DevGraphReindex", "func:internal/codegraph.Indexer.Run") {
		t.Error("DevGraphReindex -> Indexer.Run missing")
	}
	if n := len(callEdges(out)); n < 500 {
		t.Errorf("only %d call edges in this repository", n)
	}
}
