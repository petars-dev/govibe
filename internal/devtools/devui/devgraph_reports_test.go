package devui

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/web"
)

// reportsRouter indexes root into a fresh store and mounts the report handler
// the way routes.go does.
func reportsRouter(t *testing.T, root string, opts codegraph.Options, dbUI bool) (http.Handler, *codegraph.Indexer) {
	t.Helper()
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open graph store: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	opts.Root = root
	ix := codegraph.New(h.QuadStore, opts)
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatalf("index %s: %v", root, err)
	}
	r := chi.NewRouter()
	r.Get("/dev/graph/reports", DevGraphReports(ix, dbUI))
	r.Get("/dev/graph/reports/{name}", DevGraphReports(ix, dbUI))
	return r, ix
}

// mainOf is the part of a page after its head, for readable failures.
func mainOf(body string) string {
	if i := strings.Index(body, "<main"); i >= 0 {
		return body[i:]
	}
	return body
}

// get serves a request and returns the status and body.
func get(t *testing.T, h http.Handler, path string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b), rec.Header()
}

// thisRepoRouter is a router with this repository's own handlers, so the routes
// layer has something real to resolve.
func thisRepoRouter() chi.Router {
	r := chi.NewRouter()
	r.Get(web.HTMXPath, web.ServeHTMX)
	r.Get("/dev/docs/app", DevDocsAppIndex)
	return r
}

// thisRepo is the framework's own root, seen from this package.
const thisRepo = "../../.."

// every href="#x" of a page lands on an id="x" of the same page.
func checkAnchors(t *testing.T, page, body string) {
	t.Helper()
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		ids[m[1]] = true
	}
	links := regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(body, -1)
	for _, m := range links {
		if !ids[m[1]] {
			t.Errorf("%s: a link to #%s lands nowhere", page, m[1])
		}
	}
}

func TestDevGraphReportsOnThisRepo(t *testing.T) {
	h, ix := reportsRouter(t, thisRepo, codegraph.Options{Router: thisRepoRouter()}, true)
	root := ix.RootDir()
	if root == "" {
		t.Fatal("no root")
	}

	for _, name := range append([]string{""}, codegraph.ReportNames...) {
		path := "/dev/graph/reports"
		if name != "" {
			path += "/" + name
		}
		code, body, hdr := get(t, h, path)
		if code != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") || !strings.Contains(body, "<title>") {
			t.Fatalf("%s: %d %s %.200s", path, code, hdr.Get("Content-Type"), body)
		}
		for _, rn := range codegraph.ReportNames { // the navigation lists every report
			if !strings.Contains(body, `href="/dev/graph/reports/`+rn+`"`) {
				t.Errorf("%s: no link to the %s report", path, rn)
			}
		}
		if !strings.Contains(body, `href="/dev/graph"`) {
			t.Errorf("%s: no link back to the graph", path)
		}
		checkAnchors(t, path, body)
	}

	// routes: the route's row is the anchor Links() points at; its handler opens in the editor
	_, body, _ := get(t, h, "/dev/graph/reports/routes")
	for _, want := range []string{
		`id="` + codegraph.Anchor("route:GET:/_govibe/static/htmx.min.js") + `"`, "web.ServeHTMX",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("routes report lacks %q", want)
		}
	}
	editor := regexp.MustCompile(`href="vscode://file` + regexp.QuoteMeta(root) + `/web/web\.go:\d+"`)
	if !editor.MatchString(body) {
		t.Errorf("the handler is not linked into the editor (the vscode:// scheme must survive escaping):\n%.1500s", mainOf(body))
	}
	if strings.Contains(body, "#ZgotmplZ") {
		t.Error("html/template rejected a URL")
	}
	if !strings.Contains(body, "/_govibe/static/htmx.min.js") {
		t.Error("the htmx route is listed")
	}

	// config: no secret, and a sensitive key says so
	_, body, _ = get(t, h, "/dev/graph/reports/config")
	for _, want := range []string{"server.port", "secret – not shown", `id="` + codegraph.Anchor("cfg:server.port") + `"`, "MARIADB_DSN"} {
		if !strings.Contains(body, want) {
			t.Errorf("config report lacks %q", want)
		}
	}
	if strings.Contains(body, "@tcp(") {
		t.Error("the config report shows a DSN")
	}

	// schema: the framework has no migrations of its own, so no tables
	_, body, _ = get(t, h, "/dev/graph/reports/schema")
	if !strings.Contains(body, "No tables") {
		t.Errorf("schema report:\n%.800s", mainOf(body))
	}

	// layers: the framework itself has no layering violation (a fixture with one is
	// checked on the fixture, below)
	_, body, _ = get(t, h, "/dev/graph/reports/layers")
	if strings.Contains(body, "forbidden broken") {
		t.Errorf("a layering violation in this repository:\n%.1500s", mainOf(body))
	}

	// the index describes each report
	_, body, _ = get(t, h, "/dev/graph/reports")
	for _, r := range codegraph.Reports() {
		if !strings.Contains(body, r.Title) || !strings.Contains(body, r.What[:20]) {
			t.Errorf("index lacks %s", r.Name)
		}
	}
}

func TestDevGraphReportsOnTheFixtureEscapeAndNeverLeakSecrets(t *testing.T) {
	root := copyTree(t, codegraphFixture)
	// A default that would break out of the page if it were not escaped.
	env := filepath.Join(root, "config/env.yml")
	b, _ := os.ReadFile(env)
	if err := os.WriteFile(env, append(b, []byte("\nweb:\n  banner: ${FIXTURE_BANNER:<b>bold</b><script>alert(1)</script>}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _ := reportsRouter(t, root, codegraph.Options{}, true)

	_, cfg, _ := get(t, h, "/dev/graph/reports/config")
	if !strings.Contains(cfg, "&lt;b&gt;bold&lt;/b&gt;") || strings.Contains(cfg, "<b>bold</b>") || strings.Contains(cfg, "<script>alert") {
		t.Errorf("a default is not escaped:\n%s", cfg)
	}
	for _, name := range codegraph.ReportNames {
		_, body, _ := get(t, h, "/dev/graph/reports/"+name)
		for _, secret := range []string{"user:pass@tcp(db)/fixture", "s3cret", "hunter2"} {
			if strings.Contains(body, secret) {
				t.Errorf("the %s report leaks %q", name, secret)
			}
		}
		checkAnchors(t, name, body)
	}
	if !strings.Contains(cfg, "not declared") || !strings.Contains(cfg, "FIXTURE_DEBUG") || !strings.Contains(cfg, "feature.beta") {
		t.Errorf("undeclared settings are flagged:\n%s", cfg)
	}

	// no router was given, so the route table says why it is empty
	_, routes, _ := get(t, h, "/dev/graph/reports/routes")
	if !strings.Contains(routes, "routes layer was skipped") || !strings.Contains(routes, "No routes in the graph") {
		t.Errorf("routes without a router:\n%.1200s", mainOf(routes))
	}

	// schema: anchors for each table, the inferred reference with its confidence, the explorer link
	_, schema, _ := get(t, h, "/dev/graph/reports/schema")
	for _, want := range []string{
		`id="` + codegraph.Anchor("table:orders") + `"`, `id="` + codegraph.Anchor("table:users") + `"`, `id="` + codegraph.Anchor("mig:0001_init") + `"`,
		"inferred 95%", "order_items", "ON DELETE", "/dev/db#table=orders", "on delete CASCADE",
	} {
		if !strings.Contains(schema, want) && want != "ON DELETE" {
			t.Errorf("schema report lacks %q", want)
		}
	}
	if strings.Contains(schema, "'none'") {
		t.Error("the sensitive column's default is shown")
	}

	// layers: the fixture's deliberate violation
	_, layers, _ := get(t, h, "/dev/graph/reports/layers")
	if !strings.Contains(layers, "storage/orderstore") || !strings.Contains(layers, `forbidden broken`) || !strings.Contains(layers, "controllers/orders.go") {
		t.Errorf("layers report:\n%.1500s", mainOf(layers))
	}
}

func TestDevGraphReportsHideTheExplorerLinkWithoutADatabase(t *testing.T) {
	h, _ := reportsRouter(t, codegraphFixture, codegraph.Options{}, false)
	_, schema, _ := get(t, h, "/dev/graph/reports/schema")
	if strings.Contains(schema, "/dev/db#") || !strings.Contains(schema, "orders") {
		t.Errorf("without /dev/db there is no link into it:\n%.600s", mainOf(schema))
	}
}

func TestDevGraphReportsRejectUnknownReports(t *testing.T) {
	h, _ := reportsRouter(t, codegraphFixture, codegraph.Options{}, false)
	for _, path := range []string{"/dev/graph/reports/nope", "/dev/graph/reports/routes.json"} {
		if code, body, _ := get(t, h, path); code != http.StatusNotFound || !strings.Contains(body, "routes, config, schema, layers") {
			t.Errorf("%s: %d %s", path, code, body)
		}
	}
	// the name is case-insensitive
	if code, _, _ := get(t, h, "/dev/graph/reports/Layers"); code != http.StatusOK {
		t.Errorf("case: %d", code)
	}
}

// An edit that the dev server's file watcher ignores shows up on the next load.
func TestDevGraphReportsRefreshStaleLayers(t *testing.T) {
	root := copyTree(t, codegraphFixture)
	h, _ := reportsRouter(t, root, codegraph.Options{}, false)
	if _, body, _ := get(t, h, "/dev/graph/reports/config"); strings.Contains(body, "cache.ttl") {
		t.Fatal("the key exists before it was written")
	}
	env := filepath.Join(root, "config/env.yml")
	b, _ := os.ReadFile(env)
	if err := os.WriteFile(env, append(b, []byte("\ncache:\n  ttl: ${FIXTURE_CACHE_TTL:60}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, body, _ := get(t, h, "/dev/graph/reports/config"); !strings.Contains(body, "cache.ttl") || !strings.Contains(body, "FIXTURE_CACHE_TTL") {
		t.Errorf("the edited config is not in the report:\n%.600s", mainOf(body))
	}
}

func TestEditorHrefOnlyLetsEditorLinksThrough(t *testing.T) {
	f := reportFuncs["editorHref"].(func(string) template.URL)
	for in, want := range map[string]string{
		"vscode://file/Users/me/p/a.go:12": "vscode://file/Users/me/p/a.go:12",
		"javascript:alert(1)":              "#",
		"vscode://other/x":                 "#",
		"":                                 "#",
		"https://example.com":              "#",
	} {
		if got := string(f(in)); got != want {
			t.Errorf("editorHref(%q) = %q, want %q", in, got, want)
		}
	}
}

// With the call graph on, the routes report lists the tables each route reaches.
func TestRoutesReportShowsTablesWhenCallsAreIndexed(t *testing.T) {
	h, _ := reportsRouter(t, thisRepo, codegraph.Options{Router: thisRepoRouter(), Calls: true}, true)
	code, body, _ := get(t, h, "/dev/graph/reports/routes")
	if code != http.StatusOK || !strings.Contains(body, "<th>Tables reached</th>") {
		t.Fatalf("%d: %.600s", code, mainOf(body))
	}
	if !strings.Contains(body, "ServeHTMX") || !strings.Contains(body, "none</span>") {
		t.Errorf("the htmx route reaches no table: %.800s", mainOf(body))
	}
	h, _ = reportsRouter(t, thisRepo, codegraph.Options{Router: thisRepoRouter()}, true)
	if _, body, _ := get(t, h, "/dev/graph/reports/routes"); strings.Contains(body, "Tables reached") {
		t.Error("no call graph, no column")
	}
}
