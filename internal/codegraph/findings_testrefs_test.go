package codegraph

import (
	"context"
	"testing"
)

// An exported function that only tests call is referenced; one nothing calls is
// not. Tests add no nodes or edges to the graph: they only count as references.
func TestUnreferencedCountsReferencesFromTests(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/mini\n\ngo 1.25\n")
	writeFile(t, root, "main.go", "package main\n\nimport \"example.com/mini/lib\"\n\nfunc main() { lib.Used() }\n")
	writeFile(t, root, "lib/lib.go", `package lib

// Used is called by main.
func Used() {}

// OnlyInPackageTest is called only by a test in this package.
func OnlyInPackageTest() {}

// OnlyExternalTest is called only by an external test package.
func OnlyExternalTest() {}

// Dead is called by nothing.
func Dead() {}

// Sibling has a method named like a function elsewhere; a selector must not count.
func Sibling() {}

// Label is only ever a struct field key and a field name in tests.
func Label() {}
`)
	writeFile(t, root, "lib/lib_test.go", `package lib

import "testing"

func TestInPackage(t *testing.T) { OnlyInPackageTest() }

type row struct{ Label string }

func fill(label string) row { return row{Label: label} } // a field key and a field declaration, never a call of lib.Label
`)
	writeFile(t, root, "lib/ext_test.go", `package lib_test

import (
	"testing"

	"example.com/mini/lib"
)

func TestExternal(t *testing.T) {
	lib.OnlyExternalTest()
	var s struct{ Dead func() }
	_ = s.Dead // a field named Dead is not a call of lib.Dead
}
`)

	h, _ := openStore(t)
	ix := New(h.QuadStore, Options{Root: root})
	if _, err := ix.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, id := range byRule(m)["code.unreferenced"] {
		got[id] = true
	}
	for _, id := range []string{"finding:code.unreferenced:func:lib.Dead", "finding:code.unreferenced:func:lib.Sibling", "finding:code.unreferenced:func:lib.Label"} {
		if !got[id] {
			t.Errorf("%s should be reported, got %v", id, got)
		}
	}
	for _, id := range []string{"lib.Used", "lib.OnlyInPackageTest", "lib.OnlyExternalTest"} {
		if got["finding:code.unreferenced:func:"+id] {
			t.Errorf("%s is referenced (by code or a test) and must not be reported", id)
		}
	}
	// A test file is never a node of the graph.
	if n := m.Node("file:lib/lib_test.go"); n != nil {
		t.Errorf("a test file was indexed: %+v", n)
	}
}

// Editing a test changes what the findings see, so it must make the code layer stale.
func TestEditingATestMakesTheCodeLayerStale(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/mini\n\ngo 1.25\n")
	writeFile(t, root, "lib/lib.go", "package lib\n\n// F is exported.\nfunc F() {}\n")
	w := newTestWorld(t, root, Options{})
	before, err := w.codeFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F() }\n")
	after, err := newTestWorld(t, root, Options{}).codeFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("adding a test did not change the code layer's fingerprint")
	}
}
