package devmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/frameworkdocs"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/web"
)

// scopedHandler is indexedHandler with the options an application's dev server passes.
func scopedHandler(t *testing.T, opt Options) http.Handler {
	t.Helper()
	store, err := graphdb.OpenScratch(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	r := chi.NewRouter()
	r.Get(web.HTMXPath, web.ServeHTMX)
	ix := codegraph.New(store.QuadStore, codegraph.Options{Root: "../..", Router: r, CommandSource: noCommands, Build: codegraph.BuildFacts{AppName: "shop"}})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewHandler(nil, store.QuadStore, ix, opt)
}

// rawCall is call without the helper's default scope.
func rawCall(t *testing.T, h http.Handler, name string, args map[string]any) toolResult {
	t.Helper()
	res, perr := rpc(t, h, "tools/call", map[string]any{"name": name, "arguments": args})
	if perr != nil {
		t.Fatalf("%s %v: protocol error %d: %s", name, args, perr.Code, perr.Message)
	}
	var tr toolResult
	if err := json.Unmarshal(res, &tr); err != nil {
		t.Fatalf("%s: %v: %s", name, err, res)
	}
	return tr
}

func TestGraphToolsDefaultToTheAppsOwnScope(t *testing.T) {
	h := scopedHandler(t, Options{})
	const route = "GET /_govibe/static/htmx.min.js" // the framework's, by its path

	// graph_schema says whose graph it is, and how much it hides
	s := rawCall(t, h, "graph_schema", nil).ok(t, "graph_schema").StructuredContent
	hidden, _ := s["hidden"].(map[string]any)
	app, _ := s["app"].(map[string]any)
	fw, _ := s["framework"].(map[string]any)
	if s["scope"] != "app" || hidden["scope"] != "app" || hidden["nodes"].(float64) < 1 {
		t.Errorf("default scope: scope=%v hidden=%v", s["scope"], hidden)
	}
	if app["name"] != "shop" || app["module"] != "github.com/petars-dev/govibe" || fw["module"] != "github.com/petars-dev/govibe" || fw["version"] != "(this repository)" {
		t.Errorf("app %v framework %v", app, fw)
	}
	if !strings.Contains(strings.Join(strs(s["conventions"]), " "), "Scope app (the default)") {
		t.Errorf("the conventions explain the scope: %v", s["conventions"])
	}
	if f, _ := s["freshness"].(map[string]any); f["quads"].(float64) != 0 {
		t.Errorf("run counters are shown for scope all only: %v", f)
	}

	// a node of the other scope is said to exist there, not "not found"
	res := rawCall(t, h, "graph_context", map[string]any{"route": route})
	if !res.IsError || !strings.Contains(res.text(), "scope app") || !strings.Contains(res.text(), "scope=all") {
		t.Errorf("graph_context in app scope: %v", res.text())
	}
	// with scope all it is there, labelled
	c := rawCall(t, h, "graph_context", map[string]any{"route": route, "scope": "all"}).ok(t, "graph_context all").StructuredContent
	if h, _ := c["handler"].(map[string]any); h["id"] != "func:web.ServeHTMX" || h["origin"] != "app" {
		t.Errorf("the handler is the project's own code here, labelled so: %v", h)
	}
	if got := rawCall(t, h, "graph_schema", map[string]any{"scope": "all"}).ok(t, "graph_schema all").StructuredContent; got["scope"] != "all" {
		t.Errorf("scope all: %v", got["scope"])
	}
	// framework: only what it contributes
	if fwOnly := rawCall(t, h, "graph_search", map[string]any{"text": "htmx", "scope": "framework"}).ok(t, "search framework").StructuredContent; len(list(fwOnly["hits"])) == 0 {
		t.Errorf("the framework's htmx route is in its scope: %v", fwOnly)
	}

	// a scope that does not exist is an error that says what is valid
	if bad := rawCall(t, h, "graph_findings", map[string]any{"scope": "everything"}); !bad.IsError || !strings.Contains(bad.text(), "app, framework or all") {
		t.Errorf("bad scope: %v", bad.text())
	}

	// graph_query is the raw store: it is not scoped and says so
	q := rawCall(t, h, "graph_query", map[string]any{"query": "g.V(g.IRI('route:GET:/_govibe/static/htmx.min.js')).All()"}).ok(t, "graph_query")
	if q.StructuredContent["count"].(float64) != 1 {
		t.Errorf("graph_query sees the framework's route: %v", q.StructuredContent)
	}
}

func TestEveryScopedToolOffersTheScopeParameter(t *testing.T) {
	h := scopedHandler(t, Options{})
	res, perr := rpc(t, h, "tools/list", map[string]any{})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]any `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	for _, tool := range out.Tools {
		_, has := tool.InputSchema.Properties["scope"]
		scoped := strings.HasPrefix(tool.Name, "graph_") && tool.Name != "graph_query" && tool.Name != "graph_reindex"
		if scoped != has {
			t.Errorf("%s: scope parameter = %v, want %v", tool.Name, has, scoped)
		}
		if tool.Name == "graph_query" && !strings.Contains(tool.Description, "not scoped") {
			t.Errorf("graph_query must say it is unscoped: %s", tool.Description)
		}
	}
}

type fakeDocs struct{}

func (fakeDocs) List() []frameworkdocs.Doc {
	return []frameworkdocs.Doc{{Slug: "guide", Title: "GoVibe: Guide"}, {Slug: "configuration", Title: "GoVibe: Configuration"}}
}
func (fakeDocs) Read(slug string) (string, error) { return "# " + slug + "\n", nil }

func readResource(t *testing.T, h http.Handler, uri string) string {
	t.Helper()
	raw, perr := rpc(t, h, "resources/read", map[string]any{"uri": uri})
	if perr != nil {
		t.Fatalf("%s: %s", uri, perr.Message)
	}
	var out struct {
		Contents []struct{ Text string } `json:"contents"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Contents) != 1 {
		t.Fatalf("%s: %v %s", uri, err, raw)
	}
	return out.Contents[0].Text
}

func TestTheServerFrontsAnApplication(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("# Shop rules\n\nKeep it thin.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := &routeinfo.Inspect{
		Schema:    routeinfo.InspectSchema,
		App:       routeinfo.InspectApp{Name: "shop", Module: "example.com/shop", Contributes: routeinfo.InspectContributes{Config: true, Routes: true, Commands: true}},
		Framework: routeinfo.InspectModule{Module: "github.com/petars-dev/govibe", Version: "v0.3.1"},
		Build:     routeinfo.InspectBuild{Tags: "govibe_dev"},
		Routes: []routeinfo.InspectRoute{
			{Method: "GET", Path: "/orders", Handler: routeinfo.InspectRef{Symbol: "example.com/shop/controllers.ListOrders"}, MountedBy: routeinfo.MountedByApp},
			{Method: "GET", Path: "/_govibe/static/htmx.min.js", Handler: routeinfo.InspectRef{Symbol: "x.ServeHTMX"}, MountedBy: routeinfo.MountedByFramework},
		},
		Commands: []routeinfo.InspectCmd{{Name: "orders:export", Short: "Export yesterday's orders", Handler: routeinfo.InspectRef{Symbol: "example.com/shop/commands.Export"}}},
	}
	h := scopedHandler(t, Options{AppName: "shop", Root: root, Inspect: doc, Docs: fakeDocs{}})

	t.Run("the rules are the project's SKILL.md", func(t *testing.T) {
		if got := readResource(t, h, "govibe://skill"); !strings.Contains(got, "# Shop rules") {
			t.Errorf("skill = %q", got)
		}
	})
	t.Run("without a SKILL.md they are rendered from the framework's tables", func(t *testing.T) {
		h := scopedHandler(t, Options{Root: t.TempDir()})
		got := readResource(t, h, "govibe://skill")
		for _, want := range []string{"# Rules of this project", "Only business actions and services may call the storage layer", "business: nothing", "controllers: govibe/web", "wiring: govibe, govibe/config, govibe/web", "Status Table"} {
			if !strings.Contains(got, want) {
				t.Errorf("rendered rules lack %q:\n%s", want, got)
			}
		}
	})
	t.Run("the manifest is what the App declaration contributes", func(t *testing.T) {
		var m struct {
			App         struct{ Name, Module string }
			Framework   struct{ Module, Version string }
			Contributes struct{ Config, Migrations, Routes, Commands bool }
			Routes      []struct{ Method, Path, Handler string }
			Commands    []struct{ Name, Short string }
			Build       struct{ Tags string }
		}
		if err := json.Unmarshal([]byte(readResource(t, h, "govibe://app/manifest")), &m); err != nil {
			t.Fatal(err)
		}
		if m.App.Name != "shop" || m.Framework.Version != "v0.3.1" || !m.Contributes.Config || !m.Contributes.Routes || m.Contributes.Migrations || m.Build.Tags != "govibe_dev" {
			t.Errorf("manifest %+v", m)
		}
		if len(m.Routes) != 1 || m.Routes[0].Path != "/orders" || m.Routes[0].Handler != "example.com/shop/controllers.ListOrders" {
			t.Errorf("only the app's routes are what it contributes: %+v", m.Routes)
		}
		if len(m.Commands) != 1 || m.Commands[0].Name != "orders:export" {
			t.Errorf("commands %+v", m.Commands)
		}
	})
	t.Run("the framework's docs are resources, not graph nodes", func(t *testing.T) {
		raw, perr := rpc(t, h, "resources/list", map[string]any{})
		if perr != nil {
			t.Fatal(perr.Message)
		}
		for _, want := range []string{"govibe://docs/guide", "govibe://docs/configuration", "GoVibe: Guide", "govibe://skill", "govibe://app/manifest"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("resources lack %s: %s", want, raw)
			}
		}
		if got := readResource(t, h, "govibe://docs/guide"); got != "# guide\n" {
			t.Errorf("doc = %q", got)
		}
	})
	t.Run("the server is titled after the app", func(t *testing.T) {
		raw, perr := rpc(t, h, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}})
		if perr != nil || !strings.Contains(string(raw), "shop dev tools (GoVibe)") {
			t.Errorf("initialize: %s %v", raw, perr)
		}
	})
	t.Run("the manifest is absent when the binary has not said", func(t *testing.T) {
		raw, perr := rpc(t, scopedHandler(t, Options{}), "resources/list", map[string]any{})
		if perr != nil || strings.Contains(string(raw), "govibe://app/manifest") || strings.Contains(string(raw), "govibe://docs/") {
			t.Errorf("%s %v", raw, perr)
		}
	})
}
