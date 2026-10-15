package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// graph_path: how does A reach B? Gizmo has no shortest path, so this is a
// breadth-first search in Go over the dependency edges ("X depends on Y": a
// route on its handler, a function on the table it reads, a package on the ones
// it imports). It runs in two tiers so an estimate is never passed off as a fact:
//
//  1. Exact: dependency edges only. A path here is made of edges the graph holds.
//  2. Package-level, only if tier 1 finds nothing: a function may also be walked
//     out to its file and package, and a package in to the files and functions
//     it holds. Without call edges (the opt-in `calls` layer) that is the only
//     way to get from a handler to the storage code its package imports. The
//     result is flagged `inferred`, with confidence 50, and says which hops are
//     package-level. With call edges, a route, function, tool or table is
//     connected by the calls it makes or by nothing: tier 2 is not tried between
//     them, and "not found" is an answer.
//
// Each hop carries how sure it is (a stored edge 100, an edge read from SQL strings
// 90, a call through an interface 80 or less), and a path is as sure as its weakest
// hop. Among paths, the one whose weakest hop is strongest wins, then the shortest.
//
// Both orientations are tried (A depends on B, then B on A), exact before
// inferred, and the steps are always reported from A to B.

// PathQuery is the input of graph_path.
type PathQuery struct {
	From     string
	To       string
	MaxDepth int
}

// PathStep is one hop, from one node to the next.
type PathStep struct {
	From      string `json:"from"`
	Predicate string `json:"predicate"`
	To        string `json:"to"`
	// Reverse: the stored edge points from `to` to `from`.
	Reverse bool `json:"reverse,omitempty"`
	// Kind: edge (a dependency), imports (package imports package) or
	// containment (a function in its file, a file in its package, a table's columns).
	Kind string `json:"kind"`
	// Confidence and Method say how sure an edge is and how it was found; they are
	// absent on the hops that are only membership or imports.
	Confidence int    `json:"confidence,omitempty"`
	Method     string `json:"method,omitempty"`
	// Via is the interface a call goes through.
	Via string `json:"via,omitempty"`
}

// PathResult is the output of graph_path.
type PathResult struct {
	From       NodeRef    `json:"from"`
	To         NodeRef    `json:"to"`
	Found      bool       `json:"found"`
	Depends    string     `json:"depends,omitempty"` // from_depends_on_to or to_depends_on_from
	Inferred   bool       `json:"inferred,omitempty"`
	Confidence int        `json:"confidence,omitempty"`
	Steps      []PathStep `json:"steps,omitempty"`
	Nodes      []NodeRef  `json:"nodes,omitempty"`
	Note       string     `json:"note,omitempty"`
	// When nothing links the two: what each side is known to reach.
	FromReaches []NodeRef `json:"from_reaches,omitempty"`
	ToReachedBy []NodeRef `json:"to_reached_by,omitempty"`
	AsOf        string    `json:"as_of"`
}

const (
	defaultPathDepth = 12
	maxPathDepth     = 24
	sideLimit        = 12
)

// dependencyPredicates: a stored edge subject --p--> object means the subject
// depends on the object.
var dependencyPredicates = []string{"handled_by", "run_by", "uses_mw", "implemented_by", "calls", "renders", "reads_table", "writes_table",
	"uses_config", "reads_env", "maps_to", "fk_to", "implicit_ref", "imports"}

// pathNeighbors lists the steps from n. forward follows dependencies (n depends
// on the target); otherwise it follows them backwards (the target depends on n).
// packageLevel adds the containment hops of tier 2.
func (m *Model) pathNeighbors(n *Node, forward, packageLevel bool) []PathStep {
	var out []PathStep
	for _, p := range dependencyPredicates {
		kind := "edge"
		if p == "imports" {
			kind = "imports"
		}
		if forward {
			for _, t := range n.Out[p] {
				out = append(out, PathStep{From: n.ID, Predicate: p, To: t, Kind: kind})
			}
		} else {
			for _, t := range n.In[p] {
				out = append(out, PathStep{From: n.ID, Predicate: p, To: t, Reverse: true, Kind: kind})
			}
		}
	}
	// A tool depends on the function that registers it.
	if forward {
		for _, t := range n.In["registers"] {
			out = append(out, PathStep{From: n.ID, Predicate: "registers", To: t, Reverse: true, Kind: "edge"})
		}
	} else {
		for _, t := range n.Out["registers"] {
			out = append(out, PathStep{From: n.ID, Predicate: "registers", To: t, Kind: "edge"})
		}
	}
	if !packageLevel {
		return out
	}
	contain := func(pred string, ids []string, reverse bool) {
		for _, t := range ids {
			out = append(out, PathStep{From: n.ID, Predicate: pred, To: t, Reverse: reverse, Kind: "containment"})
		}
	}
	switch n.Kind {
	case "func", "type", "template":
		contain("defined_in", n.Out["defined_in"], false)
	case "file":
		contain("in_package", n.Out["in_package"], false)
		contain("defined_in", n.In["defined_in"], true)
	case "package":
		contain("in_package", n.In["in_package"], true)
	case "table":
		contain("has_column", n.Out["has_column"], false)
		contain("has_index", n.Out["has_index"], false)
	case "column":
		for _, t := range m.in(n, "has_column") {
			if t.Kind == "table" {
				contain("has_column", []string{t.ID}, true)
			}
		}
	}
	return out
}

// shortestPath is a breadth-first search from src to dst. It returns the steps
// in order and whether dst was reached.
func (m *Model) shortestPath(src, dst string, forward, packageLevel bool, maxDepth, floor int) ([]PathStep, bool) {
	if src == dst {
		return nil, true
	}
	parent := map[string]PathStep{}
	seen := map[string]bool{src: true}
	frontier := []string{src}
	for d := 0; d < maxDepth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			for _, st := range m.pathNeighbors(m.nodes[id], forward, packageLevel) {
				if seen[st.To] || m.nodes[st.To] == nil {
					continue
				}
				if floor > 0 && st.Kind == "edge" {
					if conf, _ := m.stepConfidence(st); conf < floor {
						continue
					}
				}
				seen[st.To] = true
				parent[st.To] = st
				if st.To == dst {
					var rev []PathStep
					for at := dst; at != src; at = parent[at].From {
						rev = append(rev, parent[at])
					}
					steps := make([]PathStep, len(rev))
					for i, s := range rev {
						steps[len(rev)-1-i] = s
					}
					return steps, true
				}
				next = append(next, st.To)
			}
		}
		frontier = next
	}
	return nil, false
}

// flip turns steps found from B to A into steps from A to B.
func flip(steps []PathStep) []PathStep {
	out := make([]PathStep, len(steps))
	for i, s := range steps {
		out[len(steps)-1-i] = PathStep{From: s.To, Predicate: s.Predicate, To: s.From, Reverse: !s.Reverse, Kind: s.Kind,
			Confidence: s.Confidence, Method: s.Method, Via: s.Via}
	}
	return out
}

// packageLevelPath reports whether a tier 2 path rests on package-level
// inference: it crosses a package boundary (imports) or goes up into a package
// and back down into one of its members. Walking from a function to its file and
// package is only membership.
func packageLevelPath(steps []PathStep) bool {
	up := false
	for _, s := range steps {
		switch {
		case s.Kind == "imports":
			return true
		case s.Predicate == "in_package" && !s.Reverse:
			up = true
		case s.Predicate == "in_package" && s.Reverse && up:
			return true
		}
	}
	return false
}

// dependencyKinds are the kinds that take part in dependency flow; a doc or a
// build node is not on any path.
var dependencyKinds = map[string]bool{"route": true, "command": true, "middleware": true, "func": true, "type": true, "template": true, "file": true,
	"package": true, "mcp_tool": true, "config_key": true, "env_var": true, "table": true, "column": true, "index": true}

// Path is graph_path.
func (m *Model) Path(q PathQuery) (*PathResult, error) {
	from, err := m.resolveOne(q.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to, err := m.resolveOne(q.To)
	if err != nil {
		return nil, fmt.Errorf("to: %w", err)
	}
	depth := q.MaxDepth
	switch {
	case depth <= 0:
		depth = defaultPathDepth
	case depth > maxPathDepth:
		depth = maxPathDepth
	}
	res := &PathResult{From: m.Ref(from), To: m.Ref(to), AsOf: m.AsOf()}
	if bad := firstNot(dependencyKinds, from, to); bad != nil {
		res.Note = fmt.Sprintf("%s is a %s, and %s nodes are not part of dependency flow (routes, commands, code, packages, config and tables are); "+
			"use graph_neighbors to see what it is connected to.", bad.ID, bad.Kind, bad.Kind)
		return res, nil
	}
	if from.ID == to.ID {
		res.Found, res.Confidence, res.Depends = true, 100, "from_depends_on_to"
		res.Nodes = []NodeRef{res.From}
		res.Note = "from and to are the same node"
		return res, nil
	}

	// Between code, routes, tools and tables, call edges say everything there is to
	// say: package-level inference would only guess where the call graph already
	// knows.
	calls := m.hasCalls()
	tiers := []bool{false, true}
	if calls && callGraphKinds[from.Kind] && callGraphKinds[to.Kind] {
		tiers = []bool{false}
	}
	for _, tier := range tiers {
		for _, forward := range []bool{true, false} {
			src, dst := from.ID, to.ID
			if !forward {
				src, dst = to.ID, from.ID
			}
			// The surest path first: one whose weakest hop is a stored edge or a plain
			// call, then one that goes through an interface, then anything.
			var steps []PathStep
			var ok bool
			for _, floor := range pathFloors(calls) {
				if steps, ok = m.shortestPath(src, dst, true, tier, depth, floor); ok {
					break
				}
			}
			if !ok {
				continue
			}
			res.Found = true
			res.Inferred = tier && packageLevelPath(steps) // judged as searched, before flipping
			if forward {
				res.Depends = "from_depends_on_to"
			} else {
				res.Depends, steps = "to_depends_on_from", flip(steps)
			}
			m.annotateSteps(steps)
			res.Steps = steps
			res.Confidence = pathConfidence(steps)
			if res.Inferred {
				res.Confidence = 50
			}
			res.Nodes = m.pathNodes(from, steps)
			res.Note = m.pathNote(res, to)
			return res, nil
		}
	}

	// Nothing links them, even at package level.
	res.FromReaches = m.reachSide(from, true)
	res.ToReachedBy = m.reachSide(to, false)
	if len(tiers) == 1 && calls {
		res.Note = fmt.Sprintf("No chain of calls and stored edges links them in either direction within %d hops. "+
			"Apart from calls through function values, reflection and SQL built at run time, neither depends on the other. "+
			"`from_reaches` is what %s depends on by exact edges; `to_reached_by` is what depends on %s by exact edges.", depth, from.ID, to.ID)
		return res, nil
	}
	res.Note = fmt.Sprintf("No chain of dependency edges links them in either direction within %d hops, even through package imports and shared packages. "+
		"Apart from dynamic dispatch and SQL built at run time, neither depends on the other. "+
		"`from_reaches` is what %s depends on by exact edges; `to_reached_by` is what depends on %s by exact edges.", depth, from.ID, to.ID)
	return res, nil
}

// firstNot returns the first node whose kind is not in the set.
func firstNot(set map[string]bool, ns ...*Node) *Node {
	for _, n := range ns {
		if !set[n.Kind] {
			return n
		}
	}
	return nil
}

// callGraphKinds are the kinds the call graph connects exactly.
var callGraphKinds = map[string]bool{"route": true, "command": true, "mcp_tool": true, "func": true, "table": true}

// pathFloors are the confidences a search tries, surest first. Without call edges
// every stored edge is 90 or more except an inferred relation, so one search
// (floor 0) is the old behaviour.
func pathFloors(calls bool) []int {
	if calls {
		return []int{90, 60, 0}
	}
	return []int{0}
}

// stepConfidence is how sure the edge behind a step is. Steps that are only
// membership or imports are not edges and say nothing.
func (m *Model) stepConfidence(st PathStep) (int, string) {
	if st.Kind != "edge" {
		return 0, ""
	}
	from, to := st.From, st.To
	if st.Reverse {
		from, to = st.To, st.From
	}
	return m.edgeConfidence(from, st.Predicate, to)
}

// annotateSteps fills in each edge's confidence and method, and the interface a
// call goes through.
func (m *Model) annotateSteps(steps []PathStep) {
	for i := range steps {
		st := &steps[i]
		st.Confidence, st.Method = m.stepConfidence(*st)
		if st.Predicate == "calls" && st.Method == methodDispatch {
			from, to := st.From, st.To
			if st.Reverse {
				from, to = to, from
			}
			_, _, st.Via = m.callInfo(from, to)
		}
	}
}

// pathConfidence is the weakest edge on the path; a path with no edge (only
// membership) is as sure as it can be.
func pathConfidence(steps []PathStep) int {
	conf := 100
	for _, s := range steps {
		if s.Confidence > 0 && s.Confidence < conf {
			conf = s.Confidence
		}
	}
	return conf
}

func (m *Model) pathNodes(from *Node, steps []PathStep) []NodeRef {
	out := []NodeRef{m.Ref(from)}
	for _, s := range steps {
		out = append(out, m.Ref(m.nodes[s.To]))
	}
	return out
}

// pathNote explains an inferred path, and lists the siblings a final
// package-level hop could equally have been.
func (m *Model) pathNote(res *PathResult, to *Node) string {
	if !res.Inferred {
		return m.callPathNote(res)
	}
	note := "No call edges are indexed (the opt-in `calls` layer is off), so this is the package-level path: it follows imports and code that shares a package, " +
		"and shows that a call is possible, not that one is made. Hops of kind `imports` and `containment` are the package-level ones."
	if n := len(res.Steps); n > 0 {
		last := res.Steps[n-1]
		if (last.Predicate == "reads_table" || last.Predicate == "writes_table") && !last.Reverse {
			if f := m.nodes[last.From]; f != nil {
				if pkg := m.pkgOf(f); pkg != nil {
					var touch []string
					for _, g := range m.codeIn(pkg) {
						if g.Kind != "func" {
							continue
						}
						if contains(g.Out["reads_table"], to.ID) {
							touch = append(touch, g.Str("name")+" (reads)")
						}
						if contains(g.Out["writes_table"], to.ID) {
							touch = append(touch, g.Str("name")+" (writes)")
						}
					}
					if len(touch) > 1 {
						note += fmt.Sprintf(" The last function shown is one of %d in %s that touch %s: %s; which one is called is not indexed.",
							len(touch), pkg.Str("path"), to.ID, strings.Join(touch, ", "))
					}
				}
			}
		}
	}
	return note
}

func contains(list []string, s string) bool {
	i := sort.SearchStrings(list, s)
	return i < len(list) && list[i] == s
}

// reachSide lists what a node reaches (forward) or is reached by (backward)
// through exact dependency edges, nearest first.
func (m *Model) reachSide(n *Node, forward bool) []NodeRef {
	seen := map[string]bool{n.ID: true}
	frontier := []string{n.ID}
	var out []NodeRef
	for len(frontier) > 0 && len(out) < sideLimit {
		var next []string
		for _, id := range frontier {
			for _, st := range m.pathNeighbors(m.nodes[id], forward, false) {
				if seen[st.To] || m.nodes[st.To] == nil {
					continue
				}
				seen[st.To] = true
				next = append(next, st.To)
				if len(out) < sideLimit {
					out = append(out, m.Ref(m.nodes[st.To]))
				}
			}
		}
		frontier = next
	}
	return out
}

// callPathNote explains the hops of an exact path that are not certain: the
// calls that go through an interface, and the edges read from SQL strings.
func (m *Model) callPathNote(res *PathResult) string {
	var dispatch []string
	sql := false
	for _, st := range res.Steps {
		if st.Method == methodDispatch {
			via := ""
			if st.Via != "" {
				via = " through " + m.pathLabel(st.Via)
			}
			from, to := st.From, st.To
			if st.Reverse {
				from, to = to, from
			}
			dispatch = append(dispatch, fmt.Sprintf("%s calls %s%s (confidence %d)", m.pathLabel(from), m.pathLabel(to), via, st.Confidence))
		}
		if st.Method == methodSQL {
			sql = true
		}
	}
	var parts []string
	if len(dispatch) > 0 {
		parts = append(parts, "Not every hop is certain: "+strings.Join(dispatch, "; ")+". A call through an interface is linked to each type that could implement it, so the path may run through a sibling instead.")
	}
	if sql {
		parts = append(parts, "The table hop is read from the SQL strings in the function (confidence 90); SQL built at run time is not seen.")
	}
	return strings.Join(parts, " ")
}
