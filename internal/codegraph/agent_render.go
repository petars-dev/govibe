package codegraph

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Two rendered views of the graph, for agents that read documents rather than
// call tools: the architecture overview (an MCP resource) and the briefing for a
// change (the explain-change prompt). Both are generated from the model, so they
// cannot drift from the code the way a hand-kept page does.

// --- overview ---

const (
	overviewPackagesPerLayer = 12
	overviewRoutes           = 60
)

// overviewFindings is how many findings the overview lists (the rest are counted).
const overviewFindings = 12

// Overview renders the architecture of the project as markdown.
func (m *Model) Overview(stale []string) string {
	var b strings.Builder
	f := m.Freshness()
	fmt.Fprintf(&b, "# Project graph overview\n\n")
	if !f.Indexed {
		b.WriteString("The graph has not been indexed yet. Call `graph_reindex`, or retry in a few seconds after a boot.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "As of %s · %d quads · layers: %s\n\n", m.AsOf(), f.Quads, layerSummary(f.Layers))
	if len(stale) > 0 {
		fmt.Fprintf(&b, "> Stale layers: %s (graph_reindex rebuilds them).\n\n", strings.Join(stale, ", "))
	}

	// architecture layers and their packages
	b.WriteString("## Architecture layers\n\n")
	byLayer := map[string][]*Node{}
	for _, p := range m.byKind["package"] {
		byLayer[p.Str("arch_layer")] = append(byLayer[p.Str("arch_layer")], p)
	}
	for _, al := range archLayers {
		pkgs := byLayer[al]
		if len(pkgs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "**%s**\n", al)
		for i, p := range pkgs {
			if i == overviewPackagesPerLayer {
				fmt.Fprintf(&b, "- … and %d more\n", len(pkgs)-i)
				break
			}
			funcs := 0
			for _, c := range m.codeIn(p) {
				if c.Kind == "func" {
					funcs++
				}
			}
			line := fmt.Sprintf("- `%s` (%d %s)", p.Str("path"), funcs, plural(funcs, "func", "funcs"))
			if d := p.Str("doc"); d != "" {
				line += ": " + oneLine(d, 110)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// how the layers import each other
	type pair struct{ from, to string }
	counts := map[pair]int{}
	var violations []string
	for _, p := range m.byKind["package"] {
		for _, imp := range m.out(p, "imports") {
			from, to := p.Str("arch_layer"), imp.Str("arch_layer")
			if from == to {
				continue
			}
			counts[pair{from, to}]++
			if layerVerdict(from, to) != "" {
				violations = append(violations, fmt.Sprintf("`%s` imports `%s`", p.Str("path"), imp.Str("path")))
			}
		}
	}
	if len(counts) > 0 {
		b.WriteString("## Imports between layers\n\n")
		for _, from := range archLayers {
			var parts []string
			for _, to := range archLayers {
				if n := counts[pair{from, to}]; n > 0 {
					parts = append(parts, fmt.Sprintf("%s (%d)", to, n))
				}
			}
			if len(parts) > 0 {
				fmt.Fprintf(&b, "- %s → %s\n", from, strings.Join(parts, ", "))
			}
		}
		b.WriteString("\n")
		if len(violations) > 0 && !m.layerBuilt("findings") {
			// With the findings layer these are its `layering` findings, listed below.
			sort.Strings(violations)
			b.WriteString("Against the layering rule (only business may call storage; storage depends on nothing above it; wiring and dev packages are exempt):\n")
			for _, v := range violations {
				b.WriteString("- " + v + "\n")
			}
			b.WriteString("\n")
		}
	}

	m.overviewFindings(&b)

	// routes
	routes := append([]*Node(nil), m.byKind["route"]...)
	if len(routes) > 0 {
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
		b.WriteString("## Routes\n\n| Method | Path | Handler | Middleware |\n| --- | --- | --- | --- |\n")
		for i, r := range routes {
			if i == overviewRoutes {
				fmt.Fprintf(&b, "| … | %d more | | |\n", len(routes)-i)
				break
			}
			handler := r.Str("handler")
			if h := m.nodes[r.first("handled_by")]; h != nil {
				ref := m.Ref(h)
				handler = fmt.Sprintf("`%s` (%s:%d)", strings.TrimPrefix(h.ID, "func:"), ref.Path, ref.Line)
			} else if handler != "" {
				handler = "(" + handler + ")"
			}
			dev := ""
			if r.Bool("dev_only") {
				dev = " (dev)"
			}
			fmt.Fprintf(&b, "| %s | `%s`%s | %s | %d |\n", r.Str("method"), r.Str("path"), dev, handler, len(r.Out["uses_mw"]))
		}
		b.WriteString("\n")
	} else if st := m.layerStatus("routes"); st != statusOK && st != statusPartial {
		b.WriteString("## Routes\n\nNot available: the routes layer is " + orNone(st, "not built") + ".\n\n")
	}

	// MCP tools
	if tools := m.byKind["mcp_tool"]; len(tools) > 0 {
		b.WriteString("## MCP tools\n\n")
		for _, t := range tools {
			reg := ""
			if fs := m.in(t, "registers"); len(fs) > 0 {
				reg = " — registered by `" + strings.TrimPrefix(fs[0].ID, "func:") + "`"
			}
			fmt.Fprintf(&b, "- `%s`%s: %s\n", t.Str("name"), reg, oneLine(t.Str("description"), 120))
		}
		b.WriteString("\n")
	}

	// database
	b.WriteString("## Database\n\n")
	if tables := m.byKind["table"]; len(tables) == 0 {
		b.WriteString("No tables: the migrations create none yet, and no database schema was read.\n\n")
	} else {
		for _, t := range tables {
			var rel []string
			for _, o := range m.out(t, "fk_to") {
				rel = append(rel, "→ "+o.Str("name"))
			}
			for _, o := range m.out(t, "implicit_ref") {
				conf, _ := m.annConf(t.ID, "implicit_ref", o.ID)
				rel = append(rel, fmt.Sprintf("→ %s (implicit, %d%%)", o.Str("name"), conf))
			}
			line := fmt.Sprintf("- `%s` (%d columns)", t.Str("name"), len(t.Out["has_column"]))
			if len(rel) > 0 {
				line += " " + strings.Join(rel, ", ")
			}
			readers, writers := len(t.In["reads_table"]), len(t.In["writes_table"])
			line += fmt.Sprintf("; read by %d, written by %d %s", readers, writers, plural(readers+writers, "function", "functions"))
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// config
	if keys := m.byKind["config_key"]; len(keys) > 0 {
		undeclared, sensitive := 0, 0
		for _, k := range keys {
			if !k.Bool("declared") {
				undeclared++
			}
			if k.Bool("sensitive") {
				sensitive++
			}
		}
		fmt.Fprintf(&b, "## Config\n\n%d keys in `config/env.yml` and the code (%d sensitive, %d read in code but not declared), %d environment variables.\n\n",
			len(keys), sensitive, undeclared, len(m.byKind["env_var"]))
	}

	// docs and specs
	if specs := m.byKind["spec"]; len(specs) > 0 {
		b.WriteString("## Specs\n\n")
		for _, s := range specs {
			status := map[string]int{}
			for _, it := range m.out(s, "has_item") {
				status[it.Str("status")]++
			}
			var parts []string
			for _, st := range []string{"implemented", "partial", "planned", "skipped"} {
				if status[st] > 0 {
					parts = append(parts, fmt.Sprintf("%d %s", status[st], st))
				}
			}
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", s.Str("path"), s.Str("status"), strings.Join(parts, ", "))
		}
		b.WriteString("\n")
	}

	// what is missing
	var notes []string
	for _, ol := range optInLayers {
		if !m.layerBuilt(ol.name) {
			notes = append(notes, fmt.Sprintf("the %s layer is off: %s", ol.name, ol.enable))
		}
	}
	notes = append(notes, m.warnings("code", "routes", "templates", "config", "mcp", "db", "docs", "findings")...)
	if len(notes) > 0 {
		b.WriteString("## Not covered\n\n")
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

// overviewFindings lists the errors and warnings of the findings layer, and
// counts the rest. Nothing is written when the layer is not built: the overview's
// "Not covered" section says so.
func (m *Model) overviewFindings(b *strings.Builder) {
	if !m.layerBuilt("findings") {
		return
	}
	res := m.Findings(FindingsQuery{Limit: maxFindingsLimit})
	b.WriteString("## Findings\n\n")
	var parts []string
	for _, sev := range []string{sevError, sevWarning, sevInfo} {
		if n := res.BySeverity[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, sev))
		}
	}
	if len(parts) == 0 {
		b.WriteString("None: every rule that could run found nothing.")
	} else {
		b.WriteString(strings.Join(parts, ", ") + ".")
	}
	if unchecked := res.uncheckedRules(); len(unchecked) > 0 {
		b.WriteString(" Not checked: " + strings.Join(unchecked, ", ") + ".")
	}
	b.WriteString(" `graph_findings` lists them.\n\n")
	shown := 0
	for _, f := range res.Findings {
		if severityRank(f.Severity) > 1 {
			break // info is counted, not listed
		}
		if shown == overviewFindings {
			fmt.Fprintf(b, "- … and more errors and warnings\n")
			break
		}
		fmt.Fprintf(b, "- **%s** `%s`: %s\n", f.Severity, f.Rule, oneLine(f.Message, 200))
		shown++
	}
	if shown > 0 {
		b.WriteString("\n")
	}
}

func layerSummary(ls []LayerInfo) string {
	var parts []string
	for _, l := range ls {
		parts = append(parts, l.Name+" "+l.Status)
	}
	return strings.Join(parts, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func orNone(s, none string) string {
	if s == "" {
		return none
	}
	return s
}

// --- explain-change ---

// maxChangedPaths bounds how many files of a change are analysed.
const maxChangedPaths = 15

var diffHeader = regexp.MustCompile(`(?m)^(?:diff --git a/(\S+) b/(\S+)|\+\+\+ b/(\S+))$`)

// ChangedPaths collects the repository paths of a change: the ones given
// (separated by commas or newlines) and the ones a unified diff names. Deleted
// files, which a diff shows as /dev/null, are found through their `diff --git`
// line.
func ChangedPaths(paths, diff string) []string {
	set := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "a/")
		if p != "" && p != "/dev/null" {
			set[p] = true
		}
	}
	for _, p := range strings.FieldsFunc(paths, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		add(p)
	}
	for _, g := range diffHeader.FindAllStringSubmatch(diff, -1) {
		for _, p := range g[1:] {
			if p != "" {
				add(p)
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ExplainChange renders the briefing of a change: what each changed file is,
// what it affects and how sure the graph is, which docs mention it, and which
// docs the project rule says to update (SKILL.md: after each development loop,
// the spec, and ARCHITECTURE.md or MEMORY_SCHEMA.md when structure or data
// shapes changed).
func (m *Model) ExplainChange(paths, diff string) (string, error) {
	changed := ChangedPaths(paths, diff)
	if len(changed) == 0 {
		return "", fmt.Errorf("give the changed paths (comma or newline separated) or a unified diff")
	}
	var b strings.Builder
	var unknown []string
	var resolved []*Node
	hits := map[string]DocHit{}
	specRows := map[string][]string{} // spec path -> the rows that cover the change ("" for the spec alone)
	var tables []string
	migration, codegraphChanged, structural := false, false, false

	shown := changed
	if len(shown) > maxChangedPaths {
		shown = shown[:maxChangedPaths]
	}
	fmt.Fprintf(&b, "# Explain this change\n\nGraph as of %s. %d changed path(s)", m.AsOf(), len(changed))
	if len(changed) > len(shown) {
		fmt.Fprintf(&b, " (the first %d analysed)", len(shown))
	}
	b.WriteString(".\n\n## What changed and what it affects\n\n")

	for _, p := range shown {
		switch {
		case strings.HasPrefix(p, "storage/migrations/") || strings.HasSuffix(p, ".sql"):
			migration = true
		case strings.HasPrefix(p, "internal/codegraph/") || strings.HasPrefix(p, "internal/devmcp/graph"):
			codegraphChanged = true
		}
		if (strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".templ")) && inLayeredDir(p) {
			structural = true
		}
		nodes := m.ResolvePath(p)
		if len(nodes) == 0 {
			unknown = append(unknown, p)
			continue
		}
		n := nodes[0]
		resolved = append(resolved, n)
		m.explainOne(&b, p, n, hits, specRows, &tables)
	}
	if len(resolved) == 0 {
		b.WriteString("None of the changed paths is in the graph.\n\n")
	}
	if len(unknown) > 0 {
		b.WriteString("Not in the graph:\n")
		for _, p := range unknown {
			b.WriteString("- `" + p + "`: " + notIndexedReason(p) + "\n")
			structural = structural || strings.HasSuffix(p, ".go")
		}
		b.WriteString("\n")
	}

	// docs that mention what changed
	b.WriteString("## Docs and specs that mention the changed code\n\n")
	if len(hits) == 0 {
		b.WriteString("None.\n\n")
	} else {
		list := make([]DocHit, 0, len(hits))
		for _, h := range hits {
			list = append(list, h)
		}
		sort.Slice(list, func(i, j int) bool {
			if len(list[i].Mentions) != len(list[j].Mentions) {
				return len(list[i].Mentions) > len(list[j].Mentions)
			}
			return list[i].ID < list[j].ID
		})
		for i, h := range list {
			if i == 15 {
				fmt.Fprintf(&b, "- … and %d more\n", len(list)-i)
				break
			}
			what := h.Path
			if h.Kind == "spec_item" {
				what = fmt.Sprintf("%s, row “%s” (%s)", h.Path, h.Name, orNone(h.Status, "no status"))
			}
			fmt.Fprintf(&b, "- `%s`: %s — mentions %s\n", h.ID, what, strings.Join(shortIDs(h.Mentions), ", "))
		}
		b.WriteString("\n")
	}

	// the project rule
	b.WriteString("## Docs to update\n\nThe project rule (SKILL.md, Spec-Driven Development): after each development loop, update the spec with progress and deviations, and ARCHITECTURE.md or MEMORY_SCHEMA.md if architecture or data shapes changed.\n\n")
	if len(specRows) > 0 {
		var parts []string
		for sp, rows := range specRows {
			part := "`" + sp + "`"
			if rows = uniqueStrings(rows); len(rows) > 0 {
				part += " rows " + strings.Join(rows, ", ")
			}
			parts = append(parts, part)
		}
		sort.Strings(parts)
		b.WriteString("- [ ] Spec status table: " + strings.Join(parts, "; ") + " (what covers or mentions the changed code)\n")
	} else {
		b.WriteString("- [ ] A spec: no spec row covers the changed code. Add or adjust the row in the relevant `docs/specs/SPEC_*.md` status table.\n")
	}
	arch := "- [ ] `docs/public/ARCHITECTURE.md`: if the change adds, moves or removes a package, route, layer or dev tool"
	if structural {
		arch += " (code under the layered directories changed; check)"
	}
	b.WriteString(arch + ".\n")
	mem := "- [ ] `docs/public/MEMORY_SCHEMA.md`: if a migration or a data shape changed"
	if migration || len(tables) > 0 {
		mem += " (a migration or table is involved: " + orNone(strings.Join(uniqueStrings(tables), ", "), "see the migration") + "; likely)"
	}
	b.WriteString(mem + ".\n")
	if codegraphChanged {
		b.WriteString("- [ ] `docs/public/GRAPH_SCHEMA.md` and `docs/specs/SPEC_GRAPH_INDEXER.md`: the indexer or its vocabulary changed.\n")
	}

	b.WriteString("\n## Your task\n\nExplain, in plain language, what this change affects: which routes, tools, tables and packages, and how sure the graph is (confidence 100 is a stored edge; 50 and below is a package-level estimate: possible, not proven). " +
		"Use `graph_context` and `graph_impact` on anything above that needs more detail, and `graph_path` to see how a route reaches a table. " +
		"Then list the doc edits needed, as a checklist, citing node IDs.\n")
	return b.String(), nil
}

// explainOne writes the briefing of one changed path.
func (m *Model) explainOne(b *strings.Builder, p string, n *Node, hits map[string]DocHit, specRows map[string][]string, tables *[]string) {
	res, err := m.Impact(ImpactQuery{Node: n.ID, Limit: 60})
	if err != nil {
		fmt.Fprintf(b, "### `%s`\n\nCould not analyse: %v\n\n", p, err)
		return
	}
	fmt.Fprintf(b, "### `%s` — %s `%s`", p, n.Kind, n.ID)
	if pkg := m.pkgOf(n); pkg != nil && n.Kind != "package" {
		fmt.Fprintf(b, " in `%s` (%s)", pkg.Str("path"), pkg.Str("arch_layer"))
	}
	b.WriteString("\n\n")

	scope := m.scopeOf(n)
	var defs []string
	for _, ns := range [][]*Node{scope.funcs, scope.types, scope.templates} {
		for _, d := range ns {
			defs = append(defs, d.Str("name"))
		}
	}
	if len(defs) > 0 {
		sort.Strings(defs)
		fmt.Fprintf(b, "- Defines: %s\n", listCapped(defs, 12))
	}

	var exact, possible []Affected
	for _, a := range res.Affected {
		if a.Confidence >= 90 {
			exact = append(exact, a)
		} else {
			possible = append(possible, a)
		}
	}
	describe := func(as []Affected) string {
		by := map[string][]string{}
		for _, a := range as {
			by[a.Kind] = append(by[a.Kind], shortID(a.ID))
		}
		var parts []string
		for _, k := range []string{"route", "mcp_tool", "func", "type", "template", "middleware", "table", "column", "config_key", "env_var", "package"} {
			if l := by[k]; len(l) > 0 {
				parts = append(parts, fmt.Sprintf("%s: %s", k, listCapped(l, 8)))
			}
		}
		return strings.Join(parts, "; ")
	}
	if len(exact) > 0 {
		fmt.Fprintf(b, "- Affected for certain (stored edges, confidence ≥ 90): %s\n", describe(exact))
	} else {
		b.WriteString("- Nothing affected through a stored edge.\n")
	}
	if len(possible) > 0 {
		fmt.Fprintf(b, "- Possibly affected (package-level estimate, confidence ≤ 60, not proven): %s\n", describe(possible))
	}
	for _, a := range res.Affected {
		if a.Kind == "table" {
			*tables = append(*tables, a.ID)
		}
	}
	if n.Kind == "table" || n.Kind == "migration" {
		*tables = append(*tables, n.ID)
	}
	if scope.funcs != nil {
		var touches []string
		for _, f := range scope.funcs {
			for _, t := range f.Out["reads_table"] {
				touches = append(touches, shortID(t)+" (read)")
			}
			for _, t := range f.Out["writes_table"] {
				touches = append(touches, shortID(t)+" (write)")
			}
		}
		if len(touches) > 0 {
			fmt.Fprintf(b, "- Tables it touches directly: %s\n", listCapped(uniqueStrings(touches), 8))
			for _, f := range scope.funcs {
				*tables = append(*tables, f.Out["reads_table"]...)
				*tables = append(*tables, f.Out["writes_table"]...)
			}
		}
	}
	b.WriteString("\n")

	for _, d := range res.Docs {
		if prev, ok := hits[d.ID]; ok {
			d.Mentions = append(prev.Mentions, d.Mentions...)
		}
		if d.Mentions = uniqueInOrder(d.Mentions); len(d.Mentions) > 4 {
			d.Mentions = d.Mentions[:4]
		}
		hits[d.ID] = d
		switch d.Kind {
		case "spec_item":
			specRows[d.Path] = append(specRows[d.Path], "“"+d.Name+"”")
		case "spec":
			if _, ok := specRows[d.Path]; !ok {
				specRows[d.Path] = nil
			}
		}
	}
}

// inLayeredDir reports whether a path is under one of the directories that
// make up the architecture (SKILL.md, Directory Layout).
func inLayeredDir(p string) bool {
	top, _, _ := strings.Cut(path.Clean(p), "/")
	switch top {
	case "routes", "controllers", "business", "middleware", "storage", "devmcp", "cmd", "assets",
		"internal", "config", "web", "govibetest": // the framework's own: its public packages and everything under internal/
		return true
	}
	return false
}

func notIndexedReason(p string) string {
	switch {
	case strings.HasSuffix(p, "_test.go"):
		return "test files are not indexed"
	case strings.HasSuffix(p, "_templ.go"):
		return "generated templ output is skipped"
	case strings.HasSuffix(p, ".go"):
		return "a new or deleted Go file, or one the code layer skips (testdata, vendor, a nested module); a new file shows up after the next index run"
	case strings.HasSuffix(p, ".md"):
		return "a markdown file outside docs/public, docs/specs, README.md and SKILL.md"
	case strings.HasSuffix(p, ".sql"):
		return "a migration outside storage/migrations/sql, or a new one the db layer has not read yet"
	}
	return "the graph indexes Go and templ sources, docs and specs, migrations and config/env.yml"
}

// shortID drops the kind prefix of an ID for prose: func:controllers.HelloAPI
// becomes controllers.HelloAPI; a route keeps its method and path.
func shortID(id string) string {
	if strings.HasPrefix(id, "route:") {
		return strings.Replace(strings.TrimPrefix(id, "route:"), ":", " ", 1)
	}
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[i+1:]
	}
	return id
}

func shortIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = "`" + shortID(id) + "`"
	}
	return out
}

func listCapped(l []string, max int) string {
	if len(l) <= max {
		return strings.Join(l, ", ")
	}
	return strings.Join(l[:max], ", ") + fmt.Sprintf(", … and %d more", len(l)-max)
}

// uniqueInOrder drops repeats, keeping the first of each.
func uniqueInOrder(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
