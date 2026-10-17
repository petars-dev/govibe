package scaffold

import (
	"bytes"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/profile"
)

const (
	fw  = "github.com/petars-dev/govibe"
	mod = "github.com/acme/shop"
)

func render(t *testing.T, o Options) Tree {
	t.Helper()
	if o.FrameworkModule == "" {
		o.FrameworkModule = fw
	}
	if o.FrameworkVersion == "" && o.Replace == "" {
		o.FrameworkVersion = "v0.1.0"
	}
	tree, err := Render(o)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestRenderProducesTheGeneratedTree(t *testing.T) {
	tree := render(t, Options{Module: mod})
	for _, want := range []string{
		"main.go", "main_test.go", "prod_test.go", "env.yml", "govibe.yml", "go.mod", "SKILL.md", "README.md", "Makefile", ".air.toml", ".gitignore",
		".github/workflows/govibe.yml", "routes/routes.go", "routes/console.go",
		"controllers/home.go", "controllers/api.go", "controllers/hello.go", "business/actions/hello.go",
		"storage/migrations/embed.go", "storage/migrations/sql/0001_init.sql",
		"assets/embed.go", "assets/templates/home.templ", "assets/templates/home_templ.go", "assets/templates/.gitkeep",
		"assets/static/.gitkeep", "assets/static/home.css", "assets/static/theme.css", "assets/static/theme.js", "assets/static/govibe-logo.png", "assets/static/govibe-hero.png",
		"docs/public/ARCHITECTURE.md", "docs/public/MEMORY_SCHEMA.md", "docs/specs/.gitkeep", "docs/graph/baseline.json",
	} {
		if _, ok := tree[want]; !ok {
			t.Errorf("the tree lacks %s", want)
		}
	}
	for p, b := range tree {
		if strings.HasPrefix(filepath.Base(p), "SPEC_") {
			t.Errorf("a spec is generated: %s", p)
		}
		if strings.HasSuffix(p, ".tmpl") {
			t.Errorf("%s kept the template suffix", p)
		}
		if strings.HasSuffix(p, ".png") {
			continue
		}
		if bytes.Contains(b, []byte(LeftDelim)) || bytes.Contains(b, []byte(RightDelim)) {
			t.Errorf("%s has an unrendered delimiter", p)
		}
		if strings.Contains(string(b), "examples/hello") {
			t.Errorf("%s names the reference app", p)
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_templ.go") {
			if fmted, err := format.Source(b); err != nil || !bytes.Equal(fmted, b) {
				t.Errorf("%s is not gofmt-clean: %v", p, err)
			}
		}
	}
	main := string(tree["main.go"])
	if n := strings.Count(main, "\n"); n >= 30 {
		t.Errorf("main.go has %d lines, want under 30", n)
	}
	for _, want := range []string{`Name:       "shop",`, `"github.com/acme/shop/routes"`, "Commands:   routes.Console,", "//go:embed env.yml"} {
		if !strings.Contains(main, want) {
			t.Errorf("main.go lacks %q:\n%s", want, main)
		}
	}
	if !strings.Contains(string(tree["controllers/home.go"]), `"github.com/acme/shop/assets/templates"`) {
		t.Errorf("home.go imports:\n%s", tree["controllers/home.go"])
	}
	gomod := string(tree["go.mod"])
	for _, want := range []string{"module github.com/acme/shop\n", "go " + GoVersion + "\n", fw + " v0.1.0", TemplModule + " " + TemplVersion, AirModule + " " + AirVersion,
		"tool (", TemplModule + "/cmd/templ", AirModule + "\n", fw + "/cmd/govibe"} {
		if !strings.Contains(gomod, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, gomod)
		}
	}
	if strings.Contains(gomod, "replace") {
		t.Errorf("go.mod has a replace without --replace:\n%s", gomod)
	}
	if env := string(tree["env.yml"]); strings.Contains(env, "\ndatabase:") || strings.Contains(env, "mail") {
		t.Errorf("env.yml of the default variant:\n%s", env)
	}
	for _, f := range []string{"Makefile", ".air.toml", ".github/workflows/govibe.yml"} {
		if !strings.Contains(string(tree[f]), "Managed by GoVibe") {
			t.Errorf("%s has no managed header", f)
		}
	}
	for _, want := range []string{"shop-dev", "APP_NAME := shop"} {
		if !strings.Contains(string(tree["Makefile"])+string(tree[".air.toml"]), want) {
			t.Errorf("the Makefile and .air.toml lack %q", want)
		}
	}
	if !strings.Contains(string(tree[".gitignore"]), "/shop\n") {
		t.Errorf(".gitignore:\n%s", tree[".gitignore"])
	}
}

func TestSkillHasTheManagedBlockAndTheOwnedSeed(t *testing.T) {
	tree := render(t, Options{Module: mod})
	skill := string(tree["SKILL.md"])
	block, err := Block(tree["SKILL.md"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(skill, "# shop: the rules of this GoVibe application\n") {
		t.Errorf("title:\n%s", skill[:80])
	}
	for _, want := range []string{"### Layering", "- business: nothing", "- wiring: govibe, govibe/config, govibe/web", "### Specs", "1. Description", "5. Status Table",
		"`rt.Config.Get(\"shop.key\")`", "`./shop schedule list`"} {
		if !strings.Contains(block, want) {
			t.Errorf("the managed block lacks %q", want)
		}
	}
	owned := skill[strings.Index(skill, BlockEnd)+len(BlockEnd):]
	for _, want := range []string{"## This application", "### Hello GoVibe done criteria", "`GET /api/hello`", "### Starting your own app", "`controllers/home.go`", "`business/actions/hello.go`", "`assets/templates/home.templ`", "`assets/static/home.css`"} {
		if !strings.Contains(owned, want) {
			t.Errorf("the owned section lacks %q:\n%s", want, owned)
		}
	}
	for _, demo := range []string{"Hello GoVibe", "hello.go", "home.templ"} {
		if strings.Contains(block, demo) {
			t.Errorf("the managed block mentions the demo (%q)", demo)
		}
	}
	// What the seed says to delete is exactly the demo's controllers, action, page and files.
	del := DemoDeletable()
	if len(del) < 8 || del[0] != "assets/static/govibe-hero.png" {
		t.Errorf("DemoDeletable = %v", del)
	}
	for _, f := range del {
		if _, ok := tree[f]; !ok {
			t.Errorf("%s is listed as deletable but not generated", f)
		}
		if strings.HasSuffix(f, ".gitkeep") || f == "assets/embed.go" {
			t.Errorf("%s must stay", f)
		}
	}
}

func TestManifestRecordsTheManagedHashes(t *testing.T) {
	tree := render(t, Options{Module: mod})
	var p profile.Profile = profile.Default()
	if err := p.Merge(tree["govibe.yml"]); err != nil {
		t.Fatal(err)
	}
	if p.Managed == nil || p.Managed.Version != "v0.1.0" || len(p.Managed.Files) != len(Managed) {
		t.Fatalf("managed = %+v", p.Managed)
	}
	for _, name := range Managed {
		h, err := Hash(name, tree[name])
		if err != nil {
			t.Fatal(err)
		}
		if p.Managed.Files[name] != h {
			t.Errorf("%s: manifest %s, file %s", name, p.Managed.Files[name], h)
		}
	}
	// The profile is otherwise the default: every other key is a comment.
	p.Managed, p.Source = nil, ""
	if d := profile.Default(); !equalProfiles(p, d) {
		t.Errorf("the generated govibe.yml changes the default profile: %+v", p)
	}
}

func equalProfiles(a, b profile.Profile) bool {
	return a.Framework == b.Framework && strings.Join(a.Config, ",") == strings.Join(b.Config, ",") && a.Migrations == b.Migrations &&
		a.Graph.Dir == b.Graph.Dir && len(a.Layout) == len(b.Layout) && len(a.Rules) == len(b.Rules)
}

func TestCheckManagedReportsEditsAndLosses(t *testing.T) {
	tree := render(t, Options{Module: mod})
	root := t.TempDir()
	write(t, root, tree)
	p, err := profile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := CheckManaged(root, p.Managed); len(d) != 0 {
		t.Fatalf("a fresh app drifts: %v", d)
	}
	if d := CheckManaged(root, nil); d != nil {
		t.Errorf("no manifest, but %v", d)
	}
	// The owned part of SKILL.md is the app's: an edit there is no drift. One inside the block is.
	skill := string(tree["SKILL.md"])
	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(skill+"\n### Domain\nOrders.\n"), 0o644)
	if d := CheckManaged(root, p.Managed); len(d) != 0 {
		t.Errorf("an edit of the owned section drifts: %v", d)
	}
	os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(strings.Replace(skill, "## Stack", "## Our stack", 1)), 0o644)
	os.WriteFile(filepath.Join(root, "Makefile"), append(tree["Makefile"], "\nlint:\n\tgolangci-lint run\n"...), 0o644)
	os.Remove(filepath.Join(root, ".air.toml"))
	d := CheckManaged(root, p.Managed)
	got := map[string]string{}
	for _, x := range d {
		got[x.File] = x.State
	}
	if len(d) != 3 || got["SKILL.md"] != "edited" || got["Makefile"] != "edited" || got[".air.toml"] != "missing" {
		t.Errorf("drift = %v", d)
	}
	// A manifest that forgets a managed file says so.
	delete(p.Managed.Files, "Makefile")
	os.WriteFile(filepath.Join(root, "Makefile"), tree["Makefile"], 0o644)
	found := false
	for _, x := range CheckManaged(root, p.Managed) {
		found = found || (x.File == "Makefile" && x.State == "unlisted")
	}
	if !found {
		t.Error("an unlisted managed file is not reported")
	}
}

func TestTheMariaDBVariantDeclaresTheDatabase(t *testing.T) {
	tree := render(t, Options{Module: mod, DB: DBMariaDB})
	env := string(tree["env.yml"])
	if !strings.Contains(env, "\ndatabase:\n") || !strings.Contains(env, "dsn: ${MARIADB_DSN:}") || !strings.Contains(env, "type: ${DATABASE_TYPE:mysql}") {
		t.Errorf("env.yml:\n%s", env)
	}
}

func TestReplacePointsGoModAtACheckout(t *testing.T) {
	tree := render(t, Options{Module: mod, Replace: "/work/govibe"})
	gomod := string(tree["go.mod"])
	if !strings.Contains(gomod, fw+" v0.0.0\n") || !strings.Contains(gomod, "\nreplace "+fw+" => /work/govibe\n") {
		t.Errorf("go.mod:\n%s", gomod)
	}
	if _, err := Render(Options{Module: mod, FrameworkModule: fw, Replace: "../govibe"}); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("a relative --replace: %v", err)
	}
}

func TestCompleteChecksTheOptions(t *testing.T) {
	for _, tc := range []struct {
		o    Options
		want string
	}{
		{Options{Module: "shop", FrameworkModule: fw, FrameworkVersion: "v0.1.0"}, ""}, // a module path with no dot is valid for a private module
		{Options{Module: "github.com/acme/shop/v2", FrameworkModule: fw, FrameworkVersion: "v0.1.0"}, ""},
		{Options{Module: "github.com/acme/Shop App", FrameworkModule: fw, FrameworkVersion: "v0.1.0"}, "module path"},
		{Options{Module: mod, DB: "postgres", FrameworkModule: fw, FrameworkVersion: "v0.1.0"}, "--db"},
		{Options{Module: mod, FrameworkModule: fw, FrameworkVersion: "(devel)"}, "version"},
		{Options{Module: mod, FrameworkModule: fw}, "version"},
		{Options{Module: mod, Name: "my shop", FrameworkModule: fw, FrameworkVersion: "v0.1.0"}, "app name"},
	} {
		err := tc.o.Complete()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%+v: %v", tc.o, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%+v: %v, want %q", tc.o, err, tc.want)
		}
	}
	for in, want := range map[string]string{"github.com/acme/shop": "shop", "github.com/acme/shop/v2": "shop", "shop": "shop", "example.com/a/b.c": "b.c"} {
		if got := NameOf(in); got != want {
			t.Errorf("NameOf(%s) = %s", in, got)
		}
	}
}

// The demo an app gets is the demo the framework runs and gates: the copy in templates/demo
// is generated from examples/hello, and a stale copy fails here.
func TestTheDemoCopyIsCurrent(t *testing.T) {
	src := filepath.Join("..", "..", "examples", "hello")
	if _, err := os.Stat(filepath.Join(src, "main.go")); err != nil {
		t.Skip("not inside the framework repository")
	}
	want, err := SyncDemo(os.DirFS(src), fw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := EmbeddedDemo()
	if err != nil {
		t.Fatal(err)
	}
	for p, b := range want {
		if g, ok := got[p]; !ok || !bytes.Equal(g, b) {
			t.Errorf("templates/demo/%s is stale or missing: run `go generate ./internal/scaffold`", p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("templates/demo/%s has no source in examples/hello: run `go generate ./internal/scaffold`", p)
		}
	}
}

// The reference app's SKILL.md is rendered the way a generated app's is.
func TestTheExampleSkillIsCurrent(t *testing.T) {
	src := filepath.Join("..", "..", "examples", "hello", "SKILL.md")
	got, err := os.ReadFile(src)
	if err != nil {
		t.Skip("not inside the framework repository")
	}
	o := Options{Module: DemoSource(fw), Name: DemoName, FrameworkModule: fw, FrameworkVersion: "v0.0.0"}
	if err := o.Complete(); err != nil {
		t.Fatal(err)
	}
	want, err := Skill(DataFor(o))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("examples/hello/SKILL.md is stale: run `go generate ./internal/scaffold`")
	}
}

// The versions the scaffold pins are the framework's own.
func TestTheVersionsAreTheFrameworks(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Skip("not inside the framework repository")
	}
	gomod := string(b)
	if !strings.Contains(gomod, "\ngo "+GoVersion+"\n") {
		t.Errorf("GoVersion %s is not the framework's go line", GoVersion)
	}
	if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(TemplModule) + ` ` + regexp.QuoteMeta(TemplVersion) + `$`).MatchString(gomod) {
		t.Errorf("TemplVersion %s is not the framework's", TemplVersion)
	}
}

// write puts a tree on disk.
func write(t *testing.T, root string, tree Tree) {
	t.Helper()
	for p, b := range tree {
		fp := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// goRun runs a go command in dir, outside any workspace.
func goRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("go", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// buildable writes the rendered tree with a go.mod that resolves the framework to this
// checkout at the versions the reference app already has in its go.sum, so the build
// needs no network: what the keystone job does with the real `govibe new`, offline.
func buildable(t *testing.T, tree Tree) string {
	t.Helper()
	checkout, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	example := filepath.Join(checkout, "examples", "hello")
	gomod, err := os.ReadFile(filepath.Join(example, "go.mod"))
	if err != nil {
		t.Skip("not inside the framework repository")
	}
	gosum, err := os.ReadFile(filepath.Join(example, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write(t, root, tree)
	m := strings.Replace(string(gomod), "module "+DemoSource(fw), "module "+mod, 1)
	m = strings.Replace(m, "=> ../..", "=> "+checkout, 1)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte(m), 0o644)
	os.WriteFile(filepath.Join(root, "go.sum"), gosum, 0o644)
	return root
}

// The rendered Go builds and passes its own tests under another module path, both
// ways, and the demo can be deleted as SKILL.md says, with the rest still building.
func TestTheRenderedAppBuildsAndTests(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generated app")
	}
	root := buildable(t, render(t, Options{Module: mod}))
	goRun(t, root, "build", "./...")
	goRun(t, root, "build", "-tags", "govibe_dev", "./...")
	goRun(t, root, "vet", "./...")
	goRun(t, root, "test", "./...")

	// Start your own app: delete the demo, replace the routes and the demo's test.
	for _, f := range DemoDeletable() {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(root, "assets", "templates", "home_templ.go"))
	os.Remove(filepath.Join(root, "main_test.go"))
	routes := "package routes\n\nimport (\n\t\"github.com/go-chi/chi/v5\"\n\n\t\"github.com/petars-dev/govibe\"\n)\n\nfunc Register(rt *govibe.Runtime, r chi.Router) error { return nil }\n"
	os.WriteFile(filepath.Join(root, "routes", "routes.go"), []byte(routes), 0o644)
	goRun(t, root, "build", "./...")
	goRun(t, root, "vet", "./...")
}
