package devui

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
)

// dataRouter mounts the graph page's data endpoints over an indexer that has
// indexed root, the way devtools.Mount mounts them.
func dataRouter(t *testing.T, root string, db bool) (http.Handler, *codegraph.Indexer, func(quad.Quad)) {
	t.Helper()
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: root})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ui := &GraphUI{Indexer: ix, Store: h.QuadStore, DB: db}
	r := chi.NewRouter()
	r.Get("/dev/graph", DevGraphPage(ix))
	r.Route("/dev/graph/data", func(d chi.Router) {
		d.Get("/status", ui.Status())
		d.Get("/view", ui.View())
		d.Get("/node", ui.Node())
		d.Get("/search", ui.Search())
		d.Get("/findings", ui.Findings())
		d.Get("/table", ui.TableRefs())
	})
	return r, ix, func(q quad.Quad) {
		if err := h.AddQuad(q); err != nil {
			t.Fatal(err)
		}
	}
}

func getJSON(t *testing.T, h http.Handler, url string, want int) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != want {
		t.Fatalf("GET %s = %d, want %d: %.300s", url, rec.Code, want, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("GET %s content type %q", url, ct)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET %s: bad JSON: %v\n%.300s", url, err, rec.Body)
	}
	return out
}

func list(v any) []any { l, _ := v.([]any); return l }

func ids(nodes any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, n := range list(nodes) {
		m := n.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func TestGraphDataStatusAndViews(t *testing.T) {
	h, _, _ := dataRouter(t, codegraphFixture, false)

	st := getJSON(t, h, "/dev/graph/data/status", 200)
	fr := st["freshness"].(map[string]any)
	if fr["indexed"] != true || fr["quads"].(float64) < 1000 || fr["added"].(float64) == 0 || len(list(fr["layers"])) != 10 {
		t.Errorf("freshness: %v", fr)
	}
	fc := st["findings"].(map[string]any)
	if fc["available"] != true || fc["error"].(float64) != 1 || fc["total"].(float64) < 5 {
		t.Errorf("findings counts: %v", fc)
	}

	arch := getJSON(t, h, "/dev/graph/data/view", 200) // the default preset
	if arch["preset"] != "architecture" || len(list(arch["presets"])) != 7 {
		t.Fatalf("view: %v", arch["preset"])
	}
	nodes := ids(arch["nodes"])
	if n := nodes["pkg:controllers"]; n == nil || n["worst"] != "error" || n["findings"].(float64) != 1 {
		t.Errorf("controllers carries its finding: %v", n)
	}
	var violation bool
	for _, e := range list(arch["edges"]) {
		m := e.(map[string]any)
		violation = violation || (m["source"] == "pkg:controllers" && m["target"] == "pkg:storage/orderstore" && m["violation"] == true)
	}
	if !violation {
		t.Errorf("the layering violation is an edge flagged for drawing: %v", arch["edges"])
	}

	er := getJSON(t, h, "/dev/graph/data/view?preset=db_er", 200)
	if len(ids(er["nodes"])) != 4 {
		t.Errorf("er nodes: %v", er["nodes"])
	}
	// no router in this indexer: the routes layer is skipped, and the view says so
	flow := getJSON(t, h, "/dev/graph/data/view?preset=request_flow", 200)
	if len(list(flow["nodes"])) != 0 || !strings.Contains(strings.Join(strs(flow["notes"]), " "), "routes layer") {
		t.Errorf("request flow without a router: %v", flow["notes"])
	}

	focus := getJSON(t, h, "/dev/graph/data/view?focus=controllers/orders.go&depth=2&kinds=func,file,package", 200)
	if focus["preset"] != "focus" || focus["focus"].(map[string]any)["id"] != "file:controllers/orders.go" {
		t.Errorf("focus: %v", focus["focus"])
	}
	for _, n := range ids(focus["nodes"]) {
		if k := n["kind"]; k != "func" && k != "file" && k != "package" {
			t.Errorf("the kind filter is applied: %v", n)
		}
	}
	capped := getJSON(t, h, "/dev/graph/data/view?preset=findings&limit=3", 200)
	if len(list(capped["nodes"])) != 3 || capped["truncated"] != true || capped["total"].(float64) <= 3 {
		t.Errorf("limit: %v nodes of %v", len(list(capped["nodes"])), capped["total"])
	}
}

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		out = append(out, x.(string))
	}
	return out
}

func TestGraphDataBadRequestsAre400WithAReason(t *testing.T) {
	h, _, _ := dataRouter(t, codegraphFixture, false)
	for url, want := range map[string]string{
		"/dev/graph/data/view?preset=pie":               "presets: architecture",
		"/dev/graph/data/view?focus=nope.Nope":          "no node matches",
		"/dev/graph/data/view?preset=db_er&kinds=apple": "unknown kind",
		"/dev/graph/data/node?id=nope.Nope":             "no node matches",
		"/dev/graph/data/node":                          "",
		"/dev/graph/data/search?q=%20":                  "text is required",
	} {
		body := getJSON(t, h, url, 400)
		if msg, _ := body["error"].(string); msg == "" || !strings.Contains(msg, want) {
			t.Errorf("%s: error %q, want it to contain %q", url, msg, want)
		}
	}
}

func TestGraphDataNodeSearchFindingsAndTableRefs(t *testing.T) {
	h, ix, _ := dataRouter(t, codegraphFixture, true)

	n := getJSON(t, h, "/dev/graph/data/node?id=controllers.HelloAPI", 200)
	if n["node"].(map[string]any)["id"] != "func:controllers.HelloAPI" || n["label"] != "controllers.HelloAPI" {
		t.Errorf("function inspector: %v", n["node"])
	}
	kinds := map[string]string{}
	for _, l := range list(n["links"]) {
		m := l.(map[string]any)
		kinds[m["kind"].(string)] = m["url"].(string)
	}
	root := ix.RootDir()
	if root == "" || !strings.HasPrefix(kinds["editor"], "vscode://file"+filepath.ToSlash(root)) || !strings.HasSuffix(kinds["editor"], "/controllers/hello.go:10") {
		t.Errorf("links of a function: %v (root %q)", kinds, root)
	}
	tbl := getJSON(t, h, "/dev/graph/data/node?id=table:orders", 200)
	var dbLink, report bool
	for _, l := range list(tbl["links"]) {
		u := l.(map[string]any)["url"].(string)
		dbLink = dbLink || u == "/dev/db#table=orders"
		report = report || strings.HasPrefix(u, "/dev/graph/reports/schema#")
	}
	if !report {
		t.Errorf("a table links to its place in the schema report: %v", tbl["links"])
	}
	if !dbLink {
		t.Errorf("a table links into the database explorer when it is mounted: %v", tbl["links"])
	}
	if noDB, _, _ := dataRouter(t, codegraphFixture, false); func() bool {
		for _, l := range list(getJSON(t, noDB, "/dev/graph/data/node?id=table:orders", 200)["links"]) {
			if l.(map[string]any)["kind"] == "db" {
				return true
			}
		}
		return false
	}() {
		t.Error("no database link without the explorer")
	}

	s := getJSON(t, h, "/dev/graph/data/search?q=orderstore&kinds=package,file", 200)
	hits := list(s["hits"])
	if len(hits) == 0 || hits[0].(map[string]any)["id"] != "pkg:storage/orderstore" {
		t.Errorf("search: %v", hits)
	}

	f := getJSON(t, h, "/dev/graph/data/findings?severity=error", 200)
	if f["available"] != true || f["total"].(float64) != 1 || len(list(f["rules"])) < 10 {
		t.Errorf("findings: %v", f)
	}
	if first := list(f["findings"])[0].(map[string]any); first["rule"] != "layering" || first["about"] != "pkg:controllers" {
		t.Errorf("first finding: %v", first)
	}

	refs := getJSON(t, h, "/dev/graph/data/table?name=orders", 200)
	if refs["known"] != true {
		t.Fatalf("table refs: %v", refs)
	}
	code := ids(refs["code"])
	if c := code["func:storage/orderstore.Store.List"]; c == nil || c["access"] != "read" || c["certain"] != true || c["graph"] == "" {
		t.Errorf("who reads orders: %v", refs["code"])
	}
	if len(list(refs["docs"])) == 0 {
		t.Errorf("docs that mention orders: %v", refs["docs"])
	}
	none := getJSON(t, h, "/dev/graph/data/table?name=nope", 200)
	if none["known"] != false || len(strs(none["notes"])) != 1 {
		t.Errorf("an unknown table is an answer: %v", none)
	}
}

// A hand-added quad appears in the manual view and nowhere else.
func TestGraphDataManualQuads(t *testing.T) {
	h, _, add := dataRouter(t, codegraphFixture, false)
	if m := getJSON(t, h, "/dev/graph/data/view?preset=manual", 200); len(list(m["nodes"])) != 0 {
		t.Fatalf("nothing is hand-added yet: %v", m["nodes"])
	}
	add(quad.Make(quad.String("alice"), quad.String("knows"), quad.String("<b>bob</b>"), nil))
	m := getJSON(t, h, "/dev/graph/data/view?preset=manual", 200)
	nodes := ids(m["nodes"])
	if len(nodes) != 2 || nodes["<b>bob</b>"] == nil || len(list(m["edges"])) != 1 {
		t.Errorf("manual view: %v", m)
	}
	// the indexed views never show it
	for id := range ids(getJSON(t, h, "/dev/graph/data/view?preset=findings", 200)["nodes"]) {
		if id == "alice" {
			t.Error("a hand-added quad leaks into an indexed view")
		}
	}
}

// Every data call brings stale layers up to date first, so the page is never
// older than the files.
func TestGraphDataCallsRefreshStaleLayers(t *testing.T) {
	root := copyTree(t, codegraphFixture)
	h, ix, _ := dataRouter(t, root, false)

	stale := func() bool {
		for _, f := range list(getJSON(t, h, "/dev/graph/data/findings?rule=docs.stale_ref", 200)["findings"]) {
			if strings.Contains(f.(map[string]any)["id"].(string), "legacy/handler.go") {
				return true
			}
		}
		return false
	}
	if !stale() {
		t.Fatal("the fixture's stale reference is a finding")
	}
	doc := filepath.Join(root, "docs/public/ARCHITECTURE.md")
	b, _ := os.ReadFile(doc)
	if err := os.WriteFile(doc, []byte(strings.ReplaceAll(string(b), "`legacy/handler.go`", "`controllers/hello.go`")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := ix.Stale(context.Background()); len(got) == 0 {
		t.Fatal("the edit makes layers stale")
	}
	if stale() {
		t.Error("the finding is still there after the doc was fixed: the data call did not refresh")
	}
	if got, _ := ix.Stale(context.Background()); len(got) != 0 {
		t.Errorf("stale after the call: %v", got)
	}
}

// The page is a shell: every asset it names exists, and it loads no script from elsewhere.
func TestGraphPageShellNamesRealAssets(t *testing.T) {
	rec := httptest.NewRecorder()
	DevGraphConsole(rec, httptest.NewRequest(http.MethodGet, "/dev/graph", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "GoVibe Dev Graph") {
		t.Fatalf("page: %d", rec.Code)
	}
	// The panel's files are the framework's, served under /_govibe/static from the
	// dev UI's own embed; none of them is read from the application's /assets.
	const static = "/_govibe/static/"
	for _, ref := range []string{static + "graph.js", static + "graph.css", static + "cytoscape.min.js", static + "theme.css"} {
		if !strings.Contains(body, ref) {
			t.Errorf("the page does not load %s", ref)
		}
		if _, err := fs.Stat(Static(), strings.TrimPrefix(ref, static)); err != nil {
			t.Errorf("%s: %v", ref, err)
		}
	}
	if strings.Contains(body, "/assets/") {
		t.Error("the dev page must not read anything from the application's /assets")
	}
	if strings.Contains(body, "http://") || strings.Contains(body, "https://") {
		t.Error("the dev page must not load anything from the network")
	}
	for _, id := range []string{"gv-banner", "gv-presets", "gv-kinds", "cy", "gv-inspector", "gv-findings", "gv-search"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("the shell lacks #%s, which graph.js needs", id)
		}
	}
	js, err := fs.ReadFile(Static(), "graph.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "innerHTML") {
		t.Error("graph.js puts server data on the page as text, never as HTML: it must not use innerHTML")
	}
}
