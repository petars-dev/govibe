package codegraph

import (
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

// scopeQuads is a small project seen from both sides: the app's route, handler and
// package; the framework's route, middleware and external package the app imports; a
// setting of each; and the bookkeeping nodes every graph has.
func scopeQuads() []quad.Quad {
	set := newQuadSet("test")
	typ := func(id quad.IRI, kind quad.IRI, name string) {
		set.add(id, PredType, kind)
		set.str(id, PredName, name)
	}
	// the app
	typ("pkg:controllers", KindPackage, "controllers")
	set.str("pkg:controllers", PredPath, "controllers")
	set.str("pkg:controllers", PredArchLayer, "controllers")
	typ("func:controllers.Hello", KindFunc, "Hello")
	set.add("func:controllers.Hello", PredInPackage, quad.IRI("pkg:controllers"))
	typ("route:GET:/hello", KindRoute, "GET /hello")
	set.str("route:GET:/hello", PredMethod, "GET")
	set.str("route:GET:/hello", PredPath, "/hello")
	set.str("route:GET:/hello", PredOrigin, OriginApp)
	set.add("route:GET:/hello", PredHandledBy, quad.IRI("func:controllers.Hello"))
	typ("mw:controllers.Auth", KindMiddleware, "Auth")
	set.str("mw:controllers.Auth", PredOrigin, OriginApp)
	set.add("route:GET:/hello", PredUsesMW, quad.IRI("mw:controllers.Auth"))
	typ("cfg:shop.currency", KindConfigKey, "shop.currency")
	set.str("cfg:shop.currency", PredOrigin, OriginApp)
	set.add("func:controllers.Hello", PredUsesConfig, quad.IRI("cfg:shop.currency"))

	// the framework
	typ("route:GET:/dev/graph", KindRoute, "GET /dev/graph")
	set.str("route:GET:/dev/graph", PredMethod, "GET")
	set.str("route:GET:/dev/graph", PredPath, "/dev/graph")
	set.str("route:GET:/dev/graph", PredOrigin, OriginFramework)
	typ("mw:chi.RequestID", KindMiddleware, "RequestID")
	set.str("mw:chi.RequestID", PredOrigin, OriginFramework)
	set.add("route:GET:/hello", PredUsesMW, quad.IRI("mw:chi.RequestID"))
	set.add("route:GET:/dev/graph", PredUsesMW, quad.IRI("mw:chi.RequestID"))
	typ("ext:fw/web", KindExternal, "web")
	set.str("ext:fw/web", PredOrigin, OriginFramework)
	set.add("pkg:controllers", PredImportsExt, quad.IRI("ext:fw/web"))
	typ("ext:fw/config", KindExternal, "config")
	set.str("ext:fw/config", PredOrigin, OriginFramework)
	set.add("pkg:controllers", PredImportsExt, quad.IRI("ext:fw/config"))
	typ("cfg:server.port", KindConfigKey, "server.port")
	set.str("cfg:server.port", PredOrigin, OriginFramework)
	set.add("func:controllers.Hello", PredUsesConfig, quad.IRI("cfg:server.port"))
	typ("tool:graph_context", KindMCPTool, "graph_context")
	set.str("tool:graph_context", PredOrigin, OriginFramework)

	// findings: one about the app, one about the framework's setting
	set.add("finding:layering:pkg:controllers", PredType, KindFinding)
	set.add("finding:layering:pkg:controllers", PredAbout, quad.IRI("pkg:controllers"))
	set.str("finding:layering:pkg:controllers", PredRule, "layering")
	set.add("finding:config.unused:cfg:server.port", PredType, KindFinding)
	set.add("finding:config.unused:cfg:server.port", PredAbout, quad.IRI("cfg:server.port"))
	set.str("finding:config.unused:cfg:server.port", PredRule, "config.unused")
	// an inferred edge between a table of each side, and its annotation
	typ("table:a", KindTable, "a")
	typ("table:b", KindTable, "b")
	set.str("table:b", PredOrigin, OriginFramework)
	set.add("table:a", PredImplicitRef, quad.IRI("table:b"))
	set.add("ann:1", PredType, KindAnnotation)
	set.add("ann:1", PredSubject, quad.IRI("table:a"))
	set.add("ann:1", PredObject, quad.IRI("table:b"))

	// bookkeeping
	set.add("build:latest", PredType, KindBuild)
	set.add("layer:code", PredType, KindLayer)
	set.add("build:latest", PredHasLayer, quad.IRI("layer:code"))
	set.add("rule:layering", PredType, KindRule)
	return set.quads
}

func TestParseScope(t *testing.T) {
	for in, want := range map[string]Scope{
		"": ScopeApp, "app": ScopeApp, " App ": ScopeApp, "my app": ScopeApp,
		"framework": ScopeFramework, "FW": ScopeFramework, "all": ScopeAll, "both": ScopeAll,
	} {
		got, err := ParseScope(in)
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseScope("everything"); err == nil || !strings.Contains(err.Error(), "app, framework or all") {
		t.Errorf("an unknown scope must say what is valid: %v", err)
	}
}

func has(m *Model, ids ...string) (missing []string) {
	for _, id := range ids {
		if m.Node(id) == nil {
			missing = append(missing, id)
		}
	}
	return missing
}

func TestScopeAllShowsEverythingAndHidesNothing(t *testing.T) {
	m := modelOf(scopeQuads())
	all := m.scoped(ScopeAll)
	if all != m || all.Scope() != ScopeAll || all.Hidden().Nodes != 0 || all.hiddenNeighbours("pkg:controllers") != nil {
		t.Errorf("scope all must be the whole model: %+v", all.Hidden())
	}
}

func TestAppScopeDropsTheFrameworksNodesAndEverythingThatTouchesThem(t *testing.T) {
	full := modelOf(scopeQuads())
	m := full.scoped(ScopeApp)

	if miss := has(m, "pkg:controllers", "func:controllers.Hello", "route:GET:/hello", "mw:controllers.Auth", "cfg:shop.currency",
		"finding:layering:pkg:controllers", "build:latest", "layer:code", "rule:layering", "table:a"); len(miss) > 0 {
		t.Errorf("app scope lost its own nodes: %q", miss)
	}
	for _, id := range []string{"route:GET:/dev/graph", "mw:chi.RequestID", "ext:fw/web", "ext:fw/config", "cfg:server.port", "tool:graph_context", "table:b"} {
		if m.Node(id) != nil {
			t.Errorf("app scope shows the framework's %s", id)
		}
	}
	if m.Node("finding:config.unused:cfg:server.port") != nil {
		t.Error("a finding about a hidden node must be hidden with it")
	}
	if m.Node("ann:1") != nil {
		t.Error("an annotation with an end hidden has nothing to describe")
	}

	// edges to a hidden node are gone from both ends
	route := m.Node("route:GET:/hello")
	if got := route.Out["uses_mw"]; len(got) != 1 || got[0] != "mw:controllers.Auth" {
		t.Errorf("the route's middleware = %q: the framework's RequestID must not be in the flow", got)
	}
	if got := m.Node("pkg:controllers").Out["imports_external"]; len(got) != 0 {
		t.Errorf("the package still points at externals: %q", got)
	}
	if got := m.Node("func:controllers.Hello").Out["uses_config"]; len(got) != 1 || got[0] != "cfg:shop.currency" {
		t.Errorf("uses_config = %q", got)
	}
	if got := m.Node("table:a").Out["implicit_ref"]; len(got) != 0 {
		t.Errorf("implicit_ref to a hidden table survived: %q", got)
	}
	for _, id := range m.ids {
		n := m.Node(id)
		for _, lists := range []map[string][]string{n.Out, n.In} {
			for pred, targets := range lists {
				for _, tgt := range targets {
					if m.Node(tgt) == nil {
						t.Errorf("dangling edge %s -%s-> %s", id, pred, tgt)
					}
				}
			}
		}
	}
	for _, k := range []string{"route", "middleware", "external", "config_key"} {
		for _, n := range m.Kind(k) {
			if m.originOf(n) != OriginApp {
				t.Errorf("%s in app scope has origin %q", n.ID, m.originOf(n))
			}
		}
	}
	if got := len(m.Kind("route")); got != 1 {
		t.Errorf("%d routes in app scope, want 1", got)
	}
}

func TestFrameworkScopeIsTheOtherHalf(t *testing.T) {
	m := modelOf(scopeQuads()).scoped(ScopeFramework)
	if miss := has(m, "route:GET:/dev/graph", "mw:chi.RequestID", "ext:fw/web", "cfg:server.port", "tool:graph_context", "build:latest"); len(miss) > 0 {
		t.Errorf("framework scope lost nodes: %q", miss)
	}
	for _, id := range []string{"pkg:controllers", "route:GET:/hello", "mw:controllers.Auth", "cfg:shop.currency", "func:controllers.Hello"} {
		if m.Node(id) != nil {
			t.Errorf("framework scope shows the app's %s", id)
		}
	}
	// the framework's finding is about its own setting, so it is its own
	if m.Node("finding:config.unused:cfg:server.port") == nil {
		t.Error("a finding about a framework node belongs to the framework scope")
	}
	if m.Node("finding:layering:pkg:controllers") != nil {
		t.Error("the app's finding is not in the framework scope")
	}
	if got := m.Node("mw:chi.RequestID").In["uses_mw"]; len(got) != 1 || got[0] != "route:GET:/dev/graph" {
		t.Errorf("RequestID is used by %q, want only the framework's own route", got)
	}
}

func TestAScopedModelAccountsForWhatItHides(t *testing.T) {
	m := modelOf(scopeQuads()).scoped(ScopeApp)
	h := m.Hidden()
	if h.Scope != ScopeApp || h.ByOrigin[OriginFramework] == 0 || h.ByKind["route"] != 1 || h.ByKind["external"] != 2 || h.ByKind["middleware"] != 1 {
		t.Errorf("hidden = %+v", h)
	}
	// the framework's route, middleware, two externals, setting, tool, table and its finding; and the
	// annotation of the inferred edge to that table, which has no origin of its own (it is the app's)
	if h.Nodes != 9 || h.ByOrigin[OriginFramework] != 8 || h.ByOrigin[OriginApp] != 1 {
		t.Errorf("hidden = %+v, want 9 nodes: 8 framework and the app's annotation", h)
	}
	// the framework's finding is hidden and is the framework's, though the node it was about is gone too
	if h.ByKind["finding"] != 1 || h.ByOrigin[OriginFramework] < h.ByKind["finding"] {
		t.Errorf("hidden findings = %d in %v", h.ByKind["finding"], h.ByOrigin)
	}
	if st := m.Status(nil); st.Scope != ScopeApp {
		t.Errorf("status scope = %q", st.Scope)
	}
}

func TestAnInspectorSaysWhatItUsesInTheOtherScopeInText(t *testing.T) {
	m := modelOf(scopeQuads()).scoped(ScopeApp)
	in, err := m.Inspect("pkg:controllers", LinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Uses, "uses framework:") || !strings.Contains(in.Uses, "web") || !strings.Contains(in.Uses, "config") {
		t.Errorf("uses = %q", in.Uses)
	}
	for _, g := range in.Out {
		if strings.HasSuffix(g.Predicate, "_external") {
			t.Errorf("a hidden edge is listed as a node: %+v", g)
		}
	}
	// the function also reads the framework's server.port
	fn, err := m.Inspect("func:controllers.Hello", LinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fn.Uses, "server.port") {
		t.Errorf("uses = %q", fn.Uses)
	}
	// nothing hidden, nothing said
	if in, _ := modelOf(scopeQuads()).Inspect("pkg:controllers", LinkOptions{}); in.Uses != "" {
		t.Errorf("scope all has nothing to say: %q", in.Uses)
	}
}

func TestEveryNodeIsLabelledWithItsOrigin(t *testing.T) {
	m := modelOf(scopeQuads())
	for id, want := range map[string]string{
		"route:GET:/hello": "app", "route:GET:/dev/graph": "framework", "mw:chi.RequestID": "framework",
		"pkg:controllers": "app", "ext:fw/web": "framework", "cfg:server.port": "framework",
		"finding:config.unused:cfg:server.port": "framework", "finding:layering:pkg:controllers": "app",
		"build:latest": "", "layer:code": "", "rule:layering": "",
	} {
		if got := m.Ref(m.Node(id)).Origin; got != want {
			t.Errorf("origin of %s = %q, want %q", id, got, want)
		}
	}
}

func TestANodeInTheOtherScopeIsSaidToBeThere(t *testing.T) {
	m := modelOf(scopeQuads()).scoped(ScopeApp)
	for _, ref := range []string{"route:GET:/dev/graph", "GET /dev/graph", "web", "server.port"} {
		_, err := m.resolveOne(ref)
		if err == nil || !strings.Contains(err.Error(), "scope app") || !strings.Contains(err.Error(), "framework") {
			t.Errorf("resolve(%q) = %v: it must say the node exists in the other scope", ref, err)
		}
	}
	if _, err := m.resolveOne("no-such-thing"); err == nil || strings.Contains(err.Error(), "framework") {
		t.Errorf("a name that exists nowhere must not blame the scope: %v", err)
	}
	// and it resolves normally with the right scope
	all := modelOf(scopeQuads())
	if n, err := all.resolveOne("route:GET:/dev/graph"); err != nil || n == nil {
		t.Errorf("scope all: %v", err)
	}
}

func TestTheGateJudgesTheWholeGraphOnly(t *testing.T) {
	quads := append(scopeQuads(),
		quad.Make(quad.IRI("layer:findings"), quad.IRI("type"), KindLayer, quad.IRI("layer:meta")),
		quad.Make(quad.IRI("layer:findings"), quad.IRI("status"), quad.String("ok"), quad.IRI("layer:meta")))
	m := modelOf(quads)
	if _, err := m.scoped(ScopeApp).Gate(GateOptions{FailOn: "error"}); err == nil || !strings.Contains(err.Error(), "whole graph") {
		t.Errorf("a scoped gate must refuse: %v", err)
	}
	if _, err := m.Gate(GateOptions{FailOn: "error"}); err != nil {
		t.Errorf("the whole graph can be judged: %v", err)
	}
}
