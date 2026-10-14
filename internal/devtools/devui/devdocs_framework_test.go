package devui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe/internal/frameworkdocs"
)

// docsRouter mounts the docs pages the way devtools.Mount does.
func docsRouter(d Docs, contributor bool) http.Handler {
	r := chi.NewRouter()
	r.Route("/dev/docs", func(dd chi.Router) {
		dd.Use(DocsFor(d))
		dd.Get("/", DevDocsIndex)
		dd.Get("/app", DevDocsAppIndex)
		dd.Get("/app/{slug}", DevDocsAppFile)
		dd.Get("/specs", DevDocsSpecs)
		dd.Get("/specs/{slug}", DevDocsSpecFile)
		dd.Get("/govibe", DevDocsFrameworkIndex)
		dd.Get("/govibe/{slug}", DevDocsFrameworkFile)
		if contributor {
			dd.Get("/govibe-specs", DevDocsFrameworkSpecsIndex)
			dd.Get("/govibe-specs/{slug}", DevDocsFrameworkSpecFile)
		}
	})
	return r
}

func fetch(h http.Handler, path string) (int, string, http.Header) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String(), rec.Header()
}

func writeAt(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An app with a few docs of its own.
func appRoot(t *testing.T) string {
	root := t.TempDir()
	writeAt(t, root, "docs/public/SHOP_GUIDE.md", "# Shop Guide\n\nOur own docs.\n")
	writeAt(t, root, "docs/specs/SPEC_CHECKOUT.md", "# SPEC_CHECKOUT\n\n## Description\nx\n")
	return root
}

func TestDevDocsOpensOnTheAppsOwnDocs(t *testing.T) {
	h := docsRouter(Docs{Root: appRoot(t), Framework: frameworkdocs.Embedded()}, false)
	code, _, hdr := fetch(h, "/dev/docs")
	if code != http.StatusFound || hdr.Get("Location") != "/dev/docs/app" {
		t.Fatalf("/dev/docs = %d -> %q, want a redirect to /dev/docs/app", code, hdr.Get("Location"))
	}
	if code, _, hdr := fetch(h, "/dev/docs/"); code != http.StatusFound || hdr.Get("Location") != "/dev/docs/app" {
		t.Errorf("/dev/docs/ = %d -> %q", code, hdr.Get("Location"))
	}

	// the app's own docs are first, the framework's follow in a group of their own, prefixed
	_, body, _ := fetch(h, "/dev/docs/app")
	own, fw := strings.Index(body, "Shop Guide"), strings.Index(body, "GoVibe docs (the framework)")
	if own < 0 || fw < 0 || own > fw {
		t.Fatalf("the app's docs must come first (own at %d, framework group at %d)", own, fw)
	}
	for _, want := range []string{`href="/dev/docs/govibe/guide"`, "GoVibe: Guide", "GoVibe: Configuration", "GoVibe: Graph Schema", "GoVibe: Upgrading", "GoVibe: Rules"} {
		if !strings.Contains(body, want) {
			t.Errorf("the app index lacks %q", want)
		}
	}
	// no framework doc is mixed into the app's list: its links are under the framework's namespace
	if strings.Contains(body, `href="/dev/docs/app/guide"`) || strings.Contains(body, `href="/dev/docs/app/graph-schema"`) {
		t.Error("a framework doc is listed among the app's own")
	}
	// the sidebar of an app page has the muted framework group after the app's docs
	_, page, _ := fetch(h, "/dev/docs/app/shop-guide")
	if a, b := strings.Index(page, "Shop Guide</a>"), strings.Index(page, "sidebar-group sidebar-muted"); a < 0 || b < 0 || a > b {
		t.Errorf("the sidebar lists the app's docs, then the muted framework group (%d, %d)", a, b)
	}
	if !strings.Contains(page, `class="tab tab-muted"`) {
		t.Error("the framework's docs have a muted tab")
	}
}

func TestTheFrameworkDocsAreAGroupOfTheirOwn(t *testing.T) {
	h := docsRouter(Docs{Root: appRoot(t), Framework: frameworkdocs.Embedded()}, false)
	code, body, _ := fetch(h, "/dev/docs/govibe")
	if code != 200 || !strings.Contains(body, "GoVibe: Guide") || !strings.Contains(body, `href="/dev/docs/govibe/configuration"`) {
		t.Fatalf("/dev/docs/govibe = %d", code)
	}
	if strings.Contains(body, "Shop Guide") {
		t.Error("the app's docs are in the framework's index")
	}
	code, body, _ = fetch(h, "/dev/docs/govibe/configuration")
	if code != 200 || !strings.Contains(body, "<title>GoVibe: Configuration</title>") || !strings.Contains(body, "defaults.yml") {
		t.Errorf("/dev/docs/govibe/configuration = %d", code)
	}
	// the rules are rendered from the tables
	if code, body, _ := fetch(h, "/dev/docs/govibe/skill"); code != 200 || !strings.Contains(body, "business: nothing") {
		t.Errorf("/dev/docs/govibe/skill = %d", code)
	}
	if code, _, _ := fetch(h, "/dev/docs/govibe/no-such-doc"); code != 404 {
		t.Errorf("an unknown framework doc = %d", code)
	}
	// a slug of the app's cannot reach into the framework's namespace, nor the other way round
	if code, _, _ := fetch(h, "/dev/docs/govibe/shop-guide"); code != 404 {
		t.Errorf("the app's doc under the framework's namespace = %d", code)
	}
	if code, _, _ := fetch(h, "/dev/docs/app/guide"); code != 404 {
		t.Errorf("the framework's guide under the app's namespace = %d", code)
	}
}

// Acceptance A10: no SPEC_* of the framework is reachable in an app, by test.
func TestNoFrameworkSpecIsReachableInAnApp(t *testing.T) {
	root := appRoot(t)
	set := frameworkdocs.Embedded()
	h := docsRouter(Docs{Root: root, Framework: set}, false)
	specs, _ := filepath.Glob("../../../docs/specs/SPEC_*.md")
	if len(specs) == 0 {
		t.Fatal("this repository has specs to look for")
	}
	isDoc := map[string]bool{} // the name of a spec may be the name of a doc (SPEC_DEV_DB_EXPLORER, DEV_DB_EXPLORER)
	for _, slug := range frameworkdocs.Slugs() {
		isDoc[slug] = true
	}
	for _, sp := range specs {
		name := strings.TrimSuffix(filepath.Base(sp), ".md")
		slug := strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(name, "SPEC_"), "_", "-"))
		paths := []string{"/dev/docs/specs/" + slug, "/dev/docs/app/" + slug, "/dev/docs/govibe-specs/" + slug,
			"/dev/docs/govibe/" + strings.ToLower(name), "/dev/docs/govibe/" + strings.ToLower(strings.ReplaceAll(name, "_", "-"))}
		if !isDoc[slug] {
			paths = append(paths, "/dev/docs/govibe/"+slug)
		}
		for _, path := range paths {
			if code, _, _ := fetch(h, path); code != 404 {
				t.Errorf("%s = %d: a spec of the framework is reachable in an app", path, code)
			}
		}

		// and none of the docs that are served is the spec's text: its description's first line is its fingerprint
		text, _ := os.ReadFile(sp)
		needle := ""
		for _, l := range strings.Split(strings.SplitN(string(text), "## Description", 2)[1], "\n") {
			if l = strings.TrimSpace(l); len(l) > 40 {
				needle = l[:40]
				break
			}
		}
		if needle == "" {
			t.Fatalf("%s has no description to fingerprint", name)
		}
		for _, d := range set.List() {
			if body, _ := set.Read(d.Slug); strings.Contains(body, needle) {
				t.Errorf("the framework doc %s contains the text of %s", d.Slug, name)
			}
		}
	}
	// the contributor group does not exist without a checkout
	if code, _, _ := fetch(h, "/dev/docs/govibe-specs"); code != 404 {
		t.Errorf("/dev/docs/govibe-specs = %d in an app", code)
	}
	// the app's own spec is still there
	if code, body, _ := fetch(h, "/dev/docs/specs"); code != 200 || !strings.Contains(body, "Checkout") {
		t.Errorf("the app's own spec: %d", code)
	}
}

func TestContributorModeServesTheCheckoutAndItsSpecs(t *testing.T) {
	checkout := t.TempDir()
	writeAt(t, checkout, "docs/public/GUIDE.md", "# Guide from the checkout\n\nfresh edit\n")
	writeAt(t, checkout, "docs/specs/SPEC_FRAMEWORK_EXTRACTION.md", "# SPEC_FRAMEWORK_EXTRACTION\n\n## Description\nthe plan\n")
	h := docsRouter(Docs{Root: appRoot(t), Framework: frameworkdocs.FromDisk(checkout), FrameworkSpecs: frameworkdocs.ContributorSpecs(checkout)}, true)

	if code, body, _ := fetch(h, "/dev/docs/govibe/guide"); code != 200 || !strings.Contains(body, "fresh edit") {
		t.Errorf("the guide from the checkout: %d", code)
	}
	// only the docs the checkout has, plus the rendered rules
	_, index, _ := fetch(h, "/dev/docs/govibe")
	if strings.Contains(index, "Configuration") || !strings.Contains(index, "GoVibe: Rules") {
		t.Errorf("the checkout's index:\n%.600s", index)
	}
	code, body, _ := fetch(h, "/dev/docs/govibe-specs/framework-extraction")
	if code != 200 || !strings.Contains(body, "the plan") {
		t.Fatalf("the contributor spec = %d", code)
	}
	// the group is listed, labelled as for contributors
	_, app, _ := fetch(h, "/dev/docs/app")
	if !strings.Contains(app, "GoVibe specs (contributors)") || !strings.Contains(app, `href="/dev/docs/govibe-specs/framework-extraction"`) {
		t.Error("the contributor group is not listed on the app index")
	}
}

// A profile that says a project has no specs means none: the pages must not fall back to
// docs/specs and list files the graph does not index.
func TestAProfileWithNoSpecsMeansNoSpecs(t *testing.T) {
	root := t.TempDir()
	writeAt(t, root, "docs/public/NOTES.md", "# Notes\n")
	writeAt(t, root, "docs/specs/SPEC_PAY.md", "# SPEC_PAY\n")
	h := docsRouter(Docs{Root: root, Dirs: []string{"docs/public"}, Specs: "", SpecPrefix: "", FromProfile: true}, false)
	if _, body, _ := fetch(h, "/dev/docs/specs"); strings.Contains(body, "Pay") || !strings.Contains(body, "No specs yet") {
		t.Errorf("the specs the profile does not know:\n%.600s", body)
	}
	if code, _, _ := fetch(h, "/dev/docs/specs/pay"); code != 404 {
		t.Errorf("a spec the profile does not know = %d", code)
	}
	// and with no public docs directory, no docs
	none := docsRouter(Docs{Root: root, Dirs: nil, Specs: "docs/specs", SpecPrefix: "SPEC_", FromProfile: true}, false)
	if _, body, _ := fetch(none, "/dev/docs/app"); strings.Contains(body, "Notes") {
		t.Error("a project with no doc directory lists docs/public")
	}
	// the same Docs without the flag falls back to the generated app's layout
	dflt := docsRouter(Docs{Root: root}, false)
	if _, body, _ := fetch(dflt, "/dev/docs/specs"); !strings.Contains(body, "Pay") {
		t.Error("the default layout lists docs/specs")
	}
}

func TestDocsFollowTheProfilesDirectories(t *testing.T) {
	root := t.TempDir()
	writeAt(t, root, "doc/NOTES.md", "# Notes\n")
	writeAt(t, root, "doc/specs/SP_PAY.md", "# SP_PAY\n\n## Description\nx\n")
	writeAt(t, root, "docs/public/IGNORED.md", "# Ignored\n")
	h := docsRouter(Docs{Root: root, Dirs: []string{"doc"}, Specs: "doc/specs", SpecPrefix: "SP_"}, false)
	if _, body, _ := fetch(h, "/dev/docs/app"); !strings.Contains(body, "Notes") || strings.Contains(body, "Ignored") {
		t.Errorf("the docs come from the profile's directory:\n%.500s", body)
	}
	if code, body, _ := fetch(h, "/dev/docs/specs/pay"); code != 200 || !strings.Contains(body, "<h1") || strings.Contains(body, "SP_PAY") {
		t.Errorf("the spec under the profile's prefix: %d", code)
	}
	// an app with no docs says so, and still offers no framework group when there is none
	empty := docsRouter(Docs{Root: t.TempDir()}, false)
	if _, body, _ := fetch(empty, "/dev/docs/app"); !strings.Contains(body, "no docs yet") || strings.Contains(body, "GoVibe docs") {
		t.Errorf("an app with no docs:\n%.500s", body)
	}
}
