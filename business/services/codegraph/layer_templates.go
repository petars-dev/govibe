package codegraph

import (
	"context"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"os"
	"path/filepath"
	"strings"

	templparser "github.com/a-h/templ/parser/v2"
	"github.com/cayleygraph/quad"
)

// templIndex is the .templ components of the repo, parsed with templ's own
// parser. The templates layer writes them, and the docs layer resolves
// mentions against them.
type templIndex struct {
	files     []*templFile
	templates []*templInfo
	byKey     map[string]*templInfo // "dir\x00Name"
	warnings  []string
}

type templFile struct {
	rel   string
	dir   string
	lines int
}

type templInfo struct {
	id        quad.IRI
	dir       string
	pkg       string // package clause of the .templ file
	name      string // Name, or Recv.Name for a method component
	file      string
	line      int
	signature string
	doc       string
	calls     []templCall // @Component(...) calls in the body
}

// templCall is `@Name(...)` (pkg == "") or `@pkg.Name(...)` in a template body.
type templCall struct {
	pkg  string
	name string
}

func (ti *templIndex) lookup(dir, name string) *templInfo { return ti.byKey[dir+"\x00"+name] }

// byPackageName finds the templates called name in templ packages whose package
// clause is pkg; it reports one only when that is unambiguous.
func (ti *templIndex) byPackageName(pkg, name string) *templInfo {
	var found *templInfo
	for _, t := range ti.templates {
		if t.pkg == pkg && t.name == name {
			if found != nil {
				return nil
			}
			found = t
		}
	}
	return found
}

func (w *world) templScan() (*scan, error) {
	return w.templFiles.get(func() (*scan, error) {
		return walkFiles(w.root, walkSpec{
			start:    ".",
			accept:   func(name string) bool { return strings.HasSuffix(name, ".templ") && !strings.HasPrefix(name, ".") },
			maxBytes: maxTemplFileBytes,
			maxFiles: maxTemplFiles,
		})
	})
}

func (w *world) templates() (*templIndex, error) {
	return w.templIdx.get(func() (*templIndex, error) {
		sc, err := w.templScan()
		if err != nil {
			return nil, err
		}
		return buildTemplIndex(w.ctx, sc)
	})
}

func buildTemplIndex(ctx context.Context, sc *scan) (*templIndex, error) {
	ti := &templIndex{byKey: map[string]*templInfo{}, warnings: append([]string(nil), sc.warnings...)}
	for _, sf := range sc.files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src, err := os.ReadFile(sf.abs)
		if err != nil {
			ti.warnings = append(ti.warnings, fmt.Sprintf("%s: %v", sf.rel, err))
			continue
		}
		tf, err := templparser.ParseString(string(src))
		if err != nil {
			// The parser returns what it could read, but a half-parsed file
			// would index half its components: skip it and say so.
			ti.warnings = append(ti.warnings, fmt.Sprintf("%s: %v", sf.rel, firstLine(err.Error())))
			continue
		}
		dir := pathDir(sf.rel)
		ti.files = append(ti.files, &templFile{rel: sf.rel, dir: dir, lines: countLines(src)})
		pkg := strings.TrimSpace(strings.TrimPrefix(tf.Package.Expression.Value, "package"))

		var prevGo *templparser.TemplateFileGoExpression
		for _, n := range tf.Nodes {
			switch x := n.(type) {
			case *templparser.TemplateFileGoExpression:
				prevGo = x
			case *templparser.HTMLTemplate:
				name, sig, ok := templateName(x.Expression.Value)
				if !ok {
					prevGo = nil
					continue
				}
				info := &templInfo{
					id: tmplID(dir, name), dir: dir, pkg: pkg, name: name, file: sf.rel,
					line:      int(x.Range.From.Line) + 1,
					signature: sig,
				}
				// A comment directly above the component is its doc.
				if prevGo != nil && prevGo.Expression.Range.To.Line >= x.Range.From.Line {
					info.doc = trailingComment(prevGo.Expression.Value)
				}
				prevGo = nil
				collectTemplCalls(x.Children, info)
				if _, dup := ti.byKey[dir+"\x00"+name]; dup {
					ti.warnings = append(ti.warnings, fmt.Sprintf("%s:%d: template %s declared twice in %s", sf.rel, info.line, name, dir))
					continue
				}
				ti.byKey[dir+"\x00"+name] = info
				ti.templates = append(ti.templates, info)
			default:
				prevGo = nil
			}
		}
	}
	return ti, nil
}

// templateName splits a component declaration such as `Layout(title string)` or
// `(s Server) Method()` into its name (`Layout`, `Server.Method`) and a one-line
// signature (`templ Layout(title string)`).
func templateName(expr string) (name, sig string, ok bool) {
	expr = strings.TrimSpace(expr)
	sig = "templ " + strings.Join(strings.Fields(expr), " ")
	recv := ""
	if strings.HasPrefix(expr, "(") {
		end := strings.Index(expr, ")")
		if end < 0 {
			return "", "", false
		}
		fields := strings.Fields(expr[1:end])
		if len(fields) > 0 {
			recv = strings.TrimLeft(fields[len(fields)-1], "*")
		}
		expr = strings.TrimSpace(expr[end+1:])
	}
	end := strings.IndexAny(expr, "([")
	if end <= 0 {
		return "", "", false
	}
	name = strings.TrimSpace(expr[:end])
	if name == "" || strings.ContainsAny(name, " .") {
		return "", "", false
	}
	if recv != "" {
		name = recv + "." + name
	}
	return name, truncate(sig, maxSignatureRunes), true
}

// trailingComment returns the `//` comment lines at the end of a Go fragment.
func trailingComment(code string) string {
	lines := strings.Split(code, "\n")
	var comment []string
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, "//") {
			break
		}
		comment = append([]string{strings.TrimSpace(strings.TrimPrefix(l, "//"))}, comment...)
	}
	return firstSentence(strings.Join(comment, " "))
}

// collectTemplCalls records every @Component(...) call below nodes.
func collectTemplCalls(nodes []templparser.Node, info *templInfo) {
	for _, n := range nodes {
		var expr string
		switch x := n.(type) {
		case *templparser.TemplElementExpression:
			expr = x.Expression.Value
		case *templparser.CallTemplateExpression:
			expr = x.Expression.Value
		}
		if expr != "" {
			if call, ok := parseTemplCall(expr); ok {
				info.calls = append(info.calls, call)
			}
		}
		if c, ok := n.(interface{ ChildNodes() []templparser.Node }); ok {
			collectTemplCalls(c.ChildNodes(), info)
		}
	}
}

// parseTemplCall understands `Name(args)` and `pkg.Name(args)`.
func parseTemplCall(expr string) (templCall, bool) {
	e, err := goparser.ParseExpr(strings.TrimSpace(expr))
	if err != nil {
		return templCall{}, false
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return templCall{}, false
	}
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return templCall{name: f.Name}, true
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return templCall{pkg: id.Name, name: f.Sel.Name}, true
		}
	}
	return templCall{}, false
}

type templatesLayer struct{}

func (templatesLayer) name() string   { return "templates" }
func (templatesLayer) deps() []string { return []string{"code"} }

func (templatesLayer) fingerprint(w *world) (string, error) {
	sc, err := w.templScan()
	if err != nil {
		return "", err
	}
	return fingerprint(sc.files), nil
}

func (templatesLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	ti, err := w.templates()
	if err != nil {
		return nil, err
	}
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}

	set := newQuadSet("templates")
	for _, f := range ti.files {
		id := fileID(f.rel)
		set.add(id, PredType, KindFile)
		set.str(id, PredName, filepath.Base(f.rel))
		set.str(id, PredPath, f.rel)
		set.num(id, PredLines, f.lines)
	}
	for _, t := range ti.templates {
		set.add(t.id, PredType, KindTemplate)
		set.str(t.id, PredName, t.name)
		set.str(t.id, PredPath, t.file)
		set.num(t.id, PredLine, t.line)
		set.str(t.id, PredSignature, t.signature)
		if t.doc != "" {
			set.str(t.id, PredDoc, t.doc)
		}
		set.add(t.id, PredDefinedIn, fileID(t.file))
		for _, c := range t.calls {
			var target *templInfo
			if c.pkg == "" {
				target = ti.lookup(t.dir, c.name)
			} else {
				target = ti.byPackageName(c.pkg, c.name)
			}
			if target != nil && target != t {
				set.add(t.id, PredRenders, target.id)
			}
		}
	}
	// A Go function that references a component renders it.
	for _, r := range ci.refs {
		if r.fn == "" {
			continue
		}
		if t := ti.lookup(r.toDir, r.name); t != nil {
			set.add(r.fn, PredRenders, t.id)
		}
	}
	return &layerOutput{quads: set.quads, warnings: ti.warnings}, nil
}
