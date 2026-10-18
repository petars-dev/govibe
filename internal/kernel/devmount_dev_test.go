//go:build govibe_dev

package kernel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/devext"
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

// An app adds its own tools and pages to the panel through App.DevTools: the pages are
// served under /dev/app and linked from /dev, a refused tool is logged and the rest is
// mounted, and an error from DevTools leaves the app's part out without failing the boot.
func TestAppDevToolsAreMountedUnderDevApp(t *testing.T) {
	isolate(t)
	t.Setenv("APP_ENV", "dev")
	app := probeApp()
	called := 0
	app.DevTools = func(rt *Runtime, dev *Dev) error {
		called++
		if rt == nil {
			t.Error("DevTools got no runtime")
		}
		dev.Page("/orders", "Orders", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("orders page")) }))
		dev.Handle("POST", "/orders/refresh", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
		dev.Tool(devext.ToolFunc("orders_count", "Count the orders.", func(context.Context, struct{}) (int, error) { return 3, nil }))
		dev.Tool(devext.Tool{Name: "graph_mine"}) // refused: the framework's family
		return nil
	}
	b, err := Build(context.Background(), app, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if called != 1 {
		t.Fatalf("DevTools called %d times", called)
	}
	if code, body, _ := get(t, b.Router, "/dev/app/orders"); code != 200 || body != "orders page" {
		t.Errorf("/dev/app/orders = %d %q", code, body)
	}
	rec := httptest.NewRecorder()
	b.Router.ServeHTTP(rec, httptest.NewRequest("POST", "/dev/app/orders/refresh", nil))
	if rec.Code != 202 {
		t.Errorf("POST /dev/app/orders/refresh = %d", rec.Code)
	}
	if _, body, _ := get(t, b.Router, "/dev"); !strings.Contains(body, `href="/dev/app/orders"`) || !strings.Contains(body, "orders_count") || !strings.Contains(body, "graph_mine") {
		t.Errorf("/dev does not list the app's page, tool and refused tool:\n%s", body)
	}
	if !b.Inspect().App.Contributes.DevTools {
		t.Error("inspect does not report DevTools")
	}

	// An error: the panel is up, the app's part is not.
	broken := probeApp()
	broken.DevTools = func(rt *Runtime, dev *Dev) error {
		dev.Page("/orders", "Orders", http.NotFoundHandler())
		return errors.New("the service is not configured")
	}
	b2, err := Build(context.Background(), broken, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if code, _, _ := get(t, b2.Router, "/dev/app/orders"); code != 404 {
		t.Errorf("/dev/app/orders after a failing DevTools = %d", code)
	}
	if code, _, _ := get(t, b2.Router, "/dev"); code != 200 {
		t.Errorf("/dev after a failing DevTools = %d", code)
	}
}

// The app's own dev routes are mounted by the framework, so the reserved-prefix check
// still refuses an app that registers under /dev/app itself.
func TestAppRoutesUnderDevAppAreStillRefused(t *testing.T) {
	isolate(t)
	t.Setenv("APP_ENV", "dev")
	app := App{Routes: func(rt *Runtime, r chi.Router) error {
		r.Get("/dev/app/mine", hello)
		return nil
	}}
	if _, err := Build(context.Background(), app, Options{Dev: true}); err == nil || !strings.Contains(err.Error(), "/dev/app/mine") {
		t.Fatalf("err = %v", err)
	}
}
