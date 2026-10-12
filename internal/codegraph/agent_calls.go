package codegraph

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// What the agent tools do with the calls layer. Without it the graph cannot say
// which function calls which, and every answer about reach is estimated from
// package imports (see agent_impact.go). With it, reach is a walk over stored
// `calls` edges, and the same words (confidence, method) say how sure each hop is.

// hasCalls reports whether this graph has call edges: the opt-in calls layer ran.
func (m *Model) hasCalls() bool { return m.layerBuilt(callsLayerName) }

// callInfo is how sure one `calls` edge is, and how it was found. A plain call has
// no annotation and is certain; one through an interface says which interface and
// how many types it could land on.
func (m *Model) callInfo(from, to string) (conf int, method, via string) {
	if a, ok := m.annotations()[from+"\x00calls\x00"+to]; ok && a.conf > 0 {
		return a.conf, a.method, a.via
	}
	return 100, methodEdge, ""
}

// edgeConfidence is how sure a stored edge is, by what it was read from: a call
// by its annotation, an edge read from SQL strings or db tags 90, a relation the
// schema implies by its annotation, any other stored edge 100.
func (m *Model) edgeConfidence(from, pred, to string) (int, string) {
	switch pred {
	case "calls":
		conf, method, _ := m.callInfo(from, to)
		return conf, method
	case "reads_table", "writes_table":
		return 90, methodSQL
	case "maps_to":
		return 90, methodDBTag
	case "implicit_ref":
		a := m.annotationOf(from, pred, to)
		return a.conf, methodInferred
	}
	return 100, methodEdge
}

// callFloors are the confidences a walk over call edges tries, surest first: all
// plain calls, then an interface with one or two candidates, and so on down.
var callFloors = []int{100, 80, 60, 50, 30, 0}

// walked is one function a walk over call edges reached.
type walked struct {
	parent string // the function before it; "" for the start
	conf   int    // the weakest edge on the way from the start
	depth  int
}

// callWalk explores `calls` edges from start, to the callees (forward) or the
// callers (backward), at most maxDepth hops. For each function it reaches it keeps
// the path whose weakest edge is strongest, then the shortest: first only the
// plain calls are followed, then the next-surest, and so on, so a function is
// never reported through a doubtful edge when a certain one gets there.
func (m *Model) callWalk(start string, forward bool, maxDepth int) map[string]*walked {
	out := map[string]*walked{start: {conf: 100}}
	order := []string{start}
	for _, floor := range callFloors {
		queue := append([]string(nil), order...)
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			c := out[cur]
			if c.depth >= maxDepth {
				continue
			}
			n := m.nodes[cur]
			if n == nil {
				continue
			}
			next := n.Out["calls"]
			if !forward {
				next = n.In["calls"]
			}
			for _, id := range next {
				if out[id] != nil {
					continue
				}
				from, to := cur, id
				if !forward {
					from, to = id, cur
				}
				conf, _, _ := m.callInfo(from, to)
				if conf < floor {
					continue
				}
				out[id] = &walked{parent: cur, conf: min(c.conf, conf), depth: c.depth + 1}
				order = append(order, id)
				queue = append(queue, id)
			}
		}
	}
	return out
}

// chain lists the functions from the start of a walk to id, start first.
func chain(walk map[string]*walked, id string) []string {
	var rev []string
	for at := id; at != ""; at = walk[at].parent {
		rev = append(rev, at)
	}
	out := make([]string, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}

// CallRef is one function a function calls, or is called by.
type CallRef struct {
	NodeRef
	Confidence int    `json:"confidence"`
	Method     string `json:"method"`
	Via        string `json:"via,omitempty"` // the interface a call goes through
}

// callRefs lists a function's callees (out) or callers (in), surest first.
func (m *Model) callRefs(n *Node, out bool) []CallRef {
	ids := n.In["calls"]
	if out {
		ids = n.Out["calls"]
	}
	var refs []CallRef
	for _, id := range ids {
		o := m.nodes[id]
		if o == nil {
			continue
		}
		from, to := id, n.ID
		if out {
			from, to = n.ID, id
		}
		conf, method, via := m.callInfo(from, to)
		refs = append(refs, CallRef{NodeRef: m.Ref(o), Confidence: conf, Method: method, Via: via})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Confidence != refs[j].Confidence {
			return refs[i].Confidence > refs[j].Confidence
		}
		return refs[i].ID < refs[j].ID
	})
	if len(refs) > maxListed {
		refs = refs[:maxListed]
	}
	return refs
}

// maxReachDepth bounds how far a briefing follows calls.
const maxReachDepth = 12

// reachByCalls lists the tables the function, and everything it calls, reads or
// writes: the exact version of reachFrom, where the answer is a walk over `calls`
// edges, not a guess from package imports. The start's own tables are the
// briefing's `tables`; what is listed here is what its callees add.
func (m *Model) reachByCalls(start *Node, exact *TableAccess) *Reach {
	tables, functions := m.tablesByCalls(start, false)
	r := &Reach{Method: "calls", Tables: tables, Functions: functions}
	direct := exact != nil && len(exact.Reads)+len(exact.Writes) > 0
	switch {
	case len(r.Tables) > 0:
		r.Note = fmt.Sprintf("Reached through call chains (the calls layer; %d %s in all): `via` is the chain of functions that gets there and `confidence` its weakest hop "+
			"(a call through an interface is 80 or less, an SQL string 90). `access` is the union over every function reached.", r.Functions, plural(r.Functions, "function", "functions"))
	case direct:
		r.Note = fmt.Sprintf("Nothing else is reached: the %d %s it calls (transitively) touch no other table.", r.Functions, plural(r.Functions, "function", "functions"))
	default:
		r.Note = fmt.Sprintf("No table is reached: this code touches none, and neither do the %d %s it calls (transitively). "+
			"Not seen: calls through function values, reflection, and SQL built at run time.", r.Functions, plural(r.Functions, "function", "functions"))
	}
	return r
}

// tablesByCalls walks the calls from start and lists the tables read or written
// by what it reaches (and by start itself when includeStart), surest first, with
// the chain of functions that gets to each and how many functions were reached.
func (m *Model) tablesByCalls(start *Node, includeStart bool) ([]ReachTable, int) {
	walk := m.callWalk(start.ID, true, maxReachDepth)
	ids := make([]string, 0, len(walk))
	for id := range walk {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := walk[ids[i]], walk[ids[j]]
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		return ids[i] < ids[j]
	})

	type found struct {
		access int
		via    []string
		funcs  []string
		conf   int
	}
	tables := map[string]*found{}
	for _, id := range ids {
		if id == start.ID && !includeStart {
			continue
		}
		n := m.nodes[id]
		for _, p := range []struct {
			pred string
			bit  int
		}{{"reads_table", accessRead}, {"writes_table", accessWrite}} {
			for _, t := range n.Out[p.pred] {
				f := tables[t]
				conf := min(walk[id].conf, 90)
				if f == nil {
					f = &found{via: chain(walk, id), conf: conf}
					tables[t] = f
				} else if conf > f.conf {
					f.via, f.conf = chain(walk, id), conf
				}
				f.access |= p.bit
				if len(f.funcs) < 3 && !containsString(f.funcs, id) {
					f.funcs = append(f.funcs, id)
				}
			}
		}
	}

	out := []ReachTable{}
	tids := make([]string, 0, len(tables))
	for id := range tables {
		tids = append(tids, id)
	}
	sort.Strings(tids)
	for _, id := range tids {
		f := tables[id]
		out = append(out, ReachTable{Table: m.Ref(m.nodes[id]), Access: accessString(f.access), Via: f.via, Functions: f.funcs, Confidence: f.conf})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return len(a.Via) < len(b.Via)
	})
	if len(out) > maxReachTables {
		out = out[:maxReachTables]
	}
	return out, len(walk) - 1
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pathLabel names a function in a message: `controllers.ListOrders`.
func (m *Model) pathLabel(id string) string {
	n := m.nodes[id]
	if n == nil {
		return id
	}
	name := n.Str("name")
	if name == "" {
		return id
	}
	if n.Kind == "func" || n.Kind == "type" {
		if p := m.pkgOf(n); p != nil && p.Str("name") != "" {
			return p.Str("name") + "." + name
		}
	}
	return id
}

// historyOf is what the git layer knows about a node's file: a file, doc or spec
// by itself, a function or type by the file that defines it. Nil when the layer is
// off, or the file has no commits in its window.
func (m *Model) historyOf(n *Node) *History {
	if !m.layerBuilt(gitLayerName) {
		return nil
	}
	f := n
	switch n.Kind {
	case "func", "type", "template":
		f = m.nodes[n.first("defined_in")]
	case "file", "doc", "spec":
	default:
		return nil
	}
	if f == nil || f.Int("commits") == 0 {
		return nil
	}
	h := &History{
		LastCommit: f.Str("last_commit"), LastSubject: f.Str("last_subject"),
		Commits: f.Int("commits"), Churn: f.Int("churn"), Authors: f.Int("authors"),
		CoChanged: m.coChanged(f),
	}
	if t := f.Time("last_commit_at"); !t.IsZero() {
		h.LastCommitAt = t.Format(time.RFC3339)
	}
	return h
}

// coChanged lists what changed in the same commits as a node, strongest first.
func (m *Model) coChanged(f *Node) []CoChange {
	var out []CoChange
	for _, id := range f.Out["co_changed_with"] {
		o := m.nodes[id]
		if o == nil {
			continue
		}
		a := m.annotations()[f.ID+"\x00co_changed_with\x00"+id]
		out = append(out, CoChange{NodeRef: m.Ref(o), Percent: a.conf, Together: a.together})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Percent != out[j].Percent {
			return out[i].Percent > out[j].Percent
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// isWiring reports whether a function is in a wiring package (routes, main, cmd):
// the composition roots, which call everything.
func (m *Model) isWiring(n *Node) bool {
	if n.Kind != "func" {
		return false
	}
	p := m.pkgOf(n)
	return p != nil && p.Str("arch_layer") == "wiring"
}

// typeMethods are the methods declared on a type, found by name (`Type.Method`) in
// the type's package.
func (m *Model) typeMethods(t *Node) []*Node {
	pkg := m.pkgOf(t)
	if pkg == nil {
		return nil
	}
	prefix := t.Str("name") + "."
	var out []*Node
	for _, c := range m.codeIn(pkg) {
		if c.Kind == "func" && strings.HasPrefix(c.Str("name"), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// refineAccess sets how each affected function, route and tool touches the
// table(s) of the change: the union over every call path, where the walk that
// found the node knows only the first one. A route that reaches a table by two
// handlers' calls, one reading and one writing, reads and writes it.
func (m *Model) refineAccess(seeds []*Node, affected []*Affected) {
	tables := map[string]bool{}
	for _, s := range seeds {
		switch s.Kind {
		case "table":
			tables[s.ID] = true
		case "column":
			for _, t := range m.in(s, "has_column") {
				if t.Kind == "table" {
					tables[t.ID] = true
				}
			}
		}
	}
	if len(tables) == 0 {
		return
	}
	byID := make(map[string]*Affected, len(affected))
	for _, a := range affected {
		byID[a.ID] = a
	}
	acc := map[string]int{}
	for changed := true; changed; {
		changed = false
		set := func(id string, bits int) {
			if bits != 0 && acc[id]|bits != acc[id] {
				acc[id] |= bits
				changed = true
			}
		}
		for _, a := range affected {
			n := m.nodes[a.ID]
			switch a.Kind {
			case "func":
				for _, t := range n.Out["reads_table"] {
					if tables[t] {
						set(a.ID, accessRead)
					}
				}
				for _, t := range n.Out["writes_table"] {
					if tables[t] {
						set(a.ID, accessWrite)
					}
				}
				for _, c := range n.Out["calls"] {
					if byID[c] != nil {
						set(a.ID, acc[c])
					}
				}
			case "route":
				if h := n.first("handled_by"); h != "" && byID[h] != nil {
					set(a.ID, acc[h])
				}
			case "mcp_tool":
				for _, f := range n.In["registers"] {
					if byID[f] != nil {
						set(a.ID, acc[f])
					}
				}
			}
		}
	}
	for _, a := range affected {
		switch a.Kind {
		case "func", "route", "mcp_tool":
			if bits, ok := acc[a.ID]; ok {
				a.Access = accessString(bits)
			}
		}
	}
}

// maxChain bounds the chain shown for an entry point.
const maxChain = 12

// chainEntryPoints gives each route and tool the chain of nodes that connects it
// to the change, entry point first: how the change reaches it, in nodes the agent
// can open.
func (m *Model) chainEntryPoints(affected []*Affected, seen map[string]*Affected, isSeed map[string]bool) {
	for _, a := range affected {
		if (a.Kind != "route" && a.Kind != "mcp_tool") || a.Through != nil {
			continue
		}
		path := []string{a.ID}
		for at := a.From; at != "" && len(path) < maxChain; {
			path = append(path, at)
			if isSeed[at] || seen[at] == nil {
				break
			}
			at = seen[at].From
		}
		a.Through = path
	}
}

// coChangedWith lists what usually changes with the files that hold the changed
// code (the git layer), strongest first and without the files themselves.
func (m *Model) coChangedWith(seeds []*Node) []CoChange {
	if !m.layerBuilt(gitLayerName) {
		return nil
	}
	own := map[string]bool{}
	var files []*Node
	for _, s := range seeds {
		f := s
		if s.Kind == "func" || s.Kind == "type" || s.Kind == "template" {
			f = m.nodes[s.first("defined_in")]
		}
		if f == nil || (f.Kind != "file" && f.Kind != "doc" && f.Kind != "spec") || own[f.ID] {
			continue
		}
		own[f.ID] = true
		files = append(files, f)
	}
	best := map[string]CoChange{}
	for _, f := range files {
		for _, c := range m.coChanged(f) {
			if own[c.ID] {
				continue
			}
			if prev, ok := best[c.ID]; !ok || c.Percent > prev.Percent {
				best[c.ID] = c
			}
		}
	}
	out := make([]CoChange, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Percent != out[j].Percent {
			return out[i].Percent > out[j].Percent
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxCoChanged {
		out = out[:maxCoChanged]
	}
	return out
}

const maxCoChanged = 15
