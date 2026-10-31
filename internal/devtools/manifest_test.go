package devtools

import (
	"context"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/profile"
)

const helloRoot = "../../examples/hello"

func TestTheManifestNamesTheFrameworksSettingsAndTools(t *testing.T) {
	m, err := FrameworkManifest(context.Background(), "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if m.Module != profile.FrameworkModule() || m.Version != "v1.2.3" {
		t.Errorf("module %q version %q", m.Module, m.Version)
	}
	if !strings.Contains(string(m.ConfigYAML), "server:") || !strings.Contains(string(m.ConfigYAML), "${") {
		t.Errorf("the defaults file is not the shipped one:\n%s", m.ConfigYAML)
	}
	for goPath, key := range map[string]string{
		"Server": "server", "Server.Port": "server.port", "Database.MariaDB.DSN": "database.mariadb.dsn", "Dev.Graph.Calls": "dev.graph.calls",
	} {
		if m.ConfigFields[goPath] != key {
			t.Errorf("ConfigFields[%q] = %q, want %q", goPath, m.ConfigFields[goPath], key)
		}
	}
	if _, ok := m.ConfigFields["raw"]; ok {
		t.Error("the unexported resolved tree is not a setting")
	}
	names := map[string]bool{}
	for _, tool := range m.Tools {
		names[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
	}
	for _, want := range []string{"graph_schema", "graph_context", "graph_findings", "graph_query", "db_query", "db_list_tables"} {
		if !names[want] {
			t.Errorf("the manifest does not list the tool %s: %v", want, names)
		}
	}
}

// indexHello indexes the Hello example as an app that depends on the framework, the way
// the dev server does, with the framework's manifest.
func indexHello(t *testing.T) *codegraph.Model {
	t.Helper()
	h, err := graphdb.OpenScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	mf, err := FrameworkManifest(context.Background(), "(devel)")
	if err != nil {
		t.Fatal(err)
	}
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: helloRoot, Manifest: mf})
	rep, err := ix.Run(context.Background())
	if err != nil || rep.Failed() {
		t.Fatalf("index hello: %v %+v", err, rep)
	}
	m, err := ix.Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAnAppIsIndexedAgainstTheFrameworkItDependsOn(t *testing.T) {
	m := indexHello(t)

	t.Run("its imports of the framework are labelled external nodes", func(t *testing.T) {
		web := m.Node("ext:github.com/petars-dev/govibe/web")
		if web == nil || web.Kind != "external" || web.Str("origin") != "framework" || web.Str("module") != profile.FrameworkModule() {
			t.Fatalf("web = %+v", web)
		}
		if web.Str("module_version") != "(devel)" {
			t.Errorf("module_version = %q: hello replaces the framework with a local copy", web.Str("module_version"))
		}
		// the page's Go is generated, so its import of web is read from the .templ file
		if got := m.Node("file:assets/templates/home.templ").Out["imports_external"]; len(got) != 1 || got[0] != web.ID {
			t.Errorf("the page imports web: %v", got)
		}
		root := m.Node("ext:github.com/petars-dev/govibe")
		if root == nil || root.Str("name") != "govibe" {
			t.Fatalf("main imports the framework's root package: %+v", root)
		}
		if got := m.Node("pkg:routes").Out["imports_external"]; len(got) != 1 || got[0] != root.ID {
			t.Errorf("routes imports govibe: %v", got)
		}
		// what is inside the app's own module is its own, even though its path starts with the framework's
		for _, n := range m.Kind("external") {
			if strings.Contains(n.ID, "/examples/hello") {
				t.Errorf("the app's own package %s is classified as the framework's", n.ID)
			}
		}
	})

	t.Run("the framework's settings and tools are its own", func(t *testing.T) {
		port := m.Node("cfg:server.port")
		if port == nil || port.Str("origin") != "framework" || !port.Bool("declared") || port.Str("path") != "" {
			t.Errorf("server.port = %+v", port)
		}
		if tool := m.Node("tool:graph_context"); tool == nil || tool.Str("origin") != "framework" {
			t.Errorf("graph_context = %+v", tool)
		}
		if env := m.Node("env:PORT"); env == nil || env.Str("origin") != "framework" {
			t.Errorf("PORT = %+v", env)
		}
	})

	t.Run("nothing the framework declares is a finding for the app", func(t *testing.T) {
		for _, f := range m.AllFindings() {
			t.Errorf("finding in a fresh app: %s %s: %s", f.Rule, f.About, f.Message)
		}
	})

	t.Run("the app's own scope shows the app's connections and none of the framework's", func(t *testing.T) {
		app := scoped(t, codegraph.ScopeApp)
		for _, kind := range []string{"external", "mcp_tool", "config_key", "env_var"} {
			if n := len(app.Kind(kind)); n != 0 {
				t.Errorf("app scope holds %d %s nodes", n, kind)
			}
		}
		if app.Node("pkg:controllers") == nil || app.Node("pkg:business/actions") == nil {
			t.Error("the app's own packages are missing from its scope")
		}
		if h := app.Hidden(); h.Nodes == 0 || h.ByKind["external"] == 0 {
			t.Errorf("hidden = %+v", h)
		}
		in, err := app.Inspect("pkg:routes", codegraph.LinkOptions{})
		if err != nil || !strings.Contains(in.Uses, "uses framework: ") || !strings.Contains(in.Uses, "govibe") {
			t.Errorf("routes inspector uses %q (%v)", in.Uses, err)
		}
	})
}

// scoped re-reads the hello graph in a scope.
func scoped(t *testing.T, scope codegraph.Scope) *codegraph.Model {
	t.Helper()
	h, err := graphdb.OpenScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	mf, err := FrameworkManifest(context.Background(), "(devel)")
	if err != nil {
		t.Fatal(err)
	}
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: helloRoot, Manifest: mf})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := ix.Model(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// indexHelloWithCalls indexes the example with the calls layer on, the way `--calls` does.
func indexHelloWithCalls(t *testing.T) (*codegraph.Model, *codegraph.Report) {
	t.Helper()
	h, err := graphdb.OpenScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	mf, err := FrameworkManifest(context.Background(), "(devel)")
	if err != nil {
		t.Fatal(err)
	}
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: helloRoot, Manifest: mf, Calls: true})
	rep, err := ix.Run(context.Background())
	if err != nil || rep.Failed() {
		t.Fatalf("index hello with calls: %v %+v", err, rep)
	}
	m, err := ix.Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m, rep
}

func TestCallsIntoTheFrameworkAreBoundedExternalLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the example app")
	}
	m, rep := indexHelloWithCalls(t)

	for _, l := range rep.Layers {
		if l.Name == "calls" && (l.Status != "ok" || !strings.Contains(l.Note, "-tags govibe_dev")) {
			t.Errorf("the calls layer states the tags it was type-checked with: %+v", l)
		}
	}

	// main calls govibe.Main: one leaf for that function, with the module and its version
	main := m.Node("func:..main")
	if main == nil {
		t.Fatal("no main function")
	}
	var leaf *codegraph.Node
	for _, id := range main.Out["calls_external"] {
		if id == "ext:github.com/petars-dev/govibe.Main" {
			leaf = m.Node(id)
		}
	}
	if leaf == nil {
		t.Fatalf("main calls govibe.Main: %v", main.Out["calls_external"])
	}
	if leaf.Str("name") != "govibe.Main" || leaf.Str("origin") != "framework" || leaf.Str("module") != profile.FrameworkModule() || leaf.Str("module_version") != "(devel)" {
		t.Errorf("govibe.Main = %+v", leaf)
	}

	// a library is one leaf per package, not per function, and the standard library is not recorded
	reg := m.Node("func:routes.Register")
	if reg == nil {
		t.Fatal("no routes.Register")
	}
	chi := map[string]bool{}
	for _, id := range reg.Out["calls_external"] {
		chi[id] = true
		if strings.HasPrefix(id, "ext:net/") || strings.HasPrefix(id, "ext:fmt") || strings.HasPrefix(id, "ext:strings") {
			t.Errorf("the standard library is recorded: %s", id)
		}
	}
	if !chi["ext:github.com/go-chi/chi/v5"] {
		t.Errorf("routes.Register calls chi (one leaf for the package): %v", reg.Out["calls_external"])
	}
	for id := range chi {
		if strings.HasPrefix(id, "ext:github.com/go-chi/chi/v5.") {
			t.Errorf("a library function got its own leaf: %s", id)
		}
	}

	// the app's own packages are the app's even though its module path sits under the framework's:
	// its generated templ package (no node of its own) is not an external node, calls layer or not
	for _, n := range m.Kind("external") {
		if strings.Contains(n.ID, "/examples/hello") {
			t.Errorf("the app's own package %s is an external node of the framework", n.ID)
		}
	}
	// an app's own calls stay plain `calls` edges between its own functions
	if got := m.Node("func:controllers.HelloAPI").Out["calls"]; len(got) == 0 {
		t.Errorf("HelloAPI calls the action: %v", got)
	}
	// and nothing in the graph points into the framework's internals except the one leaf
	for _, n := range m.Kind("external") {
		if strings.Contains(n.ID, "/internal/") {
			t.Errorf("a framework internal package has a node of its own: %s", n.ID)
		}
	}
}

func TestTheAppScopeHidesCallsIntoTheFramework(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the example app")
	}
	m, _ := indexHelloWithCalls(t)
	h, err := graphdb.OpenScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	_ = m
	mf, _ := FrameworkManifest(context.Background(), "(devel)")
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: helloRoot, Manifest: mf, Calls: true})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	app, err := ix.Model(context.Background(), codegraph.ScopeApp)
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Node("func:..main").Out["calls_external"]; len(got) != 0 {
		t.Errorf("app scope shows the framework's functions: %v", got)
	}
	in, err := app.Inspect("func:..main", codegraph.LinkOptions{})
	if err != nil || !strings.Contains(in.Uses, "govibe.Main") {
		t.Errorf("the inspector says what main uses of the framework, in text: %q (%v)", in.Uses, err)
	}
}
