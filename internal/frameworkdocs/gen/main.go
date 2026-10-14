// Command gen copies the framework docs of the allowlist from the repository's docs/public
// into the embedded set (internal/frameworkdocs/docs): `go generate ./internal/frameworkdocs`.
// A doc the repository does not have yet is skipped, and a copy whose source is gone is
// removed, so the set is exactly the allowlist's files that exist. (The package it generates
// embeds docs/*.md, so the directory must hold at least one copy for this to build: it always does,
// the copies are committed.)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/petars-dev/govibe/internal/frameworkdocs"
)

func main() {
	src := filepath.Join("..", "..", "docs", "public") // go generate runs in the package directory
	dst := "docs"
	want := map[string]bool{}
	for _, f := range frameworkdocs.Files() {
		b, err := os.ReadFile(filepath.Join(src, f))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
		want[f] = true
		b, err = frameworkdocs.Shipped(b) // what an application sees: no framework-only blocks
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen:", f+":", err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dst, f), b, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen:", err)
			os.Exit(1)
		}
	}
	old, _ := filepath.Glob(filepath.Join(dst, "*.md"))
	for _, p := range old {
		if !want[filepath.Base(p)] {
			_ = os.Remove(p)
		}
	}
}
