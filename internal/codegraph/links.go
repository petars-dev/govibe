package codegraph

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Where a node can be opened: in the editor, on its docs page, in the database
// explorer, on a generated report, in the graph explorer. The inspector of the
// graph page and the reports both use these, so a node is reached the same way
// from either.

// ReportNames are the generated reference pages under /dev/graph/reports, in the
// order the index lists them.
var ReportNames = []string{"routes", "config", "schema", "layers"}

// Link is a place a node can be opened.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Kind  string `json:"kind"` // editor, docs, db, report or graph
}

// LinkOptions say what the running server offers.
type LinkOptions struct {
	// Root is the absolute repository root, for editor links. Empty: no editor links.
	Root string
	// DB is whether the database explorer (/dev/db) is mounted: it needs a DSN.
	DB bool
}

// Anchor is the HTML id of a node on a report page, and so the fragment that
// links to it: stable, readable, and safe in a URL whatever the node's ID holds.
func Anchor(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if s := b.String(); s != "" && !strings.HasSuffix(s, "-") {
				b.WriteByte('-')
			}
		}
	}
	// Two IDs can fold to the same words (`/a-b` and `/a/b`), so the exact ID is
	// part of the anchor.
	return strings.Trim(b.String(), "-") + "-" + shortHash(id)
}

func shortHash(s string) string {
	var h uint32 = 2166136261 // FNV-1a
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return fmt.Sprintf("%06x", h&0xffffff)
}

// EditorURL opens a repository file at a line in VS Code. rel is
// repository-relative; "" gives "".
func EditorURL(root, rel string, line int) string {
	if root == "" || rel == "" {
		return ""
	}
	u := (&url.URL{Scheme: "vscode", Host: "file", Path: filepath.ToSlash(filepath.Join(root, filepath.FromSlash(rel)))}).String()
	if line > 0 {
		u += fmt.Sprintf(":%d", line)
	}
	return u
}

// GraphURL opens a node in the graph explorer.
func GraphURL(id string) string { return "/dev/graph#node=" + url.QueryEscape(id) }

// graphURL opens a node in the graph explorer in the scope the model was read in, so a
// link from a page that shows the framework's nodes does not land where they are hidden.
// The default scope (the app's) is not spelled out.
func (m *Model) graphURL(id string) string {
	if m.explicit && m.scope != ScopeApp {
		return "/dev/graph?scope=" + string(m.scope) + "#node=" + url.QueryEscape(id)
	}
	return GraphURL(id)
}

// ReportURL is the page of a report, and the node's place on it when id is set.
func ReportURL(name, id string) string {
	u := "/dev/graph/reports/" + name
	if id != "" {
		u += "#" + Anchor(id)
	}
	return u
}

// Links lists where n can be opened. The first is always the graph explorer.
func (m *Model) Links(n *Node, o LinkOptions) []Link {
	links := []Link{{Label: "Show in graph", URL: m.graphURL(n.ID), Kind: "graph"}}
	add := func(label, u, kind string) {
		if u != "" {
			links = append(links, Link{Label: label, URL: u, Kind: kind})
		}
	}

	// the editor: the file and line of the thing itself, or of what handles it
	target := n
	if n.Kind == "route" {
		if h := m.nodes[n.first("handled_by")]; h != nil {
			target = h
		}
	}
	ref := m.Ref(target)
	switch target.Kind {
	case "package", "file", "func", "type", "template", "mcp_tool", "config_key", "doc", "spec", "spec_item", "migration":
		add("Open in editor", EditorURL(o.Root, ref.Path, ref.Line), "editor")
	}

	// the docs pages
	switch n.Kind {
	case "spec":
		add("Open the spec", "/dev/docs/specs/"+strings.TrimPrefix(n.ID, "spec:"), "docs")
	case "spec_item":
		if s := m.in(n, "has_item"); len(s) > 0 {
			add("Open the spec", "/dev/docs/specs/"+strings.TrimPrefix(s[0].ID, "spec:"), "docs")
		}
	case "doc":
		if p := n.Str("path"); strings.HasPrefix(p, "docs/public/") {
			add("Open the doc", "/dev/docs/app/"+slugify(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))), "docs")
		}
	}

	// the database explorer, when there is one
	if o.DB {
		if t := m.tableNameOf(n); t != "" {
			add("Open in the database explorer", "/dev/db#table="+url.QueryEscape(t), "db")
		}
	}

	// the generated reference pages
	switch n.Kind {
	case "route":
		add("Routes report", ReportURL("routes", n.ID), "report")
	case "config_key", "env_var":
		add("Config report", ReportURL("config", n.ID), "report")
	case "table":
		add("Schema report", ReportURL("schema", n.ID), "report")
	case "column", "index":
		if t := m.tableNodeOf(n); t != nil {
			add("Schema report", ReportURL("schema", t.ID), "report")
		}
	case "migration":
		add("Schema report", ReportURL("schema", n.ID), "report")
	case "package":
		add("Layers report", ReportURL("layers", n.ID), "report")
	}
	return links
}

// tableNodeOf is the table a table, column or index belongs to.
func (m *Model) tableNodeOf(n *Node) *Node {
	switch n.Kind {
	case "table":
		return n
	case "column", "index":
		for _, p := range []string{"has_column", "has_index"} {
			for _, t := range m.in(n, p) {
				if t.Kind == "table" {
					return t
				}
			}
		}
	}
	return nil
}

// tableNameOf is the name of the table a node is, or belongs to; "" for anything else.
func (m *Model) tableNameOf(n *Node) string {
	if t := m.tableNodeOf(n); t != nil {
		return t.Str("name")
	}
	return ""
}
