package codegraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/graphdb"
)

// manualQuads are what a human adds through the /dev/graph form, plus the two
// near-misses a careless delete could hit: a *string* label spelled like a
// layer, and an IRI label outside the generated layers.
func manualQuads() []quad.Quad {
	return []quad.Quad{
		quad.Make(quad.String("alice"), quad.String("knows"), quad.String("bob"), nil),
		quad.Make(quad.String("pkg:controllers"), quad.String("note"), quad.String("hand-written"), quad.String("layer:code")),
		quad.Make(quad.IRI("pkg:controllers"), quad.IRI("owner"), quad.IRI("team:web"), quad.IRI("layer:manual")),
	}
}

func TestIndexerEndToEnd(t *testing.T) {
	h, dir := openStore(t)
	if err := h.AddQuadSet(manualQuads()); err != nil {
		t.Fatal(err)
	}
	ix := New(h.QuadStore, Options{Root: fixtureRoot})
	ctx := context.Background()

	rep, err := ix.Run(ctx)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if rep.Failed() || rep.Layers[0].Name != "code" || rep.Layers[0].Status != statusOK {
		t.Fatalf("first run report: %+v", rep)
	}
	code := rep.Layers[0]
	if code.Quads == 0 || code.Added != code.Quads || code.Removed != 0 {
		t.Errorf("first run should add everything: %+v", code)
	}

	t.Run("the acceptance query from the spec works", func(t *testing.T) {
		wantIDs(t, "g.V('<pkg:controllers>').Out('<imports>')",
			gizmoIDs(t, h, `g.V('<pkg:controllers>').Out('<imports>').All()`),
			"<pkg:business/actions>", "<pkg:business/services/orders>", "<pkg:storage/orderstore>")
		wantIDs(t, "reverse imports",
			gizmoIDs(t, h, `g.V(g.IRI('pkg:storage/orderstore')).In(g.IRI('imports')).All()`),
			"<pkg:controllers>", "<pkg:business/services/orders>", "<pkg:devmcp>", "<pkg:routes>")
		wantIDs(t, "functions in a file",
			gizmoIDs(t, h, `g.V(g.IRI('file:controllers/orders.go')).In(g.IRI('defined_in')).All()`),
			"<func:controllers.GetOrder>", "<func:controllers.ListOrders>", "<func:controllers.writeJSON>")
	})

	t.Run("meta node describes the run", func(t *testing.T) {
		meta := facts{storedLabel(t, h, metaLayer)}
		if got := meta.one(t, "build:latest", "indexer_version"); got != quad.Value(quad.String(IndexerVersion)) {
			t.Errorf("indexer_version = %v", got)
		}
		if got := meta.out("build:latest", "has_layer"); len(got) != len(rep.Layers) || len(got) < 5 {
			t.Errorf("has_layer = %v, want one per layer (%d)", got, len(rep.Layers))
		}
		// No router is wired here, so the routes layer is not applicable: it is
		// recorded as skipped, with the reason, and writes (and deletes) nothing.
		if got := meta.one(t, "layer:routes", "status"); got != quad.Value(quad.String("skipped")) {
			t.Errorf("routes status = %v", got)
		}
		if got := meta.out("layer:routes", "note"); len(got) != 1 {
			t.Errorf("routes note = %v", got)
		}
		if got := meta.out("layer:routes", "quads"); len(got) != 0 {
			t.Errorf("a skipped layer should not claim a quad count: %v", got)
		}
		if got := meta.one(t, "layer:code", "status"); got != quad.Value(quad.String("ok")) {
			t.Errorf("layer status = %v", got)
		}
		if got := meta.one(t, "layer:code", "quads"); got != quad.Value(quad.Int(code.Quads)) {
			t.Errorf("layer quads = %v, want %d", got, code.Quads)
		}
		if got := len(storedLabel(t, h, "code")); got != code.Quads {
			t.Errorf("store holds %d code quads, report says %d", got, code.Quads)
		}
		fp, ok := meta.one(t, "layer:code", "fingerprint").(quad.String)
		if !ok || len(fp) != 16 || string(fp) != code.Fingerprint {
			t.Errorf("fingerprint = %v, report %q", fp, code.Fingerprint)
		}
		at, ok := meta.one(t, "build:latest", "indexed_at").(quad.Time)
		if !ok || time.Since(time.Time(at)) > time.Minute {
			t.Errorf("indexed_at = %v", at)
		}
		// Timestamps live only on the meta layer, never in generated output.
		for _, q := range storedLabel(t, h, "code") {
			if _, isTime := q.Object.(quad.Time); isTime {
				t.Errorf("code layer carries a timestamp: %v", q)
			}
		}
	})

	before := nquads(t, storedLabel(t, h, "code"))

	t.Run("a second run changes nothing", func(t *testing.T) {
		rep, err := ix.Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if l := rep.Layers[0]; l.Added != 0 || l.Removed != 0 || l.Quads != code.Quads {
			t.Errorf("second run: %+v", l)
		}
		if after := nquads(t, storedLabel(t, h, "code")); after != before {
			t.Errorf("code quads changed between identical runs:\n%s", lineDiff(lines(before), lines(after)))
		}
	})

	t.Run("a restart reproduces identical quads", func(t *testing.T) {
		if err := h.Close(); err != nil {
			t.Fatal(err)
		}
		h2, err := graphdb.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer h2.Close()
		if persisted := nquads(t, storedLabel(t, h2, "code")); persisted != before {
			t.Fatalf("quads did not persist across a restart:\n%s", lineDiff(lines(before), lines(persisted)))
		}
		rep, err := New(h2.QuadStore, Options{Root: fixtureRoot}).Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if l := rep.Layers[0]; l.Added != 0 || l.Removed != 0 {
			t.Errorf("run after restart rewrote quads: %+v", l)
		}
		if after := nquads(t, storedLabel(t, h2, "code")); after != before {
			t.Errorf("restart changed the code quads:\n%s", lineDiff(lines(before), lines(after)))
		}
		// Manual quads survived both runs.
		wantIDs(t, "unlabelled string quad", gizmoIDs(t, h2, `g.V('alice').Out('knows').All()`), "bob")
		wantIDs(t, "string-labelled twin of a node",
			gizmoIDs(t, h2, `g.V('pkg:controllers').Out('note').All()`), "hand-written")
		wantIDs(t, "foreign IRI label",
			gizmoIDs(t, h2, `g.V(g.IRI('pkg:controllers')).Out(g.IRI('owner')).All()`), "<team:web>")
	})
}

// Editing a source changes exactly the quads that depend on it, and stale
// quads disappear; the fingerprint follows.
func TestIndexerFollowsSourceChanges(t *testing.T) {
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	ix := New(h.QuadStore, Options{Root: root})
	ctx := context.Background()

	rep, err := ix.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fp1 := rep.Layers[0].Fingerprint
	if gizmo := gizmoIDs(t, h, `g.V(g.IRI('func:business/actions.Hello')).All()`); len(gizmo) != 1 {
		t.Fatalf("Hello missing: %v", gizmo)
	}

	// Rename Hello, add a file, delete a file.
	hello := filepath.Join(root, "business/actions/hello.go")
	src, _ := os.ReadFile(hello)
	if err := os.WriteFile(hello, []byte(strings.ReplaceAll(string(src), "Hello", "Greet")), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "business/actions/bye.go", "package actions\n\n// Bye says goodbye.\nfunc Bye() string { return \"bye\" }\n")
	if err := os.Remove(filepath.Join(root, "middleware/logging.go")); err != nil {
		t.Fatal(err)
	}

	rep, err = ix.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if l := rep.Layers[0]; l.Added == 0 || l.Removed == 0 || l.Status != statusOK {
		t.Errorf("run after edits: %+v", l)
	}
	if rep.Layers[0].Fingerprint == fp1 {
		t.Error("fingerprint did not change after edits")
	}
	wantIDs(t, "renamed function is gone", gizmoIDs(t, h, `g.V(g.IRI('func:business/actions.Hello')).Out(g.IRI('name')).All()`))
	wantIDs(t, "functions in the actions package",
		gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:func')).Filter(like('func:business/actions.%')).All()`),
		"<func:business/actions.Greet>", "<func:business/actions.Bye>", "<func:business/actions.Notify>")
	wantIDs(t, "deleted file's package and function are gone",
		gizmoIDs(t, h, `g.V().Filter(like('%middleware%')).All()`))
	// Quads of the unchanged files were not rewritten: only the delta moved.
	if l := rep.Layers[0]; l.Added > 40 {
		t.Errorf("a three-file change added %d quads; the diff should be small", l.Added)
	}
}

// A failing layer is reported, keeps its previous quads, and never fails the
// run (boot must not depend on the indexer).
func TestLayerFailureKeepsPreviousQuadsAndRunSucceeds(t *testing.T) {
	h, _ := openStore(t)
	ctx := context.Background()
	if _, err := New(h.QuadStore, Options{Root: fixtureRoot}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	good := nquads(t, storedLabel(t, h, "code"))

	noMod := t.TempDir() // no go.mod: the module root cannot be found
	rep, err := New(h.QuadStore, Options{Root: noMod}).Run(ctx)
	if err != nil {
		t.Fatalf("a failing layer must not fail the run: %v", err)
	}
	l := rep.Layers[0]
	if !rep.Failed() || l.Status != statusError || !strings.Contains(l.Error, "go.mod") {
		t.Fatalf("report = %+v", l)
	}
	if after := nquads(t, storedLabel(t, h, "code")); after != good {
		t.Error("a failed layer changed its previous quads")
	}
	meta := facts{storedLabel(t, h, metaLayer)}
	if got := meta.one(t, "layer:code", "status"); got != quad.Value(quad.String("error")) {
		t.Errorf("meta status = %v", got)
	}
	if got := meta.out("layer:code", "error"); len(got) != 1 {
		t.Errorf("meta error = %v", got)
	}
	if got := meta.out("layer:code", "quads"); len(got) != 0 {
		t.Errorf("an errored layer should not claim a quad count: %v", got)
	}

	if _, err := New(h.QuadStore, Options{Root: filepath.Join(noMod, "does-not-exist")}).Run(ctx); err == nil {
		t.Error("a missing root should fail the run before any write")
	}
}

// A file that does not parse is skipped with a warning and the layer is
// "partial", not failed.
func TestPartialLayer(t *testing.T) {
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	writeFile(t, root, "controllers/broken.go", "package controllers\nfunc (\n")
	rep, err := New(h.QuadStore, Options{Root: root}).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l := rep.Layers[0]
	if l.Status != statusPartial || len(l.Warnings) != 1 || !strings.HasPrefix(l.Warnings[0], "controllers/broken.go:") || rep.Failed() {
		t.Errorf("report = %+v", l)
	}
	wantIDs(t, "the rest of the package is still indexed",
		gizmoIDs(t, h, `g.V(g.IRI('pkg:controllers')).In(g.IRI('in_package')).All()`),
		"<file:controllers/hello.go>", "<file:controllers/home.go>", "<file:controllers/orders.go>")
}

func TestCancelledRunWritesNothing(t *testing.T) {
	h, _ := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(h.QuadStore, Options{Root: fixtureRoot}).Run(ctx); err == nil {
		t.Fatal("cancelled run should return an error")
	}
	if n := len(storedLabel(t, h, "code")) + len(storedLabel(t, h, metaLayer)); n != 0 {
		t.Errorf("cancelled run wrote %d quads", n)
	}
}

func TestFingerprint(t *testing.T) {
	root := copyTree(t, fixtureRoot)
	abs, _ := resolveRoot(root)
	fp := func() string {
		t.Helper()
		s, err := codeLayer{}.fingerprint(newTestWorld(t, abs, Options{}))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	base := fp()
	if again := fp(); again != base {
		t.Fatalf("fingerprint is not stable: %s vs %s", base, again)
	}

	hello := filepath.Join(abs, "business/actions/hello.go")
	st, _ := os.Stat(hello)

	// Touching a file (mtime only) changes it...
	if err := os.Chtimes(hello, time.Now(), st.ModTime().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	touched := fp()
	if touched == base {
		t.Error("fingerprint ignored an mtime change")
	}
	// ...and so do added files, removed files, and go.mod.
	writeFile(t, abs, "extra.go", "package main\n")
	if fp() == touched {
		t.Error("fingerprint ignored an added file")
	}
	os.Remove(filepath.Join(abs, "extra.go"))
	if fp() != touched {
		t.Error("fingerprint did not return to its earlier value after the file was removed")
	}
	if err := os.Chtimes(filepath.Join(abs, "go.mod"), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if fp() == touched {
		t.Error("fingerprint ignored go.mod")
	}
	// Inputs the layer skips do not count.
	writeFile(t, abs, "testdata/x.go", "package x\n")
	writeFile(t, abs, "controllers/hello_test.go", "package controllers\n")
	writeFile(t, abs, "notes.md", "# hi\n")
	before := fp()
	writeFile(t, abs, "testdata/y.go", "package y\n")
	if fp() != before {
		t.Error("fingerprint changed for a file the layer never reads")
	}
}

// The writer is the only thing standing between a reindex and the user's own
// quads; pin its refusals.
func TestWriterGuards(t *testing.T) {
	h, _ := openStore(t)
	if err := h.AddQuadSet(manualQuads()); err != nil {
		t.Fatal(err)
	}
	w := newWriter(h.QuadStore)
	mk := func(label quad.Value, o string) quad.Quad {
		return quad.Quad{Subject: quad.IRI("pkg:a"), Predicate: PredImports, Object: quad.IRI(o), Label: label}
	}

	if _, err := w.replace(quad.IRI("manual"), nil); err == nil {
		t.Error("a label outside the layer: namespace must be refused")
	}
	if _, err := w.replace(LayerLabel("code"), []quad.Quad{mk(quad.IRI("layer:other"), "pkg:b")}); err == nil {
		t.Error("a quad carrying a different label must be refused")
	}
	if _, err := w.replace(LayerLabel("code"), []quad.Quad{mk(quad.String("layer:code"), "pkg:b")}); err == nil {
		t.Error("a string label must not pass for the IRI label")
	}

	// Replacing a layer with an empty set deletes only that layer.
	if _, err := w.replace(LayerLabel("code"), []quad.Quad{mk(LayerLabel("code"), "pkg:b"), mk(LayerLabel("code"), "pkg:c")}); err != nil {
		t.Fatal(err)
	}
	d, err := w.replace(LayerLabel("code"), nil)
	if err != nil || d.Removed != 2 || d.Added != 0 || d.Quads != 0 {
		t.Fatalf("empty replace = %+v, %v", d, err)
	}
	if n := len(storedLabel(t, h, "code")); n != 0 {
		t.Errorf("layer not emptied: %d quads", n)
	}
	wantIDs(t, "manual quads untouched", gizmoIDs(t, h, `g.V('alice').Out('knows').All()`), "bob")
	wantIDs(t, "string-labelled quad untouched", gizmoIDs(t, h, `g.V('pkg:controllers').Out('note').All()`), "hand-written")
	wantIDs(t, "foreign-label quad untouched", gizmoIDs(t, h, `g.V(g.IRI('pkg:controllers')).Out(g.IRI('owner')).All()`), "<team:web>")

	// Duplicates in the input collapse.
	d, err = w.replace(LayerLabel("code"), []quad.Quad{mk(LayerLabel("code"), "pkg:b"), mk(LayerLabel("code"), "pkg:b")})
	if err != nil || d.Added != 1 || d.Quads != 1 {
		t.Errorf("duplicate input = %+v, %v", d, err)
	}
}

// failingStore makes ApplyDeltas fail, as a full disk or a closed bolt file would.
type failingStore struct {
	graph.QuadStore
	fail bool
}

func (f *failingStore) ApplyDeltas(in []graph.Delta, opts graph.IgnoreOpts) error {
	if f.fail {
		return errors.New("disk on fire")
	}
	return f.QuadStore.ApplyDeltas(in, opts)
}

// After a write error the writer refuses to write again in this process: a
// retry through a poisoned node cache would silently lose data (F8). The error
// surfaces as that layer's status and the run still succeeds.
func TestWriterStaysBrokenAfterError(t *testing.T) {
	h, _ := openStore(t)
	fs := &failingStore{QuadStore: h.QuadStore}
	ix := New(fs, Options{Root: fixtureRoot})
	ctx := context.Background()

	rep, err := ix.Run(ctx)
	if err != nil || rep.Failed() {
		t.Fatalf("healthy run: %v %+v", err, rep)
	}
	good := nquads(t, storedLabel(t, h, "code"))

	// Change the sources so the next run has something to write, then fail it.
	ix.opts.Root = copyTree(t, fixtureRoot)
	writeFile(t, ix.opts.Root, "business/actions/bye.go", "package actions\n\n// Bye says bye.\nfunc Bye() {}\n")
	fs.fail = true
	rep, err = ix.Run(ctx)
	if err == nil {
		t.Fatal("meta write should have failed too")
	}
	if l := rep.Layers[0]; l.Status != statusError || !strings.Contains(l.Error, "disk on fire") {
		t.Errorf("layer report = %+v", l)
	}
	if after := nquads(t, storedLabel(t, h, "code")); after != good {
		t.Error("failed write changed the stored quads")
	}

	// The disk recovers, but this process stays refused until a restart.
	fs.fail = false
	rep, err = ix.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "restart the server") {
		t.Errorf("run after a write error: err = %v", err)
	}
	if l := rep.Layers[0]; l.Status != statusError || !strings.Contains(l.Error, "restart the server") {
		t.Errorf("layer report after a write error = %+v", l)
	}
	if after := nquads(t, storedLabel(t, h, "code")); after != good {
		t.Error("a refused writer still wrote")
	}

	// A new Indexer (what a restart builds) writes again.
	if rep, err := New(h.QuadStore, Options{Root: ix.opts.Root}).Run(ctx); err != nil || rep.Failed() {
		t.Errorf("fresh indexer: %v %+v", err, rep)
	}
}

func TestVocabularyHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"controllers/devdb.go": "controllers/devdb.go",
		"has space":            "has%20space",
		`quo"te`:               "quo%22te",
		`back\slash`:           "back%5Cslash",
		"100%":                 "100%25",
		"tab\there":            "tab%09here",
		"del\x7f":              "del%7F",
		"日本語/{id}|*":           "日本語/{id}|*",
	} {
		if got := idKey(in); got != want {
			t.Errorf("idKey(%q) = %q, want %q", in, got, want)
		}
		if !validID("x:" + idKey(in)) {
			t.Errorf("idKey(%q) = %q is not a valid ID", in, idKey(in))
		}
	}
	if validID("x: y") || validID(`x:"`) || validID("") {
		t.Error("validID accepts a bad ID")
	}
	// One name, one ID, however it is spelled.
	if idKey("a b") == idKey("a%20b") {
		t.Error("idKey is not one-to-one")
	}

	// Strings that JSON paths would reinterpret as IRIs are kept plain.
	for _, s := range []string{"<x>", "_:x", `"x"^^<t>`} {
		got := lit(s)
		if quad.StringToValue(string(got)) != quad.Value(got) {
			t.Errorf("lit(%q) = %q would still be reinterpreted", s, got)
		}
	}
	if lit("plain") != quad.String("plain") {
		t.Error("lit changed a plain string")
	}
}
