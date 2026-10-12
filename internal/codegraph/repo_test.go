package codegraph

import (
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/quad"
)

var knownPredicates = map[quad.IRI]bool{
	PredType: true, PredDefinedIn: true, PredInPackage: true, PredImports: true,
	PredName: true, PredDoc: true, PredSignature: true, PredLine: true, PredExported: true,
	PredPath: true, PredImportPath: true, PredArchLayer: true, PredExternalImports: true,
	PredLines: true, PredGenerated: true,
}

var codeKinds = map[quad.IRI]bool{KindPackage: true, KindFile: true, KindFunc: true, KindType: true}

// The code layer run over this very repository: whatever the source tree
// looks like today, the graph it produces must be well formed. These are the
// invariants agents rely on when they follow edges without checking.
func TestRepoInvariants(t *testing.T) {
	start := time.Now()
	out := buildCode(t, repoRoot)
	elapsed := time.Since(start)
	t.Logf("code layer: %d quads in %s", len(out.quads), elapsed)
	if elapsed > 2*time.Second {
		t.Errorf("code layer took %s; the budget is 500 ms on this repo (asserted loosely for slow machines)", elapsed)
	}
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	// Every subject has exactly one known kind; every quad is in the code layer
	// with a known predicate and a valid ID.
	kind := map[string]quad.IRI{}
	for _, q := range out.quads {
		if q.Label != quad.Value(LayerLabel("code")) {
			t.Fatalf("quad outside layer:code: %v", q)
		}
		subj, ok := q.Subject.(quad.IRI)
		if !ok || !validID(string(subj)) {
			t.Fatalf("subject %v is not a valid IRI ID", q.Subject)
		}
		pred := q.Predicate.(quad.IRI)
		if !knownPredicates[pred] {
			t.Errorf("unknown predicate %s on %s", pred, subj)
		}
		if pred == PredType {
			k, ok := q.Object.(quad.IRI)
			if !ok || !codeKinds[k] {
				t.Errorf("%s has type %v", subj, q.Object)
			}
			if prev, dup := kind[string(subj)]; dup {
				t.Errorf("%s has two kinds: %s and %s", subj, prev, k)
			}
			kind[string(subj)] = k
		}
	}
	for _, q := range out.quads {
		if _, ok := kind[string(q.Subject.(quad.IRI))]; !ok {
			t.Errorf("%v: subject has no type quad", q)
		}
	}

	// No dangling references: the object of every structural predicate is a
	// node the layer defines, of the right kind.
	wantKind := map[quad.IRI][2]quad.IRI{ // predicate -> {subject kinds are checked below, object kind}
		PredImports:   {KindPackage, KindPackage},
		PredInPackage: {KindFile, KindPackage},
	}
	for _, q := range out.quads {
		pred := q.Predicate.(quad.IRI)
		isStructural := false
		for _, sp := range structuralPredicates {
			isStructural = isStructural || sp == pred
		}
		if !isStructural {
			continue
		}
		obj, ok := q.Object.(quad.IRI)
		if !ok {
			t.Errorf("structural %s has a literal object: %v", pred, q)
			continue
		}
		if _, defined := kind[string(obj)]; !defined {
			t.Errorf("dangling reference: %v", q)
		}
		if want, ok := wantKind[pred]; ok {
			if kind[string(q.Subject.(quad.IRI))] != want[0] || kind[string(obj)] != want[1] {
				t.Errorf("%s connects %s to %s, want %s to %s: %v", pred, kind[string(q.Subject.(quad.IRI))], kind[string(obj)], want[0], want[1], q)
			}
		}
		if pred == PredDefinedIn && kind[string(obj)] != KindFile {
			t.Errorf("defined_in must point at a file: %v", q)
		}
		if pred == PredImports && q.Subject == q.Object {
			t.Errorf("package imports itself: %v", q)
		}
	}

	// Per-kind completeness.
	archSet := map[string]bool{}
	for _, a := range archLayers {
		archSet[a] = true
	}
	for id, k := range kind {
		switch k {
		case KindPackage:
			for _, p := range []string{"name", "path", "import_path", "arch_layer", "external_imports"} {
				f.one(t, id, p)
			}
			if a := f.one(t, id, "arch_layer"); !archSet[string(a.(quad.String))] {
				t.Errorf("%s has arch_layer %v", id, a)
			}
		case KindFile:
			for _, p := range []string{"name", "path", "lines", "generated", "in_package"} {
				f.one(t, id, p)
			}
		case KindFunc, KindType:
			for _, p := range []string{"name", "defined_in", "line", "exported", "signature"} {
				f.one(t, id, p)
			}
			if n := f.one(t, id, "line").(quad.Int); n < 1 {
				t.Errorf("%s has line %d", id, n)
			}
		}
	}

	// Nothing the layer is meant to skip leaked in: look at the path segments
	// of every file and package, not at symbol names.
	for id, k := range kind {
		if k != KindFile && k != KindPackage {
			continue
		}
		path := strings.TrimPrefix(strings.TrimPrefix(id, "file:"), "pkg:")
		for i, seg := range strings.Split(path, "/") {
			switch {
			case seg == "testdata", seg == "node_modules",
				strings.HasPrefix(seg, ".") && seg != ".", strings.HasPrefix(seg, "_"),
				i == 0 && (seg == "tmp" || seg == "vendor"),
				strings.HasSuffix(seg, "_test.go"), strings.HasSuffix(seg, "_templ.go"):
				t.Errorf("%s should not be indexed", id)
			}
		}
	}

	// The graph agrees with this repo's own layout today. These are the
	// examples in the spec and the first acceptance queries; they change only
	// when the code they describe does.
	for _, id := range []string{
		"pkg:.", "pkg:config", "pkg:web", "pkg:internal/codegraph", "pkg:internal/graphdb", "pkg:internal/kernel",
		"pkg:cmd/govibe", "func:..Main", "func:web.ServeHTMX", "func:internal/kernel.Main",
		"func:internal/codegraph.Indexer.Run", "typ:internal/codegraph.Indexer",
		"file:govibe.go",
	} {
		if !f.exists(id) {
			t.Errorf("expected %s in the graph of this repo", id)
		}
	}
	if got := f.one(t, "pkg:.", "name"); got != quad.Value(quad.String("govibe")) {
		t.Errorf("root package name = %v", got)
	}
	if got := f.one(t, "pkg:cmd/govibe", "arch_layer"); got != quad.Value(quad.String("wiring")) {
		t.Errorf("cmd/govibe arch_layer = %v", got)
	}
	if got := f.one(t, "pkg:internal/kernel", "arch_layer"); got != quad.Value(quad.String("wiring")) {
		t.Errorf("internal/kernel arch_layer = %v", got)
	}
	if got := f.one(t, "pkg:internal/devtools", "arch_layer"); got != quad.Value(quad.String("dev")) {
		t.Errorf("internal/devtools arch_layer = %v", got)
	}
	for _, c := range [][2]string{
		{"pkg:.", "pkg:internal/kernel"},
		{"pkg:internal/kernel", "pkg:web"},
		{"pkg:internal/kernel", "pkg:internal/devtools"}, // from the file only a govibe_dev build compiles: the code layer reads every file
		{"pkg:internal/devtools", "pkg:internal/devtools/devui"},
		{"pkg:cmd/govibe", "pkg:internal/codegraph"},
	} {
		if !f.has(c[0], "imports", quad.IRI(c[1])) {
			t.Errorf("missing %s --imports--> %s", c[0], c[1])
		}
	}
	if got := f.one(t, "func:web.ServeHTMX", "defined_in"); got != quad.Value(quad.IRI("file:web/web.go")) {
		t.Errorf("ServeHTMX defined_in %v", got)
	}

	// And it is reproducible.
	again := buildCode(t, repoRoot)
	if a, b := nquads(t, out.quads), nquads(t, again.quads); a != b {
		t.Errorf("two builds over the same tree differ:\n%s", lineDiff(lines(a), lines(b)))
	}
}
