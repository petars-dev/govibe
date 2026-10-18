package generate

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/scaffold"
)

var update = flag.Bool("update", false, "rewrite the golden files from the generators' output")

const (
	fw  = "github.com/petars-dev/govibe"
	mod = "github.com/acme/shop"
)

// app writes a fresh generated app (the scaffold's tree for github.com/acme/shop) into a
// temp dir and loads it as a Project. extra files are written over it.
func app(t *testing.T, extra map[string]string) *Project {
	t.Helper()
	tree, err := scaffold.Render(scaffold.Options{Module: mod, FrameworkModule: fw, FrameworkVersion: "v0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for p, b := range tree {
		fp := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, s := range extra {
		fp := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		if err := os.WriteFile(fp, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// goldenCase is one generator run against the fresh app.
type goldenCase struct {
	name  string
	extra map[string]string
	run   func(p *Project) (*Result, error)
}

var goldenCases = []goldenCase{
	{"page", nil, func(p *Project) (*Result, error) { return Page(p, "about", Options{}) }},
	{"page_path", nil, func(p *Project) (*Result, error) { return Page(p, "privacyPolicy", Options{Path: "/legal/privacy"}) }},
	{"action", nil, func(p *Project) (*Result, error) { return Action(p, "notify", Options{}) }},
	{"service", nil, func(p *Project) (*Result, error) { return Service(p, "billing", Options{}) }},
	{"store", nil, func(p *Project) (*Result, error) { return Store(p, "order", Options{}) }},
	{"migration", nil, func(p *Project) (*Result, error) { return Migration(p, "add_status", Options{}) }},
	{"migration_table", nil, func(p *Project) (*Result, error) { return Migration(p, "create_orders", Options{Table: "orders"}) }},
	{"middleware", nil, func(p *Project) (*Result, error) { return Middleware(p, "rate-limit", Options{}) }},
	{"command", nil, func(p *Project) (*Result, error) { return Command(p, "items:sync", Options{Short: "Sync the items"}) }},
	{"command_scheduled", nil, func(p *Project) (*Result, error) { return Command(p, "items:prune", Options{Schedule: "0 3 * * *"}) }},
	{"command_every", nil, func(p *Project) (*Result, error) {
		return Command(p, "cleanup", Options{Schedule: "@every 10m", Short: "Clean up"})
	}},
	{"config", map[string]string{"env.yml": "shop:\n  greeting: ${GREETING:Hello}\n  page_size: ${PAGE_SIZE:20}\n  smtp:\n    host: ${SMTP_HOST:}\n    tls: ${SMTP_TLS:true}\n"},
		func(p *Project) (*Result, error) { return Config(p, Options{}) }},
	{"config_empty", nil, func(p *Project) (*Result, error) { return Config(p, Options{}) }},
	{"spec", nil, func(p *Project) (*Result, error) { return Spec(p, "payments", Options{}) }},
	{"resource", nil, func(p *Project) (*Result, error) { return Resource(p, "order", Options{}) }},
	{"resource_category", nil, func(p *Project) (*Result, error) { return Resource(p, "categories", Options{}) }},
}

// summary is the part of a result that is not a file: what the tool prints.
func summary(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "kind: %s\ntempl: %v\nfiles:\n", r.Kind, r.Templ)
	for _, p := range r.Paths() {
		kind := "create"
		for _, f := range r.Files {
			if f.Path == p && f.Edit {
				kind = "edit"
			}
		}
		fmt.Fprintf(&b, "  %s %s\n", kind, p)
	}
	for _, s := range r.Snippets {
		fmt.Fprintf(&b, "snippet (%s):\n%s\n", s.Where, s.Text)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	return b.String()
}

// Each generator's output, against a fresh app, is a golden tree under testdata/golden:
// the files it creates and the files it edits (the marker files, the docs), plus a
// summary of its snippets and notes. Regenerate with -update and review the diff.
func TestGenerators(t *testing.T) {
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			p := app(t, tc.extra)
			r, err := tc.run(p)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]byte{"_result.txt": []byte(summary(r))}
			for _, f := range r.Files {
				if _, dup := got[f.Path]; dup {
					t.Errorf("%s is in the result twice", f.Path)
				}
				got[f.Path] = f.Content
			}
			dir := filepath.Join("testdata", "golden", tc.name)
			if *update {
				os.RemoveAll(dir)
				for p, b := range got {
					fp := filepath.Join(dir, filepath.FromSlash(p))
					os.MkdirAll(filepath.Dir(fp), 0o755)
					if err := os.WriteFile(fp, b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			want := map[string][]byte{}
			filepath.WalkDir(dir, func(fp string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, _ := filepath.Rel(dir, fp)
				b, _ := os.ReadFile(fp)
				want[filepath.ToSlash(rel)] = b
				return nil
			})
			if len(want) == 0 {
				t.Fatalf("no golden files under %s: run with -update", dir)
			}
			var paths []string
			for p := range got {
				paths = append(paths, p)
			}
			for p := range want {
				if _, ok := got[p]; !ok {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			for _, p := range paths {
				g, w := got[p], want[p]
				switch {
				case g == nil:
					t.Errorf("%s: in the golden tree, not generated", p)
				case w == nil:
					t.Errorf("%s: generated, not in the golden tree (run with -update)", p)
				case !bytes.Equal(g, w):
					t.Errorf("%s differs from the golden file (run with -update and review the diff):\n%s", p, g)
				}
			}
			// Nothing a generator writes mentions the reference app or a template delimiter.
			for p, b := range got {
				if bytes.Contains(b, []byte("[[")) || bytes.Contains(b, []byte("examples/hello")) {
					t.Errorf("%s has an unrendered delimiter or names the reference app", p)
				}
			}
		})
	}
}

// Without the markers the wiring is not touched: the snippets are returned instead.
func TestWithoutMarkersTheSnippetsArePrinted(t *testing.T) {
	p := app(t, map[string]string{
		"routes/routes.go":  "package routes\n\nimport (\n\t\"github.com/go-chi/chi/v5\"\n\n\t\"github.com/petars-dev/govibe\"\n)\n\nfunc Register(rt *govibe.Runtime, r chi.Router) error { return nil }\n",
		"routes/console.go": "package routes\n\nimport \"github.com/petars-dev/govibe\"\n\nfunc Console(rt *govibe.Runtime) []govibe.Command { return nil }\n",
	})
	r, err := Page(p, "about", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Files {
		if strings.HasPrefix(f.Path, "routes/") {
			t.Errorf("%s was edited without a marker", f.Path)
		}
	}
	if len(r.Snippets) != 1 || !strings.Contains(r.Snippets[0].Text, `r.Get("/about", controllers.About)`) {
		t.Errorf("snippets = %+v", r.Snippets)
	}
	r, err = Command(p, "items:sync", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Snippets) != 1 || !strings.Contains(r.Snippets[0].Text, `Run: commands.SyncItems}`) {
		t.Errorf("snippets = %+v", r.Snippets)
	}
	// Without the doc's heading, the doc line is a snippet too.
	os.WriteFile(filepath.Join(p.Root, "docs", "public", "ARCHITECTURE.md"), []byte("# shop\n\nNothing here.\n"), 0o644)
	r, err = Page(p, "contact", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Snippets) != 2 || !strings.Contains(r.Snippets[1].Text, "`GET /contact`") {
		t.Errorf("snippets = %+v", r.Snippets)
	}
}

func TestGeneratorsRefuseWhatExists(t *testing.T) {
	p := app(t, nil)
	if _, err := Page(p, "home", Options{}); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("a page over the demo's controller: %v", err)
	}
	if r, err := Page(p, "home", Options{Force: true}); err != nil || len(r.Files) < 2 {
		t.Errorf("--force: %v", err)
	}
	for _, bad := range []struct{ kind, name string }{{"page", "9lives"}, {"action", "func"}, {"command", "Items:Sync"}, {"command", "serve"}, {"migration", "x y"}} {
		if _, err := Make(p, bad.kind, bad.name, Options{}); err == nil {
			t.Errorf("%s %q was accepted", bad.kind, bad.name)
		}
	}
	if _, err := Command(p, "items:sync", Options{Schedule: "* * * * * *"}); err == nil || !strings.Contains(err.Error(), "invalid schedule") {
		t.Errorf("a six-field schedule: %v", err)
	}
	if _, err := Page(p, "panel", Options{Path: "/dev/panel"}); err == nil || !strings.Contains(err.Error(), "/dev") {
		t.Errorf("a route under /dev: %v", err)
	}
	if _, err := Make(p, "widget", "x", Options{}); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

// A second generator inserts at the same markers, after the first: the markers stay.
func TestMarkersSurviveAnInsertion(t *testing.T) {
	p := app(t, nil)
	r, err := Resource(p, "order", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Files {
		fp := filepath.Join(p.Root, filepath.FromSlash(f.Path))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, f.Content, 0o644)
	}
	r2, err := Page(p, "about", Options{})
	if err != nil {
		t.Fatal(err)
	}
	var routes string
	for _, f := range r2.Files {
		if f.Path == "routes/routes.go" {
			routes = string(f.Content)
		}
	}
	for _, want := range []string{`r.Get("/api/orders", controllers.ListOrders(orderSvc))`, `r.Get("/about", controllers.About)`, RoutesMarker, `"github.com/acme/shop/storage/orderstore"`} {
		if !strings.Contains(routes, want) {
			t.Errorf("routes.go lacks %q:\n%s", want, routes)
		}
	}
	if i, j := strings.Index(routes, "/api/orders"), strings.Index(routes, "/about"); i > j {
		t.Error("the second route was not inserted after the first")
	}
	// The migration numbering continues.
	if p.nextMigration() != "0003" {
		t.Errorf("next migration = %s", p.nextMigration())
	}
}

func TestNames(t *testing.T) {
	for in, want := range map[string][4]string{
		"order":          {"Order", "order", "order", "order"},
		"rate-limit":     {"RateLimit", "rate_limit", "ratelimit", "rate-limit"},
		"RateLimit":      {"RateLimit", "rate_limit", "ratelimit", "rate-limit"},
		"privacyPolicy":  {"PrivacyPolicy", "privacy_policy", "privacypolicy", "privacy-policy"},
		"HTTPServer":     {"HttpServer", "http_server", "httpserver", "http-server"},
		"add_status_col": {"AddStatusCol", "add_status_col", "addstatuscol", "add-status-col"},
	} {
		n, err := ParseName(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := [4]string{n.Pascal, n.Snake, n.Pkg, n.Kebab}; got != want {
			t.Errorf("%s = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{"order": "orders", "category": "categories", "box": "boxes", "status": "statuses", "day": "days", "dish": "dishes"} {
		if got := Plural(in); got != want {
			t.Errorf("Plural(%s) = %s", in, got)
		}
		if got := Singular(want); got != in {
			t.Errorf("Singular(%s) = %s", want, got)
		}
	}
}

func TestInsertUnderHeading(t *testing.T) {
	md := "# Doc\n\n## Routes\n- `GET /`: home.\n\n## Commands\nNone yet.\n"
	out, ok := insertUnderHeading([]byte(md), "routes", []string{"- `GET /about`: about."})
	if !ok || string(out) != "# Doc\n\n## Routes\n- `GET /`: home.\n- `GET /about`: about.\n\n## Commands\nNone yet.\n" {
		t.Errorf("got %v:\n%s", ok, out)
	}
	out, ok = insertUnderHeading(out, "Commands", []string{"- `x:y`: x."})
	if !ok || !strings.HasSuffix(string(out), "## Commands\nNone yet.\n- `x:y`: x.\n") {
		t.Errorf("got %v:\n%s", ok, out)
	}
	if _, ok := insertUnderHeading(out, "Tables", nil); ok {
		t.Error("a missing heading was found")
	}
}
