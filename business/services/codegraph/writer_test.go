package codegraph

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
)

func iriQuad(s, p, o string) quad.Quad {
	return quad.Quad{Subject: quad.IRI(s), Predicate: quad.IRI(p), Object: quad.IRI(o), Label: LayerLabel("code")}
}

// Layers are rewritten with arbitrary adds and deletes against a small pool of
// nodes (so nodes are shared, which is what breaks a mixed batch, F11). After
// every replace, the stored label, and the predicate/object index Gizmo uses,
// must equal the intended set. A single mixed ApplyDeltas fails this in nearly
// every trial; the writer's add-then-delete split must never.
func TestReplaceNeverCorruptsTheStore(t *testing.T) {
	const trials, steps = 12, 5
	for trial := 0; trial < trials; trial++ {
		h, _ := openStore(t)
		w := newWriter(h.QuadStore)
		r := rand.New(rand.NewSource(int64(trial)))
		for step := 0; step < steps; step++ {
			want := map[string]quad.Quad{}
			for i, n := 0, r.Intn(16); i < n; i++ {
				q := iriQuad(fmt.Sprintf("s%d", r.Intn(6)), fmt.Sprintf("p%d", r.Intn(3)), fmt.Sprintf("o%d", r.Intn(4)))
				want[quadKey(q)] = q
			}
			var quads []quad.Quad
			for _, q := range want {
				quads = append(quads, q)
			}
			if _, err := w.replace(LayerLabel("code"), quads); err != nil {
				t.Fatalf("trial %d step %d: %v", trial, step, err)
			}
			for p := 0; p < 3; p++ {
				for o := 0; o < 4; o++ {
					var subjects []string
					for _, q := range want {
						if q.Predicate == quad.Value(quad.IRI(fmt.Sprintf("p%d", p))) && q.Object == quad.Value(quad.IRI(fmt.Sprintf("o%d", o))) {
							subjects = append(subjects, "<"+string(q.Subject.(quad.IRI))+">")
						}
					}
					got := gizmoIDs(t, h, fmt.Sprintf(`g.V().Has(g.IRI('p%d'), g.IRI('o%d')).All()`, p, o))
					wantIDs(t, fmt.Sprintf("trial %d step %d Has(p%d, o%d)", trial, step, p, o), got, subjects...)
				}
			}
		}
	}
}

// droppingStore applies deltas without error but loses some of them, the way
// the F11 bug does.
type droppingStore struct {
	graph.QuadStore
	dropAdds bool
}

func (d *droppingStore) ApplyDeltas(in []graph.Delta, opts graph.IgnoreOpts) error {
	if d.dropAdds {
		var kept []graph.Delta
		for i, delta := range in {
			if delta.Action == graph.Add && i%2 == 0 {
				continue
			}
			kept = append(kept, delta)
		}
		in = kept
	}
	return d.QuadStore.ApplyDeltas(in, opts)
}

// A store that loses writes without an error is caught by the read-back, and
// the layer reports it instead of claiming success.
func TestReplaceVerifiesWhatItWrote(t *testing.T) {
	h, _ := openStore(t)
	ds := &droppingStore{QuadStore: h.QuadStore, dropAdds: true}
	w := newWriter(ds)
	quads := []quad.Quad{iriQuad("s1", "p", "o1"), iriQuad("s2", "p", "o2"), iriQuad("s3", "p", "o3"), iriQuad("s4", "p", "o4")}
	_, err := w.replace(LayerLabel("code"), quads)
	if err == nil || !strings.Contains(err.Error(), "verify") || !strings.Contains(err.Error(), "indexes.bolt") {
		t.Fatalf("expected a verification failure that says how to recover, got %v", err)
	}

	// Verification failure is not an ApplyDeltas error: once the store behaves,
	// the next run re-diffs against what is really stored and heals the layer.
	ds.dropAdds = false
	d, err := w.replace(LayerLabel("code"), quads)
	if err != nil || d.Quads != 4 {
		t.Fatalf("healing run: %+v, %v", d, err)
	}
	var got []string
	for _, q := range storedLabel(t, h, "code") {
		got = append(got, q.Subject.String())
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "<s1> <s2> <s3> <s4>" {
		t.Errorf("layer after healing: %v", got)
	}
}
