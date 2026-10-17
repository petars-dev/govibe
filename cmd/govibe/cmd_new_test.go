package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder stands in for the steps `govibe new` runs after it writes the tree.
type recorder struct{ steps []string }

func (r *recorder) run(ctx context.Context, dir string, out, errOut io.Writer, args ...string) error {
	r.steps = append(r.steps, strings.Join(displayArgs(args), " "))
	return nil
}

func TestNewWritesTheAppAndRunsTheSteps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	rec := &recorder{}
	var out bytes.Buffer
	err := runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: dir, version: "v0.1.0", run: rec.run}, &out, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"main.go", "env.yml", "govibe.yml", "go.mod", "SKILL.md", "Makefile", ".air.toml", ".github/workflows/govibe.yml", "routes/routes.go", "assets/static/govibe-logo.png"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s was not written", f)
		}
	}
	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.Contains(string(gomod), "github.com/petars-dev/govibe v0.1.0") {
		t.Errorf("go.mod:\n%s", gomod)
	}
	want := []string{"go mod tidy", "go tool templ generate", "git init -q", "git add -A", "git commit -q -m govibe new github.com/acme/shop", "go tool govibe check"}
	if strings.Join(rec.steps, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(rec.steps, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range []string{"govibe new: github.com/acme/shop", "==> go mod tidy", "cd " + dir, "make dev"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("output lacks %q:\n%s", s, out.String())
		}
	}
}

func TestNewSkipsGitAndCheckOnRequest(t *testing.T) {
	rec := &recorder{}
	err := runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: filepath.Join(t.TempDir(), "x"), version: "v0.1.0", noGit: true, noCheck: true, run: rec.run}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rec.steps, "; "); got != "go mod tidy; go tool templ generate" {
		t.Errorf("steps: %s", got)
	}
}

func TestNewRefusesANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	err := runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: dir, version: "v0.1.0", run: (&recorder{}).run}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Errorf("err = %v", err)
	}
}

func TestNewNeedsAReplaceWhenBuiltFromACheckout(t *testing.T) {
	for _, v := range []string{"(devel)", "v0.0.0-20261017120000-abcdef123456+dirty", "v0.0.0"} {
		err := runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: filepath.Join(t.TempDir(), "x"), version: v, run: (&recorder{}).run}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "--replace") {
			t.Errorf("%s: err = %v", v, err)
		}
	}
	// A release and a commit are fetchable.
	for _, v := range []string{"v0.1.0", "v0.0.0-20261017120000-abcdef123456", "v1.0.0-rc.1"} {
		if !fetchable(v) {
			t.Errorf("%s should be fetchable", v)
		}
	}
	// With a checkout, no version is needed, and go.mod points at it.
	checkout := t.TempDir()
	os.WriteFile(filepath.Join(checkout, "go.mod"), []byte("module github.com/petars-dev/govibe\n"), 0o644)
	dir := filepath.Join(t.TempDir(), "shop")
	err := runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: dir, version: "(devel)", replace: checkout, run: (&recorder{}).run}, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.Contains(string(gomod), "replace github.com/petars-dev/govibe => "+filepath.ToSlash(checkout)) {
		t.Errorf("go.mod:\n%s", gomod)
	}
	// A --replace that is not a checkout is refused before anything is written.
	err = runNew(context.Background(), newOptions{module: "github.com/acme/shop", dir: filepath.Join(t.TempDir(), "y"), replace: t.TempDir(), run: (&recorder{}).run}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not a checkout") {
		t.Errorf("err = %v", err)
	}
}

func TestNewAndCheckAreRegistered(t *testing.T) {
	for _, args := range [][]string{{"new", "github.com/acme/shop"}, {"check"}} {
		cmd, _, err := rootCmd.Find(args)
		if err != nil || cmd == rootCmd {
			t.Errorf("%v: %v %v", args, cmd, err)
		}
	}
}
