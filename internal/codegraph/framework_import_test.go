package codegraph

import (
	"strings"
	"testing"
)

const (
	fwRoot = "github.com/petars-dev/govibe"
)

// appFiles is a small app that imports the framework, each layer taking something:
// what its layer may, and (marked) what it may not.
func appFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/shop\n\ngo 1.25.0\n\nrequire github.com/petars-dev/govibe v0.3.1\n")
	writeFile(t, root, "env.yml", "# no settings\n")
	// allowed
	writeFile(t, root, "main.go", "package main\n\nimport (\n\t\"github.com/petars-dev/govibe\"\n\t\"github.com/petars-dev/govibe/config\"\n\t\"github.com/petars-dev/govibe/web\"\n)\n\nvar _, _, _ = govibe.Main, config.Load, web.JSON\n\nfunc main() {}\n")
	writeFile(t, root, "controllers/home.go", "package controllers\n\nimport \"github.com/petars-dev/govibe/web\"\n\nvar _ = web.JSON\n")
	writeFile(t, root, "commands/export.go", "package commands\n\nimport \"github.com/petars-dev/govibe\"\n\nvar _ govibe.RunFunc\n")
	writeFile(t, root, "assets/templates/page.templ", "package templates\n\nimport \"github.com/petars-dev/govibe/web\"\n\ntempl Page() {\n\t@web.HTMXScript()\n}\n")
	// not allowed
	writeFile(t, root, "business/actions/order.go", "package actions\n\nimport (\n\t\"fmt\"\n\n\t\"github.com/petars-dev/govibe/web\"\n)\n\nvar _ = fmt.Sprint\nvar _ = web.JSON\n")
	writeFile(t, root, "business/actions/kernel.go", "package actions\n\nimport _ \"github.com/petars-dev/govibe/internal/kernel\"\n")
	writeFile(t, root, "storage/orderstore/store.go", "package orderstore\n\nimport \"github.com/petars-dev/govibe\"\n\nvar _ = govibe.Main\n")
	writeFile(t, root, "controllers/admin.go", "package controllers\n\nimport (\n\t\"github.com/petars-dev/govibe/config\"\n\t\"github.com/petars-dev/govibe/govibetest\"\n)\n\nvar _, _ = config.Load, govibetest.Boot\n")
	writeFile(t, root, "business/actions/page.templ", "package actions\n\nimport \"github.com/petars-dev/govibe/web\"\n\ntempl Fragment() {\n\t@web.HTMXScript()\n}\n")
	return root
}

func TestFrameworkImportRuleJudgesEveryLayer(t *testing.T) {
	_, rep, m := indexRoot(t, appFiles(t))
	if rep.Failed() {
		t.Fatalf("a layer failed: %+v", rep.Layers)
	}

	got := map[string]string{} // "subject -> external" -> message
	for _, f := range m.Kind("finding") {
		if f.Str("rule") == "framework.import" {
			if f.Str("severity") != "error" {
				t.Errorf("%s has severity %s: an import against the rule is an error", f.ID, f.Str("severity"))
			}
			got[strings.TrimPrefix(f.ID, "finding:framework.import:")] = f.Str("message")
		}
	}
	want := []string{
		"pkg:business/actions#ext:" + fwRoot + "/web",
		"pkg:business/actions#ext:" + fwRoot + "/internal/kernel",
		"pkg:storage/orderstore#ext:" + fwRoot,
		"pkg:controllers#ext:" + fwRoot + "/config",
		"pkg:controllers#ext:" + fwRoot + "/govibetest",
		"file:business/actions/page.templ#ext:" + fwRoot + "/web",
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("no finding for %s; have %q", w, findingKeys(got))
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d findings, want %d: %q", len(got), len(want), findingKeys(got))
	}

	// the allowed imports are not findings: wiring takes govibe, config and web, a controller web,
	// a command govibe, and a view (the assets templates) web
	for key := range got {
		for _, ok := range []string{"pkg:.#", "pkg:commands#", "pkg:assets/templates#", "file:assets/templates/page.templ#"} {
			if strings.HasPrefix(key, ok) {
				t.Errorf("an allowed import is a finding: %s", key)
			}
		}
	}
	if key := "pkg:controllers#ext:" + fwRoot + "/web"; got[key] != "" {
		t.Errorf("controllers may import web: %s", got[key])
	}

	t.Run("a message says what, why and where", func(t *testing.T) {
		msg := got["pkg:business/actions#ext:"+fwRoot+"/web"]
		for _, want := range []string{"business/actions", fwRoot + "/web", "framework-import rule", "plain Go", "business layer", "imported at business/actions/order.go:6"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q lacks %q", msg, want)
			}
		}
		if internal := got["pkg:business/actions#ext:"+fwRoot+"/internal/kernel"]; !strings.Contains(internal, "not API") {
			t.Errorf("an internal import is called what it is: %q", internal)
		}
		if page := got["file:business/actions/page.templ#ext:"+fwRoot+"/web"]; !strings.Contains(page, "business layer") {
			t.Errorf("a template is judged by the directory it is in: %q", page)
		}
	})

	t.Run("the finding is about the importer, in the importer's scope", func(t *testing.T) {
		m := m.scoped(ScopeApp)
		f := m.Node("finding:framework.import:pkg:business/actions#ext:" + fwRoot + "/web")
		if f == nil {
			t.Fatal("the finding is hidden with the framework's node it names: it must stay, about the app's package")
		}
		if f.Str("path") != "business/actions/order.go" || f.Int("line") != 6 {
			t.Errorf("the finding points at %s:%d", f.Str("path"), f.Int("line"))
		}
	})
}

func findingKeys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestFrameworkImportRuleDoesNotApplyToTheFrameworkItself(t *testing.T) {
	_, _, m := indexRoot(t, repoRoot)
	for _, r := range m.ruleInfos(nil) {
		if r.Name != "framework.import" {
			continue
		}
		if r.Checked || !strings.Contains(r.Note, "not applicable") || !strings.Contains(r.Note, "framework itself") {
			t.Errorf("framework.import in the framework's own repository = %+v", r)
		}
		return
	}
	t.Fatal("no framework.import rule")
}

func TestFrameworkImportRuleCanBeSwitchedOffInGovibeYml(t *testing.T) {
	root := appFiles(t)
	writeFile(t, root, "govibe.yml", "rules:\n  framework.import: off\n")
	_, _, m := indexRoot(t, root)
	if got := findingsOf(m, "framework.import"); len(got) != 0 {
		t.Errorf("a rule that is off found %q", got)
	}
}
