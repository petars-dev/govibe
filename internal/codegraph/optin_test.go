package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/codegraph/internal/graphcheck"
)

func layerReport(rep *Report, name string) LayerReport {
	for _, l := range rep.Layers {
		if l.Name == name {
			return l
		}
	}
	return LayerReport{}
}

// calls and git cost more than parsing, so they are off until asked for: they
// hold no quads, are never stale, and say so.
func TestOptInLayersAreOffByDefault(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	rep, err := ix.Run(ctx)
	if err != nil || rep.Failed() {
		t.Fatalf("run: %v %+v", err, rep)
	}
	for _, name := range []string{callsLayerName, gitLayerName} {
		l := layerReport(rep, name)
		if l.Status != statusOff || l.Quads != 0 || !strings.Contains(l.Note, "opt-in") {
			t.Errorf("%s: %+v", name, l)
		}
		if got := storedLabel(t, h, name); len(got) != 0 {
			t.Errorf("%s holds %d quads while off", name, len(got))
		}
		if ix.Enabled(name) {
			t.Errorf("%s reports itself enabled", name)
		}
	}
	if ix.Enabled("code") != true || ix.Enabled("nosuchlayer") != false {
		t.Error("Enabled: the always-on layers are on, an unknown name is not")
	}
	meta := facts{storedLabel(t, h, metaLayer)}
	if got := meta.one(t, "layer:calls", "status"); got != str("off") {
		t.Errorf("layer:calls status %v", got)
	}
	if got := meta.one(t, "layer:calls", "note"); !strings.Contains(string(got.(quad.String)), "GRAPH_CALLS") {
		t.Errorf("the note says how to switch it on: %v", got)
	}

	// An edit does not make a layer that is off stale.
	appendTo(t, root, "business/actions/hello.go", "\n// Bye says goodbye.\nfunc Bye() string { return \"bye\" }\n")
	stale, _ := ix.Stale(ctx)
	for _, s := range stale {
		if s == callsLayerName || s == gitLayerName {
			t.Errorf("%s is off and cannot be stale: %v", s, stale)
		}
	}
	if rep, err := ix.Refresh(ctx); err != nil || rep == nil || contains2(ran(rep), callsLayerName) {
		t.Errorf("refresh with calls off: %v %v", ran(rep), err)
	}
}

// Asking for an opt-in layer by name is how it is switched on, for the life of
// the Indexer: from then on it is kept as fresh as any other layer.
func TestRunLayersSwitchesAnOptInLayerOn(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	rep, err := ix.RunLayers(ctx, []string{"calls"})
	if err != nil || rep.Failed() {
		t.Fatalf("RunLayers: %v %+v", err, rep)
	}
	if l := layerReport(rep, callsLayerName); l.Status != statusOK || !l.Ran || l.Quads == 0 {
		t.Errorf("calls after being asked for: %+v", l)
	}
	if !ix.Enabled(callsLayerName) || ix.Enabled(gitLayerName) {
		t.Error("only the layer asked for is on")
	}
	if got := (facts{storedLabel(t, h, "calls")}).out("func:controllers.HelloAPI", "calls"); len(got) != 2 {
		t.Errorf("HelloAPI calls %v", got)
	}

	// It is now stale when the sources change, and Refresh rebuilds it.
	appendTo(t, root, "business/actions/hello.go", "\n// Bye says goodbye.\nfunc Bye() string { return Hello() }\n")
	stale, _ := ix.Stale(ctx)
	if !contains2(stale, callsLayerName) {
		t.Fatalf("a Go edit must make calls stale: %v", stale)
	}
	rep, err = ix.Refresh(ctx)
	if err != nil || rep == nil || !contains2(ran(rep), callsLayerName) {
		t.Fatalf("refresh: %v %v", rep, err)
	}
	if got := (facts{storedLabel(t, h, "calls")}).out("func:business/actions.Bye", "calls"); len(got) != 1 {
		t.Errorf("the new function's call is in the graph: %v", got)
	}

	if _, err := ix.RunLayers(ctx, []string{"layer:nosuch"}); err == nil || !strings.Contains(err.Error(), "calls") {
		t.Errorf("an unknown layer is an error that lists the real ones: %v", err)
	}
}

// What a process that had a layer on left in the store must not outlive it: a
// reader would take stale call edges for the code as it is.
func TestOptInLayerQuadsAreRemovedWhenItIsOff(t *testing.T) {
	ctx := context.Background()
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root = fixtureRoot

	on := opts
	on.Calls = true
	if rep, err := New(h.QuadStore, on).Run(ctx); err != nil || layerReport(rep, callsLayerName).Quads == 0 {
		t.Fatalf("with calls on: %v %+v", err, rep)
	}
	if len(storedLabel(t, h, "calls")) == 0 {
		t.Fatal("no call quads were written")
	}

	rep, err := New(h.QuadStore, opts).Run(ctx) // a restart without the setting
	if err != nil {
		t.Fatal(err)
	}
	if l := layerReport(rep, callsLayerName); l.Status != statusOff || l.Removed == 0 {
		t.Errorf("calls after a restart with it off: %+v", l)
	}
	if got := storedLabel(t, h, "calls"); len(got) != 0 {
		t.Errorf("%d stale call quads outlived the setting", len(got))
	}
}

// Every layer at once, the opt-in ones on, over the fixture (in a throw-away
// git repository, so its history is known): the same invariants as everywhere.
func TestFixtureGraphInvariantsWithOptInLayers(t *testing.T) {
	r := newTestRepo(t)
	root := copyTree(t, fixtureRoot)
	r.root = root
	r.git(nil, "init", "-q", "-b", "main")
	for i := 1; i <= 4; i++ {
		r.commit("alice", "Edit the greeting", map[string]string{
			"business/actions/hello.go": strings.Replace(readFile(t, root, "business/actions/hello.go"), "Fixture!", "Fixture"+strings.Repeat("!", i+1), 1),
			"docs/public/GUIDE.md":      readFile(t, root, "docs/public/GUIDE.md") + "\nedit\n",
		})
	}

	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root, opts.Calls, opts.Git = root, true, true
	rep, err := New(h.QuadStore, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		if l.Status == statusError || l.Status == statusOff {
			t.Errorf("layer %s: %+v", l.Name, l)
		}
	}
	res := graphcheck.Check(t, h, graphcheck.Options{
		Layers:       []string{"code", "routes", "templates", "config", "mcp", "db", "docs", "calls", "git", "findings"},
		RequireEdges: []string{"calls", "co_changed_with", "handled_by", "reads_table", "subject", "object", "via"},
	})
	t.Logf("%d quads in total", len(res.Quads))
	wantIDs(t, "route -> handler -> service -> store -> table is a path of stored edges",
		gizmoIDs(t, h, `g.V(g.IRI('route:GET:/api/orders')).Out(g.IRI('handled_by')).Out(g.IRI('calls')).Out(g.IRI('calls')).Out(g.IRI('reads_table')).All()`),
		"<table:orders>")
	wantIDs(t, "the greeting action and its doc changed together",
		gizmoIDs(t, h, `g.V(g.IRI('file:business/actions/hello.go')).Out(g.IRI('co_changed_with')).All()`),
		"<doc:docs/public/GUIDE.md>")
	// The queries GRAPH_SCHEMA.md shows for the opt-in layers.
	wantIDs(t, "what a handler calls",
		gizmoIDs(t, h, `g.V(g.IRI('route:GET:/api/orders')).Out(g.IRI('handled_by')).Out(g.IRI('calls')).All()`),
		"<func:business/services/orders.Service.List>", "<func:controllers.writeJSON>")
	wantIDs(t, "who calls the store",
		gizmoIDs(t, h, `g.V(g.IRI('func:storage/orderstore.Store.List')).In(g.IRI('calls')).All()`), "<func:business/services/orders.Service.List>")
	wantIDs(t, "the interface a call goes through",
		gizmoIDs(t, h, `g.V().Has(g.IRI('subject'), g.IRI('func:business/services/orders.Service.List')).Out(g.IRI('via')).All()`), "<typ:business/services/orders.Lister>")
	wantIDs(t, "how sure the interface call is",
		gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:annotation')).Has(g.IRI('method'), 'interface_dispatch').Out(g.IRI('confidence')).All()`), "70")
	wantIDs(t, "commits of a file",
		gizmoIDs(t, h, `g.V(g.IRI('file:business/actions/hello.go')).Out(g.IRI('commits')).All()`), "4")
	wantIDs(t, "layers that are off", gizmoIDs(t, h, `g.V().Has(g.IRI('status'), 'off').All()`))
	if again, err := New(h.QuadStore, opts).Run(context.Background()); err != nil || again.Added != 0 || again.Removed != 0 {
		t.Errorf("second run: %+v %v", again, err)
	}
}
