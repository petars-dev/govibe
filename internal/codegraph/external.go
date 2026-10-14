package codegraph

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// Module is one dependency of the project's binary.
type Module struct {
	Path       string
	Version    string
	ReplaceDir string // where a local `replace` points, as written in go.mod; "" when there is none
}

// BuildFacts are what the running binary (or its `inspect --json`) knows about itself
// that the sources cannot say: which modules it was built from and at which versions,
// and which routes the framework registered before the application's did. Every field
// is optional: without them the indexer falls back to go.mod and to the route's path.
type BuildFacts struct {
	AppName         string
	AppModule       string
	FrameworkModule string // "" is this repository's own module path
	// FrameworkVersion is the framework's version in this build: "(devel)" for a local
	// replace, "" when unknown.
	FrameworkVersion string
	Modules          []Module
	Tags             string
	Trimpath         bool
	// FrameworkRoutes holds the key ("METHOD path") of every route the framework
	// registered before the application's Routes ran: the one origin fact only the
	// running process knows. A route under /dev or /_govibe is the framework's as well.
	FrameworkRoutes map[string]bool
}

// frameworkModule is the module path of the framework the project is built on.
func (b BuildFacts) frameworkModule() string {
	if b.FrameworkModule != "" {
		return b.FrameworkModule
	}
	return profile.FrameworkModule()
}

// moduleTable says which module an import path belongs to, at which version, and
// whether that module is the framework.
type moduleTable struct {
	app       string // the module being indexed
	framework string
	version   map[string]string
	order     []string // module paths, longest first
}

func newModuleTable(app string, facts BuildFacts, goMod string) *moduleTable {
	t := &moduleTable{app: app, framework: facts.frameworkModule(), version: map[string]string{}}
	// go.mod first: it is there offline. The build's own table, when there is one, is
	// exact and wins.
	requires, replaces := readGoModDeps(goMod)
	for path, v := range requires {
		t.version[path] = v
	}
	for path := range replaces {
		t.version[path] = "(devel)"
	}
	for _, m := range facts.Modules {
		v := m.Version
		if m.ReplaceDir != "" {
			v = "(devel)"
		}
		t.version[m.Path] = v
	}
	if facts.FrameworkVersion != "" {
		t.version[t.framework] = facts.FrameworkVersion
	}
	for path := range t.version {
		t.order = append(t.order, path)
	}
	sort.Slice(t.order, func(i, j int) bool {
		if len(t.order[i]) != len(t.order[j]) {
			return len(t.order[i]) > len(t.order[j])
		}
		return t.order[i] < t.order[j]
	})
	return t
}

// moduleOf is the module an import path belongs to: the longest module path that
// prefixes it, or "" when none is known.
func (t *moduleTable) moduleOf(importPath string) string {
	// What is inside the project's own module is the project's, even when its path sits under
	// a module it requires (the reference app lives under the framework's path).
	if t.app != "" && (importPath == t.app || strings.HasPrefix(importPath, t.app+"/")) {
		return ""
	}
	for _, m := range t.order {
		if importPath == m || strings.HasPrefix(importPath, m+"/") {
			return m
		}
	}
	return ""
}

func (t *moduleTable) versionOf(module string) string { return t.version[module] }

// isFramework reports whether an import path is the framework's. The framework is
// never its own dependency: in its own repository nothing is external to it.
func (t *moduleTable) isFramework(importPath string) bool {
	if t.app == t.framework {
		return false
	}
	// An application may live under the framework's own path (the reference app in
	// examples/hello does): what is inside its module is its own, whatever the prefix.
	if t.app != "" && (importPath == t.app || strings.HasPrefix(importPath, t.app+"/")) {
		return false
	}
	return importPath == t.framework || strings.HasPrefix(importPath, t.framework+"/")
}

// isInternal reports whether a framework import path is inside its internal/: not
// public API, collapsed to one leaf per package.
func (t *moduleTable) isInternal(importPath string) bool {
	rel := strings.TrimPrefix(strings.TrimPrefix(importPath, t.framework), "/")
	return rel == "internal" || strings.HasPrefix(rel, "internal/") || strings.Contains(rel, "/internal/")
}

// shortName is how a node names an external package: its path under its module
// (`web`, `config`), or the last element of the module for its root package.
func (t *moduleTable) shortName(importPath string) string {
	mod := t.moduleOf(importPath)
	if mod == "" {
		return importPath
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(importPath, mod), "/")
	if rel == "" {
		// The module's own name: its last element, with a major-version suffix kept beside what
		// it versions (`chi/v5`, not `v5`).
		parts := strings.Split(mod, "/")
		name := parts[len(parts)-1]
		if len(parts) > 1 && moduleMajorSuffix.MatchString(name) {
			name = parts[len(parts)-2] + "/" + name
		}
		return name
	}
	return rel
}

// internalLeafID is the ID of the one node every function in the framework's internal/
// collapses to.
func (t *moduleTable) internalLeafID() quad.IRI {
	return quad.IRI("ext:" + idKey(t.framework) + "/internal")
}

// pkgRef is the ID of the node of an external package.
func (t *moduleTable) pkgRef(importPath string) quad.IRI { return extID(importPath, "") }

// leafRef is the ID of the external node a use of name in the package at importPath points
// to: a function of the framework's public API, the single internal leaf for the framework's
// internals, or one leaf per package for a library. The layers that point at externals write
// only the edge, with this ID; the externals layer owns the nodes.
func (t *moduleTable) leafRef(importPath, name string) quad.IRI {
	switch {
	case t.isFramework(importPath) && t.isInternal(importPath):
		return t.internalLeafID()
	case t.isFramework(importPath) && name != "":
		return extID(importPath, name)
	}
	return t.pkgRef(importPath)
}

var moduleMajorSuffix = regexp.MustCompile(`^v[0-9]+$`)

// externals writes the external nodes of the externals layer, once each.
type externals struct {
	t    *moduleTable
	set  *quadSet
	done map[quad.IRI]bool
}

func newExternals(t *moduleTable, set *quadSet) *externals {
	return &externals{t: t, set: set, done: map[quad.IRI]bool{}}
}

// pkg returns the node of an external package, writing it the first time.
func (e *externals) pkg(importPath string) quad.IRI {
	id := extID(importPath, "")
	if e.done[id] {
		return id
	}
	e.done[id] = true
	e.set.add(id, PredType, KindExternal)
	e.set.str(id, PredName, e.t.shortName(importPath))
	e.set.str(id, PredImportPath, importPath)
	e.describe(id, importPath)
	return id
}

// fn returns the node of one function of an external package.
func (e *externals) fn(importPath, name string) quad.IRI {
	id := extID(importPath, name)
	if e.done[id] {
		return id
	}
	e.done[id] = true
	e.set.add(id, PredType, KindExternal)
	e.set.str(id, PredName, e.t.shortName(importPath)+"."+name)
	e.set.str(id, PredImportPath, importPath)
	e.describe(id, importPath)
	return id
}

// internalLeaf is the one node every function in the framework's internal/ collapses
// to: an app has no business with those names, and they change freely.
func (e *externals) internalLeaf() quad.IRI {
	id := e.t.internalLeafID()
	if e.done[id] {
		return id
	}
	e.done[id] = true
	e.set.add(id, PredType, KindExternal)
	e.set.str(id, PredName, e.t.framework[strings.LastIndex(e.t.framework, "/")+1:]+" (internal)")
	e.set.str(id, PredImportPath, e.t.framework+"/internal")
	e.set.flag(id, PredInternal, true)
	e.set.str(id, PredOrigin, OriginFramework)
	e.set.str(id, PredModule, e.t.framework)
	if v := e.t.versionOf(e.t.framework); v != "" {
		e.set.str(id, PredModuleVer, v)
	}
	return id
}

// leaf is the node a use of name in the package at importPath points to: a function
// of the framework's public API, the single internal leaf for the framework's
// internals, or one leaf per package for a library.
func (e *externals) leaf(importPath, name string) quad.IRI {
	switch {
	case e.t.isFramework(importPath) && e.t.isInternal(importPath):
		return e.internalLeaf()
	case e.t.isFramework(importPath) && name != "":
		return e.fn(importPath, name)
	}
	return e.pkg(importPath)
}

func (e *externals) describe(id quad.IRI, importPath string) {
	origin := OriginFramework // not the project's code: the framework's or a library's
	if e.t.isFramework(importPath) {
		e.set.flag(id, PredInternal, e.t.isInternal(importPath))
	}
	e.set.str(id, PredOrigin, origin)
	if mod := e.t.moduleOf(importPath); mod != "" {
		e.set.str(id, PredModule, mod)
		if v := e.t.versionOf(mod); v != "" {
			e.set.str(id, PredModuleVer, v)
		}
	}
}

// readGoModDeps reads the requirements and the local replacements of a go.mod. It is a
// line reader, not a parser: it understands `require x v`, `require ( ... )`, and
// `replace x [v] => ./dir` (a local replacement), which is all the versions need.
func readGoModDeps(goMod string) (requires map[string]string, localReplaces map[string]bool) {
	requires, localReplaces = map[string]string{}, map[string]bool{}
	f, err := os.Open(goMod)
	if err != nil {
		return requires, localReplaces
	}
	defer f.Close()
	block := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == ")":
			block = ""
			continue
		case strings.HasSuffix(line, "("):
			block = strings.TrimSpace(strings.TrimSuffix(line, "("))
			continue
		}
		verb, rest := block, line
		if block == "" {
			verb, rest, _ = strings.Cut(line, " ")
			rest = strings.TrimSpace(rest)
		}
		fields := strings.Fields(rest)
		switch verb {
		case "require":
			if len(fields) >= 2 {
				requires[strings.Trim(fields[0], `"`)] = fields[1]
			}
		case "replace":
			for i, fl := range fields {
				if fl == "=>" && i+1 < len(fields) && (strings.HasPrefix(fields[i+1], ".") || strings.HasPrefix(fields[i+1], "/")) {
					localReplaces[strings.Trim(fields[0], `"`)] = true
				}
			}
		}
	}
	return requires, localReplaces
}

// reservedPath reports whether a URL path is in a namespace the framework owns: the
// dev panel (/dev) and the framework's runtime (/_govibe).
func reservedPath(p string) bool {
	for _, prefix := range []string{"/dev", "/_govibe"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// routeOrigin says who registered a route: the framework when it did so before the
// application's Routes ran (the build facts say which), or when the route is in a
// namespace the framework reserves, and the application otherwise.
func routeOrigin(facts BuildFacts, r routeinfo.Route) string {
	if facts.FrameworkRoutes[r.Key()] || reservedPath(r.Path) {
		return OriginFramework
	}
	return OriginApp
}

// sortedKeys lists the keys of a set, in order.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
