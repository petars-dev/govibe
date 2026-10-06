package codegraph

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"

	"petars-dev/govibe/storage/graphdb"
)

// fixtureStore indexes the fixture (every layer, the routes from fixtureRoutes,
// the tables from its migrations alone) into a fresh store.
func fixtureStore(t *testing.T) (*cayley.Handle, *Indexer) {
	t.Helper()
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root = fixtureRoot
	ix := New(h.QuadStore, opts)
	rep, err := ix.Run(context.Background())
	if err != nil {
		t.Fatalf("index fixture: %v", err)
	}
	for _, l := range rep.Layers {
		if l.Status == statusError {
			t.Fatalf("layer %s failed: %s", l.Name, l.Error)
		}
	}
	return h, ix
}

// freshGraph is a newly indexed and loaded model of the fixture, for a test that
// changes it.
func freshGraph(t *testing.T) *Model {
	t.Helper()
	h, _ := fixtureStore(t)
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	return m
}

var sharedFixture struct {
	once sync.Once
	m    *Model
	err  error
}

// fixtureGraph is the loaded model of the indexed fixture, built once and shared:
// a test that only reads it uses this one, and must not change it. A model needs
// nothing from the store once loaded, so the store is closed straight away.
func fixtureGraph(t *testing.T) *Model {
	t.Helper()
	sharedFixture.once.Do(func() {
		dir, err := os.MkdirTemp("", "codegraph-fixture-")
		if err != nil {
			sharedFixture.err = err
			return
		}
		defer os.RemoveAll(dir)
		h, err := graphdb.Open(dir)
		if err != nil {
			sharedFixture.err = err
			return
		}
		defer h.Close()
		opts := fixtureOptions()
		opts.Root = fixtureRoot
		if _, err := New(h.QuadStore, opts).Run(context.Background()); err != nil {
			sharedFixture.err = err
			return
		}
		sharedFixture.m, sharedFixture.err = Load(context.Background(), h.QuadStore)
	})
	if sharedFixture.err != nil {
		t.Fatalf("index the fixture: %v", sharedFixture.err)
	}
	return sharedFixture.m
}

func ids(refs []NodeRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

// mustHave fails unless every want is among got.
func mustHave(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("%s: missing %s in %s", what, w, strings.Join(got, ", "))
		}
	}
}

// mustNotHave fails if any unwanted ID is among got.
func mustNotHave(t *testing.T, what string, got []string, unwanted ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	for _, w := range unwanted {
		if have[w] {
			t.Errorf("%s: unexpected %s in %s", what, w, strings.Join(got, ", "))
		}
	}
}

// gizmoRun runs a Gizmo query the way graph_query does and returns what came
// back, one string per result, or the error.
func gizmoRun(t *testing.T, h *cayley.Handle, q string) ([]string, error) {
	t.Helper()
	ctx := context.Background()
	it, err := query.Execute(ctx, h.QuadStore, gizmo.Name, q, query.Options{Collation: query.JSON, Limit: 1000})
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var out []string
	for it.Next(ctx) {
		out = append(out, fmt.Sprint(it.Result()))
	}
	return out, it.Err()
}
