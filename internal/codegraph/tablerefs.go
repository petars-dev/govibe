package codegraph

import "fmt"

// What refers to a table: the code that reads or writes it, the routes and tools
// that may reach it, and the docs and specs that mention it. The database
// explorer shows it beside the table, so someone looking at a table sees what
// depends on it without leaving the page. It is graph_impact of the table,
// grouped for a person, and it keeps that tool's honesty: a route listed through
// a package import is possible, not proven, and says so.

// RefItem is one thing that refers to a table.
type RefItem struct {
	NodeRef
	Access     string `json:"access,omitempty"` // read, write or read+write
	Confidence int    `json:"confidence"`
	Method     string `json:"method"`             // edge, sql_literal, db_tag, package_imports, ...
	Certain    bool   `json:"certain"`            // a stored edge, not a package-level estimate
	Editor     string `json:"editor,omitempty"`   // vscode:// link to the file and line
	Graph      string `json:"graph"`              // link to the node in the graph explorer
	Spec       string `json:"spec,omitempty"`     // for a spec row: its spec
	Schedule   string `json:"schedule,omitempty"` // for a scheduled command: how it is scheduled
	Mentions   string `json:"mentions,omitempty"` // for a doc: what of the table it names
}

// TableRefs is what refers to one table.
type TableRefs struct {
	Table    NodeRef      `json:"table"`
	Known    bool         `json:"known"` // the graph has this table
	Code     []RefItem    `json:"code"`
	Routes   []RefItem    `json:"routes"`
	Commands []RefItem    `json:"commands"` // the application's commands that reach it; a scheduled one carries its schedule in Schedule
	Tools    []RefItem    `json:"tools"`
	Docs     []RefItem    `json:"docs"`
	Findings []FindingRef `json:"findings"`
	Links    []Link       `json:"links"`
	Notes    []string     `json:"notes,omitempty"`
	AsOf     string       `json:"as_of"`
}

// maxRefsPerGroup bounds each group; the report and the graph hold the rest.
const maxRefsPerGroup = 30

// TableRefs gathers what refers to a table (`items` or `table:items`).
func (m *Model) TableRefs(table string, o LinkOptions) (*TableRefs, error) {
	res := &TableRefs{Code: []RefItem{}, Routes: []RefItem{}, Commands: []RefItem{}, Tools: []RefItem{}, Docs: []RefItem{}, Findings: []FindingRef{}, Links: []Link{}, AsOf: m.AsOf()}
	found := m.ResolveTable(table)
	switch len(found) {
	case 0:
		res.Table = NodeRef{ID: "table:" + table, Kind: "table", Name: table}
		res.Notes = []string{fmt.Sprintf("The graph does not know a table %q: it is not in the migrations or in the last schema the indexer read.", table)}
		if m.Empty() {
			res.Notes = []string{"The graph has not been indexed yet."}
		}
		return res, nil
	case 1:
	default:
		return nil, fmt.Errorf("%d tables answer to %q (they differ in case): %v", len(found), table, sortedIDs(found))
	}
	t := found[0]
	res.Table, res.Known = m.Ref(t), true
	res.Links = m.Links(t, o)
	for _, f := range m.findingNodesAbout(t.ID) {
		res.Findings = append(res.Findings, m.findingRef(f))
	}
	sortFindings(res.Findings)

	imp, err := m.Impact(ImpactQuery{Node: t.ID, Depth: 1, Limit: maxImpactLimit})
	if err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, imp.Notes...)
	affected := imp.Affected
	if m.hasCalls() {
		// With call edges an entry point is several hops from the table (table, store,
		// service, handler, route), so a walk of one hop finds the code that touches the
		// table and none of what reaches it. Entry points come from the walk the calls layer
		// makes possible; the code group keeps its one hop (what reads or writes the table
		// itself, not everything that calls it).
		var kinds []string
		for _, k := range []string{"route", "command", "mcp_tool"} {
			if len(m.byKind[k]) > 0 {
				kinds = append(kinds, k)
			}
		}
		if len(kinds) > 0 {
			deep, err := m.Impact(ImpactQuery{Node: t.ID, Kinds: kinds, Limit: maxImpactLimit})
			if err != nil {
				return nil, err
			}
			kept := affected[:0:0]
			for _, a := range affected {
				if a.Kind != "route" && a.Kind != "command" && a.Kind != "mcp_tool" {
					kept = append(kept, a)
				}
			}
			affected = append(kept, deep.Affected...)
		}
	}
	item := func(a NodeRef, conf int, method, access string) RefItem {
		it := RefItem{NodeRef: a, Access: access, Confidence: conf, Method: method, Certain: conf >= 90, Graph: m.graphURL(a.ID)}
		if a.Path != "" {
			it.Editor = EditorURL(o.Root, a.Path, a.Line)
		}
		return it
	}
	add := func(list *[]RefItem, it RefItem) {
		if len(*list) < maxRefsPerGroup {
			*list = append(*list, it)
		}
	}
	for _, a := range affected {
		it := item(a.NodeRef, a.Confidence, a.Method, a.Access)
		switch a.Kind {
		case "func", "type":
			add(&res.Code, it)
		case "route":
			add(&res.Routes, it)
		case "command":
			if n := m.nodes[a.ID]; n != nil {
				it.Schedule = n.Str("schedule")
			}
			add(&res.Commands, it)
		case "mcp_tool":
			add(&res.Tools, it)
		}
	}
	for _, d := range imp.Docs {
		it := item(d.NodeRef, 100, "mentions", "")
		it.Spec = d.Spec
		if len(d.Mentions) > 0 {
			it.Mentions = d.Mentions[0]
		}
		add(&res.Docs, it)
	}
	return res, nil
}
