package devmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"petars-dev/govibe/storage/graphdb"
)

// callTool posts a tools/call to the stateless streamable-HTTP handler, the way
// an agent does.
func callTool(t *testing.T, h http.Handler, name, args string) string {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/dev/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// Every graph_query first lets the graph catch up with edits; the hook is
// optional.
func TestGraphQueryRefreshesFirst(t *testing.T) {
	store, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.AddQuad(quad.Make(quad.IRI("pkg:a"), quad.IRI("name"), quad.String("a"), nil)); err != nil {
		t.Fatal(err)
	}

	calls := 0
	h := NewHandler(nil, store.QuadStore, func(ctx context.Context) {
		calls++
		// What a refresh does: the graph changes before the query runs.
		_ = store.AddQuad(quad.Make(quad.IRI("pkg:b"), quad.IRI("name"), quad.String("fresh"), nil))
	})
	out := callTool(t, h, "graph_query", `{"query":"g.V(g.IRI('pkg:b')).Out(g.IRI('name')).All()"}`)
	if calls != 1 {
		t.Errorf("refresh called %d times, want once per call", calls)
	}
	if !strings.Contains(out, "fresh") {
		t.Errorf("the query did not see what the refresh wrote: %s", out)
	}
	callTool(t, h, "graph_query", `{"query":"g.V().All()"}`)
	if calls != 2 {
		t.Errorf("refresh called %d times after two calls", calls)
	}

	// No hook: still works.
	plain := NewHandler(nil, store.QuadStore, nil)
	if out := callTool(t, plain, "graph_query", `{"query":"g.V(g.IRI('pkg:a')).Out(g.IRI('name')).All()"}`); !strings.Contains(out, `\"id\":\"a\"`) {
		t.Errorf("graph_query without a refresh hook: %s", out)
	}
}
