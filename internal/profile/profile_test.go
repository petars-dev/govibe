package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultClassifiesAGeneratedApp(t *testing.T) {
	p := Default()
	for dir, want := range map[string]string{
		".":                          "wiring",
		"routes":                     "wiring",
		"cmd/app":                    "wiring",
		"controllers":                "controllers",
		"controllers/admin":          "controllers",
		"business/actions":           "business",
		"business/services/orders":   "business",
		"storage/orderstore":         "storage",
		"storage/migrations":         "storage",
		"middleware":                 "middleware",
		"commands":                   "commands",
		"assets":                     "views",
		"config":                     "support",
		"internal/anything":          "support",
		"controllersextra":           "support", // a prefix of the name is not a directory prefix
		"examples/hello/controllers": "support",
		"web":                        "support",
	} {
		if got := p.Classify(dir); got != want {
			t.Errorf("Classify(%q) = %s, want %s", dir, got, want)
		}
	}
}

func TestRootEntryClaimsOnlyTheRootPackage(t *testing.T) {
	p := Default()
	if got := p.Classify("."); got != "wiring" {
		t.Errorf("root = %s", got)
	}
	if got := p.Classify("tools"); got != "support" {
		t.Errorf("tools = %s: the '.' entry must not claim every directory", got)
	}
}

func TestTheMostSpecificDirectoryWins(t *testing.T) {
	p := Default()
	if err := p.Merge([]byte(`
layout:
  controllers: [internal/http]
  business: [internal/core]
  dev: [internal/http/debug]
`)); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		"internal/http":         "controllers",
		"internal/http/orders":  "controllers",
		"internal/http/debug":   "dev",
		"internal/http/debug/x": "dev",
		"internal/core/orders":  "business",
		"controllers":           "support", // the layer's default entry was replaced
		"storage/orderstore":    "storage", // a layer that was not mentioned keeps its default
	} {
		if got := p.Classify(dir); got != want {
			t.Errorf("Classify(%q) = %s, want %s", dir, got, want)
		}
	}
}

func TestLoadWithoutAFileIsTheDefault(t *testing.T) {
	p, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "" || p.Config[0] != "env.yml" || p.Migrations != "storage/migrations/sql" {
		t.Errorf("not the default: %+v", p)
	}
}

func TestLoadOverridesOnlyWhatTheFileSays(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, `
# comments are fine
config: [config/defaults.yml, env.yml]
migrations: db/migrations
docs:
  dirs: [doc]
rules:
  code.unreferenced: off
  config.unused: warning
graph:
  derived: [docs/graph/*.bolt, build/*.out]
`)
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != File {
		t.Errorf("source = %q", p.Source)
	}
	if got := strings.Join(p.Config, ","); got != "config/defaults.yml,env.yml" {
		t.Errorf("config = %s", got)
	}
	if p.Migrations != "db/migrations" || strings.Join(p.Docs.Dirs, ",") != "doc" {
		t.Errorf("migrations/docs not overridden: %+v", p)
	}
	// what the file did not mention keeps the default, including inside a section it did.
	if p.Docs.Specs != "docs/specs" || p.Docs.SpecPrefix != "SPEC_" || strings.Join(p.Docs.Root, ",") != "README.md,SKILL.md" {
		t.Errorf("docs defaults lost: %+v", p.Docs)
	}
	if p.Graph.Dir != "docs/graph" {
		t.Errorf("graph.dir = %q", p.Graph.Dir)
	}
	if v, ok := p.RuleSetting("code.unreferenced"); !ok || v != RuleOff {
		t.Errorf("rule setting = %q, %v", v, ok)
	}
	if _, ok := p.RuleSetting("layering"); ok {
		t.Error("a rule the file does not mention has a setting")
	}
	if !p.IsDerived("docs/graph/indexes.bolt") || !p.IsDerived("build/a.out") || p.IsDerived("docs/graph/baseline.json") {
		t.Error("IsDerived does not follow graph.derived")
	}
}

func TestAnEmptyOrCommentOnlyFileChangesNothing(t *testing.T) {
	for _, body := range []string{"", "# nothing here\n"} {
		root := t.TempDir()
		write(t, root, File, body)
		p, err := Load(root)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if p.Source != File || p.Config[0] != "env.yml" {
			t.Errorf("%q: %+v", body, p)
		}
	}
}

func TestInvalidFilesAreErrorsThatNameTheKey(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"unknown key":      {"migration: x\n", "migration"},
		"unknown nested":   {"docs:\n  spec: x\n", "spec"},
		"absolute path":    {"migrations: /etc\n", "absolute"},
		"escapes the root": {"config: [../env.yml]\n", "leaves the project"},
		"backslash":        {"docs:\n  dirs: ['docs\\\\public']\n", "backslash"},
		"unclean path":     {"migrations: ./db\n", "clean"},
		"empty path":       {"config: ['']\n", "empty"},
		"unknown layer":    {"layout:\n  controler: [x]\n", "architecture layer"},
		"layout escapes":   {"layout:\n  storage: ['../x']\n", "leaves the project"},
		"claimed twice":    {"layout:\n  storage: [internal/core]\n  business: [internal/core]\n", "claimed by both"},
		"bad rule value":   {"rules:\n  layering: sometimes\n", "off, error, warning or info"},
		"bad glob":         {"graph:\n  derived: ['[']\n", "glob"},
		"not a mapping":    {"- a\n- b\n", ""},
		"wrong type":       {"config: env.yml\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, File, tc.doc)
			_, err := Load(root)
			if err == nil {
				t.Fatalf("accepted:\n%s", tc.doc)
			}
			if !strings.Contains(err.Error(), File) {
				t.Errorf("error does not name the file: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestOptionalPathsMayBeEmpty(t *testing.T) {
	root := t.TempDir()
	write(t, root, File, "migrations: ''\ndocs:\n  specs: ''\n")
	p, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Migrations != "" || p.Docs.Specs != "" {
		t.Errorf("%+v", p)
	}
	if got := strings.Join(p.ScanDirs(), ","); got != "docs/public" {
		t.Errorf("scan dirs = %s", got)
	}
}

func TestTheProfileNamesItsFramework(t *testing.T) {
	p := Default()
	if p.FrameworkModule() != "github.com/petars-dev/govibe" {
		t.Errorf("default framework = %q", p.FrameworkModule())
	}
	if err := p.Merge([]byte("framework: example.com/stubfw\n")); err != nil || p.FrameworkModule() != "example.com/stubfw" {
		t.Errorf("%v %q", err, p.FrameworkModule())
	}
	for _, bad := range []string{"framework: /abs\n", "framework: 'a b'\n", "framework: x/\n"} {
		q := Default()
		if err := q.Merge([]byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestFrameworkModuleIsDerivedFromThePackagePath(t *testing.T) {
	if got := FrameworkModule(); got != "github.com/petars-dev/govibe" {
		t.Errorf("FrameworkModule() = %q", got)
	}
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A key with nothing under it is "not set", never "set to nothing": a scaffold's commented-out
// examples must not wipe the layout (every package would become `support`).
func TestAKeyWithNoValueKeepsTheDefault(t *testing.T) {
	for name, doc := range map[string]string{
		"bare layout":        "layout:\n  # controllers: [internal/http]\n",
		"bare layout, text":  "layout:\n",
		"null layout":        "layout: ~\n",
		"bare config":        "config:\n",
		"bare rules":         "rules:\n  # code.unreferenced: off\n",
		"bare docs":          "docs:\n  # dirs: [doc]\n",
		"null inside a docs": "docs:\n  dirs:\n  specs: doc/specs\n",
		"bare layer entry":   "layout:\n  controllers:\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := Default()
			if err := p.Merge([]byte(doc)); err != nil {
				t.Fatal(err)
			}
			if got := p.Classify("controllers"); got != "controllers" || p.Classify("business/actions") != "business" || p.Classify(".") != "wiring" {
				t.Errorf("the layout was wiped: controllers is %q", got)
			}
			if len(p.Config) != 1 || p.Config[0] != "env.yml" || len(p.Docs.Dirs) != 1 || p.Docs.Dirs[0] != "docs/public" {
				t.Errorf("a default was emptied: %+v", p)
			}
		})
	}
	// and what is set still takes effect beside a bare key
	p := Default()
	if err := p.Merge([]byte("layout:\n  controllers:\n  storage: [internal/store]\nrules:\n")); err != nil || p.Classify("internal/store/x") != "storage" || p.Classify("controllers") != "controllers" {
		t.Errorf("%v", err)
	}
}
