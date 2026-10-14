package rules

import (
	"strings"
	"testing"
)

func TestLayerVerdict(t *testing.T) {
	allowed := [][2]string{
		{"controllers", "business"}, {"controllers", "support"}, {"controllers", "views"}, {"controllers", "middleware"},
		{"business", "storage"}, {"business", "support"}, {"business", "business"},
		{"storage", "support"}, {"storage", "storage"},
		{"wiring", "storage"}, {"wiring", "controllers"}, {"dev", "storage"}, {"dev", "business"},
		{"middleware", "support"}, {"views", "views"},
		{"commands", "business"}, {"commands", "support"}, {"wiring", "commands"},
	}
	for _, p := range allowed {
		if why := LayerVerdict(p[0], p[1]); why != "" {
			t.Errorf("%s -> %s should be allowed: %s", p[0], p[1], why)
		}
	}
	forbidden := [][2]string{
		{"controllers", "storage"}, {"commands", "storage"}, {"middleware", "storage"}, {"views", "storage"}, {"support", "storage"},
		{"storage", "business"}, {"storage", "controllers"}, {"storage", "commands"}, {"storage", "wiring"},
		{"business", "controllers"}, {"business", "commands"}, {"business", "middleware"}, {"business", "views"}, {"business", "wiring"}, {"business", "dev"},
	}
	for _, p := range forbidden {
		if LayerVerdict(p[0], p[1]) == "" {
			t.Errorf("%s -> %s should be forbidden", p[0], p[1])
		}
	}
	// unknown layers are not judged: a package the profile leaves out is not a violation
	if LayerVerdict("", "storage") != "" || LayerVerdict("controllers", "") != "" {
		t.Error("an unclassified package must not be judged")
	}
}

// The table of the spec (SPEC_FRAMEWORK_EXTRACTION, "The app layers may import from the
// framework"), one row per layer.
func TestFrameworkImportsFollowTheTable(t *testing.T) {
	const (
		govibe  = PkgGovibe
		web     = PkgWeb
		config  = PkgConfig
		govtest = PkgGovibetest
	)
	table := map[string]map[string]bool{ // layer -> package -> allowed
		"business":    {govibe: false, web: false, config: false},
		"storage":     {govibe: false, web: false, config: false},
		"controllers": {govibe: false, web: true, config: false},
		"middleware":  {govibe: false, web: true, config: false},
		"views":       {govibe: false, web: true, config: false},
		"commands":    {govibe: true, web: false, config: false},
		"support":     {govibe: false, web: false, config: true},
		"wiring":      {govibe: true, web: true, config: true},
	}
	for layer, pkgs := range table {
		for pkg, ok := range pkgs {
			got := FrameworkImportVerdict(layer, pkg, false)
			if ok && got != "" {
				t.Errorf("%s may import %s: %s", layer, pkgName(pkg), got)
			}
			if !ok && got == "" {
				t.Errorf("%s must not import %s", layer, pkgName(pkg))
			}
		}
		if got := FrameworkImportVerdict(layer, govtest, false); got == "" || !strings.Contains(got, "tests") {
			t.Errorf("%s imports govibetest: %q (only tests may)", layer, got)
		}
		if got := FrameworkImportVerdict(layer, "internal/kernel", true); got == "" || !strings.Contains(got, "internal/") {
			t.Errorf("%s imports an internal package: %q", layer, got)
		}
	}
	if FrameworkImportVerdict("dev", "internal/kernel", true) != "" {
		t.Error("the framework's own dev tooling is not judged")
	}
	if FrameworkImportVerdict("brand-new-layer", web, false) != "" {
		t.Error("a layer the table does not know is not judged")
	}
}

func TestAVerdictNamesThePackageAndSaysWhy(t *testing.T) {
	got := FrameworkImportVerdict("business", PkgWeb, false)
	for _, want := range []string{"govibe/web", "business", "plain Go"} {
		if !strings.Contains(got, want) {
			t.Errorf("verdict %q lacks %q", got, want)
		}
	}
	if got := strings.Join(FrameworkImports("wiring"), ","); got != "govibe,govibe/config,govibe/web" {
		t.Errorf("wiring imports %s", got)
	}
	if len(FrameworkImports("business")) != 0 {
		t.Error("business imports nothing")
	}
}

func TestRelativePath(t *testing.T) {
	for in, want := range map[string]struct {
		rel string
		ok  bool
	}{
		"github.com/x/fw":              {"", true},
		"github.com/x/fw/web":          {"web", true},
		"github.com/x/fw/internal/k/z": {"internal/k/z", true},
		"github.com/x/fwother":         {"", false},
		"github.com/y/fw":              {"", false},
	} {
		rel, ok := RelativePath("github.com/x/fw", in)
		if rel != want.rel || ok != want.ok {
			t.Errorf("RelativePath(%q) = %q, %v", in, rel, ok)
		}
	}
}

func TestSpecSections(t *testing.T) {
	full := []string{"Description", "Goals", "Phases & Tasks", "Backlog", "Status Table"}
	if got := MissingSpecSections(full); len(got) != 0 {
		t.Errorf("a complete spec lacks %v", got)
	}
	if got := MissingSpecSections([]string{"description", " GOALS ", "Phases and Tasks", "backlog", "Status"}); len(got) != 0 {
		t.Errorf("case and spelling variants: %v", got)
	}
	if got := strings.Join(MissingSpecSections([]string{"Description", "Backlog"}), ", "); got != "Goals, Phases and Tasks, Status Table" {
		t.Errorf("missing = %q", got)
	}
}
