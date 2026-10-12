//go:build govibe_dev

package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// With the govibe_dev tag the dev tools are compiled in: in a development
// environment, when asked for, they are mounted, pages see the marker and the
// panel's files are served.
func TestDevToolsMountWhenCompiledIn(t *testing.T) {
	root := isolate(t)
	t.Setenv("APP_ENV", "dev")
	b, err := Build(context.Background(), probeApp(), Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !b.DevMounted {
		t.Fatal("dev tools were not mounted")
	}
	if _, body, _ := get(t, b.Router, "/probe"); body != "dev" {
		t.Fatalf("a page must see the dev marker, got %q", body)
	}
	if code, body, _ := get(t, b.Router, "/_govibe/static/theme.css"); code != 200 || len(body) == 0 {
		t.Fatalf("the panel's theme = %d", code)
	}
	// The panel's files are the framework's, and never served from the app's /assets.
	if code, _, _ := get(t, b.Router, "/assets/static/graph.js"); code != 404 {
		t.Fatalf("/assets/static/graph.js = %d", code)
	}
	if code, _, _ := get(t, b.Router, "/_govibe/static/graph.js"); code != 200 {
		t.Fatalf("/_govibe/static/graph.js = %d", code)
	}
	// The graph store lives under the application root, never next to the process.
	if _, err := os.Stat(filepath.Join(root, "docs", "graph")); err != nil {
		t.Fatalf("the graph store is not under the root: %v", err)
	}
}

// Compiled in does not mean mounted: production refuses, whatever is asked.
func TestDevToolsRefuseProductionEvenWhenCompiledIn(t *testing.T) {
	isolate(t) // APP_ENV=prod
	b, err := Build(context.Background(), probeApp(), Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.DevMounted {
		t.Fatal("the dev tools were mounted in production")
	}
	for _, p := range []string{"/dev/graph", "/dev/docs/app", "/dev/mcp", "/_govibe/static/graph.js"} {
		if code, _, _ := get(t, b.Router, p); code != 404 {
			t.Errorf("%s = %d in production", p, code)
		}
	}
}
