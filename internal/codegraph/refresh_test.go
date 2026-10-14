package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/quad"
)

func ran(rep *Report) []string {
	var names []string
	for _, l := range rep.Layers {
		if l.Ran {
			names = append(names, l.Name)
		}
	}
	return names
}

func layerNames(ix *Indexer) []string {
	var names []string
	for _, l := range ix.layers {
		names = append(names, l.name())
	}
	return names
}

func appendTo(t *testing.T, root, rel, extra string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(b, []byte(extra)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureIndexer(t *testing.T) (*Indexer, *cayley.Handle, string) {
	t.Helper()
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	opts := fixtureOptions()
	opts.Root = root
	return New(h.QuadStore, opts), h, root
}

// Editing docs/**, which the dev server's file watcher ignores, must show up
// without a restart: Refresh rebuilds exactly the layers whose inputs, or
// whose dependencies' inputs, changed.
func TestRefreshRebuildsOnlyStaleLayers(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)

	// Nothing has been built yet: everything is stale, and Refresh builds it.
	stale, err := ix.Stale(ctx)
	if err != nil || !reflect.DeepEqual(stale, layerNames(ix)) {
		t.Fatalf("before the first run Stale = %v (%v), want every layer", stale, err)
	}
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil || !reflect.DeepEqual(ran(rep), layerNames(ix)) {
		t.Fatalf("first Refresh: %v %v", ran(rep), err)
	}

	// Everything fresh: Refresh does nothing, and says so.
	if rep, err := ix.Refresh(ctx); rep != nil || err != nil {
		t.Fatalf("Refresh of a fresh graph = %v, %v; want nil, nil", rep, err)
	}
	if stale, _ := ix.Stale(ctx); len(stale) != 0 {
		t.Fatalf("Stale of a fresh graph = %v", stale)
	}

	item := `g.V(g.IRI('item:hello/greeting-analytics'))`
	wantIDs(t, "the new row does not exist yet", gizmoIDs(t, h, item+`.Out(g.IRI('type')).All()`))

	check := func(what string, edit func(), wantStale ...string) {
		t.Helper()
		edit()
		stale, err := ix.Stale(ctx)
		if err != nil || !reflect.DeepEqual(stale, wantStale) {
			t.Fatalf("%s: Stale = %v (%v), want %v", what, stale, err, wantStale)
		}
		rep, err := ix.Refresh(ctx)
		if err != nil || rep == nil {
			t.Fatalf("%s: Refresh = %v, %v", what, rep, err)
		}
		if got := ran(rep); !reflect.DeepEqual(got, wantStale) {
			t.Fatalf("%s: Refresh rebuilt %v, want exactly %v", what, got, wantStale)
		}
		if rep.Failed() {
			t.Fatalf("%s: %+v", what, rep.Layers)
		}
		if stale, _ := ix.Stale(ctx); len(stale) != 0 {
			t.Fatalf("%s: still stale after Refresh: %v", what, stale)
		}
	}

	// A spec edit: only the docs layer.
	check("spec edit", func() {
		appendTo(t, root, "docs/specs/SPEC_HELLO.md", "| Greeting analytics | No | | Planned: `actions.Hello` counts calls first |\n")
	}, "docs", "findings")
	wantIDs(t, "the new spec row is in the graph", gizmoIDs(t, h, item+`.Out(g.IRI('status')).All()`), "planned")
	wantIDs(t, "and the spec now has eight items",
		gizmoIDs(t, h, `g.V(g.IRI('spec:hello')).Out(g.IRI('has_item')).All()`),
		"<item:hello/get-api-hello-endpoint>", "<item:hello/landing-page>", "<item:hello/mcp-tool-fixture-ping>",
		"<item:hello/greeting-history>", "<item:hello/localised-greetings>", "<item:hello/settings-fixture-db-max-conns>", "<item:hello/audit-trail>", "<item:hello/greeting-analytics>")

	// A doc edit that adds a mention: only docs.
	check("doc edit", func() {
		appendTo(t, root, "docs/public/GUIDE.md", "\nSee also `fixture_echo`.\n")
	}, "docs", "findings")
	wantIDs(t, "the new mention", gizmoIDs(t, h, `g.V(g.IRI('doc:docs/public/GUIDE.md')).Out(g.IRI('mentions')).Has(g.IRI('type'), g.IRI('kind:mcp_tool')).All()`), "<tool:fixture_echo>")

	// A template edit: the templates layer, and docs, which mentions templates.
	check("template edit", func() {
		appendTo(t, root, "assets/templates/home.templ", "\ntempl Banner() {\n\t<p>hi</p>\n}\n")
	}, "templates", "docs", "externals", "findings")
	wantIDs(t, "the new template", gizmoIDs(t, h, `g.V(g.IRI('tmpl:assets/templates.Banner')).Out(g.IRI('name')).All()`), "Banner")

	// A config edit: config, and docs, which mentions keys.
	check("env.yml edit", func() {
		appendTo(t, root, "env.yml", "\ncache:\n  ttl: ${FIXTURE_CACHE_TTL:60}\n")
	}, "config", "docs", "findings")
	wantIDs(t, "the new key", gizmoIDs(t, h, `g.V(g.IRI('cfg:cache.ttl')).Out(g.IRI('default')).All()`), "60")

	// A migration edit: the db layer, and docs, which mentions tables.
	check("migration edit", func() {
		writeFile(t, root, "storage/migrations/sql/0002_tags.sql", "-- +goose Up\nCREATE TABLE tags (id INT PRIMARY KEY, label VARCHAR(20));\n")
	}, "db", "docs", "findings")
	wantIDs(t, "the new table", gizmoIDs(t, h, `g.V(g.IRI('table:tags')).Out(g.IRI('source')).All()`), "migration")

	// A Go source edit: the code layer and everything that resolves against it.
	check("go edit", func() {
		appendTo(t, root, "business/actions/hello.go", "\n// Bye says goodbye.\nfunc Bye() string { return \"bye\" }\n")
	}, "code", "routes", "templates", "config", "mcp", "db", "docs", "externals", "findings")
	wantIDs(t, "the new function", gizmoIDs(t, h, `g.V(g.IRI('func:business/actions.Bye')).Out(g.IRI('doc')).All()`), "Bye says goodbye.")

	// The layers that did not run kept their own records.
	meta := facts{storedLabel(t, h, metaLayer)}
	for _, l := range layerNames(ix) {
		want := "ok"
		if l == callsLayerName || l == gitLayerName {
			want = "off" // opt-in, and nothing here switched them on
		}
		if got := meta.one(t, "layer:"+l, "status"); got != quad.Value(quad.String(want)) {
			t.Errorf("layer %s status = %v, want %s", l, got, want)
		}
	}
}

// A report from Refresh says which layers it rebuilt, and carries every layer.
func TestRefreshReport(t *testing.T) {
	ctx := context.Background()
	ix, _, root := fixtureIndexer(t)
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	appendTo(t, root, "docs/public/GUIDE.md", "\nMore words.\n")
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil {
		t.Fatal(rep, err)
	}
	if len(rep.Layers) != len(ix.layers) {
		t.Errorf("report has %d layers, want %d", len(rep.Layers), len(ix.layers))
	}
	for _, l := range rep.Layers {
		if l.Ran != (l.Name == "docs" || l.Name == "findings") {
			t.Errorf("layer %s Ran = %v", l.Name, l.Ran)
		}
		if !l.Ran && (l.Added != 0 || l.Removed != 0) {
			t.Errorf("layer %s did not run but reports +%d/-%d", l.Name, l.Added, l.Removed)
		}
		if l.Status != statusOK && l.Status != statusPartial && l.Status != statusOff {
			t.Errorf("layer %s status %s", l.Name, l.Status)
		}
	}
	// The total is the whole graph's size, not just what this call touched.
	var sum int
	for _, l := range rep.Layers {
		sum += l.Quads
	}
	if rep.Quads != sum || rep.Quads <= rep.Added {
		t.Errorf("Quads = %d, layers sum to %d, added %d", rep.Quads, sum, rep.Added)
	}
}

// A layer that fails is not retried on every page load or tool call, only once
// its inputs change; an explicit Run retries it at once.
func TestRefreshDoesNotRetryAFailedLayerUntilItsInputsChange(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	good := nquads(t, storedLabel(t, h, "config"))

	appendTo(t, root, "env.yml", "\nbroken: [unclosed\n")
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil {
		t.Fatal(rep, err)
	}
	var cfg LayerReport
	for _, l := range rep.Layers {
		if l.Name == "config" {
			cfg = l
		}
	}
	if cfg.Status != statusError || !strings.Contains(cfg.Error, "env.yml") {
		t.Fatalf("config layer = %+v", cfg)
	}
	if after := nquads(t, storedLabel(t, h, "config")); after != good {
		t.Error("a failed layer changed its previous quads")
	}

	if rep, err := ix.Refresh(ctx); rep != nil || err != nil {
		t.Fatalf("a failed layer with unchanged inputs was retried: %v %v", rep, err)
	}
	// An explicit reindex does retry (and fails the same way).
	rep, err = ix.Run(ctx)
	if err != nil || !rep.Failed() {
		t.Fatalf("Run = %v, %v", rep, err)
	}

	// Fixing the input heals it on the next Refresh.
	if err := os.WriteFile(filepath.Join(root, "env.yml"), []byte("env: ${APP_ENV:local}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = ix.Refresh(ctx)
	if err != nil || rep == nil || rep.Failed() {
		t.Fatalf("after the fix: %v %v", rep, err)
	}
	wantIDs(t, "only the remaining key", gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:config_key')).Has(g.IRI('declared'), true).All()`), "<cfg:env>")
}

// An offline run (no router) must not erase what a running server derived from
// its router: the routes layer is skipped, not emptied.
func TestOfflineRunLeavesRoutesAlone(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	before := nquads(t, storedLabel(t, h, "routes"))
	if before == "" {
		t.Fatal("no route quads")
	}

	offline := New(h.QuadStore, Options{Root: root}) // same store, no router
	rep, err := offline.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		switch l.Name {
		case "routes":
			if l.Status != statusSkipped || l.Note == "" {
				t.Errorf("routes layer = %+v", l)
			}
		case "docs":
			if l.Status != statusPartial {
				t.Errorf("docs without routes should be partial: %+v", l)
			}
		}
	}
	if after := nquads(t, storedLabel(t, h, "routes")); after != before {
		t.Errorf("an offline run changed the routes layer:\n%s", lineDiff(lines(before), lines(after)))
	}
	if rep.Failed() {
		t.Errorf("an offline run is not a failure: %+v", rep.Layers)
	}
}

// The fingerprint of a layer includes its dependencies', so a dependent layer
// reads as stale whenever what it resolves against changed.
func TestFingerprintsIncludeDependencies(t *testing.T) {
	ix, _, root := fixtureIndexer(t)
	fps := func() map[string]fpResult {
		w := newWorld(context.Background(), ix, mustResolve(t, root), false)
		return ix.fingerprints(w)
	}
	base := fps()
	for name, r := range base {
		if r.err != nil {
			t.Fatalf("%s: %v", name, r.err)
		}
	}

	appendTo(t, root, "docs/public/GUIDE.md", "\nx\n")
	after := fps()
	for name := range base {
		if changed := base[name].fp != after[name].fp; changed != (name == "docs" || name == "findings") {
			t.Errorf("after a doc edit, %s fingerprint changed = %v", name, changed)
		}
	}

	appendTo(t, root, "controllers/hello.go", "\n// x\n")
	next := fps()
	for _, name := range []string{"code", "routes", "templates", "config", "mcp", "db", "docs", "findings"} {
		if next[name].fp == after[name].fp {
			t.Errorf("after a Go edit, %s fingerprint did not change", name)
		}
	}
}

func mustResolve(t *testing.T, root string) string {
	t.Helper()
	abs, err := resolveRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// Boot, an HTTP reindex, page loads and agents overlap; runs are serialised and
// the graph ends consistent.
func TestConcurrentRunsAndRefreshes(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				if _, err := ix.Run(ctx); err != nil {
					t.Errorf("Run: %v", err)
				}
				return
			}
			if _, err := ix.Refresh(ctx); err != nil {
				t.Errorf("Refresh: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if stale, _ := ix.Stale(ctx); len(stale) != 0 {
		t.Errorf("stale after concurrent runs: %v", stale)
	}
	if rep, err := ix.Run(ctx); err != nil || rep.Failed() || rep.Added != 0 || rep.Removed != 0 {
		t.Errorf("graph not converged: %+v %v", rep, err)
	}
	_ = h
	_ = root
}
