package graphdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/graph/shape"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	cayleyhttp "github.com/cayleygraph/cayley/server/http"
	"github.com/cayleygraph/quad"
)

// These tests pin how the Cayley version in go.mod treats the values the graph
// indexer writes: IRI vs string vs typed-literal terms, how they come back out
// of /v2/read and Gizmo, how labels scope reads and deletes, and which write
// paths are safe. They are the executable half of the "Spike results" section
// of docs/public/GRAPH_SCHEMA.md. If one fails after a cayley/quad upgrade,
// re-read that section before changing the indexer.

type testGraph struct {
	t   *testing.T
	dir string
	h   *cayley.Handle
	api http.Handler // the same /dev/graph/api/* handler routes.go mounts
}

func newTestGraph(t *testing.T) *testGraph {
	t.Helper()
	g := &testGraph{t: t, dir: t.TempDir()}
	g.open()
	t.Cleanup(func() { g.h.Close() })
	return g
}

// open (re)opens the store at g.dir, as a server restart would.
func (g *testGraph) open() {
	g.t.Helper()
	h, err := Open(g.dir)
	if err != nil {
		g.t.Fatalf("open: %v", err)
	}
	g.h = h
	api := cayleyhttp.NewAPIv2(&graph.Handle{QuadStore: h.QuadStore, QuadWriter: h.QuadWriter})
	g.api = http.StripPrefix("/dev/graph", api)
}

func (g *testGraph) add(qs ...quad.Quad) {
	g.t.Helper()
	if err := g.h.AddQuadSet(qs); err != nil {
		g.t.Fatalf("add: %v", err)
	}
}

func (g *testGraph) http(method, path, ctype, body string) (int, string) {
	g.t.Helper()
	req := httptest.NewRequest(method, "/dev/graph/api/v2/"+path, strings.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	g.api.ServeHTTP(rec, req)
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, strings.TrimSpace(string(b))
}

type jsonQuad struct{ Subject, Predicate, Object, Label string }

// readJSON calls GET /v2/read?format=json (what the /dev/graph visualizer uses).
func (g *testGraph) readJSON(filters string) []jsonQuad {
	g.t.Helper()
	code, body := g.http("GET", "read?format=json&"+filters, "", "")
	if code != http.StatusOK {
		g.t.Fatalf("read: %d %s", code, body)
	}
	var out []jsonQuad
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		g.t.Fatalf("read: bad json: %v\n%s", err, body)
	}
	return out
}

// gizmo runs a query the way the graph_query MCP tool does and returns the
// results normalised through JSON, so assertions see what a client sees.
func (g *testGraph) gizmo(q string) ([]any, error) {
	g.t.Helper()
	ctx := context.Background()
	it, err := query.Execute(ctx, g.h.QuadStore, gizmo.Name, q, query.Options{Collation: query.JSON, Limit: 100})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []any
	for it.Next(ctx) {
		out = append(out, normalise(g.t, it.Result()))
	}
	return out, it.Err()
}

// ids returns the sorted "id" of every result; a result without an id (a node
// whose name could not be resolved) is reported as nil.
func (g *testGraph) ids(q string) []any {
	g.t.Helper()
	res, err := g.gizmo(q)
	if err != nil {
		g.t.Fatalf("gizmo %s: %v", q, err)
	}
	var out []any
	for _, r := range res {
		m, _ := r.(map[string]any)
		out = append(out, m["id"])
	}
	sortAny(out)
	return out
}

func normalise(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return out
}

func sortAny(a []any) {
	sort.Slice(a, func(i, j int) bool { return fmt.Sprintf("%T:%v", a[i], a[i]) < fmt.Sprintf("%T:%v", a[j], a[j]) })
}

// expect compares results with want after normalising want the same way
// (ints stay ints, and a quad.IRI is written the way clients see it: "<...>").
func expect(t *testing.T, what string, got []any, want ...any) {
	t.Helper()
	w := make([]any, len(want))
	for i, x := range want {
		if v, ok := x.(quad.IRI); ok {
			x = "<" + string(v) + ">"
		}
		w[i] = normalise(t, x)
	}
	sortAny(w)
	if len(got) == 0 && len(w) == 0 {
		return
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, w)
	}
}

func iri(s string) quad.IRI { return quad.IRI(s) }

func q4(s, p string, o quad.Value, label string) quad.Quad {
	return quad.Make(iri(s), iri(p), o, iri(label))
}

// Structural IDs are IRIs and attribute values are literals. A plain string in
// Gizmo is a *string* term, never an IRI, so IRIs must be addressed with
// g.IRI('x') or '<x>' - including predicates.
func TestIRIsAndStringsAreDistinctTerms(t *testing.T) {
	g := newTestGraph(t)
	g.add(
		q4("pkg:controllers", "imports", iri("pkg:business/actions"), "layer:code"),
		q4("pkg:controllers", "name", quad.String("controllers"), "layer:code"),
		// What the "Add a quad" form writes: plain strings, no label.
		quad.Make(quad.String("pkg:controllers"), quad.String("knows"), quad.String("bob"), nil),
	)

	expect(t, "bare V() + bare Out() matches nothing in IRI space",
		g.ids(`g.V('pkg:controllers').Out('imports').All()`))
	expect(t, "bare V() reaches only the string twin",
		g.ids(`g.V('pkg:controllers').Out('knows').All()`), "bob")
	expect(t, "bracket form",
		g.ids(`g.V('<pkg:controllers>').Out('<imports>').All()`), iri("pkg:business/actions"))
	expect(t, "g.IRI form",
		g.ids(`g.V(g.IRI('pkg:controllers')).Out(g.IRI('imports')).All()`), iri("pkg:business/actions"))
	expect(t, "IRI node with bare predicate matches nothing",
		g.ids(`g.V(g.IRI('pkg:controllers')).Out('imports').All()`))
	expect(t, "string literal objects come back bare",
		g.ids(`g.V(g.IRI('pkg:controllers')).Out(g.IRI('name')).All()`), "controllers")
	expect(t, "reverse traversal",
		g.ids(`g.V(g.IRI('pkg:business/actions')).In(g.IRI('imports')).All()`), iri("pkg:controllers"))
	expect(t, "predicates are enumerable as IRIs",
		g.ids(`g.V(g.IRI('pkg:controllers')).OutPredicates().All()`), iri("imports"), iri("name"))

	// The HTTP read filters follow the same rule: bare = string, <x> = IRI.
	bare := g.readJSON("sub=pkg:controllers")
	if len(bare) != 1 || bare[0].Predicate != "knows" {
		t.Errorf("sub=pkg:controllers (bare) should hit only the string twin, got %+v", bare)
	}
	bracketed := g.readJSON("sub=" + url.QueryEscape("<pkg:controllers>"))
	if len(bracketed) != 2 {
		t.Errorf("sub=<pkg:controllers> should hit the two IRI-subject quads, got %+v", bracketed)
	}
}

// Typed literals survive as native values in Gizmo; /v2/read renders them as
// schema-typed strings. Strings that merely look like IRIs are ambiguous in the
// JSON format, so the vocabulary module must never emit them.
func TestLiteralsRoundTrip(t *testing.T) {
	g := newTestGraph(t)
	when := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	doc := "Says \"hi\"\nover two lines, with <angle> brackets and {braces}."
	g.add(
		q4("func:controllers.HelloAPI", "line", quad.Int(12), "layer:code"),
		q4("func:controllers.HelloAPI", "exported", quad.Bool(true), "layer:code"),
		q4("func:controllers.HelloAPI", "doc", quad.String(doc), "layer:code"),
		q4("build:latest", "indexed_at", quad.Time(when), "layer:meta"),
	)

	fn := `g.V(g.IRI('func:controllers.HelloAPI'))`
	expect(t, "int is native", g.ids(fn+`.Out(g.IRI('line')).All()`), 12)
	expect(t, "bool is native", g.ids(fn+`.Out(g.IRI('exported')).All()`), true)
	expect(t, "string is exact, quotes and newlines intact", g.ids(fn+`.Out(g.IRI('doc')).All()`), doc)
	expect(t, "time renders as RFC 3339", g.ids(`g.V(g.IRI('build:latest')).Out(g.IRI('indexed_at')).All()`), "2026-10-01T12:00:00Z")

	expect(t, "Has with native int", g.ids(`g.V().Has(g.IRI('line'), 12).All()`), iri("func:controllers.HelloAPI"))
	expect(t, "Has with native bool", g.ids(`g.V().Has(g.IRI('exported'), true).All()`), iri("func:controllers.HelloAPI"))
	expect(t, "Has with bracketed predicate", g.ids(`g.V().Has('<exported>', true).All()`), iri("func:controllers.HelloAPI"))
	expect(t, "Has with a string never matches an int", g.ids(`g.V().Has(g.IRI('line'), '12').All()`))
	expect(t, "Has with a string never matches a bool", g.ids(`g.V().Has(g.IRI('exported'), 'true').All()`))

	want := map[string]string{
		"<line>":       `"12"^^<schema:Integer>`,
		"<exported>":   `"True"^^<schema:Boolean>`,
		"<doc>":        doc,
		"<indexed_at>": `"2026-10-01T12:00:00Z"^^<schema:DateTime>`,
	}
	for _, jq := range g.readJSON("") {
		if w, ok := want[jq.Predicate]; ok {
			if jq.Object != w {
				t.Errorf("/v2/read json object for %s = %q, want %q", jq.Predicate, jq.Object, w)
			}
			delete(want, jq.Predicate)
		}
	}
	if len(want) != 0 {
		t.Errorf("predicates missing from /v2/read output: %v", want)
	}
}

func TestStringShapedLikeIRIIsAmbiguousInJSON(t *testing.T) {
	g := newTestGraph(t)
	g.add(
		q4("pkg:a", "doc", quad.String("<looks-like-iri>"), "layer:code"),
		q4("pkg:a", "imports", iri("looks-like-iri"), "layer:code"),
	)
	var objs []string
	for _, jq := range g.readJSON("") {
		objs = append(objs, jq.Object)
	}
	if len(objs) != 2 || objs[0] != objs[1] || objs[0] != "<looks-like-iri>" {
		t.Errorf("JSON read should render the string and the IRI identically as <looks-like-iri>, got %v", objs)
	}
	// StringToValue is what JSON write, JSON read and ?sub= filters use; it
	// reinterprets more than the angle-bracket form.
	for _, str := range []string{"<x>", "_:x", `"x"^^<t>`, `"x"@en`} {
		if quad.StringToValue(str) == quad.String(str) {
			t.Errorf("StringToValue(%q) stayed a plain string; the vocabulary's escaping rule can be relaxed", str)
		}
	}
	// N-Quads keeps them apart, which is why exports and goldens use it.
	_, nq := g.http("GET", "read?format=nquads", "", "")
	if !strings.Contains(nq, `"<looks-like-iri>"`) || !strings.Contains(nq, `<looks-like-iri> <layer:code>`) {
		t.Errorf("N-Quads should quote the string and bracket the IRI:\n%s", nq)
	}
}

// The default HTTP wire format (N-Quads) cannot carry control characters, space,
// '"' or a backslash inside an IRI. With a label it fails loudly; without one a
// space silently splits the IRI into garbage terms. Everything else printable
// round-trips.
func TestNQuadsIRICharacterSafety(t *testing.T) {
	nq := quad.FormatByName("nquads")
	var bad []string
	for c := 0; c <= 0x7f; c++ {
		// The letter after c matters: a backslash is only accepted before one
		// of " ' \ b f n r t, so 'e' exposes it where 'b' would hide it.
		id := "x:a" + string(rune(c)) + "e"
		for _, label := range []string{"", " <layer:code>"} {
			line := fmt.Sprintf("<%s> <p> <%s>%s .\n", id, id, label)
			got, err := nq.Reader(strings.NewReader(line)).ReadQuad()
			if err != nil || got.Subject != iri(id) || got.Object != iri(id) {
				bad = append(bad, fmt.Sprintf("%#02x", c))
				break
			}
		}
	}
	for _, id := range []string{"x:ünï", "x:日本語", "x:a{b}|c^d#e?f*g:h/i"} {
		line := fmt.Sprintf("<%s> <p> <%s> <layer:code> .\n", id, id)
		got, err := nq.Reader(strings.NewReader(line)).ReadQuad()
		if err != nil || got.Subject != iri(id) || got.Object != iri(id) {
			t.Errorf("%q should round-trip through N-Quads, got %v (err %v)", id, got, err)
		}
	}
	var want []string
	for c := 0; c <= 0x1f; c++ {
		want = append(want, fmt.Sprintf("%#02x", c))
	}
	want = append(want, "0x20", "0x22", "0x5c", "0x7f")
	if !reflect.DeepEqual(bad, want) {
		t.Errorf("IRI characters N-Quads cannot carry:\n got  %v\n want %v", bad, want)
	}

	// The silent failure: this is how a space-separated route ID would be stored.
	q, err := nq.Reader(strings.NewReader("<route:GET /x> <handled_by> <func:x.X> .\n")).ReadQuad()
	if err != nil || q.Subject != quad.String("<route:GET") {
		t.Errorf("expected a silent mis-split of a spaced IRI, got %v (err %v)", q, err)
	}

	// The ID shape the vocabulary uses (no spaces; braces and colons are fine)
	// goes through /v2/write and comes back out of Gizmo untouched.
	g := newTestGraph(t)
	line := "<route:GET:/dev/db/api/tables/{table}> <handled_by> <func:controllers.DevDBDescribeTable> <layer:manual> .\n"
	if code, body := g.http("POST", "write", "application/n-quads", line); code != http.StatusOK {
		t.Fatalf("write: %d %s", code, body)
	}
	expect(t, "route ID round-trips via N-Quads",
		g.ids(`g.V(g.IRI('route:GET:/dev/db/api/tables/{table}')).Out(g.IRI('handled_by')).All()`),
		iri("func:controllers.DevDBDescribeTable"))
}

// Any ID is writable through the Go API and the JSON wire format, including
// ones N-Quads rejects. Spaces are therefore *discouraged* (see above), not
// impossible.
func TestJSONAndGoAPICarryAnyIRI(t *testing.T) {
	g := newTestGraph(t)
	id := "route:GET /api/hello"
	g.add(q4(id, "p", iri("o"), "l"))
	expect(t, "Go API write found by Gizmo", g.ids(fmt.Sprintf(`g.V(g.IRI(%q)).Out(g.IRI('p')).All()`, id)), iri("o"))

	body := fmt.Sprintf(`[{"subject":%q,"predicate":"<p2>","object":"<o>","label":"<l>"}]`, "<"+id+">")
	if code, out := g.http("POST", "write", "application/json", body); code != http.StatusOK {
		t.Fatalf("json write: %d %s", code, out)
	}
	if code, out := g.http("POST", "delete", "application/json", body); code != http.StatusOK {
		t.Fatalf("json delete: %d %s", code, out)
	}
	expect(t, "JSON delete removed it", g.ids(fmt.Sprintf(`g.V(g.IRI(%q)).Out(g.IRI('p2')).All()`, id)))
}

// A label is a reserved ownership marker: reads scoped to an IRI label see only
// that label, a string with the same text is a different term, and deletes must
// name the exact label.
func TestLabelsScopeReadsAndDeletes(t *testing.T) {
	g := newTestGraph(t)
	code := q4("pkg:a", "imports", iri("pkg:b"), "layer:code")
	meta := q4("build:latest", "git_sha", quad.String("405ab1d"), "layer:meta")
	manualNoLabel := quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil)
	manualStringLabel := quad.Make(quad.String("a"), quad.String("b"), quad.String("c"), quad.String("layer:code"))
	g.add(code, meta, manualNoLabel, manualStringLabel)

	readLabel := func(label quad.Value) []quad.Quad {
		it := shape.FilterQuads(nil, nil, nil, []quad.Value{label}).BuildIterator(g.h.QuadStore)
		rd := graph.NewResultReader(g.h.QuadStore, it)
		defer rd.Close()
		var out []quad.Quad
		for {
			q, err := rd.ReadQuad()
			if err != nil {
				return out
			}
			out = append(out, q)
		}
	}
	if got := readLabel(iri("layer:code")); !reflect.DeepEqual(got, []quad.Quad{code}) {
		t.Errorf("IRI label read = %v, want only %v", got, code)
	}
	if got := readLabel(quad.String("layer:code")); !reflect.DeepEqual(got, []quad.Quad{manualStringLabel}) {
		t.Errorf("string label read = %v, want only %v", got, manualStringLabel)
	}
	if got := g.readJSON("label=" + url.QueryEscape("<layer:meta>")); len(got) != 1 || got[0].Predicate != "<git_sha>" {
		t.Errorf("/v2/read label=<layer:meta> = %+v", got)
	}
	if got := g.readJSON("label=layer:meta"); len(got) != 0 {
		t.Errorf("bare label filter must not match the IRI label, got %+v", got)
	}

	// Deleting without the label does not find the quad.
	unlabelled := quad.Make(code.Subject, code.Predicate, code.Object, nil)
	if err := g.h.RemoveQuad(unlabelled); err == nil {
		t.Error("RemoveQuad without the label should fail with 'quad does not exist'")
	}
	expect(t, "quad survives a label-less delete", g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`), iri("pkg:b"))

	// Same s/p/o under two labels are two quads, and Out() reports both.
	g.add(q4("pkg:a", "imports", iri("pkg:b"), "layer:other"))
	expect(t, "duplicate fact across layers shows up twice",
		g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`), iri("pkg:b"), iri("pkg:b"))
}

// The shape of the Phase 1 diff writer: one tolerant ApplyDeltas per layer that
// deletes stale quads (with their exact labels), adds new ones, and shrugs off
// duplicates and already-gone quads. Manual quads are untouched.
func TestApplyDeltasDiffWriter(t *testing.T) {
	g := newTestGraph(t)
	manual := quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil)
	old := q4("pkg:a", "imports", iri("pkg:old"), "layer:code")
	keep := q4("pkg:a", "imports", iri("pkg:keep"), "layer:code")
	fresh := q4("pkg:a", "imports", iri("pkg:new"), "layer:code")
	gone := q4("pkg:a", "imports", iri("pkg:gone"), "layer:code")
	g.add(manual, old, keep)

	deltas := []graph.Delta{
		{Quad: old, Action: graph.Delete},
		{Quad: gone, Action: graph.Delete}, // already absent
		{Quad: fresh, Action: graph.Add},
		{Quad: keep, Action: graph.Add}, // already present
	}
	if err := g.h.QuadStore.ApplyDeltas(deltas, graph.IgnoreOpts{IgnoreDup: true, IgnoreMissing: true}); err != nil {
		t.Fatalf("tolerant ApplyDeltas: %v", err)
	}
	expect(t, "layer after diff", g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`), iri("pkg:keep"), iri("pkg:new"))
	expect(t, "manual quad untouched", g.ids(`g.V('alice').Out('knows').All()`), "bob")

	// Strict mode is what ApplyTransaction uses; it reports dups and misses.
	if err := g.h.QuadStore.ApplyDeltas([]graph.Delta{{Quad: keep, Action: graph.Add}}, graph.IgnoreOpts{}); err == nil {
		t.Error("strict ApplyDeltas should report a duplicate add")
	}
}

// HAZARD: a strict transaction that fails part-way rolls back on disk but leaves
// the store's in-process node cache believing the rolled-back nodes exist. A
// retry then writes a quad whose nodes have no names. Only a restart clears it.
// The Phase 1 writer must therefore use the tolerant ApplyDeltas above and never
// ApplyTransaction. If this test starts failing after a cayley upgrade the bug
// is fixed: revisit "Writer contract" in docs/public/GRAPH_SCHEMA.md.
func TestStrictTransactionFailurePoisonsNodeCache(t *testing.T) {
	g := newTestGraph(t)
	a := q4("pkg:a", "imports", iri("pkg:b"), "layer:code")

	tx := graph.NewTransaction()
	tx.AddQuad(a)
	tx.RemoveQuad(q4("pkg:missing", "imports", iri("pkg:b"), "layer:code"))
	if err := g.h.ApplyTransaction(tx); err == nil {
		t.Fatal("transaction with a missing delete should fail")
	}
	expect(t, "failed transaction rolled back", g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`))

	g.add(a) // the natural retry
	got := g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`)
	if len(got) != 1 || got[0] != nil {
		t.Errorf("expected the retried quad to resolve to a nameless node (the hazard), got %#v", got)
	}

	// A restart clears the cache, but the retried write is gone with it.
	if err := g.h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	g.open()
	expect(t, "after a restart", g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`))
	g.add(a)
	expect(t, "a write after the restart sticks", g.ids(`g.V(g.IRI('pkg:a')).Out(g.IRI('imports')).All()`), iri("pkg:b"))
}

// Everything an agent or the docs may rely on in Gizmo, as dialect facts.
func TestGizmoDialect(t *testing.T) {
	g := newTestGraph(t)
	g.add(
		q4("func:a.B", "type", iri("kind:func"), "layer:code"),
		q4("func:a.C", "type", iri("kind:func"), "layer:code"),
		q4("pkg:a", "type", iri("kind:package"), "layer:code"),
		q4("pkg:a", "name", quad.String("a"), "layer:code"),
	)
	funcs := []any{iri("func:a.B"), iri("func:a.C")}

	expect(t, "regex needs includeIRIs=true to see IRIs", g.ids(`g.V().Filter(regex('^func:', true)).All()`), funcs...)
	expect(t, "regex without it skips IRIs", g.ids(`g.V().Filter(regex('^func:')).All()`))
	expect(t, "like sees IRIs", g.ids(`g.V().Filter(like('func:a%')).All()`), funcs...)
	expect(t, "Has by IRI object", g.ids(`g.V().Has(g.IRI('type'), g.IRI('kind:func')).All()`), funcs...)
	expect(t, "Has by bracketed object", g.ids(`g.V().Has(g.IRI('type'), '<kind:func>').All()`), funcs...)
	expect(t, "Has by bare object is a string compare", g.ids(`g.V().Has(g.IRI('type'), 'kind:func').All()`))
	expect(t, "Out with a tag exposes the predicate", func() []any {
		res, err := g.gizmo(`g.V(g.IRI('pkg:a')).Out(g.IRI('name'), 'p').All()`)
		if err != nil || len(res) != 1 {
			t.Fatalf("tagged Out: %v %v", res, err)
		}
		return []any{res[0].(map[string]any)["p"]}
	}(), iri("name"))

	// Count() produces a single null through both the MCP path and /v2/query;
	// counting goes through Emit.
	if res, err := g.gizmo(`g.V().Has(g.IRI('type'), g.IRI('kind:func')).Count()`); err != nil || !reflect.DeepEqual(res, []any{nil}) {
		t.Errorf("Count() = %v (err %v), expected a single null", res, err)
	}
	if res, err := g.gizmo(`g.Emit(g.V().Has(g.IRI('type'), g.IRI('kind:func')).ToArray().length)`); err != nil || !reflect.DeepEqual(res, []any{float64(2)}) {
		t.Errorf("Emit(...ToArray().length) = %v (err %v), expected [2]", res, err)
	}

	// Methods the spec text assumed that do not exist in this Gizmo.
	for _, q := range []string{`g.V().Regex('x').All()`, `g.V().Like('x%').All()`, `g.V().Order().All()`, `g.V().Select(["a"])`} {
		if _, err := g.gizmo(q); err == nil {
			t.Errorf("%s unexpectedly worked; update GRAPH_SCHEMA.md", q)
		}
	}

	code, body := g.http("POST", "query?lang=gizmo", "", `g.V(g.IRI('pkg:a')).Out(g.IRI('name')).All()`)
	if code != http.StatusOK || body != `{"result":[{"id":"a"}]}` {
		t.Errorf("/v2/query = %d %s", code, body)
	}
}

// A graph exported as N-Quads can be imported into a fresh store and compares
// equal, including typed literals and multi-line strings. (Only because IDs
// avoid the characters covered in TestNQuadsIRICharacterSafety.)
func TestNQuadsExportImportRoundTrip(t *testing.T) {
	src := newTestGraph(t)
	src.add(
		q4("pkg:a", "imports", iri("pkg:b"), "layer:code"),
		q4("func:a.B", "doc", quad.String("Says \"hi\"\nsecond line"), "layer:code"),
		q4("func:a.B", "line", quad.Int(7), "layer:code"),
		q4("func:a.B", "exported", quad.Bool(false), "layer:code"),
		q4("build:latest", "indexed_at", quad.Time(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)), "layer:meta"),
		quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil),
	)
	code, exported := src.http("GET", "read?format=nquads", "", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, exported)
	}
	dst := newTestGraph(t)
	if code, body := dst.http("POST", "write", "application/n-quads", exported+"\n"); code != http.StatusOK {
		t.Fatalf("import: %d %s", code, body)
	}
	_, again := dst.http("GET", "read?format=nquads", "", "")
	if lines(exported) != lines(again) {
		t.Errorf("export -> import -> export differs:\n--- first\n%s\n--- second\n%s", exported, again)
	}
}

func lines(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	sort.Strings(l)
	return strings.Join(l, "\n")
}

// Every query in the "Querying" section of docs/public/GRAPH_SCHEMA.md, run
// against a handful of IRI quads shaped like the code layer's output, so the
// doc cannot advertise a query this Gizmo rejects.
func TestSchemaDocCookbook(t *testing.T) {
	g := newTestGraph(t)
	g.add(
		q4("pkg:controllers", "type", iri("kind:package"), "layer:code"),
		q4("pkg:controllers", "imports", iri("pkg:business/actions"), "layer:code"),
		q4("pkg:business/actions", "type", iri("kind:package"), "layer:code"),
		q4("func:controllers.HelloAPI", "type", iri("kind:func"), "layer:code"),
		q4("func:controllers.HelloAPI", "exported", quad.Bool(true), "layer:code"),
		q4("func:controllers.hidden", "type", iri("kind:func"), "layer:code"),
		q4("func:controllers.hidden", "exported", quad.Bool(false), "layer:code"),
		q4("func:business/actions.Hello", "type", iri("kind:func"), "layer:code"),
	)
	hello, hidden := iri("func:controllers.HelloAPI"), iri("func:controllers.hidden")

	expect(t, "Out bracket form", g.ids(`g.V('<pkg:controllers>').Out('<imports>').All()`), iri("pkg:business/actions"))
	expect(t, "In g.IRI form", g.ids(`g.V(g.IRI('pkg:business/actions')).In(g.IRI('imports')).All()`), iri("pkg:controllers"))
	expect(t, "all functions", g.ids(`g.V().Has(g.IRI('type'), g.IRI('kind:func')).All()`), hello, hidden, iri("func:business/actions.Hello"))
	expect(t, "exported", g.ids(`g.V().Has(g.IRI('exported'), true).All()`), hello)
	expect(t, "like", g.ids(`g.V().Filter(like('func:controllers.%')).All()`), hello, hidden)
	expect(t, "regex", g.ids(`g.V().Filter(regex('^func:controllers[.]', true)).All()`), hello, hidden)
	expect(t, "OutPredicates", g.ids(`g.V(g.IRI('pkg:controllers')).OutPredicates().All()`), iri("type"), iri("imports"))

	res, err := g.gizmo(`g.V(g.IRI('pkg:controllers')).Out(null, 'predicate').All()`)
	if err != nil || len(res) != 2 {
		t.Fatalf("tagged Out(null): %v %v", res, err)
	}
	var preds []any
	for _, r := range res {
		preds = append(preds, r.(map[string]any)["predicate"])
	}
	sortAny(preds)
	expect(t, "Out(null, tag) exposes each predicate", preds, iri("imports"), iri("type"))

	if res, err := g.gizmo(`g.Emit(g.V().Has(g.IRI('type'), g.IRI('kind:func')).ToArray().length)`); err != nil || !reflect.DeepEqual(res, []any{float64(3)}) {
		t.Errorf("count = %v (err %v), want [3]", res, err)
	}

	// The HTTP expansion query from the doc.
	got := g.readJSON("sub=" + url.QueryEscape("<pkg:controllers>"))
	if len(got) != 2 {
		t.Errorf("sub=<pkg:controllers> = %+v, want its two outgoing quads", got)
	}
}
