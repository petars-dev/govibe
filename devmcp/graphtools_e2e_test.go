package devmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"petars-dev/govibe/business/services/codegraph"
	"petars-dev/govibe/controllers"
	"petars-dev/govibe/storage/graphdb"
)

// rpc sends one JSON-RPC request to the stateless streamable-HTTP handler, the
// way an agent does, and returns the result (or the protocol error).
func rpc(t *testing.T, h http.Handler, method string, params any) (json.RawMessage, *struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req := httptest.NewRequest(http.MethodPost, "/dev/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, rec.Code, rec.Body)
	}
	raw := rec.Body.String()
	// the reply is a server-sent event or plain JSON
	for _, line := range strings.Split(raw, "\n") {
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			raw = d
		}
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("%s: unreadable reply %q: %v", method, raw, err)
	}
	return resp.Result, resp.Error
}

// toolResult is what tools/call returns.
type toolResult struct {
	IsError           bool           `json:"isError"`
	StructuredContent map[string]any `json:"structuredContent"`
	Content           []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// call runs a tool and returns its result; a protocol error fails the test (a
// tool that merely fails comes back as isError).
func call(t *testing.T, h http.Handler, name string, args map[string]any) toolResult {
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

func (r toolResult) text() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

// ok fails the test if the tool reported an error.
func (r toolResult) ok(t *testing.T, what string) toolResult {
	t.Helper()
	if r.IsError {
		t.Fatalf("%s failed: %s", what, r.text())
	}
	if r.StructuredContent == nil {
		t.Fatalf("%s returned no structured content", what)
	}
	return r
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func strs(v any) []string {
	var out []string
	for _, x := range list(v) {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// indexedHandler serves the graph tools over this repository, indexed with a
// router holding a few of its real handlers.
func indexedHandler(t *testing.T) (http.Handler, *codegraph.Indexer) {
	t.Helper()
	store, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	r := chi.NewRouter()
	r.Get("/", controllers.Home)
	r.Route("/api", func(api chi.Router) { api.Get("/hello", controllers.HelloAPI) })
	r.Get("/hello", controllers.Hello)
	ix := codegraph.New(store.QuadStore, codegraph.Options{Root: "..", Router: r})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewHandler(nil, store.QuadStore, ix), ix
}

func TestEveryGraphToolIsListedWithGuidance(t *testing.T) {
	h, _ := indexedHandler(t)
	res, perr := rpc(t, h, "tools/list", map[string]any{})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Type       string         `json:"type"`
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			} `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint   bool `json:"readOnlyHint"`
				IdempotentHint bool `json:"idempotentHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	tools := map[string]int{}
	for i, tl := range out.Tools {
		tools[tl.Name] = i
	}
	for _, name := range []string{"graph_query", "graph_schema", "graph_context", "graph_impact", "graph_path", "graph_search", "graph_neighbors", "graph_findings", "graph_reindex"} {
		i, ok := tools[name]
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}
		tl := out.Tools[i]
		if len(tl.Description) < 80 || tl.InputSchema.Type != "object" {
			t.Errorf("%s: %+v", name, tl)
		}
		if name == "graph_reindex" {
			if tl.Annotations.ReadOnlyHint || !tl.Annotations.IdempotentHint {
				t.Errorf("graph_reindex writes (idempotently): %+v", tl.Annotations)
			}
		} else if !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s only reads: %+v", name, tl.Annotations)
		}
	}
	// the agent is pointed at graph_schema, from the tool it already knows and from the server's own words
	if !strings.Contains(out.Tools[tools["graph_query"]].Description, "graph_schema first") {
		t.Error("graph_query should send agents to graph_schema")
	}
	if !strings.HasPrefix(out.Tools[tools["graph_schema"]].Description, "START HERE") {
		t.Error("graph_schema should say it is the start")
	}
	for name, required := range map[string][]string{"graph_impact": {"node"}, "graph_path": {"from", "to"}, "graph_search": {"text"}, "graph_neighbors": {"node"}, "graph_context": nil} {
		got := out.Tools[tools[name]].InputSchema.Required
		if strings.Join(got, ",") != strings.Join(required, ",") && len(got)+len(required) > 0 {
			// order is not significant
			if len(got) != len(required) {
				t.Errorf("%s requires %v, want %v", name, got, required)
			}
		}
	}

	// the initialize reply carries the instructions
	init, perr := rpc(t, h, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	var ir struct {
		Instructions string `json:"instructions"`
	}
	_ = json.Unmarshal(init, &ir)
	if !strings.Contains(ir.Instructions, "START WITH graph_schema") || !strings.Contains(ir.Instructions, "not proven") {
		t.Errorf("instructions: %s", ir.Instructions)
	}
}

// Without an indexer the graph tools answer from the store as it is, and there
// is nothing to reindex.
func TestWithoutAnIndexerThereIsNoReindexTool(t *testing.T) {
	store, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h := NewHandler(nil, store.QuadStore, nil)
	res, _ := rpc(t, h, "tools/list", map[string]any{})
	if strings.Contains(string(res), "graph_reindex") || !strings.Contains(string(res), "graph_context") {
		t.Errorf("tools: %s", res)
	}
	// an empty graph is an error that says what to do, not an empty answer
	tr := call(t, h, "graph_schema", map[string]any{})
	if !tr.IsError || !strings.Contains(tr.text(), "has not been indexed yet") {
		t.Errorf("empty graph: %+v", tr)
	}
}

func TestGraphToolsAnswerOverMCP(t *testing.T) {
	h, _ := indexedHandler(t)

	// graph_schema: the first call an agent makes
	s := call(t, h, "graph_schema", nil).ok(t, "graph_schema").StructuredContent
	fresh, _ := s["freshness"].(map[string]any)
	if fresh["indexed"] != true || len(list(s["kinds"])) < 10 || len(list(s["predicates"])) < 30 || len(list(s["examples"])) < 5 || len(list(s["questions"])) < 6 {
		t.Errorf("schema: freshness=%v kinds=%d predicates=%d", fresh, len(list(s["kinds"])), len(list(s["predicates"])))
	}
	if !strings.Contains(strings.Join(strs(s["notes"]), " "), "calls layer is off") {
		t.Errorf("notes: %v", s["notes"])
	}

	// graph_context on a file: the routes it serves
	c := call(t, h, "graph_context", map[string]any{"path": "controllers/api.go"}).ok(t, "graph_context").StructuredContent
	routes := list(c["routes"])
	if len(routes) != 1 || routes[0].(map[string]any)["id"] != "route:GET:/api/hello" {
		t.Errorf("routes of controllers/api.go: %v", routes)
	}
	// ... and on the route: handler, and an explicit statement that no table is reached
	c = call(t, h, "graph_context", map[string]any{"route": "GET /api/hello"}).ok(t, "graph_context route").StructuredContent
	if h := c["handler"].(map[string]any); h["id"] != "func:controllers.HelloAPI" || h["path"] != "controllers/api.go" {
		t.Errorf("handler: %v", h)
	}
	reach := c["reach"].(map[string]any)
	if len(list(reach["tables"])) != 0 || !strings.Contains(reach["note"].(string), "No table is reached") {
		t.Errorf("reach: %v", reach)
	}

	// graph_impact: what breaks if I change a file (one call)
	imp := call(t, h, "graph_impact", map[string]any{"node": "controllers/api.go"}).ok(t, "graph_impact").StructuredContent
	var routeHit bool
	for _, a := range list(imp["affected"]) {
		am := a.(map[string]any)
		if am["id"] == "route:GET:/api/hello" && am["confidence"] == float64(100) && am["method"] == "edge" {
			routeHit = true
		}
	}
	if !routeHit || len(list(imp["docs"])) == 0 {
		t.Errorf("impact of controllers/api.go: %v", imp)
	}
	// a node nothing depends on: an empty list, still valid output
	imp = call(t, h, "graph_impact", map[string]any{"node": "route:GET:/api/hello"}).ok(t, "graph_impact of a leaf").StructuredContent
	if len(list(imp["affected"])) != 0 || imp["total"] != float64(0) {
		t.Errorf("a route has no dependents: %v", imp)
	}
	// the kinds filter and the limit
	imp = call(t, h, "graph_impact", map[string]any{"node": "business/actions", "kinds": []string{"package"}, "limit": 1}).ok(t, "graph_impact kinds").StructuredContent
	if len(list(imp["affected"])) != 1 || imp["truncated"] != true {
		t.Errorf("limit: %v", imp)
	}

	// graph_path: exact, and the steps
	p := call(t, h, "graph_path", map[string]any{"from": "GET /api/hello", "to": "controllers.HelloAPI"}).ok(t, "graph_path").StructuredContent
	if p["found"] != true || p["inferred"] == true || len(list(p["steps"])) != 1 {
		t.Errorf("path: %v", p)
	}
	// ... and a clean no, with what each side reaches
	p = call(t, h, "graph_path", map[string]any{"from": "GET /api/hello", "to": "cfg:server.port"}).ok(t, "graph_path no").StructuredContent
	if _, found := p["found"]; !found || p["found"] != false || p["note"] == "" {
		t.Errorf("path: %v", p)
	}

	// graph_search and graph_neighbors
	sr := call(t, h, "graph_search", map[string]any{"text": "HelloAPI", "kinds": []string{"function"}}).ok(t, "graph_search").StructuredContent
	if hits := list(sr["hits"]); len(hits) == 0 || hits[0].(map[string]any)["id"] != "func:controllers.HelloAPI" {
		t.Errorf("search: %v", sr)
	}
	sr = call(t, h, "graph_search", map[string]any{"text": "zzz no such thing"}).ok(t, "graph_search empty").StructuredContent
	if len(list(sr["hits"])) != 0 || sr["total"] != float64(0) {
		t.Errorf("empty search: %v", sr)
	}
	nb := call(t, h, "graph_neighbors", map[string]any{"node": "controllers", "predicates": []string{"imports"}, "direction": "out"}).ok(t, "graph_neighbors").StructuredContent
	var imports []string
	for _, n := range list(nb["nodes"]) {
		imports = append(imports, n.(map[string]any)["id"].(string))
	}
	if !strings.Contains(strings.Join(imports, ","), "pkg:business/actions") {
		t.Errorf("neighbors: %v", imports)
	}

	// graph_findings: this repository's known layering violation leads, and the rules that ran are listed
	f := call(t, h, "graph_findings", map[string]any{"severity": "error"}).ok(t, "graph_findings").StructuredContent
	fl := list(f["findings"])
	if f["available"] != true || len(fl) != 1 || len(list(f["rules"])) < 10 {
		t.Fatalf("findings: %v", f)
	}
	first := fl[0].(map[string]any)
	if first["rule"] != "layering" || first["about"] != "pkg:controllers" || !strings.Contains(first["message"].(string), "storage/dbexplorer") {
		t.Errorf("the controllers -> storage import is a finding: %v", first)
	}
	// and the context of the package carries it
	cc := call(t, h, "graph_context", map[string]any{"path": "controllers"}).ok(t, "graph_context").StructuredContent
	if cf := list(cc["findings"]); len(cf) == 0 || cf[0].(map[string]any)["rule"] != "layering" {
		t.Errorf("a briefing carries its findings: %v", cc["findings"])
	}
}

func TestGraphToolErrorsAreReportedToTheAgent(t *testing.T) {
	h, _ := indexedHandler(t)
	for name, tc := range map[string]struct {
		tool string
		args map[string]any
		want string
	}{
		"two arguments":  {"graph_context", map[string]any{"path": "a", "table": "b"}, "exactly one of"},
		"no argument":    {"graph_context", map[string]any{}, "exactly one of"},
		"unknown symbol": {"graph_context", map[string]any{"symbol": "nope.Nope"}, "no node matches"},
		"a near miss":    {"graph_impact", map[string]any{"node": "hello api"}, "did you mean"},
		"bad kind":       {"graph_search", map[string]any{"text": "x", "kinds": []string{"banana"}}, "unknown kind"},
		"bad predicate":  {"graph_neighbors", map[string]any{"node": "controllers", "predicates": []string{"befriends"}}, "unknown predicate"},
		"bad direction":  {"graph_neighbors", map[string]any{"node": "controllers", "direction": "up"}, "direction"},
		"bad path end":   {"graph_path", map[string]any{"from": "nope.Nope", "to": "controllers"}, "from: "},
		"unknown layer":  {"graph_reindex", map[string]any{"layers": []string{"nope"}}, "unknown layer"},
		"empty search":   {"graph_search", map[string]any{"text": " "}, "text is required"},
	} {
		tr := call(t, h, tc.tool, tc.args)
		if !tr.IsError || !strings.Contains(tr.text(), tc.want) {
			t.Errorf("%s: isError=%v text=%q, want %q", name, tr.IsError, tr.text(), tc.want)
		}
	}
	// a missing required argument is rejected by the schema, before the tool runs
	res, perr := rpc(t, h, "tools/call", map[string]any{"name": "graph_impact", "arguments": map[string]any{}})
	if perr == nil {
		var tr toolResult
		_ = json.Unmarshal(res, &tr)
		if !tr.IsError {
			t.Errorf("graph_impact without a node should fail: %s", res)
		}
	}
}

func TestGraphReindexRebuildsLayers(t *testing.T) {
	h, _ := indexedHandler(t)
	r := call(t, h, "graph_reindex", map[string]any{"layers": []string{"docs"}}).ok(t, "graph_reindex").StructuredContent
	if r["failed"] != false || len(strs(r["known_layers"])) != 10 {
		t.Errorf("reindex: %v", r)
	}
	var ran []string
	for _, l := range list(r["layers"]) {
		lm := l.(map[string]any)
		if lm["ran"] == true {
			ran = append(ran, lm["name"].(string))
		}
		if lm["status"] != "ok" && lm["status"] != "partial" && lm["status"] != "off" { // calls and git are opt-in
			t.Errorf("layer %v: %v", lm["name"], lm["status"])
		}
	}
	if strings.Join(ran, ",") != "docs,findings" {
		t.Errorf("ran %v (docs, and the findings derived from it)", ran)
	}
	// no layers: all of them
	r = call(t, h, "graph_reindex", nil).ok(t, "graph_reindex all").StructuredContent
	n := 0
	for _, l := range list(r["layers"]) {
		if l.(map[string]any)["ran"] == true {
			n++
		}
	}
	if n != 8 || r["quads"].(float64) < 1000 {
		t.Errorf("full reindex: %d layers, %v quads", n, r["quads"])
	}
}

func TestGraphResourcesAndPrompt(t *testing.T) {
	h, _ := indexedHandler(t)

	res, perr := rpc(t, h, "resources/list", map[string]any{})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	for _, uri := range []string{"govibe://graph/overview", "govibe://graph/schema"} {
		if !strings.Contains(string(res), uri) {
			t.Errorf("resource %s is not listed: %s", uri, res)
		}
	}
	read := func(uri string) (text, mime string) {
		raw, perr := rpc(t, h, "resources/read", map[string]any{"uri": uri})
		if perr != nil {
			t.Fatalf("%s: %s", uri, perr.Message)
		}
		var out struct {
			Contents []struct {
				URI      string `json:"uri"`
				MIMEType string `json:"mimeType"`
				Text     string `json:"text"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Contents) != 1 || out.Contents[0].URI != uri {
			t.Fatalf("%s: %v %s", uri, err, raw)
		}
		return out.Contents[0].Text, out.Contents[0].MIMEType
	}
	overview, mime := read("govibe://graph/overview")
	if mime != "text/markdown" || !strings.Contains(overview, "# Project graph overview") || !strings.Contains(overview, "`/api/hello`") {
		t.Errorf("overview (%s):\n%.600s", mime, overview)
	}
	schema, mime := read("govibe://graph/schema")
	var sj map[string]any
	if err := json.Unmarshal([]byte(schema), &sj); err != nil || mime != "application/json" || len(list(sj["kinds"])) < 10 {
		t.Errorf("schema (%s): %v", mime, err)
	}

	// the prompt
	pl, perr := rpc(t, h, "prompts/list", map[string]any{})
	if perr != nil || !strings.Contains(string(pl), "explain-change") || !strings.Contains(string(pl), `"paths"`) || !strings.Contains(string(pl), `"diff"`) {
		t.Errorf("prompts/list: %s %v", pl, perr)
	}
	pg, perr := rpc(t, h, "prompts/get", map[string]any{"name": "explain-change", "arguments": map[string]string{"paths": "controllers/api.go"}})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	var pr struct {
		Messages []struct {
			Role    string `json:"role"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(pg, &pr); err != nil || len(pr.Messages) != 1 || pr.Messages[0].Role != "user" {
		t.Fatalf("prompt: %v %s", err, pg)
	}
	for _, want := range []string{"# Explain this change", "controllers/api.go", "GET /api/hello", "## Docs to update", "SKILL.md"} {
		if !strings.Contains(pr.Messages[0].Content.Text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, pr.Messages[0].Content.Text)
		}
	}
	// a diff works as well as paths, and neither is an error
	diff := "diff --git a/controllers/api.go b/controllers/api.go\n--- a/controllers/api.go\n+++ b/controllers/api.go\n@@ -1 +1 @@\n"
	pg, perr = rpc(t, h, "prompts/get", map[string]any{"name": "explain-change", "arguments": map[string]string{"diff": diff}})
	if perr != nil || !strings.Contains(string(pg), "GET /api/hello") {
		t.Errorf("a diff: %v %s", perr, pg)
	}
	if _, perr = rpc(t, h, "prompts/get", map[string]any{"name": "explain-change", "arguments": map[string]string{}}); perr == nil {
		t.Error("explain-change with nothing to explain should fail")
	}
}
