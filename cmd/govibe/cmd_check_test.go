package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/scaffold"
)

// The fixture has one error finding and no manifest: the check reports both facts and
// fails the gate.
func TestCheckRunsTheGateInAScratchStore(t *testing.T) {
	var out, errOut bytes.Buffer
	err := runCheck(context.Background(), checkOptions{root: fixture, failOn: "error", format: "text"}, &out, &errOut)
	if exitCode(err) != 1 {
		t.Fatalf("exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"govibe check: ", "managed files: none (no manifest", "graph index:", "calls      ok", "NEW error   layering"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), fixture+"/docs/graph") {
		t.Error("the check wrote into the project's own store")
	}
}

// A generated app's managed files are compared with its manifest.
func TestCheckReportsManagedDrift(t *testing.T) {
	tree, err := scaffold.Render(scaffold.Options{Module: "github.com/acme/shop", FrameworkModule: "github.com/petars-dev/govibe", FrameworkVersion: "v0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for p, b := range tree {
		fp := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(fp), 0o755)
		os.WriteFile(fp, b, 0o644)
	}
	os.WriteFile(filepath.Join(root, "Makefile"), []byte("all:\n\ttrue\n"), 0o644)
	var out bytes.Buffer
	// --no-inspect: the tree has no go.sum, so its binary cannot be built here; the gate is not the point.
	err = runCheck(context.Background(), checkOptions{root: root, failOn: "none", format: "text", noInspect: true}, &out, &out)
	if exitCode(err) != 0 {
		t.Fatalf("exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"managed files: 1 of 4 not as generated (framework v0.1.0)", "Makefile: edited", "graph index:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// The default baseline is the app's own.
	if !strings.Contains(out.String(), "gate (fail on none)") && !strings.Contains(out.String(), "findings:") {
		t.Errorf("no gate in:\n%s", out.String())
	}
}

func TestCheckRejectsABadFormat(t *testing.T) {
	err := runCheck(context.Background(), checkOptions{root: fixture, failOn: "error", format: "xml"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--format") {
		t.Errorf("err = %v", err)
	}
}
