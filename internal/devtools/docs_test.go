package devtools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/config"
)

func TestFrameworkSpecsAreOnlyListedForAContributor(t *testing.T) {
	var cfg config.Config

	d := docsFor(helloRoot, cfg)
	if d.FrameworkSpecs != nil {
		t.Error("the framework's specs are listed with no dev.framework_src")
	}
	if len(d.Framework.List()) < 7 {
		t.Errorf("the embedded framework docs: %v", d.Framework.List())
	}
	if d.Root != helloRoot || strings.Join(d.Dirs, ",") != "docs/public" || d.Specs != "docs/specs" {
		t.Errorf("the app's docs by its profile: %+v", d)
	}

	// pointed at a checkout (this repository), the docs are read from it and its specs listed
	cfg.Dev.FrameworkSrc = "../.."
	d = docsFor(helloRoot, cfg)
	if d.FrameworkSpecs == nil || len(d.FrameworkSpecs.List()) < 3 {
		t.Fatalf("contributor mode lists the specs of the checkout: %v", d.FrameworkSpecs)
	}
	found := false
	for _, s := range d.FrameworkSpecs.List() {
		found = found || s.Slug == "framework-extraction"
	}
	if !found {
		t.Errorf("specs: %v", d.FrameworkSpecs.List())
	}

	// a directory that is not a checkout is refused: the embedded docs, and no specs
	cfg.Dev.FrameworkSrc = t.TempDir()
	if d = docsFor(helloRoot, cfg); d.FrameworkSpecs != nil || len(d.Framework.List()) < 7 {
		t.Errorf("not a checkout: %+v", d)
	}
}

func TestTheDocsFollowTheProjectsProfile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "govibe.yml"), []byte("docs:\n  dirs: [doc]\n  specs: doc/specs\n  spec_prefix: SP_\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := docsFor(root, config.Config{})
	if strings.Join(d.Dirs, ",") != "doc" || d.Specs != "doc/specs" || d.SpecPrefix != "SP_" {
		t.Errorf("%+v", d)
	}
	// a govibe.yml that does not parse falls back to the default places (and says so in the log)
	if err := os.WriteFile(filepath.Join(root, "govibe.yml"), []byte("nonsense: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d = docsFor(root, config.Config{}); len(d.Dirs) != 0 {
		t.Errorf("%+v", d)
	}
}
