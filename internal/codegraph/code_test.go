package codegraph

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

// checkGolden compares quads, as N-Quads, with testdata/golden/<name>.nq, or
// rewrites that file under -update.
func checkGolden(t *testing.T, name string, quads []quad.Quad) {
	t.Helper()
	file := filepath.Join("testdata", "golden", name+".nq")
	got := nquads(t, quads)
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/codegraph -update`): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s layer output differs from %s (run with -update if the change is intended):\n%s",
			name, file, lineDiff(lines(string(want)), lines(got)))
	}
}

// The code layer's complete output for the fixture module, as N-Quads. The
// fixture and this file change together (see docs/public/GRAPH_SCHEMA.md).
func TestCodeLayerGolden(t *testing.T) {
	checkGolden(t, "code", buildCode(t, fixtureRoot).quads)
}

// lineDiff lists lines present on only one side.
func lineDiff(want, got []string) string {
	in := func(set []string) map[string]bool {
		m := map[string]bool{}
		for _, l := range set {
			m[l] = true
		}
		return m
	}
	wantSet, gotSet := in(want), in(got)
	var b strings.Builder
	n := 0
	for _, l := range want {
		if !gotSet[l] && n < 40 {
			b.WriteString("- " + l + "\n")
			n++
		}
	}
	for _, l := range got {
		if !wantSet[l] && n < 40 {
			b.WriteString("+ " + l + "\n")
			n++
		}
	}
	if b.Len() == 0 {
		return "(same lines, different order or duplicates)"
	}
	return b.String()
}

// The goldens could be regenerated wrongly with -update, so the facts the spec
// promises are asserted independently of them.
func TestCodeLayerFixtureFacts(t *testing.T) {
	out := buildCode(t, fixtureRoot)
	if len(out.warnings) != 0 {
		t.Errorf("unexpected warnings: %v", out.warnings)
	}
	f := facts{out.quads}
	iri := func(s string) quad.Value { return quad.IRI(s) }

	t.Run("packages", func(t *testing.T) {
		got := f.subjects(KindPackage)
		want := []string{
			"pkg:.", "pkg:business/actions", "pkg:business/services/orders", "pkg:config", "pkg:controllers", "pkg:devmcp",
			"pkg:mcp", "pkg:middleware", "pkg:routes", "pkg:storage/orderstore",
		}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("packages:\n got  %v\n want %v", got, want)
		}
		if f.exists("pkg:assets/templates") || f.exists("file:assets/templates/home_templ.go") {
			t.Error("generated *_templ.go must be skipped, and a package of only those has no node")
		}
	})

	t.Run("classification", func(t *testing.T) {
		for pkg, want := range map[string]string{
			"pkg:.": "wiring", "pkg:routes": "wiring", "pkg:controllers": "controllers",
			"pkg:business/actions": "business", "pkg:business/services/orders": "business",
			"pkg:storage/orderstore": "storage", "pkg:middleware": "middleware", "pkg:devmcp": "dev",
		} {
			if got := f.one(t, pkg, "arch_layer"); got != quad.Value(quad.String(want)) {
				t.Errorf("%s arch_layer = %v, want %s", pkg, got, want)
			}
		}
	})

	t.Run("package attributes", func(t *testing.T) {
		if got := f.one(t, "pkg:.", "name"); got != quad.Value(quad.String("main")) {
			t.Errorf("root package name = %v", got)
		}
		if got := f.one(t, "pkg:routes", "import_path"); got != quad.Value(quad.String("example.com/fixture/routes")) {
			t.Errorf("import_path = %v", got)
		}
		if got := f.one(t, "pkg:.", "import_path"); got != quad.Value(quad.String("example.com/fixture")) {
			t.Errorf("root import_path = %v", got)
		}
		if got := f.one(t, "pkg:devmcp", "doc"); got != quad.Value(quad.String("Package devmcp stands in for the dev-only tooling package.")) {
			t.Errorf("package doc = %v", got)
		}
		// Standard library and third-party imports are counted, not noded; so is
		// a module package with no node of its own (assets/templates holds only
		// generated templ output). The framework's packages are counted too, and also get an
		// external node and an imports_external edge (the stub framework's web helpers).
		if got := f.one(t, "pkg:controllers", "external_imports"); got != quad.Value(quad.Int(4)) {
			t.Errorf("controllers external_imports = %v, want 4 (encoding/json, net/http, assets/templates, the framework's web)", got)
		}
		if !f.has("pkg:controllers", "imports_external", quad.IRI("ext:example.com/stubfw/web")) {
			t.Error("controllers does not point at the framework's web package")
		}
		if !f.has("pkg:.", "imports_external", quad.IRI("ext:example.com/stubfw")) {
			t.Error("main does not point at the framework's root package")
		}
		if f.exists("ext:example.com/stubfw/web") {
			t.Error("the code layer wrote an external node: the externals layer owns them")
		}
	})

	t.Run("imports", func(t *testing.T) {
		for _, c := range []struct{ from, to string }{
			{"pkg:controllers", "pkg:business/actions"},
			{"pkg:controllers", "pkg:business/services/orders"},
			{"pkg:controllers", "pkg:storage/orderstore"}, // the deliberate layering violation
			{"pkg:business/services/orders", "pkg:storage/orderstore"},
			{"pkg:devmcp", "pkg:storage/orderstore"},
			{"pkg:.", "pkg:routes"},
		} {
			if !f.has(c.from, "imports", iri(c.to)) {
				t.Errorf("missing %s --imports--> %s", c.from, c.to)
			}
		}
		if f.has("pkg:business/actions", "imports", iri("pkg:controllers")) {
			t.Error("imports must follow the import direction")
		}
		if got := f.out("pkg:business/actions", "imports"); len(got) != 1 || got[0] != iri("pkg:config") {
			t.Errorf("actions imports only config, got %v", got)
		}
		if f.has("pkg:controllers", "imports", iri("pkg:assets/templates")) || f.exists("pkg:assets/templates") {
			t.Error("a templ-only package has no node, so no edge")
		}
	})

	t.Run("files", func(t *testing.T) {
		if got := f.one(t, "file:controllers/hello.go", "in_package"); got != iri("pkg:controllers") {
			t.Errorf("in_package = %v", got)
		}
		if got := f.one(t, "file:storage/orderstore/zz_generated.go", "generated"); got != quad.Value(quad.Bool(true)) {
			t.Errorf("zz_generated.go generated = %v", got)
		}
		if got := f.one(t, "file:storage/orderstore/orderstore.go", "generated"); got != quad.Value(quad.Bool(false)) {
			t.Errorf("orderstore.go generated = %v", got)
		}
		if got := f.one(t, "file:business/actions/hello.go", "lines"); got != quad.Value(quad.Int(6)) {
			t.Errorf("hello.go lines = %v, want 6", got)
		}
	})

	t.Run("functions and methods", func(t *testing.T) {
		hello := "func:controllers.HelloAPI"
		if got := f.one(t, hello, "defined_in"); got != iri("file:controllers/hello.go") {
			t.Errorf("defined_in = %v", got)
		}
		if got := f.one(t, hello, "line"); got != quad.Value(quad.Int(10)) {
			t.Errorf("line = %v, want 10", got)
		}
		if got := f.one(t, hello, "exported"); got != quad.Value(quad.Bool(true)) {
			t.Errorf("exported = %v", got)
		}
		if got := f.one(t, hello, "signature"); got != quad.Value(quad.String("func HelloAPI(w http.ResponseWriter, r *http.Request)")) {
			t.Errorf("signature = %v", got)
		}
		if got := f.one(t, hello, "doc"); got != quad.Value(quad.String("HelloAPI returns a JSON greeting.")) {
			t.Errorf("doc = %v", got)
		}
		// First sentence only, from a two-sentence comment.
		if got := f.one(t, "func:business/actions.Hello", "doc"); got != quad.Value(quad.String("Hello returns the hello string used by the API.")) {
			t.Errorf("Hello doc = %v", got)
		}
		// Methods carry the receiver, pointer or value, without the star.
		for _, id := range []string{
			"func:business/services/orders.Service.List",     // pointer receiver
			"func:business/services/orders.Service.hasStore", // value receiver
			"func:storage/orderstore.Store.Get",
		} {
			if !f.exists(id) {
				t.Errorf("missing method %s", id)
			}
		}
		if got := f.one(t, "func:business/services/orders.Service.List", "signature"); got != quad.Value(quad.String("func (s *Service) List(ctx context.Context) ([]orderstore.Order, error)")) {
			t.Errorf("method signature = %v", got)
		}
		if got := f.one(t, "func:business/services/orders.Service.hasStore", "exported"); got != quad.Value(quad.Bool(false)) {
			t.Errorf("unexported method exported = %v", got)
		}
		if got := f.one(t, "func:controllers.writeJSON", "exported"); got != quad.Value(quad.Bool(false)) {
			t.Errorf("writeJSON exported = %v", got)
		}
		// Closures get no node: only the declared handler factories exist.
		for _, id := range f.subjects(KindFunc) {
			if strings.Contains(id, "func1") || strings.Contains(id, "lit") {
				t.Errorf("closure node %s", id)
			}
		}
		if !f.exists("func:..main") {
			t.Error("root package main should be func:..main")
		}
	})

	t.Run("types", func(t *testing.T) {
		for id, sig := range map[string]string{
			"typ:business/services/orders.Lister":  "type Lister interface",
			"typ:business/services/orders.Service": "type Service struct",
			"typ:storage/orderstore.Order":         "type Order struct",
		} {
			if got := f.one(t, id, "signature"); got != quad.Value(quad.String(sig)) {
				t.Errorf("%s signature = %v, want %q", id, got, sig)
			}
			if got := f.one(t, id, "type"); got != quad.Value(KindType) {
				t.Errorf("%s type = %v", id, got)
			}
		}
		if got := f.one(t, "typ:business/services/orders.Lister", "doc"); got != quad.Value(quad.String("Lister is the read side of the order store.")) {
			t.Errorf("type doc = %v", got)
		}
	})
}

// Layer output is a pure function of the sources: no clock, no map order.
func TestCodeLayerIsDeterministic(t *testing.T) {
	first := nquads(t, buildCode(t, fixtureRoot).quads)
	for i := 0; i < 5; i++ {
		if again := nquads(t, buildCode(t, fixtureRoot).quads); again != first {
			t.Fatalf("run %d differs from run 0:\n%s", i+1, lineDiff(lines(first), lines(again)))
		}
	}
}

// What the walker must not index, and must not follow.
func TestScanSkipRules(t *testing.T) {
	root := t.TempDir()
	for rel, src := range map[string]string{
		"go.mod":                            "module example.com/m\n\ngo 1.25\n",
		"main.go":                           "package main\nfunc main() {}\n",
		"ok/ok.go":                          "package ok\nfunc Ok() {}\n",
		"ok/ok_test.go":                     "package ok\nfunc TestOk() {}\n",
		"ok/view_templ.go":                  "package ok\nfunc View() {}\n",
		"ok/_ignored.go":                    "package ok\nfunc Ignored() {}\n",
		"ok/.hidden.go":                     "package ok\nfunc Hidden() {}\n",
		"ok/testdata/x/x.go":                "package x\nfunc X() {}\n",
		"tmp/build.go":                      "package tmp\nfunc Tmp() {}\n",
		"vendor/v/v.go":                     "package v\nfunc V() {}\n",
		".git/hook.go":                      "package hook\nfunc Hook() {}\n",
		".idea/idea.go":                     "package idea\nfunc Idea() {}\n",
		"_build/b.go":                       "package b\nfunc B() {}\n",
		"nested/go.mod":                     "module example.com/nested\n",
		"nested/n.go":                       "package nested\nfunc N() {}\n",
		"deep/tmp/keep.go":                  "package tmp\nfunc Keep() {}\n", // only a root tmp/ is skipped
		"has space/s.go":                    "package space\nfunc S() {}\n",
		"broken/broken.go":                  "package broken\nfunc (\n",
		"outside/ignored.txt":               "not go",
		"ok/dir_with_go_suffix.go/file.txt": "a directory named like a go file",
	} {
		writeFile(t, root, rel, src)
	}
	// A directory and a file outside the repo, reached through symlinks.
	outside := t.TempDir()
	writeFile(t, outside, "leak.go", "package leak\nfunc Leak() {}\n")
	writeFile(t, outside, "pkg/p.go", "package p\nfunc P() {}\n")
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "leak.go"), filepath.Join(root, "ok", "link.go")); err != nil {
		t.Fatal(err)
	}

	out := buildCode(t, root)
	f := facts{out.quads}

	wantFiles := []string{"file:deep/tmp/keep.go", "file:has%20space/s.go", "file:main.go", "file:ok/ok.go"}
	if got := f.subjects(KindFile); strings.Join(got, " ") != strings.Join(wantFiles, " ") {
		t.Errorf("files:\n got  %v\n want %v", got, wantFiles)
	}
	if f.exists("pkg:linkdir") || f.exists("func:ok.Leak") || f.exists("func:linkdir.Leak") {
		t.Error("indexing followed a symlink out of the repo")
	}
	// A broken file is skipped with a warning; it does not fail the layer.
	if len(out.warnings) != 1 || !strings.HasPrefix(out.warnings[0], "broken/broken.go:") {
		t.Errorf("warnings = %v, want one for broken/broken.go", out.warnings)
	}
	// Directories with awkward names still yield valid IDs.
	if !f.exists("pkg:has%20space") {
		t.Error("expected pkg:has%20space")
	}
	for _, q := range out.quads {
		if s, ok := q.Subject.(quad.IRI); ok && !validID(string(s)) {
			t.Errorf("invalid ID %q", s)
		}
	}
}

// Several declarations may share a name (init, or files with build tags).
func TestDuplicateNamesGetStableSuffixes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n")
	writeFile(t, root, "a.go", "package m\nfunc init() {}\nfunc init() {}\n")
	writeFile(t, root, "b.go", "package m\nfunc init() {}\n")
	f := facts{buildCode(t, root).quads}
	got := f.subjects(KindFunc)
	want := []string{"func:..init", "func:..init#2", "func:..init#3"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("init funcs = %v, want %v", got, want)
	}
	if line := f.one(t, "func:..init#3", "defined_in"); line != quad.Value(quad.IRI("file:b.go")) {
		t.Errorf("third init is in %v, want file:b.go", line)
	}
}

func TestFirstSentenceAndTruncate(t *testing.T) {
	for in, want := range map[string]string{
		"A short one.":                                 "A short one.",
		"First. Second.":                               "First.",
		"No period at all":                             "No period at all",
		"Wraps\nacross lines. Then more.":              "Wraps across lines.",
		"See docs/public/GRAPH_SCHEMA.md for details.": "See docs/public/GRAPH_SCHEMA.md for details.",
		"  leading   and   inner   spaces.  Next.  ":   "leading and inner spaces.",
		"": "",
	} {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 300)
	got := firstSentence(long)
	if r := []rune(got); len(r) != maxDocRunes || r[len(r)-1] != '…' {
		t.Errorf("long doc not capped to %d runes: %d runes", maxDocRunes, len(r))
	}
}

func TestGenericDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n")
	writeFile(t, root, "g.go", `package m

// Set is a generic set.
type Set[T comparable] struct{ m map[T]struct{} }

// Add adds v.
func (s *Set[T]) Add(v T) {}

type Pair[K comparable, V any] struct{}

func (Pair[K, V]) Key() {}

type Alias = Set[string]

type Handler func(string) error

// Map applies f.
func Map[T, U any](xs []T, f func(T) U) []U { return nil }
`)
	f := facts{buildCode(t, root).quads}
	for id, sig := range map[string]string{
		"typ:..Set":       "type Set[T comparable] struct",
		"typ:..Pair":      "type Pair[K comparable, V any] struct",
		"typ:..Alias":     "type Alias = Set[string]",
		"typ:..Handler":   "type Handler func(string) error",
		"func:..Set.Add":  "func (s *Set[T]) Add(v T)",
		"func:..Pair.Key": "func (Pair[K, V]) Key()",
		"func:..Map":      "func Map[T, U any](xs []T, f func(T) U) []U",
	} {
		if got := f.one(t, id, "signature"); got != quad.Value(quad.String(sig)) {
			t.Errorf("%s signature = %v, want %q", id, got, sig)
		}
	}
}
