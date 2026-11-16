package main

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// devOnly names govibe's dev packages. Every way into the dev tooling goes through one
// of them; the libraries they use (the MCP SDK, goldmark, chroma, x/tools, cayley) are
// not named, since the app may use them in production itself.
var devOnly = regexp.MustCompile(`petars-dev/govibe/internal/(devtools|codegraph|dbexplorer|devmcp|graphdb)`)

// A production build links no dev tooling: not the panel, the graph, the MCP server
// or the database explorer, and none of the panel's files (acceptance scenario A7). The default build is what ships; govibe_dev is
// opt-in, so this holds by construction and fails the moment something breaks it.
func TestDefaultBuildLinksNoDevTools(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	if len(deps) < 100 {
		t.Fatalf("go list reported only %d packages: %s", len(deps), out)
	}
	for _, d := range deps {
		if devOnly.MatchString(d) {
			t.Errorf("the default build links %s", d)
		}
	}
}

// The dev tag does the opposite, which proves the check above looks at something real.
func TestDevBuildLinksTheDevTools(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "govibe_dev", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "petars-dev/govibe/internal/devtools") {
		t.Error("a govibe_dev build does not link the dev tools")
	}
}
