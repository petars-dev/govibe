package govibe_test

import (
	"encoding/json"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/petars-dev/govibe"

// public is the framework's whole public surface: an application imports these and
// nothing else of the framework.
var public = map[string]bool{
	module:                 true, // govibe
	module + "/config":     true,
	module + "/web":        true,
	module + "/govibetest": true,
}

type pkg struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

// list runs `go list -json` over patterns, with the dev tag so that the files only
// a govibe_dev build compiles are read too. A pattern that matches nothing, or a
// package that does not load, fails the test.
func list(t *testing.T, patterns ...string) []pkg {
	t.Helper()
	args := append([]string{"list", "-tags", "govibe_dev", "-json=ImportPath,Imports,TestImports,XTestImports"}, patterns...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		t.Fatalf("go %s: %v", strings.Join(args, " "), err)
	}
	var pkgs []pkg
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p pkg
		if err := dec.Decode(&p); err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatalf("go list %v matched nothing", patterns)
	}
	return pkgs
}

func imports(p pkg, withTests bool) []string {
	all := append([]string(nil), p.Imports...)
	if withTests {
		all = append(append(all, p.TestImports...), p.XTestImports...)
	}
	sort.Strings(all)
	return all
}

func under(path, prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }

// The dev tools are a leaf: they declare the ports they need (devtools.Deps) and never
// reach back into the kernel that hosts them, so another host can mount them later.
func TestDevToolsDoNotImportTheKernel(t *testing.T) {
	for _, p := range list(t, module+"/internal/devtools/...") {
		for _, imp := range imports(p, true) {
			if under(imp, module+"/internal/kernel") {
				t.Errorf("%s imports %s", p.ImportPath, imp)
			}
		}
	}
}

// The public packages stay light: none of them imports the dev stack, directly or
// through another package, in a default build (the dev stack's one way in is the
// kernel's govibe_dev file), and none imports an application.
func TestPublicPackagesDoNotReachTheDevStack(t *testing.T) {
	devStack := []string{"/internal/devtools", "/internal/codegraph", "/internal/devmcp", "/internal/dbexplorer", "/internal/graphdb"}
	for _, p := range list(t, module, module+"/config", module+"/web", module+"/govibetest") {
		for _, imp := range imports(p, false) {
			for _, d := range devStack {
				if under(imp, module+d) {
					t.Errorf("public package %s imports %s", p.ImportPath, imp)
				}
			}
		}
	}
	// Transitively, in the build that ships.
	for _, pat := range []string{module, module + "/config", module + "/web", module + "/govibetest"} {
		out, err := exec.Command("go", "list", "-deps", "-tags", "", pat).Output()
		if err != nil {
			t.Fatalf("go list -deps %s: %v", pat, err)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, d := range devStack {
				if under(dep, module+d) {
					t.Errorf("a default build of %s links %s", pat, dep)
				}
			}
		}
	}
}

// Nothing in the framework imports an example: the dependency points one way.
func TestNothingImportsExamples(t *testing.T) {
	for _, p := range list(t, "./...") {
		for _, imp := range imports(p, true) {
			if under(imp, module+"/examples") {
				t.Errorf("%s imports %s", p.ImportPath, imp)
			}
		}
	}
}

// An application imports the four public packages and nothing else of the framework:
// the compiler would allow its internal/ (the paths share a prefix), and this test is
// what says no. The graph's framework.import rule judges an application's own layers
// (Phase 3).
func TestExampleAppImportsOnlyThePublicPackages(t *testing.T) {
	pkgs := list(t, "./examples/hello/...")
	// The example is a module of its own: it is found through go.work. Without the
	// workspace there is nothing to check, which must not read as a pass.
	var found bool
	for _, p := range pkgs {
		found = found || p.ImportPath == module+"/examples/hello"
	}
	if !found {
		t.Fatalf("examples/hello is not among the packages go list found (is go.work in effect?): %v", pkgs)
	}
	for _, p := range pkgs {
		for _, imp := range imports(p, true) {
			if !under(imp, module) || under(imp, module+"/examples/hello") {
				continue
			}
			if !public[imp] {
				t.Errorf("%s imports %s, which is not public API", p.ImportPath, imp)
			}
		}
	}
}
