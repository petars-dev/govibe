package codegraph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/petars-dev/govibe/internal/rules"
)

// The generated reference pages (/dev/graph/reports/{routes,config,schema,layers}):
// the route table, the config reference, the database schema and the package
// layer map, rendered from the graph so they cannot drift from the code the way a
// hand-kept page does. This file builds their data; the controllers render it.
// Nothing here reads a row of data, and nothing prints a value the graph keeps
// secret: a sensitive setting has no default in the graph, and the builders
// refuse to show one even if it had.

// ReportRef is a node as a report row names it: what it is, how a row labels it,
// and where it can be opened.
type ReportRef struct {
	NodeRef
	Label     string `json:"label"`
	Anchor    string `json:"anchor"`
	GraphURL  string `json:"graph_url"`
	EditorURL string `json:"editor_url,omitempty"` // path:line in the editor, when the node has a place and the root is known
	PageURL   string `json:"page_url,omitempty"`   // its /dev/docs page, or its row on this report
	DBURL     string `json:"db_url,omitempty"`     // the table in the database explorer, when that is mounted
}

// FindingSummary is what the findings layer says about a row.
type FindingSummary struct {
	Count int          `json:"count"`
	Worst string       `json:"worst,omitempty"` // the worst severity among them
	Items []FindingRef `json:"items,omitempty"` // worst first
}

// reportRef describes a node for a report row.
func (m *Model) reportRef(n *Node, o LinkOptions) ReportRef {
	r := ReportRef{NodeRef: m.Ref(n), Label: m.reportLabel(n), Anchor: Anchor(n.ID), GraphURL: m.graphURL(n.ID)}
	for _, l := range m.Links(n, o) {
		switch l.Kind {
		case "editor":
			r.EditorURL = l.URL
		case "docs":
			r.PageURL = l.URL
		case "db":
			r.DBURL = l.URL
		}
	}
	return r
}

// onPage points a ref at its own row on the report that is being built.
func onPage(r ReportRef) ReportRef {
	r.PageURL = "#" + r.Anchor
	return r
}

// reportLabel is how a row names a node: a function or type with its package
// (`controllers.GetOrder`), a package by its directory, a route by its method and
// path, a doc by its file.
func (m *Model) reportLabel(n *Node) string {
	name := n.Str("name")
	switch n.Kind {
	case "func", "type":
		if p := m.pkgOf(n); p != nil && p.Str("name") != "" && name != "" {
			return p.Str("name") + "." + name
		}
	case "package":
		if p := n.Str("path"); p != "" {
			return p
		}
	case "route":
		return n.Str("method") + " " + n.Str("path")
	case "doc", "spec":
		if p := n.Str("path"); p != "" {
			return p
		}
	}
	if name == "" {
		return n.ID
	}
	return name
}

func (m *Model) reportRefs(ns []*Node, o LinkOptions) []ReportRef {
	out := make([]ReportRef, 0, len(ns))
	for _, n := range ns {
		out = append(out, m.reportRef(n, o))
	}
	return out
}

// docRefsOf lists the docs and specs that mention or cover a node, once each (a
// spec row counts for its spec).
func (m *Model) docRefsOf(n *Node, o LinkOptions) []ReportRef {
	seen := map[string]bool{}
	var docs []*Node
	var add func(d *Node)
	add = func(d *Node) {
		switch d.Kind {
		case "spec_item":
			for _, s := range m.in(d, "has_item") {
				add(s)
			}
		case "doc", "spec":
			if !seen[d.ID] {
				seen[d.ID] = true
				docs = append(docs, d)
			}
		}
	}
	for _, p := range []string{"mentions", "covers"} {
		for _, d := range m.in(n, p) {
			add(d)
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
	return m.reportRefs(docs, o)
}

// summaryOf collects the findings about the nodes.
func (m *Model) summaryOf(nodes ...*Node) FindingSummary {
	items := m.findingsAbout(nodes)
	s := FindingSummary{Count: len(items), Items: items}
	if len(items) > 0 {
		s.Worst = items[0].Severity
	}
	return s
}

// partialNotes says which of the layers are only partly indexed, for a report
// that reads them: a warning, not a failure.
func (m *Model) partialNotes(layers ...string) []string {
	var out []string
	for _, l := range layers {
		if m.layerStatus(l) == statusPartial {
			out = append(out, fmt.Sprintf("the %s layer is partial: some of its inputs were skipped, so this report may be missing rows", l))
		}
	}
	return out
}

// notesFor lists what a report cannot show: layers it reads that are missing or
// failed, and the report's own layers when they are only partly indexed. A
// partial layer it merely resolves against (the docs, the findings) is not worth
// a warning on a page that does not depend on its completeness.
func (m *Model) notesFor(own string, others ...string) []string {
	return append(m.warnings(append([]string{own}, others...)...), m.partialNotes(own)...)
}

// --- routes ---

// RoutesReport is the route table.
type RoutesReport struct {
	AsOf      string     `json:"as_of"`
	Notes     []string   `json:"notes,omitempty"`
	AppRoutes int        `json:"app_routes"`
	DevRoutes int        `json:"dev_routes"`
	Routes    []RouteRow `json:"routes"` // application routes first, then /dev
	Calls     bool       `json:"calls"`  // the call graph is indexed, so each route lists the tables it reaches
}

// RouteRow is one route.
type RouteRow struct {
	Route      ReportRef      `json:"route"`
	Method     string         `json:"method"`
	Path       string         `json:"path"`
	Dev        bool           `json:"dev"`
	Handler    *ReportRef     `json:"handler,omitempty"`
	LibHandler string         `json:"lib_handler,omitempty"` // what serves it when that is not application code
	Middleware []ReportRef    `json:"middleware,omitempty"`
	Docs       []ReportRef    `json:"docs,omitempty"`
	Tables     []RouteTable   `json:"tables,omitempty"` // with the calls layer: what the handler's calls reach
	Findings   FindingSummary `json:"findings"`
}

// RouteTable is a table a route's handler reaches through its calls.
type RouteTable struct {
	Table      ReportRef `json:"table"`
	Access     string    `json:"access"`
	Confidence int       `json:"confidence"`
}

// RoutesReport builds the route table.
func (m *Model) RoutesReport(o LinkOptions) *RoutesReport {
	rep := &RoutesReport{AsOf: m.AsOf(), Notes: m.notesFor("routes", "code", "docs", "findings"), Routes: []RouteRow{}, Calls: m.hasCalls()}
	for _, n := range m.byKind["route"] {
		row := RouteRow{
			Route: m.reportRef(n, o), Method: n.Str("method"), Path: n.Str("path"), Dev: n.Bool("dev_only"),
			Docs: m.docRefsOf(n, o), Findings: m.summaryOf(n),
		}
		if h := m.nodes[n.first("handled_by")]; h != nil {
			ref := m.reportRef(h, o)
			row.Handler = &ref
			if rep.Calls {
				tables, _ := m.tablesByCalls(h, true)
				for _, t := range tables {
					row.Tables = append(row.Tables, RouteTable{Table: m.reportRef(m.nodes[t.Table.ID], o), Access: t.Access, Confidence: t.Confidence})
				}
			}
		} else {
			row.LibHandler = n.Str("handler")
		}
		mws := m.out(n, "uses_mw")
		sort.Slice(mws, func(i, j int) bool { return mws[i].ID < mws[j].ID })
		row.Middleware = m.reportRefs(mws, o)
		if row.Dev {
			rep.DevRoutes++
		} else {
			rep.AppRoutes++
		}
		rep.Routes = append(rep.Routes, row)
	}
	sort.SliceStable(rep.Routes, func(i, j int) bool {
		a, b := rep.Routes[i], rep.Routes[j]
		if a.Dev != b.Dev {
			return !a.Dev
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Method < b.Method
	})
	return rep
}

// --- commands ---

// CommandsReport is the command table.
type CommandsReport struct {
	AsOf      string       `json:"as_of"`
	Notes     []string     `json:"notes,omitempty"`
	Total     int          `json:"total"`
	Scheduled int          `json:"scheduled"`
	Commands  []CommandRow `json:"commands"` // scheduled commands first, then by name
	Calls     bool         `json:"calls"`    // the call graph is indexed, so each command lists the tables it reaches
}

// CommandRow is one command.
type CommandRow struct {
	Command     ReportRef      `json:"command"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Scheduled   bool           `json:"scheduled"`
	Schedule    string         `json:"schedule,omitempty"`
	Timezone    string         `json:"timezone,omitempty"`
	Overlap     string         `json:"overlap,omitempty"`
	Timeout     string         `json:"timeout,omitempty"`
	Every       string         `json:"every,omitempty"`
	Handler     *ReportRef     `json:"handler,omitempty"`
	LibHandler  string         `json:"lib_handler,omitempty"` // what runs it when that is not application code
	Docs        []ReportRef    `json:"docs,omitempty"`
	Tables      []RouteTable   `json:"tables,omitempty"` // with the calls layer: what the function that runs it reaches
	Findings    FindingSummary `json:"findings"`
}

// CommandsReport builds the command table.
func (m *Model) CommandsReport(o LinkOptions) *CommandsReport {
	rep := &CommandsReport{AsOf: m.AsOf(), Notes: m.notesFor("commands", "code", "docs", "findings"), Commands: []CommandRow{}, Calls: m.hasCalls()}
	for _, n := range m.byKind["command"] {
		row := CommandRow{
			Command: m.reportRef(n, o), Name: n.Str("name"), Description: n.Str("description"),
			Scheduled: n.Bool("scheduled"), Schedule: n.Str("schedule"), Timezone: n.Str("timezone"),
			Overlap: n.Str("overlap"), Timeout: n.Str("timeout"), Every: n.Str("every"),
			Docs: m.docRefsOf(n, o), Findings: m.summaryOf(n),
		}
		if h := m.nodes[n.first("run_by")]; h != nil {
			ref := m.reportRef(h, o)
			row.Handler = &ref
			if rep.Calls {
				tables, _ := m.tablesByCalls(h, true)
				for _, t := range tables {
					row.Tables = append(row.Tables, RouteTable{Table: m.reportRef(m.nodes[t.Table.ID], o), Access: t.Access, Confidence: t.Confidence})
				}
			}
		} else {
			row.LibHandler = n.Str("handler")
		}
		rep.Total++
		if row.Scheduled {
			rep.Scheduled++
		}
		rep.Commands = append(rep.Commands, row)
	}
	sort.SliceStable(rep.Commands, func(i, j int) bool {
		a, b := rep.Commands[i], rep.Commands[j]
		if a.Scheduled != b.Scheduled {
			return a.Scheduled
		}
		return a.Name < b.Name
	})
	return rep
}

// --- config ---

// ConfigReport is the configuration reference: every key of the settings files and of
// the code, and the environment variables the code reads on its own.
type ConfigReport struct {
	AsOf        string      `json:"as_of"`
	Notes       []string    `json:"notes,omitempty"`
	Keys        []ConfigRow `json:"keys"`
	EnvOnly     []EnvRow    `json:"env_only"` // read in code through os.Getenv, with no config key behind them
	Undeclared  int         `json:"undeclared"`
	Sensitive   int         `json:"sensitive"`
	UnusedCount int         `json:"unused"`
}

// ConfigRow is one config key.
type ConfigRow struct {
	Key         ReportRef      `json:"key"`
	Name        string         `json:"name"`
	Env         *ReportRef     `json:"env,omitempty"`
	Default     string         `json:"default,omitempty"` // never for a sensitive key
	HasDefault  bool           `json:"has_default"`
	Sensitive   bool           `json:"sensitive"`
	Declared    bool           `json:"declared"`
	Field       string         `json:"field,omitempty"`
	UsedBy      []ReportRef    `json:"used_by,omitempty"` // the first few
	UsedByTotal int            `json:"used_by_total"`
	Docs        []ReportRef    `json:"docs,omitempty"`
	Findings    FindingSummary `json:"findings"`
}

// EnvRow is an environment variable the code reads directly.
type EnvRow struct {
	Env       ReportRef      `json:"env"`
	Name      string         `json:"name"`
	Declared  bool           `json:"declared"`
	Sensitive bool           `json:"sensitive"`
	ReadBy    []ReportRef    `json:"read_by,omitempty"`
	ReadTotal int            `json:"read_total"`
	Docs      []ReportRef    `json:"docs,omitempty"`
	Findings  FindingSummary `json:"findings"`
}

// maxUsedBy is how many users of a setting a row names; the rest are counted.
const maxUsedBy = 5

// declaredAttr reads `declared`, which only an explicit false takes away.
func declaredAttr(n *Node) bool { return !declaredFalse(n) }

// ConfigReport builds the configuration reference.
func (m *Model) ConfigReport(o LinkOptions) *ConfigReport {
	rep := &ConfigReport{AsOf: m.AsOf(), Notes: m.notesFor("config", "code", "docs", "findings"), Keys: []ConfigRow{}, EnvOnly: []EnvRow{}}
	for _, n := range m.byKind["config_key"] {
		sensitive := n.Bool("sensitive") || sensitiveRe.MatchString(n.Str("name"))
		row := ConfigRow{
			Key: m.reportRef(n, o), Name: n.Str("name"), Sensitive: sensitive, Declared: declaredAttr(n),
			Field: n.Str("field"), Docs: m.docRefsOf(n, o), Findings: m.summaryOf(n),
		}
		if !sensitive {
			if d := n.Str("default"); n.Has("default") {
				row.Default, row.HasDefault = d, true
			}
		}
		if envs := m.out(n, "reads_env"); len(envs) > 0 {
			ref := m.reportRef(envs[0], o)
			row.Env = &ref
		}
		users := readersOf(m.in(n, "uses_config"))
		row.UsedByTotal = len(users)
		if len(users) > maxUsedBy {
			users = users[:maxUsedBy]
		}
		row.UsedBy = m.reportRefs(users, o)
		if !row.Declared {
			rep.Undeclared++
		}
		if sensitive {
			rep.Sensitive++
		}
		if row.Declared && row.UsedByTotal == 0 {
			rep.UnusedCount++
		}
		rep.Keys = append(rep.Keys, row)
	}
	sort.SliceStable(rep.Keys, func(i, j int) bool { return rep.Keys[i].Name < rep.Keys[j].Name })

	for _, n := range m.byKind["env_var"] {
		keyed := false
		for _, r := range m.in(n, "reads_env") {
			keyed = keyed || r.Kind == "config_key"
		}
		if keyed {
			continue // listed with its key
		}
		var readers []*Node
		for _, r := range m.in(n, "reads_env") {
			if r.Kind == "func" {
				readers = append(readers, r)
			}
		}
		row := EnvRow{
			Env: m.reportRef(n, o), Name: n.Str("name"), Declared: declaredAttr(n),
			Sensitive: n.Bool("sensitive") || sensitiveRe.MatchString(n.Str("name")),
			ReadTotal: len(readers), Docs: m.docRefsOf(n, o), Findings: m.summaryOf(n),
		}
		if len(readers) > maxUsedBy {
			readers = readers[:maxUsedBy]
		}
		row.ReadBy = m.reportRefs(readers, o)
		if !row.Declared {
			rep.Undeclared++
		}
		rep.EnvOnly = append(rep.EnvOnly, row)
	}
	sort.SliceStable(rep.EnvOnly, func(i, j int) bool { return rep.EnvOnly[i].Name < rep.EnvOnly[j].Name })
	return rep
}

// --- schema ---

// SchemaReport is the database schema, from the migrations and, when one is
// configured, the live database.
type SchemaReport struct {
	AsOf       string         `json:"as_of"`
	Notes      []string       `json:"notes,omitempty"`
	Tables     []TableReport  `json:"tables"`
	Migrations []MigrationRow `json:"migrations"`
	Live       bool           `json:"live"` // a live database was read, so `source` means something
}

// TableReport is one table with everything the graph knows about it.
type TableReport struct {
	Table        ReportRef      `json:"table"`
	Name         string         `json:"name"`
	Source       string         `json:"source,omitempty"`
	Engine       string         `json:"engine,omitempty"`
	Comment      string         `json:"comment,omitempty"`
	IntroducedIn []ReportRef    `json:"introduced_in,omitempty"`
	Columns      []ColumnRow    `json:"columns"`
	Indexes      []IndexRow     `json:"indexes,omitempty"`
	References   []RelationRow  `json:"references,omitempty"`    // what this table points at: foreign keys, then inferred relations
	ReferencedBy []RelationRow  `json:"referenced_by,omitempty"` // what points at this table
	ReadBy       []ReportRef    `json:"read_by,omitempty"`
	WrittenBy    []ReportRef    `json:"written_by,omitempty"`
	MappedTo     []ReportRef    `json:"mapped_to,omitempty"` // the Go types whose db tags map to it
	Findings     FindingSummary `json:"findings"`            // about the table, its columns and its indexes
}

// ColumnRow is one column.
type ColumnRow struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type,omitempty"`
	Nullable   bool   `json:"nullable"`
	Key        string `json:"key,omitempty"`
	Default    string `json:"default,omitempty"` // as the graph has it: absent for a sensitive column
	HasDefault bool   `json:"has_default"`
	Extra      string `json:"extra,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Source     string `json:"source,omitempty"`
	Drift      string `json:"drift,omitempty"`
	Anchor     string `json:"anchor"`
}

// IndexRow is one index.
type IndexRow struct {
	Name     string `json:"name"`
	Columns  string `json:"columns,omitempty"`
	Unique   bool   `json:"unique"`
	Primary  bool   `json:"primary"`
	Implicit bool   `json:"implicit,omitempty"` // added by the server for a foreign key
	Source   string `json:"source,omitempty"`
	Drift    string `json:"drift,omitempty"`
}

// RelationRow is one edge between tables: a declared foreign key, or a relation
// the schema implies (with how sure the indexer is of it).
type RelationRow struct {
	Kind       string     `json:"kind"` // foreign_key, implicit_ref or many_to_many
	Inferred   bool       `json:"inferred"`
	Confidence int        `json:"confidence,omitempty"`
	Method     string     `json:"method,omitempty"`
	FromTable  ReportRef  `json:"from_table"`
	FromColumn string     `json:"from_column,omitempty"`
	ToTable    ReportRef  `json:"to_table"`
	ToColumn   string     `json:"to_column,omitempty"`
	Via        *ReportRef `json:"via,omitempty"` // the junction table of a many_to_many
	OnDelete   string     `json:"on_delete,omitempty"`
	OnUpdate   string     `json:"on_update,omitempty"`
	FKName     string     `json:"fk_name,omitempty"`
	OneToOne   bool       `json:"one_to_one,omitempty"`
}

// MigrationRow is one migration.
type MigrationRow struct {
	Migration ReportRef   `json:"migration"`
	Name      string      `json:"name"`
	Version   string      `json:"version,omitempty"`
	Applied   string      `json:"applied"` // yes, no or unknown (no live database was read)
	Tables    []ReportRef `json:"tables,omitempty"`
}

// columnName is the name of a column node.
func columnName(c *Node) string { return c.Str("name") }

// SchemaReport builds the schema reference.
func (m *Model) SchemaReport(o LinkOptions) *SchemaReport {
	rep := &SchemaReport{AsOf: m.AsOf(), Notes: m.notesFor("db", "code", "docs", "findings"), Tables: []TableReport{}, Migrations: []MigrationRow{}}
	rep.Live = (&ruleEnv{m: m}).liveKnown()

	for _, t := range m.byKind["table"] {
		tr := TableReport{
			Table: m.reportRef(t, o), Name: t.Str("name"), Source: t.Str("source"), Engine: t.Str("engine"), Comment: t.Str("comment"),
			Columns: []ColumnRow{},
		}
		for _, mg := range m.out(t, "introduced_in") {
			tr.IntroducedIn = append(tr.IntroducedIn, onPage(m.reportRef(mg, o)))
		}

		cols := m.out(t, "has_column")
		sort.SliceStable(cols, func(i, j int) bool {
			if a, b := cols[i].Int("ordinal"), cols[j].Int("ordinal"); a != b {
				return a < b
			}
			return cols[i].ID < cols[j].ID
		})
		about := []*Node{t}
		for _, c := range cols {
			cr := ColumnRow{
				Name: columnName(c), DataType: c.Str("data_type"), Nullable: c.Bool("nullable"), Key: c.Str("key"),
				Extra: c.Str("extra"), Comment: c.Str("comment"), Source: c.Str("source"), Drift: c.Str("drift"), Anchor: Anchor(c.ID),
			}
			if c.Has("default") && !sensitiveRe.MatchString(columnName(c)) {
				cr.Default, cr.HasDefault = c.Str("default"), true
			}
			tr.Columns = append(tr.Columns, cr)
			about = append(about, c)
		}

		idxs := m.out(t, "has_index")
		sort.SliceStable(idxs, func(i, j int) bool {
			if idxs[i].Bool("primary") != idxs[j].Bool("primary") {
				return idxs[i].Bool("primary")
			}
			return idxs[i].ID < idxs[j].ID
		})
		for _, ix := range idxs {
			tr.Indexes = append(tr.Indexes, IndexRow{
				Name: ix.Str("name"), Columns: ix.Str("columns"), Unique: ix.Bool("unique"), Primary: ix.Bool("primary"),
				Implicit: ix.Bool("implicit"), Source: ix.Str("source"), Drift: ix.Str("drift"),
			})
			about = append(about, ix)
		}

		tr.References = m.relationsFrom(t, cols, o)
		tr.ReferencedBy = m.relationsTo(t, cols, o)
		for _, f := range m.in(t, "reads_table") {
			tr.ReadBy = append(tr.ReadBy, m.reportRef(f, o))
		}
		for _, f := range m.in(t, "writes_table") {
			tr.WrittenBy = append(tr.WrittenBy, m.reportRef(f, o))
		}
		for _, ty := range m.in(t, "maps_to") {
			tr.MappedTo = append(tr.MappedTo, m.reportRef(ty, o))
		}
		tr.Findings = m.summaryOf(about...)
		rep.Tables = append(rep.Tables, tr)
	}
	sort.SliceStable(rep.Tables, func(i, j int) bool { return rep.Tables[i].Name < rep.Tables[j].Name })

	for _, mg := range m.byKind["migration"] {
		row := MigrationRow{Migration: m.reportRef(mg, o), Name: mg.Str("name"), Version: mg.Str("version"), Applied: "unknown"}
		for _, v := range mg.Attrs["applied"] {
			if b, ok := v.(bool); ok {
				row.Applied = map[bool]string{true: "yes", false: "no"}[b]
			}
		}
		for _, t := range m.in(mg, "introduced_in") {
			if t.Kind == "table" {
				row.Tables = append(row.Tables, onPage(m.reportRef(t, o)))
			}
		}
		sort.Slice(row.Tables, func(i, j int) bool { return row.Tables[i].ID < row.Tables[j].ID })
		rep.Migrations = append(rep.Migrations, row)
	}
	sort.SliceStable(rep.Migrations, func(i, j int) bool {
		if a, b := rep.Migrations[i].Version, rep.Migrations[j].Version; a != b {
			return a < b
		}
		return rep.Migrations[i].Name < rep.Migrations[j].Name
	})
	return rep
}

// relationsFrom lists what a table points at: its columns' foreign keys and
// implied references, then the many-to-many relations through a junction.
func (m *Model) relationsFrom(t *Node, cols []*Node, o LinkOptions) []RelationRow {
	var out []RelationRow
	for _, c := range cols {
		for _, pred := range []string{"fk_to", "implicit_ref"} {
			for _, target := range m.out(c, pred) {
				if target.Kind != "column" {
					continue // the table-level edge repeats the column-level one
				}
				out = append(out, m.columnRelation(pred, c, target, o))
			}
		}
	}
	for _, other := range m.out(t, "many_to_many") {
		a := m.annotationOf(t.ID, "many_to_many", other.ID)
		rel := RelationRow{
			Kind: "many_to_many", Inferred: true, Confidence: a.conf, Method: a.method,
			FromTable: onPage(m.reportRef(t, o)), ToTable: onPage(m.reportRef(other, o)),
		}
		if via := m.nodes[a.via]; via != nil {
			ref := onPage(m.reportRef(via, o))
			rel.Via = &ref
		}
		out = append(out, rel)
	}
	return out
}

// relationsTo lists what points at a table: foreign keys and implied references
// into any of its columns, and many-to-many relations arriving from other tables.
func (m *Model) relationsTo(t *Node, cols []*Node, o LinkOptions) []RelationRow {
	var out []RelationRow
	for _, c := range cols {
		for _, pred := range []string{"fk_to", "implicit_ref"} {
			for _, from := range m.in(c, pred) {
				if from.Kind != "column" {
					continue
				}
				out = append(out, m.columnRelation(pred, from, c, o))
			}
		}
	}
	for _, other := range m.in(t, "many_to_many") {
		a := m.annotationOf(other.ID, "many_to_many", t.ID)
		rel := RelationRow{
			Kind: "many_to_many", Inferred: true, Confidence: a.conf, Method: a.method,
			FromTable: onPage(m.reportRef(other, o)), ToTable: onPage(m.reportRef(t, o)),
		}
		if via := m.nodes[a.via]; via != nil {
			ref := onPage(m.reportRef(via, o))
			rel.Via = &ref
		}
		out = append(out, rel)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FromTable.ID != out[j].FromTable.ID {
			return out[i].FromTable.ID < out[j].FromTable.ID
		}
		return out[i].FromColumn < out[j].FromColumn
	})
	return out
}

// columnRelation describes a column-level edge (foreign key or implied reference).
func (m *Model) columnRelation(pred string, from, to *Node, o LinkOptions) RelationRow {
	rel := RelationRow{Kind: "foreign_key", FromColumn: columnName(from), ToColumn: columnName(to)}
	if t := m.tableNodeOf(from); t != nil {
		rel.FromTable = onPage(m.reportRef(t, o))
	}
	if t := m.tableNodeOf(to); t != nil {
		rel.ToTable = onPage(m.reportRef(t, o))
	}
	if pred == "implicit_ref" {
		a := m.annotationOf(from.ID, "implicit_ref", to.ID)
		rel.Kind, rel.Inferred, rel.Confidence, rel.Method = "implicit_ref", true, a.conf, a.method
		return rel
	}
	rel.OnDelete, rel.OnUpdate, rel.FKName = from.Str("on_delete"), from.Str("on_update"), from.Str("fk_name")
	rel.OneToOne = from.Str("cardinality") == "one_to_one"
	return rel
}

// --- layers ---

// LayersReport is the package layer map: the packages of each architecture layer,
// how the layers import each other, and the rule they are held to.
type LayersReport struct {
	AsOf       string         `json:"as_of"`
	Notes      []string       `json:"notes,omitempty"`
	Layers     []LayerRow     `json:"layers"`
	Matrix     LayerMatrix    `json:"matrix"`
	Principles []string       `json:"principles"` // the rule in words
	Rule       []LayerRule    `json:"rule"`       // what each layer must not import, and why
	Exempt     []string       `json:"exempt"`     // layers that may import anything
	Violations []ViolationRow `json:"violations"`
	// FindingsBuilt is false when the findings layer is not built: the matrix
	// still marks the imports that break the rule, but Violations is not a list
	// of what was checked.
	FindingsBuilt bool `json:"findings_built"`
}

// LayerRow is one architecture layer and its packages.
type LayerRow struct {
	Name     string       `json:"name"`
	Packages []PackageRow `json:"packages"`
}

// PackageRow is one package.
type PackageRow struct {
	Package  ReportRef      `json:"package"`
	Doc      string         `json:"doc,omitempty"`
	Funcs    int            `json:"funcs"`
	Imports  []ReportRef    `json:"imports,omitempty"` // packages of this module it imports, anchored on this page
	Findings FindingSummary `json:"findings"`
}

// LayerMatrix counts the imports between layers: rows import columns.
type LayerMatrix struct {
	Layers []string    `json:"layers"`
	Rows   []MatrixRow `json:"rows"`
}

// MatrixRow is the imports of one layer.
type MatrixRow struct {
	From  string       `json:"from"`
	Cells []MatrixCell `json:"cells"`
}

// MatrixCell counts the imports from one layer into another.
type MatrixCell struct {
	To        string `json:"to"`
	Count     int    `json:"count"`
	Same      bool   `json:"same,omitempty"`      // a layer importing itself
	Violation bool   `json:"violation,omitempty"` // the rule forbids these imports (whether or not any exist)
	Reason    string `json:"reason,omitempty"`
}

// LayerRule is what one layer must not import.
type LayerRule struct {
	From      string      `json:"from"`
	Forbidden []Forbidden `json:"forbidden"`
}

// Forbidden is one layer a layer must not import, and why.
type Forbidden struct {
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// ViolationRow is a `layering` finding with its place.
type ViolationRow struct {
	Finding   FindingRef `json:"finding"`
	Package   *ReportRef `json:"package,omitempty"`
	Path      string     `json:"path,omitempty"`
	Line      int        `json:"line,omitempty"`
	EditorURL string     `json:"editor_url,omitempty"`
}

// exemptLayers may import anything (the allow-list of the layering rule).
var exemptLayers = rules.Exempt

// layerPrinciples state the rule of layerVerdict in words, for the page. The pairs it
// forbids are derived from layerVerdict itself, so only these sentences can fall behind
// it (a test pins them to its verdicts).
var layerPrinciples = rules.Principles

// LayersReport builds the layer map.
func (m *Model) LayersReport(o LinkOptions) *LayersReport {
	rep := &LayersReport{AsOf: m.AsOf(), Notes: m.notesFor("code", "findings"), Layers: []LayerRow{}, Principles: layerPrinciples, Exempt: exemptLayers, Violations: []ViolationRow{}}
	rep.FindingsBuilt = m.layerBuilt("findings")

	byLayer := map[string][]*Node{}
	for _, p := range m.byKind["package"] {
		byLayer[p.Str("arch_layer")] = append(byLayer[p.Str("arch_layer")], p)
	}
	// The closed set first, in its order; anything else after, so no package is hidden.
	names := append([]string(nil), archLayers...)
	var extra []string
	for l := range byLayer {
		known := false
		for _, a := range archLayers {
			known = known || a == l
		}
		if !known {
			extra = append(extra, l)
		}
	}
	sort.Strings(extra)
	names = append(names, extra...)

	counts := map[[2]string]int{}
	for _, name := range names {
		pkgs := byLayer[name]
		if len(pkgs) == 0 {
			continue
		}
		sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Str("path") < pkgs[j].Str("path") })
		lr := LayerRow{Name: name}
		for _, p := range pkgs {
			funcs := 0
			for _, c := range m.codeIn(p) {
				if c.Kind == "func" {
					funcs++
				}
			}
			row := PackageRow{Package: m.reportRef(p, o), Doc: p.Str("doc"), Funcs: funcs, Findings: m.summaryOf(p)}
			imps := m.out(p, "imports")
			sort.Slice(imps, func(i, j int) bool { return imps[i].Str("path") < imps[j].Str("path") })
			for _, imp := range imps {
				row.Imports = append(row.Imports, onPage(m.reportRef(imp, o)))
				counts[[2]string{name, imp.Str("arch_layer")}]++
			}
			lr.Packages = append(lr.Packages, row)
		}
		rep.Layers = append(rep.Layers, lr)
	}

	for _, l := range rep.Layers {
		rep.Matrix.Layers = append(rep.Matrix.Layers, l.Name)
	}
	for _, from := range rep.Matrix.Layers {
		mr := MatrixRow{From: from}
		for _, to := range rep.Matrix.Layers {
			mr.Cells = append(mr.Cells, MatrixCell{
				To: to, Count: counts[[2]string{from, to}], Same: from == to,
				Violation: layerVerdict(from, to) != "", Reason: layerVerdict(from, to),
			})
		}
		rep.Matrix.Rows = append(rep.Matrix.Rows, mr)
	}
	// The rule, derived from the same function the findings use, so the page and
	// the findings cannot disagree about it.
	for _, from := range archLayers {
		lr := LayerRule{From: from}
		for _, to := range archLayers {
			if why := layerVerdict(from, to); why != "" {
				lr.Forbidden = append(lr.Forbidden, Forbidden{To: to, Reason: why})
			}
		}
		if len(lr.Forbidden) > 0 {
			rep.Rule = append(rep.Rule, lr)
		}
	}

	if rep.FindingsBuilt {
		res := m.Findings(FindingsQuery{Rule: "layering", Limit: maxFindingsLimit})
		for _, fr := range res.Findings {
			v := ViolationRow{Finding: fr}
			if p := m.nodes[fr.About]; p != nil {
				ref := m.reportRef(p, o)
				v.Package = &ref
			}
			if f := m.nodes[fr.ID]; f != nil {
				v.Path, v.Line = f.Str("path"), f.Int("line")
				v.EditorURL = EditorURL(o.Root, v.Path, v.Line)
			}
			rep.Violations = append(rep.Violations, v)
		}
	}
	return rep
}

// ReportInfo describes one report for the index page.
type ReportInfo struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	What  string `json:"what"`
	URL   string `json:"url"`
}

// Reports lists the generated reports.
func Reports() []ReportInfo {
	info := map[string][2]string{
		"routes":   {"Routes", "Every HTTP route the running router serves: method, path, handler, middleware, and the docs that mention it."},
		"commands": {"Commands and schedules", "Every CLI command of the application: what runs it, how and when it is scheduled (cron spec, zone, overlap, timeout), the tables it reaches, and the docs that mention it."},
		"config":   {"Config and environment", "Every config key and environment variable, with its default (never a secret), what reads it, and what is declared where."},
		"schema":   {"Database schema", "Tables, columns, indexes and foreign keys from the migrations (and the live database, when configured), who reads and writes each table, and the relations the schema implies."},
		"layers":   {"Package layers", "The packages of each architecture layer, how the layers import each other, and the layering rule with its violations."},
	}
	out := make([]ReportInfo, 0, len(ReportNames))
	for _, n := range ReportNames {
		out = append(out, ReportInfo{Name: n, Title: info[n][0], What: info[n][1], URL: "/dev/graph/reports/" + n})
	}
	return out
}

// ReportTitle is the heading of a report, "" for an unknown name.
func ReportTitle(name string) string {
	for _, r := range Reports() {
		if r.Name == strings.ToLower(name) {
			return r.Title
		}
	}
	return ""
}
