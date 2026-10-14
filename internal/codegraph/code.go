package codegraph

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/profile"
)

const (
	maxDocRunes       = 240 // a doc attribute is the first sentence, capped
	maxSignatureRunes = 500
)

// codeLayer indexes the Go source of the repo with go/parser: packages, files,
// functions and methods, types, and module-internal imports. It is AST only,
// with no type-checking, so it is fast and cannot fail on unresolved imports.
//
// The same single pass also records the facts the other layers resolve against
// (function spans, struct fields, qualified references, config and env reads,
// mcp.AddTool calls), so nothing is parsed twice: see codeIndex.
type codeLayer struct{}

func (codeLayer) name() string   { return "code" }
func (codeLayer) deps() []string { return nil }

func (codeLayer) fingerprint(w *world) (string, error) {
	return w.codeFingerprint()
}

func (codeLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	ci, err := w.code()
	if err != nil {
		return nil, err
	}
	return &layerOutput{quads: ci.quads, warnings: ci.warnings}, nil
}

// codeIndex is everything one parse of the repo's Go source yields. The code
// layer writes its quads; the routes, templates, config, mcp and docs layers
// read the rest to resolve what they point at without parsing again.
type codeIndex struct {
	module   string
	prof     *profile.Profile // which directory is which architecture layer
	mods     *moduleTable     // which module an import path belongs to
	quads    []quad.Quad
	warnings []string

	pkgs  map[string]*pkgInfo // by directory
	files map[string]*fileInfo
	funcs []*funcInfo // in ID allocation order
	types []*typeInfo

	funcsByFile map[string][]*funcInfo
	funcsByName map[string]*funcInfo // "dir\x00Recv\x00Name"
	typesByKey  map[string]*typeInfo // "dir\x00Name"

	// Facts, in source order and deduplicated.
	extImports []refSite  // imports of packages outside the module, with where each is written: name is the import path
	refs       []refSite  // pkg.Name references into other module packages
	typeRefs   []refSite  // the same, in signatures and type declarations (not in bodies): where a type of another package is named
	cfgChains  []cfgChain // cfg.A.B selector chains
	envReads   []envRead  // os.Getenv("X") / os.LookupEnv("X")
	cfgCalls   []cfgCall  // config.Get("a.b") / config.Unmarshal("a.b", ...), or the same methods on a config value (rt.Config.Get)
	tools      []toolReg  // mcp.AddTool(srv, &mcp.Tool{...}, handler)
	toolWarns  []string
	sql        []sqlText // SQL-looking strings used inside storage and business functions

	// What the test sources reference (they add no nodes): see readTests.
	testRefs   map[string]bool            // "dir\x00Name", referenced as pkg.Name
	testIdents map[string]map[string]bool // dir -> exported bare names an in-package test mentions

	// topLevel holds every name declared at package level (functions, types,
	// variables, constants), by directory: what a doc's `pkg.Name` must be
	// found in to still exist.
	topLevel map[string]map[string]bool
	// identUses records, by directory and name, the declared functions inside
	// which an exported identifier is used as a bare name ("" for a package-level
	// initializer): the evidence that an exported function is called from its own
	// package. Qualified uses from other packages are refs.
	identUses map[string]map[string]map[quad.IRI]bool

	// pkgStrings holds the string constants of storage and business packages by
	// directory and name, so a function that passes `listSQL` to a query counts
	// as using the SQL in it.
	pkgStrings map[string]map[string]string
}

type pkgInfo struct {
	dir        string
	names      map[string]int // package clause names seen, by file count
	doc        string         // first sentence of the package comment
	docFromGen bool
	imports    map[string]bool // module-internal import dirs
	external   map[string]bool // import paths with no package node
}

type fileInfo struct {
	rel string
	dir string
}

type funcInfo struct {
	id      quad.IRI
	dir     string
	recv    string
	name    string
	file    string
	line    int // the func keyword
	endLine int
}

type typeInfo struct {
	id       quad.IRI
	dir      string
	name     string
	file     string
	line     int
	fields   []fieldInfo // set for struct types
	isStruct bool        // declared as a struct
	embeds   bool        // a struct that embeds another type, whose methods and fields it promotes
}

// fieldInfo is one named field of a struct. An inline struct type carries its
// own fields in inline.
type fieldInfo struct {
	name   string
	typ    string // rendered type expression; "struct" for an inline struct
	tag    reflect.StructTag
	inline []fieldInfo
}

// site is where a fact was found. fn is the enclosing declared function (a
// closure resolves to the function that contains it), or "" at package level.
type site struct {
	fn   quad.IRI
	dir  string
	file string
	line int
}

type refSite struct {
	site
	toDir string
	name  string
}

type cfgChain struct {
	site
	names []string // fields after the root, e.g. [Database MariaDB DSN]
}

type envRead struct {
	site
	name string
}

type cfgCall struct {
	site
	callee string // Get or Unmarshal
	key    string
}

// sqlText is a string a function uses that may be a SQL statement, found
// either as a literal in its body or through a package-level constant it names.
type sqlText struct {
	site
	text string
}

type toolReg struct {
	site
	name        string
	description string
	input       string // the handler's input type name, "" if unknown
	emptyInput  bool   // the handler takes struct{}
}

// declare records a name declared at package level.
func (ci *codeIndex) declare(dir, name string) {
	if ci.topLevel[dir] == nil {
		ci.topLevel[dir] = map[string]bool{}
	}
	ci.topLevel[dir][name] = true
}

// useIdent records that fn (or a package-level initializer, "") uses the
// exported identifier name as a bare name.
func (ci *codeIndex) useIdent(dir, name string, fn quad.IRI) {
	byName := ci.identUses[dir]
	if byName == nil {
		byName = map[string]map[quad.IRI]bool{}
		ci.identUses[dir] = byName
	}
	if byName[name] == nil {
		byName[name] = map[quad.IRI]bool{}
	}
	byName[name][fn] = true
}

// usedWithin reports whether a function other than self, or a package-level
// initializer, of the package in dir uses name as a bare identifier.
func (ci *codeIndex) usedWithin(dir, name string, self quad.IRI) bool {
	for fn := range ci.identUses[dir][name] {
		if fn != self {
			return true
		}
	}
	return false
}

// hasEmbedded reports whether a struct embeds another type.
func hasEmbedded(st *ast.StructType) bool {
	if st.Fields == nil {
		return false
	}
	for _, f := range st.Fields.List {
		if len(f.Names) == 0 {
			return true
		}
	}
	return false
}

// funcAt returns the declared function whose body contains file:line.
func (ci *codeIndex) funcAt(file string, line int) *funcInfo {
	for _, f := range ci.funcsByFile[file] {
		if line >= f.line && line <= f.endLine {
			return f
		}
	}
	return nil
}

func (ci *codeIndex) funcNamed(dir, recv, name string) *funcInfo {
	return ci.funcsByName[dir+"\x00"+recv+"\x00"+name]
}

func (ci *codeIndex) typeNamed(dir, name string) *typeInfo {
	return ci.typesByKey[dir+"\x00"+name]
}

// pkgNamed returns the directories of the packages whose clause name is name.
func (ci *codeIndex) pkgNamed(name string) []string {
	var dirs []string
	for d, p := range ci.pkgs {
		if p.name() == name {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func buildCodeIndex(ctx context.Context, root string, sc *scan, prof *profile.Profile, mods *moduleTable) (*codeIndex, error) {
	module, err := readModulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	ci := &codeIndex{
		module:      module,
		prof:        prof,
		mods:        mods,
		warnings:    append([]string(nil), sc.warnings...),
		pkgs:        map[string]*pkgInfo{},
		files:       map[string]*fileInfo{},
		funcsByFile: map[string][]*funcInfo{},
		funcsByName: map[string]*funcInfo{},
		typesByKey:  map[string]*typeInfo{},
		pkgStrings:  map[string]map[string]string{},
		topLevel:    map[string]map[string]bool{},
		identUses:   map[string]map[string]map[quad.IRI]bool{},
		testRefs:    map[string]bool{},
		testIdents:  map[string]map[string]bool{},
	}
	set := newQuadSet("code")
	fset := token.NewFileSet()

	var parsed []*parsedFile
	for _, sf := range sc.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src, err := os.ReadFile(sf.abs)
		if err != nil {
			ci.warnings = append(ci.warnings, fmt.Sprintf("%s: %v", sf.rel, err))
			continue
		}
		af, err := parser.ParseFile(fset, sf.rel, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			ci.warnings = append(ci.warnings, fmt.Sprintf("%s: %v", sf.rel, firstLine(err.Error())))
			continue
		}
		dir := pathDir(sf.rel)
		p := ci.pkgs[dir]
		if p == nil {
			p = &pkgInfo{dir: dir, names: map[string]int{}, imports: map[string]bool{}, external: map[string]bool{}}
			ci.pkgs[dir] = p
		}
		generated := ast.IsGenerated(af)
		p.names[af.Name.Name]++
		if af.Doc != nil && (p.doc == "" || (p.docFromGen && !generated)) {
			if s := firstSentence(af.Doc.Text()); s != "" {
				p.doc, p.docFromGen = s, generated
			}
		}
		ci.files[sf.rel] = &fileInfo{rel: sf.rel, dir: dir}
		parsed = append(parsed, &parsedFile{sf: sf, src: src, ast: af, generated: generated, dir: dir})
	}

	// Imports can only be resolved once every package is known.
	for _, pf := range parsed {
		p := ci.pkgs[pf.dir]
		for _, imp := range pf.ast.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if d, ok := moduleDir(module, path); ok && ci.pkgs[d] != nil {
				p.imports[d] = true
				continue
			}
			p.external[path] = true
			if mods.isFramework(path) {
				// Where the import is written, for a finding that says where to look.
				ci.extImports = append(ci.extImports, refSite{
					site: site{dir: pf.dir, file: pf.sf.rel, line: fset.Position(imp.Pos()).Line},
					name: path,
				})
			}
		}
	}

	// Package-level string constants of the layers that talk to the database, in
	// source order so one constant may build on an earlier one.
	for _, pf := range parsed {
		if !ci.holdsSQL(pf.dir) {
			continue
		}
		for _, decl := range pf.ast.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i >= len(vs.Values) {
						break
					}
					if v, ok := constStringIn(vs.Values[i], ci.pkgStrings[pf.dir]); ok {
						if ci.pkgStrings[pf.dir] == nil {
							ci.pkgStrings[pf.dir] = map[string]string{}
						}
						ci.pkgStrings[pf.dir][n.Name] = v
					}
				}
			}
		}
	}

	ids := newIDAllocator()
	seen := map[string]bool{}
	for _, pf := range parsed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ci.emitFile(set, fset, ids, seen, pf)
	}

	dirs := make([]string, 0, len(ci.pkgs))
	for d := range ci.pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		p := ci.pkgs[d]
		id := pkgID(d)
		set.add(id, PredType, KindPackage)
		set.str(id, PredName, p.name())
		set.str(id, PredPath, d)
		set.str(id, PredImportPath, importPath(module, d))
		set.str(id, PredArchLayer, prof.Classify(d))
		if p.doc != "" {
			set.str(id, PredDoc, p.doc)
		}
		set.num(id, PredExternalImports, len(p.external))
		for imp := range p.imports {
			set.add(id, PredImports, pkgID(imp))
		}
		// What the project takes from the framework it is built on: one node per package
		// it imports, so the framework's side of the connection is in the graph, labelled.
		for _, imp := range sortedKeys(p.external) {
			if mods.isFramework(imp) {
				set.add(id, PredImportsExt, mods.pkgRef(imp))
			}
		}
	}

	if err := ci.readTests(ctx, fset, sc.extra); err != nil {
		return nil, err
	}

	ci.quads = set.quads
	return ci, nil
}

// name is the package clause name shared by most of the directory's files; on
// a tie the alphabetically first wins, so the choice is deterministic.
func (p *pkgInfo) name() string {
	best, n := "", -1
	for name, c := range p.names {
		if c > n || (c == n && name < best) {
			best, n = name, c
		}
	}
	return best
}

type parsedFile struct {
	sf        sourceFile
	src       []byte
	ast       *ast.File
	generated bool
	dir       string
}

// emitFile writes the file's quads and records its facts. seen deduplicates
// facts across the whole run.
func (ci *codeIndex) emitFile(set *quadSet, fset *token.FileSet, ids *idAllocator, seen map[string]bool, pf *parsedFile) {
	fid := fileID(pf.sf.rel)
	set.add(fid, PredType, KindFile)
	set.str(fid, PredName, filepath.Base(pf.sf.rel))
	set.str(fid, PredPath, pf.sf.rel)
	set.num(fid, PredLines, countLines(pf.src))
	set.flag(fid, PredGenerated, pf.generated)
	set.add(fid, PredInPackage, pkgID(pf.dir))

	col := &collector{ci: ci, fset: fset, seen: seen, dir: pf.dir, file: pf.sf.rel, imps: ci.importNames(pf.ast), sql: ci.holdsSQL(pf.dir)}

	for _, decl := range pf.ast.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			recv := receiverName(d)
			if recv == "" {
				ci.declare(pf.dir, d.Name.Name)
			}
			id := ids.alloc(funcID(pf.dir, recv, d.Name.Name))
			name := d.Name.Name
			if recv != "" {
				name = recv + "." + name
			}
			set.add(id, PredType, KindFunc)
			set.str(id, PredName, name)
			set.add(id, PredDefinedIn, fid)
			line := fset.Position(d.Pos()).Line
			set.num(id, PredLine, line)
			set.flag(id, PredExported, ast.IsExported(d.Name.Name))
			set.str(id, PredSignature, funcSignature(fset, d))
			if d.Doc != nil {
				if s := firstSentence(d.Doc.Text()); s != "" {
					set.str(id, PredDoc, s)
				}
			}
			fi := &funcInfo{id: id, dir: pf.dir, recv: recv, name: d.Name.Name, file: pf.sf.rel, line: line, endLine: fset.Position(d.End()).Line}
			ci.funcs = append(ci.funcs, fi)
			ci.funcsByFile[pf.sf.rel] = append(ci.funcsByFile[pf.sf.rel], fi)
			if key := pf.dir + "\x00" + recv + "\x00" + d.Name.Name; ci.funcsByName[key] == nil {
				ci.funcsByName[key] = fi
			}
			if d.Body != nil {
				col.fn = id
				ast.Inspect(d.Body, col.visit)
				col.fn = ""
			}
			col.fn = id
			if d.Recv != nil {
				ast.Inspect(d.Recv, col.visitType)
			}
			ast.Inspect(d.Type, col.visitType)
			col.fn = ""
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					ci.declare(pf.dir, ts.Name.Name)
					ast.Inspect(ts.Type, col.visitType)
					id := ids.alloc(typeID(pf.dir, ts.Name.Name))
					set.add(id, PredType, KindType)
					set.str(id, PredName, ts.Name.Name)
					set.add(id, PredDefinedIn, fid)
					line := fset.Position(ts.Pos()).Line
					set.num(id, PredLine, line)
					set.flag(id, PredExported, ast.IsExported(ts.Name.Name))
					set.str(id, PredSignature, typeSignature(fset, ts))
					doc := ts.Doc
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc // `// T ...` above a lone `type T ...`
					}
					if doc != nil {
						if s := firstSentence(doc.Text()); s != "" {
							set.str(id, PredDoc, s)
						}
					}
					ti := &typeInfo{id: id, dir: pf.dir, name: ts.Name.Name, file: pf.sf.rel, line: line}
					if st, ok := ts.Type.(*ast.StructType); ok {
						ti.fields = structFields(fset, st)
						ti.isStruct, ti.embeds = true, hasEmbedded(st)
					}
					ci.types = append(ci.types, ti)
					if key := pf.dir + "\x00" + ts.Name.Name; ci.typesByKey[key] == nil {
						ci.typesByKey[key] = ti
					}
				}
			case token.VAR, token.CONST:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							ci.declare(pf.dir, n.Name)
						}
					}
				}
				// Package-level initializers: facts found here belong to the package.
				ast.Inspect(d, col.visit)
			}
		}
	}
}

// importNames maps the local name of each import in the file to its path.
func (ci *codeIndex) importNames(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		switch {
		case imp.Name != nil:
			name = imp.Name.Name
		default:
			if d, ok := moduleDir(ci.module, path); ok && ci.pkgs[d] != nil {
				name = ci.pkgs[d].name()
			} else {
				name = guessPkgName(path)
			}
		}
		if name == "" || name == "_" || name == "." {
			continue
		}
		out[name] = path
	}
	return out
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// guessPkgName is the usual package name for an import path: its last element,
// without a /vN major-version suffix or a .vN dot suffix.
func guessPkgName(path string) string {
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if majorVersion.MatchString(last) && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	if i := strings.LastIndex(last, ".v"); i > 0 {
		last = last[:i]
	}
	return last
}

// collector walks one function body (or package-level initializer) and records
// the facts the other layers resolve against.
type collector struct {
	ci   *codeIndex
	fset *token.FileSet
	seen map[string]bool
	imps map[string]string
	dir  string
	file string
	fn   quad.IRI
	sql  bool // the package is one that holds SQL (storage, business)
}

func (c *collector) site(pos token.Pos) site {
	return site{fn: c.fn, dir: c.dir, file: c.file, line: c.fset.Position(pos).Line}
}

// firstOnce reports whether key has not been recorded before in this run.
func (c *collector) firstOnce(key string) bool {
	if c.seen[key] {
		return false
	}
	c.seen[key] = true
	return true
}

// configRoots are the identifiers a config.Config value is conventionally
// bound to; without type information a selector chain can only be attributed
// to the config struct by this name.
var configRoots = map[string]bool{"cfg": true, "conf": true}

func (c *collector) visit(n ast.Node) bool {
	switch x := n.(type) {
	case *ast.SelectorExpr:
		root, names := flattenSelector(x)
		if root != nil && configRoots[root.Name] {
			if c.firstOnce("cfg\x00" + string(c.fn) + "\x00" + c.dir + "\x00" + strings.Join(names, ".")) {
				c.ci.cfgChains = append(c.ci.cfgChains, cfgChain{site: c.site(x.Pos()), names: names})
			}
			return false // the prefixes of the chain are the same use
		}
		if id, ok := x.X.(*ast.Ident); ok {
			if path, ok := c.imps[id.Name]; ok {
				// A package with no Go sources of its own (only generated templ
				// output) has no node but can still be referenced.
				if d, ok := moduleDir(c.ci.module, path); ok && d != c.dir {
					if c.firstOnce("ref\x00" + string(c.fn) + "\x00" + c.dir + "\x00" + d + "\x00" + x.Sel.Name) {
						c.ci.refs = append(c.ci.refs, refSite{site: c.site(x.Pos()), toDir: d, name: x.Sel.Name})
					}
				}
			}
		}
	case *ast.CallExpr:
		c.call(x)
	case *ast.BasicLit:
		if c.sql && x.Kind == token.STRING {
			if s, err := strconv.Unquote(x.Value); err == nil {
				c.sqlText(x.Pos(), s)
			}
		}
	case *ast.BinaryExpr:
		// "SELECT ... " + "FROM items" is one statement, not two fragments.
		if c.sql && x.Op == token.ADD {
			if s, ok := constStringIn(x, c.ci.pkgStrings[c.dir]); ok {
				c.sqlText(x.Pos(), s)
				return false
			}
		}
	case *ast.Ident:
		if ast.IsExported(x.Name) {
			c.ci.useIdent(c.dir, x.Name, c.fn)
		}
		// A package-level string constant used by name.
		if c.sql && c.fn != "" {
			if s, ok := c.ci.pkgStrings[c.dir][x.Name]; ok {
				c.sqlText(x.Pos(), s)
			}
		}
	}
	return true
}

// visitType records the other packages a type expression names: the parameters
// and results of a function, the fields of a struct. They are kept apart from
// refs, which the templates layer reads as "this function uses that component".
func (c *collector) visitType(n ast.Node) bool {
	if x, ok := n.(*ast.SelectorExpr); ok {
		if id, ok := x.X.(*ast.Ident); ok {
			if path, ok := c.imps[id.Name]; ok {
				if d, ok := moduleDir(c.ci.module, path); ok && d != c.dir {
					if c.firstOnce("tref\x00" + string(c.fn) + "\x00" + c.dir + "\x00" + d + "\x00" + x.Sel.Name) {
						c.ci.typeRefs = append(c.ci.typeRefs, refSite{site: c.site(x.Pos()), toDir: d, name: x.Sel.Name})
					}
				}
			}
		}
	}
	return true
}

// sqlText records text when it looks like it may be a SQL statement.
// Package-level initializers are skipped: a constant is attributed to the
// functions that use it.
func (c *collector) sqlText(pos token.Pos, text string) {
	if c.fn == "" || !sqlHintRe.MatchString(text) {
		return
	}
	if c.firstOnce("sql\x00" + string(c.fn) + "\x00" + text) {
		c.ci.sql = append(c.ci.sql, sqlText{site: c.site(pos), text: text})
	}
}

// sqlHintRe is a cheap filter: a string that names none of the keywords that
// introduce a table is not worth keeping.
var sqlHintRe = regexp.MustCompile(`(?is)\b(from|join|into|update|truncate)\b`)

// holdsSQL reports whether packages in dir are expected to talk to the
// database: the storage and business layers.
func (ci *codeIndex) holdsSQL(dir string) bool {
	switch ci.prof.Classify(dir) {
	case "storage", "business":
		return true
	}
	return false
}

// flattenSelector turns a.b.c into (a, [b c]). The root is nil when the chain
// does not start at a plain identifier.
func flattenSelector(x *ast.SelectorExpr) (*ast.Ident, []string) {
	var names []string
	var cur ast.Expr = x
	for {
		switch e := cur.(type) {
		case *ast.SelectorExpr:
			names = append([]string{e.Sel.Name}, names...)
			cur = e.X
		case *ast.ParenExpr:
			cur = e.X
		case *ast.Ident:
			return e, names
		default:
			return nil, nil
		}
	}
}

// configReaders are the dynamic helpers of the loaded configuration: with a literal
// key they read one setting, or (Unmarshal, and a group key) everything below it.
var configReaders = map[string]bool{"Get": true, "String": true, "Unmarshal": true}

// configReceiver reports whether x is, by naming convention, a loaded config value:
// a variable called cfg or conf, or a field called Config (rt.Config). Without type
// information the receiver can only be recognised by its name.
func configReceiver(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.Ident:
		return configRoots[e.Name]
	case *ast.SelectorExpr:
		return e.Sel.Name == "Config"
	}
	return false
}

func (c *collector) call(call *ast.CallExpr) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	// rt.Config.Get("a.b"), cfg.Unmarshal("a", &v): the helpers as methods.
	if configReaders[sel.Sel.Name] && configReceiver(sel.X) {
		if key, ok := stringArg(call, 0); ok && c.firstOnce("cfgcall\x00"+string(c.fn)+"\x00"+c.dir+"\x00"+sel.Sel.Name+"\x00"+key) {
			c.ci.cfgCalls = append(c.ci.cfgCalls, cfgCall{site: c.site(call.Pos()), callee: sel.Sel.Name, key: key})
		}
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	path, ok := c.imps[id.Name]
	if !ok {
		return
	}
	switch {
	case path == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv"):
		if name, ok := stringArg(call, 0); ok && c.firstOnce("env\x00"+string(c.fn)+"\x00"+c.dir+"\x00"+name) {
			c.ci.envReads = append(c.ci.envReads, envRead{site: c.site(call.Pos()), name: name})
		}
	case sel.Sel.Name == "AddTool" && guessPkgName(path) == "mcp":
		c.addTool(call)
	default:
		if d, ok := moduleDir(c.ci.module, path); ok && c.ci.pkgs[d] != nil && c.ci.pkgs[d].name() == "config" &&
			(sel.Sel.Name == "Get" || sel.Sel.Name == "Unmarshal") {
			if key, ok := stringArg(call, 0); ok && c.firstOnce("cfgcall\x00"+string(c.fn)+"\x00"+c.dir+"\x00"+sel.Sel.Name+"\x00"+key) {
				c.ci.cfgCalls = append(c.ci.cfgCalls, cfgCall{site: c.site(call.Pos()), callee: sel.Sel.Name, key: key})
			}
		}
	}
}

// addTool records mcp.AddTool(srv, &mcp.Tool{Name: "x", Description: "..."}, handler).
func (c *collector) addTool(call *ast.CallExpr) {
	if len(call.Args) < 2 {
		return
	}
	pos := c.fset.Position(call.Pos())
	lit := toolLiteral(call.Args[1])
	if lit == nil {
		c.ci.toolWarns = append(c.ci.toolWarns, fmt.Sprintf("%s:%d: mcp.AddTool without a &mcp.Tool{...} literal, tool not indexed", pos.Filename, pos.Line))
		return
	}
	var tr toolReg
	tr.site = c.site(call.Pos())
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			tr.name, _ = constString(kv.Value)
		case "Description":
			tr.description, _ = constString(kv.Value)
		}
	}
	if tr.name == "" {
		c.ci.toolWarns = append(c.ci.toolWarns, fmt.Sprintf("%s:%d: mcp.AddTool with a Name that is not a string constant, tool not indexed", pos.Filename, pos.Line))
		return
	}
	if len(call.Args) >= 3 {
		if fl, ok := call.Args[2].(*ast.FuncLit); ok {
			tr.input, tr.emptyInput = handlerInput(fl)
		}
	}
	c.ci.tools = append(c.ci.tools, tr)
}

func toolLiteral(e ast.Expr) *ast.CompositeLit {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	lit, _ := e.(*ast.CompositeLit)
	return lit
}

// handlerInput returns the name of the third parameter's type of a tool
// handler literal, or reports that it is struct{}.
func handlerInput(fl *ast.FuncLit) (name string, empty bool) {
	var types []ast.Expr
	for _, f := range fl.Type.Params.List {
		n := len(f.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			types = append(types, f.Type)
		}
	}
	if len(types) < 3 {
		return "", false
	}
	switch t := types[2].(type) {
	case *ast.Ident:
		return t.Name, false
	case *ast.StructType:
		return "", t.Fields == nil || len(t.Fields.List) == 0
	}
	return "", false
}

// stringArg returns argument i of call when it is a string constant.
func stringArg(call *ast.CallExpr, i int) (string, bool) {
	if len(call.Args) <= i {
		return "", false
	}
	return constString(call.Args[i])
}

// constString evaluates a string literal, or a + concatenation of them.
func constString(e ast.Expr) (string, bool) { return constStringIn(e, nil) }

// constStringIn is constString where an identifier may name a string constant
// of env (the package-level constants seen so far).
func constStringIn(e ast.Expr, env map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return constStringIn(x.X, env)
	case *ast.Ident:
		s, ok := env[x.Name]
		return s, ok
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, ok1 := constStringIn(x.X, env)
		r, ok2 := constStringIn(x.Y, env)
		return l + r, ok1 && ok2
	}
	return "", false
}

// structFields lists the named fields of a struct type; inline struct types
// carry their own fields. Embedded fields are skipped.
func structFields(fset *token.FileSet, st *ast.StructType) []fieldInfo {
	var out []fieldInfo
	if st.Fields == nil {
		return nil
	}
	for _, f := range st.Fields.List {
		var tag reflect.StructTag
		if f.Tag != nil {
			if s, err := strconv.Unquote(f.Tag.Value); err == nil {
				tag = reflect.StructTag(s)
			}
		}
		for _, n := range f.Names {
			fi := fieldInfo{name: n.Name, tag: tag}
			if inner, ok := f.Type.(*ast.StructType); ok {
				fi.typ = "struct"
				fi.inline = structFields(fset, inner)
			} else {
				fi.typ = render(fset, f.Type)
			}
			out = append(out, fi)
		}
	}
	return out
}

// idAllocator keeps IDs unique within a run. Two declarations can legitimately
// share a name (several `init` functions, or the same function in files with
// different build tags); the second and later get a `#2`, `#3`, ... suffix in
// file and source order, which is stable for identical input.
type idAllocator struct{ seen map[quad.IRI]int }

func newIDAllocator() *idAllocator { return &idAllocator{seen: map[quad.IRI]int{}} }

func (a *idAllocator) alloc(id quad.IRI) quad.IRI {
	a.seen[id]++
	if n := a.seen[id]; n > 1 {
		return quad.IRI(fmt.Sprintf("%s#%d", string(id), n))
	}
	return id
}

// receiverName returns the receiver's type name without `*` or type
// parameters, or "" for a plain function.
func receiverName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}
	t := d.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return "_"
		}
	}
}

// funcSignature renders the declaration without its body or doc comment.
func funcSignature(fset *token.FileSet, d *ast.FuncDecl) string {
	bare := *d
	bare.Body, bare.Doc = nil, nil
	return truncate(render(fset, &bare), maxSignatureRunes)
}

// typeSignature is `type Name` plus `struct`, `interface`, or the underlying
// type expression, so a struct's fields do not bloat the graph.
func typeSignature(fset *token.FileSet, ts *ast.TypeSpec) string {
	var b strings.Builder
	b.WriteString("type " + ts.Name.Name)
	if ts.TypeParams != nil {
		b.WriteString(typeParams(fset, ts.TypeParams))
	}
	if ts.Assign.IsValid() {
		b.WriteString(" =")
	}
	switch t := ts.Type.(type) {
	case *ast.StructType:
		b.WriteString(" struct")
	case *ast.InterfaceType:
		b.WriteString(" interface")
	default:
		b.WriteString(" " + render(fset, t))
	}
	return truncate(b.String(), maxSignatureRunes)
}

// typeParams renders a type parameter list as `[K comparable, V any]`.
func typeParams(fset *token.FileSet, fl *ast.FieldList) string {
	parts := make([]string, 0, len(fl.List))
	for _, f := range fl.List {
		names := make([]string, 0, len(f.Names))
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, strings.Join(names, ", ")+" "+render(fset, f.Type))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// render prints a node on one line.
func render(fset *token.FileSet, n any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

// firstSentence returns the first sentence of a comment: up to the first
// period followed by white space or the end, whitespace collapsed, capped.
func firstSentence(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	for i := 0; i < len(text); i++ {
		if text[i] == '.' && (i+1 == len(text) || text[i+1] == ' ') {
			text = text[:i+1]
			break
		}
	}
	return truncate(text, maxDocRunes)
}

// truncate caps s at max runes, ending in an ellipsis when it cuts.
func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

func countLines(src []byte) int {
	if len(src) == 0 {
		return 0
	}
	n := bytes.Count(src, []byte{'\n'})
	if src[len(src)-1] != '\n' {
		n++
	}
	return n
}

// pathDir is the directory of a repo-relative file, "." for the root.
func pathDir(rel string) string {
	d := filepath.ToSlash(filepath.Dir(rel))
	if d == "" {
		return "."
	}
	return d
}

func importPath(module, dir string) string {
	if dir == "." {
		return module
	}
	return module + "/" + dir
}

// moduleDir maps an import path inside the module to its repo-relative directory.
func moduleDir(module, path string) (string, bool) {
	if path == module {
		return ".", true
	}
	if rest, ok := strings.CutPrefix(path, module+"/"); ok {
		return rest, true
	}
	return "", false
}

// readModulePath returns the module path declared by a go.mod file.
func readModulePath(goMod string) (string, error) {
	f, err := os.Open(goMod)
	if err != nil {
		return "", fmt.Errorf("find the module root: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			path := strings.TrimSpace(rest)
			if i := strings.Index(path, "//"); i >= 0 {
				path = strings.TrimSpace(path[:i])
			}
			if unq, err := strconv.Unquote(path); err == nil {
				path = unq
			}
			if path != "" {
				return path, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s has no module line", goMod)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
