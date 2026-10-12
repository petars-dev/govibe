package devmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
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

// fakeIndexer counts the refreshes the graph tools ask for.
type fakeIndexer struct {
	refresh   func()
	refreshes int
}

func (f *fakeIndexer) Refresh(context.Context) (*codegraph.Report, error) {
	f.refreshes++
	if f.refresh != nil {
		f.refresh()
	}
	return nil, nil
}

func (f *fakeIndexer) RunLayers(context.Context, []string) (*codegraph.Report, error) {
	return &codegraph.Report{}, nil
}
func (f *fakeIndexer) Stale(context.Context) ([]string, error) { return nil, nil }
func (f *fakeIndexer) LayerNames() []string                    { return []string{"code"} }

// Every graph_query first lets the graph catch up with edits; the indexer is
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

	ix := &fakeIndexer{refresh: func() {
		// What a refresh does: the graph changes before the query runs.
		_ = store.AddQuad(quad.Make(quad.IRI("pkg:b"), quad.IRI("name"), quad.String("fresh"), nil))
	}}
	h := NewHandler(nil, store.QuadStore, ix)
	out := callTool(t, h, "graph_query", `{"query":"g.V(g.IRI('pkg:b')).Out(g.IRI('name')).All()"}`)
	if ix.refreshes != 1 {
		t.Errorf("refresh called %d times, want once per call", ix.refreshes)
	}
	if !strings.Contains(out, "fresh") {
		t.Errorf("the query did not see what the refresh wrote: %s", out)
	}
	callTool(t, h, "graph_query", `{"query":"g.V().All()"}`)
	if ix.refreshes != 2 {
		t.Errorf("refresh called %d times after two calls", ix.refreshes)
	}

	// No hook: still works.
	plain := NewHandler(nil, store.QuadStore, nil)
	if out := callTool(t, plain, "graph_query", `{"query":"g.V(g.IRI('pkg:a')).Out(g.IRI('name')).All()"}`); !strings.Contains(out, `\"id\":\"a\"`) {
		t.Errorf("graph_query without a refresh hook: %s", out)
	}
}
