package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pinnedGoMod = `module github.com/acme/shop

go 1.25.0

require github.com/petars-dev/govibe v0.2.0

tool github.com/petars-dev/govibe/cmd/govibe
`

func writeGoMod(t *testing.T, text string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Inside an app that pins another version, every command but new, version and help is
// handed to `go tool govibe` in the same directory, once.
func TestShimDelegatesToThePinnedVersion(t *testing.T) {
	app := writeGoMod(t, pinnedGoMod)
	sub := filepath.Join(app, "controllers")
	os.MkdirAll(sub, 0o755)
	env := []string{"PATH=/usr/bin", "HOME=/home/x"}

	d := shim([]string{"check", "--fail-on", "info"}, sub, env, "v0.1.0")
	if d.Cmd == nil {
		t.Fatalf("no delegation: %+v", d)
	}
	if got := strings.Join(d.Cmd.Args, " "); got != "go tool govibe check --fail-on info" {
		t.Errorf("args = %q", got)
	}
	if d.Cmd.Dir != sub {
		t.Errorf("dir = %q, want where the user ran it", d.Cmd.Dir)
	}
	if !strings.Contains(strings.Join(d.Cmd.Env, " "), "GOVIBE_SHIMMED=1") || len(d.Cmd.Env) != 3 {
		t.Errorf("env = %v", d.Cmd.Env)
	}
	if !strings.Contains(d.Reason, "v0.2.0") || !strings.Contains(d.Reason, "v0.1.0") || d.Pinned != "v0.2.0" {
		t.Errorf("reason = %q pinned = %q", d.Reason, d.Pinned)
	}
	// A tool built from a checkout, run in a released app, is another version too.
	if d := shim([]string{"doctor"}, app, env, "(devel)"); d.Cmd == nil {
		t.Error("a (devel) tool did not delegate")
	}
	// --root names the project, wherever the command runs.
	if d := shim([]string{"doctor", "--root", app}, t.TempDir(), env, "v0.1.0"); d.Cmd == nil {
		t.Error("--root was not honoured")
	}
	if d := shim([]string{"check"}, t.TempDir(), append(env, "GOVIBE_ROOT="+app), "v0.1.0"); d.Cmd == nil {
		t.Error("GOVIBE_ROOT was not honoured")
	}
}

func TestShimRunsThisBinaryWhenItShould(t *testing.T) {
	app := writeGoMod(t, pinnedGoMod)
	env := []string{"PATH=/usr/bin"}
	cases := map[string]struct {
		args []string
		dir  string
		env  []string
		tool string
	}{
		"same version":         {[]string{"check"}, app, env, "v0.2.0"},
		"new":                  {[]string{"new", "github.com/acme/other"}, app, env, "v0.1.0"},
		"version":              {[]string{"version"}, app, env, "v0.1.0"},
		"help":                 {[]string{"help"}, app, env, "v0.1.0"},
		"flags only":           {[]string{"--help"}, app, env, "v0.1.0"},
		"no arguments":         {nil, app, env, "v0.1.0"},
		"already delegated":    {[]string{"check"}, app, append(env, "GOVIBE_SHIMMED=1"), "v0.1.0"},
		"switched off":         {[]string{"check"}, app, append(env, "GOVIBE_SHIM=off"), "v0.1.0"},
		"outside any module":   {[]string{"check"}, t.TempDir(), env, "v0.1.0"},
		"no tool directive":    {[]string{"check"}, writeGoMod(t, "module x\n\ngo 1.25.0\n\nrequire github.com/petars-dev/govibe v0.2.0\n"), env, "v0.1.0"},
		"not an app":           {[]string{"check"}, writeGoMod(t, "module x\n\ngo 1.25.0\n"), env, "v0.1.0"},
		"a checkout (replace)": {[]string{"check"}, writeGoMod(t, pinnedGoMod+"\nreplace github.com/petars-dev/govibe => ../govibe\n"), env, "v0.1.0"},
	}
	for name, c := range cases {
		if d := shim(c.args, c.dir, c.env, c.tool); d.Cmd != nil {
			t.Errorf("%s: delegated: %s", name, d.Reason)
		}
	}
}

func TestPinOf(t *testing.T) {
	pinned, tool, replaced := pinOf([]byte(pinnedGoMod), "github.com/petars-dev/govibe")
	if pinned != "v0.2.0" || !tool || replaced {
		t.Errorf("pinOf = %q %v %v", pinned, tool, replaced)
	}
	_, _, replaced = pinOf([]byte(pinnedGoMod+"\nreplace github.com/petars-dev/govibe => /src/govibe\n"), "github.com/petars-dev/govibe")
	if !replaced {
		t.Error("a directory replace was not seen")
	}
	_, _, replaced = pinOf([]byte(pinnedGoMod+"\nreplace github.com/petars-dev/govibe => github.com/fork/govibe v0.2.1\n"), "github.com/petars-dev/govibe")
	if replaced {
		t.Error("a module replace counts as a checkout")
	}
	if p, _, _ := pinOf([]byte("not a go.mod"), "x"); p != "" {
		t.Error("garbage parsed")
	}
}
