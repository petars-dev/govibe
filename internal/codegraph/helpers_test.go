package codegraph

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	"github.com/cayleygraph/quad"
	_ "github.com/cayleygraph/quad/nquads"

	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/profile"
)

const fixtureRoot = "testdata/fixture"

// repoRoot is the real repository, for the invariant tests.
const repoRoot = "../.."

// newTestWorld returns a world over root, as an explicit reindex would see it.
func newTestWorld(t *testing.T, root string, opts Options) *world {
	t.Helper()
	abs, err := resolveRoot(root)
	if err != nil {
		t.Fatalf("resolve %s: %v", root, err)
	}
	opts.Root = root
	return newWorld(context.Background(), New(nil, opts), abs, true)
}

// mustProfile is the profile a world reads the project through.
func mustProfile(t *testing.T, w *world) *profile.Profile {
	t.Helper()
	p, err := w.profile()
	if err != nil {
		t.Fatalf("profile of %s: %v", w.root, err)
	}
	return p
}

// loadProfileForTest reads root's govibe.yml over the default profile.
func loadProfileForTest(root string) (profile.Profile, error) { return profile.Load(root) }

// buildLayer runs one layer over a world and returns its sorted quads.
func buildLayer(t *testing.T, w *world, l layer) *layerOutput {
	t.Helper()
	out, err := l.build(context.Background(), w)
	if err != nil {
		t.Fatalf("%s layer over %s: %v", l.name(), w.root, err)
	}
	out.quads = sortedUnique(out.quads)
	return out
}

// buildCode runs the code layer over root and returns its sorted quads.
func buildCode(t *testing.T, root string) *layerOutput {
	t.Helper()
	return buildLayer(t, newTestWorld(t, root, Options{}), codeLayer{})
}

// nquads renders quads in the wire format goldens and exports use.
func nquads(t *testing.T, quads []quad.Quad) string {
	t.Helper()
	var buf bytes.Buffer
	w := quad.FormatByName("nquads").Writer(&buf)
	if _, err := w.WriteQuads(quads); err != nil {
		t.Fatalf("write quads: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// facts indexes quads for assertions.
type facts struct{ quads []quad.Quad }

func (f facts) out(subj, pred string) []quad.Value {
	var vs []quad.Value
	for _, q := range f.quads {
		if q.Subject == quad.Value(quad.IRI(subj)) && q.Predicate == quad.Value(quad.IRI(pred)) {
			vs = append(vs, q.Object)
		}
	}
	return vs
}

// one returns the single object of (subj, pred) or fails.
func (f facts) one(t *testing.T, subj, pred string) quad.Value {
	t.Helper()
	vs := f.out(subj, pred)
	if len(vs) != 1 {
		t.Fatalf("%s %s: want exactly one object, got %v", subj, pred, vs)
	}
	return vs[0]
}

func (f facts) has(subj, pred string, obj quad.Value) bool {
	for _, v := range f.out(subj, pred) {
		if v == obj {
			return true
		}
	}
	return false
}

func (f facts) subjects(kind quad.IRI) []string {
	var ids []string
	for _, q := range f.quads {
		if q.Predicate == quad.Value(PredType) && q.Object == quad.Value(kind) {
			ids = append(ids, string(q.Subject.(quad.IRI)))
		}
	}
	sort.Strings(ids)
	return ids
}

func (f facts) exists(subj string) bool {
	for _, q := range f.quads {
		if q.Subject == quad.Value(quad.IRI(subj)) {
			return true
		}
	}
	return false
}

// openStore opens a fresh embedded store, the way the dev server does.
func openStore(t *testing.T) (*cayley.Handle, string) {
	t.Helper()
	dir := t.TempDir()
	h, err := graphdb.OpenScratch(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h, dir
}

// gizmoIDs runs a Gizmo query the way the graph_query tool does and returns
// the "id" of every result, sorted.
func gizmoIDs(t *testing.T, h *cayley.Handle, q string) []string {
	t.Helper()
	ctx := context.Background()
	it, err := query.Execute(ctx, h.QuadStore, gizmo.Name, q, query.Options{Collation: query.JSON, Limit: 1000})
	if err != nil {
		t.Fatalf("gizmo %s: %v", q, err)
	}
	defer it.Close()
	var ids []string
	for it.Next(ctx) {
		m, _ := it.Result().(map[string]any)
		ids = append(ids, fmt.Sprint(m["id"]))
	}
	if err := it.Err(); err != nil {
		t.Fatalf("gizmo %s: %v", q, err)
	}
	sort.Strings(ids)
	return ids
}

func wantIDs(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// storedLabel reads every quad under a layer label straight from the store.
func storedLabel(t *testing.T, h *cayley.Handle, layer string) []quad.Quad {
	t.Helper()
	qs, err := newWriter(h.QuadStore).read(LayerLabel(layer))
	if err != nil {
		t.Fatalf("read %s: %v", layer, err)
	}
	return qs
}

// copyTree copies src into a fresh temp dir and returns it. A fixture that depends on the stub
// framework names it by a relative replace, which means nothing from a temp dir: it is pointed
// at the stub's real directory.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	defer func() {
		goMod := filepath.Join(dst, "go.mod")
		b, err := os.ReadFile(goMod)
		if err != nil || !strings.Contains(string(b), "=> ../stubfw") {
			return
		}
		stub, err := filepath.Abs("testdata/stubfw")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goMod, []byte(strings.ReplaceAll(string(b), "=> ../stubfw", "=> "+stub)), 0o644); err != nil {
			t.Fatal(err)
		}
	}()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
	return dst
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
