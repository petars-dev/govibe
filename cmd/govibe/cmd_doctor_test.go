package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// A fresh app passes every static check; the build checks are faked here (the keystone
// runs the real ones) and one failure fails the doctor.
func TestDoctorReportsAndFails(t *testing.T) {
	root := generated(t, "v0.1.0")
	var out bytes.Buffer
	err := runDoctor(context.Background(), doctorOptions{root: root, noBuild: true}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, want := range []string{"warn  framework  go.mod pins github.com/petars-dev/govibe v0.1.0 but this tool is", "ok    managed    4 files as generated", "ok    specs      none yet",
		"ok    baseline   0 accepted findings", "ok    markers    // govibe:routes in routes/routes.go", "ok    markers    // govibe:commands in routes/console.go", "ok    scripts", "0 failures"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	// A spec without its sections, a baseline entry with no reason, a CDN script, a
	// deleted marker, an edited Makefile.
	os.WriteFile(filepath.Join(root, "docs", "specs", "SPEC_PAY.md"), []byte("# SPEC_PAY\n\n## Description\nx\n"), 0o644)
	os.WriteFile(filepath.Join(root, "docs", "graph", "baseline.json"), []byte(`{"findings":[{"id":"finding:layering:x","rule":"layering","severity":"error","message":"m"}]}`), 0o644)
	os.WriteFile(filepath.Join(root, "assets", "templates", "cdn.templ"), []byte("package templates\n\ntempl X() {\n\t<script src=\"https://unpkg.com/htmx.org\"></script>\n}\n"), 0o644)
	console, _ := os.ReadFile(filepath.Join(root, "routes", "console.go"))
	os.WriteFile(filepath.Join(root, "routes", "console.go"), bytes.Replace(console, []byte("// govibe:commands"), nil, 1), 0o644)
	os.WriteFile(filepath.Join(root, "Makefile"), []byte("all:\n"), 0o644)
	fakeInspect := func(ctx context.Context, root string) (*routeinfo.Inspect, error) {
		doc := &routeinfo.Inspect{Schema: routeinfo.InspectSchema}
		doc.App.Contributes.Config = true
		doc.Routes = []routeinfo.InspectRoute{{Method: "GET", Path: "/", MountedBy: routeinfo.MountedByApp}}
		return doc, nil
	}
	fakeDeps := func(ctx context.Context, root string) (string, error) {
		return "fmt\ngithub.com/petars-dev/govibe/internal/devtools\n", nil
	}
	out.Reset()
	err = runDoctor(context.Background(), doctorOptions{root: root, inspect: fakeInspect, listDeps: fakeDeps}, &out)
	if exitCode(err) != 1 {
		t.Fatalf("exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"note  managed    Makefile: edited", "warn  specs      docs/specs/SPEC_PAY.md lacks the sections: Goals, Phases and Tasks, Backlog, Status Table",
		"fail  baseline   1 entry says no reason", "note  markers    no // govibe:commands", "warn  scripts    assets/templates/cdn.templ: <script src=\"https://unpkg.com/htmx.org\"",
		"ok    inspect    the app declares 1 routes and 0 commands", "warn  embeds     assets/static holds", "but App.Assets is nil", "holds 1 migration but App.Migrations is nil",
		"fail  build      the default build links dev tooling: github.com/petars-dev/govibe/internal/devtools", "2 failures"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// An app that does not boot fails the inspect check with its own words.
	out.Reset()
	err = runDoctor(context.Background(), doctorOptions{root: root, inspect: func(ctx context.Context, root string) (*routeinfo.Inspect, error) {
		return nil, errors.New("exit status 1\ncommand \"items:prune\": invalid schedule \"x\"")
	}, listDeps: fakeDeps}, &out)
	if exitCode(err) != 1 || !strings.Contains(out.String(), "fail  inspect    the app could not say what it declares") || !strings.Contains(out.String(), "items:prune") {
		t.Errorf("exit %d:\n%s", exitCode(err), out.String())
	}
}

// check prints the doctor's static findings before the gate.
func TestCheckPrintsTheDoctorsStaticFindings(t *testing.T) {
	root := generated(t, "v0.1.0")
	os.WriteFile(filepath.Join(root, "docs", "specs", "SPEC_PAY.md"), []byte("# SPEC_PAY\n"), 0o644)
	logrus.SetLevel(logrus.PanicLevel)
	var out bytes.Buffer
	err := runCheck(context.Background(), checkOptions{root: root, failOn: "none", format: "text", noInspect: true}, &out, &out)
	if exitCode(err) != 0 {
		t.Fatalf("exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"managed files: 4 as generated", "doctor:", "warn  specs      docs/specs/SPEC_PAY.md lacks the sections", "graph index:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestMakeUpgradeAndDoctorAreRegistered(t *testing.T) {
	for _, args := range [][]string{{"make", "page", "x"}, {"make", "resource", "x"}, {"make", "command", "x:y"}, {"make", "config"}, {"upgrade"}, {"doctor"}} {
		cmd, _, err := rootCmd.Find(args)
		if err != nil || cmd == rootCmd || cmd == makeCmd {
			t.Errorf("%v: %v %v", args, cmd, err)
		}
	}
}
