package codegraph

import (
	"fmt"
	"sort"
	"strings"
)

// graph_schema: what an agent reads first. It describes the graph as it is right
// now (the kinds and predicates that exist, with counts and real example IDs,
// which layers are built and how fresh they are), with the meaning of each from
// docs/public/GRAPH_SCHEMA.md, which stays the authority.

type kindDoc struct {
	kind, layer, id, example, what string
}

// kindDocs: keep in step with the ID table of GRAPH_SCHEMA.md.
var kindDocs = []kindDoc{
	{"package", "code", "pkg:<dir>", "pkg:business/actions", "A Go package (a directory), classified by arch_layer: controllers, business, storage, middleware, commands, wiring, dev, views or support."},
	{"file", "code, templates", "file:<path>", "file:controllers/api.go", "A .go file (code layer) or a .templ file (templates layer)."},
	{"func", "code", "func:<dir>.<Name> or func:<dir>.<Recv>.<Name>", "func:controllers.HelloAPI", "A function or method. Closures have no node: a handler closure resolves to the function that contains it."},
	{"type", "code", "typ:<dir>.<Name>", "typ:storage/dbexplorer.Explorer", "A Go type declaration."},
	{"route", "routes", "route:<METHOD>:<path>", "route:GET:/api/hello", "An HTTP route from the live router. Method ANY is a route registered for every method."},
	{"middleware", "routes", "mw:<dir>.<Name> or mw:<import path>.<Name>", "mw:middleware.RequestLogger", "A middleware on one or more routes; implemented_by links it to application code."},
	{"template", "templates", "tmpl:<dir>.<Name>", "tmpl:assets/templates.Home", "A templ component."},
	{"mcp_tool", "mcp", "tool:<name>", "tool:graph_query", "An MCP tool registered with mcp.AddTool; the function that registers it `registers` it."},
	{"config_key", "config", "cfg:<dotted.key>", "cfg:server.port", "A key of a settings file (env.yml), with its default, backing environment variable and every use in the code."},
	{"env_var", "config", "env:<NAME>", "env:PORT", "An environment variable the config or the code reads."},
	{"doc", "docs", "doc:<path>", "doc:docs/public/ARCHITECTURE.md", "A markdown doc: docs/public, README.md or SKILL.md."},
	{"spec", "docs", "spec:<slug>", "spec:graph-indexer", "A spec under docs/specs, with its overall status."},
	{"spec_item", "docs", "item:<spec slug>/<row slug>", "item:graph-indexer/indexer-core-code-layer", "One row of a spec's status table: status, why it was skipped, and what it covers."},
	{"table", "db", "table:<name>", "table:items", "A database table, from the migrations and the live database."},
	{"column", "db", "col:<table>.<column>", "col:items.owner_id", "A table column."},
	{"index", "db", "idx:<table>.<name>", "idx:items.PRIMARY", "A table index; the primary key is PRIMARY."},
	{"migration", "db", "mig:<file stem>", "mig:0001_init", "A goose migration; introduced_in says which created a table, column or index."},
	{"external", "code, routes, calls", "ext:<import path>[.<Name>]", "", "A package or function outside the project that it imports, calls or is served by: the framework's public API, or a library. origin says whose it is, module and module_version which module of the build it comes from; the framework's internal/ is one leaf, flagged internal."},
	{"annotation", "db, calls, git", "ann:<hash>", "", "How sure the indexer is of one inferred edge (implicit_ref, many_to_many, a call through an interface, a pair of files that change together): its method and confidence."},
	{"finding", "findings", "finding:<rule>:<subject id>[#<detail>]", "finding:layering:pkg:controllers#pkg:storage/dbexplorer", "A derived problem: a layering violation, a stale doc, an unused config key. `about` is the node at fault; `rule`, `severity` and `message` say what and how to fix it."},
	{"rule", "findings", "rule:<name>", "rule:layering", "A check of the findings layer, and whether it ran on this graph (`checked`; `note` says why not). A rule that did not run has not looked: no findings from it is not a clean bill."},
	{"build", "meta", "build:latest", "build:latest", "The most recent index run: git sha, dirty flag, when, how long."},
	{"layer", "meta", "layer:<name>", "layer:code", "One layer's last run: status, quads, fingerprint, errors."},
}

// predicateDocs: the meaning of every predicate. A test checks that every
// predicate in a real graph has an entry.
var predicateDocs = map[string]string{
	// structure
	"type":                "The kind of a node (kind:func, kind:table, ...).",
	"defined_in":          "func, type or template -> the file that defines it.",
	"in_package":          "file -> its package.",
	"imports":             "package -> a package of this module it imports.",
	"has_layer":           "build -> a layer's last run.",
	"handled_by":          "route -> the function that handles it (absent when the handler is not application code).",
	"uses_mw":             "route -> a middleware on it (a set: the order is not kept).",
	"implemented_by":      "middleware -> the function that implements it.",
	"calls":               "func -> func it calls (the opt-in calls layer). A plain edge is a call the compiler resolves; one through an interface has an annotation with its confidence and the interface.",
	"co_changed_with":     "file, doc or spec -> one that changed in the same commits (the opt-in git layer); the annotation says in what share of this node's commits.",
	"renders":             "func or template -> the template it renders.",
	"uses_config":         "func or package -> a config key it reads.",
	"reads_env":           "config key or func -> an environment variable it reads.",
	"registers":           "func -> the MCP tool it registers.",
	"has_item":            "spec -> a row of its status table.",
	"mentions":            "doc, spec or spec item -> a node its text names.",
	"covers":              "spec item -> a node its row names.",
	"reads_table":         "func -> a table its SQL strings read.",
	"writes_table":        "func -> a table its SQL strings write.",
	"maps_to":             "type -> the table its db tags map it to.",
	"has_column":          "table or index -> a column.",
	"has_index":           "table -> an index.",
	"fk_to":               "column -> column, and table -> table, of a declared foreign key.",
	"implicit_ref":        "column -> column, table -> table: a reference the schema implies but does not declare (see the annotation for confidence).",
	"many_to_many":        "table -> table, through a junction table (inferred).",
	"introduced_in":       "table, column or index -> the migration that created it.",
	"subject":             "annotation -> where the inferred edge starts.",
	"object":              "annotation -> where the inferred edge ends.",
	"via":                 "annotation -> the junction table of a many_to_many edge.",
	"about":               "finding -> the node at fault.",
	"imports_external":    "package or .templ file -> a package outside the project that it imports (the framework's public packages).",
	"calls_external":      "func -> a function or package outside the project that it calls (the opt-in calls layer; bounded: one leaf per public framework function, one per library package, one for the framework's internals).",
	"handled_by_external": "route -> the function outside the project that serves it (a framework or library handler); the project's own handler is handled_by.",
	// attributes
	"name":              "Short display name.",
	"doc":               "First sentence of the doc comment.",
	"signature":         "One-line declaration.",
	"line":              "Source line of the declaration.",
	"exported":          "The name starts with an upper-case letter.",
	"path":              "Repository-relative file or directory; for a route its URL path.",
	"import_path":       "Go import path of a package.",
	"arch_layer":        "Architecture layer of a package: controllers, business, storage, middleware, commands, wiring, dev, views, support.",
	"origin":            "Whose a node is: app (the project's own) or framework. A route's is who registered it, a finding's is that of the node it is about. Written on routes, middleware, config keys, environment variables, MCP tools and externals; every other node is the project's.",
	"framework_key":     "On a config key: the framework's own defaults declare it too, so an application that overrides it is not the one that reads it.",
	"module":            "On an external: the module its package belongs to.",
	"module_version":    "On an external: that module's version in this build (\"(devel)\" for a local replace).",
	"internal":          "On an external: it is inside the framework's internal/ (not public API), collapsed to one leaf.",
	"off":               "On a rule: switched off in govibe.yml. A choice, not a rule that could not look.",
	"lines":             "Number of lines in a file.",
	"external_imports":  "Imports of packages outside this module (standard library, third party).",
	"generated":         "The file carries a `Code generated ... DO NOT EDIT.` marker.",
	"method":            "HTTP method of a route (ANY: every method); on an annotation, how the edge was inferred.",
	"dev_only":          "The route is /dev or under /dev/.",
	"handler":           "What serves a route that is not application code.",
	"description":       "An MCP tool's description.",
	"input_schema":      "An MCP tool's input as a JSON schema.",
	"default":           "Default value (never for sensitive keys or columns).",
	"sensitive":         "The key looks like a secret: it has no default or value in the graph.",
	"declared":          "Declared in a settings file; false means code reads it but nothing declares it.",
	"field":             "The Config struct field a key binds to.",
	"title":             "A doc's first heading.",
	"status":            "A spec's or spec item's status; on a layer run: ok, partial, skipped, off (an opt-in layer that is not switched on) or error.",
	"why_skipped":       "Why a spec row was skipped.",
	"note":              "Free-text note: a spec row's Why cell, or why a layer was skipped.",
	"data_type":         "A column's type as the database or migration spells it.",
	"nullable":          "The column allows NULL.",
	"key":               "PRI, UNI or MUL for a column that is indexed.",
	"extra":             "auto_increment, on update ..., generated.",
	"ordinal":           "Position of a column in its table.",
	"comment":           "A table's or column's COMMENT.",
	"engine":            "A table's storage engine.",
	"unique":            "The index is unique.",
	"primary":           "The index is the primary key.",
	"columns":           "An index's columns in order, comma-separated.",
	"implicit":          "The index was added by the server for a foreign key.",
	"source":            "Where a table, column or index comes from: migration, live or both.",
	"drift":             "How the migration and the live database disagree about this element.",
	"cardinality":       "one_to_one or one_to_many, of a single-column foreign key.",
	"fk_name":           "A foreign key's constraint name.",
	"on_delete":         "A foreign key's ON DELETE action.",
	"on_update":         "A foreign key's ON UPDATE action.",
	"version":           "A migration's version.",
	"applied":           "The live database has applied the migration.",
	"predicate":         "On an annotation: the inferred predicate's name.",
	"confidence":        "On an annotation: 0 to 100, how sure the inference is (for a co-change: the percentage of the subject's commits that also touched the object).",
	"rule":              "The rule that produced a finding.",
	"severity":          "error (breaks a stated rule), warning (wrong or stale) or info (a smell); a rule's own default on a rule node.",
	"message":           "What is wrong and what to do about it.",
	"checked":           "On a rule: it ran on this graph. false means a layer it reads is not built (see note) and its absence of findings proves nothing.",
	"unresolved_ref":    "On a doc or spec row: \"<line>:<text>\" of something it names that does not exist (a file, a symbol, a page).",
	"last_commit":       "The newest commit that touched a file, doc or spec (the git layer): the first 12 characters of its hash.",
	"last_commit_at":    "When that commit was made.",
	"last_subject":      "That commit's subject line.",
	"commits":           "Commits that touched it within the git layer's window (the newest 1,000).",
	"churn":             "Lines added plus lines deleted by those commits.",
	"authors":           "How many people made them (a count: names and addresses are never kept).",
	"together":          "On a co-change annotation: commits that touched both files.",
	"app_name":          "On the build node: the application's name (App.Name).",
	"app_module":        "On the build node: the application's module path.",
	"framework_module":  "On the build node: the framework module the application is built on.",
	"framework_version": "On the build node: that framework's version in the indexed build (\"(devel)\" for a local replace).",
	"git_sha":           "Short HEAD sha when the graph was indexed.",
	"dirty":             "The work tree had uncommitted changes.",
	"indexed_at":        "When it was indexed.",
	"duration_ms":       "How long it took.",
	"indexer_version":   "The indexer's output version.",
	"quads":             "Number of quads written.",
	"fingerprint":       "Hash of a layer's inputs; a changed fingerprint means the layer is stale.",
	"added":             "Quads added by the run.",
	"removed":           "Quads removed by the run.",
	"error":             "Why a layer failed.",
}

// Example is a Gizmo query for graph_query.
type Example struct {
	Question string `json:"question"`
	Query    string `json:"query"`
}

// exampleQueries are run in tests, so they cannot rot.
var exampleQueries = []Example{
	{"What handles a route?", "g.V(g.IRI('route:GET:/api/hello')).Out(g.IRI('handled_by')).All()"},
	{"Which packages import a package?", "g.V(g.IRI('pkg:business/actions')).In(g.IRI('imports')).All()"},
	{"Which functions read a table?", "g.V().Has(g.IRI('type'), g.IRI('kind:table')).In(g.IRI('reads_table')).All()"},
	{"Which code uses a config key?", "g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).All()"},
	{"What does the graph know about a node?", "g.V(g.IRI('func:controllers.HelloAPI')).OutPredicates().All()"},
	{"How many functions are there? (counting)", "g.Emit(g.V().Has(g.IRI('type'), g.IRI('kind:func')).ToArray().length)"},
}

// Question maps what an agent wants to know to the tool that answers it.
type Question struct {
	Question string `json:"question"`
	Use      string `json:"use"`
}

var questions = []Question{
	{"What is this file, package, function, route or table, and what is around it?", "graph_context"},
	{"What breaks if I change it? Which routes, tools, packages and docs are affected?", "graph_impact"},
	{"How does a route reach a table (or anything reach anything)?", "graph_path"},
	{"Where is it? I only know part of a name or a phrase.", "graph_search"},
	{"What is directly connected to this node?", "graph_neighbors"},
	{"What is stale, broken or against the rules?", "graph_findings"},
	{"Something else, or a pattern none of the above covers.", "graph_query (Gizmo)"},
}

// KindInfo describes one kind of node in this graph.
type KindInfo struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	Layer   string `json:"layer"`
	ID      string `json:"id"`
	Example string `json:"example,omitempty"`
	What    string `json:"what"`
}

// PredicateInfo describes one predicate in this graph.
type PredicateInfo struct {
	Predicate string   `json:"predicate"`
	Count     int      `json:"count"`
	Structure bool     `json:"structural"` // links two nodes (else: a literal attribute)
	From      []string `json:"from,omitempty"`
	OnKinds   int      `json:"on_kinds,omitempty"` // instead of `from`, for an attribute on more than six kinds
	To        []string `json:"to,omitempty"`
	What      string   `json:"what,omitempty"`
}

// AppInfo says what was indexed.
type AppInfo struct {
	Name   string `json:"name,omitempty"`
	Module string `json:"module,omitempty"`
}

// FrameworkInfo is the framework the application is built on, and its version in the
// indexed build.
type FrameworkInfo struct {
	Module  string `json:"module,omitempty"`
	Version string `json:"version,omitempty"`
}

// SchemaInfo is the output of graph_schema.
type SchemaInfo struct {
	AsOf string `json:"as_of"`
	// App and Framework say what the graph is of: the project, and what it is built on.
	App       AppInfo       `json:"app"`
	Framework FrameworkInfo `json:"framework"`
	// Scope is whose nodes the answers hold (the app's, the framework's, or both), and
	// Hidden what that leaves out: the counts below are of what the scope shows.
	Scope       Scope           `json:"scope"`
	Hidden      Hidden          `json:"hidden"`
	Freshness   Freshness       `json:"freshness"`
	Kinds       []KindInfo      `json:"kinds"`
	Predicates  []PredicateInfo `json:"predicates"`
	Conventions []string        `json:"conventions"`
	Questions   []Question      `json:"questions"`
	Examples    []Example       `json:"examples"`
	Notes       []string        `json:"notes,omitempty"`
}

// optInLayers are the layers that are off unless asked for; when one is missing
// from a graph it is named, with what it would give and how to switch it on, so an
// empty answer is not read as a clean one.
var optInLayers = []struct{ name, gives, enable string }{
	{callsLayerName, "call edges: with them a route's path to a table is exact; without them it is estimated from package imports (method package_imports, confidence <= 60)",
		"set GRAPH_CALLS=true, or call graph_reindex with layers [\"calls\"] (it type-checks the module, about a second)"},
	{gitLayerName, "last commit, churn and the files that change together, per file, doc and spec",
		"set GRAPH_GIT=true, or call graph_reindex with layers [\"git\"]"},
}

// Schema is graph_schema. stale lists the layers the indexer says are stale.
func (m *Model) Schema(stale []string) *SchemaInfo {
	s := &SchemaInfo{AsOf: m.AsOf(), Freshness: m.Freshness(), Questions: questions, Examples: m.examples(), Scope: m.Scope(), Hidden: m.Hidden()}
	s.Freshness.StaleLayers = stale
	if b := m.nodes[string(buildID)]; b != nil {
		s.App = AppInfo{Name: b.Str("app_name"), Module: b.Str("app_module")}
		s.Framework = FrameworkInfo{Module: b.Str("framework_module"), Version: b.Str("framework_version")}
	}
	if m.Scope() != ScopeAll {
		// The run's and the layers' quad counts are of the whole graph; under a scope
		// they would not add up to what the answers hold, so they are shown for `all` only.
		s.Freshness.Quads, s.Freshness.Added, s.Freshness.Removed = 0, 0, 0
		for i := range s.Freshness.Layers {
			s.Freshness.Layers[i].Quads = 0
		}
	}

	for _, kd := range kindDocs {
		ns := m.byKind[kd.kind]
		if len(ns) == 0 {
			continue
		}
		ex := kd.example
		if m.nodes[ex] == nil {
			ex = ns[0].ID
		}
		s.Kinds = append(s.Kinds, KindInfo{Kind: kd.kind, Count: len(ns), Layer: kd.layer, ID: kd.id, Example: ex, What: kd.what})
	}
	for k, ns := range m.byKind { // a kind the table does not know yet
		if k == "" || len(ns) == 0 {
			continue
		}
		known := false
		for _, kd := range kindDocs {
			known = known || kd.kind == k
		}
		if !known {
			s.Kinds = append(s.Kinds, KindInfo{Kind: k, Count: len(ns), Example: ns[0].ID})
		}
	}
	sort.SliceStable(s.Kinds, func(i, j int) bool { return s.Kinds[i].Kind < s.Kinds[j].Kind })

	type stat struct {
		count    int
		edge     bool
		from, to map[string]bool
	}
	stats := map[string]*stat{}
	get := func(p string) *stat {
		st := stats[p]
		if st == nil {
			st = &stat{from: map[string]bool{}, to: map[string]bool{}}
			stats[p] = st
		}
		return st
	}
	for _, n := range m.nodes {
		for p, vs := range n.Attrs {
			st := get(p)
			st.count += len(vs)
			st.from[n.Kind] = true
		}
		for p, ts := range n.Out {
			st := get(p)
			st.count += len(ts)
			st.edge = true
			st.from[n.Kind] = true
			for _, t := range ts {
				if tn := m.nodes[t]; tn != nil {
					st.to[tn.Kind] = true
				}
			}
		}
	}
	for p, st := range stats {
		pi := PredicateInfo{Predicate: p, Count: st.count, Structure: st.edge, From: setList(st.from), To: setList(st.to), What: predicateDocs[p]}
		if !st.edge && len(pi.From) > 6 {
			pi.OnKinds, pi.From = len(pi.From), nil
		}
		s.Predicates = append(s.Predicates, pi)
	}
	// `type` is stored as a kind, not an edge, but is a predicate agents query.
	s.Predicates = append(s.Predicates, PredicateInfo{Predicate: "type", Count: m.countNodes(), Structure: true, What: predicateDocs["type"], To: m.kindNames()})
	sort.Slice(s.Predicates, func(i, j int) bool {
		a, b := s.Predicates[i], s.Predicates[j]
		if a.Structure != b.Structure {
			return a.Structure
		}
		return a.Predicate < b.Predicate
	})

	s.Conventions = []string{
		"Node IDs are IRIs of the form <prefix>:<key> (see `kinds`). Treat them as opaque: follow edges and attributes, do not parse them.",
		"In graph_query, write IRIs as '<iri>' or g.IRI('iri'); a bare 'x' is a string and matches nothing generated. Counting is g.Emit(<path>.ToArray().length), since Count() returns null.",
		"Structural predicates link two nodes; attribute predicates carry a literal (name, line, doc, ...). `structural` in `predicates` tells which.",
		"Every graph tool brings stale layers up to date first and says what it answered from in `as_of`; a layer that failed or was skipped is named in `notes`.",
		"Confidence is 0 to 100. 100 is a stored edge, 90 an edge read from SQL strings or db tags, below that an estimate; `method` says which (edge, sql_literal, db_tag, table_level, inferred, interface_dispatch, package_imports, package_scope).",
		callConvention(m),
		"Hand-added quads (the /dev/graph form) are plain strings under no layer label; the graph_* tools never see them, graph_query does.",
		scopeConvention(m),
	}

	for _, ol := range optInLayers {
		if !m.layerBuilt(ol.name) {
			s.Notes = append(s.Notes, fmt.Sprintf("The %s layer is off, so the graph has no %s. To switch it on: %s.", ol.name, ol.gives, ol.enable))
		}
	}
	s.Notes = append(s.Notes, m.warnings("code", "routes", "templates", "config", "mcp", "db", "docs", "findings")...)
	for _, l := range stale {
		s.Notes = append(s.Notes, fmt.Sprintf("The %s layer is stale (its inputs changed since it was built, or it failed and has not been retried); graph_reindex rebuilds it.", l))
	}
	return s
}

// scopeConvention says whose nodes the answers hold and how to see the rest.
func scopeConvention(m *Model) string {
	switch m.Scope() {
	case ScopeApp:
		h := m.Hidden()
		return fmt.Sprintf("Scope app (the default): the answers are about the project's own code, docs and connections; %d nodes of the framework it is built on (its routes, "+
			"middleware, settings, tools, public API) are hidden, and what a node uses of it is a line of text (`uses`), not nodes. Pass scope=all to see both, each node labelled "+
			"with its `origin`, or scope=framework for the framework alone. graph_query is not scoped: it reads the raw store.", h.Nodes)
	case ScopeFramework:
		return "Scope framework: only what the framework contributes (its routes, middleware, settings, tools, public API) is shown; the project's own nodes are hidden. " +
			"Pass scope=all to see both. graph_query is not scoped: it reads the raw store."
	}
	return "Scope all: every node of the project and of the framework it is built on, each labelled with its `origin` (app or framework). Pass scope=app (the default) for the project alone. " +
		"graph_query is not scoped: it reads the raw store."
}

// examples are the example queries of graph_schema, with each one's subject taken from
// this graph, scope included, when it holds a better one than the stock example: a route
// of the project that has a handler, a package that is imported, a setting that is read.
func (m *Model) examples() []Example {
	out := append([]Example(nil), exampleQueries...)
	// An ID with a quote in it would break the query it is written into: keep the stock example.
	set := func(i int, format, id string) {
		if !strings.ContainsAny(id, `'\`) {
			out[i].Query = fmt.Sprintf(format, id)
		}
	}
	for _, r := range m.byKind["route"] {
		if len(r.Out["handled_by"]) > 0 && m.originOf(r) != OriginFramework {
			set(0, "g.V(g.IRI('%s')).Out(g.IRI('handled_by')).All()", r.ID)
			break
		}
	}
	for _, p := range m.byKind["package"] {
		if len(p.In["imports"]) > 0 {
			set(1, "g.V(g.IRI('%s')).In(g.IRI('imports')).All()", p.ID)
			break
		}
	}
	for _, k := range m.byKind["config_key"] {
		if len(k.In["uses_config"]) > 0 && m.originOf(k) != OriginFramework {
			set(3, "g.V(g.IRI('%s')).In(g.IRI('uses_config')).All()", k.ID)
			break
		}
	}
	for _, f := range m.byKind["func"] {
		if len(f.In["handled_by"]) > 0 {
			set(4, "g.V(g.IRI('%s')).OutPredicates().All()", f.ID)
			break
		}
	}
	return out
}

// callConvention says how reach is known in this graph, which depends on whether
// the opt-in calls layer was built.
func callConvention(m *Model) string {
	if m.layerBuilt(callsLayerName) {
		return "Call edges are indexed (the calls layer): a route reaches a table through a chain of stored `calls` edges. A plain call is certain; a call through an " +
			"interface is linked to each type that could implement it (method interface_dispatch, confidence 80 down to 30). Not seen: calls through function values, " +
			"reflection, and SQL built at run time, so \"no table\" means none that those leave out."
	}
	return "No call edges are indexed (the calls layer is off), so reach from a route or function to a table or another package is estimated from package imports and labelled so: possible, not proven."
}

func (m *Model) countNodes() int {
	n := 0
	for _, nd := range m.nodes {
		if nd.Kind != "" {
			n++
		}
	}
	return n
}

func setList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// --- findings ---

// FindingsQuery is the input of graph_findings.
type FindingsQuery struct {
	Severity string
	Rule     string
	Limit    int
}

// FindingsResult is the output of graph_findings.
type FindingsResult struct {
	// Available is false when the findings layer is not built: an empty list then
	// means "nothing was checked", not "nothing is wrong". The same holds for a
	// rule in Rules that did not run.
	Available  bool           `json:"available"`
	Rules      []RuleInfo     `json:"rules,omitempty"` // every rule, and whether it ran on this graph
	Findings   []FindingRef   `json:"findings"`
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity,omitempty"`
	ByRule     map[string]int `json:"by_rule,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
	Note       string         `json:"note,omitempty"`
	AsOf       string         `json:"as_of"`
}

const (
	defaultFindingsLimit = 50
	maxFindingsLimit     = 200
)

// Findings is graph_findings. The counts describe every finding; the list is
// what the filters leave.
func (m *Model) Findings(q FindingsQuery) *FindingsResult {
	res := &FindingsResult{Findings: []FindingRef{}, AsOf: m.AsOf(), Available: m.layerBuilt("findings")}
	if !res.Available {
		res.Note = "The findings layer is not built, so nothing has been checked: an empty list does not mean the code is clean."
		if st := m.layerStatus("findings"); st != "" {
			res.Note = fmt.Sprintf("The findings layer did not run successfully (status %s), so its findings are missing.", st)
		}
		return res
	}
	limit := clampLimit(q.Limit, defaultFindingsLimit, maxFindingsLimit)
	sev := strings.ToLower(strings.TrimSpace(q.Severity))
	rule := strings.TrimSpace(q.Rule)
	res.BySeverity, res.ByRule = map[string]int{}, map[string]int{}
	var all []FindingRef
	for _, f := range m.byKind["finding"] {
		fr := m.findingRef(f)
		res.BySeverity[fr.Severity]++
		res.ByRule[fr.Rule]++
		if sev != "" && strings.ToLower(fr.Severity) != sev {
			continue
		}
		if rule != "" {
			if p, wild := strings.CutSuffix(rule, "*"); wild && !strings.HasPrefix(fr.Rule, p) || !wild && fr.Rule != rule {
				continue
			}
		}
		all = append(all, fr)
	}
	res.Rules = m.ruleInfos(res.ByRule)
	if un := res.uncheckedRules(); len(un) > 0 {
		res.Note = fmt.Sprintf("%s did not run (see rules[].note for why): no findings from %s does not mean it is clean.",
			strings.Join(un, ", "), plural(len(un), "it", "them"))
	}
	sortFindings(all)
	res.Total = len(all)
	if len(all) > limit {
		all, res.Truncated = all[:limit], true
	}
	if all != nil {
		res.Findings = all
	}
	return res
}

// ruleInfos lists every rule of the graph, with how many findings each has.
func (m *Model) ruleInfos(byRule map[string]int) []RuleInfo {
	var out []RuleInfo
	for _, r := range m.byKind["rule"] {
		name := r.Str("name")
		out = append(out, RuleInfo{
			Name: name, Title: r.Str("title"), Severity: r.Str("severity"), Description: r.Str("description"),
			Checked: r.Bool("checked"), Off: r.Bool("off"), Note: r.Str("note"), Findings: byRule[name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// uncheckedRules names the rules that did not run.
func (r *FindingsResult) uncheckedRules() []string {
	var out []string
	for _, ri := range r.Rules {
		if !ri.Checked && !ri.Off {
			out = append(out, ri.Name)
		}
	}
	return out
}
