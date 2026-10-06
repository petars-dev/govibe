package codegraph

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// An agent rarely has a node ID in hand. It has what it read in the code or a
// stack trace: a file path, `controllers.HelloAPI`, `GET /api/hello`, a table
// name. Resolve turns those into nodes. It never guesses between candidates: a
// reference that fits more than one node is reported as ambiguous with the
// candidates, and one that fits none comes back with the nearest names.

var routeRef = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|ANY)[ :]+(/\S*)$`)

// lineSuffix is the `:12` or `:12:5` that editors, compilers and stack traces
// append to a path.
var lineSuffix = regexp.MustCompile(`:\d+(?::\d+)?$`)

// impliedPrefixes are the ID prefixes tried, in order, when a reference is a
// bare name: `orders` is `table:orders`, `server.port` is `cfg:server.port`.
var impliedPrefixes = []string{"table:", "col:", "idx:", "cfg:", "env:", "tool:", "spec:", "mig:", "pkg:", "file:", "doc:",
	"func:", "typ:", "tmpl:", "mw:", "item:"}

// Resolve returns the nodes a reference names, sorted by kind then ID. It tries,
// in order, and stops at the first rule that finds anything:
//
//  1. a node ID, with or without angle brackets;
//  2. a route: `GET /api/hello`, `GET:/api/hello`, or a bare path (all its
//     methods), also matched against `{param}` and `*` patterns, so a concrete
//     URL finds its route;
//  3. a repository path of a file, package directory, doc or spec (also with a
//     `:line` suffix, an absolute path inside the repo, or a Go import path);
//  4. a bare name with an ID prefix implied (`orders` for `table:orders`);
//  5. a Go symbol: `pkg.Name`, `dir/pkg.Name`, `pkg.Type.Method`, `Type.Method`
//     or a bare name, for functions, types and templ components;
//  6. any of the above ignoring case.
func (m *Model) Resolve(ref string) []*Node {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimSuffix(strings.TrimPrefix(ref, "<"), ">")
	if ref == "" {
		return nil
	}
	for _, rule := range []func(string) []*Node{m.byID, m.ResolveRoute, m.ResolvePath, m.byImplied, m.ResolveSymbol, m.byFold} {
		if got := rule(ref); len(got) > 0 {
			sortNodes(got)
			return got
		}
	}
	return nil
}

// resolveOne is Resolve for a caller that needs exactly one node.
func (m *Model) resolveOne(ref string) (*Node, error) {
	return m.pick(ref, m.Resolve(ref))
}

// pick turns candidates into one node, or the error that says what to do.
func (m *Model) pick(ref string, got []*Node) (*Node, error) {
	switch len(got) {
	case 1:
		return got[0], nil
	case 0:
		return nil, m.notFound(ref)
	}
	const show = 8
	var ids []string
	for i, n := range got {
		if i == show {
			ids = append(ids, fmt.Sprintf("... and %d more", len(got)-show))
			break
		}
		ids = append(ids, n.ID)
	}
	return nil, fmt.Errorf("%q is ambiguous (%d matches): %s; pass one of these IDs", ref, len(got), strings.Join(ids, ", "))
}

// notFound explains a reference that matched nothing, with the nearest names.
func (m *Model) notFound(ref string) error {
	msg := fmt.Sprintf("no node matches %q", ref)
	if routeRef.MatchString(ref) || strings.HasPrefix(ref, "/") {
		if w := m.warnings("routes"); len(w) > 0 {
			msg += " (" + w[0] + ", so routes cannot be found)"
		}
	}
	var near []string
	for _, h := range m.Search(ref, nil, 5) {
		near = append(near, h.ID)
	}
	if len(near) > 0 {
		msg += "; did you mean: " + strings.Join(near, ", ")
	} else {
		msg += "; try graph_search"
	}
	return fmt.Errorf("%s", msg)
}

func (m *Model) byID(ref string) []*Node {
	if n := m.nodes[ref]; n != nil && n.Kind != "" {
		return []*Node{n}
	}
	return nil
}

// ResolveRoute finds routes by `METHOD /path`, `METHOD:/path` or a bare path.
// A route registered for every method (ANY) answers any method.
func (m *Model) ResolveRoute(ref string) []*Node {
	method, p := "", ref
	if g := routeRef.FindStringSubmatch(ref); g != nil {
		method, p = strings.ToUpper(g[1]), g[2]
	} else if !strings.HasPrefix(ref, "/") {
		return nil
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i] // a URL with a query string
	}
	routes := m.byKind["route"]
	ok := func(n *Node) bool {
		return method == "" || method == "ANY" || n.Str("method") == method || n.Str("method") == "ANY"
	}
	trim := func(s string) string {
		if len(s) > 1 {
			return strings.TrimSuffix(s, "/")
		}
		return s
	}
	var exact, pattern []*Node
	for _, n := range routes {
		if !ok(n) {
			continue
		}
		rp := n.Str("path")
		if rp == p || trim(rp) == trim(p) {
			exact = append(exact, n)
		} else if strings.ContainsAny(rp, "{*") {
			if re := routePattern(rp); re != nil && re.MatchString(p) {
				pattern = append(pattern, n)
			}
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return pattern
}

// routePattern compiles a chi pattern (`/tables/{table}/rows`, `/assets/*`,
// `/{id:[0-9]+}`) into a matcher for concrete URL paths.
func routePattern(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(p); {
		switch c := p[i]; {
		case c == '{':
			end := strings.IndexByte(p[i:], '}')
			if end < 0 {
				return nil
			}
			if _, re, ok := strings.Cut(p[i+1:i+end], ":"); ok && re != "" {
				b.WriteString("(?:" + re + ")")
			} else {
				b.WriteString("[^/]+")
			}
			i += end + 1
		case c == '*':
			b.WriteString(".*")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("/?$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil // a pattern with a regexp Go cannot read
	}
	return re
}

// pathIndex maps repository paths to the packages, files, docs, specs and
// migrations at them, and Go import paths to packages.
func (m *Model) pathIndex() map[string][]*Node {
	if m.pathIdx != nil {
		return m.pathIdx
	}
	m.pathIdx = map[string][]*Node{}
	for _, k := range []string{"file", "package", "doc", "spec", "migration"} {
		for _, n := range m.byKind[k] {
			if p := n.Str("path"); p != "" {
				m.pathIdx[p] = append(m.pathIdx[p], n)
			}
			if ip := n.Str("import_path"); ip != "" {
				m.pathIdx[ip] = append(m.pathIdx[ip], n)
			}
		}
	}
	return m.pathIdx
}

// ResolvePath finds a file, package directory, doc, spec or migration by
// repository path.
func (m *Model) ResolvePath(ref string) []*Node {
	p := strings.TrimPrefix(filepath.ToSlash(ref), "file://")
	p = lineSuffix.ReplaceAllString(p, "")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	idx := m.pathIndex()
	if got := idx[p]; len(got) > 0 {
		return append([]*Node(nil), got...)
	}
	if !strings.HasPrefix(p, "/") {
		return nil
	}
	// An absolute path: the longest repo-relative path it ends with.
	best, bestLen := []*Node(nil), 0
	for key, ns := range idx {
		if key == "." || !strings.HasSuffix(p, "/"+key) || len(key) < bestLen {
			continue
		}
		if len(key) > bestLen {
			best, bestLen = nil, len(key)
		}
		best = append(best, ns...)
	}
	return best
}

func (m *Model) byImplied(ref string) []*Node {
	var out []*Node
	for _, p := range impliedPrefixes {
		if n := m.nodes[p+ref]; n != nil && n.Kind != "" {
			out = append(out, n)
		}
	}
	if len(out) > 0 {
		return out
	}
	// a package by its name (`orders` for business/services/orders)
	for _, n := range m.byKind["package"] {
		if n.Str("name") == ref {
			out = append(out, n)
		}
	}
	return out
}

// codeDir is the directory of the package a function, type or component
// belongs to, and that package's name.
func (m *Model) codeDir(n *Node) (dir, pkgName string) {
	switch n.Kind {
	case "func", "type":
		if f := m.nodes[n.first("defined_in")]; f != nil {
			if p := m.nodes[f.first("in_package")]; p != nil {
				return p.Str("path"), p.Str("name")
			}
		}
	case "template":
		dir = path.Dir(n.Str("path"))
		return dir, path.Base(dir)
	}
	return "", ""
}

func (m *Model) symbolIndex() (exact, folded map[string][]*Node) {
	if m.symIdx != nil {
		return m.symIdx, m.symIdxLow
	}
	m.symIdx, m.symIdxLow = map[string][]*Node{}, map[string][]*Node{}
	for _, k := range []string{"func", "type", "template"} {
		for _, n := range m.byKind[k] {
			sym := n.Str("name")
			if sym == "" {
				continue
			}
			dir, pkgName := m.codeDir(n)
			keys := []string{sym}
			if i := strings.LastIndexByte(sym, '.'); i >= 0 {
				keys = append(keys, sym[i+1:]) // a method by its bare name
			}
			if pkgName != "" {
				keys = append(keys, pkgName+"."+sym)
			}
			if dir != "" && dir != pkgName {
				keys = append(keys, dir+"."+sym)
			}
			for _, key := range keys {
				m.symIdx[key] = append(m.symIdx[key], n)
				low := strings.ToLower(key)
				m.symIdxLow[low] = append(m.symIdxLow[low], n)
			}
		}
	}
	return m.symIdx, m.symIdxLow
}

// ResolveSymbol finds a function, type or templ component by `pkg.Name`,
// `dir/pkg.Name`, `Type.Method`, or a bare function, type or method name. Case is ignored only when
// nothing matches exactly.
func (m *Model) ResolveSymbol(ref string) []*Node {
	exact, folded := m.symbolIndex()
	got := exact[ref]
	if len(got) == 0 {
		got = folded[strings.ToLower(ref)]
	}
	return uniqueNodes(got)
}

// byFold is the last resort: an ID with an implied prefix, ignoring case.
func (m *Model) byFold(ref string) []*Node {
	var out []*Node
	for _, id := range m.ids {
		for _, p := range impliedPrefixes {
			if n := m.nodes[id]; n.Kind != "" && strings.EqualFold(id, p+ref) {
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func uniqueNodes(in []*Node) []*Node {
	seen := map[string]bool{}
	var out []*Node
	for _, n := range in {
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

// ResolveTable finds a table by name (`orders` or `table:orders`).
func (m *Model) ResolveTable(ref string) []*Node {
	name := strings.TrimPrefix(strings.TrimSpace(ref), "table:")
	if n := m.nodes["table:"+name]; n != nil && n.Kind == "table" {
		return []*Node{n}
	}
	var out []*Node
	for _, n := range m.byKind["table"] {
		if strings.EqualFold(n.Str("name"), name) {
			out = append(out, n)
		}
	}
	return out
}

// sortedIDs returns the IDs of nodes, sorted.
func sortedIDs(ns []*Node) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.ID)
	}
	sort.Strings(out)
	return out
}
