package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/govibetest"
)

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// The Hello GoVibe done criteria, against the app booted exactly as `serve` boots it.
func TestHelloGoVibe(t *testing.T) {
	// Production, so the page's files come from what is embedded: in local and dev the
	// framework serves ./assets from disk instead, which would pass with a broken embed.
	t.Setenv("APP_ENV", "prod")
	srv := govibetest.Boot(t, newApp())

	// UI: a templ page with a button that triggers htmx to fetch the greeting (/hello).
	code, body, hdr := get(t, srv.URL+"/")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/html") {
		t.Fatalf("GET / = %d %s", code, hdr.Get("Content-Type"))
	}
	for _, want := range []string{"GoVibe", "Try the htmx demo", `hx-get="/hello"`, `hx-target="#hello-result"`, `src="/_govibe/static/htmx.min.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / lacks %q", want)
		}
	}
	// The dev tools are not mounted under a test, so the page links to none of them.
	if strings.Contains(body, `href="/dev`) {
		t.Error("GET / links to /dev, which is not mounted")
	}

	// API: {"message":"Hello GoVibe!"}.
	code, body, hdr = get(t, srv.URL+"/api/hello")
	if code != 200 || strings.TrimSpace(body) != `{"message":"Hello GoVibe!"}` || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		t.Errorf("GET /api/hello = %d %q %s", code, body, hdr.Get("Content-Type"))
	}

	// The htmx button's target: plain text.
	if code, body, _ = get(t, srv.URL+"/hello"); code != 200 || body != "Hello GoVibe!" {
		t.Errorf("GET /hello = %d %q", code, body)
	}

	// One binary: the page's own files and the framework's htmx are served from what is embedded.
	for _, u := range []string{"/assets/static/theme.css", "/assets/static/home.css", "/assets/static/govibe-logo.png", "/_govibe/static/htmx.min.js"} {
		if code, _, _ := get(t, srv.URL+u); code != 200 {
			t.Errorf("GET %s = %d", u, code)
		}
	}
	// The panel's files and htmx are the framework's: the app's /assets carries none of them.
	for _, u := range []string{"/assets/static/graph.js", "/assets/static/cytoscape.min.js", "/assets/htmx/htmx.min.js"} {
		if code, _, _ := get(t, srv.URL+u); code != 404 {
			t.Errorf("GET %s = %d, want 404", u, code)
		}
	}
	if code, _, _ := get(t, srv.URL+"/dev/graph"); code != 404 {
		t.Errorf("GET /dev/graph = %d without the dev tools", code)
	}
}

// Nothing in the demo opens a database: it works with no DSN, and the test never opens one.
func TestHelloHasNoDatabase(t *testing.T) {
	if srv := govibetest.Boot(t, newApp()); srv.Rt.DB != nil {
		t.Fatal("govibetest opened a database")
	}
}
