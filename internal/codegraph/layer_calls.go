package codegraph

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cayleygraph/quad"
	"golang.org/x/tools/go/packages"
)

const callsLayerName = "calls"

// callsTimeout bounds one type-check of the module. `go list` compiles what the
// module imports the first time, so a cold build cache is slow; a warm one takes
// about a second for this repository.
const callsTimeout = 3 * time.Minute

// Interface calls: a call through an interface declared in this module is linked
// to every method of the module's types that could be on the other end. The more
// candidates, the less one edge says, and past maxImplementers the call is not
// expanded at all (it would link nearly everything to nearly everything).
const (
	methodDispatch  = "interface_dispatch" // annotation method of an interface call (the edge's own confidence says how sure)
	maxImplementers = 24
)

// dispatchConfidence is how sure one edge of an interface call is, by how many
// types it could land on: 80 for the only one, then 70, 60, 50 and 30.
func dispatchConfidence(candidates int) int {
	switch {
	case candidates <= 1:
		return 80
	case candidates == 2:
		return 70
	case candidates <= 4:
		return 60
	case candidates <= 8:
		return 50
	}
	return 30
}

// callsLayer indexes which function calls which: `func --calls--> func`. Unlike
// the other layers it type-checks the module (golang.org/x/tools/go/packages: one
// `go list` plus the type checker over the module's own sources, about a second),
// because a call cannot be resolved from syntax alone (`s.store.List()` is a
// method call on whatever type `store` has). That is why it is opt-in
// (Options.Calls), and why it needs the Go toolchain.
//
// What an edge means:
//
//   - A call whose target the compiler resolves (a function, a method on a concrete
//     type, a method expression) is stored as a plain edge: certain, up to the
//     program running that line.
//   - A call through an interface declared in this module is stored once per
//     module type that could implement it (class-hierarchy analysis), each with an
//     annotation (the same device as an inferred database relation) that gives the
//     confidence and the interface it goes through.
//   - A closure has no node, so a call inside one belongs to the declared function
//     that contains it, as a handler closure's route does.
//   - A call out of the module is bounded, so the graph says what the project takes
//     from outside without becoming a map of it: a call into the framework's public
//     packages is an edge to one external leaf per function (`web.JSON`,
//     `Config.Get`), a call into the framework's internal/ collapses to one leaf for
//     all of it, a call into a library is one leaf per package, and the standard
//     library is not recorded. Those edges are `calls_external` and the leaves carry
//     the module and its version.
//   - The module is type-checked with the dev build tag (`govibe_dev` unless the
//     binary says otherwise), so a file only a dev build compiles is seen; the layer's
//     note states the tags.
//   - Not seen: calls through function values (a callback, a handler held in a
//     variable or a map), reflection, calls from package-level initializers, calls
//     through interfaces the module does not declare (http.Handler), and calls
//     into generated templ output.
type callsLayer struct{}

func (callsLayer) name() string   { return callsLayerName }
func (callsLayer) deps() []string { return []string{"code"} }
func (callsLayer) optIn() string {
	return "opt-in: it type-checks the module. Set GRAPH_CALLS=true, run `govibe graph index --calls`, or call graph_reindex with layers [\"calls\"]"
}

// fingerprint covers what type-checking reads besides the Go sources the code
// layer already stands for: the dependency checksums.
func (callsLayer) fingerprint(w *world) (string, error) {
	var files []sourceFile
	if sf, ok := statRegular(w.root, "go.sum", 64<<20); ok {
		files = append(files, sf)
	}
	return fingerprint(files), nil
}

func (callsLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	idx, err := w.calls()
	if err != nil {
		return nil, err
	}
	set := newQuadSet(callsLayerName)
	mods := w.modules()
	seenExt := map[callKey]bool{}
	for _, c := range idx.external {
		leaf := mods.leafRef(c.importPath, c.name)
		if k := (callKey{c.from, leaf}); !seenExt[k] { // the framework's internals are one leaf
			seenExt[k] = true
			set.add(c.from, PredCallsExt, leaf)
		}
	}
	keys := make([]callKey, 0, len(idx.edges))
	for k := range idx.edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].from != keys[j].from {
			return keys[i].from < keys[j].from
		}
		return keys[i].to < keys[j].to
	})
	for _, k := range keys {
		e := idx.edges[k]
		set.add(e.from, PredCalls, e.to)
		if e.method == "" {
			continue // a plain call: no annotation, certain
		}
		aid := annID(e.from, PredCalls, e.to)
		set.add(aid, PredType, KindAnnotation)
		set.add(aid, PredSubject, e.from)
		set.str(aid, PredPredicate, string(PredCalls))
		set.add(aid, PredObject, e.to)
		set.str(aid, PredMethod, e.method)
		set.num(aid, PredConfidence, e.confidence)
		if e.via != "" {
			set.add(aid, PredVia, e.via)
		}
	}
	return &layerOutput{quads: set.quads, warnings: idx.warnings, note: "type-checked with -tags " + idx.tags}, nil
}

// callKey is one edge: a caller and the function it calls.
type callKey struct{ from, to quad.IRI }

// callEdge is what the layer knows about one edge. A plain call has no method.
type callEdge struct {
	from, to   quad.IRI
	method     string   // "" for a plain call, else methodDispatch
	confidence int      // 100 for a plain call
	via        quad.IRI // the interface type a dispatched call goes through, when it has a node
}

// callIndex is the result of type-checking the module once.
type callIndex struct {
	edges    map[callKey]*callEdge
	external []extCall // calls out of the module, bounded (see callsLayer), sorted
	warnings []string
	packages int    // module packages that were type-checked
	tags     string // the build tags it was type-checked with
}

// extCall is a call out of the module: the caller, and the package and the function
// (`Name` or `Recv.Name`) it calls there.
type extCall struct {
	from             quad.IRI
	importPath, name string
}

// defaultCallTags are the build tags the module is type-checked with when the running
// binary did not say: the dev tag, so the files only a dev build compiles are seen.
const defaultCallTags = "govibe_dev"

// calls returns the call index, built once per run.
func (w *world) calls() (*callIndex, error) {
	return w.callIdx.get(func() (*callIndex, error) {
		ci, err := w.code()
		if err != nil {
			return nil, fmt.Errorf("code index: %w", err)
		}
		tags := w.x.opts.Build.Tags
		if tags == "" {
			tags = defaultCallTags
		}
		// Type-checking the module is the one expensive step, and two layers read its result
		// (calls, and externals for the leaves). It depends on the Go sources, the dependency
		// checksums and the tags, so it is reused across runs until one of them changes, and an
		// edit to a template or a route does not repeat it.
		cfp, err := w.codeFingerprint()
		if err != nil {
			return nil, err
		}
		fp := cfp + "|" + tags
		if sf, ok := statRegular(w.root, "go.sum", 64<<20); ok {
			fp += "|" + fingerprint([]sourceFile{sf})
		}
		if c := w.x.callCache; !w.force && c.idx != nil && c.fp == fp {
			return c.idx, nil
		}
		idx, err := buildCallIndex(w.ctx, w.root, ci, w.modules(), tags)
		if err != nil {
			return nil, err
		}
		w.x.callCache.fp, w.x.callCache.idx = fp, idx
		return idx, nil
	})
}

// buildCallIndex loads and type-checks the module and records the calls between
// its declared functions.
func buildCallIndex(ctx context.Context, root string, ci *codeIndex, mods *moduleTable, tags string) (*callIndex, error) {
	ctx, cancel := context.WithTimeout(ctx, callsTimeout)
	defer cancel()

	cfg := &packages.Config{
		Context: ctx,
		Dir:     root,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Tests: false,
	}
	if tags != "" {
		cfg.BuildFlags = []string{"-tags=" + tags}
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("calls layer: type-checking the module was cut short: %w", ctx.Err())
		}
		return nil, fmt.Errorf("calls layer: it needs the Go toolchain (`go list` failed): %w", err)
	}

	idx := &callIndex{edges: map[callKey]*callEdge{}, tags: tags}
	r := &callResolver{ci: ci, idx: idx, mods: mods, byPkg: map[string][]*types.Named{}, expanded: map[string]bool{}, ext: map[extCall]bool{}}

	var mod []*packages.Package
	for _, p := range pkgs {
		if _, ok := moduleDir(ci.module, p.PkgPath); ok {
			mod = append(mod, p)
		}
	}
	if len(mod) == 0 {
		return nil, errors.New("calls layer: `go list ./...` found no package of the module")
	}
	sort.Slice(mod, func(i, j int) bool { return mod[i].PkgPath < mod[j].PkgPath })

	// What did not type-check is reported, not hidden: its calls are missing or
	// partial, which an answer built on them must not read as "no calls".
	bad := 0
	for _, p := range mod {
		if len(p.Errors) == 0 {
			continue
		}
		bad++
		first := firstLine(p.Errors[0].Error())
		more := ""
		if n := len(p.Errors); n > 1 {
			more = fmt.Sprintf(" (and %d more)", n-1)
		}
		idx.warnings = append(idx.warnings, fmt.Sprintf("%s did not type-check cleanly: %s%s", p.PkgPath, first, more))
	}
	if bad == len(mod) {
		return nil, fmt.Errorf("calls layer: none of the %d packages type-checked: %s", len(mod), summarize(idx.warnings))
	}

	for _, p := range mod {
		if p.Types != nil {
			r.collect(p)
		}
	}
	for _, p := range mod {
		if p.TypesInfo == nil || p.Fset == nil {
			continue
		}
		idx.packages++
		for _, f := range p.Syntax {
			r.file(root, p, f)
		}
	}
	sort.Strings(idx.warnings)
	for c := range r.ext {
		idx.external = append(idx.external, c)
	}
	sort.Slice(idx.external, func(i, j int) bool {
		a, b := idx.external[i], idx.external[j]
		if a.from != b.from {
			return a.from < b.from
		}
		if a.importPath != b.importPath {
			return a.importPath < b.importPath
		}
		return a.name < b.name
	})
	return idx, nil
}

// callResolver turns the type checker's answers into edges between the nodes of
// the code index.
type callResolver struct {
	ci   *codeIndex
	idx  *callIndex
	mods *moduleTable
	ext  map[extCall]bool // calls out of the module, once each

	// byPkg are the module's named, non-interface, non-generic types by package
	// path: the candidates for the other end of an interface call.
	byPkg    map[string][]*types.Named
	expanded map[string]bool // interface methods already reported as too wide
}

// collect records the module's concrete types a call through an interface may
// land on.
func (r *callResolver) collect(p *packages.Package) {
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		n, ok := tn.Type().(*types.Named)
		if !ok || n.TypeParams().Len() > 0 || types.IsInterface(n) {
			continue
		}
		r.byPkg[p.PkgPath] = append(r.byPkg[p.PkgPath], n)
	}
}

// file records the calls made by the declared functions of one file.
func (r *callResolver) file(root string, p *packages.Package, f *ast.File) {
	tf := p.Fset.File(f.Pos())
	if tf == nil {
		return
	}
	rel := relativeTo(root, tf.Name())
	if rel == "" || r.ci.files[rel] == nil {
		return // generated templ output, or a file the code layer does not index
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		caller := r.ci.funcDeclaredAt(rel, p.Fset.Position(fd.Pos()).Line, fd.Name.Name)
		if caller == nil {
			continue
		}
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				r.call(p.TypesInfo, caller.id, call)
			}
			return true
		})
	}
}

// call records the callee of one call expression, if the type checker knows it.
func (r *callResolver) call(info *types.Info, caller quad.IRI, call *ast.CallExpr) {
	fun := ast.Unparen(call.Fun)
	switch x := fun.(type) { // F[int](x): the generic function is what is called
	case *ast.IndexExpr:
		fun = ast.Unparen(x.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(x.X)
	}
	var fn *types.Func
	var sel *types.Selection
	switch x := fun.(type) {
	case *ast.Ident:
		fn, _ = info.Uses[x].(*types.Func)
	case *ast.SelectorExpr:
		if s := info.Selections[x]; s != nil {
			sel = s
			fn, _ = s.Obj().(*types.Func)
		} else {
			fn, _ = info.Uses[x.Sel].(*types.Func) // pkg.Func
		}
	}
	if fn == nil {
		return // a conversion, a builtin, or a call through a function value
	}
	fn = fn.Origin()

	if iface := abstractReceiver(fn, sel); iface != nil {
		if _, ours := moduleDir(r.ci.module, fn.Pkg().Path()); !ours {
			r.external(caller, fn) // an interface the framework or a library declares: not ours to expand
			return
		}
		r.dispatch(caller, fn, iface)
		return
	}
	if to, ok := r.nodeOf(fn); ok {
		r.add(caller, to, "", 100, "")
		return
	}
	r.external(caller, fn)
}

// external records a call out of the module, when it is to something worth a leaf: the
// framework (a function of its public API, or the one leaf for its internals) or a
// library. The standard library is not recorded.
func (r *callResolver) external(caller quad.IRI, fn *types.Func) {
	if fn.Pkg() == nil {
		return
	}
	path := fn.Pkg().Path()
	if r.mods.moduleOf(path) == "" {
		return // the standard library, or a package no module of the build explains
	}
	name := fn.Name()
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		if named, ok := t.(*types.Named); ok {
			name = named.Obj().Name() + "." + name
		}
	}
	if !r.mods.isFramework(path) {
		name = "" // a library is one leaf per package
	}
	r.ext[extCall{from: caller, importPath: path, name: name}] = true
}

// abstractReceiver is the interface a method call goes through, or nil when it
// is a call of a concrete function or method. Only interfaces the module
// declares are expanded: a call through http.Handler or error has nothing in
// this module worth linking to.
func abstractReceiver(fn *types.Func, sel *types.Selection) types.Type {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil || !types.IsInterface(sig.Recv().Type()) {
		return nil
	}
	if fn.Pkg() == nil {
		return nil
	}
	if sel != nil && types.IsInterface(sel.Recv()) {
		return sel.Recv() // the static type of the expression, which may add methods
	}
	return sig.Recv().Type()
}

// dispatch links a call through an interface to the methods of the module's types
// that implement it.
func (r *callResolver) dispatch(caller quad.IRI, method *types.Func, iface types.Type) {
	if _, ok := moduleDir(r.ci.module, method.Pkg().Path()); !ok {
		return
	}
	it, ok := iface.Underlying().(*types.Interface)
	if !ok || it.NumMethods() == 0 {
		return
	}
	var targets []*types.Func
	paths := make([]string, 0, len(r.byPkg))
	for path := range r.byPkg {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		for _, n := range r.byPkg[path] {
			ptr := types.NewPointer(n) // its method set contains the value type's
			if !types.Implements(ptr, it) {
				continue
			}
			s := types.NewMethodSet(ptr).Lookup(method.Pkg(), method.Name())
			if s == nil {
				continue
			}
			if m, ok := s.Obj().(*types.Func); ok {
				targets = append(targets, m.Origin())
			}
		}
	}
	// Every candidate counts toward how unsure the call is, the caller itself
	// included (a type may well hold another value of its own type); an edge to
	// the caller is then dropped, as a function calling itself is not an edge.
	var nodes []quad.IRI
	seen := map[quad.IRI]bool{}
	for _, m := range targets {
		if to, ok := r.nodeOf(m); ok && !seen[to] {
			seen[to] = true
			nodes = append(nodes, to)
		}
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > maxImplementers {
		key := types.TypeString(iface, nil) + "." + method.Name()
		if !r.expanded[key] {
			r.expanded[key] = true
			r.idx.warnings = append(r.idx.warnings, fmt.Sprintf("%s.%s has %d implementers: calls through it are not linked to any of them (the limit is %d)",
				types.TypeString(iface, func(p *types.Package) string { return p.Name() }), method.Name(), len(nodes), maxImplementers))
		}
		return
	}
	var via quad.IRI
	if named, ok := iface.(*types.Named); ok && named.Obj().Pkg() != nil {
		if dir, ok := moduleDir(r.ci.module, named.Obj().Pkg().Path()); ok && r.ci.typeNamed(dir, named.Obj().Name()) != nil {
			via = typeID(dir, named.Obj().Name())
		}
	}
	conf := dispatchConfidence(len(nodes))
	for _, to := range nodes {
		r.add(caller, to, methodDispatch, conf, via)
	}
}

// add records an edge, keeping the surer evidence when a pair has both a plain
// call and an interface call. A function calling itself is not an edge.
func (r *callResolver) add(from, to quad.IRI, method string, conf int, via quad.IRI) {
	if from == to {
		return
	}
	k := callKey{from, to}
	if prev := r.idx.edges[k]; prev != nil && prev.confidence >= conf {
		return
	}
	r.idx.edges[k] = &callEdge{from: from, to: to, method: method, confidence: conf, via: via}
}

// nodeOf maps a function or method the type checker found to its node in the code
// index, by package and name: a function the module declares and the code layer
// indexed.
func (r *callResolver) nodeOf(fn *types.Func) (quad.IRI, bool) {
	if fn.Pkg() == nil {
		return "", false
	}
	dir, ok := moduleDir(r.ci.module, fn.Pkg().Path())
	if !ok {
		return "", false
	}
	recv := ""
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		t := sig.Recv().Type()
		if p, ok := t.(*types.Pointer); ok {
			t = p.Elem()
		}
		named, ok := t.(*types.Named)
		if !ok {
			return "", false
		}
		recv = named.Obj().Name()
	}
	if fi := r.ci.funcNamed(dir, recv, fn.Name()); fi != nil {
		return fi.id, true
	}
	return "", false
}

// funcDeclaredAt returns the function declared at file:line with that name. A
// name alone is not enough: init may be declared many times in a package, and one
// function may exist in files for different build tags.
func (ci *codeIndex) funcDeclaredAt(file string, line int, name string) *funcInfo {
	for _, f := range ci.funcsByFile[file] {
		if f.line == line && f.name == name {
			return f
		}
	}
	return nil
}

// relativeTo returns abs relative to root with '/' separators, or "" when it is
// outside it. root has its symlinks resolved, so a file reached through the
// unresolved spelling of the same directory is tried once more resolved.
func relativeTo(root, abs string) string {
	for attempt := 0; attempt < 2; attempt++ {
		if rel, err := filepath.Rel(root, abs); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil || real == abs {
			return ""
		}
		abs = real
	}
	return ""
}
