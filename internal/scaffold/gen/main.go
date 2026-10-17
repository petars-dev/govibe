// Command gen syncs the Hello GoVibe demo into the scaffold's template set and writes
// the reference app's SKILL.md: `go generate ./internal/scaffold`.
//
// examples/hello is the source of truth for the demo. go:embed cannot reach a nested
// module, so its files are copied into templates/demo with the module path templated;
// a test fails when the copy is stale. The reference app's SKILL.md is rendered the way
// a generated app's is, so the rules an app reads are the rules the example follows.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/scaffold"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	src := filepath.Join("..", "..", "examples", "hello") // go generate runs in the package directory
	dst := filepath.Join("templates", "demo")
	fw := profile.FrameworkModule()

	set, err := scaffold.SyncDemo(os.DirFS(src), fw)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	for rel, b := range set {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}

	o := scaffold.Options{Module: scaffold.DemoSource(fw), Name: scaffold.DemoName, FrameworkModule: fw, FrameworkVersion: "v0.0.0"}
	if err := o.Complete(); err != nil {
		return err
	}
	skill, err := scaffold.Skill(scaffold.DataFor(o))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(src, "SKILL.md"), skill, 0o644)
}
