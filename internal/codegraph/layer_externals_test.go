package codegraph

import (
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

// The fixture is an application built on a stub framework (testdata/stubfw), so its graph
// has a framework side: the packages it imports and the functions it calls.
func externalsWorld(t *testing.T) *world {
	t.Helper()
	opts := fixtureOptions()
	opts.Calls = true
	return newTestWorld(t, fixtureRoot, opts)
}

func TestExternalsLayerGolden(t *testing.T) {
	checkGolden(t, "externals", buildLayer(t, externalsWorld(t), externalsLayer{}).quads)
}

func TestExternalsLayerFacts(t *testing.T) {
	f := facts{buildLayer(t, externalsWorld(t), externalsLayer{}).quads}

	for id, name := range map[string]string{
		"ext:example.com/stubfw":                "stubfw",
		"ext:example.com/stubfw/web":            "web",
		"ext:example.com/stubfw.Main":           "stubfw.Main",
		"ext:example.com/stubfw/web.HTMXScript": "web.HTMXScript",
	} {
		if got := f.one(t, id, "name"); got != str(name) {
			t.Errorf("%s name = %v, want %s", id, got, name)
		}
		if got := f.one(t, id, "origin"); got != str("framework") {
			t.Errorf("%s origin = %v", id, got)
		}
		if got := f.one(t, id, "module"); got != str("example.com/stubfw") {
			t.Errorf("%s module = %v", id, got)
		}
		// the stub is reached by a local replace
		if got := f.one(t, id, "module_version"); got != str("(devel)") {
			t.Errorf("%s version = %v", id, got)
		}
		if got := f.one(t, id, "type"); got != iri("kind:external") {
			t.Errorf("%s type = %v", id, got)
		}
	}
	// the standard library is not recorded, and no node is the project's own
	for _, id := range f.subjects(KindExternal) {
		if !strings.HasPrefix(id, "ext:example.com/stubfw") {
			t.Errorf("an external node that is not the framework's: %s", id)
		}
	}
	// the framework's internal/ is reached by no application call: nothing collapses here
	if f.exists("ext:example.com/stubfw/internal") {
		t.Error("an internal leaf nothing points at")
	}
}

// Every edge to an external node, in whichever layer, has its node in the externals layer: the
// layers that point at externals write the edge only.
func TestEveryExternalEdgeHasItsNode(t *testing.T) {
	w := externalsWorld(t)
	nodes := map[string]bool{}
	for _, q := range buildLayer(t, w, externalsLayer{}).quads {
		if q.Predicate == quad.Value(PredType) {
			nodes[string(q.Subject.(quad.IRI))] = true
		}
	}
	pointed := 0
	for name, l := range map[string]layer{"code": codeLayer{}, "templates": templatesLayer{}, "routes": routesLayer{}, "calls": callsLayer{}} {
		for _, q := range buildLayer(t, w, l).quads {
			switch q.Predicate {
			case quad.Value(PredImportsExt), quad.Value(PredCallsExt), quad.Value(PredHandledByExt):
				pointed++
				if id := string(q.Object.(quad.IRI)); !nodes[id] {
					t.Errorf("layer %s points at %s, which the externals layer does not write", name, id)
				}
			}
			if q.Predicate == quad.Value(PredType) && q.Object == quad.Value(KindExternal) {
				t.Errorf("layer %s wrote an external node (%v): the externals layer owns them", name, q.Subject)
			}
		}
	}
	if pointed < 4 {
		t.Errorf("only %d edges to externals: the fixture should have imports and calls", pointed)
	}
}

// A project that is not built on a framework has no externals, and the layer says so with no quads.
func TestExternalsLayerOfAProjectWithoutAFramework(t *testing.T) {
	w := newTestWorld(t, layoutRoot, Options{})
	if out := buildLayer(t, w, externalsLayer{}); len(out.quads) != 0 {
		t.Errorf("%d quads for a project that imports nothing from a framework", len(out.quads))
	}
}
