package frameworkdocs

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTheEmbeddedSetIsTheAllowlistAndNothingElse(t *testing.T) {
	entries, err := fs.ReadDir(embedded, "docs")
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{}
	for _, f := range Files() {
		allow[f] = true
	}
	for _, e := range entries {
		if !allow[e.Name()] {
			t.Errorf("docs/%s is embedded but not on the allowlist", e.Name())
		}
		// The framework's development specs are process artifacts of the framework repository:
		// they are never shipped, to an app or anywhere else.
		if strings.HasPrefix(strings.ToUpper(e.Name()), "SPEC_") {
			t.Errorf("a spec is embedded: %s", e.Name())
		}
	}
	for _, d := range Embedded().List() {
		b, err := Embedded().Read(d.Slug)
		if err != nil {
			t.Fatal(err)
		}
		if regexp.MustCompile(`(?m)^# SPEC_`).Match([]byte(b)) {
			t.Errorf("doc %s is a spec", d.Slug)
		}
	}
	for _, f := range Files() {
		if strings.HasPrefix(strings.ToUpper(f), "SPEC_") {
			t.Errorf("the allowlist names a spec: %s", f)
		}
	}
}

func TestEveryTitleCarriesThePrefixAndEverySlugIsItsOwn(t *testing.T) {
	seen := map[string]bool{}
	slug := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	docs := Embedded().List()
	if len(docs) < 7 {
		t.Fatalf("only %d docs: %v", len(docs), docs)
	}
	for _, d := range docs {
		if !strings.HasPrefix(d.Title, "GoVibe: ") {
			t.Errorf("%s: title %q has no prefix", d.Slug, d.Title)
		}
		if !slug.MatchString(d.Slug) || seen[d.Slug] {
			t.Errorf("slug %q is not a unique kebab-case name", d.Slug)
		}
		seen[d.Slug] = true
	}
	for _, want := range []string{"guide", "configuration", "architecture", "graph-schema", "dev-db-explorer", "skill", "upgrading"} {
		if !seen[want] {
			t.Errorf("the set lacks %s", want)
		}
	}
}

func TestTheRulesDocIsRenderedFromTheTables(t *testing.T) {
	text, err := Embedded().Read("skill")
	if err != nil || !strings.Contains(text, "# Rules of this project") || !strings.Contains(text, "business: nothing") {
		t.Errorf("%v\n%s", err, text)
	}
}

func TestAnUnknownDocIsAnErrorThatListsTheRealOnes(t *testing.T) {
	_, err := Embedded().Read("spec-framework-extraction")
	if err == nil || !strings.Contains(err.Error(), "guide") {
		t.Errorf("%v", err)
	}
}

// The embedded copies are generated from docs/public: a stale copy would ship an old doc.
func TestEmbeddedCopiesAreCurrent(t *testing.T) {
	src := filepath.Join("..", "..", "docs", "public")
	if _, err := os.Stat(src); err != nil {
		t.Skip("not inside the framework repository")
	}
	for _, f := range Files() {
		want, err := os.ReadFile(filepath.Join(src, f))
		if os.IsNotExist(err) {
			if _, e := embedded.ReadFile("docs/" + f); e == nil {
				t.Errorf("docs/%s is embedded but its source is gone: run go generate ./internal/frameworkdocs", f)
			}
			continue
		}
		want, err = Shipped(want)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		got, err := embedded.ReadFile("docs/" + f)
		if err != nil || string(got) != string(want) {
			t.Errorf("the embedded %s is stale: run `go generate ./internal/frameworkdocs`", f)
		}
	}
}

func TestFromDiskServesACheckoutAndSkipsWhatItLacks(t *testing.T) {
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "docs", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "docs", "public", "GUIDE.md"), []byte("# Edited guide\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := FromDisk(checkout)
	got := s.List()
	var slugs []string
	for _, d := range got {
		slugs = append(slugs, d.Slug)
	}
	// the guide from disk, and the rules doc, which is rendered and needs no file
	if strings.Join(slugs, ",") != "guide,skill" {
		t.Errorf("slugs = %v", slugs)
	}
	if text, _ := s.Read("guide"); text != "# Edited guide\n" {
		t.Errorf("guide = %q", text)
	}
	if _, err := s.Read("configuration"); err == nil {
		t.Error("a doc the checkout lacks is an error")
	}
}

func TestContributorSpecsListTheCheckoutsSpecs(t *testing.T) {
	checkout := t.TempDir()
	specs := filepath.Join(checkout, "docs", "specs")
	if err := os.MkdirAll(specs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"SPEC_GRAPH_INDEXER.md": "# SPEC_GRAPH_INDEXER\n", "SPEC_FRAMEWORK_EXTRACTION.md": "# x\n", "NOTES.md": "not a spec"} {
		if err := os.WriteFile(filepath.Join(specs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := ContributorSpecs(checkout)
	list := s.List()
	if len(list) != 2 || list[0].Slug != "framework-extraction" || list[0].Title != "Framework Extraction" || list[1].Slug != "graph-indexer" {
		t.Errorf("specs = %+v", list)
	}
	if text, err := s.Read("graph-indexer"); err != nil || !strings.HasPrefix(text, "# SPEC_GRAPH_INDEXER") {
		t.Errorf("%q %v", text, err)
	}
	if _, err := s.Read("notes"); err == nil {
		t.Error("only SPEC_ files are specs")
	}
	// and a checkout with no specs directory lists none
	if got := ContributorSpecs(t.TempDir()).List(); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

func TestFrameworkOnlyBlocksAreNotShipped(t *testing.T) {
	in := "# Doc\n\nkeep one\n\n<!-- framework-only:start -->\n## Fixture\nthe orders fixture\n<!-- framework-only:end -->\n\n## After\nkeep two\n"
	got, err := Shipped([]byte(in))
	if err != nil || string(got) != "# Doc\n\nkeep one\n## After\nkeep two\n" {
		t.Errorf("%q %v", got, err)
	}
	for _, bad := range []string{"a\n<!-- framework-only:start -->\nb\n", "a\n<!-- framework-only:end -->\n"} {
		if _, err := Shipped([]byte(bad)); err == nil {
			t.Errorf("accepted an unbalanced marker: %q", bad)
		}
	}
	// two blocks, and a doc with none
	if got, _ := Shipped([]byte("a\n<!-- framework-only:start -->x<!-- framework-only:end -->\nb\n<!-- framework-only:start -->y<!-- framework-only:end -->\nc\n")); string(got) != "a\nb\nc\n" {
		t.Errorf("%q", got)
	}
	if got, _ := Shipped([]byte("plain\n")); string(got) != "plain\n" {
		t.Errorf("%q", got)
	}
}

// What an application is served names none of the framework's test fixtures or their domain.
func TestNoShippedDocNamesTheFrameworksFixtures(t *testing.T) {
	for _, d := range Embedded().List() {
		text, err := Embedded().Read(d.Slug)
		if err != nil {
			t.Fatal(err)
		}
		for _, banned := range []string{"testdata/fixture", "testdata/stubfw", "orderstore", "order_items", "ListOrders", "stubfw", "fixture_layout"} {
			if strings.Contains(text, banned) {
				t.Errorf("%s (served to every app) mentions %q", d.Slug, banned)
			}
		}
	}
}
