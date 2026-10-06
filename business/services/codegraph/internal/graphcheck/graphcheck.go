// Package graphcheck holds the invariants every graph the indexer writes must
// satisfy, whatever project it describes: the ones an agent relies on when it
// follows edges without checking each one. It is test support, shared by the
// tests that index this repository and the ones that index the fixture; nothing
// outside tests imports it.
package graphcheck

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/quad"
)

// Options say what a particular graph is expected to contain.
type Options struct {
	// Layers are the layers allowed to have written quads (meta always is).
	Layers []string
	// RequireEdges are predicates of which the graph must hold at least one edge.
	RequireEdges []string
}

// Result is what Check found, for assertions specific to the graph.
type Result struct {
	Quads []quad.Quad
	Kind  map[string]string // node ID -> kind, without the "kind:" prefix
	Edges map[string]int    // structural predicate -> number of edges
}

// AllQuads reads every quad of the store.
func AllQuads(t *testing.T, h *cayley.Handle) []quad.Quad {
	t.Helper()
	it := h.QuadStore.QuadsAllIterator()
	defer it.Close()
	var out []quad.Quad
	for it.Next(context.Background()) {
		q := h.QuadStore.Quad(it.Result())
		if !q.IsValid() {
			t.Fatalf("a quad with an unresolved node: %v", q)
		}
		out = append(out, q)
	}
	return out
}

func iri(v quad.Value) string {
	if i, ok := v.(quad.IRI); ok {
		return string(i)
	}
	return ""
}

var (
	// sensitiveName is written out here on purpose, not shared with the code
	// under test: it is the oracle.
	sensitiveName = regexp.MustCompile(`(?i)key|secret|password|token|dsn`)
	valueKinds    = []string{"package", "file", "func", "type", "route", "template", "config_key", "env_var", "mcp_tool", "doc", "spec", "table", "column", "index", "migration"}
)

// edge rules: predicate -> the kinds of subject and object it may join.
// "=" as the object list means "the same kind as the subject".
var edges = map[string][2][]string{
	"defined_in":     {{"func", "type", "template"}, {"file"}},
	"in_package":     {{"file"}, {"package"}},
	"imports":        {{"package"}, {"package"}},
	"has_layer":      {{"build"}, {"layer"}},
	"handled_by":     {{"route"}, {"func"}},
	"uses_mw":        {{"route"}, {"middleware"}},
	"implemented_by": {{"middleware"}, {"func"}},
	"renders":        {{"func", "template"}, {"template"}},
	"uses_config":    {{"func", "package"}, {"config_key"}},
	"reads_env":      {{"config_key", "func"}, {"env_var"}},
	"registers":      {{"func"}, {"mcp_tool"}},
	"has_item":       {{"spec"}, {"spec_item"}},
	"mentions":       {{"doc", "spec"}, valueKinds},
	"covers":         {{"spec_item"}, valueKinds},
	"has_column":     {{"table", "index"}, {"column"}},
	"has_index":      {{"table"}, {"index"}},
	"fk_to":          {{"column", "table"}, {"="}},
	"implicit_ref":   {{"column", "table"}, {"="}},
	"many_to_many":   {{"table"}, {"table"}},
	"introduced_in":  {{"table", "column", "index"}, {"migration"}},
	"reads_table":    {{"func"}, {"table"}},
	"writes_table":   {{"func"}, {"table"}},
	"maps_to":        {{"type"}, {"table"}},
	"subject":        {{"annotation"}, {"column", "table"}},
	"object":         {{"annotation"}, {"column", "table"}},
	"via":            {{"annotation"}, {"table"}},
}

func in(list []string, kind string) bool {
	for _, x := range list {
		if x == kind {
			return true
		}
	}
	return false
}

// Check reads the whole store and fails the test for every violation.
func Check(t *testing.T, h *cayley.Handle, opts Options) *Result {
	t.Helper()
	quads := AllQuads(t, h)
	res := &Result{Quads: quads, Kind: map[string]string{}, Edges: map[string]int{}}

	// Every quad belongs to a layer, with IRI subject and predicate, and no
	// connection string or URL hides in a free-text attribute's neighbours.
	known := map[string]bool{"meta": true}
	for _, l := range opts.Layers {
		known[l] = true
	}
	layerOf := func(q quad.Quad) string { return strings.TrimPrefix(iri(q.Label), "layer:") }
	for _, q := range quads {
		if !strings.HasPrefix(iri(q.Label), "layer:") || !known[layerOf(q)] {
			t.Fatalf("quad outside the layers %v: %v", opts.Layers, q)
		}
		if iri(q.Subject) == "" || iri(q.Predicate) == "" {
			t.Fatalf("subject and predicate must be IRIs: %v", q)
		}
		if s, ok := q.Object.(quad.String); ok && strings.Contains(string(s), "@tcp(") {
			t.Errorf("a connection string in the graph: %v", q)
		}
	}

	// Exactly one kind per node, and one owning layer for its type and name.
	owner := map[string]string{}
	for _, q := range quads {
		if iri(q.Predicate) != "type" {
			continue
		}
		s := iri(q.Subject)
		kind := strings.TrimPrefix(iri(q.Object), "kind:")
		if prev, dup := res.Kind[s]; dup {
			t.Errorf("%s has two kinds: %s and %s", s, prev, kind)
		}
		res.Kind[s] = kind
		owner[s] = layerOf(q)
	}
	for _, q := range quads {
		s, p := iri(q.Subject), iri(q.Predicate)
		if _, ok := res.Kind[s]; !ok {
			t.Errorf("%s has no type quad (%v)", s, q)
		}
		if p == "name" && owner[s] != layerOf(q) {
			t.Errorf("%s: type is owned by layer %s but name by %s", s, owner[s], layerOf(q))
		}
	}

	// Structural edges: nothing dangles, and each joins the kinds it should.
	for _, q := range quads {
		p := iri(q.Predicate)
		rule, ok := edges[p]
		if !ok {
			continue
		}
		s, o := iri(q.Subject), iri(q.Object)
		if o == "" {
			t.Errorf("structural edge to a literal: %v", q)
			continue
		}
		res.Edges[p]++
		if _, dangling := res.Kind[o]; !dangling {
			t.Errorf("dangling reference: %v", q)
			continue
		}
		okObj := in(rule[1], res.Kind[o])
		if len(rule[1]) == 1 && rule[1][0] == "=" {
			okObj = res.Kind[o] == res.Kind[s]
		}
		if !in(rule[0], res.Kind[s]) || !okObj {
			t.Errorf("%s joins %s to %s: %v", p, res.Kind[s], res.Kind[o], q)
		}
		if p == "imports" && s == o {
			t.Errorf("package imports itself: %v", q)
		}
	}
	for _, p := range opts.RequireEdges {
		if res.Edges[p] == 0 {
			t.Errorf("no %s edge in this graph", p)
		}
	}

	// Sensitive things carry no default, ever.
	sensitive := map[string]bool{}
	for _, q := range quads {
		if iri(q.Predicate) == "sensitive" && q.Object == quad.Value(quad.Bool(true)) {
			sensitive[iri(q.Subject)] = true
		}
	}
	for _, q := range quads {
		if iri(q.Predicate) != "default" {
			continue
		}
		s := iri(q.Subject)
		if sensitive[s] {
			t.Errorf("a sensitive setting carries a default: %v", q)
		}
		if res.Kind[s] == "column" {
			col := s[strings.LastIndex(s, ".")+1:]
			if sensitiveName.MatchString(col) {
				t.Errorf("a sensitive column carries a default: %v", q)
			}
		}
	}

	checkSchema(t, quads, res)
	return res
}

// checkSchema holds the invariants of the db layer's nodes, when there are any.
func checkSchema(t *testing.T, quads []quad.Quad, res *Result) {
	t.Helper()
	// Where an element comes from is one of three things, on every table, column
	// and index; a column or index only drifts when both sides have it.
	sources := map[string]string{}
	for _, q := range quads {
		if iri(q.Predicate) == "source" {
			s := iri(q.Subject)
			v, _ := q.Object.(quad.String)
			sources[s] = string(v)
			if v != "migration" && v != "live" && v != "both" {
				t.Errorf("%s has source %q", s, v)
			}
		}
	}
	for id, kind := range res.Kind {
		if (kind == "table" || kind == "column" || kind == "index") && sources[id] == "" {
			t.Errorf("%s has no source", id)
		}
	}
	for _, q := range quads {
		s, p := iri(q.Subject), iri(q.Predicate)
		if p == "drift" && sources[s] != "both" && !strings.Contains(string(q.Object.(quad.String)), "foreign key") {
			t.Errorf("%s drifts but exists on one side only (%s)", s, sources[s])
		}
		// An index's columns belong to its own table.
		if p == "has_column" && res.Kind[s] == "index" {
			tbl := strings.TrimPrefix(s, "idx:")
			tbl = tbl[:strings.LastIndex(tbl, ".")]
			if !strings.HasPrefix(iri(q.Object), "col:"+tbl+".") {
				t.Errorf("index %s lists a column of another table: %v", s, q.Object)
			}
		}
	}

	// Each inferred edge has exactly one annotation that names it, says how it
	// was inferred, and how sure that is; each annotation names a real edge.
	type edge struct{ s, p, o string }
	inferred := map[edge]bool{}
	for _, q := range quads {
		if p := iri(q.Predicate); p == "implicit_ref" || p == "many_to_many" {
			inferred[edge{iri(q.Subject), p, iri(q.Object)}] = true
		}
	}
	annotated := map[edge]int{}
	for id, kind := range res.Kind {
		if kind != "annotation" {
			continue
		}
		var e edge
		var conf int64 = -1
		method := ""
		for _, q := range quads {
			if iri(q.Subject) != id {
				continue
			}
			switch iri(q.Predicate) {
			case "subject":
				e.s = iri(q.Object)
			case "object":
				e.o = iri(q.Object)
			case "predicate":
				e.p = string(q.Object.(quad.String))
			case "method":
				method = string(q.Object.(quad.String))
			case "confidence":
				conf = int64(q.Object.(quad.Int))
			}
		}
		if !inferred[e] {
			t.Errorf("annotation %s names an edge that does not exist: %v", id, e)
		}
		if method == "" || conf < 0 || conf > 100 {
			t.Errorf("annotation %s: method %q confidence %d", id, method, conf)
		}
		annotated[e]++
	}
	for e := range inferred {
		if annotated[e] != 1 {
			t.Errorf("inferred edge %v has %d annotations, want 1", e, annotated[e])
		}
	}
}
