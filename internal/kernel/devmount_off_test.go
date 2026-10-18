//go:build !govibe_dev

package kernel

import (
	"context"
	"testing"
)

// A default build has no dev tools to mount: even in a development environment and
// when asked for, nothing under /dev or of the panel's files exists, no page sees
// the dev marker, and what the framework serves for the app (htmx) still works.
func TestDefaultBuildMountsNoDevTools(t *testing.T) {
	isolate(t)
	t.Setenv("APP_ENV", "dev")
	b, err := Build(context.Background(), probeApp(), Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.DevMounted || mountDev != nil {
		t.Fatalf("a default build mounted the dev tools (mounted=%v)", b.DevMounted)
	}
	if _, body, _ := get(t, b.Router, "/probe"); body != "plain" {
		t.Fatalf("a page must not see the dev marker, got %q", body)
	}
	for _, p := range []string{"/dev/graph", "/dev/docs/app", "/dev/db", "/dev/mcp", "/_govibe/static/graph.js", "/_govibe/static/theme.css"} {
		if code, _, _ := get(t, b.Router, p); code != 404 {
			t.Errorf("%s = %d in a default build", p, code)
		}
	}
	if code, body, _ := get(t, b.Router, "/_govibe/static/htmx.min.js"); code != 200 || len(body) == 0 {
		t.Errorf("htmx = %d", code)
	}
}

// A default build never calls App.DevTools: there is no panel to add to.
func TestDefaultBuildNeverCallsDevTools(t *testing.T) {
	isolate(t)
	t.Setenv("APP_ENV", "dev")
	app := probeApp()
	app.DevTools = func(rt *Runtime, dev *Dev) error {
		t.Error("DevTools was called in a default build")
		return nil
	}
	b, err := Build(context.Background(), app, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if code, _, _ := get(t, b.Router, "/dev/app/anything"); code != 404 {
		t.Errorf("/dev/app/anything = %d", code)
	}
}
