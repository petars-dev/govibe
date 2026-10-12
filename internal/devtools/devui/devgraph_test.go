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

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/graphdb"
)

const codegraphFixture = "../../codegraph/testdata/fixture"

func reindexRouter(t *testing.T, root string) (http.Handler, func() int) {
	t.Helper()
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open graph store: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	r := chi.NewRouter()
	r.Post("/dev/graph/reindex", DevGraphReindex(codegraph.New(h.QuadStore, codegraph.Options{Root: root})))
	count := func() int {
		n := 0
		it := h.QuadStore.QuadsAllIterator()
		defer it.Close()
		for it.Next(t.Context()) {
			n++
		}
		return n
	}
	return r, count
}

func TestDevGraphReindex(t *testing.T) {
	r, quads := reindexRouter(t, codegraphFixture)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/dev/graph/reindex", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type %q", ct)
	}
	var body struct {
		Build struct {
			IndexerVersion string `json:"indexer_version"`
		} `json:"build"`
		Quads  int `json:"quads"`
		Added  int `json:"added"`
		Layers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Quads  int    `json:"quads"`
		} `json:"layers"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, rec.Body)
	}
	status := map[string]string{}
	sum := 0
	for _, l := range body.Layers {
		status[l.Name] = l.Status
		sum += l.Quads
	}
	want := map[string]string{"code": "ok", "routes": "skipped", "templates": "ok", "config": "ok", "mcp": "ok", "docs": "partial"}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("layer %s = %q, want %q (all: %v)", name, status[name], st, status)
		}
	}
	if body.Build.IndexerVersion == "" || body.Quads != sum || body.Added != body.Quads || body.Error != "" {
		t.Errorf("body = %+v", body)
	}
	if n := quads(); n <= body.Quads {
		t.Errorf("store holds %d quads, want the layer's %d plus the meta layer's", n, body.Quads)
	}

	// Idempotent: a second call reports nothing added or removed.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/dev/graph/reindex", nil))
	var again struct{ Added, Removed int }
	_ = json.Unmarshal(rec.Body.Bytes(), &again)
	if rec.Code != http.StatusOK || again.Added != 0 || again.Removed != 0 {
		t.Errorf("second reindex: %d %s", rec.Code, rec.Body)
	}
}

func TestDevGraphReindexIsPostOnly(t *testing.T) {
	r, _ := reindexRouter(t, codegraphFixture)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dev/graph/reindex", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
}

// A layer that cannot run is reported with the same JSON body and a 500, so a
// script does not mistake it for success.
func TestDevGraphReindexReportsFailedLayers(t *testing.T) {
	r, _ := reindexRouter(t, t.TempDir()) // no go.mod
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/dev/graph/reindex", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Layers []struct{ Name, Status, Error string } `json:"layers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body = %s (%v)", rec.Body, err)
	}
	// The code layer fails, and so does everything that resolves against it,
	// each saying why; nothing reports a result built on a missing code index.
	errs := map[string]string{}
	for _, l := range body.Layers {
		if l.Status == "error" {
			errs[l.Name] = l.Error
		}
	}
	for _, name := range []string{"code", "templates", "config", "mcp", "docs"} {
		if !strings.Contains(errs[name], "go.mod") {
			t.Errorf("layer %s error = %q, want one naming go.mod (all: %v)", name, errs[name], errs)
		}
	}
}

func TestDevGraphReindexMissingRoot(t *testing.T) {
	r, _ := reindexRouter(t, t.TempDir()+"/nope")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/dev/graph/reindex", nil))
	var body struct{ Error string }
	if rec.Code != http.StatusInternalServerError || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

// copyTree copies a directory tree, so a test can edit it.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// Editing a spec, which the dev server's file watcher ignores, shows up in the
// graph on the next page load, without a restart.
func TestDevGraphPageRefreshesStaleLayers(t *testing.T) {
	root := copyTree(t, codegraphFixture)
	h, err := graphdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ix := codegraph.New(h.QuadStore, codegraph.Options{Root: root})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	hasItem := func() bool {
		it := h.QuadStore.QuadsAllIterator()
		defer it.Close()
		for it.Next(context.Background()) {
			if strings.Contains(h.QuadStore.Quad(it.Result()).Subject.String(), "item:hello/greeting-analytics") {
				return true
			}
		}
		return false
	}
	if hasItem() {
		t.Fatal("the row exists before it was written")
	}

	spec := filepath.Join(root, "docs/specs/SPEC_HELLO.md")
	b, _ := os.ReadFile(spec)
	if err := os.WriteFile(spec, append(b, []byte("| Greeting analytics | No | | Planned |\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	DevGraphPage(ix).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dev/graph", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "GoVibe Dev Graph") {
		t.Fatalf("page: %d %.200s", rec.Code, rec.Body)
	}
	if !hasItem() {
		t.Error("the edited spec row is not in the graph after the page load")
	}
	if stale, _ := ix.Stale(context.Background()); len(stale) != 0 {
		t.Errorf("still stale after the page load: %v", stale)
	}
}
