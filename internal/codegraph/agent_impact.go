package codegraph

import "sort"

// graph_impact answers "what is affected if I change X" in two tiers, and says
// which tier each answer comes from:
//
//   - Exact: a reverse walk over the dependency edges the graph really has
//     (handled_by, reads_table, imports of a package, renders, uses_config, fk_to,
//     ...), at most `depth` hops. Confidence 100, or 90 where the edge itself comes
//     from matching SQL literals or db tags, and the annotation's own figure for an
//     inferred relation.
//   - Package-level: without call edges (the opt-in `calls` layer is off) nothing
//     says which function calls which, so a function that is affected puts its whole
//     package in play. Importers of that package (confidence 60) and the routes
//     and MCP tools registered by code in those packages (50; 30 inside the
//     package itself) are possible, not proven, and each says so (method
//     `package_imports` / `package_scope`) and shows the import chain it rests on.
//
// With the calls layer the second tier is not needed for code: the walk over `calls`
// edges is exact (a call through an interface is an edge of lower confidence, method
// `interface_dispatch`), reaches the routes and tools that run the changed code at
// any depth the call chain needs, and says which way each route touches a table
// (the union over every path, not the first one found). A type keeps the package-level
// estimate, because a use of a type is not a call.
//
// Docs that mention the changed code are listed apart: they are what to update,
// not what breaks.

// Methods say how an affected node was found.
const (
	methodEdge      = "edge"            // a stored edge
	methodSQL       = "sql_literal"     // reads_table / writes_table, from SQL strings in the code
	methodDBTag     = "db_tag"          // maps_to, from `db:"col"` struct tags
	methodTable     = "table_level"     // a column change, seen at the level of its table
	methodInferred  = "inferred"        // an implicit relation, with the annotation's confidence
	methodPkgImport = "package_imports" // a package that imports an affected package
	methodPkgScope  = "package_scope"   // code in the same package as an affected function
	methodWiring    = "wiring_closure"  // a route or command whose handler is a closure declared inside an affected wiring function
)

// Access bits: how code touches a table.
const (
	accessRead  = 1
	accessWrite = 2
)

func accessString(a int) string {
	switch a {
	case accessRead:
		return "read"
	case accessWrite:
		return "write"
	case accessRead | accessWrite:
		return "read+write"
	}
	return ""
}

// ImpactQuery is the input of graph_impact.
type ImpactQuery struct {
	Node  string
	Depth int
	Limit int
	Kinds []string
}

// Affected is one node a change may reach.
type Affected struct {
	NodeRef
	Depth      int      `json:"depth"`
	Via        string   `json:"via"`
	From       string   `json:"from"`
	Through    []string `json:"through,omitempty"`
	Confidence int      `json:"confidence"`
	Method     string   `json:"method"`
	Access     string   `json:"access,omitempty"`
	// Schedule is how a command that is on a schedule is scheduled; it is what tells "a
	// scheduled command writes this table" from a command someone runs by hand.
	Schedule string `json:"schedule,omitempty"`
}

// DocHit is a doc, spec or spec row that mentions code in question.
type DocHit struct {
	NodeRef
	Title    string   `json:"title,omitempty"`
	Via      string   `json:"via"`
	Mentions []string `json:"mentions"`
	Spec     string   `json:"spec,omitempty"`
	Status   string   `json:"status,omitempty"`
}

// ImpactResult is the output of graph_impact.
type ImpactResult struct {
	Target        NodeRef        `json:"target"`
	SeedCount     int            `json:"seed_count"`
	Seeds         []string       `json:"seeds,omitempty"`
	Depth         int            `json:"depth"`
	Affected      []Affected     `json:"affected"`
	Total         int            `json:"total"`
	ByKind        map[string]int `json:"by_kind,omitempty"`
	ByMethod      map[string]int `json:"by_method,omitempty"`
	Docs          []DocHit       `json:"docs,omitempty"`
	DocsTruncated bool           `json:"docs_truncated,omitempty"`
	CoChanged     []CoChange     `json:"co_changed,omitempty"` // files, docs and specs that usually change with the changed code (the git layer)
	Truncated     bool           `json:"truncated,omitempty"`
	Notes         []string       `json:"notes,omitempty"`
	AsOf          string         `json:"as_of"`
}

const (
	defaultImpactDepth = 3
	maxImpactDepth     = 6
	// With call edges a route is typically four or five hops from the table it
	// reads (table, store function, service, handler, route), so the walk goes deeper.
	defaultCallsDepth  = 6
	maxCallsDepth      = 10
	defaultImpactLimit = 100
	maxImpactLimit     = 500
	maxDocHits         = 25
	maxSeedsShown      = 20
)

// pkgOf is the package a code node belongs to: a package is its own, a file its
// directory's, a function or type the one of the file that defines it. Templ
// components belong to no package node (their directory holds only generated Go).
func (m *Model) pkgOf(n *Node) *Node {
	switch n.Kind {
	case "package":
		return n
	case "file":
		return m.nodes[n.first("in_package")]
	case "func", "type":
		if f := m.nodes[n.first("defined_in")]; f != nil {
			return m.pkgOf(f)
		}
	}
	return nil
}

// codeIn lists the functions and types a package defines.
func (m *Model) codeIn(pkg *Node) []*Node {
	if m.pkgFuncs == nil {
		m.pkgFuncs = map[string][]*Node{}
	}
	if got, ok := m.pkgFuncs[pkg.ID]; ok {
		return got
	}
	var out []*Node
	for _, f := range m.in(pkg, "in_package") {
		for _, c := range m.in(f, "defined_in") {
			if c.Kind == "func" || c.Kind == "type" {
				out = append(out, c)
			}
		}
	}
	sortNodes(out)
	m.pkgFuncs[pkg.ID] = out
	return out
}

func (m *Model) annotations() map[string]annotation {
	if m.annIdx != nil {
		return m.annIdx
	}
	m.annIdx = map[string]annotation{}
	for _, a := range m.byKind["annotation"] {
		key := a.first("subject") + "\x00" + a.Str("predicate") + "\x00" + a.first("object")
		m.annIdx[key] = annotation{conf: a.Int("confidence"), method: a.Str("method"), via: a.first("via"), together: a.Int("together")}
	}
	return m.annIdx
}

// annotationOf is what the indexer recorded about an inferred edge: how sure it
// is and how it inferred it. An edge with no annotation reads as a 60.
func (m *Model) annotationOf(subject, pred, object string) annotation {
	if a, ok := m.annotations()[subject+"\x00"+pred+"\x00"+object]; ok && a.conf > 0 {
		return a
	}
	return annotation{conf: 60, method: methodInferred}
}

// annConf is how sure the indexer is of an inferred edge.
func (m *Model) annConf(subject, pred, object string) (int, string) {
	return m.annotationOf(subject, pred, object).conf, methodInferred
}

// step is one hop from a changed node to something that depends on it.
type step struct {
	to     *Node
	via    string
	conf   int
	method string
	access int
}

// dependents are the nodes that depend on x through an edge the graph holds.
// seedIsPkg says the change is to a whole package, whose importers then depend
// on it for certain; for anything else an importer is only a possibility.
func (m *Model) dependents(x *Node, seedIsPkg bool) []step {
	var out []step
	add := func(ns []*Node, via string, conf int, method string, access int) {
		for _, n := range ns {
			out = append(out, step{to: n, via: via, conf: conf, method: method, access: access})
		}
	}
	switch x.Kind {
	case "table":
		add(m.in(x, "reads_table"), "reads_table", 90, methodSQL, accessRead)
		add(m.in(x, "writes_table"), "writes_table", 90, methodSQL, accessWrite)
		add(m.in(x, "maps_to"), "maps_to", 90, methodDBTag, 0)
		add(m.in(x, "fk_to"), "fk_to", 100, methodEdge, 0)
		m.addInferred(&out, x, "implicit_ref")
	case "column":
		add(m.in(x, "fk_to"), "fk_to", 100, methodEdge, 0)
		m.addInferred(&out, x, "implicit_ref")
		// SQL is matched to tables, not columns: the readers of the column's
		// table may or may not use it.
		for _, t := range m.in(x, "has_column") {
			if t.Kind != "table" {
				continue
			}
			add(m.in(t, "reads_table"), "reads_table", 70, methodTable, accessRead)
			add(m.in(t, "writes_table"), "writes_table", 70, methodTable, accessWrite)
			add(m.in(t, "maps_to"), "maps_to", 70, methodTable, 0)
		}
	case "func":
		// A handler or a Run that is a closure inside a wiring function (routes.Register,
		// routes.Console, main) resolves to that function, which constructs dependencies and
		// so calls whatever changes: every entry point declared inline in it would be reported
		// as certain. They are possible, not proven.
		entryConf, entryMethod := 100, methodEdge
		if m.isWiring(x) {
			entryConf, entryMethod = 60, methodWiring
		}
		add(m.in(x, "handled_by"), "handled_by", entryConf, entryMethod, 0)
		add(m.in(x, "run_by"), "run_by", entryConf, entryMethod, 0)
		add(m.in(x, "implemented_by"), "implemented_by", 100, methodEdge, 0)
		add(m.out(x, "registers"), "registers", 100, methodEdge, 0)
		for _, c := range m.in(x, "calls") {
			conf, method, _ := m.callInfo(c.ID, x.ID)
			out = append(out, step{to: c, via: "calls", conf: conf, method: method})
		}
	case "middleware":
		add(m.in(x, "uses_mw"), "uses_mw", 100, methodEdge, 0)
	case "template":
		add(m.in(x, "renders"), "renders", 100, methodEdge, 0)
	case "config_key":
		add(m.in(x, "uses_config"), "uses_config", 100, methodEdge, 0)
	case "env_var":
		add(m.in(x, "reads_env"), "reads_env", 100, methodEdge, 0)
	case "package":
		if seedIsPkg {
			add(m.in(x, "imports"), "imports", 100, methodEdge, 0)
		} else {
			add(m.in(x, "imports"), "imports", 60, methodPkgImport, 0)
		}
	case "doc", "spec":
		for _, d := range m.in(x, "mentions") {
			if d.Kind == "doc" || d.Kind == "spec" {
				add([]*Node{d}, "mentions", 100, methodEdge, 0)
			}
		}
	}
	return out
}

// addInferred adds the nodes whose inferred relation (implicit_ref) points at x,
// each with the confidence its annotation records.
func (m *Model) addInferred(out *[]step, x *Node, pred string) {
	for _, n := range m.in(x, pred) {
		conf, method := m.annConf(n.ID, pred, x.ID)
		*out = append(*out, step{to: n, via: pred, conf: conf, method: method})
	}
}

// impactSeeds are the nodes a change to t changes: t, and the parts of it.
func (m *Model) impactSeeds(t *Node) []*Node {
	seeds := []*Node{t}
	switch t.Kind {
	case "package":
		for _, f := range m.in(t, "in_package") {
			seeds = append(seeds, m.codeMembers(f)...)
		}
	case "file":
		seeds = append(seeds, m.codeMembers(t)...)
	case "migration":
		for _, n := range m.in(t, "introduced_in") {
			if n.Kind == "table" {
				seeds = append(seeds, n)
			}
		}
	}
	return seeds
}

// codeMembers are the functions, types and components a file defines.
func (m *Model) codeMembers(f *Node) []*Node {
	var out []*Node
	for _, c := range m.in(f, "defined_in") {
		switch c.Kind {
		case "func", "type", "template":
			out = append(out, c)
		}
	}
	return out
}

// exportedCode reports whether code outside its package can use a function,
// type or component.
func exportedCode(n *Node) bool {
	if n.Kind == "template" {
		name := n.Str("name")
		return name != "" && name[0] >= 'A' && name[0] <= 'Z'
	}
	return n.Bool("exported")
}

type touched struct {
	exported bool
	helper   bool // a touched function is not itself an entry point, so it may be called by its siblings
	access   int
}

// Impact is graph_impact.
func (m *Model) Impact(q ImpactQuery) (*ImpactResult, error) {
	target, err := m.resolveOne(q.Node)
	if err != nil {
		return nil, err
	}
	kinds, err := m.kindSet(q.Kinds)
	if err != nil {
		return nil, err
	}
	calls := m.hasCalls()
	depth, deepest := q.Depth, maxImpactDepth
	if calls {
		deepest = maxCallsDepth
	}
	switch {
	case depth <= 0:
		depth = defaultImpactDepth
		if calls {
			depth = defaultCallsDepth
		}
	case depth > deepest:
		depth = deepest
	}
	limit := clampLimit(q.Limit, defaultImpactLimit, maxImpactLimit)

	seeds := m.impactSeeds(target)
	if calls && target.Kind == "type" {
		// A type's methods are part of it, and callers can be followed from them.
		seeds = append(seeds, m.typeMethods(target)...)
	}
	isSeed := map[string]bool{}
	for _, s := range seeds {
		isSeed[s.ID] = true
	}
	seen := map[string]*Affected{}
	for _, s := range seeds {
		seen[s.ID] = nil // a seed is not "affected"
	}
	access := map[string]int{} // access of the affected node, by ID
	var affected []*Affected

	// Tier 1: exact dependents, level by level.
	type reach struct {
		n      *Node
		conf   int
		method string
	}
	frontier := make([]reach, 0, len(seeds))
	for _, s := range seeds {
		frontier = append(frontier, reach{s, 100, methodEdge})
	}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		cand := map[string]*Affected{}
		candAccess := map[string]int{}
		var order []string
		for _, f := range frontier {
			for _, st := range m.dependents(f.n, target.Kind == "package") {
				if _, dup := seen[st.to.ID]; dup {
					continue
				}
				if calls && st.via == "calls" && !isSeed[f.n.ID] && m.isWiring(f.n) {
					// Composition roots (routes, main, cmd) call everything, so whatever
					// changes reaches them: they are listed when they call an affected
					// function, and not followed further.
					continue
				}
				a := &Affected{NodeRef: m.Ref(st.to), Depth: d, Via: st.via, From: f.n.ID, Confidence: f.conf, Method: f.method}
				if st.conf < a.Confidence {
					a.Confidence, a.Method = st.conf, st.method
				}
				acc := access[f.n.ID] | st.access
				if prev, ok := cand[st.to.ID]; ok {
					candAccess[st.to.ID] |= acc // however it is reached, it is reached by both
					if a.Confidence > prev.Confidence {
						cand[st.to.ID] = a
					}
					continue
				}
				cand[st.to.ID], candAccess[st.to.ID] = a, acc
				order = append(order, st.to.ID)
			}
		}
		frontier = frontier[:0]
		for _, id := range order {
			a := cand[id]
			a.Access = accessString(candAccess[id])
			access[id] = candAccess[id]
			seen[id] = a
			affected = append(affected, a)
			frontier = append(frontier, reach{m.nodes[id], a.Confidence, a.Method})
		}
	}

	// Tier 2: package-level reach from the code that is changed or affected.
	tp := map[string]*touched{}
	touch := func(n *Node, acc int) {
		p := m.pkgOf(n)
		if p == nil {
			return
		}
		t := tp[p.ID]
		if t == nil {
			t = &touched{}
			tp[p.ID] = t
		}
		t.exported = t.exported || exportedCode(n) || n.Kind == "package"
		// A function that is not itself a handler or a tool's registrar may be
		// called by the code around it. A type, or a whole package, is not taken to
		// be: that would put every route of the package in doubt whenever a file
		// declares a struct, and the guess is rarely right.
		t.helper = t.helper || n.Kind == "func" && len(n.In["handled_by"]) == 0 && len(n.In["run_by"]) == 0 && len(n.Out["registers"]) == 0
		t.access |= acc
	}
	// With call edges, code is traced exactly and the package-level estimate is only
	// for what a call edge cannot show: where a type is used.
	touches := func(kind string) bool {
		if calls {
			return kind == "type"
		}
		return kind == "func" || kind == "type" || kind == "package"
	}
	for _, s := range seeds {
		if touches(s.Kind) {
			touch(s, 0)
		}
	}
	for _, a := range affected {
		// Where an affected type is used is a guess; with call edges the code that
		// matters (what reads the table the type maps to) is already exact.
		if !calls && (a.Kind == "func" || a.Kind == "type") {
			touch(m.nodes[a.ID], access[a.ID])
		}
	}
	m.packageReach(tp, seen, &affected, access, target.Kind == "package")
	if calls {
		m.refineAccess(seeds, affected)
		m.chainEntryPoints(affected, seen, isSeed)
	}

	for _, a := range affected {
		if a.Kind == "command" {
			if n := m.nodes[a.ID]; n != nil {
				a.Schedule = n.Str("schedule")
			}
		}
	}
	res := &ImpactResult{Target: m.Ref(target), SeedCount: len(seeds), Depth: depth, AsOf: m.AsOf(),
		ByKind: map[string]int{}, ByMethod: map[string]int{}}
	for i, s := range seeds {
		if i == maxSeedsShown {
			break
		}
		res.Seeds = append(res.Seeds, s.ID)
	}
	for _, a := range affected {
		res.ByKind[a.Kind]++
		res.ByMethod[a.Method]++
	}
	var kept []Affected
	for _, a := range affected {
		if kinds == nil || kinds[a.Kind] {
			kept = append(kept, *a)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if ra, rb := rankOf(a.Kind), rankOf(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Kind == "command" && b.Kind == "command" && (a.Schedule != "") != (b.Schedule != "") {
			return a.Schedule != "" // what runs unwatched first
		}
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		return a.ID < b.ID
	})
	res.Total = len(kept)
	if len(kept) > limit {
		kept, res.Truncated = kept[:limit], true
	}
	res.Affected = kept
	if res.Affected == nil {
		res.Affected = []Affected{}
	}

	res.Docs, res.DocsTruncated = m.docsFor(m.docTargets(target, seeds), maxDocHits)
	res.CoChanged = m.coChangedWith(seeds)
	res.Notes = m.impactNotes(target, seeds, affected)
	return res, nil
}

// packageReach adds tier 2 to the affected list: the importers of every
// touched package that outside code can use, and the routes and MCP tools
// registered by code in a touched package or one of those importers.
func (m *Model) packageReach(tp map[string]*touched, seen map[string]*Affected, affected *[]*Affected, access map[string]int, seedIsPkg bool) {
	if len(tp) == 0 {
		return
	}
	type chain struct {
		path     []string // importer first, the touched package last; just the package when it is only touched
		access   int
		imported bool // reached through an import chain, not merely touched
	}
	reached := map[string]*chain{} // by package ID
	roots := make([]string, 0, len(tp))
	for id := range tp {
		roots = append(roots, id)
	}
	sort.Strings(roots)
	for _, root := range roots {
		t := tp[root]
		// the touched package itself: same-package code may call what changed
		if c := reached[root]; c != nil {
			c.access |= t.access
		} else if t.helper {
			reached[root] = &chain{path: []string{root}, access: t.access}
		}
		if !t.exported {
			continue // unexported code can only be used inside its package
		}
		parent := map[string]string{root: ""}
		queue := []string{root}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, imp := range m.nodes[cur].In["imports"] {
				if _, ok := parent[imp]; ok {
					continue
				}
				parent[imp] = cur
				queue = append(queue, imp)
				p := []string{imp}
				for at := cur; at != ""; at = parent[at] {
					p = append(p, at)
				}
				if c := reached[imp]; c == nil {
					reached[imp] = &chain{path: p, access: t.access, imported: true}
				} else {
					c.access |= t.access
					if !c.imported || len(p) < len(c.path) {
						c.path, c.imported = p, true
					}
				}
			}
		}
	}

	pkgIDs := make([]string, 0, len(reached))
	for id := range reached {
		pkgIDs = append(pkgIDs, id)
	}
	sort.Strings(pkgIDs)
	for _, id := range pkgIDs {
		c := reached[id]
		pkg := m.nodes[id]
		same := !c.imported
		conf, method := 50, methodPkgImport
		if same {
			conf, method = 30, methodPkgScope
		}
		if !same {
			if _, dup := seen[id]; !dup {
				a := &Affected{NodeRef: m.Ref(pkg), Depth: len(c.path) - 1, Via: "imports", From: c.path[1], Through: c.path,
					Confidence: 60, Method: methodPkgImport, Access: accessString(c.access)}
				seen[id] = a
				*affected = append(*affected, a)
			}
		}
		for _, code := range m.codeIn(pkg) {
			if code.Kind != "func" {
				continue
			}
			for _, r := range m.in(code, "handled_by") {
				m.addEntry(seen, affected, r, "handled_by", code.ID, c.path, conf, method, c.access)
			}
			for _, cmd := range m.in(code, "run_by") {
				m.addEntry(seen, affected, cmd, "run_by", code.ID, c.path, conf, method, c.access)
			}
			for _, tl := range m.out(code, "registers") {
				m.addEntry(seen, affected, tl, "registers", code.ID, c.path, conf, method, c.access)
			}
		}
	}
}

func (m *Model) addEntry(seen map[string]*Affected, affected *[]*Affected, n *Node, via, from string, path []string, conf int, method string, acc int) {
	if _, dup := seen[n.ID]; dup {
		return
	}
	a := &Affected{NodeRef: m.Ref(n), Depth: len(path), Via: via, From: from, Through: path, Confidence: conf, Method: method, Access: accessString(acc)}
	seen[n.ID] = a
	*affected = append(*affected, a)
}

// docTargets are the nodes whose mentions count as "this doc is about what
// changes": the seeds, what they sit in, and a table's columns.
func (m *Model) docTargets(target *Node, seeds []*Node) []*Node {
	var out []*Node
	have := map[string]bool{}
	add := func(n *Node) {
		if n != nil && !have[n.ID] {
			have[n.ID] = true
			out = append(out, n)
		}
	}
	for _, s := range seeds {
		add(s)
		switch s.Kind {
		case "func", "type":
			if f := m.nodes[s.first("defined_in")]; f != nil {
				add(f)
				add(m.pkgOf(f))
			}
		case "table":
			for _, c := range m.out(s, "has_column") {
				add(c)
			}
		case "column":
			for _, t := range m.in(s, "has_column") {
				if t.Kind == "table" {
					add(t)
				}
			}
		}
	}
	return out
}

// docsFor lists the docs, specs and spec rows that mention or cover any of the
// nodes, those hitting the most nodes first.
func (m *Model) docsFor(targets []*Node, limit int) ([]DocHit, bool) {
	hits := map[string]*DocHit{}
	for _, t := range targets {
		for _, via := range []string{"mentions", "covers"} {
			for _, d := range m.in(t, via) {
				switch d.Kind {
				case "doc", "spec", "spec_item":
				default:
					continue
				}
				h := hits[d.ID]
				if h == nil {
					h = &DocHit{NodeRef: m.Ref(d), Title: d.Str("title"), Via: via}
					if d.Kind == "spec_item" {
						h.Status = d.Str("status")
						if specs := m.in(d, "has_item"); len(specs) > 0 {
							h.Spec = specs[0].ID
						}
					} else if d.Kind == "spec" {
						h.Status = d.Str("status")
					}
					hits[d.ID] = h
				} else if via == "covers" {
					h.Via = "covers"
				}
				if len(h.Mentions) < 3 {
					h.Mentions = append(h.Mentions, t.ID)
				}
			}
		}
	}
	out := make([]DocHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if len(a.Mentions) != len(b.Mentions) {
			return len(a.Mentions) > len(b.Mentions)
		}
		if ra, rb := rankOf(a.Kind), rankOf(b.Kind); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
	if len(out) > limit {
		return out[:limit], true
	}
	return out, false
}

// impactNotes say what the answer cannot see, so that a short list is not read
// as a clean bill of health.
func (m *Model) impactNotes(target *Node, seeds []*Node, affected []*Affected) []string {
	var notes []string
	codeish := false
	for _, s := range seeds {
		switch s.Kind {
		case "func", "type", "package", "table", "column", "template", "config_key", "env_var", "middleware", "migration":
			codeish = true
		}
	}
	switch {
	case codeish && !m.hasCalls():
		notes = append(notes, "No call edges are indexed (the opt-in `calls` layer is off: set GRAPH_CALLS=true, or call graph_reindex with layers [\"calls\"]), so reach beyond the exact edges is estimated from packages: "+
			"anything with method `"+methodPkgImport+"` or `"+methodPkgScope+"` is possible, not proven, and rests on the import chain in `through`.")
	case codeish:
		notes = append(notes, "Callers come from the `calls` layer: a plain call is certain, a call through an interface is linked to each type that could implement it (method `"+methodDispatch+
			"`, confidence 80 down to 30). Not seen: calls through function values (callbacks, handlers held in variables), reflection, and SQL built at run time. "+
			"Wiring packages (routes, main, cmd) are listed when they call something affected and not followed further. Entry points (routes, commands, tools) carry the chain that reaches them in `through`.")
	}
	pkgLevel := false
	for _, a := range affected {
		if a.Method == methodPkgImport || a.Method == methodPkgScope {
			pkgLevel = true
		}
	}
	if codeish && !pkgLevel && !m.hasCalls() {
		notes = append(notes, "No route, tool or importing package was found through package imports.")
	}
	if target.Kind == "table" || target.Kind == "column" {
		notes = append(notes, "Readers and writers come from SQL strings in `storage` and `business` code; SQL built at run time is not seen.")
	}
	if (target.Kind == "func" || target.Kind == "type") && !exportedCode(target) && !m.hasCalls() {
		notes = append(notes, "This is unexported: only code in its own package can use it, and callers inside a package are not indexed (no `calls` layer); same-package routes and tools are listed with method `"+methodPkgScope+"`.")
	}
	if target.Kind == "type" && m.hasCalls() {
		notes = append(notes, "A type is used without being called: its methods are followed through `calls`, and where the type itself is used is estimated from package imports (method `"+methodPkgImport+"`).")
	}
	notes = append(notes, m.warnings(layersFor(target)...)...)
	return notes
}

// layersFor are the layers whose absence makes an answer about a node
// incomplete.
func layersFor(n *Node) []string {
	switch n.Kind {
	case "table", "column", "migration", "index":
		return []string{"code", "db", "commands"}
	case "route", "middleware":
		return []string{"code", "routes"}
	case "command":
		return []string{"code", "commands"}
	case "template":
		return []string{"code", "templates"}
	case "config_key", "env_var":
		return []string{"code", "config"}
	case "mcp_tool":
		return []string{"code", "mcp"}
	case "doc", "spec", "spec_item":
		return []string{"docs"}
	}
	return []string{"code", "routes", "commands"}
}
