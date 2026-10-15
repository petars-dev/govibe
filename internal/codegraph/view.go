package codegraph

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
)

// The visualizer's data. The graph page does not load every quad and lay them
// all out (that stopped being readable at about a thousand): it asks for a view,
// a bounded subgraph the server has already chosen and ordered, and draws that.
// A view is either a preset (architecture, request flow, the database's ER
// diagram, docs against code, config against its readers, the findings) or the
// neighbourhood of one node, which is also what expanding a node asks for.
// Whatever the graph's size, a view holds at most Limit nodes and says when it
// had to leave some out.

// PresetInfo names one preset view.
type PresetInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// ViewPresets are the views the graph page offers, in the order it lists them.
// "manual" (quads added by hand) is not drawn from the model: see ManualView.
var ViewPresets = []PresetInfo{
	{"app_map", "App map", "The app's own connections: each route and each command (a scheduled one shows its schedule), the function that serves or runs it, the actions, services and stores it reaches, the tables at the end, and the templates, config keys and docs on the way. Estimated from package imports where the calls layer is off (dashed). In scope Framework or All it maps the framework's routes too, each labelled."},
	{"architecture", "Architecture", "Packages in their architecture layers, and who imports whom. A red edge is an import that breaks the layering rule."},
	{"request_flow", "Request flow", "Each route, the middleware it runs, the function that handles it, and what that renders. With the calls layer, also the calls down to the tables it reads and writes (dashed: a call through an interface)."},
	{"db_er", "Database ER", "Tables and the relations between them: declared foreign keys (solid), and relations the schema implies (dashed)."},
	{"docs_coverage", "Docs ↔ code", "Which docs and specs mention which packages, routes, commands, tables and tools. An orange outline means no doc mentions it."},
	{"config_usage", "Config → usage", "Config keys, the environment variables behind them, and the packages that read them."},
	{"findings", "Findings", "Every finding and the node it is about, worst first."},
	{"manual", "Hand-added quads", "Quads added with the form below or the HTTP API: not part of the indexed graph."},
}

// ViewQuery asks for one view.
type ViewQuery struct {
	Preset string   // one of ViewPresets; "" is the architecture
	Focus  string   // a node (any reference graph_context takes): its neighbourhood instead of a preset
	Depth  int      // focus only: hops from the node (default 1, at most 3)
	Kinds  []string // keep only these kinds of node
	Limit  int      // most nodes to return (default 300, at most 1500)
}

const (
	defaultViewLimit = 300
	maxViewLimit     = 1500
	maxViewDepth     = 3
	focusFanOut      = 15 // edges followed per node, predicate and direction in a focus view
)

// ViewNode is one node of a view.
type ViewNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	Group    string `json:"group,omitempty"` // what to colour by within a kind: a package's architecture layer, a finding's severity
	Col      int    `json:"col"`             // column in a layered layout (see ViewResult.Columns)
	Dist     int    `json:"dist,omitempty"`  // focus view: hops from the focus node
	Degree   int    `json:"degree"`          // edges to other nodes of the view
	Findings int    `json:"findings,omitempty"`
	Worst    string `json:"worst,omitempty"`  // severity of the worst finding about it
	Flag     string `json:"flag,omitempty"`   // uncovered, sensitive, undeclared, dev, scheduled
	More     int    `json:"more,omitempty"`   // focus view: edges at this node that the view leaves out
	Origin   string `json:"origin,omitempty"` // app or framework: whose node it is, so a view of both can label the framework's
}

// ViewEdge is one stored edge between two nodes of a view.
type ViewEdge struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Predicate  string `json:"predicate"`
	Violation  bool   `json:"violation,omitempty"`  // an import against the layering rule
	Inferred   bool   `json:"inferred,omitempty"`   // not declared: inferred by the indexer
	Confidence int    `json:"confidence,omitempty"` // of an inferred edge
}

// KindCount is how many nodes of a kind a view holds.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// ViewResult is a drawable, bounded subgraph.
type ViewResult struct {
	Preset      string       `json:"preset"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Focus       *NodeRef     `json:"focus,omitempty"`
	Columns     []string     `json:"columns,omitempty"` // when set, the view is laid out in these columns left to right
	Nodes       []ViewNode   `json:"nodes"`
	Edges       []ViewEdge   `json:"edges"`
	Kinds       []KindCount  `json:"kinds"`
	Total       int          `json:"total"` // nodes the view holds before the cap
	Limit       int          `json:"limit"`
	Truncated   bool         `json:"truncated,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	Presets     []PresetInfo `json:"presets"`
	AsOf        string       `json:"as_of"`
	Quads       int          `json:"quads"` // size of the whole graph
	// Scope is whose nodes the view is drawn from, and Hidden what that leaves out:
	// the page's banner always says both.
	Scope  Scope  `json:"scope"`
	Hidden Hidden `json:"hidden"`
}

// View builds a preset view, or the neighbourhood of a node.
func (m *Model) View(q ViewQuery) (*ViewResult, error) {
	b := &viewBuilder{m: m, limit: clampLimit(q.Limit, defaultViewLimit, maxViewLimit), idx: map[string]*ViewNode{}, seenEdge: map[string]bool{}}
	res := &ViewResult{Presets: ViewPresets, AsOf: m.AsOf(), Quads: m.quads, Limit: b.limit, Scope: m.Scope(), Hidden: m.Hidden()}
	if m.Empty() {
		res.Preset, res.Title = q.Preset, "Nothing indexed yet"
		res.Notes = []string{"The graph has not been indexed yet: reindex, or retry in a few seconds after a boot."}
		res.Nodes, res.Edges, res.Kinds = []ViewNode{}, []ViewEdge{}, []KindCount{}
		return res, nil
	}
	kinds, err := m.kindSet(q.Kinds)
	if err != nil {
		return nil, err
	}

	if q.Focus != "" {
		start, err := m.resolveOne(q.Focus)
		if err != nil {
			return nil, err
		}
		depth := q.Depth
		switch {
		case depth <= 0:
			depth = 1
		case depth > maxViewDepth:
			depth = maxViewDepth
		}
		ref := m.Ref(start)
		res.Focus = &ref
		res.Preset, res.Title = "focus", "Around "+viewLabel(m, start)
		res.Description = fmt.Sprintf("%s and what is connected to it, up to %d %s away.", viewLabel(m, start), depth, plural(depth, "hop", "hops"))
		b.focus(start, depth)
	} else {
		id := q.Preset
		if id == "" {
			id = "app_map" // the app's own connections come first
		}
		var info *PresetInfo
		for i := range ViewPresets {
			if ViewPresets[i].ID == id {
				info = &ViewPresets[i]
			}
		}
		if info == nil || id == "manual" {
			ids := make([]string, 0, len(ViewPresets))
			for _, p := range ViewPresets {
				if p.ID != "manual" {
					ids = append(ids, p.ID)
				}
			}
			return nil, fmt.Errorf("unknown preset %q; presets: %s, or focus on a node", q.Preset, strings.Join(ids, ", "))
		}
		res.Preset, res.Title, res.Description = info.ID, info.Title, info.Description
		switch id {
		case "app_map":
			b.appMap()
		case "architecture":
			b.architecture()
		case "request_flow":
			b.requestFlow()
		case "db_er":
			b.dbER()
		case "docs_coverage":
			b.docsCoverage()
		case "config_usage":
			b.configUsage()
		case "findings":
			b.findings()
		}
	}
	b.finish(res, kinds)
	return res, nil
}

// viewBuilder collects the candidate nodes of a view in priority order, and the
// edges between them; finish cuts the list at the limit.
type viewBuilder struct {
	m        *Model
	limit    int
	nodes    []*ViewNode
	idx      map[string]*ViewNode
	edges    []ViewEdge
	seenEdge map[string]bool
	cols     []string
	notes    []string
}

// add puts a node in the view (once) and returns it.
func (b *viewBuilder) add(n *Node, col int) *ViewNode {
	if vn := b.idx[n.ID]; vn != nil {
		return vn
	}
	vn := &ViewNode{ID: n.ID, Kind: n.Kind, Label: viewLabel(b.m, n), Col: col, Origin: b.m.originOf(n)}
	switch n.Kind {
	case "package":
		vn.Group = n.Str("arch_layer")
	case "table", "column", "index":
		vn.Group = n.Str("source")
	case "route":
		if n.Bool("dev_only") {
			vn.Flag = "dev"
		}
	case "command":
		if n.Bool("scheduled") {
			vn.Flag = "scheduled"
		}
	case "config_key", "env_var":
		switch {
		case n.Bool("sensitive"):
			vn.Flag = "sensitive"
		case declaredFalse(n):
			vn.Flag = "undeclared"
		}
	case "finding":
		vn.Group = n.Str("severity")
	}
	b.idx[n.ID] = vn
	b.nodes = append(b.nodes, vn)
	return vn
}

// link adds an edge once.
func (b *viewBuilder) link(from, to *Node, pred string, set ...func(*ViewEdge)) {
	key := from.ID + "\x00" + pred + "\x00" + to.ID
	if b.seenEdge[key] {
		return
	}
	b.seenEdge[key] = true
	e := ViewEdge{Source: from.ID, Target: to.ID, Predicate: pred}
	for _, s := range set {
		s(&e)
	}
	b.edges = append(b.edges, e)
}

func inferred(conf int) func(*ViewEdge) {
	return func(e *ViewEdge) { e.Inferred, e.Confidence = true, conf }
}

// finish applies the kind filter and the cap, drops edges whose ends are gone,
// and fills in what depends on the final node set.
func (b *viewBuilder) finish(res *ViewResult, kinds map[string]bool) {
	nodes := b.nodes
	if kinds != nil {
		nodes = nodes[:0:0]
		for _, n := range b.nodes {
			if kinds[n.Kind] || (res.Focus != nil && n.ID == res.Focus.ID) {
				nodes = append(nodes, n)
			}
		}
	}
	res.Total = len(nodes)
	if len(nodes) > b.limit {
		nodes = nodes[:b.limit]
		res.Truncated = true
		b.notes = append(b.notes, fmt.Sprintf("Showing %d of %d nodes. Narrow the view with the kind filter or a search, or expand a node.", b.limit, res.Total))
	}
	keep := make(map[string]*ViewNode, len(nodes))
	for _, n := range nodes {
		keep[n.ID] = n
	}
	res.Edges = []ViewEdge{}
	for _, e := range b.edges {
		if keep[e.Source] == nil || keep[e.Target] == nil {
			continue
		}
		if len(res.Edges) >= 4*b.limit {
			b.notes = append(b.notes, fmt.Sprintf("More than %d edges: the rest are not drawn.", 4*b.limit))
			break
		}
		res.Edges = append(res.Edges, e)
		keep[e.Source].Degree++
		keep[e.Target].Degree++
	}

	counts := map[string]int{}
	res.Nodes = make([]ViewNode, 0, len(nodes))
	for _, n := range nodes {
		if fs := b.m.findingNodesAbout(n.ID); len(fs) > 0 {
			n.Findings, n.Worst = len(fs), worstSeverity(fs)
		}
		counts[n.Kind]++
		res.Nodes = append(res.Nodes, *n)
	}
	res.Kinds = make([]KindCount, 0, len(counts))
	for k, c := range counts {
		res.Kinds = append(res.Kinds, KindCount{Kind: k, Count: c})
	}
	sort.Slice(res.Kinds, func(i, j int) bool {
		if a, c := rankOf(res.Kinds[i].Kind), rankOf(res.Kinds[j].Kind); a != c {
			return a < c
		}
		return res.Kinds[i].Kind < res.Kinds[j].Kind
	})
	res.Columns = b.cols
	res.Notes = append(res.Notes, b.notes...)
	if len(res.Nodes) == 0 && len(res.Notes) == 0 {
		res.Notes = append(res.Notes, "Nothing to show here: the layers this view reads are empty or not built.")
	}
}

// viewLabel is what a node is called on the canvas.
func viewLabel(m *Model, n *Node) string {
	switch n.Kind {
	case "package":
		if p := n.Str("path"); p != "." {
			return p
		}
		return "(root)"
	case "func", "type":
		name := n.Str("name")
		if p := m.pkgOf(n); p != nil && p.Str("name") != "" {
			return p.Str("name") + "." + name
		}
		return name
	case "route":
		return n.Str("method") + " " + n.Str("path")
	case "command":
		// The schedule is what makes a command worth a place on the map: it runs unwatched. One
		// line, because the canvas ellipsises a label and does not break it (hovering shows it whole).
		if s := n.Str("schedule"); s != "" {
			return n.Str("name") + " · " + s
		}
		return n.Str("name")
	case "file", "doc", "spec":
		if p := n.Str("path"); p != "" {
			return p
		}
	case "column", "index":
		return strings.TrimPrefix(strings.TrimPrefix(n.ID, "col:"), "idx:")
	case "finding":
		if r := n.Str("rule"); r != "" {
			return r
		}
	case "middleware":
		if n.Str("name") != "" {
			return n.Str("name")
		}
	}
	if s := n.Str("name"); s != "" {
		return s
	}
	return n.ID
}

// --- findings per node ---

// findingNodesAbout lists the finding nodes whose `about` is the node.
func (m *Model) findingNodesAbout(id string) []*Node {
	if m.findIdx == nil {
		m.findIdx = map[string][]*Node{}
		for _, f := range m.byKind["finding"] {
			for _, t := range f.Out["about"] {
				m.findIdx[t] = append(m.findIdx[t], f)
			}
		}
	}
	return m.findIdx[id]
}

// worstSeverity is the severity of the most severe finding, normalised to error,
// warning or info.
func worstSeverity(fs []*Node) string {
	best := 3
	for _, f := range fs {
		if r := severityRank(f.Str("severity")); r < best {
			best = r
		}
	}
	return [...]string{sevError, sevWarning, sevInfo, ""}[best]
}

// --- presets ---

// archOrder is the order of the architecture layers, left to right, in the
// architecture view: from the entry points down to the storage they must go
// through business to reach.
var archOrder = []string{"wiring", "dev", "controllers", "commands", "middleware", "views", "business", "storage", "support"}

func (b *viewBuilder) architecture() {
	m := b.m
	pkgs := append([]*Node(nil), m.byKind["package"]...)
	if len(pkgs) == 0 {
		b.notes = append(b.notes, "No packages: the code layer is not built.")
		return
	}
	degree := map[string]int{}
	for _, p := range pkgs {
		degree[p.ID] += len(p.Out["imports"]) + len(p.In["imports"])
	}
	col := map[string]int{}
	present := map[string]bool{}
	for _, p := range pkgs {
		present[p.Str("arch_layer")] = true
	}
	for _, al := range archOrder {
		if present[al] {
			col[al] = len(b.cols)
			b.cols = append(b.cols, al)
		}
	}
	sort.SliceStable(pkgs, func(i, j int) bool {
		if len(pkgs) > b.limit && degree[pkgs[i].ID] != degree[pkgs[j].ID] {
			return degree[pkgs[i].ID] > degree[pkgs[j].ID]
		}
		return false
	})
	for _, p := range pkgs {
		b.add(p, col[p.Str("arch_layer")])
	}
	for _, p := range pkgs {
		from := p.Str("arch_layer")
		for _, imp := range m.out(p, "imports") {
			v := layerVerdict(from, imp.Str("arch_layer")) != ""
			b.link(p, imp, "imports", func(e *ViewEdge) { e.Violation = v })
		}
	}
}

// appMap draws the project's own connections: a route, the handler that serves it, what
// the handler reaches on the way to the data (functions through the calls layer, else the
// business and storage packages its package imports, dashed), the tables at the end, and
// the templates it renders, the settings it reads and the docs that mention the route.
func (b *viewBuilder) appMap() {
	m := b.m
	routes := append([]*Node(nil), m.byKind["route"]...)
	commands := append([]*Node(nil), m.byKind["command"]...)
	if len(routes) == 0 && len(commands) == 0 {
		b.notes = append(b.notes, "No routes or commands: the routes and commands layers are not built (an offline run of a project that is not an application has no router), or the scope shows none.")
		return
	}
	sort.SliceStable(commands, func(i, j int) bool {
		a, c := commands[i], commands[j]
		if a.Bool("scheduled") != c.Bool("scheduled") {
			return a.Bool("scheduled")
		}
		return a.Str("name") < c.Str("name")
	})
	sort.SliceStable(routes, func(i, j int) bool {
		a, c := routes[i], routes[j]
		if (m.originOf(a) == OriginFramework) != (m.originOf(c) == OriginFramework) {
			return m.originOf(c) == OriginFramework // the project's own first
		}
		if a.Str("path") != c.Str("path") {
			return a.Str("path") < c.Str("path")
		}
		return a.ID < c.ID
	})
	calls := m.hasCalls()
	b.cols = []string{"route, command", "handler", "actions, services, stores", "tables", "templates, settings, docs"}
	const (
		colRoute, colHandler, colReach, colTables, colAside = 0, 1, 2, 3, 4
	)
	// entry adds an entry point (a route, or a command) with the docs that mention it, the
	// function that serves or runs it, and what that function reaches.
	entry := func(r *Node, handledBy string) {
		b.add(r, colRoute)
		for _, d := range append(m.in(r, "mentions"), m.in(r, "covers")...) {
			if d.Kind == "doc" || d.Kind == "spec" || d.Kind == "spec_item" {
				b.add(d, colAside)
				b.link(d, r, "mentions")
			}
		}
		h := m.nodes[r.first(handledBy)]
		if h == nil {
			return
		}
		b.add(h, colHandler)
		b.link(r, h, handledBy)
		for _, t := range m.out(h, "renders") {
			b.add(t, colAside)
			b.link(h, t, "renders")
		}
		for _, k := range m.out(h, "uses_config") {
			b.add(k, colAside)
			b.link(h, k, "uses_config")
		}
		if calls {
			b.reachByCalls(h, colReach, colTables)
		} else if p := m.pkgOf(h); p != nil {
			b.reachByImports(p, h, colReach, colTables)
		}
	}
	// What runs with nobody watching comes first, so a view cut at its node limit (which keeps
	// nodes in the order they were added) drops routes before it drops a scheduled command.
	// Every entry point is placed before any of them is expanded, so one command's reach
	// cannot crowd the next command out.
	type ep struct {
		n         *Node
		handledBy string
	}
	var order []ep
	for _, c := range commands {
		if c.Bool("scheduled") {
			order = append(order, ep{c, "run_by"})
		}
	}
	for _, r := range routes {
		order = append(order, ep{r, "handled_by"})
	}
	for _, c := range commands {
		if !c.Bool("scheduled") {
			order = append(order, ep{c, "run_by"})
		}
	}
	for _, e := range order {
		b.add(e.n, colRoute)
	}
	for _, e := range order {
		entry(e.n, e.handledBy)
	}
}

// reachByCalls adds the functions in the business and storage layers a handler calls, and
// the tables they read or write, following stored calls only down to a bounded depth.
func (b *viewBuilder) reachByCalls(h *Node, colReach, colTables int) {
	m := b.m
	walk := m.callWalk(h.ID, true, 4)
	ids := make([]string, 0, len(walk))
	for id := range walk {
		if id != h.ID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if walk[ids[i]].depth != walk[ids[j]].depth {
			return walk[ids[i]].depth < walk[ids[j]].depth
		}
		return ids[i] < ids[j]
	})
	inData := func(n *Node) bool {
		p := m.pkgOf(n)
		return p != nil && (p.Str("arch_layer") == "business" || p.Str("arch_layer") == "storage")
	}
	for _, id := range ids {
		f := m.nodes[id]
		if !inData(f) {
			continue
		}
		path := chain(walk, id)
		for i := 1; i < len(path); i++ {
			from, to := m.nodes[path[i-1]], m.nodes[path[i]]
			if inData(to) {
				b.add(to, colReach)
				b.link(from, to, "calls", b.edgeFlags(from, to, "calls"))
			}
		}
		for _, t := range m.out(f, "reads_table") {
			b.add(t, colTables)
			b.link(f, t, "reads_table")
		}
		for _, t := range m.out(f, "writes_table") {
			b.add(t, colTables)
			b.link(f, t, "writes_table")
		}
	}
}

// reachByImports is reachByCalls without call edges: the business and storage packages
// the handler's package imports, directly or through each other, and the tables the
// functions of those packages touch. Every link is an estimate, drawn dashed.
func (b *viewBuilder) reachByImports(from, handler *Node, colReach, colTables int) {
	m := b.m
	seen := map[string]bool{from.ID: true}
	frontier := []*Node{from}
	for depth := 0; depth < 3 && len(frontier) > 0; depth++ {
		var next []*Node
		for _, p := range frontier {
			for _, imp := range m.out(p, "imports") {
				layer := imp.Str("arch_layer")
				if seen[imp.ID] || (layer != "business" && layer != "storage") {
					continue
				}
				seen[imp.ID] = true
				b.add(imp, colReach)
				if p == from {
					b.link(handler, imp, "imports", inferred(50))
				} else {
					b.link(p, imp, "imports", inferred(50))
				}
				next = append(next, imp)
				if layer == "storage" {
					for _, f := range m.codeIn(imp) {
						for _, t := range m.out(f, "reads_table") {
							b.add(t, colTables)
							b.link(imp, t, "reads_table", inferred(50))
						}
						for _, t := range m.out(f, "writes_table") {
							b.add(t, colTables)
							b.link(imp, t, "writes_table", inferred(50))
						}
					}
				}
			}
		}
		frontier = next
	}
}

func (b *viewBuilder) requestFlow() {
	m := b.m
	routes := append([]*Node(nil), m.byKind["route"]...)
	if len(routes) == 0 {
		b.notes = append(b.notes, "No routes: the routes layer is not built (an offline run has no router).")
		return
	}
	sort.SliceStable(routes, func(i, j int) bool {
		a, c := routes[i], routes[j]
		if a.Bool("dev_only") != c.Bool("dev_only") {
			return !a.Bool("dev_only")
		}
		if a.Str("path") != c.Str("path") {
			return a.Str("path") < c.Str("path")
		}
		return a.ID < c.ID
	})
	b.cols = []string{"route", "middleware", "handler", "renders / registers"}
	last := 3 // the column of what a handler leads to
	calls := m.hasCalls()
	if calls {
		b.cols = []string{"route", "middleware", "handler", "calls", "tables, renders, tools"}
		last = 4
	}
	for _, r := range routes {
		b.add(r, 0)
		if h := m.nodes[r.first("handled_by")]; h != nil {
			b.add(h, 2)
			b.link(r, h, "handled_by")
			for _, t := range m.out(h, "renders") {
				b.add(t, last)
				b.link(h, t, "renders")
			}
			for _, t := range m.out(h, "registers") {
				b.add(t, last)
				b.link(h, t, "registers")
			}
			if calls {
				b.callChains(h, last)
			}
		}
		for _, mw := range m.out(r, "uses_mw") {
			b.add(mw, 1)
			b.link(r, mw, "uses_mw")
			if impl := m.nodes[mw.first("implemented_by")]; impl != nil {
				b.add(impl, 2)
				b.link(mw, impl, "implemented_by")
			}
		}
	}
}

// callChains adds what a handler calls on the way to a table: each function
// that reads or writes one, the functions between it and the handler, and the
// tables. Helpers that touch no table (a JSON writer) are left out, so the chain
// is the request's way to its data and not the handler's whole call tree.
func (b *viewBuilder) callChains(h *Node, last int) {
	m := b.m
	walk := m.callWalk(h.ID, true, 4)
	ids := make([]string, 0, len(walk))
	for id := range walk {
		if id != h.ID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if walk[ids[i]].depth != walk[ids[j]].depth {
			return walk[ids[i]].depth < walk[ids[j]].depth
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		f := m.nodes[id]
		reads, writes := m.out(f, "reads_table"), m.out(f, "writes_table")
		if len(reads)+len(writes) == 0 {
			continue
		}
		path := chain(walk, id)
		for i := 1; i < len(path); i++ {
			from, to := m.nodes[path[i-1]], m.nodes[path[i]]
			b.add(to, 3)
			b.link(from, to, "calls", b.edgeFlags(from, to, "calls"))
		}
		for _, t := range reads {
			b.add(t, last)
			b.link(f, t, "reads_table")
		}
		for _, t := range writes {
			b.add(t, last)
			b.link(f, t, "writes_table")
		}
	}
}

func (b *viewBuilder) dbER() {
	m := b.m
	tables := m.byKind["table"]
	if len(tables) == 0 {
		b.notes = append(b.notes, "No tables: the migrations create none yet, and no live database schema was read.")
		return
	}
	for _, t := range tables {
		b.add(t, 0)
	}
	for _, t := range tables {
		for _, o := range m.out(t, "fk_to") {
			if o.Kind == "table" {
				b.link(t, o, "fk_to")
			}
		}
		for _, o := range m.out(t, "implicit_ref") {
			if o.Kind == "table" {
				conf := m.annotationOf(t.ID, "implicit_ref", o.ID).conf
				b.link(t, o, "implicit_ref", inferred(conf))
			}
		}
		for _, o := range m.out(t, "many_to_many") {
			if o.Kind == "table" && t.ID < o.ID { // stored in both directions
				conf := m.annotationOf(t.ID, "many_to_many", o.ID).conf
				b.link(t, o, "many_to_many", inferred(conf))
			}
		}
	}
}

// coverTarget maps something a doc mentions to the thing coverage is judged on:
// a package (for a file, function, type or template of it), a route, a table
// (for a column or index), or a tool. Anything else is not judged.
func (m *Model) coverTarget(n *Node) *Node {
	switch n.Kind {
	case "package", "route", "command", "table", "mcp_tool":
		return n
	case "file", "func", "type":
		return m.pkgOf(n)
	case "column", "index":
		return m.tableNodeOf(n)
	case "template":
		if f := m.nodes[n.first("defined_in")]; f != nil {
			return m.pkgOf(f)
		}
	}
	return nil
}

func (b *viewBuilder) docsCoverage() {
	m := b.m
	var docs []*Node
	for _, k := range []string{"doc", "spec"} {
		docs = append(docs, m.byKind[k]...)
	}
	if len(docs) == 0 {
		b.notes = append(b.notes, "No docs: the docs layer is not built.")
		return
	}
	// what each doc covers, with specs also covering what their rows name
	type pair struct{ doc, target *Node }
	var pairs []pair
	covered := map[string]bool{}
	for _, d := range docs {
		seen := map[string]bool{}
		note := func(t *Node) {
			if c := m.coverTarget(t); c != nil && !seen[c.ID] {
				seen[c.ID] = true
				covered[c.ID] = true
				pairs = append(pairs, pair{d, c})
			}
		}
		for _, t := range m.out(d, "mentions") {
			note(t)
		}
		for _, it := range m.out(d, "has_item") {
			for _, t := range m.out(it, "covers") {
				note(t)
			}
		}
	}
	b.cols = []string{"docs and specs", "code, routes, tables, tools"}
	for _, d := range docs {
		b.add(d, 0)
	}
	// What nothing mentions comes first: it is what the view is for.
	var judged []*Node
	for _, k := range []string{"package", "route", "command", "table", "mcp_tool"} {
		for _, n := range m.byKind[k] {
			if n.Kind == "route" && n.Bool("dev_only") {
				continue // dev tooling is documented as a whole, not route by route
			}
			judged = append(judged, n)
		}
	}
	for _, n := range judged {
		if !covered[n.ID] {
			b.add(n, 1).Flag = "uncovered"
		}
	}
	for _, n := range judged {
		if covered[n.ID] {
			b.add(n, 1)
		}
	}
	for _, p := range pairs {
		b.link(p.doc, p.target, "mentions")
	}
}

func (b *viewBuilder) configUsage() {
	m := b.m
	keys := append([]*Node(nil), m.byKind["config_key"]...)
	if len(keys) == 0 {
		b.notes = append(b.notes, "No config keys: the config layer is not built.")
		return
	}
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i].In["uses_config"]) > len(keys[j].In["uses_config"]) })
	b.cols = []string{"read by (package)", "config key", "environment variable"}
	owner := func(n *Node) *Node {
		if n.Kind == "package" {
			return n
		}
		return m.pkgOf(n)
	}
	for _, k := range keys {
		b.add(k, 1)
		for _, e := range m.out(k, "reads_env") {
			b.add(e, 2)
			b.link(k, e, "reads_env")
		}
		for _, u := range m.in(k, "uses_config") {
			if p := owner(u); p != nil {
				b.add(p, 0)
				b.link(p, k, "uses_config")
			}
		}
	}
	for _, e := range m.byKind["env_var"] {
		for _, f := range m.in(e, "reads_env") {
			if f.Kind != "func" {
				continue
			}
			if p := owner(f); p != nil {
				b.add(e, 2)
				b.add(p, 0)
				b.link(p, e, "reads_env")
			}
		}
	}
}

func (b *viewBuilder) findings() {
	m := b.m
	fs := append([]*Node(nil), m.byKind["finding"]...)
	if !m.layerBuilt("findings") {
		b.notes = append(b.notes, "The findings layer is not built, so nothing has been checked.")
		return
	}
	sort.SliceStable(fs, func(i, j int) bool {
		a, c := severityRank(fs[i].Str("severity")), severityRank(fs[j].Str("severity"))
		if a != c {
			return a < c
		}
		if fs[i].Str("rule") != fs[j].Str("rule") {
			return fs[i].Str("rule") < fs[j].Str("rule")
		}
		return fs[i].ID < fs[j].ID
	})
	if len(fs) == 0 {
		b.notes = append(b.notes, "No findings: every rule that could run found nothing.")
		return
	}
	b.cols = []string{"finding", "about"}
	for _, f := range fs {
		b.add(f, 0)
		for _, s := range m.out(f, "about") {
			b.add(s, 1)
			b.link(f, s, "about")
		}
	}
}

// --- focus ---

// bookkeeping kinds: the "how sure" of an inferred edge, the build and its layer
// runs. They show only when the node asked about is one of them.
func bookkeeping(kind string) bool { return kind == "annotation" || kind == "layer" || kind == "build" }

// focus walks outward from a node, a few edges per node and predicate, so a hub
// (a package with fifty files) stays readable; More says what was left out.
func (b *viewBuilder) focus(start *Node, depth int) {
	m := b.m
	showBook := bookkeeping(start.Kind)
	dist := map[string]int{start.ID: 0}
	vs := b.add(start, 0)
	frontier := []*Node{start}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []*Node
		for _, n := range frontier {
			from := b.idx[n.ID]
			taken := map[string]int{}
			for _, a := range adjacency(n) {
				o := m.nodes[a.other]
				if o == nil || (!showBook && bookkeeping(o.Kind)) {
					continue
				}
				_, known := dist[o.ID]
				key := a.pred
				if a.out {
					key += ">"
				}
				if !known && taken[key] >= focusFanOut {
					from.More++
					continue
				}
				if !known {
					taken[key]++
					dist[o.ID] = d
					vn := b.add(o, 0)
					vn.Dist = d
					next = append(next, o)
				}
				if a.out {
					b.link(n, o, a.pred, b.edgeFlags(n, o, a.pred))
				} else {
					b.link(o, n, a.pred, b.edgeFlags(o, n, a.pred))
				}
			}
		}
		frontier = next
	}
	vs.Dist = 0
}

// edgeFlags marks the edges the drawing treats differently.
func (b *viewBuilder) edgeFlags(from, to *Node, pred string) func(*ViewEdge) {
	return func(e *ViewEdge) {
		switch pred {
		case "imports":
			e.Violation = layerVerdict(from.Str("arch_layer"), to.Str("arch_layer")) != ""
		case "implicit_ref", "many_to_many":
			e.Inferred, e.Confidence = true, b.m.annotationOf(from.ID, pred, to.ID).conf
		case "calls":
			if conf, _, _ := b.m.callInfo(from.ID, to.ID); conf < 100 {
				e.Inferred, e.Confidence = true, conf
			}
		case "co_changed_with":
			e.Inferred, e.Confidence = true, b.m.annotations()[from.ID+"\x00co_changed_with\x00"+to.ID].conf
		}
	}
}

// --- hand-added quads ---

// ManualView draws the quads that are not part of the indexed graph: the ones
// added with the form on the graph page or through the HTTP API, which carry no
// `layer:*` label. They are not in the model, so they are read from the store.
func ManualView(ctx context.Context, qs graph.QuadStore, limit int) (*ViewResult, error) {
	limit = clampLimit(limit, defaultViewLimit, maxViewLimit)
	info := ViewPresets[len(ViewPresets)-1]
	res := &ViewResult{Preset: info.ID, Title: info.Title, Description: info.Description, Presets: ViewPresets, Limit: limit}
	b := &viewBuilder{m: newModel(), limit: limit, idx: map[string]*ViewNode{}, seenEdge: map[string]bool{}}
	it := qs.QuadsAllIterator()
	defer it.Close()
	ids := map[string]*Node{}
	node := func(v quad.Value) *Node {
		id := termText(v)
		if n := ids[id]; n != nil {
			return n
		}
		n := &Node{ID: id, Kind: "term", Attrs: map[string][]any{"name": {id}}}
		ids[id] = n
		return n
	}
	total := 0
	for it.Next(ctx) {
		q := qs.Quad(it.Result())
		if !q.IsValid() {
			continue
		}
		if l, ok := q.Label.(quad.IRI); ok && strings.HasPrefix(string(l), labelPrefix) {
			continue // indexed
		}
		total++
		if total > limit {
			res.Truncated = true
			continue
		}
		s, o := node(q.Subject), node(q.Object)
		b.add(s, 0)
		b.add(o, 0)
		b.link(s, o, termText(q.Predicate))
	}
	if err := it.Err(); err != nil {
		return nil, err
	}
	res.Total = total
	b.limit = 2 * limit // the limit counts quads; each brings at most two nodes
	b.finish(res, nil)
	res.Total, res.Truncated = total, total > limit
	res.Notes = nil
	if total == 0 {
		res.Notes = []string{"No hand-added quads. Add one with the form below."}
	}
	return res, nil
}

// termText is a term as the form that added it would show it.
func termText(v quad.Value) string {
	switch x := v.(type) {
	case quad.IRI:
		return string(x)
	case quad.String:
		return string(x)
	case quad.BNode:
		return "_:" + string(x)
	}
	return fmt.Sprint(v.Native())
}
