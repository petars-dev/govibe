package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/codegraph/internal/graphcheck"
	"github.com/petars-dev/govibe/internal/dbexplorer"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// fixtureRoutes is what the fixture's router would report at runtime, as the Go
// runtime spells it (file and line of each handler declaration or closure). The
// fixture is never run, so the facts are written down here; a test against a
// real chi router lives in routes_chi_test.go.
func fixtureRoutes() []runtimeRoute {
	logger := handlerRef{Symbol: "example.com/fixture/middleware.RequestLogger", File: "middleware/logging.go", Line: 10}
	requestID := handlerRef{
		Symbol: "github.com/go-chi/chi/v5/middleware.RequestID",
		File:   "/home/dev/go/pkg/mod/github.com/go-chi/chi/v5@v5.1.0/middleware/request_id.go",
		Line:   50,
	}
	mw := []handlerRef{requestID, logger}
	return []runtimeRoute{
		{Method: "GET", Path: "/", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/controllers.HomePage", File: "controllers/home.go", Line: 11}},
		{Method: "GET", Path: "/api/hello", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/controllers.HelloAPI", File: "controllers/hello.go", Line: 10}},
		// A handler factory: the closure resolves to the function that contains it.
		{Method: "GET", Path: "/api/orders", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/controllers.ListOrders.func1", File: "controllers/orders.go", Line: 14}},
		{Method: "GET", Path: "/api/orders/{id}", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/controllers.GetOrder.func1", File: "controllers/orders.go", Line: 28}},
		{Method: anyMethod, Path: "/dev/mcp", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/devmcp.NewHandler.func1", File: "devmcp/devmcp.go", Line: 16}},
		// A library handler inlined into the application's own function, as Go names it.
		{Method: anyMethod, Path: "/assets/", MW: mw,
			Handler: handlerRef{Symbol: "example.com/fixture/routes.New.StripPrefix.1", File: "/usr/local/go/src/net/http/server.go", Line: 2338}},
	}
}

// fixtureCommands is what the fixture's binary would report for its commands (`inspect
// --json`): where each Run function was declared, and the schedule each one declares.
func fixtureCommands() []routeinfo.InspectCmd {
	return []routeinfo.InspectCmd{
		{Name: "orders:export", Short: "Export yesterday's orders",
			Handler:  routeinfo.InspectRef{Symbol: "example.com/fixture/commands.ExportOrders.func1", File: "commands/export.go", Line: 15},
			Schedule: &routeinfo.InspectSchedule{Spec: "0 2 * * *", Timezone: "UTC", Overlap: "skip", Timeout: "10m0s"}},
		{Name: "reports:digest", Short: "Send the digest",
			Handler:  routeinfo.InspectRef{Symbol: "example.com/fixture/commands.Digest.func1", File: "commands/export.go", Line: 23},
			Schedule: &routeinfo.InspectSchedule{Spec: "@every 6h", Timezone: "UTC", Overlap: "allow", Every: "6h0m0s"}},
		{Name: "cache:warm", Short: "Warm the cache",
			Handler: routeinfo.InspectRef{Symbol: "example.com/fixture/commands.WarmCache", File: "commands/export.go", Line: 31}},
	}
}

func fixtureOptions() Options {
	return Options{
		RouteSource:   func() ([]runtimeRoute, error) { return sortRoutes(fixtureRoutes()), nil },
		CommandSource: func() ([]routeinfo.InspectCmd, error) { return fixtureCommands(), nil },
	}
}

func fixtureWorld(t *testing.T) *world { return newTestWorld(t, fixtureRoot, fixtureOptions()) }

func str(s string) quad.Value { return quad.String(s) }
func iri(s string) quad.Value { return quad.IRI(s) }

func TestRoutesLayerGolden(t *testing.T) {
	checkGolden(t, "routes", buildLayer(t, fixtureWorld(t), routesLayer{}).quads)
}

func TestCommandsLayerGolden(t *testing.T) {
	checkGolden(t, "commands", buildLayer(t, fixtureWorld(t), commandsLayer{}).quads)
}

func TestTemplatesLayerGolden(t *testing.T) {
	checkGolden(t, "templates", buildLayer(t, fixtureWorld(t), templatesLayer{}).quads)
}

func TestConfigLayerGolden(t *testing.T) {
	checkGolden(t, "config", buildLayer(t, fixtureWorld(t), configLayer{}).quads)
}

func TestMCPLayerGolden(t *testing.T) {
	checkGolden(t, "mcp", buildLayer(t, fixtureWorld(t), mcpLayer{}).quads)
}

func TestDBLayerGolden(t *testing.T) {
	checkGolden(t, "db", buildLayer(t, fixtureWorld(t), dbLayer{}).quads)
}

func TestDocsLayerGolden(t *testing.T) {
	checkGolden(t, "docs", buildLayer(t, fixtureWorld(t), docsLayer{}).quads)
}

func TestRoutesLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), routesLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	for route, handler := range map[string]string{
		"route:GET:/":                "func:controllers.HomePage",
		"route:GET:/api/hello":       "func:controllers.HelloAPI",
		"route:GET:/api/orders":      "func:controllers.ListOrders", // a closure -> its factory
		"route:GET:/api/orders/{id}": "func:controllers.GetOrder",
		"route:ANY:/dev/mcp":         "func:devmcp.NewHandler",
	} {
		if got := f.one(t, route, "handled_by"); got != iri(handler) {
			t.Errorf("%s handled_by %v, want %s", route, got, handler)
		}
		if len(f.out(route, "handler")) != 0 {
			t.Errorf("%s: a resolved route should not carry a handler label", route)
		}
	}

	// A library handler is labelled with where it lives, and not linked.
	if got := f.out("route:ANY:/assets/", "handled_by"); len(got) != 0 {
		t.Errorf("a library handler must not be linked to code: %v", got)
	}
	if got := f.one(t, "route:ANY:/assets/", "handler"); got != str("net/http.StripPrefix") {
		t.Errorf("handler label = %v, want net/http.StripPrefix (not the application function it was inlined into)", got)
	}

	for route, want := range map[string]bool{"route:GET:/api/hello": false, "route:ANY:/dev/mcp": true} {
		if got := f.one(t, route, "dev_only"); got != quad.Value(quad.Bool(want)) {
			t.Errorf("%s dev_only = %v, want %v", route, got, want)
		}
	}
	if got := f.one(t, "route:GET:/api/orders/{id}", "path"); got != str("/api/orders/{id}") {
		t.Errorf("path = %v", got)
	}
	if got := f.one(t, "route:ANY:/dev/mcp", "method"); got != str("ANY") {
		t.Errorf("method = %v", got)
	}

	// Middleware are shared nodes; an application one traces to its function,
	// a library one has no code.
	if n := len(f.out("route:GET:/api/hello", "uses_mw")); n != 2 {
		t.Errorf("GET /api/hello uses %d middleware, want 2", n)
	}
	if got := f.one(t, "mw:middleware.RequestLogger", "implemented_by"); got != iri("func:middleware.RequestLogger") {
		t.Errorf("implemented_by = %v", got)
	}
	const lib = "mw:github.com/go-chi/chi/v5/middleware.RequestID"
	if got := f.out(lib, "implemented_by"); len(got) != 0 {
		t.Errorf("a library middleware has no code node: %v", got)
	}
	if got := f.one(t, lib, "name"); got != str("RequestID") {
		t.Errorf("library middleware name = %v", got)
	}
	var mwNodes int
	for _, id := range f.subjects(KindMiddleware) {
		_ = id
		mwNodes++
	}
	if mwNodes != 2 {
		t.Errorf("%d middleware nodes, want 2 (shared across routes)", mwNodes)
	}
}

func TestTemplatesLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), templatesLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	want := []string{"tmpl:assets/templates.Footer", "tmpl:assets/templates.Home", "tmpl:assets/templates.Layout"}
	if got := f.subjects(KindTemplate); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("templates = %v, want %v", got, want)
	}
	home := "tmpl:assets/templates.Home"
	if got := f.one(t, home, "defined_in"); got != iri("file:assets/templates/home.templ") {
		t.Errorf("defined_in = %v", got)
	}
	if got := f.one(t, home, "line"); got != quad.Value(quad.Int(4)) {
		t.Errorf("Home line = %v, want 4", got)
	}
	if got := f.one(t, "tmpl:assets/templates.Footer", "line"); got != quad.Value(quad.Int(11)) {
		t.Errorf("Footer line = %v, want 11", got)
	}
	if got := f.one(t, "tmpl:assets/templates.Layout", "signature"); got != str("templ Layout(title string)") {
		t.Errorf("signature = %v", got)
	}
	if got := f.one(t, home, "doc"); got != str("Home is the landing page.") {
		t.Errorf("doc = %v", got)
	}
	// Component to component: @Layout("Home") { ... @Footer() }.
	for _, target := range []string{"tmpl:assets/templates.Layout", "tmpl:assets/templates.Footer"} {
		if !f.has(home, "renders", iri(target)) {
			t.Errorf("Home should render %s", target)
		}
	}
	if n := len(f.out("tmpl:assets/templates.Layout", "renders")); n != 0 {
		t.Errorf("Layout renders nothing, got %d edges", n)
	}
	// A Go function that calls the generated component renders it. The package
	// holds only generated templ output, so it has no code node to attach to.
	if got := f.one(t, "func:controllers.HomePage", "renders"); got != iri(home) {
		t.Errorf("HomePage renders %v", got)
	}
	if got := f.one(t, "file:assets/templates/home.templ", "lines"); got != quad.Value(quad.Int(13)) {
		t.Errorf("home.templ lines = %v", got)
	}
	if got := f.out("file:assets/templates/home.templ", "in_package"); len(got) != 0 {
		t.Errorf("a templ-only package has no node to point at: %v", got)
	}
}

func TestConfigLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), configLayer{})
	f := facts{out.quads}

	// Sensitive settings carry no default and no value, ever.
	for _, key := range []string{"database.dsn", "auth.api_key", "queue.broker.password"} {
		id := "cfg:" + key
		if got := f.one(t, id, "sensitive"); got != quad.Value(quad.Bool(true)) {
			t.Errorf("%s sensitive = %v", key, got)
		}
		if got := f.out(id, "default"); len(got) != 0 {
			t.Errorf("%s leaked a default: %v", key, got)
		}
	}
	for _, env := range []string{"env:FIXTURE_DSN", "env:FIXTURE_API_KEY", "env:BROKER_PASSWORD"} {
		if got := f.out(env, "default"); len(got) != 0 {
			t.Errorf("%s leaked a default: %v", env, got)
		}
	}
	for _, secret := range []string{"user:pass", "s3cret", "hunter2"} {
		if strings.Contains(nquads(t, out.quads), secret) {
			t.Errorf("the value %q reached the graph", secret)
		}
	}

	// key -> env var -> default, and key -> struct field.
	if got := f.one(t, "cfg:server.port", "reads_env"); got != iri("env:PORT") {
		t.Errorf("server.port reads_env %v", got)
	}
	if got := f.one(t, "cfg:server.port", "default"); got != str("8080") {
		t.Errorf("server.port default = %v", got)
	}
	if got := f.one(t, "env:PORT", "default"); got != str("8080") {
		t.Errorf("PORT default = %v", got)
	}
	if got := f.one(t, "cfg:server.port", "field"); got != str("Config.Server.Port") {
		t.Errorf("server.port field = %v", got)
	}
	if got := f.one(t, "cfg:database.max_conns", "field"); got != str("Config.Database.MaxConns") {
		t.Errorf("max_conns field = %v", got)
	}
	if got := f.one(t, "cfg:server.port", "path"); got != str("env.yml") {
		t.Errorf("path = %v", got)
	}
	if got := f.one(t, "cfg:server.port", "line"); got != quad.Value(quad.Int(4)) {
		t.Errorf("server.port line = %v, want 4", got)
	}
	// A default that contains a colon survives (only the first colon splits).
	if got := f.one(t, "cfg:database.max_conns", "default"); got != str("10") {
		t.Errorf("max_conns default = %v", got)
	}
	// A static value has no environment variable behind it.
	if got := f.one(t, "cfg:region", "default"); got != str("eu-west-1") {
		t.Errorf("region default = %v", got)
	}
	if got := f.out("cfg:region", "reads_env"); len(got) != 0 {
		t.Errorf("a static value has no env var: %v", got)
	}
	// An empty default is no default.
	if got := f.out("cfg:queue.broker.host", "default"); len(got) != 0 {
		t.Errorf("an empty default should be absent: %v", got)
	}

	// Usage: selector chains on cfg, os.Getenv, and config.Get (a group, and an
	// undeclared key).
	for _, u := range [][2]string{
		{"func:..main", "cfg:server.port"},
		{"func:..main", "cfg:database.dsn"},
		{"pkg:.", "cfg:server.port"},
		{"func:business/actions.Notify", "cfg:queue.broker.host"},
		{"func:business/actions.Notify", "cfg:queue.broker.port"},
		{"func:business/actions.Notify", "cfg:queue.broker.password"},
		{"pkg:business/actions", "cfg:queue.broker.host"},
		{"func:business/actions.Notify", "cfg:feature.beta"},
	} {
		if !f.has(u[0], "uses_config", iri(u[1])) {
			t.Errorf("missing %s --uses_config--> %s", u[0], u[1])
		}
	}
	// Declared but never read.
	for _, key := range []string{"cfg:auth.api_key", "cfg:unused.flag", "cfg:database.max_conns"} {
		for _, q := range out.quads {
			if q.Predicate == quad.Value(PredUsesConfig) && q.Object == iri(key) {
				t.Errorf("%s should have no users, got %v", key, q)
			}
		}
	}

	// Declared vs read-but-undeclared, for both kinds of name.
	if got := f.one(t, "cfg:feature.beta", "declared"); got != quad.Value(quad.Bool(false)) {
		t.Errorf("feature.beta declared = %v", got)
	}
	if got := f.out("cfg:feature.beta", "path"); len(got) != 0 {
		t.Errorf("an undeclared key has no env.yml location: %v", got)
	}
	if got := f.one(t, "env:FIXTURE_DEBUG", "declared"); got != quad.Value(quad.Bool(false)) {
		t.Errorf("FIXTURE_DEBUG declared = %v", got)
	}
	if !f.has("func:..main", "reads_env", iri("env:FIXTURE_DEBUG")) {
		t.Error("main reads FIXTURE_DEBUG")
	}
	if got := f.one(t, "env:PORT", "declared"); got != quad.Value(quad.Bool(true)) {
		t.Errorf("PORT declared = %v", got)
	}
}

func TestMCPLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), mcpLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}
	if got := f.subjects(KindMCPTool); strings.Join(got, " ") != "tool:fixture_cache_flush tool:fixture_echo tool:fixture_orders_count tool:fixture_ping" {
		t.Errorf("tools = %v", got)
	}
	// The tools the app adds to the framework's dev server through App.DevTools: a typed
	// DevToolFunc (its schema from the handler's second parameter) and an untyped DevTool
	// literal (no schema), both registered by the function that holds them and both the app's.
	for _, tool := range []string{"tool:fixture_orders_count", "tool:fixture_cache_flush"} {
		if !f.has("func:routes.registerDevTools", "registers", iri(tool)) {
			t.Errorf("registerDevTools should register %s", tool)
		}
		if got := f.one(t, tool, "path"); got != str("routes/devtools_dev.go") {
			t.Errorf("%s path = %v", tool, got)
		}
		if got := f.one(t, tool, "origin"); got != str(OriginApp) {
			t.Errorf("%s origin = %v", tool, got)
		}
	}
	if got := f.one(t, "tool:fixture_orders_count", "input_schema"); got != str(`{"properties":{"status":{"description":"only orders in this status","type":"string"}},"type":"object"}`) {
		t.Errorf("fixture_orders_count schema = %v", got)
	}
	if got := f.out("tool:fixture_cache_flush", "input_schema"); len(got) != 0 {
		t.Errorf("an untyped DevTool has a schema: %v", got)
	}
	for _, tool := range []string{"tool:fixture_echo", "tool:fixture_ping"} {
		if !f.has("func:devmcp.registerTools", "registers", iri(tool)) {
			t.Errorf("registerTools should register %s", tool)
		}
		if got := f.one(t, tool, "path"); got != str("devmcp/devmcp.go") {
			t.Errorf("%s path = %v", tool, got)
		}
	}
	// A concatenated description is evaluated.
	if got := f.one(t, "tool:fixture_echo", "description"); got != str("Echo a message. Read-only.") {
		t.Errorf("description = %v", got)
	}
	// The input schema comes from the handler's input struct: json names,
	// omitempty = optional, the jsonschema tag = description.
	const schema = `{"properties":{"message":{"description":"text to echo back","type":"string"},` +
		`"repeat":{"description":"how many times to repeat it","type":"integer"}},"required":["message"],"type":"object"}`
	if got := f.one(t, "tool:fixture_echo", "input_schema"); got != str(schema) {
		t.Errorf("fixture_echo input_schema = %v", got)
	}
	if got := f.one(t, "tool:fixture_ping", "input_schema"); got != str(`{"type":"object"}`) {
		t.Errorf("fixture_ping input_schema = %v", got)
	}
}

func TestDocsLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), docsLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	if got := f.subjects(KindDoc); strings.Join(got, " ") != "doc:README.md doc:docs/public/ARCHITECTURE.md doc:docs/public/GUIDE.md" {
		t.Errorf("docs = %v", got)
	}
	if got := f.subjects(KindSpec); strings.Join(got, " ") != "spec:hello" {
		t.Errorf("specs = %v", got)
	}
	if got := f.one(t, "doc:docs/public/ARCHITECTURE.md", "title"); got != str("Fixture Architecture") {
		t.Errorf("title = %v", got)
	}
	if got := f.one(t, "spec:hello", "title"); got != str("SPEC_HELLO") {
		t.Errorf("spec title = %v", got)
	}

	arch := "doc:docs/public/ARCHITECTURE.md"
	mustMention := []string{
		"pkg:controllers",                            // controllers/
		"pkg:storage/orderstore",                     // storage/orderstore
		"route:GET:/api/hello",                       // GET /api/hello
		"func:controllers.HelloAPI",                  // controllers.HelloAPI   (pkg.Name)
		"func:business/services/orders.Service.List", // orders.Service.List (pkg.Type.Method)
		"func:storage/orderstore.Store.List",         // Store.List             (Type.Method)
		"route:GET:/api/orders",                      //
		"route:ANY:/dev/mcp",                         // GET /dev/mcp: an ANY route serves GET
		"command:orders:export",                      // `orders:export`: a command by the name it is run with
		"command:reports:digest",                     // `./fixture reports:digest`: as it is written when run by hand
		"tool:fixture_echo", "tool:fixture_ping",     //
		"func:controllers.GetOrder",    //
		"typ:storage/orderstore.Store", // orderstore.Store
		"spec:hello",                   // a /dev/docs/specs/ link
		"cfg:server.port", "env:PORT", "env:FIXTURE_DSN",
		"cfg:queue.broker.host", "cfg:queue.broker.port", "cfg:queue.broker.password", // queue.broker.*
		"env:FIXTURE_DEBUG",
		"file:main.go",
		"tmpl:assets/templates.Home", // templates.Home
		"file:assets/templates/home.templ",
		"doc:docs/public/GUIDE.md", // [guide](GUIDE.md)
		"doc:README.md",            // [README](../../README.md)
		// The schema: tables by name, a column as table.column, a migration by path.
		"table:orders", "table:users", "table:products", "table:order_items", "col:orders.user_id", "mig:0001_init",
	}
	for _, target := range mustMention {
		if !f.has(arch, "mentions", iri(target)) {
			t.Errorf("ARCHITECTURE.md should mention %s", target)
		}
	}
	// Only what resolves exactly is a mention: a path that no longer exists is not.
	if n := len(f.out(arch, "mentions")); n != len(mustMention) {
		var extra []string
		for _, m := range f.out(arch, "mentions") {
			extra = append(extra, m.String())
		}
		t.Errorf("ARCHITECTURE.md has %d mentions, want exactly %d:\n%s", n, len(mustMention), strings.Join(extra, "\n"))
	}

	guide := "doc:docs/public/GUIDE.md"
	for _, target := range []string{"doc:docs/public/ARCHITECTURE.md", "file:main.go", "file:routes/routes.go", "route:GET:/api/hello"} {
		if !f.has(guide, "mentions", iri(target)) {
			t.Errorf("GUIDE.md should mention %s", target)
		}
	}
	if f.has(guide, "mentions", iri("cfg:region")) {
		t.Error("a bare word is never resolved as a config key")
	}

	// Spec status rows.
	if got := f.one(t, "spec:hello", "status"); got != str("in_progress") {
		t.Errorf("spec status = %v", got)
	}
	if n := len(f.out("spec:hello", "has_item")); n != 7 {
		t.Errorf("spec has %d items, want 7", n)
	}
	type row struct{ id, status, why, note string }
	for _, r := range []row{
		{"item:hello/get-api-hello-endpoint", "implemented", "", "Served by controllers.HelloAPI"},
		{"item:hello/landing-page", "implemented", "", "Rendered by templates.Home"},
		{"item:hello/mcp-tool-fixture-ping", "implemented", "", "Registered by devmcp.NewHandler"},
		{"item:hello/greeting-history", "skipped", "Out of scope: no fixture_forget tool either", ""},
		{"item:hello/localised-greetings", "planned", "", "Planned: actions.Hello returns one language"},
		{"item:hello/settings-fixture-db-max-conns", "partial", "", "Declared, not yet read"},
	} {
		if got := f.one(t, r.id, "status"); got != str(r.status) {
			t.Errorf("%s status = %v, want %s", r.id, got, r.status)
		}
		if got := f.out(r.id, "why_skipped"); (r.why == "") != (len(got) == 0) || (r.why != "" && got[0] != str(r.why)) {
			t.Errorf("%s why_skipped = %v, want %q", r.id, got, r.why)
		}
		if got := f.out(r.id, "note"); (r.note == "") != (len(got) == 0) || (r.note != "" && got[0] != str(r.note)) {
			t.Errorf("%s note = %v, want %q", r.id, got, r.note)
		}
		if !f.has("spec:hello", "has_item", iri(r.id)) {
			t.Errorf("spec should have item %s", r.id)
		}
		if got := f.one(t, r.id, "path"); got != str("docs/specs/SPEC_HELLO.md") {
			t.Errorf("%s path = %v", r.id, got)
		}
	}
	if got := f.one(t, "item:hello/localised-greetings", "line"); got != quad.Value(quad.Int(14)) {
		t.Errorf("row line = %v, want 14", got)
	}
	// A row covers what its own backticks name.
	for _, c := range [][2]string{
		{"item:hello/get-api-hello-endpoint", "route:GET:/api/hello"},
		{"item:hello/get-api-hello-endpoint", "func:controllers.HelloAPI"},
		{"item:hello/landing-page", "tmpl:assets/templates.Home"},
		{"item:hello/mcp-tool-fixture-ping", "tool:fixture_ping"},
		{"item:hello/mcp-tool-fixture-ping", "func:devmcp.NewHandler"},
		{"item:hello/localised-greetings", "func:business/actions.Hello"},
		{"item:hello/settings-fixture-db-max-conns", "env:FIXTURE_DB_MAX_CONNS"},
	} {
		if !f.has(c[0], "covers", iri(c[1])) {
			t.Errorf("%s should cover %s", c[0], c[1])
		}
	}
	if n := len(f.out("item:hello/greeting-history", "covers")); n != 0 {
		t.Errorf("`fixture_forget` is not a tool, so the row covers nothing: %d", n)
	}
}

// With no router wired and no command table (an offline run) route and command mentions
// cannot be resolved; the docs layer says so, for each, instead of silently dropping them.
func TestDocsLayerWithoutRouter(t *testing.T) {
	out := buildLayer(t, newTestWorld(t, fixtureRoot, Options{}), docsLayer{})
	if len(out.warnings) != 2 || !strings.Contains(out.warnings[0], "route") || !strings.Contains(out.warnings[1], "command") {
		t.Errorf("warnings = %v", out.warnings)
	}
	f := facts{out.quads}
	if f.has("doc:docs/public/ARCHITECTURE.md", "mentions", iri("route:GET:/api/hello")) {
		t.Error("no router, so no route to mention")
	}
	if f.has("doc:docs/public/ARCHITECTURE.md", "mentions", iri("command:orders:export")) {
		t.Error("no command table, so no command to mention")
	}
	// With the command table and no router, only the routes are missing.
	only := buildLayer(t, newTestWorld(t, fixtureRoot, Options{CommandSource: fixtureOptions().CommandSource}), docsLayer{})
	if len(only.warnings) != 1 || !strings.Contains(only.warnings[0], "route") {
		t.Errorf("with a command table only the routes are missing: %v", only.warnings)
	}
	if !f.has("doc:docs/public/ARCHITECTURE.md", "mentions", iri("func:controllers.HelloAPI")) {
		t.Error("the rest of the mentions still resolve")
	}
}

func TestSpecStatusAndItemStatus(t *testing.T) {
	for _, c := range []struct{ impl, skip, want string }{
		{"Yes", "", "implemented"}, {"yes (mostly)", "", "implemented"}, {"✅", "", "implemented"},
		{"No", "", "planned"}, {"", "", "planned"}, {"No", "Skipped", "skipped"}, {"No", "Yes", "skipped"},
		{"Partial", "", "partial"}, {"Yes", "Skipped", "implemented"}, {"No", "n/a", "planned"},
	} {
		if got := itemStatus(c.impl, c.skip); got != c.want {
			t.Errorf("itemStatus(%q, %q) = %s, want %s", c.impl, c.skip, got, c.want)
		}
	}
	it := func(s ...string) []*itemInfo {
		var out []*itemInfo
		for _, st := range s {
			out = append(out, &itemInfo{status: st})
		}
		return out
	}
	for want, items := range map[string][]*itemInfo{
		"":            nil,
		"complete":    it("implemented", "skipped"),
		"planned":     it("planned", "skipped"),
		"in_progress": it("implemented", "planned"),
	} {
		if got := specStatus(items); got != want {
			t.Errorf("specStatus = %q, want %q", got, want)
		}
	}
}

func TestSymbolParsing(t *testing.T) {
	for in, want := range map[string][3]string{
		"github.com/petars-dev/govibe/controllers.Home":                {"github.com/petars-dev/govibe/controllers", "", "Home"},
		"github.com/petars-dev/govibe/controllers.(*Server).Handle-fm": {"github.com/petars-dev/govibe/controllers", "Server", "Handle"},
		"github.com/petars-dev/govibe/controllers.Server.Handle":       {"github.com/petars-dev/govibe/controllers", "Server", "Handle"},
		"example.com/m.Map[...]":                                       {"example.com/m", "", "Map"},
		"github.com/a/b.(*T[...]).Method":                              {"github.com/a/b", "T", "Method"},
	} {
		pkg, recv, name, ok := parseRuntimeSymbol(in)
		if !ok || [3]string{pkg, recv, name} != want {
			t.Errorf("parseRuntimeSymbol(%q) = %q %q %q %v, want %v", in, pkg, recv, name, ok, want)
		}
	}
	if _, _, _, ok := parseRuntimeSymbol("controllers.Home.func1"); ok {
		t.Error("a closure is not a declared function")
	}
	for in, want := range map[string]string{
		"pkg/a.B.func1":               "pkg/a.B",
		"pkg/a.B.func1.2":             "pkg/a.B",
		"pkg/a.(*T).M-fm":             "pkg/a.(*T).M",
		"pkg/a.Caller.func2.Callee.1": "pkg/a.Caller.func2.Callee",
		"pkg/a.B":                     "pkg/a.B",
	} {
		if got := cleanSymbol(in); got != want {
			t.Errorf("cleanSymbol(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"/usr/local/go/src/net/http/server.go":                               "net/http",
		"/home/u/go/pkg/mod/github.com/go-chi/chi/v5@v5.1.0/middleware/x.go": "github.com/go-chi/chi/v5/middleware",
		"/home/u/go/pkg/mod/github.com/a/b@v1.0.0/x.go":                      "github.com/a/b",
		"net/http/server.go":                                                 "net/http",
		"/somewhere/else/x.go":                                               "",
	} {
		if got := externalPackage(in); got != want {
			t.Errorf("externalPackage(%q) = %q, want %q", in, got, want)
		}
	}
}

// The schema comes from the migrations alone: no database is configured. The
// fixture's two migrations are the sample the spec asks for: users, orders and
// order_items (and products, the other side of the junction).
func TestDBLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), dbLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}
	b := func(v bool) quad.Value { return quad.Bool(v) }

	want := []string{"table:order_items", "table:orders", "table:products", "table:users"}
	if got := f.subjects(KindTable); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tables = %v, want %v", got, want)
	}
	for _, id := range want {
		if got := f.one(t, id, "source"); got != str("migration") {
			t.Errorf("%s source = %v: with no database every element comes from the migrations", id, got)
		}
	}

	t.Run("migrations", func(t *testing.T) {
		if got := f.subjects(KindMigration); strings.Join(got, " ") != "mig:0001_init" {
			t.Errorf("migrations = %v", got)
		}
		if got := f.one(t, "mig:0001_init", "version"); got != str("0001") {
			t.Errorf("version = %v", got)
		}
		if got := f.one(t, "mig:0001_init", "path"); got != str("storage/migrations/sql/0001_init.sql") {
			t.Errorf("path = %v", got)
		}
		if got := f.out("mig:0001_init", "applied"); len(got) != 0 {
			t.Errorf("without a database nothing says whether a migration is applied: %v", got)
		}
	})

	t.Run("history", func(t *testing.T) {
		for subj, mig := range map[string]string{
			"table:users": "mig:0001_init", "table:orders": "mig:0001_init",
			"col:users.email":           "mig:0001_init",
			"idx:orders.ix_orders_user": "mig:0001_init",
		} {
			if got := f.one(t, subj, "introduced_in"); got != iri(mig) {
				t.Errorf("%s introduced_in %v, want %s", subj, got, mig)
			}
		}
	})

	t.Run("columns", func(t *testing.T) {
		email := "col:users.email"
		if got := f.one(t, email, "data_type"); got != str("varchar(255)") {
			t.Errorf("email type = %v", got)
		}
		if got := f.one(t, email, "comment"); got != str("login name") {
			t.Errorf("email comment = %v", got)
		}
		if got := f.one(t, email, "nullable"); got != b(false) {
			t.Errorf("email nullable = %v", got)
		}
		if got := f.one(t, email, "key"); got != str("UNI") {
			t.Errorf("email key = %v", got)
		}
		if got := f.one(t, "col:users.id", "key"); got != str("PRI") {
			t.Errorf("id key = %v", got)
		}
		if got := f.one(t, "col:users.id", "extra"); got != str("auto_increment") {
			t.Errorf("id extra = %v", got)
		}
		if got := f.one(t, "col:orders.user_id", "key"); got != str("MUL") {
			t.Errorf("user_id key = %v", got)
		}
		if got := f.one(t, "col:users.created_at", "default"); got != str("current_timestamp()") {
			t.Errorf("created_at default = %v", got)
		}
		if got := f.one(t, "col:orders.status", "default"); got != str("'new'") {
			t.Errorf("status default = %v", got)
		}
		if got := f.one(t, "col:users.created_at", "ordinal"); got != quad.Value(quad.Int(4)) {
			t.Errorf("ordinal = %v", got)
		}
		if got := f.one(t, "table:users", "comment"); got != str("Accounts") {
			t.Errorf("table comment = %v", got)
		}
		if got := f.one(t, "table:users", "engine"); got != str("InnoDB") {
			t.Errorf("engine = %v", got)
		}
		// A sensitive column's default is never recorded.
		if got := f.out("col:users.password_hash", "default"); len(got) != 0 {
			t.Errorf("a sensitive column leaked its default: %v", got)
		}
		if !f.has("table:orders", "has_column", iri("col:orders.total")) {
			t.Error("table has_column column")
		}
	})

	t.Run("indexes", func(t *testing.T) {
		for _, c := range []struct {
			id, cols                  string
			unique, primary, implicit bool
		}{
			{"idx:users.PRIMARY", "id", true, true, false},
			{"idx:users.ux_users_email", "email", true, false, false},
			{"idx:products.sku", "sku", true, false, false}, // an inline UNIQUE is named after its column
			{"idx:order_items.PRIMARY", "order_id,product_id", true, true, false},
			{"idx:order_items.product_id", "product_id", false, false, true}, // the index the server adds for the unnamed FK
			{"idx:orders.ix_orders_user", "user_id", false, false, false},
		} {
			if got := f.one(t, c.id, "columns"); got != str(c.cols) {
				t.Errorf("%s columns = %v, want %s", c.id, got, c.cols)
			}
			if got := f.one(t, c.id, "unique"); got != b(c.unique) {
				t.Errorf("%s unique = %v", c.id, got)
			}
			if got := f.one(t, c.id, "primary"); got != b(c.primary) {
				t.Errorf("%s primary = %v", c.id, got)
			}
			if got := f.out(c.id, "implicit"); (len(got) == 1) != c.implicit {
				t.Errorf("%s implicit = %v, want %v", c.id, got, c.implicit)
			}
		}
		// Ordered columns, and the membership edges.
		for _, col := range []string{"col:order_items.order_id", "col:order_items.product_id"} {
			if !f.has("idx:order_items.PRIMARY", "has_column", iri(col)) {
				t.Errorf("PRIMARY should have column %s", col)
			}
		}
		if !f.has("table:orders", "has_index", iri("idx:orders.ix_orders_user")) {
			t.Error("table has_index index")
		}
		if f.exists("idx:order_items.fk_items_order") {
			t.Error("the primary key already covers order_id: the server adds no index for it")
		}
	})

	t.Run("foreign keys", func(t *testing.T) {
		for _, e := range [][2]string{
			{"col:order_items.order_id", "col:orders.id"},
			{"col:order_items.product_id", "col:products.id"},
			{"table:order_items", "table:orders"},
			{"table:order_items", "table:products"},
		} {
			if !f.has(e[0], "fk_to", iri(e[1])) {
				t.Errorf("missing %s --fk_to--> %s", e[0], e[1])
			}
		}
		// The rules and the constraint name travel with the column.
		oc := "col:order_items.order_id"
		for p, want := range map[string]string{"fk_name": "fk_items_order", "on_delete": "CASCADE", "on_update": "RESTRICT"} {
			if got := f.one(t, oc, p); got != str(want) {
				t.Errorf("%s %s = %v, want %s", oc, p, got, want)
			}
		}
		if got := f.out("col:order_items.product_id", "fk_name"); len(got) != 0 {
			t.Errorf("an unnamed constraint has no name to report: %v", got)
		}
		// Each is only half of the primary key, so neither is unique alone.
		for _, col := range []string{"col:order_items.order_id", "col:order_items.product_id"} {
			if got := f.one(t, col, "cardinality"); got != str("one_to_many") {
				t.Errorf("%s cardinality = %v", col, got)
			}
		}
	})

	t.Run("inferred relations", func(t *testing.T) {
		// orders.user_id has no foreign key but looks like one.
		if !f.has("col:orders.user_id", "implicit_ref", iri("col:users.id")) || !f.has("table:orders", "implicit_ref", iri("table:users")) {
			t.Error("orders.user_id should be an implicit reference to users.id")
		}
		// Declared keys are not implicit.
		for _, q := range out.quads {
			if q.Predicate != quad.Value(PredImplicitRef) {
				continue
			}
			if s := q.Subject.(quad.IRI); s == "col:order_items.order_id" || s == "col:order_items.product_id" {
				t.Errorf("%s has a foreign key, so it is not an implicit reference", s)
			}
		}
		// order_items is a junction between orders and products, both ways.
		for _, e := range [][2]string{{"table:orders", "table:products"}, {"table:products", "table:orders"}} {
			if !f.has(e[0], "many_to_many", iri(e[1])) {
				t.Errorf("missing %s --many_to_many--> %s", e[0], e[1])
			}
		}
		// Every inferred edge has an annotation that says how, and how sure.
		ann := func(from, pred, to string) string { return string(annID(quad.IRI(from), quad.IRI(pred), quad.IRI(to))) }
		a := ann("col:orders.user_id", "implicit_ref", "col:users.id")
		if got := f.one(t, a, "subject"); got != iri("col:orders.user_id") {
			t.Errorf("annotation subject = %v", got)
		}
		if got := f.one(t, a, "object"); got != iri("col:users.id") {
			t.Errorf("annotation object = %v", got)
		}
		if got := f.one(t, a, "predicate"); got != str("implicit_ref") {
			t.Errorf("annotation predicate = %v", got)
		}
		if got := f.one(t, a, "method"); got != str("name_match") {
			t.Errorf("annotation method = %v", got)
		}
		if got := f.one(t, a, "confidence"); got != quad.Value(quad.Int(95)) {
			t.Errorf("confidence = %v, want 95 (same type, id, and indexed)", got)
		}
		j := ann("table:orders", "many_to_many", "table:products")
		if got := f.one(t, j, "via"); got != iri("table:order_items") {
			t.Errorf("junction via = %v", got)
		}
		if got := f.one(t, j, "method"); got != str("junction_pk") {
			t.Errorf("junction method = %v", got)
		}
		if got := f.one(t, j, "confidence"); got != quad.Value(quad.Int(90)) {
			t.Errorf("junction confidence = %v", got)
		}
		if got := len(f.subjects(KindAnnotation)); got != 4 {
			t.Errorf("%d annotations, want 4 (column and table implicit_ref, and both many_to_many)", got)
		}
	})

	t.Run("code to tables", func(t *testing.T) {
		for fn, p := range map[string]string{
			"func:storage/orderstore.Store.List":   "reads_table",
			"func:storage/orderstore.Store.Get":    "reads_table",
			"func:storage/orderstore.Store.Create": "writes_table",
		} {
			if got := f.out(fn, p); len(got) != 1 || got[0] != iri("table:orders") {
				t.Errorf("%s %s = %v, want table:orders", fn, p, got)
			}
		}
		// Reads and writes stay apart.
		if got := f.out("func:storage/orderstore.Store.Create", "reads_table"); len(got) != 0 {
			t.Errorf("Create reads %v", got)
		}
		if got := f.out("func:storage/orderstore.Store.List", "writes_table"); len(got) != 0 {
			t.Errorf("List writes %v", got)
		}
		// The struct whose db tags are the orders columns is its row.
		if got := f.one(t, "typ:storage/orderstore.Order", "maps_to"); got != iri("table:orders") {
			t.Errorf("Order maps to %v", got)
		}
		if got := f.out("typ:storage/orderstore.Store", "maps_to"); len(got) != 0 {
			t.Errorf("a struct with no db tags: %v", got)
		}
	})
}

// The whole fixture graph, every layer at once, with a live database merged in:
// the same invariants as for this repository, on a graph that has tables.
func TestFixtureGraphInvariants(t *testing.T) {
	h, _ := openStore(t)
	snap := staticSnapshot(t)
	snap.Tables = append(snap.Tables, dbexplorer.SnapshotTable{ // a table only the database has
		Name: "audit_log", Columns: []dbexplorer.SnapshotColumn{{Name: "id", Type: "int(11)", Key: "PRI"}},
		Indexes: []dbexplorer.Index{{Name: "PRIMARY", Unique: true, Columns: []string{"id"}}},
	})
	tableOfSnap(snap, "orders").Columns[2].Type = "decimal(12,2)" // and a column whose type drifted
	opts := fixtureOptions()
	opts.Root = fixtureRoot
	opts.Schema = func(context.Context) (*dbexplorer.Snapshot, error) { return snap, nil }
	rep, err := New(h.QuadStore, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		if l.Status == statusError || l.Status == statusSkipped {
			t.Errorf("layer %s: %+v", l.Name, l)
		}
	}
	res := graphcheck.Check(t, h, graphcheck.Options{
		Layers: []string{"code", "routes", "commands", "templates", "config", "mcp", "db", "docs", "externals", "findings"},
		RequireEdges: []string{"handled_by", "uses_mw", "renders", "uses_config", "reads_env", "registers", "has_item", "mentions", "covers",
			"has_column", "has_index", "fk_to", "implicit_ref", "many_to_many", "introduced_in", "reads_table", "writes_table", "maps_to",
			"subject", "object", "via"},
	})
	t.Logf("%d quads in total", len(res.Quads))
	wantIDs(t, "the drift is recorded where it is",
		gizmoIDs(t, h, `g.V().Has(g.IRI('drift'), 'data_type: migration bigint, live decimal(12,2)').All()`), "<col:orders.total>")
	wantIDs(t, "route -> handler -> storage function reading a table is two hops short of the calls layer; the pieces are there",
		gizmoIDs(t, h, `g.V(g.IRI('func:storage/orderstore.Store.List')).Out(g.IRI('reads_table')).All()`), "<table:orders>")
	if again, err := New(h.QuadStore, opts).Run(context.Background()); err != nil || again.Added != 0 || again.Removed != 0 {
		t.Errorf("second run: %+v %v", again, err)
	}
}
