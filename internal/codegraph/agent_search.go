package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// --- search ---

// SearchQuery is the input of graph_search.
type SearchQuery struct {
	Text  string
	Kinds []string
	Limit int
}

// SearchHit is one node that matched.
type SearchHit struct {
	NodeRef
	Score   int    `json:"score"`
	Match   string `json:"match"`
	Summary string `json:"summary,omitempty"`
}

// SearchResult is the output of graph_search.
type SearchResult struct {
	Query     string      `json:"query"`
	Hits      []SearchHit `json:"hits"`
	Total     int         `json:"total"`
	Truncated bool        `json:"truncated,omitempty"`
	AsOf      string      `json:"as_of"`
}

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

// kindAliases are the words an agent is likely to use for a kind.
var kindAliases = map[string]string{
	"function": "func", "method": "func", "pkg": "package", "dir": "package", "directory": "package",
	"endpoint": "route", "url": "route", "tool": "mcp_tool", "mcp": "mcp_tool", "config": "config_key", "cfg": "config_key",
	"env": "env_var", "environment": "env_var", "component": "template", "tmpl": "template", "templ": "template",
	"col": "column", "idx": "index", "mw": "middleware", "typ": "type", "struct": "type", "interface": "type",
	"item": "spec_item", "documentation": "doc", "cmd": "command", "task": "command", "job": "command", "cron": "command",
}

// kindSet validates and normalizes kind names ("func", "kind:func", "function").
func (m *Model) kindSet(kinds []string) (map[string]bool, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	set := map[string]bool{}
	for _, k := range kinds {
		k = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(k), "kind:"))
		if a, ok := kindAliases[k]; ok {
			k = a
		}
		if _, ok := m.byKind[k]; !ok || k == "" {
			return nil, fmt.Errorf("unknown kind %q; kinds in this graph: %s", k, strings.Join(m.kindNames(), ", "))
		}
		set[k] = true
	}
	return set, nil
}

func (m *Model) kindNames() []string {
	var out []string
	for k := range m.byKind {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// searchFields are the attributes text is looked for in, with their weight.
var searchFields = []struct {
	attr string
	w    int
}{
	{"name", 60}, {"path", 45}, {"title", 40}, {"signature", 30}, {"description", 20}, {"doc", 20},
	{"message", 20}, {"comment", 20}, {"note", 15}, {"why_skipped", 15}, {"columns", 15},
}

// Search finds nodes whose ID, name, path or text contain every word of text,
// best match first. kinds, if set, limits the kinds searched.
func (m *Model) Search(text string, kinds []string, limit int) []SearchHit {
	set, _ := m.kindSet(kinds)
	hits := m.search(text, set)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// SearchNodes is graph_search.
func (m *Model) SearchNodes(q SearchQuery) (*SearchResult, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("text is required")
	}
	set, err := m.kindSet(q.Kinds)
	if err != nil {
		return nil, err
	}
	limit := clampLimit(q.Limit, defaultSearchLimit, maxSearchLimit)
	hits := m.search(q.Text, set)
	res := &SearchResult{Query: q.Text, Total: len(hits), AsOf: m.AsOf()}
	if len(hits) > limit {
		hits, res.Truncated = hits[:limit], true
	}
	res.Hits = hits
	if res.Hits == nil {
		res.Hits = []SearchHit{}
	}
	return res, nil
}

func clampLimit(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

func (m *Model) search(text string, kinds map[string]bool) []SearchHit {
	tokens := strings.Fields(strings.ToLower(text))
	if len(tokens) == 0 {
		return nil
	}
	var hits []SearchHit
	for _, id := range m.ids {
		n := m.nodes[id]
		if n.Kind == "" {
			continue
		}
		if kinds != nil {
			if !kinds[n.Kind] {
				continue
			}
		} else if n.Kind == "annotation" || n.Kind == "layer" || n.Kind == "build" {
			continue // bookkeeping, found only when asked for
		}
		score, match, ok := 0, "", true
		low := strings.ToLower(id)
		key := low
		if i := strings.IndexByte(low, ':'); i >= 0 {
			key = low[i+1:]
		}
		for ti, t := range tokens {
			best, field := 0, ""
			try := func(w int, f string) {
				if w > best {
					best, field = w, f
				}
			}
			switch {
			case low == t:
				try(110, "id")
			case key == t:
				try(105, "id") // `table:items` for "items", ahead of anything merely named items
			case strings.Contains(low, t):
				try(50, "id")
			}
			for _, f := range searchFields {
				for _, v := range n.Attrs[f.attr] {
					s, isStr := v.(string)
					if !isStr {
						continue
					}
					s = strings.ToLower(s)
					switch {
					case f.attr == "name" && s == t:
						try(100, "name")
					case f.attr == "name" && strings.HasPrefix(s, t):
						try(80, "name")
					case strings.Contains(s, t):
						try(f.w, f.attr)
					}
				}
			}
			if best == 0 {
				ok = false
				break
			}
			score += best
			if ti == 0 {
				match = field
			}
		}
		if !ok {
			continue
		}
		hits = append(hits, SearchHit{NodeRef: m.Ref(n), Score: score, Match: match, Summary: m.summary(n)})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if a, b := rankOf(hits[i].Kind), rankOf(hits[j].Kind); a != b {
			return a < b
		}
		return hits[i].ID < hits[j].ID
	})
	return hits
}

// summary is the one line that says what a node is.
func (m *Model) summary(n *Node) string {
	for _, a := range []string{"doc", "description", "title", "message", "signature", "comment"} {
		if s := n.Str(a); s != "" {
			return oneLine(s, 160)
		}
	}
	return ""
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}

// --- neighbors ---

// NeighborsQuery is the input of graph_neighbors.
type NeighborsQuery struct {
	Node       string
	Direction  string // out, in or both (default)
	Predicates []string
	Depth      int
	Limit      int
}

// NeighborNode is a node reached from the start, with its distance.
type NeighborNode struct {
	NodeRef
	Depth int `json:"depth"`
}

// Edge is one stored triple between two nodes.
type Edge struct {
	From      string `json:"from"`
	Predicate string `json:"predicate"`
	To        string `json:"to"`
}

// NeighborsResult is the output of graph_neighbors.
type NeighborsResult struct {
	Node      NodeRef        `json:"node"`
	Direction string         `json:"direction"`
	Depth     int            `json:"depth"`
	Nodes     []NeighborNode `json:"nodes"`
	Edges     []Edge         `json:"edges"`
	Degree    map[string]int `json:"degree,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
	AsOf      string         `json:"as_of"`
}

const (
	defaultNeighborDepth = 1
	maxNeighborDepth     = 3
	defaultNeighborLimit = 50
	maxNeighborLimit     = 300
)

type adjacent struct {
	pred  string
	out   bool // the stored edge leaves the node
	other string
}

// adjacency lists a node's edges, sorted by predicate, then the other end.
func adjacency(n *Node) []adjacent {
	var adj []adjacent
	for p, ids := range n.Out {
		for _, id := range ids {
			adj = append(adj, adjacent{p, true, id})
		}
	}
	for p, ids := range n.In {
		for _, id := range ids {
			adj = append(adj, adjacent{p, false, id})
		}
	}
	sort.Slice(adj, func(i, j int) bool {
		a, b := adj[i], adj[j]
		if a.pred != b.pred {
			return a.pred < b.pred
		}
		if a.other != b.other {
			return a.other < b.other
		}
		return a.out && !b.out
	})
	return adj
}

// edgePredicates are the predicates that link two nodes anywhere in the graph.
func (m *Model) edgePredicates() []string {
	set := map[string]bool{}
	for _, n := range m.nodes {
		for p := range n.Out {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Neighbors is graph_neighbors: a bounded breadth-first walk from a node.
func (m *Model) Neighbors(q NeighborsQuery) (*NeighborsResult, error) {
	start, err := m.resolveOne(q.Node)
	if err != nil {
		return nil, err
	}
	dir := strings.ToLower(strings.TrimSpace(q.Direction))
	switch dir {
	case "":
		dir = "both"
	case "out", "in", "both":
	default:
		return nil, fmt.Errorf("direction must be out, in or both, not %q", q.Direction)
	}
	depth := q.Depth
	switch {
	case depth <= 0:
		depth = defaultNeighborDepth
	case depth > maxNeighborDepth:
		depth = maxNeighborDepth
	}
	limit := clampLimit(q.Limit, defaultNeighborLimit, maxNeighborLimit)

	var want map[string]bool
	if len(q.Predicates) > 0 {
		known := map[string]bool{}
		for _, p := range m.edgePredicates() {
			known[p] = true
		}
		want = map[string]bool{}
		for _, p := range q.Predicates {
			p = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(p), "<"), ">")
			if !known[p] {
				return nil, fmt.Errorf("unknown predicate %q; edge predicates in this graph: %s", p, strings.Join(m.edgePredicates(), ", "))
			}
			want[p] = true
		}
	}
	// Annotation nodes (the "how sure" of an inferred edge) and the build's layer
	// list are bookkeeping; they show only when asked for.
	showBook := start.Kind == "annotation" || start.Kind == "build" || start.Kind == "layer" ||
		want["subject"] || want["object"] || want["via"] || want["has_layer"]
	keep := func(a adjacent) bool {
		if want != nil && !want[a.pred] {
			return false
		}
		if dir == "out" && !a.out || dir == "in" && a.out {
			return false
		}
		if !showBook {
			if o := m.nodes[a.other]; o == nil || o.Kind == "annotation" || o.Kind == "layer" || o.Kind == "build" {
				return false
			}
		}
		return true
	}

	res := &NeighborsResult{Node: m.Ref(start), Direction: dir, Depth: depth, Degree: map[string]int{}, AsOf: m.AsOf()}
	for _, a := range adjacency(start) {
		if keep(a) {
			if a.out {
				res.Degree["out:"+a.pred]++
			} else {
				res.Degree["in:"+a.pred]++
			}
		}
	}

	seen := map[string]int{start.ID: 0}
	order := []string{start.ID}
	var edges []Edge
	edgeSeen := map[Edge]bool{}
	frontier := []string{start.ID}
	for d := 1; d <= depth && len(frontier) > 0 && !res.Truncated; d++ {
		var next []string
	expand:
		for _, id := range frontier {
			for _, a := range adjacency(m.nodes[id]) {
				if !keep(a) {
					continue
				}
				e := Edge{From: id, Predicate: a.pred, To: a.other}
				if !a.out {
					e = Edge{From: a.other, Predicate: a.pred, To: id}
				}
				if _, known := seen[a.other]; !known {
					if len(order) > limit {
						res.Truncated = true
						break expand
					}
					seen[a.other] = d
					order = append(order, a.other)
					next = append(next, a.other)
				}
				if !edgeSeen[e] {
					edgeSeen[e] = true
					edges = append(edges, e)
				}
			}
		}
		frontier = next
	}
	included := map[string]bool{}
	for _, id := range order {
		included[id] = true
		if id != start.ID {
			res.Nodes = append(res.Nodes, NeighborNode{NodeRef: m.Ref(m.nodes[id]), Depth: seen[id]})
		}
	}
	for _, e := range edges {
		if included[e.From] && included[e.To] {
			res.Edges = append(res.Edges, e)
		}
	}
	if res.Nodes == nil {
		res.Nodes = []NeighborNode{}
	}
	if res.Edges == nil {
		res.Edges = []Edge{}
	}
	return res, nil
}
