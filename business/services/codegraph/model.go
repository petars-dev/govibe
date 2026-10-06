package codegraph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
)

// This file is the read side of the graph. The agent tools (graph_context,
// graph_impact, graph_path, ...) do not run Gizmo: they work on a Model, an
// in-memory copy of every quad the indexer wrote, which is small (a few
// thousand quads, a few milliseconds to read) and lets breadth-first searches,
// closures and rankings be plain Go.

// Node is one subject of the graph: its kind, its literal attributes and its
// edges, by predicate. Edge targets are node IDs, sorted.
type Node struct {
	ID    string
	Kind  string // without the "kind:" prefix; "" for a node that has no type quad
	Attrs map[string][]any
	Out   map[string][]string
	In    map[string][]string
}

// Str returns the first string attribute named pred, or "".
func (n *Node) Str(pred string) string {
	for _, v := range n.Attrs[pred] {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Int returns the first integer attribute named pred, or 0.
func (n *Node) Int(pred string) int {
	for _, v := range n.Attrs[pred] {
		if i, ok := v.(int); ok {
			return i
		}
	}
	return 0
}

// Bool returns the first boolean attribute named pred, or false.
func (n *Node) Bool(pred string) bool {
	for _, v := range n.Attrs[pred] {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// Time returns the first time attribute named pred, or the zero time.
func (n *Node) Time(pred string) time.Time {
	for _, v := range n.Attrs[pred] {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}
	return time.Time{}
}

// Has reports whether the node has an attribute or an edge named pred.
func (n *Node) Has(pred string) bool {
	return len(n.Attrs[pred]) > 0 || len(n.Out[pred]) > 0
}

// first returns the first target of an outgoing edge, or "".
func (n *Node) first(pred string) string {
	if t := n.Out[pred]; len(t) > 0 {
		return t[0]
	}
	return ""
}

// annotation is what an inferred edge's `ann:` node says about it.
type annotation struct {
	conf   int
	method string // how it was inferred: name_match, junction_pk, junction_unique
	via    string // the junction table of a many_to_many edge
}

// Model is a read-only snapshot of the graph the indexer wrote: every quad
// under a `layer:*` label. Hand-added quads are not part of it. A Model is
// not safe for concurrent use (it builds lookup tables lazily); load one per
// call.
type Model struct {
	nodes  map[string]*Node
	ids    []string // sorted
	byKind map[string][]*Node
	quads  int

	// lazily built lookup tables
	pathIdx   map[string][]*Node
	symIdx    map[string][]*Node
	symIdxLow map[string][]*Node
	annIdx    map[string]annotation
	pkgFuncs  map[string][]*Node
	findIdx   map[string][]*Node // finding nodes by the node they are about
}

// Load reads the indexed graph from qs.
func Load(ctx context.Context, qs graph.QuadStore) (*Model, error) {
	return loadModel(ctx, qs, "")
}

// loadModel reads every quad under a `layer:*` label, except those of the layer
// named skip (the findings layer reads the graph without its own last output).
func loadModel(ctx context.Context, qs graph.QuadStore, skip string) (*Model, error) {
	m := newModel()
	it := qs.QuadsAllIterator()
	defer it.Close()

	n := 0
	for it.Next(ctx) {
		if n++; n%2048 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		q := qs.Quad(it.Result())
		if !q.IsValid() {
			return nil, errors.New("graph store returned a quad with an unresolved node (GRAPH_SCHEMA.md, F8); restart the server")
		}
		if skip != "" && q.Label == quad.Value(LayerLabel(skip)) {
			continue
		}
		m.add(q)
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("read graph: %w", err)
	}
	m.finish()
	return m, nil
}

// modelOf builds a model from quads held in memory, as Load would read them
// from the store.
func modelOf(quads []quad.Quad) *Model {
	m := newModel()
	for _, q := range quads {
		m.add(q)
	}
	m.finish()
	return m
}

func newModel() *Model {
	return &Model{nodes: map[string]*Node{}, byKind: map[string][]*Node{}}
}

// add records one quad. Hand-added quads (no `layer:*` label, or a string
// label) are not part of the model.
func (m *Model) add(q quad.Quad) {
	label, ok := q.Label.(quad.IRI)
	if !ok || !strings.HasPrefix(string(label), labelPrefix) {
		return
	}
	subj, ok := q.Subject.(quad.IRI)
	if !ok {
		return
	}
	pred, ok := q.Predicate.(quad.IRI)
	if !ok {
		return
	}
	m.quads++
	s := m.node(string(subj))
	switch o := q.Object.(type) {
	case quad.IRI:
		if pred == PredType {
			s.Kind = strings.TrimPrefix(string(o), "kind:")
			return
		}
		t := m.node(string(o))
		s.Out[string(pred)] = append(s.Out[string(pred)], string(o))
		t.In[string(pred)] = append(t.In[string(pred)], string(subj))
	default:
		s.Attrs[string(pred)] = append(s.Attrs[string(pred)], nativeValue(o))
	}
}

// finish sorts the edge lists and indexes the nodes by kind.
func (m *Model) finish() {
	m.ids = make([]string, 0, len(m.nodes))
	for id, nd := range m.nodes {
		m.ids = append(m.ids, id)
		for _, lists := range []map[string][]string{nd.Out, nd.In} {
			for p, l := range lists {
				lists[p] = sortedUniqueStrings(l)
			}
		}
	}
	sort.Strings(m.ids)
	for _, id := range m.ids {
		nd := m.nodes[id]
		m.byKind[nd.Kind] = append(m.byKind[nd.Kind], nd)
	}
}

func (m *Model) node(id string) *Node {
	if n, ok := m.nodes[id]; ok {
		return n
	}
	n := &Node{ID: id, Attrs: map[string][]any{}, Out: map[string][]string{}, In: map[string][]string{}}
	m.nodes[id] = n
	return n
}

// nativeValue turns a literal into a plain Go value. The writer wraps a string
// that would otherwise read back as an IRI or a typed value in backticks (lit),
// so the wrapping is undone here.
func nativeValue(v quad.Value) any {
	switch x := v.(type) {
	case quad.String:
		return unlit(string(x))
	case quad.Int:
		return int(x)
	case quad.Bool:
		return bool(x)
	case quad.Time:
		return time.Time(x)
	default:
		return fmt.Sprint(v.Native())
	}
}

func unlit(s string) string {
	if len(s) >= 2 && s[0] == '`' && s[len(s)-1] == '`' {
		inner := s[1 : len(s)-1]
		if quad.StringToValue(inner) != quad.Value(quad.String(inner)) {
			return inner
		}
	}
	return s
}

func sortedUniqueStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	sort.Strings(in)
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// Node returns the node with the ID, or nil.
func (m *Model) Node(id string) *Node { return m.nodes[id] }

// Kind returns every node of a kind (without the "kind:" prefix), sorted by ID.
func (m *Model) Kind(kind string) []*Node { return m.byKind[kind] }

// Quads is the number of quads the model was read from.
func (m *Model) Quads() int { return m.quads }

// Empty reports whether nothing has been indexed yet.
func (m *Model) Empty() bool { return m.quads == 0 }

// targets resolves edge target IDs to nodes.
func (m *Model) targets(ids []string) []*Node {
	out := make([]*Node, 0, len(ids))
	for _, id := range ids {
		if n := m.nodes[id]; n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (m *Model) out(n *Node, pred string) []*Node { return m.targets(n.Out[pred]) }
func (m *Model) in(n *Node, pred string) []*Node  { return m.targets(n.In[pred]) }

// NodeRef is the compact description of a node used in every tool's output.
type NodeRef struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	Line int    `json:"line,omitempty"`
}

// Ref describes a node. A function or type has no path of its own; it takes the
// one of the file that defines it. A route's `path` attribute is its URL, which
// its name already carries.
func (m *Model) Ref(n *Node) NodeRef {
	r := NodeRef{ID: n.ID, Kind: n.Kind, Name: n.Str("name"), Line: n.Int("line")}
	switch n.Kind {
	case "route", "build", "layer", "annotation":
	case "func", "type":
		if f := m.nodes[n.first("defined_in")]; f != nil {
			r.Path = f.Str("path")
		}
	default:
		r.Path = n.Str("path")
	}
	return r
}

func (m *Model) refs(ns []*Node) []NodeRef {
	out := make([]NodeRef, 0, len(ns))
	for _, n := range ns {
		out = append(out, m.Ref(n))
	}
	return out
}

// kindRank orders kinds by how interesting they usually are to someone asking
// what a change touches: entry points first, bookkeeping last.
var kindRank = func() map[string]int {
	order := []string{"route", "mcp_tool", "func", "type", "template", "middleware", "package", "file",
		"table", "column", "index", "migration", "config_key", "env_var", "spec_item", "spec", "doc", "finding"}
	r := make(map[string]int, len(order))
	for i, k := range order {
		r[k] = i
	}
	return r
}()

func rankOf(kind string) int {
	if r, ok := kindRank[kind]; ok {
		return r
	}
	return len(kindRank)
}

// sortNodes orders nodes by kind rank, then ID.
func sortNodes(ns []*Node) {
	sort.SliceStable(ns, func(i, j int) bool {
		if a, b := rankOf(ns[i].Kind), rankOf(ns[j].Kind); a != b {
			return a < b
		}
		return ns[i].ID < ns[j].ID
	})
}

// --- freshness ---

// LayerInfo is one layer's last run, from its `layer:<name>` node.
type LayerInfo struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Quads     int       `json:"quads,omitempty"`
	IndexedAt time.Time `json:"indexed_at,omitzero"`
	Note      string    `json:"note,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// Freshness says how current the graph is, from `build:latest` and the
// `layer:<name>` nodes.
type Freshness struct {
	Indexed        bool        `json:"indexed"`
	GitSHA         string      `json:"git_sha,omitempty"`
	Dirty          bool        `json:"dirty,omitempty"`
	IndexedAt      time.Time   `json:"indexed_at,omitzero"`
	IndexerVersion string      `json:"indexer_version,omitempty"`
	DurationMS     int         `json:"duration_ms,omitempty"`
	Added          int         `json:"added"`   // quads the last run added, over the data layers
	Removed        int         `json:"removed"` // and removed
	Quads          int         `json:"quads"`
	Layers         []LayerInfo `json:"layers,omitempty"`
	StaleLayers    []string    `json:"stale_layers,omitempty"`
}

// Freshness reads the build and layer-run nodes. StaleLayers is left for the
// caller, which owns the indexer.
func (m *Model) Freshness() Freshness {
	f := Freshness{Quads: m.quads}
	b := m.nodes[string(buildID)]
	if b == nil {
		return f
	}
	f.Indexed = true
	f.GitSHA, f.Dirty = b.Str("git_sha"), b.Bool("dirty")
	f.IndexedAt, f.IndexerVersion = b.Time("indexed_at"), b.Str("indexer_version")
	f.DurationMS, f.Added, f.Removed = b.Int("duration_ms"), b.Int("added"), b.Int("removed")
	for _, l := range m.out(b, "has_layer") {
		f.Layers = append(f.Layers, LayerInfo{
			Name: l.Str("name"), Status: l.Str("status"), Quads: l.Int("quads"),
			IndexedAt: l.Time("indexed_at"), Note: l.Str("note"), Error: l.Str("error"),
		})
	}
	sort.Slice(f.Layers, func(i, j int) bool { return f.Layers[i].Name < f.Layers[j].Name })
	return f
}

// AsOf is a one-line statement of what the answers were computed from, for
// every tool's output: "2026-10-06T12:00:00Z @ 2c5dfd3+dirty".
func (m *Model) AsOf() string {
	f := m.Freshness()
	if !f.Indexed {
		return "not indexed yet"
	}
	s := f.IndexedAt.Format(time.RFC3339)
	if f.GitSHA != "" {
		s += " @ " + f.GitSHA
		if f.Dirty {
			s += "+dirty"
		}
	}
	return s
}

// layerStatus is the recorded status of a layer: "" when it has never run, so
// the layer is not built in this graph.
func (m *Model) layerStatus(name string) string {
	if n := m.nodes[string(layerNodeID(name))]; n != nil && n.Kind == "layer" {
		return n.Str("status")
	}
	return ""
}

// layerBuilt reports whether a layer has written its quads (ok or partial).
func (m *Model) layerBuilt(name string) bool {
	s := m.layerStatus(name)
	return s == statusOK || s == statusPartial
}

// warnings lists the layers whose data an answer may be missing, for the
// output of tools that depend on them.
func (m *Model) warnings(layers ...string) []string {
	var out []string
	for _, l := range layers {
		switch m.layerStatus(l) {
		case statusOK, statusPartial:
		case "":
			out = append(out, fmt.Sprintf("the %s layer is not built in this graph", l))
		case statusSkipped:
			out = append(out, fmt.Sprintf("the %s layer was skipped (%s)", l, m.nodes[string(layerNodeID(l))].Str("note")))
		default:
			out = append(out, fmt.Sprintf("the %s layer failed (%s): its data may be missing or old", l, m.nodes[string(layerNodeID(l))].Str("error")))
		}
	}
	return out
}
