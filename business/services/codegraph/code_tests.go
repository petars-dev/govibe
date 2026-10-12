package codegraph

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// readTests records what the test sources reference. Tests add no nodes or edges
// to the graph, but an exported function that only a test calls is used, so the
// findings that ask "does anything reference this" must see them. Two facts are
// kept: qualified references into a module package (pkg.Name, which also covers
// an external test package such as codegraph_test), and the exported bare names an
// in-package test mentions. A test that does not parse is the compiler's to
// report, not a warning of the code layer.
func (ci *codeIndex) readTests(ctx context.Context, fset *token.FileSet, files []sourceFile) error {
	for _, sf := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		src, err := os.ReadFile(sf.abs)
		if err != nil {
			continue
		}
		af, err := parser.ParseFile(fset, sf.rel, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		dir := pathDir(sf.rel)
		imps := ci.importNames(af)
		// A bare name means the package's own only in an in-package test: in an
		// external test package (name_test) it is a name of that test's own.
		inPackage := !strings.HasSuffix(af.Name.Name, "_test")
		var visit func(n ast.Node) bool
		visit = func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				// A composite literal's key is a field name (Name: "x"), not a reference;
				// a function is never a usable key. Only the value can reference.
				ast.Inspect(x.Value, visit)
				return false
			case *ast.Field:
				// Declared names (struct fields, parameters) are not references: only the type is.
				ast.Inspect(x.Type, visit)
				return false
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok {
					if path, ok := imps[id.Name]; ok {
						if d, ok := moduleDir(ci.module, path); ok {
							ci.testRefs[d+"\x00"+x.Sel.Name] = true
						}
					}
				}
				// The selected name belongs to its operand, not to this package.
				ast.Inspect(x.X, visit)
				return false
			case *ast.Ident:
				if inPackage && ast.IsExported(x.Name) {
					if ci.testIdents[dir] == nil {
						ci.testIdents[dir] = map[string]bool{}
					}
					ci.testIdents[dir][x.Name] = true
				}
			}
			return true
		}
		ast.Inspect(af, visit)
	}
	return nil
}

// referencedByTests reports whether a test source references name of the package
// in dir: qualified from another package, or as a bare name from a test in dir.
func (ci *codeIndex) referencedByTests(dir, name string) bool {
	return ci.testRefs[dir+"\x00"+name] || ci.testIdents[dir][name]
}
