package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// Scope says whose nodes a Model shows. A project's own graph is its own code, docs
// and connections; the framework it runs on is one switch away and always labelled.
//
// The scope is applied once, when the Model is built: nodes of the other origin are
// dropped, and with them every edge that touches one, so the views, the reports and
// the agent tools need no per-view logic. What is dropped is remembered (Hidden), so a
// page can say how much it hides and an error can say where a node went.
//
// Scope is a parameter of Load, never a global, and it is not a filter on the store:
// graph_query, the Cayley HTTP API and the hand-added quads are deliberately unscoped
// (they read quads, not a Model), and say so.
type Scope string

const (
	ScopeApp       Scope = "app"       // the project's own nodes (the default)
	ScopeFramework Scope = "framework" // what the framework contributes: its routes, middleware, settings, tools
	ScopeAll       Scope = "all"       // both, each labelled with its origin
)

// Origins: where a node comes from.
const (
	OriginApp       = "app"
	OriginFramework = "framework"
)

// ParseScope reads the `scope` parameter of a page or a tool. Empty is the default,
// the app.
func ParseScope(s string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "app", "my-app", "my app":
		return ScopeApp, nil
	case "framework", "fw":
		return ScopeFramework, nil
	case "all", "both":
		return ScopeAll, nil
	}
	return "", fmt.Errorf("unknown scope %q: use app, framework or all", s)
}

// Title is how a page names the scope.
func (s Scope) Title() string {
	switch s {
	case ScopeFramework:
		return "Framework"
	case ScopeAll:
		return "All"
	}
	return "My app"
}

// shows reports whether a node of the origin is part of the scope. An empty origin is
// bookkeeping (the build, the layer runs, the rules): it is in every scope.
func (s Scope) shows(origin string) bool {
	switch {
	case origin == "" || s == ScopeAll || s == "":
		return true
	case s == ScopeApp:
		return origin == OriginApp
	case s == ScopeFramework:
		return origin == OriginFramework
	}
	return true
}

// Hidden says what a scoped Model left out.
type Hidden struct {
	Scope    Scope          `json:"scope"`
	Nodes    int            `json:"nodes"`               // nodes of the other origin that were dropped
	ByOrigin map[string]int `json:"by_origin,omitempty"` // the same, by the origin they have
	ByKind   map[string]int `json:"by_kind,omitempty"`   // and by kind
}

// hiddenIndex is what the filter drops, so that it can be accounted for.
type hiddenIndex struct {
	nodes  map[string]*Node    // every dropped node, as it was in the whole graph
	origin map[string]string   // and its origin there (a finding's is that of a node that is gone from here)
	out    map[string][]string // a kept node -> the dropped nodes its outgoing edges pointed at
	in     map[string][]string // and the ones that pointed at it
}

// Scope is the scope the Model was built for.
func (m *Model) Scope() Scope {
	if m.scope == "" {
		return ScopeAll
	}
	return m.scope
}

// Hidden counts what the scope left out; the zero value for scope all.
func (m *Model) Hidden() Hidden {
	h := Hidden{Scope: m.Scope()}
	if m.hidden == nil {
		return h
	}
	h.ByOrigin, h.ByKind = map[string]int{}, map[string]int{}
	for _, n := range m.hidden.nodes {
		if n.Kind == "" {
			continue
		}
		h.Nodes++
		h.ByOrigin[m.hidden.origin[n.ID]]++
		h.ByKind[n.Kind]++
	}
	return h
}

// originOf says where a node comes from: its `origin` attribute where a layer wrote
// one (a route, a middleware, a setting, a tool, an external), a finding's is that of
// the node it is about, and everything else was indexed from the project's own tree,
// so it is the app's. Bookkeeping nodes have none ("").
func (m *Model) originOf(n *Node) string {
	switch n.Kind {
	case "build", "layer", "rule":
		return ""
	case "finding":
		for _, id := range n.Out["about"] {
			if t := m.nodes[id]; t != nil && t != n {
				return m.originOf(t)
			}
		}
		return OriginApp
	}
	if o := n.Str("origin"); o != "" {
		return o
	}
	return OriginApp
}

// scoped returns the Model restricted to a scope. A Model of scope all is returned
// as it is.
func (m *Model) scoped(scope Scope) *Model {
	if scope == "" || scope == ScopeAll {
		m.scope = ScopeAll
		return m
	}
	keep := make(map[string]bool, len(m.nodes))
	for id, n := range m.nodes {
		// A node with no type is only the target of an edge whose own node a layer did not
		// write (a dangling reference): it belongs to no origin, so no scope shows it.
		keep[id] = n.Kind != "" && scope.shows(m.originOf(n))
	}
	// An annotation says how sure an inferred edge is: with either end gone it has
	// nothing to describe.
	for _, a := range m.byKind["annotation"] {
		for _, p := range []string{"subject", "object"} {
			for _, t := range a.Out[p] {
				if !keep[t] {
					keep[a.ID] = false
				}
			}
		}
	}

	out := newModel()
	out.scope, out.quads = scope, m.quads
	out.hidden = &hiddenIndex{nodes: map[string]*Node{}, origin: map[string]string{}, out: map[string][]string{}, in: map[string][]string{}}
	for id, n := range m.nodes {
		if !keep[id] {
			out.hidden.nodes[id] = n
			out.hidden.origin[id] = m.originOf(n)
			continue
		}
		nn := &Node{ID: n.ID, Kind: n.Kind, Attrs: n.Attrs, Out: map[string][]string{}, In: map[string][]string{}}
		for _, side := range []struct {
			from, to map[string][]string
			dropped  map[string][]string
		}{{n.Out, nn.Out, out.hidden.out}, {n.In, nn.In, out.hidden.in}} {
			for pred, targets := range side.from {
				var kept []string
				for _, t := range targets {
					if keep[t] {
						kept = append(kept, t)
					} else {
						side.dropped[id] = append(side.dropped[id], t)
					}
				}
				if len(kept) > 0 {
					side.to[pred] = kept
				}
			}
		}
		out.nodes[id] = nn
	}
	for id := range out.hidden.out {
		out.hidden.out[id] = sortedUniqueStrings(out.hidden.out[id])
	}
	for id := range out.hidden.in {
		out.hidden.in[id] = sortedUniqueStrings(out.hidden.in[id])
	}
	out.ids = make([]string, 0, len(out.nodes))
	for id := range out.nodes {
		out.ids = append(out.ids, id)
	}
	sort.Strings(out.ids)
	for _, id := range out.ids {
		nd := out.nodes[id]
		out.byKind[nd.Kind] = append(out.byKind[nd.Kind], nd)
	}
	return out
}

// hiddenNeighbours are the nodes a kept node is connected to in the other scope.
func (m *Model) hiddenNeighbours(id string) []*Node {
	if m.hidden == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*Node
	for _, list := range [][]string{m.hidden.out[id], m.hidden.in[id]} {
		for _, t := range list {
			if n := m.hidden.nodes[t]; n != nil && n.Kind != "" && !seen[t] && n.Kind != "annotation" && n.Kind != "finding" {
				seen[t] = true
				out = append(out, n)
			}
		}
	}
	sortNodes(out)
	return out
}

// elsewhere explains a reference that matches nothing here but names a node the scope
// hides: "it exists in scope framework".
func (m *Model) elsewhere(ref string) string {
	if m.hidden == nil {
		return ""
	}
	ref = strings.TrimSpace(ref)
	for _, n := range m.hidden.nodes {
		if n.Kind == "" || n.Kind == "annotation" {
			continue
		}
		if n.ID == ref || n.Str("name") == ref || (n.Kind == "route" && n.Str("method")+" "+n.Str("path") == ref) {
			o := m.hidden.origin[n.ID]
			return fmt.Sprintf("%s is a %s of the %s, which scope %s hides; pass scope=all (or scope=%s) to see it",
				ref, strings.ReplaceAll(n.Kind, "_", " "), o, m.Scope(), o)
		}
	}
	return ""
}

// usesText is the one line an inspector shows for what a node connects to in the
// other scope: "uses framework: web, config". Empty when there is nothing.
func (m *Model) usesText(id string) string {
	hidden := m.hiddenNeighbours(id)
	if len(hidden) == 0 {
		return ""
	}
	// Libraries are not the framework: an external of another module than the framework's is
	// said to be a library's, though its origin is "framework" (not the project's own code).
	fw := ""
	if b := m.nodes[string(buildID)]; b != nil {
		fw = b.Str("framework_module")
	}
	byOrigin := map[string][]string{}
	for _, n := range hidden {
		o := m.hidden.origin[n.ID]
		if n.Kind == "external" && fw != "" && n.Str("module") != fw {
			o = "libraries"
		}
		byOrigin[o] = append(byOrigin[o], hiddenName(n))
	}
	var parts []string
	for _, origin := range []string{OriginFramework, "libraries", OriginApp} {
		names := sortedUniqueStrings(byOrigin[origin])
		if len(names) == 0 {
			continue
		}
		const show = 6
		more := ""
		if len(names) > show {
			more = fmt.Sprintf(" and %d more", len(names)-show)
			names = names[:show]
		}
		parts = append(parts, "uses "+origin+": "+strings.Join(names, ", ")+more)
	}
	return strings.Join(parts, "; ")
}

// hiddenName is how a line of text names a node: the framework's `web` or `web.ServeHTMX`,
// a route as `GET /dev`, anything else by its name.
func hiddenName(n *Node) string {
	if n.Kind == "route" {
		return n.Str("method") + " " + n.Str("path")
	}
	if s := n.Str("name"); s != "" {
		return s
	}
	return n.ID
}
