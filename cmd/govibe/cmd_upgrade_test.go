package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/scaffold"
)

// generated writes a fresh app, generated at v0.1.0, pinning the framework at pin.
func generated(t *testing.T, pin string) string {
	t.Helper()
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
	if pin != "v0.1.0" {
		gomod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
		os.WriteFile(filepath.Join(root, "go.mod"), bytes.Replace(gomod, []byte("govibe v0.1.0"), []byte("govibe "+pin), 1), 0o644)
	}
	return root
}

func TestUpgradeShowsDiffsRefusesEditsAndForces(t *testing.T) {
	// A commit of the framework, not a release: the unreleased notes apply to it.
	root := generated(t, "v0.0.0-20261017120000-abcdef123456")
	mk, _ := os.ReadFile(filepath.Join(root, "Makefile"))
	os.WriteFile(filepath.Join(root, "Makefile"), append(mk, "\nlint:\n\ttrue\n"...), 0o644)
	var out bytes.Buffer
	err := runUpgrade(upgradeOptions{root: root}, &out)
	if exitCode(err) != 1 {
		t.Fatalf("exit %d: %v\n%s", exitCode(err), err, out.String())
	}
	for _, want := range []string{"framework v0.1.0 -> v0.0.0-20261017120000-abcdef123456", "edited    Makefile (you edited it: refused", "-lint:", "unchanged .air.toml", "nothing was written", "Upgrade notes", "## Unreleased"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := runUpgrade(upgradeOptions{root: root, dryRun: true, force: true, noNotes: true}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run: 1 file would be written, and govibe.yml") || strings.Contains(out.String(), "Upgrade notes") {
		t.Errorf("dry run:\n%s", out.String())
	}
	got, _ := os.ReadFile(filepath.Join(root, "Makefile"))
	if !strings.Contains(string(got), "lint:") {
		t.Error("the dry run wrote the Makefile")
	}
	out.Reset()
	if err := runUpgrade(upgradeOptions{root: root, force: true, noNotes: true}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "forced    Makefile") || !strings.Contains(out.String(), "wrote 1 file and govibe.yml (managed: v0.0.0-20261017120000-abcdef123456)") {
		t.Errorf("forced:\n%s", out.String())
	}
	out.Reset()
	if err := runUpgrade(upgradeOptions{root: root, noNotes: true}, &out); err != nil || !strings.Contains(out.String(), "up to date; nothing was written") {
		t.Errorf("second run: %v\n%s", err, out.String())
	}
}

func TestUpgradeRefusesAProjectWithoutAManifest(t *testing.T) {
	var out bytes.Buffer
	err := runUpgrade(upgradeOptions{root: fixture}, &out)
	if err == nil || !strings.Contains(err.Error(), "managed") {
		t.Errorf("err = %v", err)
	}
}
