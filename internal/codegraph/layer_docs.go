package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cayleygraph/quad"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"

	"github.com/petars-dev/govibe/internal/profile"
)

// The docs layer reads the project's human-written documentation: every
// markdown file under the profile's doc directories (docs/public and docs/specs by
// default), plus the docs at the root (README.md and SKILL.md).
//
//   - SPEC_*.md files in the specs directory are specs (spec:<slug>); everything else is a doc.
//   - A spec's status table (| Item | Implemented | Skipped | Why |) becomes one
//     spec_item per row, with a status and, for skipped rows, why.
//   - Backticked text and link targets that name something the graph knows (a
//     file, package, function, route, config key, environment variable, MCP
//     tool, template, another doc) become `mentions` edges. A row's own
//     backticked text becomes `covers` edges on its spec item.
//
// Nothing is guessed: a token that does not resolve exactly is ignored, and a
// bare identifier is never resolved (only pkg.Name and Type.Method forms are).

type docsIndex struct {
	docs     []*docInfo // sorted by path
	warnings []string
	// probed is what the build learned about the disk: every repo-relative path
	// it looked for, and whether it was there. The layer's fingerprint covers
	// it, so creating or removing a file a document names makes the layer stale.
	probed map[string]bool
}

type docInfo struct {
	rel      string
	id       quad.IRI
	isSpec   bool
	slug     string // /dev/docs slug: specs under /dev/docs/specs/, docs under /dev/docs/app/
	name     string
	title    string
	status   string // specs only
	mentions []quad.IRI
	items    []*itemInfo
	stale    []unresolvedRef // what a doc (not a spec) names that is gone
}

type itemInfo struct {
	id     quad.IRI
	name   string
	status string // implemented, partial, skipped, planned
	why    string // the Why cell of a skipped row
	note   string // the Why cell of any other row
	line   int
	covers []quad.IRI
	stale  []unresolvedRef // what the row names that is gone
}

func (w *world) docScan() (*scan, error) {
	return w.docFiles.get(func() (*scan, error) {
		prof, err := w.profile()
		if err != nil {
			return nil, err
		}
		out := &scan{}
		for _, rel := range prof.Docs.Root {
			if sf, ok := statRegular(w.root, rel, maxDocFileBytes); ok {
				out.files = append(out.files, sf)
			}
		}
		for _, dir := range prof.ScanDirs() {
			sc, err := walkFiles(w.root, walkSpec{
				start:    dir,
				accept:   func(n string) bool { return strings.HasSuffix(n, ".md") && !strings.HasPrefix(n, ".") },
				maxBytes: maxDocFileBytes,
				maxFiles: maxDocFiles,
			})
			if err != nil {
				return nil, err
			}
			out.files = append(out.files, sc.files...)
			out.warnings = append(out.warnings, sc.warnings...)
		}
		sort.Slice(out.files, func(i, j int) bool { return out.files[i].rel < out.files[j].rel })
		return out, nil
	})
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify lowercases s and turns every run of other characters into one '-',
// the way /dev/docs names its pages.
func slugify(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// docKind classifies a markdown file: a spec (a prefixed file in the specs
// directory), a public doc (a file directly in one of the doc directories, which has
// a slug), or any other doc (a file at the root, or deeper down).
func docKind(prof *profile.Profile, rel string) (isSpec bool, slug string) {
	base := path.Base(rel)
	name := strings.TrimSuffix(base, path.Ext(base))
	dir := path.Dir(rel)
	if prof.Docs.Specs != "" && dir == prof.Docs.Specs && prof.Docs.SpecPrefix != "" && strings.HasPrefix(name, prof.Docs.SpecPrefix) {
		return true, slugify(strings.TrimPrefix(name, prof.Docs.SpecPrefix))
	}
	for _, d := range prof.Docs.Dirs {
		if dir == d {
			return false, slugify(name)
		}
	}
	return false, ""
}

func (w *world) docs() (*docsIndex, error) {
	return w.docIdx.get(func() (*docsIndex, error) {
		sc, err := w.docScan()
		if err != nil {
			return nil, err
		}
		ci, err := w.code()
		if err != nil {
			return nil, fmt.Errorf("code index: %w", err)
		}
		rt, err := w.routeTable()
		if err != nil {
			return nil, fmt.Errorf("route table: %w", err)
		}
		ti, err := w.templates()
		if err != nil {
			return nil, fmt.Errorf("templates: %w", err)
		}
		cx, err := w.configIndex()
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		dm, err := w.dbModel()
		if err != nil {
			return nil, fmt.Errorf("db: %w", err)
		}
		prof, err := w.profile()
		if err != nil {
			return nil, err
		}
		return buildDocsIndex(w.ctx, sc, newResolver(w.root, prof, sc, ci, rt, ti, cx, dm))
	})
}

func buildDocsIndex(ctx context.Context, sc *scan, rs *resolver) (*docsIndex, error) {
	di := &docsIndex{warnings: append([]string(nil), sc.warnings...)}
	if rs.noRoutes {
		di.warnings = append(di.warnings, "no router is wired: mentions of routes were not resolved")
	}
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	for _, sf := range sc.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src, err := os.ReadFile(sf.abs)
		if err != nil {
			di.warnings = append(di.warnings, fmt.Sprintf("%s: %v", sf.rel, err))
			continue
		}
		isSpec, slug := docKind(rs.prof, sf.rel)
		d := &docInfo{rel: sf.rel, isSpec: isSpec, slug: slug}
		if isSpec {
			d.id, d.name = specID(slug), slug
		} else {
			d.id, d.name = docID(sf.rel), path.Base(sf.rel)
		}
		root := md.Parser().Parse(text.NewReader(src))
		parseDoc(root, src, d, rs)
		di.docs = append(di.docs, d)
	}
	di.probed = rs.probe
	return di, nil
}

// parseDoc fills a docInfo from the markdown AST.
func parseDoc(root ast.Node, src []byte, d *docInfo, rs *resolver) {
	mentions := map[quad.IRI]bool{}
	mention := func(targets []quad.IRI) {
		for _, t := range targets {
			if t != d.id {
				mentions[t] = true
			}
		}
	}
	docDir := path.Dir(d.rel)

	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch x := n.(type) {
		case *ast.Heading:
			if x.Level == 1 && d.title == "" {
				d.title = plainText(x, src)
			}
		case *ast.CodeSpan:
			tok := plainText(x, src)
			ids := rs.resolveToken(tok, docDir)
			mention(ids)
			// A spec's prose describes what is planned as well as what is done, so
			// only its status rows are judged (see statusRows).
			if len(ids) == 0 && !d.isSpec {
				if s, ok := rs.staleToken(tok, docDir); ok {
					d.stale = addUnresolved(d.stale, nodeLine(x, src), s)
				}
			}
		case *ast.Link:
			ids := rs.resolveLink(string(x.Destination), docDir)
			mention(ids)
			if len(ids) == 0 && !d.isSpec {
				if s, ok := rs.staleLink(string(x.Destination), docDir); ok {
					d.stale = addUnresolved(d.stale, nodeLine(x, src), s)
				}
			}
		case *east.Table:
			if d.isSpec {
				d.items = append(d.items, statusRows(x, src, d, rs)...)
			}
		}
		return ast.WalkContinue, nil
	})

	if d.title == "" {
		d.title = strings.TrimSuffix(path.Base(d.rel), path.Ext(d.rel))
	}
	for t := range mentions {
		d.mentions = append(d.mentions, t)
	}
	sort.Slice(d.mentions, func(i, j int) bool { return d.mentions[i] < d.mentions[j] })
	sortUnresolved(d.stale)
	d.status = specStatus(d.items)
}

// statusRows turns a table with an Item and an Implemented column into spec
// items; any other table yields none.
func statusRows(tbl *east.Table, src []byte, d *docInfo, rs *resolver) []*itemInfo {
	cols := map[string]int{}
	var rows []*east.TableRow
	for c := tbl.FirstChild(); c != nil; c = c.NextSibling() {
		switch r := c.(type) {
		case *east.TableHeader:
			i := 0
			for cell := r.FirstChild(); cell != nil; cell = cell.NextSibling() {
				name := strings.ToLower(plainText(cell, src))
				switch name {
				case "why", "rationale", "reason", "notes", "note":
					name = "why"
				}
				if _, dup := cols[name]; !dup {
					cols[name] = i
				}
				i++
			}
		case *east.TableRow:
			rows = append(rows, r)
		}
	}
	itemCol, hasItem := cols["item"]
	implCol, hasImpl := cols["implemented"]
	if !hasItem || !hasImpl {
		return nil
	}
	skipCol, hasSkip := cols["skipped"]
	whyCol, hasWhy := cols["why"]

	var items []*itemInfo
	used := map[string]int{}
	for _, row := range rows {
		var cells []ast.Node
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, cell)
		}
		cellText := func(i int, ok bool) string {
			if !ok || i >= len(cells) {
				return ""
			}
			return strings.ReplaceAll(plainText(cells[i], src), `\|`, "|")
		}
		name := cellText(itemCol, true)
		if name == "" {
			continue
		}
		slug := slugify(name)
		if len(slug) > 60 {
			slug = strings.Trim(slug[:60], "-")
		}
		if slug == "" {
			slug = "item"
		}
		if used[slug]++; used[slug] > 1 {
			slug = fmt.Sprintf("%s-%d", slug, used[slug])
		}

		it := &itemInfo{id: itemID(d.slug, slug), name: name}
		why := cellText(whyCol, hasWhy)
		it.status = itemStatus(cellText(implCol, true), cellText(skipCol, hasSkip))
		switch {
		case it.status == "skipped":
			it.why = why
		case why != "" && !strings.EqualFold(why, "n/a"):
			it.note = why
		}
		it.line = nodeLine(cells[itemCol], src)
		seen := map[quad.IRI]bool{}
		for _, c := range cells {
			_ = ast.Walk(c, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
				if cs, ok := n.(*ast.CodeSpan); ok && entering {
					tok := plainText(cs, src)
					ids := rs.resolveToken(tok, path.Dir(d.rel))
					for _, t := range ids {
						if !seen[t] && t != d.id {
							seen[t] = true
							it.covers = append(it.covers, t)
						}
					}
					if len(ids) == 0 {
						if s, ok := rs.staleToken(tok, path.Dir(d.rel)); ok {
							it.stale = addUnresolved(it.stale, nodeLine(cs, src), s)
						}
					}
				}
				return ast.WalkContinue, nil
			})
		}
		sort.Slice(it.covers, func(i, j int) bool { return it.covers[i] < it.covers[j] })
		sortUnresolved(it.stale)
		items = append(items, it)
	}
	return items
}

// itemStatus reads the Implemented and Skipped cells of a status row.
func itemStatus(implemented, skipped string) string {
	impl := strings.ToLower(strings.TrimSpace(implemented))
	skip := strings.ToLower(strings.TrimSpace(skipped))
	yes := strings.HasPrefix(impl, "yes") || strings.HasPrefix(impl, "done") || strings.HasPrefix(impl, "✅")
	switch {
	case skip != "" && skip != "no" && skip != "n/a" && skip != "-" && !yes:
		return "skipped"
	case yes:
		return "implemented"
	case strings.HasPrefix(impl, "partial"):
		return "partial"
	}
	return "planned"
}

// specStatus summarises a spec from its rows: complete when every row is
// implemented or skipped, planned when none is implemented, else in_progress.
func specStatus(items []*itemInfo) string {
	if len(items) == 0 {
		return ""
	}
	implemented, open := 0, 0
	for _, it := range items {
		switch it.status {
		case "implemented":
			implemented++
		case "skipped":
		default:
			open++
		}
	}
	switch {
	case open == 0:
		return "complete"
	case implemented == 0:
		return "planned"
	}
	return "in_progress"
}

// plainText is the text content of a node, whitespace collapsed.
func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(src))
			if t.SoftLineBreak() || t.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// nodeLine is the 1-based source line of the first text inside n, 0 if unknown.
func nodeLine(n ast.Node, src []byte) int {
	line := 0
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			line = 1 + strings.Count(string(src[:t.Segment.Start]), "\n")
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return line
}

// resolver turns the text of a doc into references to nodes the graph holds. It
// is built from the same indexes the other layers write from, so everything it
// returns exists.
type resolver struct {
	root string
	prof *profile.Profile
	ci   *codeIndex
	rt   *routeTable
	ti   *templIndex
	cx   *configIndex

	probe         map[string]bool // paths looked for on disk, and whether they were there
	externalNames map[string]bool // names of the libraries the module imports

	noRoutes bool
	ids      map[string]quad.IRI   // every node ID, as text
	routes   map[string][]quad.IRI // by path
	routeIDs map[string]quad.IRI   // by "METHOD path"
	docPath  map[string]quad.IRI   // markdown file -> doc or spec
	docSlug  map[string]quad.IRI   // /dev/docs/app slug -> doc
	specSlug map[string]quad.IRI   // /dev/docs/specs slug -> spec
	tools    map[string]quad.IRI
	typesBy  map[string][]*typeInfo // type name -> types
	filesBy  map[string][]string    // base name -> .go files
	tables   map[string]quad.IRI    // lower-case table name
	columns  map[string]quad.IRI    // lower-case "table.column"
	migPath  map[string]quad.IRI    // migration file path, and its base name when unambiguous
}

func newResolver(root string, prof *profile.Profile, sc *scan, ci *codeIndex, rt *routeTable, ti *templIndex, cx *configIndex, dm *dbModel) *resolver {
	rs := &resolver{
		root: root, prof: prof,
		ci: ci, rt: rt, ti: ti, cx: cx,
		probe:         map[string]bool{},
		externalNames: externalPackageNames(ci),
		noRoutes:      rt.absent,
		ids:           map[string]quad.IRI{},
		routes:        map[string][]quad.IRI{},
		routeIDs:      map[string]quad.IRI{},
		docPath:       map[string]quad.IRI{},
		docSlug:       map[string]quad.IRI{},
		specSlug:      map[string]quad.IRI{},
		tools:         map[string]quad.IRI{},
		typesBy:       map[string][]*typeInfo{},
		tables:        map[string]quad.IRI{},
		columns:       map[string]quad.IRI{},
		migPath:       map[string]quad.IRI{},
		filesBy:       map[string][]string{},
	}
	add := func(id quad.IRI) { rs.ids[string(id)] = id }
	for d := range ci.pkgs {
		add(pkgID(d))
	}
	for rel := range ci.files {
		add(fileID(rel))
		rs.filesBy[path.Base(rel)] = append(rs.filesBy[path.Base(rel)], rel)
	}
	for _, f := range ci.funcs {
		add(f.id)
	}
	for _, t := range ci.types {
		add(t.id)
		rs.typesBy[t.name] = append(rs.typesBy[t.name], t)
	}
	for _, f := range ti.files {
		add(fileID(f.rel))
	}
	for _, t := range ti.templates {
		add(t.id)
	}
	for _, r := range rt.routes {
		id := routeID(r.Method, r.Path)
		add(id)
		rs.routes[r.Path] = append(rs.routes[r.Path], id)
		rs.routeIDs[r.Method+" "+r.Path] = id
	}
	for _, k := range cx.keys {
		add(cfgID(k.key))
	}
	for _, e := range cx.envs {
		add(envID(e.name))
	}
	for _, t := range ci.tools {
		id := toolID(t.name)
		add(id)
		rs.tools[t.name] = id
	}
	for _, t := range dm.tables {
		tid := tableID(t.name)
		add(tid)
		rs.tables[strings.ToLower(t.name)] = tid
		for _, c := range t.columns {
			cid := colID(t.name, c.name)
			add(cid)
			rs.columns[strings.ToLower(t.name+"."+c.name)] = cid
		}
		for _, ix := range t.indexes {
			add(idxID(t.name, ix.name))
		}
	}
	baseCount := map[string]int{}
	for _, mg := range dm.migrations {
		add(migID(mg.stem))
		rs.migPath[mg.path] = migID(mg.stem)
		baseCount[path.Base(mg.path)]++
	}
	for _, mg := range dm.migrations {
		if baseCount[path.Base(mg.path)] == 1 {
			rs.migPath[path.Base(mg.path)] = migID(mg.stem)
		}
	}
	for _, sf := range sc.files {
		isSpec, slug := docKind(prof, sf.rel)
		var id quad.IRI
		if isSpec {
			id = specID(slug)
			rs.specSlug[slug] = id
		} else {
			id = docID(sf.rel)
			if slug != "" {
				rs.docSlug[slug] = id
			}
		}
		add(id)
		rs.docPath[sf.rel] = id
	}
	for _, l := range rs.filesBy {
		sort.Strings(l)
	}
	return rs
}

var (
	routeTokenRe = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|ANY)\s+(/\S*)$`)
	envTokenRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,}$`)
	cfgTokenRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+(\.\*)?$`)
	toolTokenRe  = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)
	symbolRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*){1,2}$`)
	lineSuffixRe = regexp.MustCompile(`:[0-9]+(:[0-9]+)?$`)
)

// resolveToken resolves the text of an inline code span. docDir is the
// directory of the document, for paths written relative to it.
func (rs *resolver) resolveToken(tok, docDir string) []quad.IRI {
	tok = strings.TrimSpace(tok)
	if tok == "" || len(tok) > 200 || strings.ContainsAny(tok, "\n\t") {
		return nil
	}
	// A node ID written out in full.
	if id, ok := rs.ids[tok]; ok {
		return []quad.IRI{id}
	}
	// A route: "GET /api/hello", or a bare path.
	if m := routeTokenRe.FindStringSubmatch(tok); m != nil {
		if id, ok := rs.routeIDs[m[1]+" "+m[2]]; ok {
			return []quad.IRI{id}
		}
		// A route registered for every method serves this one too.
		if id, ok := rs.routeIDs[anyMethod+" "+m[2]]; ok && m[1] != anyMethod {
			return []quad.IRI{id}
		}
		return nil
	}
	if strings.HasPrefix(tok, "/") && !strings.ContainsAny(tok, " ") {
		if ids := rs.routes[tok]; len(ids) > 0 {
			return ids
		}
		if t := strings.TrimSuffix(tok, "/"); t != tok {
			if ids := rs.routes[t]; len(ids) > 0 {
				return ids
			}
		}
	}
	if strings.ContainsAny(tok, " ") {
		return nil
	}
	// A repository path.
	if ids := rs.resolvePath(tok, docDir); len(ids) > 0 {
		return ids
	}
	// An environment variable.
	if envTokenRe.MatchString(tok) {
		if e := rs.cx.envByName[tok]; e != nil {
			return []quad.IRI{envID(tok)}
		}
		return nil
	}
	// A config key, or every key below a group or a "prefix.*".
	if cfgTokenRe.MatchString(tok) {
		if ids := rs.resolveConfig(strings.TrimSuffix(tok, ".*")); len(ids) > 0 {
			return ids
		}
	}
	if toolTokenRe.MatchString(tok) {
		if id, ok := rs.tools[tok]; ok {
			return []quad.IRI{id}
		}
	}
	// pkg.Name, Type.Method, pkg.Type.Method.
	if ids := rs.resolveSymbol(strings.TrimSuffix(tok, "()")); len(ids) > 0 {
		return ids
	}
	// dir.Name: `storage/migrations.SetDialect`, the way a function's ID is written.
	if ids := rs.resolveDirSymbol(strings.TrimSuffix(tok, "()")); len(ids) > 0 {
		return ids
	}
	return rs.resolveSchema(tok)
}

func (rs *resolver) resolveConfig(key string) []quad.IRI {
	if _, ok := rs.cx.keyByPath[key]; ok {
		return []quad.IRI{cfgID(key)}
	}
	var out []quad.IRI
	for _, k := range rs.cx.keys {
		if strings.HasPrefix(k.key, key+".") {
			out = append(out, cfgID(k.key))
		}
	}
	return out
}

// resolvePath resolves a file or directory path, trying it relative to the
// document first and then to the repo root.
func (rs *resolver) resolvePath(tok, docDir string) []quad.IRI {
	p := lineSuffixRe.ReplaceAllString(strings.TrimPrefix(tok, "./"), "")
	if p == "" || strings.HasPrefix(p, "/") {
		return nil
	}
	p = strings.TrimSuffix(p, "/")
	var candidates []string
	if docDir != "." && docDir != "" {
		// path.Join cleans "..", and a path that climbs out of the repo is dropped.
		if c := path.Join(docDir, p); !strings.HasPrefix(c, "..") {
			candidates = append(candidates, c)
		}
	}
	if !strings.Contains(p, "..") {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		if id, ok := rs.docPath[c]; ok {
			return []quad.IRI{id}
		}
		if id, ok := rs.migPath[c]; ok {
			return []quad.IRI{id}
		}
		if _, ok := rs.ci.files[c]; ok {
			return []quad.IRI{fileID(c)}
		}
		for _, f := range rs.ti.files {
			if f.rel == c {
				return []quad.IRI{fileID(c)}
			}
		}
		if _, ok := rs.ci.pkgs[c]; ok {
			return []quad.IRI{pkgID(c)}
		}
	}
	// A bare Go file name names the file only when it is unambiguous.
	if !strings.Contains(p, "/") && strings.HasSuffix(p, ".go") {
		if files := rs.filesBy[p]; len(files) == 1 {
			return []quad.IRI{fileID(files[0])}
		}
	}
	return nil
}

// resolveSchema resolves the name of a table, or table.column. It runs after
// the symbol rules, so `items.Service` stays the package's type.
func (rs *resolver) resolveSchema(tok string) []quad.IRI {
	if !identRe.MatchString(tok) && !tableColumnRe.MatchString(tok) {
		return nil
	}
	if id, ok := rs.tables[strings.ToLower(tok)]; ok {
		return []quad.IRI{id}
	}
	if id, ok := rs.columns[strings.ToLower(tok)]; ok {
		return []quad.IRI{id}
	}
	return nil
}

var (
	identRe       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableColumnRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*$`)
)

// dirSymbol splits `storage/migrations.SetDialect` into the package directory
// and the name; ok is false for anything else (including a file name).
func dirSymbol(tok string) (dir, name string, ok bool) {
	i := strings.LastIndex(tok, ".")
	if i <= 0 || !strings.Contains(tok[:i], "/") || pathExts[tok[i:]] {
		return "", "", false
	}
	dir, name = tok[:i], tok[i+1:]
	if !identRe.MatchString(name) || !isExportedName(name) {
		return "", "", false
	}
	return dir, name, true
}

// resolveDirSymbol resolves dir.Name, a function or type of the package in dir.
func (rs *resolver) resolveDirSymbol(tok string) []quad.IRI {
	dir, name, ok := dirSymbol(tok)
	if !ok {
		return nil
	}
	if _, ok := rs.ci.pkgs[dir]; !ok {
		return nil
	}
	if f := rs.ci.funcNamed(dir, "", name); f != nil {
		return []quad.IRI{f.id}
	}
	if t := rs.ci.typeNamed(dir, name); t != nil {
		return []quad.IRI{t.id}
	}
	return nil
}

// resolveSymbol resolves pkg.Name, Type.Method and pkg.Type.Method, but only
// when exactly one thing answers to the name.
func (rs *resolver) resolveSymbol(tok string) []quad.IRI {
	if !symbolRe.MatchString(tok) {
		return nil
	}
	parts := strings.Split(tok, ".")
	switch len(parts) {
	case 2:
		a, b := parts[0], parts[1]
		// pkg.Name: a function or type of the one package called pkg.
		if dirs := rs.ci.pkgNamed(a); len(dirs) == 1 {
			if f := rs.ci.funcNamed(dirs[0], "", b); f != nil {
				return []quad.IRI{f.id}
			}
			if t := rs.ci.typeNamed(dirs[0], b); t != nil {
				return []quad.IRI{t.id}
			}
		}
		// A template, by its package: templates.Home.
		if t := rs.ti.byPackageName(a, b); t != nil {
			return []quad.IRI{t.id}
		}
		// Type.Method: a method of the one type called Type.
		if types := rs.typesBy[a]; len(types) == 1 {
			if f := rs.ci.funcNamed(types[0].dir, a, b); f != nil {
				return []quad.IRI{f.id}
			}
		}
	case 3:
		if dirs := rs.ci.pkgNamed(parts[0]); len(dirs) == 1 {
			if f := rs.ci.funcNamed(dirs[0], parts[1], parts[2]); f != nil {
				return []quad.IRI{f.id}
			}
		}
	}
	return nil
}

// resolveLink resolves a markdown link target: another page of /dev/docs, a
// route, or a path in the repository.
func (rs *resolver) resolveLink(dest, docDir string) []quad.IRI {
	dest = strings.TrimSpace(dest)
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i]
	}
	if dest == "" || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
		return nil
	}
	if slug, ok := strings.CutPrefix(dest, "/dev/docs/specs/"); ok {
		if id, ok := rs.specSlug[strings.TrimSuffix(slug, "/")]; ok {
			return []quad.IRI{id}
		}
	}
	if slug, ok := strings.CutPrefix(dest, "/dev/docs/app/"); ok {
		if id, ok := rs.docSlug[strings.TrimSuffix(slug, "/")]; ok {
			return []quad.IRI{id}
		}
	}
	if strings.HasPrefix(dest, "/") {
		if ids := rs.routes[dest]; len(ids) > 0 {
			return ids
		}
		if t := strings.TrimSuffix(dest, "/"); t != dest {
			return rs.routes[t]
		}
		return nil
	}
	return rs.resolvePath(dest, docDir)
}

type docsLayer struct{}

func (docsLayer) name() string { return "docs" }
func (docsLayer) deps() []string {
	return []string{"code", "routes", "templates", "config", "mcp", "db"}
}

func (docsLayer) settles() {}

// fingerprint covers the documents (path, mtime, size) and whether each file the
// last build looked for on disk is there, which no edit of a document shows.
func (docsLayer) fingerprint(w *world) (string, error) {
	sc, err := w.docScan()
	if err != nil {
		return "", err
	}
	fp := fingerprint(sc.files)
	probed := w.x.docProbed
	if len(probed) == 0 {
		return fp, nil
	}
	rels := make([]string, 0, len(probed))
	for rel := range probed {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", fp)
	for _, rel := range rels {
		there := probed[rel]
		if !w.settling {
			_, err := os.Lstat(filepath.Join(w.root, filepath.FromSlash(rel)))
			there = err == nil
		}
		fmt.Fprintf(h, "%s\x00%t\n", rel, there)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func (docsLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	di, err := w.docs()
	if err != nil {
		return nil, err
	}
	w.x.docProbed = di.probed
	set := newQuadSet("docs")
	for _, d := range di.docs {
		if d.isSpec {
			set.add(d.id, PredType, KindSpec)
		} else {
			set.add(d.id, PredType, KindDoc)
		}
		set.str(d.id, PredName, d.name)
		set.str(d.id, PredTitle, d.title)
		set.str(d.id, PredPath, d.rel)
		if d.status != "" {
			set.str(d.id, PredStatus, d.status)
		}
		for _, t := range d.mentions {
			set.add(d.id, PredMentions, t)
		}
		for _, u := range d.stale {
			set.str(d.id, PredUnresolvedRef, u.String())
		}
		for _, it := range d.items {
			set.add(d.id, PredHasItem, it.id)
			set.add(it.id, PredType, KindSpecItem)
			set.str(it.id, PredName, it.name)
			set.str(it.id, PredStatus, it.status)
			set.str(it.id, PredPath, d.rel)
			if it.line > 0 {
				set.num(it.id, PredLine, it.line)
			}
			if it.why != "" {
				set.str(it.id, PredWhySkipped, it.why)
			}
			if it.note != "" {
				set.str(it.id, PredNote, it.note)
			}
			for _, t := range it.covers {
				set.add(it.id, PredCovers, t)
			}
			for _, u := range it.stale {
				set.str(it.id, PredUnresolvedRef, u.String())
			}
		}
	}
	return &layerOutput{quads: set.quads, warnings: di.warnings}, nil
}
