package codegraph

import (
	"strings"
	"testing"
)

// Test sources are read for facts only: they have a cap of their own, and passing it
// skips the rest with a warning instead of failing the layer. The indexed sources keep
// the failing cap.
func TestTestFilesHaveACapOfTheirOwn(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a/a.go", "package a\n")
	for _, n := range []string{"a_test.go", "b_test.go", "c_test.go"} {
		writeFile(t, root, "a/"+n, "package a\n")
	}
	spec := walkSpec{start: ".", accept: isIndexedGoFile, alsoAccept: isGoTestFile, maxBytes: maxGoFileBytes, maxFiles: 2}
	sc, err := walkFiles(root, spec)
	if err != nil {
		t.Fatalf("too many tests must not fail the walk: %v", err)
	}
	if len(sc.files) != 1 || len(sc.extra) != 2 {
		t.Fatalf("files %d, extra %d", len(sc.files), len(sc.extra))
	}
	warned := false
	for _, w := range sc.warnings {
		warned = warned || strings.Contains(w, "test files")
	}
	if !warned {
		t.Fatalf("no warning about the skipped tests: %v", sc.warnings)
	}

	// The indexed sources still fail the layer past their cap.
	writeFile(t, root, "a/b.go", "package a\n")
	writeFile(t, root, "a/c.go", "package a\n")
	if _, err := walkFiles(root, spec); err == nil {
		t.Fatal("more indexed sources than the cap must fail")
	}
}
