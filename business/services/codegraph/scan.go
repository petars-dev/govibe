package codegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Caps that keep a layer's input bounded, whatever is lying around in the repo.
const (
	maxGoFileBytes = 2 << 20 // a Go source file larger than this is skipped
	maxWalkDepth   = 24      // directories nested deeper than this are skipped
	maxGoFiles     = 20000   // more than this fails the layer rather than index half the repo

	maxTemplFileBytes = 2 << 20 // a .templ source larger than this is skipped
	maxTemplFiles     = 5000
	maxDocFileBytes   = 1 << 20 // a markdown file larger than this is skipped
	maxDocFiles       = 2000
	maxConfigBytes    = 1 << 20 // config/env.yml
)

// sourceFile is one input file of a layer. Its path, mtime and size are what
// the layer's fingerprint hashes.
type sourceFile struct {
	rel   string // repo-relative, '/' separated
	abs   string
	size  int64
	mtime int64 // UnixNano
}

// scan is the result of walking the repo for one layer's inputs.
type scan struct {
	files    []sourceFile // sorted by rel
	extra    []sourceFile // sorted by rel: files read for facts only, never indexed (test sources)
	warnings []string
}

// fingerprint hashes the sorted path, mtime and size of every input, so it
// changes when a file is added, removed, edited or touched, and never otherwise.
func fingerprint(files []sourceFile) string {
	sorted := make([]sourceFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rel < sorted[j].rel })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", f.rel, f.mtime, f.size)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// resolveRoot returns the absolute, symlink-free form of root. Everything the
// indexer reads is confined to it.
func resolveRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", real)
	}
	return real, nil
}

// skipDir reports whether the code layer must not descend into a directory.
// rel is the directory's repo-relative path ("." for the root).
func skipDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	// Dot and underscore directories are ignored by the Go tool too (.git,
	// .idea, .tools, _build, ...).
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "testdata", "node_modules":
		return true
	case "tmp", "vendor":
		// Air's build dir and a vendored module tree live at the repo root only.
		return !strings.Contains(rel, "/")
	}
	return false
}

// walkSpec describes one layer's inputs: the files under start whose name
// accept approves.
type walkSpec struct {
	start      string // repo-relative directory to walk, "." for the whole repo
	accept     func(name string) bool
	alsoAccept func(name string) bool // optional: files that go to scan.extra, under the same caps
	maxBytes   int64
	maxFiles   int
}

// scanGo walks root for the .go files the code layer indexes: no tests, no
// generated templ output, nothing under testdata/, tmp/, vendor/ or a dot
// directory, nothing inside a nested module (a directory with its own
// go.mod), and nothing reached through a symlink. The test sources found on the
// way are listed apart (scan.extra): they are not indexed, but what they
// reference counts as used.
func scanGo(root string) (*scan, error) {
	return walkFiles(root, walkSpec{start: ".", accept: isIndexedGoFile, alsoAccept: isGoTestFile, maxBytes: maxGoFileBytes, maxFiles: maxGoFiles})
}

// walkFiles is the confined walker every layer's input scan uses: it never
// follows a symlink, applies the same directory skip rules, and enforces
// size, depth and count caps. A start directory that does not exist yields no
// files, not an error.
func walkFiles(root string, ws walkSpec) (*scan, error) {
	s := &scan{}
	extraCapped := false
	start := filepath.Join(root, filepath.FromSlash(ws.start))
	if st, err := os.Lstat(start); err != nil || !st.IsDir() {
		return s, nil
	}
	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if err != nil {
			if rel == ws.start {
				return err
			}
			s.warnings = append(s.warnings, fmt.Sprintf("%s: %v", rel, err))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow a link out of the repo
		}
		if d.IsDir() {
			if skipDir(rel, d.Name()) {
				return fs.SkipDir
			}
			if rel != "." {
				if strings.Count(rel, "/")+1 > maxWalkDepth {
					s.warnings = append(s.warnings, fmt.Sprintf("%s: deeper than %d levels, skipped", rel, maxWalkDepth))
					return fs.SkipDir
				}
				if st, err := os.Stat(filepath.Join(path, "go.mod")); err == nil && st.Mode().IsRegular() {
					return fs.SkipDir // a nested module is another project
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		primary := ws.accept(d.Name())
		if !primary && (ws.alsoAccept == nil || !ws.alsoAccept(d.Name())) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			s.warnings = append(s.warnings, fmt.Sprintf("%s: %v", rel, err))
			return nil
		}
		if info.Size() > ws.maxBytes {
			s.warnings = append(s.warnings, fmt.Sprintf("%s: %d bytes, larger than %d, skipped", rel, info.Size(), ws.maxBytes))
			return nil
		}
		sf := sourceFile{rel: rel, abs: path, size: info.Size(), mtime: info.ModTime().UnixNano()}
		if !primary {
			// Read for facts only: past its own cap the rest is left out with a warning
			// (a reference may be missed), never a failed layer.
			switch {
			case len(s.extra) < ws.maxFiles:
				s.extra = append(s.extra, sf)
			case !extraCapped:
				extraCapped = true
				s.warnings = append(s.warnings, fmt.Sprintf("more than %d test files: the rest were not read for references", ws.maxFiles))
			}
			return nil
		}
		if len(s.files) >= ws.maxFiles {
			return fmt.Errorf("more than %d matching files under %s", ws.maxFiles, filepath.Join(root, ws.start))
		}
		s.files = append(s.files, sf)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(s.files, func(i, j int) bool { return s.files[i].rel < s.files[j].rel })
	sort.Slice(s.extra, func(i, j int) bool { return s.extra[i].rel < s.extra[j].rel })
	return s, nil
}

// statRegular describes a single repo-relative file as a layer input. It
// reports ok=false when the file is absent, is not a regular file, is a
// symlink, or is larger than max.
func statRegular(root, rel string, max int64) (sourceFile, bool) {
	abs := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(abs)
	if err != nil || !st.Mode().IsRegular() || st.Size() > max {
		return sourceFile{}, false
	}
	return sourceFile{rel: rel, abs: abs, size: st.Size(), mtime: st.ModTime().UnixNano()}, true
}

// isIndexedGoFile reports whether a file name is a Go source the code layer
// indexes. Test files add no nodes (see isGoTestFile), and *_templ.go is
// generated output of the .templ sources.
func isIndexedGoFile(name string) bool {
	if !strings.HasSuffix(name, ".go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return false
	}
	return !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, "_templ.go")
}

// isGoTestFile reports whether a file name is a Go test source. Tests are not
// indexed (they add no nodes or edges), but a function that only a test calls is
// referenced, so the code layer reads them for that fact alone.
func isGoTestFile(name string) bool {
	return strings.HasSuffix(name, "_test.go") && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "_")
}
