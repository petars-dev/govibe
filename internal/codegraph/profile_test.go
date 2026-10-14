package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/quad"
)

// layoutRoot is a project that does not use the default layout: its govibe.yml says
// where everything is.
const layoutRoot = "testdata/fixture_layout"

// indexRoot runs a full index of root into a fresh store and returns the indexer, the
// report and the model it wrote.
func indexRoot(t *testing.T, root string) (*Indexer, *Report, *Model) {
	t.Helper()
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: root})
	rep, err := ix.Run(context.Background())
	if err != nil {
		t.Fatalf("index %s: %v", root, err)
	}
	m, err := ix.Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ix, rep, m
}

// findingsOf lists "rule subject" for every finding, or only those of one rule.
func findingsOf(m *Model, rule string) []string {
	var out []string
	for _, f := range m.Kind("finding") {
		if rule == "" || f.Str("rule") == rule {
			out = append(out, f.Str("rule")+" "+f.first("about"))
		}
	}
	return out
}

func TestProfileLayoutClassifiesPackages(t *testing.T) {
	facts := facts{quads: buildCode(t, layoutRoot).quads}
	for pkg, want := range map[string]string{
		"pkg:.":                         "wiring",
		"pkg:internal/app":              "wiring",
		"pkg:internal/http/handlers":    "controllers",
		"pkg:internal/core/orders":      "business",
		"pkg:internal/store/orderstore": "storage",
	} {
		if got := facts.one(t, pkg, "arch_layer"); got != quad.Value(quad.String(want)) {
			t.Errorf("%s arch_layer = %v, want %s", pkg, got, want)
		}
	}
}

func TestProfileLayoutDrivesEveryLayer(t *testing.T) {
	_, rep, m := indexRoot(t, layoutRoot)
	if rep.Failed() {
		t.Fatalf("a layer failed: %+v", rep.Layers)
	}

	t.Run("settings come from the files the profile lists, the later winning", func(t *testing.T) {
		cur := m.Node("cfg:shop.currency")
		if cur == nil || cur.Str("path") != "config/settings.yml" || cur.Str("default") != "BGN" {
			t.Errorf("shop.currency = %+v: the later file must win", cur)
		}
		if n := m.Node("cfg:shop.region"); n == nil || !n.Bool("declared") {
			t.Errorf("shop.region = %+v", n)
		}
		if n := m.Node("cfg:shop.limit"); n == nil || n.Bool("declared") {
			t.Errorf("shop.limit should be read and not declared: %+v", n)
		}
	})

	t.Run("migrations come from the directory the profile names", func(t *testing.T) {
		for _, id := range []string{"table:orders", "table:audit_log"} {
			if m.Node(id) == nil {
				t.Errorf("%s is missing", id)
			}
		}
		if mg := m.Node("mig:00001_orders"); mg == nil || mg.Str("path") != "db/migrations/00001_orders.sql" {
			t.Errorf("migration = %+v", mg)
		}
	})

	t.Run("docs and specs come from the directories and prefix the profile names", func(t *testing.T) {
		if m.Node("spec:orders") == nil {
			t.Error("doc/specs/SP_ORDERS.md should be spec:orders")
		}
		for _, id := range []string{"doc:README.md", "doc:doc/ARCHITECTURE.md", "doc:doc/specs/SPEC_NOT_A_SPEC_HERE.md"} {
			if n := m.Node(id); n == nil || n.Kind != "doc" {
				t.Errorf("%s should be a doc: %+v", id, n)
			}
		}
		if m.Node("spec:not-a-spec-here") != nil {
			t.Error("a file with the framework's prefix is not a spec under SP_")
		}
	})

	t.Run("findings follow the layout", func(t *testing.T) {
		wantFinding := func(rule, subject string) {
			t.Helper()
			for _, f := range findingsOf(m, rule) {
				if f == rule+" "+subject {
					return
				}
			}
			t.Errorf("no %s finding about %s; have %q", rule, subject, findingsOf(m, ""))
		}
		wantFinding("layering", "pkg:internal/http/handlers")
		wantFinding("config.undeclared", "cfg:shop.limit")
		wantFinding("db.no_primary_key", "table:audit_log")
		wantFinding("docs.stale_ref", "doc:doc/ARCHITECTURE.md")
		wantFinding("docs.spec_missing", "item:orders/exports")
	})

	t.Run("a path the profile calls derived is not stale for being absent", func(t *testing.T) {
		var stale []string
		for _, u := range unresolvedRefs(m.Node("doc:doc/ARCHITECTURE.md")) {
			stale = append(stale, u.token)
		}
		if len(stale) != 1 || stale[0] != "internal/core/gone.go" {
			t.Errorf("stale references = %q, want only internal/core/gone.go (build/report.out is derived)", stale)
		}
	})

	t.Run("a rule the profile switches off is a choice, not a gap", func(t *testing.T) {
		var found bool
		for _, r := range m.ruleInfos(nil) {
			if r.Name != "config.unused" {
				continue
			}
			found = true
			if r.Checked || !r.Off || !strings.Contains(r.Note, "govibe.yml") {
				t.Errorf("config.unused = %+v", r)
			}
		}
		if !found {
			t.Fatal("no config.unused rule node")
		}
		if got := findingsOf(m, "config.unused"); len(got) != 0 {
			t.Errorf("a rule that is off found %q", got)
		}
		gate, err := m.Gate(GateOptions{FailOn: "info"})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range gate.NotRun {
			if r.Name == "config.unused" {
				t.Error("a rule that is off must not be listed among the rules that did not run")
			}
		}
		if st := m.Status(nil); len(st.Findings.NotRun) > 0 {
			for _, n := range st.Findings.NotRun {
				if n == "config.unused" {
					t.Error("the banner lists a rule that is off as not run")
				}
			}
		}
	})

	t.Run("a rule's severity can be changed", func(t *testing.T) {
		got := findingsOf(m, "code.unreferenced")
		if len(got) == 0 {
			t.Fatal("the unused exported function was not found")
		}
		for _, f := range m.Kind("finding") {
			if f.Str("rule") == "code.unreferenced" && f.Str("severity") != "warning" {
				t.Errorf("%s has severity %q, want warning (govibe.yml)", f.ID, f.Str("severity"))
			}
		}
	})
}

func TestADifferentProfileGivesADifferentGraph(t *testing.T) {
	// The same sources under the default profile: nothing sits where it expects to,
	// so every package is `support`, there are no settings, no migrations, no specs.
	root := copyTree(t, layoutRoot)
	if err := os.Remove(filepath.Join(root, "govibe.yml")); err != nil {
		t.Fatal(err)
	}
	_, rep, m := indexRoot(t, root)
	for _, id := range []string{"pkg:internal/http/handlers", "pkg:internal/core/orders", "pkg:internal/store/orderstore"} {
		if got := m.Node(id).Str("arch_layer"); got != "support" {
			t.Errorf("%s = %s under the default profile", id, got)
		}
	}
	if got := findingsOf(m, "layering"); len(got) != 0 {
		t.Errorf("no layers, no layering findings; got %q", got)
	}
	if len(m.Kind("table")) != 0 {
		t.Error("the default profile looks for migrations elsewhere: there must be no tables")
	}
	if m.Node("spec:orders") != nil {
		t.Error("SP_ORDERS.md is not a spec under the default prefix")
	}
	cfg := layerReport(rep, "config")
	if cfg.Status != statusSkipped || !strings.Contains(cfg.Note, "env.yml") {
		t.Errorf("config layer = %s %q: with no settings file it has not looked, and must say so", cfg.Status, cfg.Note)
	}
}

func TestNoSettingsFileIsNotAnEmptyConfig(t *testing.T) {
	root := copyTree(t, layoutRoot)
	if err := os.RemoveAll(filepath.Join(root, "config")); err != nil {
		t.Fatal(err)
	}
	_, rep, m := indexRoot(t, root)
	cfg := layerReport(rep, "config")
	if cfg.Status != statusSkipped || !strings.Contains(cfg.Note, "config/base.yml") || !strings.Contains(cfg.Note, "govibe.yml") {
		t.Errorf("config layer = %s %q", cfg.Status, cfg.Note)
	}
	for _, r := range m.ruleInfos(nil) {
		if r.Name == "config.undeclared" && r.Checked {
			t.Error("config.undeclared claims to have looked at settings that were not there")
		}
	}
}

func TestAnInvalidProfileFailsTheRunWithItsName(t *testing.T) {
	for name, tc := range map[string]struct{ doc, want string }{
		"unknown key":   {"migration: x\n", "migration"},
		"unknown layer": {"layout:\n  controler: [x]\n", "architecture layer"},
		"escapes":       {"migrations: ../outside\n", "leaves the project"},
	} {
		t.Run(name, func(t *testing.T) {
			root := copyTree(t, layoutRoot)
			writeFile(t, root, "govibe.yml", tc.doc)
			h, _ := openStore(t)
			_, err := New(h.QuadStore, Options{Root: root}).Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), "govibe.yml") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one naming govibe.yml and %q", err, tc.want)
			}
		})
	}
}

func TestARuleTheProfileInventsFailsTheFindingsLayer(t *testing.T) {
	root := copyTree(t, layoutRoot)
	writeFile(t, root, "govibe.yml", "rules:\n  layerng: off\n")
	_, rep, _ := indexRoot(t, root)
	fl := layerReport(rep, "findings")
	if fl.Status != statusError || !strings.Contains(fl.Error, "layerng") || !strings.Contains(fl.Error, "layering") {
		t.Errorf("findings layer = %s %q: a typo'd rule must fail loudly and list the real names", fl.Status, fl.Error)
	}
}

func TestEditingTheProfileMakesLayersStale(t *testing.T) {
	root := copyTree(t, layoutRoot)
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: root})
	ctx := context.Background()
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if stale, err := ix.Stale(ctx); err != nil || len(stale) != 0 {
		t.Fatalf("fresh index is stale: %v %v", stale, err)
	}

	// Move controllers somewhere else; the mtime is pushed forward so the edit is seen
	// even on a file system with coarse timestamps.
	writeFile(t, root, "govibe.yml", "layout:\n  controllers: [internal/core]\n")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root, "govibe.yml"), later, later); err != nil {
		t.Fatal(err)
	}
	stale, err := ix.Stale(ctx)
	if err != nil || len(stale) == 0 {
		t.Fatalf("an edit to govibe.yml left every layer fresh: %v %v", stale, err)
	}
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := ix.Model(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Node("pkg:internal/core/orders").Str("arch_layer"); got != "controllers" {
		t.Errorf("after the edit internal/core/orders is %s, want controllers", got)
	}
}

func TestAProfileHandedInWinsOverTheFile(t *testing.T) {
	p, err := loadProfileForTest(layoutRoot)
	if err != nil {
		t.Fatal(err)
	}
	p.Layout["business"] = []string{"internal/none"}
	p.Layout["storage"] = []string{"internal/core"}
	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: layoutRoot, Profile: &p})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := ix.Model(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Node("pkg:internal/core/orders").Str("arch_layer"); got != "storage" {
		t.Errorf("a profile passed in Options was ignored: %s", got)
	}
}
