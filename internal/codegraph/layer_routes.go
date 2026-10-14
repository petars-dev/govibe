package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The route table is read through internal/routeinfo, the light package the app
// binary shares for `inspect --json`; these names keep the layer's own vocabulary.
type (
	// runtimeRoute is one route as the running router reports it.
	runtimeRoute = routeinfo.Route
	// handlerRef is a function as the Go runtime describes it.
	handlerRef = routeinfo.Ref
)

// anyMethod is the method token of a route registered for every method.
const anyMethod = routeinfo.AnyMethod

// routeTable is the router's routes, or the fact that there is no router.
type routeTable struct {
	absent bool
	routes []runtimeRoute
}

// routeTable returns the routes, once per run.
func (w *world) routeTable() (*routeTable, error) {
	return w.routesV.get(func() (*routeTable, error) {
		switch {
		case w.x.opts.RouteSource != nil:
			rs, err := w.x.opts.RouteSource()
			if err != nil {
				return nil, err
			}
			return &routeTable{routes: sortRoutes(rs)}, nil
		case w.x.opts.Router != nil:
			return &routeTable{routes: routeinfo.Walk(w.x.opts.Router)}, nil
		}
		return &routeTable{absent: true}, nil
	})
}

func sortRoutes(rs []runtimeRoute) []runtimeRoute { return routeinfo.Sort(rs) }

type routesLayer struct{}

func (routesLayer) name() string   { return "routes" }
func (routesLayer) deps() []string { return []string{"code"} }

// fingerprint hashes the route table, which is fixed once the router is wired:
// it changes only across a restart, when the whole graph is rebuilt anyway.
func (routesLayer) fingerprint(w *world) (string, error) {
	rt, err := w.routeTable()
	if err != nil {
		return "", err
	}
	if rt.absent {
		return "no-router", nil
	}
	h := sha256.New()
	for _, r := range rt.routes {
		fmt.Fprintf(h, "%s %s %s|%s:%d", r.Method, r.Path, r.Handler.Symbol, r.Handler.File, r.Handler.Line)
		for _, m := range r.MW {
			fmt.Fprintf(h, " %s|%s:%d", m.Symbol, m.File, m.Line)
		}
		fmt.Fprintln(h)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func (routesLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	rt, err := w.routeTable()
	if err != nil {
		return nil, err
	}
	if rt.absent {
		return &layerOutput{skipped: "no router is wired (offline run); routes left as they were"}, nil
	}
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}

	facts, mods := w.x.opts.Build, w.modules()
	set := newQuadSet("routes")
	mwSeen := map[quad.IRI]bool{}
	fwMW := frameworkMiddleware(rt.routes, facts)
	for _, r := range rt.routes {
		id := routeID(r.Method, r.Path)
		set.add(id, PredType, KindRoute)
		set.str(id, PredName, r.Method+" "+r.Path)
		set.str(id, PredMethod, r.Method)
		set.str(id, PredPath, r.Path)
		set.flag(id, PredDevOnly, r.Path == "/dev" || strings.HasPrefix(r.Path, "/dev/"))
		// Whose route it is: who registered it, not whose function serves it (an app can
		// mount a framework handler, and the framework mounts stdlib ones).
		set.str(id, PredOrigin, routeOrigin(facts, r))
		if fi := w.resolveFunc(ci, r.Handler); fi != nil {
			set.add(id, PredHandledBy, fi.id)
		} else if sym := externalSymbol(ci, r.Handler); sym != "" {
			// Not application code (a stdlib or library handler, or a wrapper):
			// say what the runtime sees instead of inventing a link, and point at the
			// external node when the symbol belongs to a module of the build.
			set.str(id, PredHandler, sym)
			if path, name, ok := externalSpec(mods, sym); ok {
				set.add(id, PredHandledByExt, mods.leafRef(path, name))
			}
		}
		used := map[quad.IRI]bool{}
		for _, m := range r.MW {
			mid, fi, name := w.middlewareNode(ci, m)
			if mid == "" || used[mid] {
				continue
			}
			used[mid] = true
			set.add(id, PredUsesMW, mid)
			if !mwSeen[mid] {
				mwSeen[mid] = true
				set.add(mid, PredType, KindMiddleware)
				set.str(mid, PredName, name)
				set.str(mid, PredOrigin, w.middlewareOrigin(ci, mods, m, fi, fwMW))
				if fi != nil {
					set.add(mid, PredImplementedBy, fi.id)
				}
			}
		}
	}
	return &layerOutput{quads: set.quads}, nil
}

// mwKey identifies a middleware function as the runtime describes it.
func mwKey(ref handlerRef) string { return fmt.Sprintf("%s|%s:%d", ref.Symbol, ref.File, ref.Line) }

// frameworkMiddleware is the set of middleware the framework itself runs: those on the
// chain of a route the framework registered. The application cannot add middleware to
// the framework's routes, so whatever they run is the standard stack.
func frameworkMiddleware(routes []runtimeRoute, facts BuildFacts) map[string]bool {
	out := map[string]bool{}
	for _, r := range routes {
		if routeOrigin(facts, r) != OriginFramework {
			continue
		}
		for _, m := range r.MW {
			out[mwKey(m)] = true
		}
	}
	return out
}

// middlewareOrigin says whose a middleware is: the framework's when it is on the
// framework's own routes or lives in the framework's module, the application's
// otherwise (its own function, or a library it chose to apply).
func (w *world) middlewareOrigin(ci *codeIndex, mods *moduleTable, ref handlerRef, fi *funcInfo, fwMW map[string]bool) string {
	if fi != nil {
		return OriginApp
	}
	if fwMW[mwKey(ref)] {
		return OriginFramework
	}
	if pkgPath, _, _, ok := parseRuntimeSymbol(externalSymbol(ci, ref)); ok && mods.isFramework(pkgPath) {
		return OriginFramework
	}
	return OriginApp
}

// externalSpec is the package and function (`Name` or `Recv.Name`) of a symbol the runtime
// reports, when it belongs to a module the build knows; the standard library has none.
func externalSpec(mods *moduleTable, sym string) (importPath, name string, ok bool) {
	pkgPath, recv, name, ok := parseRuntimeSymbol(sym)
	if !ok || mods.moduleOf(pkgPath) == "" {
		return "", "", false
	}
	if recv != "" {
		name = recv + "." + name
	}
	return pkgPath, name, true
}

// resolveFunc maps a runtime function onto a declared function of the code
// index by file and line: a closure resolves to the function that contains it.
// The method-value wrappers the compiler generates (x.Method-fm) have no source
// of their own, so those resolve by name.
func (w *world) resolveFunc(ci *codeIndex, ref handlerRef) *funcInfo {
	if ref.File != "" && ref.File != "<autogenerated>" {
		if rel, ok := w.sourceRel(ci, ref.File); ok {
			return ci.funcAt(rel, ref.Line)
		}
		return nil
	}
	if strings.HasSuffix(ref.Symbol, "-fm") {
		if pkgPath, recv, name, ok := parseRuntimeSymbol(ref.Symbol); ok {
			if dir, ok := moduleDir(ci.module, pkgPath); ok {
				return ci.funcNamed(dir, recv, name)
			}
		}
	}
	return nil
}

// sourceRel maps a source path reported by the runtime onto a file of the code
// index. The runtime reports the path the binary was compiled from: absolute
// (possibly a different checkout than the one being indexed), or, with
// -trimpath, prefixed by the module path. The compile-time root is recovered
// from where this very package was compiled, which holds in both cases.
func (w *world) sourceRel(ci *codeIndex, file string) (string, bool) {
	if ci.files[file] != nil {
		return file, true // already repo-relative
	}
	br := w.buildRoot(ci)
	if br == "" || !strings.HasPrefix(file, br) {
		return "", false
	}
	rel := strings.TrimPrefix(file, br)
	if ci.files[rel] == nil {
		return "", false
	}
	return rel, true
}

// buildRoot is the directory (with a trailing slash) the running binary's
// module was compiled in, as the runtime spells paths.
func (w *world) buildRoot(ci *codeIndex) string {
	if w.x.opts.buildRoot != nil {
		return *w.x.opts.buildRoot
	}
	if h := w.x.opts.RootHint; h.File != "" {
		if root := rootFromHint(ci.module, h); root != "" {
			return root
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	pkg := reflect.TypeOf(world{}).PkgPath()
	rel, ok := strings.CutPrefix(pkg, ci.module+"/")
	if !ok {
		return "" // the indexer is not part of the module being indexed
	}
	dir := filepath.ToSlash(filepath.Dir(file))
	root, ok := strings.CutSuffix(dir, rel)
	if !ok {
		return ""
	}
	return root
}

// rootFromHint recovers the directory (with a trailing slash) a module was compiled
// in from one of its functions: the function's package path says how far below the
// module root its file sits, and the file is spelled as the runtime spells it
// (absolute, or module-prefixed under -trimpath). It returns "" when the function
// is not in the module.
func rootFromHint(module string, h handlerRef) string {
	slash := strings.LastIndex(h.Symbol, "/")
	dot := strings.Index(h.Symbol[slash+1:], ".")
	if dot < 0 {
		return ""
	}
	pkgPath := h.Symbol[:slash+1+dot]
	dir := path.Dir(h.File)
	switch {
	case pkgPath == module:
		return dir + "/"
	case strings.HasPrefix(pkgPath, module+"/"):
		root, ok := strings.CutSuffix(dir, "/"+strings.TrimPrefix(pkgPath, module+"/"))
		if !ok {
			return ""
		}
		return root + "/"
	}
	return ""
}

// middlewareNode returns the node ID, the code func (when it is application
// code) and the display name of a middleware.
func (w *world) middlewareNode(ci *codeIndex, ref handlerRef) (quad.IRI, *funcInfo, string) {
	if ref.Symbol == "" && ref.File == "" {
		return "", nil, ""
	}
	if fi := w.resolveFunc(ci, ref); fi != nil {
		key := fi.dir + "." + fi.name
		if fi.recv != "" {
			key = fi.dir + "." + fi.recv + "." + fi.name
		}
		return mwID(key), fi, funcDisplayName(fi)
	}
	sym := externalSymbol(ci, ref)
	if sym == "" {
		return "", nil, ""
	}
	name := sym
	if i := strings.LastIndex(sym, "."); i >= 0 {
		name = sym[i+1:]
	}
	return mwID(sym), nil, name
}

func funcDisplayName(fi *funcInfo) string {
	if fi.recv != "" {
		return fi.recv + "." + fi.name
	}
	return fi.name
}

// externalSymbol names a function that is not application code. It is the
// runtime's symbol, except when that merely names the application function the
// callee was inlined into (Go names an inlined closure after its caller, e.g.
// `app/routes.NewRouter.func2.StripPrefix`): the source file then says where
// the function really lives, so the label is built from that instead.
func externalSymbol(ci *codeIndex, ref handlerRef) string {
	sym := cleanSymbol(ref.Symbol)
	if sym == "" {
		return ""
	}
	if strings.HasPrefix(sym, ci.module+"/") || strings.HasPrefix(sym, ci.module+".") {
		if pkg := externalPackage(ref.File); pkg != "" {
			return pkg + "." + sym[strings.LastIndex(sym, ".")+1:]
		}
	}
	return sym
}

// externalPackage is the import path of the package a source file outside the
// module belongs to: from GOROOT (.../src/net/http/x.go -> net/http) or the
// module cache (.../pkg/mod/github.com/a/b@v1.2.3/c/x.go -> github.com/a/b/c).
func externalPackage(file string) string {
	dir := path.Dir(file)
	if i := strings.Index(dir, "/pkg/mod/"); i >= 0 {
		rest := dir[i+len("/pkg/mod/"):]
		if at := strings.Index(rest, "@"); at >= 0 {
			if slash := strings.Index(rest[at:], "/"); slash >= 0 {
				return rest[:at] + rest[at+slash:]
			}
			return rest[:at]
		}
		return rest
	}
	if i := strings.Index(dir, "/src/"); i >= 0 {
		return dir[i+len("/src/"):]
	}
	if !strings.HasPrefix(dir, "/") && dir != "." {
		return dir // -trimpath: already an import path
	}
	return ""
}

// typeArgs matches the instantiated type arguments the runtime prints for
// generic code: `Map[...]`, `(*Set[go.shape.int]).Add`.
var typeArgs = regexp.MustCompile(`\[[^\]]*\]`)

// closureName matches the synthetic names of closures and inlining counters.
var closureName = regexp.MustCompile(`^(func)?[0-9]+$`)

var closureSuffix = regexp.MustCompile(`(\.func[0-9]+)+(\.[0-9]+)*$|\.[0-9]+$`)

// cleanSymbol reduces a runtime symbol to the function that contains a
// closure: no method-value suffix, no .funcN, no inlining counters.
func cleanSymbol(sym string) string {
	sym = strings.TrimSuffix(sym, "-fm")
	for {
		cut := closureSuffix.ReplaceAllString(sym, "")
		if cut == sym || cut == "" {
			return sym
		}
		sym = cut
	}
}

// parseRuntimeSymbol splits `pkg/path.(*Recv).Name` or `pkg/path.Recv.Name`
// or `pkg/path.Name`. It only understands plain declared functions and
// methods, not closures.
func parseRuntimeSymbol(sym string) (pkgPath, recv, name string, ok bool) {
	sym = typeArgs.ReplaceAllString(strings.TrimSuffix(sym, "-fm"), "")
	slash := strings.LastIndex(sym, "/")
	dot := strings.Index(sym[slash+1:], ".")
	if dot < 0 {
		return "", "", "", false
	}
	pkgPath = sym[:slash+1+dot]
	rest := sym[slash+1+dot+1:]
	switch {
	case strings.HasPrefix(rest, "(*"):
		end := strings.Index(rest, ").")
		if end < 0 {
			return "", "", "", false
		}
		recv, name = rest[2:end], rest[end+2:]
	case strings.Contains(rest, "."):
		i := strings.Index(rest, ".")
		recv, name = rest[:i], rest[i+1:]
	default:
		name = rest
	}
	if name == "" || strings.Contains(name, ".") || closureName.MatchString(name) {
		return "", "", "", false
	}
	return pkgPath, recv, name, true
}
