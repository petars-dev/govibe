package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

// Regression tests for what the review of Phase 3 found.

// An application that overrides a default of the framework's does not read the setting: the
// framework does. It is not "declared and read by nothing".
func TestAnApplicationOverridingAFrameworkDefaultIsNotAnUnusedKey(t *testing.T) {
	root := appFiles(t)
	writeFile(t, root, "env.yml", "server:\n  port: ${PORT:9090}\nshop:\n  currency: ${SHOP_CURRENCY:EUR}\n")
	manifest := &Manifest{ConfigYAML: []byte("server:\n  port: ${PORT:8080}\n  log_level: ${LOG_LEVEL:info}\n")}
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: root, Manifest: manifest})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := ix.Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	port := m.Node("cfg:server.port")
	if port == nil || port.Str("origin") != "app" || !port.Bool("framework_key") || port.Str("default") != "9090" {
		t.Fatalf("server.port = %+v: the app's value, and the framework's key", port)
	}
	if k := m.Node("cfg:server.log_level"); k == nil || k.Str("origin") != "framework" {
		t.Errorf("log_level = %+v", k)
	}
	got := findingsOf(m, "config.unused")
	if len(got) != 1 || got[0] != "config.unused cfg:shop.currency" {
		t.Errorf("config.unused = %q: only the app's own unread key is unused, not its override of server.port", got)
	}
}

// With no settings file the config layer writes nothing, so a document may not "mention" a
// key it does not have (the framework's, from the manifest, included).
func TestNothingResolvesAgainstSettingsTheConfigLayerDidNotWrite(t *testing.T) {
	root := appFiles(t)
	if err := os.Remove(filepath.Join(root, "env.yml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "docs/public/ARCHITECTURE.md", "# Shop\n\nThe port is `server.port`.\n")
	manifest := &Manifest{ConfigYAML: []byte("server:\n  port: ${PORT:8080}\n")}
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: root, Manifest: manifest})
	rep, err := ix.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cfg := layerReport(rep, "config"); cfg.Status != statusSkipped {
		t.Fatalf("config = %+v", cfg)
	}
	m, _ := ix.Model(context.Background())
	for _, d := range m.Kind("doc") {
		for _, id := range d.Out["mentions"] {
			if strings.HasPrefix(id, "cfg:") {
				t.Errorf("%s mentions %s, a node nothing wrote", d.ID, id)
			}
		}
	}
	// and no node is left without a type: every edge target exists
	for _, id := range m.ids {
		if m.Node(id).Kind == "" {
			t.Errorf("%s is the target of an edge and has no type", id)
		}
	}
}

// A node with no type is a dangling reference: no scope shows it.
func TestScopeDropsDanglingTargets(t *testing.T) {
	set := newQuadSet("test")
	set.add("pkg:a", PredType, KindPackage)
	set.add("pkg:a", PredImportsExt, quad.IRI("ext:gone")) // nothing wrote ext:gone
	m := modelOf(set.quads)
	if m.Node("ext:gone") == nil {
		t.Fatal("the whole graph holds the dangling target")
	}
	for _, scope := range []Scope{ScopeApp, ScopeFramework} {
		sm := m.scoped(scope)
		if sm.Node("ext:gone") != nil {
			t.Errorf("scope %s shows a node with no type", scope)
		}
		if n := sm.Node("pkg:a"); n != nil && len(n.Out["imports_external"]) != 0 {
			t.Errorf("scope %s keeps an edge to it", scope)
		}
	}
}

// What is inside the project's own module is the project's, even under the path of a module it
// requires (the reference app lives under the framework's path).
func TestTheProjectsOwnPackagesAreNeverExternal(t *testing.T) {
	const fw, app = "github.com/petars-dev/govibe", "github.com/petars-dev/govibe/examples/hello"
	mt := newModuleTable(app, BuildFacts{Modules: []Module{{Path: fw, Version: "v0.3.1"}, {Path: "github.com/go-chi/chi/v5", Version: "v5.1.0"}}}, "/nonexistent/go.mod")
	for path, want := range map[string]string{
		fw + "/web":                           fw,
		fw:                                    fw,
		app + "/assets/templates":             "", // the app's own (generated templ output: no node of its own)
		app:                                   "",
		"github.com/go-chi/chi/v5/middleware": "github.com/go-chi/chi/v5",
		"net/http":                            "",
	} {
		if got := mt.moduleOf(path); got != want {
			t.Errorf("moduleOf(%q) = %q, want %q", path, got, want)
		}
	}
	if mt.isFramework(app + "/assets/templates") {
		t.Error("the app's own package is the framework's")
	}
}

func TestExternalLeavesAreBounded(t *testing.T) {
	const fw = "github.com/petars-dev/govibe"
	mt := newModuleTable("example.com/shop", BuildFacts{Modules: []Module{{Path: fw, Version: "v0.3.1"}, {Path: "github.com/go-chi/chi/v5", Version: "v5.1.0"}, {Path: "github.com/sirupsen/logrus", Version: "v1.9.0"}}}, "/nonexistent/go.mod")
	for _, c := range []struct{ path, name, want string }{
		{fw + "/web", "JSON", "ext:" + fw + "/web.JSON"},             // a function of the public API: a leaf of its own
		{fw + "/web", "Config.Get", "ext:" + fw + "/web.Config.Get"}, // a method
		{fw + "/internal/kernel", "Run", "ext:" + fw + "/internal"},  // the framework's internals: one leaf
		{fw + "/internal/devtools/devui", "X", "ext:" + fw + "/internal"},
		{"github.com/go-chi/chi/v5", "Router.Get", "ext:github.com/go-chi/chi/v5"}, // a library: one leaf per package
		{"github.com/go-chi/chi/v5/middleware", "RequestID", "ext:github.com/go-chi/chi/v5/middleware"},
	} {
		if got := mt.leafRef(c.path, c.name); string(got) != c.want {
			t.Errorf("leafRef(%s, %s) = %s, want %s", c.path, c.name, got, c.want)
		}
	}
	// the nodes the externals layer writes for them, with their modules and versions
	set := newQuadSet("externals")
	ex := newExternals(mt, set)
	ex.leaf(fw+"/internal/kernel", "Run")
	ex.leaf(fw+"/internal/kernel", "Other") // collapses to the same node
	ex.leaf("github.com/go-chi/chi/v5", "Router.Get")
	f := facts{set.quads}
	if got := f.subjects(KindExternal); len(got) != 2 {
		t.Errorf("nodes = %v", got)
	}
	in := "ext:" + fw + "/internal"
	if f.one(t, in, "internal") != quad.Value(quad.Bool(true)) || f.one(t, in, "module") != str(fw) || f.one(t, in, "module_version") != str("v0.3.1") || f.one(t, in, "name") != str("govibe (internal)") {
		t.Errorf("the internal leaf: %v", f.out(in, "name"))
	}
	chi := "ext:github.com/go-chi/chi/v5"
	if f.one(t, chi, "name") != str("chi/v5") || f.one(t, chi, "module_version") != str("v5.1.0") || f.one(t, chi, "origin") != str("framework") {
		t.Errorf("chi: name %v", f.out(chi, "name"))
	}
}

// The inspector tells the framework's side from a library's, though both are "not the app's".
func TestTheInspectorSaysLibrariesAreNotTheFramework(t *testing.T) {
	set := newQuadSet("test")
	set.add("build:latest", PredType, KindBuild)
	set.str("build:latest", PredFrameworkModule, "example.com/fw")
	set.add("pkg:a", PredType, KindPackage)
	for id, mod := range map[string]string{"ext:example.com/fw/web": "example.com/fw", "ext:github.com/go-chi/chi/v5": "github.com/go-chi/chi/v5"} {
		qid := quad.IRI(id)
		set.add(qid, PredType, KindExternal)
		set.str(qid, PredName, map[string]string{"ext:example.com/fw/web": "web", "ext:github.com/go-chi/chi/v5": "chi/v5"}[id])
		set.str(qid, PredOrigin, OriginFramework)
		set.str(qid, PredModule, mod)
		set.add("pkg:a", PredImportsExt, qid)
	}
	m := modelOf(set.quads).scoped(ScopeApp)
	got := m.usesText("pkg:a")
	if got != "uses framework: web; uses libraries: chi/v5" {
		t.Errorf("uses = %q", got)
	}
}

// Externals that point at a calls layer that failed must not drop the leaves the calls layer's old
// edges still point at: the layer fails with it and keeps its own old nodes.
func TestExternalsFailWithTheCallsLayerInsteadOfDroppingItsNodes(t *testing.T) {
	root := copyTree(t, fixtureRoot)
	goMod := filepath.Join(root, "go.mod")
	b, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	// break `go list`: a go.mod with a directive it does not know
	if err := os.WriteFile(goMod, append(b, []byte("\nunknowndirective x\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := fixtureOptions()
	opts.Root, opts.Calls = root, true
	w := newTestWorld(t, root, opts)
	if _, err := w.calls(); err == nil {
		t.Fatal("the call index built from a go.mod that does not parse")
	}
	if _, err := (externalsLayer{}).build(context.Background(), w); err == nil || !strings.Contains(err.Error(), "calls index") {
		t.Errorf("externals built from a failed calls layer: %v", err)
	}
}

// The call index is the one expensive step: an edit to a template does not repeat it.
func TestTheCallIndexIsReusedUntilItsInputsChange(t *testing.T) {
	ctx := context.Background()
	ix, _, root := fixtureIndexer(t)
	ix.opts.Calls, ix.enabled[callsLayerName] = true, true
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	first := ix.callCache.idx
	if first == nil {
		t.Fatal("no call index was kept")
	}
	appendTo(t, root, "assets/templates/home.templ", "\ntempl Banner() {\n\t<p>hi</p>\n}\n")
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ix.callCache.idx != first {
		t.Error("a template edit type-checked the module again")
	}
	appendTo(t, root, "controllers/hello.go", "\n// changed\n")
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if ix.callCache.idx == first {
		t.Error("a Go edit reused a stale call index")
	}
}
