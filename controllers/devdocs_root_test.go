package controllers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The docs pages read the project's docs from the root they are given, not from the
// working directory: a process started elsewhere (GOVIBE_ROOT, a subdirectory) still
// lists them.
func TestDocsPagesReadFromTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/public/SHOP_GUIDE.md", "# Shop Guide\n\nHello from the project.\n")
	write("docs/specs/SPEC_CHECKOUT.md", "# SPEC_CHECKOUT\n\n## Description\nx\n")

	r := chi.NewRouter()
	r.Route("/dev/docs", func(d chi.Router) {
		d.Use(DocsRoot(root))
		d.Get("/app", DevDocsAppIndex)
		d.Get("/app/{slug}", DevDocsAppFile)
		d.Get("/specs", DevDocsSpecs)
		d.Get("/specs/{slug}", DevDocsSpecFile)
	})
	t.Chdir(t.TempDir()) // the working directory has no docs at all

	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/dev/docs/app"); code != 200 || !strings.Contains(body, "Shop Guide") {
		t.Fatalf("/dev/docs/app = %d, lacks the project's doc", code)
	}
	if code, body := get("/dev/docs/app/shop-guide"); code != 200 || !strings.Contains(body, "Hello from the project.") {
		t.Fatalf("/dev/docs/app/shop-guide = %d", code)
	}
	if code, body := get("/dev/docs/specs"); code != 200 || !strings.Contains(body, "Checkout") {
		t.Fatalf("/dev/docs/specs = %d, lacks the project's spec", code)
	}
	if code, _ := get("/dev/docs/specs/checkout"); code != 200 {
		t.Fatalf("/dev/docs/specs/checkout = %d", code)
	}

	// Without the middleware: relative to the working directory, as before.
	bare := chi.NewRouter()
	bare.Get("/dev/docs/app", DevDocsAppIndex)
	rec := httptest.NewRecorder()
	bare.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dev/docs/app", nil))
	if strings.Contains(rec.Body.String(), "Shop Guide") {
		t.Fatal("without DocsRoot the pages must not find the project's docs from another directory")
	}
}
