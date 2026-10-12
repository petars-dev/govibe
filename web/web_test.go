package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func render(t *testing.T, ctx context.Context, c templ.Component) string {
	t.Helper()
	var b bytes.Buffer
	if err := c.Render(ctx, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// IfDev draws its children only for a request the dev mount marked.
func TestIfDev(t *testing.T) {
	children := templ.WithChildren(context.Background(), templ.Raw(`<a href="/dev/graph">Graph</a>`))
	if got := render(t, children, IfDev()); got != "" {
		t.Errorf("an unmarked request rendered %q", got)
	}

	var seen string
	h := MarkDev(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = render(t, templ.WithChildren(r.Context(), templ.Raw("dev link")), IfDev())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if seen != "dev link" {
		t.Errorf("a marked request rendered %q", seen)
	}
}

func TestIsDevDefaultsToFalse(t *testing.T) {
	if IsDev(context.Background()) {
		t.Fatal("no marker, no dev")
	}
}

func TestHTMXScript(t *testing.T) {
	got := render(t, context.Background(), HTMXScript())
	if got != `<script src="/_govibe/static/htmx.min.js"></script>` {
		t.Fatalf("tag = %q", got)
	}
}

func TestServeHTMX(t *testing.T) {
	rec := httptest.NewRecorder()
	ServeHTMX(rec, httptest.NewRequest("GET", HTMXPath, nil))
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "htmx") || rec.Body.Len() < 10_000 {
		t.Fatalf("not the htmx build: %d bytes", rec.Body.Len())
	}
}
