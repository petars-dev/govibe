package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/scaffold"
)

func TestMakeWritesTheFilesAndRunsTempl(t *testing.T) {
	root := generated(t, "v0.1.0")
	rec := &recorder{}
	var out bytes.Buffer
	err := runMake(context.Background(), makeOptions{kind: "page", name: "about", root: root, run: rec.run}, &out, io.Discard)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, f := range []string{"assets/templates/about.templ", "controllers/about.go"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s was not written", f)
		}
	}
	routes, _ := os.ReadFile(filepath.Join(root, "routes", "routes.go"))
	if !strings.Contains(string(routes), `r.Get("/about", controllers.About)`) {
		t.Errorf("routes.go:\n%s", routes)
	}
	if got := strings.Join(rec.steps, ";"); got != "go tool templ generate" {
		t.Errorf("steps = %q", got)
	}
	for _, want := range []string{"govibe make page about", "write assets/templates/about.templ", "edit  routes/routes.go", "edit  docs/public/ARCHITECTURE.md", "==> go tool templ generate", "Next:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// A second page over the same file is refused before anything is written.
	out.Reset()
	err = runMake(context.Background(), makeOptions{kind: "page", name: "about", root: root, run: rec.run}, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("err = %v", err)
	}
	// A dry run writes nothing.
	out.Reset()
	if err := runMake(context.Background(), makeOptions{kind: "action", name: "notify", root: root, dryRun: true}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "business", "actions", "notify.go")); err == nil || !strings.Contains(out.String(), "would write business/actions/notify.go") {
		t.Errorf("dry run:\n%s", out.String())
	}
}

// buildable turns a generated tree into a module that builds against this checkout
// offline: the example's go.mod and go.sum, with the module path swapped.
func buildable(t *testing.T, root string) {
	t.Helper()
	checkout, _ := filepath.Abs(filepath.Join("..", ".."))
	example := filepath.Join(checkout, "examples", "hello")
	gomod, err := os.ReadFile(filepath.Join(example, "go.mod"))
	if err != nil {
		t.Skip("not inside the framework repository")
	}
	gosum, _ := os.ReadFile(filepath.Join(example, "go.sum"))
	m := strings.Replace(string(gomod), "module "+scaffold.DemoSource("github.com/petars-dev/govibe"), "module github.com/acme/shop", 1)
	m = strings.Replace(m, "=> ../..", "=> "+checkout, 1)
	os.WriteFile(filepath.Join(root, "go.mod"), []byte(m), 0o644)
	os.WriteFile(filepath.Join(root, "go.sum"), gosum, 0o644)
}

func goIn(t *testing.T, dir string, args ...string) string {
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

// Everything the generators write builds, vets and passes its tests in a generated
// app, and the gate finds nothing at any severity: the resource, the scheduled command,
// the page (when templ is installed), the action, the service, the store, the
// migration, the middleware, the config and the spec. The keystone does the same
// through the real tool; this runs offline against the checkout.
func TestGeneratedCodeBuildsAndPassesTheGate(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the generated app")
	}
	logrus.SetLevel(logrus.PanicLevel)
	root := generated(t, "v0.1.0")
	buildable(t, root)
	os.WriteFile(filepath.Join(root, "env.yml"), []byte("shop:\n  greeting: ${GREETING:Hello}\n  page_size: ${PAGE_SIZE:20}\n"), 0o644)
	templ := templBinary()
	runTempl := func(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error {
		c := exec.Command(templ, "generate")
		c.Dir = dir
		c.Stdout, c.Stderr = out, errOut
		return c.Run()
	}
	for _, m := range []makeOptions{
		{kind: "resource", name: "order"},
		{kind: "command", name: "items:prune", gen: optsSchedule("0 3 * * *", "Delete archived items")},
		{kind: "command", name: "cache:warm", gen: optsSchedule("", "Warm the cache")},
		{kind: "action", name: "notify"},
		{kind: "service", name: "billing"},
		{kind: "store", name: "invoice"},
		{kind: "migration", name: "create_invoices", gen: optsTable("invoices")},
		{kind: "middleware", name: "rate-limit"},
		{kind: "config"},
		{kind: "spec", name: "payments"},
		{kind: "page", name: "about"},
	} {
		if m.kind == "page" && templ == "" {
			t.Log("templ is not installed: the page is not generated here (the keystone covers it)")
			continue
		}
		m.root, m.run = root, runTempl
		var out bytes.Buffer
		if err := runMake(context.Background(), m, &out, &out); err != nil {
			t.Fatalf("make %s %s: %v\n%s", m.kind, m.name, err, out.String())
		}
	}
	goIn(t, root, "build", "./...")
	goIn(t, root, "build", "-tags", "govibe_dev", "./...")
	goIn(t, root, "vet", "./...")
	test := goIn(t, root, "test", "./...")
	for _, pkg := range []string{"github.com/acme/shop\t", "shop/business/services/orders", "shop/storage/orderstore", "shop/business/actions", "shop/middleware", "shop/config"} {
		if !strings.Contains(test, "ok  \t"+pkg) && !strings.Contains(test, "ok  \tgithub.com/acme/"+pkg) {
			t.Errorf("go test: no ok for %s:\n%s", pkg, test)
		}
	}
	// The gate, with the app's own inspect --json, finds nothing at any severity.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	var out bytes.Buffer
	err := runCheck(context.Background(), checkOptions{root: root, failOn: "info", format: "text"}, &out, &out)
	if exitCode(err) != 0 {
		t.Fatalf("check exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"findings: 0 error, 0 warning, 0 info", "commands: 2 (1 scheduled)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("check lacks %q:\n%s", want, out.String())
		}
	}
	// And the doctor, with the real build checks, passes.
	out.Reset()
	if err := runDoctor(context.Background(), doctorOptions{root: root}, &out); err != nil {
		t.Fatalf("doctor: %v\n%s", err, out.String())
	}
	for _, want := range []string{"ok    inspect", "ok    embeds", "ok    build", "ok    specs      1 with every required section", "0 failures"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("doctor lacks %q:\n%s", want, out.String())
		}
	}
}

// templBinary finds a templ on the PATH or in GOPATH/bin, "" when there is none.
func templBinary() string {
	if p, err := exec.LookPath("templ"); err == nil {
		return p
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	p := filepath.Join(strings.TrimSpace(string(out)), "bin", "templ")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func optsSchedule(spec, short string) (o generateOptions) {
	o.Schedule, o.Short = spec, short
	return o
}

func optsTable(table string) (o generateOptions) {
	o.Table = table
	return o
}
