package codegraph

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// graph_context: one call, one briefing. Whatever an agent has in hand (a file,
// a symbol, a route, a table) comes back with the package it lives in, what it
// defines, the routes it serves, the tables it touches, the config it reads, the
// docs and specs that mention it and the findings about it, so it does not have
// to grep the repo to orient itself.

// ContextQuery is the input of graph_context: exactly one field is set.
type ContextQuery struct {
	Path   string
	Symbol string
	Route  string
	Table  string
	Node   string
}

// Symbol is a function, type or component a file or package defines.
type Symbol struct {
	NodeRef
	Exported  bool   `json:"exported,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// RouteInfo is a route a piece of code serves.
type RouteInfo struct {
	ID         string   `json:"id"`
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Handler    string   `json:"handler,omitempty"`
	DevOnly    bool     `json:"dev_only,omitempty"`
	Middleware []string `json:"middleware,omitempty"`
}

// ConfigUse is a config key or environment variable the code reads.
type ConfigUse struct {
	NodeRef
	Default   string `json:"default,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	Env       string `json:"env,omitempty"`
}

// TableAccess is what code reads and writes, by the SQL it contains.
type TableAccess struct {
	Reads  []NodeRef `json:"reads,omitempty"`
	Writes []NodeRef `json:"writes,omitempty"`
	MapsTo []NodeRef `json:"maps_to,omitempty"`
}

// ReachTable is a table that code in a package the handler imports touches.
type ReachTable struct {
	Table      NodeRef  `json:"table"`
	Access     string   `json:"access"`
	Via        []string `json:"via"`
	Functions  []string `json:"functions,omitempty"`
	Confidence int      `json:"confidence"`
}

// Reach is the tables a function or handler reaches. With the calls layer it is
// a walk over call edges (Method "calls": each table comes with the chain of
// functions that gets there); without it, an estimate from package imports
// (Method "package_imports"): possible, not proven.
type Reach struct {
	Method    string       `json:"method"`
	Tables    []ReachTable `json:"tables"`
	Functions int          `json:"functions_reached,omitempty"` // how many functions the walk reached (calls only)
	Note      string       `json:"note"`
}

// History is what the git layer knows about a file, doc or spec: when it last
// changed, how often and how much, by how many people, and what changed with it.
type History struct {
	LastCommit   string     `json:"last_commit"`
	LastCommitAt string     `json:"last_commit_at,omitempty"`
	LastSubject  string     `json:"last_subject,omitempty"`
	Commits      int        `json:"commits"`
	Churn        int        `json:"churn"`
	Authors      int        `json:"authors"`
	CoChanged    []CoChange `json:"co_changed,omitempty"`
}

// CoChange is a file, doc or spec that changed in the same commits.
type CoChange struct {
	NodeRef
	Percent  int `json:"percent"`  // of this node's commits that also touched it
	Together int `json:"together"` // commits that touched both
}

// Relation links one table to another.
type Relation struct {
	Table      NodeRef `json:"table"`
	Kind       string  `json:"kind"` // foreign_key, implicit_ref, many_to_many
	Via        string  `json:"via,omitempty"`
	Columns    string  `json:"columns,omitempty"`
	Confidence int     `json:"confidence,omitempty"`
	Method     string  `json:"method,omitempty"`
}

// ColumnInfo describes a table's column.
type ColumnInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Nullable    bool   `json:"nullable"`
	Key         string `json:"key,omitempty"`
	Default     string `json:"default,omitempty"`
	Extra       string `json:"extra,omitempty"`
	Comment     string `json:"comment,omitempty"`
	References  string `json:"references,omitempty"`
	OnDelete    string `json:"on_delete,omitempty"`
	Implicit    string `json:"implicit_ref,omitempty"`
	ImplicitPct int    `json:"implicit_confidence,omitempty"`
	Source      string `json:"source,omitempty"`
	Drift       string `json:"drift,omitempty"`
}

// IndexInfo describes a table's index.
type IndexInfo struct {
	Name     string `json:"name"`
	Columns  string `json:"columns,omitempty"`
	Unique   bool   `json:"unique,omitempty"`
	Primary  bool   `json:"primary,omitempty"`
	Implicit bool   `json:"implicit,omitempty"`
}

// TableInfo is the schema side of a table's briefing.
type TableInfo struct {
	Engine       string       `json:"engine,omitempty"`
	Comment      string       `json:"comment,omitempty"`
	Source       string       `json:"source,omitempty"`
	Columns      []ColumnInfo `json:"columns"`
	Indexes      []IndexInfo  `json:"indexes,omitempty"`
	References   []Relation   `json:"references,omitempty"`
	ReferencedBy []Relation   `json:"referenced_by,omitempty"`
	ManyToMany   []Relation   `json:"many_to_many,omitempty"`
	Readers      []NodeRef    `json:"readers,omitempty"`
	Writers      []NodeRef    `json:"writers,omitempty"`
	MappedBy     []NodeRef    `json:"mapped_by,omitempty"`
	Migrations   []NodeRef    `json:"migrations,omitempty"`
}

// SpecItem is a row of a spec's status table.
type SpecItem struct {
	NodeRef
	Status     string   `json:"status,omitempty"`
	WhySkipped string   `json:"why_skipped,omitempty"`
	Note       string   `json:"note,omitempty"`
	Covers     []string `json:"covers,omitempty"`
}

// FindingRef is a finding about a node (the Phase 5 findings layer).
type FindingRef struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	About    string `json:"about,omitempty"`
	Path     string `json:"path,omitempty"` // where to look, when the rule knows
	Line     int    `json:"line,omitempty"`
}

// Context is the output of graph_context.
type Context struct {
	Node         NodeRef              `json:"node"`
	Attrs        map[string]any       `json:"attrs,omitempty"`
	ArchLayer    string               `json:"arch_layer,omitempty"`
	Package      *NodeRef             `json:"package,omitempty"`
	File         *NodeRef             `json:"file,omitempty"`
	Symbols      []Symbol             `json:"symbols,omitempty"`
	SymbolCounts map[string]int       `json:"symbol_counts,omitempty"`
	Routes       []RouteInfo          `json:"routes,omitempty"`
	Handler      *NodeRef             `json:"handler,omitempty"`
	Middleware   []NodeRef            `json:"middleware,omitempty"`
	Renders      []NodeRef            `json:"renders,omitempty"`
	RenderedBy   []NodeRef            `json:"rendered_by,omitempty"`
	Config       []ConfigUse          `json:"config,omitempty"`
	Tools        []NodeRef            `json:"tools,omitempty"`
	Tables       *TableAccess         `json:"tables,omitempty"`
	Calls        []CallRef            `json:"calls,omitempty"`
	CalledBy     []CallRef            `json:"called_by,omitempty"`
	Reach        *Reach               `json:"reach,omitempty"`
	History      *History             `json:"history,omitempty"`
	Table        *TableInfo           `json:"table,omitempty"`
	Items        []SpecItem           `json:"items,omitempty"`
	Imports      []NodeRef            `json:"imports,omitempty"`
	ImportedBy   []NodeRef            `json:"imported_by,omitempty"`
	Docs         []DocHit             `json:"docs,omitempty"`
	Findings     []FindingRef         `json:"findings,omitempty"`
	Related      map[string][]NodeRef `json:"related,omitempty"`
	Connections  map[string]int       `json:"connections,omitempty"`
	Notes        []string             `json:"notes,omitempty"`
	AsOf         string               `json:"as_of"`
}

const (
	maxSymbols      = 60
	maxListed       = 30
	maxRelatedPer   = 15
	maxReachTables  = 20
	maxAttrChars    = 400
	maxFindingsHere = 20
)

// Context is graph_context.
func (m *Model) Context(q ContextQuery) (*Context, error) {
	n, err := m.contextTarget(q)
	if err != nil {
		return nil, err
	}
	c := &Context{Node: m.Ref(n), Attrs: m.attrsOf(n), AsOf: m.AsOf(), Connections: m.connections(n)}

	switch n.Kind {
	case "package", "file", "func", "type":
		m.codeContext(c, n)
	case "template":
		m.templateContext(c, n)
	case "route":
		m.routeContext(c, n)
	case "table":
		m.tableContext(c, n)
	case "spec":
		m.specContext(c, n)
	default:
		c.Related = m.related(n)
	}

	c.History = m.historyOf(n)
	c.Docs, _ = m.docsFor(m.docTargets(n, m.impactSeeds(n)), maxDocHits)
	m.addFindings(c, n)
	c.Notes = append(c.Notes, m.warnings(layersFor(n)...)...)
	return c, nil
}

func (m *Model) contextTarget(q ContextQuery) (*Node, error) {
	given := map[string]string{}
	for k, v := range map[string]string{"path": q.Path, "symbol": q.Symbol, "route": q.Route, "table": q.Table, "node": q.Node} {
		if strings.TrimSpace(v) != "" {
			given[k] = strings.TrimSpace(v)
		}
	}
	if len(given) != 1 {
		return nil, fmt.Errorf("give exactly one of path, symbol, route, table or node (got %d)", len(given))
	}
	for k, v := range given {
		switch k {
		case "path":
			if got := m.ResolvePath(v); len(got) > 0 {
				return m.pick(v, got)
			}
			return nil, fmt.Errorf("%w; the graph holds .go and .templ files, packages (directories), docs and specs, not tests or other files", m.notFound(v))
		case "symbol":
			return m.pick(v, m.ResolveSymbol(v))
		case "route":
			return m.pick(v, m.ResolveRoute(v))
		case "table":
			return m.pick(v, m.ResolveTable(v))
		default:
			return m.resolveOne(v)
		}
	}
	return nil, fmt.Errorf("unreachable")
}

// attrsOf returns a node's literal attributes, except the ones NodeRef carries.
func (m *Model) attrsOf(n *Node) map[string]any {
	out := map[string]any{}
	for k, vs := range n.Attrs {
		switch k {
		case "name", "path", "line":
			continue
		}
		var conv []any
		for _, v := range vs {
			switch x := v.(type) {
			case string:
				if len(x) > maxAttrChars {
					x = x[:maxAttrChars] + "…"
				}
				conv = append(conv, x)
			case time.Time:
				conv = append(conv, x.Format(time.RFC3339))
			default:
				conv = append(conv, x)
			}
		}
		if len(conv) == 1 {
			out[k] = conv[0]
		} else if len(conv) > 1 {
			out[k] = conv
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// connections counts a node's edges by direction and predicate, so what the
// briefing leaves out can be found with graph_neighbors.
func (m *Model) connections(n *Node) map[string]int {
	out := map[string]int{}
	for p, ids := range n.Out {
		out["out:"+p] = len(ids)
	}
	for p, ids := range n.In {
		out["in:"+p] = len(ids)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// related lists a node's neighbours by direction and predicate, the briefing of
// a kind that has no section of its own.
func (m *Model) related(n *Node) map[string][]NodeRef {
	out := map[string][]NodeRef{}
	for _, a := range adjacency(n) {
		o := m.nodes[a.other]
		if o == nil || o.Kind == "annotation" || o.Kind == "layer" || o.Kind == "build" {
			continue
		}
		key := "in:" + a.pred
		if a.out {
			key = "out:" + a.pred
		}
		if len(out[key]) < maxRelatedPer {
			out[key] = append(out[key], m.Ref(o))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m *Model) capRefs(ns []*Node, max int) []NodeRef {
	sortNodes(ns)
	if len(ns) > max {
		ns = ns[:max]
	}
	return m.refs(ns)
}

// codeScope is the code a node stands for: itself, or what it holds.
type codeScope struct {
	funcs, types, templates []*Node
	pkgs                    []*Node
}

func (m *Model) scopeOf(n *Node) codeScope {
	var s codeScope
	add := func(c *Node) {
		switch c.Kind {
		case "func":
			s.funcs = append(s.funcs, c)
		case "type":
			s.types = append(s.types, c)
		case "template":
			s.templates = append(s.templates, c)
		}
	}
	switch n.Kind {
	case "package":
		s.pkgs = []*Node{n}
		for _, f := range m.in(n, "in_package") {
			for _, c := range m.in(f, "defined_in") {
				add(c)
			}
		}
	case "file":
		for _, c := range m.in(n, "defined_in") {
			add(c)
		}
	case "func", "type", "template":
		add(n)
	}
	return s
}

func (m *Model) codeContext(c *Context, n *Node) {
	pkg := m.pkgOf(n)
	if pkg != nil {
		if n.Kind != "package" {
			r := m.Ref(pkg)
			c.Package = &r
		}
		c.ArchLayer = pkg.Str("arch_layer")
	}
	if n.Kind == "func" || n.Kind == "type" {
		if f := m.nodes[n.first("defined_in")]; f != nil {
			r := m.Ref(f)
			c.File = &r
		}
	}
	scope := m.scopeOf(n)

	if n.Kind == "package" || n.Kind == "file" {
		var syms []*Node
		syms = append(syms, scope.funcs...)
		syms = append(syms, scope.types...)
		syms = append(syms, scope.templates...)
		sort.Slice(syms, func(i, j int) bool {
			a, b := m.Ref(syms[i]), m.Ref(syms[j])
			if a.Path != b.Path {
				return a.Path < b.Path
			}
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.ID < b.ID
		})
		if len(syms) > maxSymbols {
			c.SymbolCounts = map[string]int{}
			for _, s := range syms {
				c.SymbolCounts[s.Kind]++
			}
			syms = syms[:maxSymbols]
		}
		for _, s := range syms {
			c.Symbols = append(c.Symbols, Symbol{NodeRef: m.Ref(s), Exported: exportedCode(s), Signature: s.Str("signature")})
		}
	}

	routes := map[string]*Node{}
	renders := map[string]*Node{}
	cfg := map[string]*Node{}
	tools := map[string]*Node{}
	reads, writes, maps := map[string]*Node{}, map[string]*Node{}, map[string]*Node{}
	collect := func(dst map[string]*Node, ns []*Node) {
		for _, x := range ns {
			dst[x.ID] = x
		}
	}
	for _, f := range scope.funcs {
		collect(routes, m.in(f, "handled_by"))
		collect(renders, m.out(f, "renders"))
		collect(cfg, m.out(f, "uses_config"))
		collect(cfg, m.out(f, "reads_env"))
		collect(tools, m.out(f, "registers"))
		collect(reads, m.out(f, "reads_table"))
		collect(writes, m.out(f, "writes_table"))
	}
	for _, t := range scope.types {
		collect(maps, m.out(t, "maps_to"))
	}
	for _, p := range scope.pkgs {
		collect(cfg, m.out(p, "uses_config"))
	}
	c.Routes = m.routeInfos(routes)
	c.Renders = m.capRefs(valuesOf(renders), maxListed)
	c.Tools = m.capRefs(valuesOf(tools), maxListed)
	c.Config = m.configUses(cfg)
	if len(reads)+len(writes)+len(maps) > 0 {
		c.Tables = &TableAccess{Reads: m.capRefs(valuesOf(reads), maxListed), Writes: m.capRefs(valuesOf(writes), maxListed), MapsTo: m.capRefs(valuesOf(maps), maxListed)}
	}

	if pkg != nil {
		c.Imports = m.capRefs(m.out(pkg, "imports"), maxListed)
		c.ImportedBy = m.capRefs(m.in(pkg, "imports"), maxListed)
	}
	if n.Kind == "func" {
		if m.hasCalls() {
			c.Calls, c.CalledBy = m.callRefs(n, true), m.callRefs(n, false)
			c.Reach = m.reachByCalls(n, c.Tables)
		} else {
			c.Reach = m.reachFrom(pkg, c.Tables, n.ID)
		}
	}
	if n.Kind == "type" && len(maps) == 0 {
		c.Notes = append(c.Notes, "This type is mapped to no table (maps_to needs `db:\"col\"` tags covering one table's columns).")
	}
}

func valuesOf(m map[string]*Node) []*Node {
	out := make([]*Node, 0, len(m))
	for _, n := range m {
		out = append(out, n)
	}
	return out
}

// routeInfos describes routes, sorted by path then method.
func (m *Model) routeInfos(routes map[string]*Node) []RouteInfo {
	list := valuesOf(routes)
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Str("path") != b.Str("path") {
			return a.Str("path") < b.Str("path")
		}
		return a.ID < b.ID
	})
	if len(list) > maxListed {
		list = list[:maxListed]
	}
	var out []RouteInfo
	for _, r := range list {
		ri := RouteInfo{ID: r.ID, Method: r.Str("method"), Path: r.Str("path"), Handler: r.first("handled_by"), DevOnly: r.Bool("dev_only")}
		for _, mw := range m.out(r, "uses_mw") {
			ri.Middleware = append(ri.Middleware, mw.Str("name"))
		}
		out = append(out, ri)
	}
	return out
}

func (m *Model) configUses(cfg map[string]*Node) []ConfigUse {
	list := valuesOf(cfg)
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	if len(list) > maxListed {
		list = list[:maxListed]
	}
	var out []ConfigUse
	for _, k := range list {
		u := ConfigUse{NodeRef: m.Ref(k), Default: k.Str("default"), Sensitive: k.Bool("sensitive")}
		if envs := k.Out["reads_env"]; len(envs) > 0 && k.Kind == "config_key" {
			u.Env = strings.TrimPrefix(envs[0], "env:")
		}
		out = append(out, u)
	}
	return out
}

func (m *Model) templateContext(c *Context, n *Node) {
	if f := m.nodes[n.first("defined_in")]; f != nil {
		r := m.Ref(f)
		c.File = &r
	}
	c.Renders = m.capRefs(m.out(n, "renders"), maxListed)
	c.RenderedBy = m.capRefs(m.in(n, "renders"), maxListed)
	// the routes whose handler renders it
	routes := map[string]*Node{}
	for _, f := range m.in(n, "renders") {
		if f.Kind == "func" {
			for _, r := range m.in(f, "handled_by") {
				routes[r.ID] = r
			}
		}
	}
	c.Routes = m.routeInfos(routes)
}

func (m *Model) routeContext(c *Context, n *Node) {
	c.Middleware = m.capRefs(m.out(n, "uses_mw"), maxListed)
	h := m.nodes[n.first("handled_by")]
	if h == nil {
		if s := n.Str("handler"); s != "" {
			c.Notes = append(c.Notes, fmt.Sprintf("The handler is not application code (%s), so nothing below it is traced.", s))
		}
		return
	}
	r := m.Ref(h)
	c.Handler = &r
	if f := m.nodes[h.first("defined_in")]; f != nil {
		fr := m.Ref(f)
		c.File = &fr
	}
	if pkg := m.pkgOf(h); pkg != nil {
		pr := m.Ref(pkg)
		c.Package = &pr
		c.ArchLayer = pkg.Str("arch_layer")
		c.Imports = m.capRefs(m.out(pkg, "imports"), maxListed)
	}
	cfg := map[string]*Node{}
	for _, k := range m.out(h, "uses_config") {
		cfg[k.ID] = k
	}
	for _, k := range m.out(h, "reads_env") {
		cfg[k.ID] = k
	}
	c.Config = m.configUses(cfg)
	c.Renders = m.capRefs(m.out(h, "renders"), maxListed)
	ta := &TableAccess{Reads: m.capRefs(m.out(h, "reads_table"), maxListed), Writes: m.capRefs(m.out(h, "writes_table"), maxListed)}
	if len(ta.Reads)+len(ta.Writes) > 0 {
		c.Tables = ta
	}
	if m.hasCalls() {
		c.Calls = m.callRefs(h, true)
		c.Reach = m.reachByCalls(h, c.Tables)
		return
	}
	c.Reach = m.reachFrom(m.pkgOf(h), c.Tables, h.ID)
}

// reachFrom lists the tables that code in pkg, or in packages it imports
// (transitively), reads or writes, shortest import chain first. It leaves out
// the tables already named exactly. The answer is a superset of what the code
// reaches: no table here means none is reached (SQL built at run time aside); a
// table here may well not be.
func (m *Model) reachFrom(pkg *Node, exact *TableAccess, from string) *Reach {
	if pkg == nil || !m.layerBuilt("db") {
		return nil
	}
	skip := map[string]bool{}
	if exact != nil {
		for _, l := range [][]NodeRef{exact.Reads, exact.Writes} {
			for _, t := range l {
				skip[t.ID] = true
			}
		}
	}
	type found struct {
		access int
		via    []string
		funcs  []string
	}
	tables := map[string]*found{}
	parent := map[string]string{pkg.ID: ""}
	queue := []string{pkg.ID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		node := m.nodes[cur]
		for _, code := range m.codeIn(node) {
			if code.Kind != "func" {
				continue
			}
			for _, pred := range []struct {
				p string
				a int
			}{{"reads_table", accessRead}, {"writes_table", accessWrite}} {
				for _, t := range code.Out[pred.p] {
					if skip[t] && code.ID == from {
						continue
					}
					f := tables[t]
					if f == nil {
						var via []string
						for at := cur; at != ""; at = parent[at] {
							via = append([]string{at}, via...)
						}
						f = &found{via: via}
						tables[t] = f
					}
					f.access |= pred.a
					if len(f.funcs) < 3 && f.via[len(f.via)-1] == cur {
						f.funcs = append(f.funcs, code.ID)
					}
				}
			}
		}
		for _, imp := range node.Out["imports"] {
			if _, ok := parent[imp]; !ok {
				parent[imp] = cur
				queue = append(queue, imp)
			}
		}
	}
	r := &Reach{Method: methodPkgImport, Tables: []ReachTable{}}
	ids := make([]string, 0, len(tables))
	for id := range tables {
		if !skip[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		f := tables[id]
		r.Tables = append(r.Tables, ReachTable{Table: m.Ref(m.nodes[id]), Access: accessString(f.access), Via: f.via, Functions: f.funcs, Confidence: 50})
	}
	sort.SliceStable(r.Tables, func(i, j int) bool { return len(r.Tables[i].Via) < len(r.Tables[j].Via) })
	if len(r.Tables) > maxReachTables {
		r.Tables = r.Tables[:maxReachTables]
	}
	direct := exact != nil && len(exact.Reads)+len(exact.Writes) > 0
	switch {
	case len(r.Tables) > 0 && !direct:
		r.Note = "This code touches no table itself. No call edges are indexed (the `calls` layer is Phase 6), so the tables below are possible, not proven: " +
			"code in the packages its package imports (transitively) touches them, and it may call none of it. `via` is the shortest import chain."
	case len(r.Tables) > 0:
		r.Note = "Besides the tables it touches itself: no call edges are indexed (the `calls` layer is Phase 6), so the tables below are possible, not proven. `via` is the shortest import chain."
	case direct:
		r.Note = "Nothing else is reachable: no other table is touched by the packages this one imports."
	default:
		r.Note = "No table is reached: this code touches none directly, and nothing in the packages it imports (transitively) reads or writes one. Not seen: SQL built at run time."
	}
	return r
}

func (m *Model) tableContext(c *Context, n *Node) {
	t := &TableInfo{Engine: n.Str("engine"), Comment: n.Str("comment"), Source: n.Str("source"), Columns: []ColumnInfo{}}
	cols := m.out(n, "has_column")
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].Int("ordinal") < cols[j].Int("ordinal") })
	for _, col := range cols {
		ci := ColumnInfo{Name: col.Str("name"), Type: col.Str("data_type"), Nullable: col.Bool("nullable"), Key: col.Str("key"),
			Default: col.Str("default"), Extra: col.Str("extra"), Comment: col.Str("comment"), OnDelete: col.Str("on_delete"),
			Source: col.Str("source"), Drift: col.Str("drift")}
		if refs := col.Out["fk_to"]; len(refs) > 0 {
			ci.References = refs[0]
		}
		if refs := col.Out["implicit_ref"]; len(refs) > 0 {
			ci.Implicit = refs[0]
			ci.ImplicitPct = m.annotationOf(col.ID, "implicit_ref", refs[0]).conf
		}
		t.Columns = append(t.Columns, ci)
	}
	for _, ix := range m.out(n, "has_index") {
		t.Indexes = append(t.Indexes, IndexInfo{Name: ix.Str("name"), Columns: ix.Str("columns"), Unique: ix.Bool("unique"),
			Primary: ix.Bool("primary"), Implicit: ix.Bool("implicit")})
	}
	for _, o := range m.out(n, "fk_to") {
		t.References = append(t.References, Relation{Table: m.Ref(o), Kind: "foreign_key", Columns: m.fkColumns(n, o)})
	}
	for _, o := range m.out(n, "implicit_ref") {
		a := m.annotationOf(n.ID, "implicit_ref", o.ID)
		t.References = append(t.References, Relation{Table: m.Ref(o), Kind: "implicit_ref", Confidence: a.conf, Method: a.method, Columns: m.implicitColumns(n, o)})
	}
	for _, o := range m.in(n, "fk_to") {
		if o.Kind == "table" {
			t.ReferencedBy = append(t.ReferencedBy, Relation{Table: m.Ref(o), Kind: "foreign_key", Columns: m.fkColumns(o, n)})
		}
	}
	for _, o := range m.in(n, "implicit_ref") {
		if o.Kind == "table" {
			a := m.annotationOf(o.ID, "implicit_ref", n.ID)
			t.ReferencedBy = append(t.ReferencedBy, Relation{Table: m.Ref(o), Kind: "implicit_ref", Confidence: a.conf, Method: a.method, Columns: m.implicitColumns(o, n)})
		}
	}
	for _, o := range m.out(n, "many_to_many") {
		a := m.annotationOf(n.ID, "many_to_many", o.ID)
		t.ManyToMany = append(t.ManyToMany, Relation{Table: m.Ref(o), Kind: "many_to_many", Via: a.via, Confidence: a.conf, Method: a.method})
	}
	t.Readers = m.capRefs(m.in(n, "reads_table"), maxListed)
	t.Writers = m.capRefs(m.in(n, "writes_table"), maxListed)
	t.MappedBy = m.capRefs(m.in(n, "maps_to"), maxListed)
	t.Migrations = m.capRefs(m.out(n, "introduced_in"), maxListed)
	c.Table = t
	if len(t.Readers)+len(t.Writers) == 0 {
		c.Notes = append(c.Notes, "No code reads or writes this table by an SQL string the indexer can see (SQL built at run time is not seen).")
	}
	if m.layerBuilt("db") {
		c.Notes = append(c.Notes, "For the routes and tools that can reach this table, call graph_impact.")
	}
}

// fkColumns names the columns of a that reference b, as `a.col -> b.col`.
func (m *Model) fkColumns(a, b *Node) string { return m.columnPairs(a, b, "fk_to") }

func (m *Model) implicitColumns(a, b *Node) string { return m.columnPairs(a, b, "implicit_ref") }

func (m *Model) columnPairs(a, b *Node, pred string) string {
	var pairs []string
	for _, col := range m.out(a, "has_column") {
		for _, t := range col.Out[pred] {
			if tn := m.nodes[t]; tn != nil && contains(b.Out["has_column"], t) {
				pairs = append(pairs, strings.TrimPrefix(col.ID, "col:")+" -> "+strings.TrimPrefix(tn.ID, "col:"))
			}
		}
	}
	return strings.Join(pairs, ", ")
}

func (m *Model) specContext(c *Context, n *Node) {
	for _, it := range m.out(n, "has_item") {
		si := SpecItem{NodeRef: m.Ref(it), Status: it.Str("status"), WhySkipped: it.Str("why_skipped"), Note: it.Str("note"), Covers: it.Out["covers"]}
		c.Items = append(c.Items, si)
	}
	sort.SliceStable(c.Items, func(i, j int) bool { return c.Items[i].Line < c.Items[j].Line })
	c.Related = m.related(n)
	delete(c.Related, "out:has_item")
}

// addFindings attaches the findings about a node (and, for a package or file,
// about its members) and says when there is no findings layer to ask.
func (m *Model) addFindings(c *Context, n *Node) {
	if !m.layerBuilt("findings") {
		c.Notes = append(c.Notes, "The findings layer is not built yet (Phase 5 of SPEC_GRAPH_INDEXER): no finding here does not mean the code is clean.")
		return
	}
	for _, f := range m.findingsAbout(m.impactSeeds(n)) {
		if len(c.Findings) == maxFindingsHere {
			break
		}
		c.Findings = append(c.Findings, f)
	}
}

// findingsAbout returns the findings whose `about` is one of the nodes.
func (m *Model) findingsAbout(nodes []*Node) []FindingRef {
	var out []FindingRef
	seen := map[string]bool{}
	for _, n := range nodes {
		for _, f := range m.in(n, "about") {
			if f.Kind == "finding" && !seen[f.ID] {
				seen[f.ID] = true
				out = append(out, m.findingRef(f))
			}
		}
	}
	sortFindings(out)
	return out
}

func (m *Model) findingRef(f *Node) FindingRef {
	return FindingRef{ID: f.ID, Rule: f.Str("rule"), Severity: f.Str("severity"), Message: f.Str("message"), About: f.first("about"),
		Path: f.Str("path"), Line: f.Int("line")}
}

// severityRank orders findings, worst first; unknown severities last.
func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "error", "high", "critical":
		return 0
	case "warning", "warn", "medium":
		return 1
	case "info", "low", "note":
		return 2
	}
	return 3
}

func sortFindings(fs []FindingRef) {
	sort.SliceStable(fs, func(i, j int) bool {
		if a, b := severityRank(fs[i].Severity), severityRank(fs[j].Severity); a != b {
			return a < b
		}
		if fs[i].Rule != fs[j].Rule {
			return fs[i].Rule < fs[j].Rule
		}
		return fs[i].ID < fs[j].ID
	})
}
