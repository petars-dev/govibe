package codegraph

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// A document that names something the project no longer has is stale. The docs
// layer records such a reference (`unresolved_ref`, "<line>:<text>") so the
// findings layer can report it; this file decides what counts. It has to be
// conservative, because a backticked word in prose is far more often a command,
// a type from a library or an example than a pointer into this repository, and a
// finding that cries wolf is ignored. A reference is stale only when it is
// unambiguously a pointer into this repository and nothing answers to it:
//
//   - a path with a slash that ends in a source extension (so `legacy/handler.go`
//     counts wherever `legacy` used to be) or starts at something that exists at
//     the repository root (`docs/graph/gone.bolt`), and that is neither a node of
//     the graph nor on disk. `net/http`, `application/json` and
//     `example.com/x/y` are not paths into the repository;
//   - `pkg.Name`, when `pkg` is the one package of that name, no library the
//     module imports shares the name (`middleware.RequestID` is chi's), and
//     nothing called Name is declared in it;
//   - `Type.Member` of a struct that embeds nothing, when it has no such field
//     or method;
//   - a link to a /dev/docs page, or to a relative file, that is not there.
//
// Routes, node IDs, config keys and environment variables are never judged:
// documents use them as examples and for things not built yet.

// unresolvedRef is something a document names that nothing answers to.
type unresolvedRef struct {
	line  int
	token string
}

func (u unresolvedRef) String() string { return fmt.Sprintf("%d:%s", u.line, u.token) }

// pathExts are the extensions that make a token with a slash a file path
// whatever its first segment is.
var pathExts = map[string]bool{
	".go": true, ".templ": true, ".sql": true, ".md": true, ".yml": true, ".yaml": true,
	".json": true, ".toml": true, ".css": true, ".js": true, ".html": true, ".sh": true,
}

// pathJunk are the characters that mean a token is a pattern, an expression or
// a placeholder, not a path.
const pathJunk = " \t\n*?{}<>$|\"'`()[]\\,;=!^&%@#~"

// staleToken reports whether a backticked token is a reference to something that
// is gone, and the text to record.
func (rs *resolver) staleToken(tok, docDir string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if tok == "" || len(tok) > 200 {
		return "", false
	}
	if rs.stalePath(tok, docDir, true) {
		return tok, true
	}
	if rs.staleSymbol(strings.TrimSuffix(tok, "()")) {
		return tok, true
	}
	if dir, name, ok := dirSymbol(strings.TrimSuffix(tok, "()")); ok {
		// `dir.Name` of a package that exists: the name must still be declared.
		if _, there := rs.ci.pkgs[dir]; there && !rs.ci.topLevel[dir][name] {
			return tok, true
		}
	}
	return "", false
}

// staleLink reports whether a markdown link target points at a page or file that
// is gone. Only /dev/docs pages and relative files are judged.
func (rs *resolver) staleLink(dest, docDir string) (string, bool) {
	dest = strings.TrimSpace(dest)
	if i := strings.IndexAny(dest, "#?"); i >= 0 {
		dest = dest[:i]
	}
	if dest == "" || strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
		return "", false
	}
	if strings.HasPrefix(dest, "/dev/docs/specs/") || strings.HasPrefix(dest, "/dev/docs/app/") {
		return dest, true // the caller has already failed to resolve it
	}
	if strings.HasPrefix(dest, "/") {
		return "", false
	}
	if rs.stalePath(dest, docDir, false) {
		return dest, true
	}
	return "", false
}

// stalePath reports whether tok is a path into the repository that names
// nothing: not a node of the graph and not on disk. A token must have a slash
// unless it is a link target, which is a path by construction.
func (rs *resolver) stalePath(tok, docDir string, needSlash bool) bool {
	if strings.ContainsAny(tok, pathJunk) {
		return false
	}
	p := lineSuffixRe.ReplaceAllString(tok, "")
	if strings.Contains(p, ":") {
		return false // a URL, a node ID, a drive
	}
	p = strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/")
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	if _, _, sym := dirSymbol(p); sym {
		return false // `dir.Name` is a symbol, judged by staleToken
	}
	if needSlash && !strings.Contains(p, "/") {
		return false
	}
	first := strings.SplitN(p, "/", 2)[0]
	if first != ".." && !pathExts[path.Ext(p)] && !rs.atRoot(first) {
		return false // `net/http`, `application/json`
	}
	if strings.Contains(p, "/") && strings.Contains(first, ".") && first != ".." && !rs.atRoot(first) {
		return false // `example.com/x/y.go`: a module path, not ours
	}
	if len(rs.resolvePath(tok, docDir)) > 0 {
		return false
	}
	var candidates []string
	if docDir != "." && docDir != "" {
		if c := path.Join(docDir, p); !strings.HasPrefix(c, "..") {
			candidates = append(candidates, c)
		}
	}
	if !strings.Contains(p, "..") {
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return false // climbs out of the repository: not ours to judge
	}
	for _, c := range candidates {
		if rs.exists(c) {
			return false
		}
	}
	return true
}

// atRoot reports whether name is an entry of the repository root.
func (rs *resolver) atRoot(name string) bool { return rs.exists(name) }

// exists reports whether a repo-relative path is on disk, and remembers the
// answer: the docs layer's fingerprint covers it, so creating a file a document
// names, or removing one it names, makes the layer stale.
func (rs *resolver) exists(rel string) bool {
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") || path.IsAbs(rel) {
		return false
	}
	if v, ok := rs.probe[rel]; ok {
		return v
	}
	_, err := os.Lstat(filepath.Join(rs.root, filepath.FromSlash(rel)))
	rs.probe[rel] = err == nil
	return err == nil
}

// staleSymbol reports whether tok is `pkg.Name` or `Type.Member` of this module
// and no longer exists. See the conservative conditions at the top of the file.
func (rs *resolver) staleSymbol(tok string) bool {
	if !symbolRe.MatchString(tok) {
		return false
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 2 || !isExportedName(parts[1]) {
		return false
	}
	a, b := parts[0], parts[1]
	if dirs := rs.ci.pkgNamed(a); len(dirs) == 1 {
		if rs.externalNames[a] {
			return false // `middleware.RequestID` is chi's, not this module's
		}
		return !rs.ci.topLevel[dirs[0]][b]
	} else if len(dirs) > 1 {
		return false
	}
	types := rs.typesBy[a]
	if len(types) != 1 || !isExportedName(a) {
		return false
	}
	t := types[0]
	if !t.isStruct || t.embeds {
		return false // an interface or a promoted method: not knowable without types
	}
	if rs.ci.funcNamed(t.dir, a, b) != nil {
		return false
	}
	for _, f := range t.fields {
		if f.name == b {
			return false
		}
	}
	return true
}

func isExportedName(s string) bool {
	for _, r := range s {
		return unicode.IsUpper(r)
	}
	return false
}

// externalPackageNames are the names of the libraries the module imports (the
// last element of each import path with no node of ours): a document that says
// `middleware.X` may mean theirs.
func externalPackageNames(ci *codeIndex) map[string]bool {
	out := map[string]bool{}
	for _, p := range ci.pkgs {
		for imp := range p.external {
			out[guessPkgName(imp)] = true
		}
	}
	return out
}

// addUnresolved appends a reference once per line and text.
func addUnresolved(list []unresolvedRef, line int, token string) []unresolvedRef {
	for _, u := range list {
		if u.line == line && u.token == token {
			return list
		}
	}
	return append(list, unresolvedRef{line: line, token: token})
}

func sortUnresolved(list []unresolvedRef) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].line != list[j].line {
			return list[i].line < list[j].line
		}
		return list[i].token < list[j].token
	})
}
