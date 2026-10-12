package codegraph

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/graph/shape"
	"github.com/cayleygraph/quad"
)

// writer replaces the quads of one layer label at a time. It follows the
// "Writer contract" in docs/public/GRAPH_SCHEMA.md:
//
//   - it only ever reads, deletes or adds quads under a `layer:*` IRI label, so
//     unlabelled quads, quads with another label and quads with a *string* label
//     (what the /dev/graph "Add a quad" form writes) are never touched;
//   - it diffs the new set against the stored one on the full quad and writes
//     only the difference, as two tolerant ApplyDeltas calls (IgnoreDup and
//     IgnoreMissing, never ApplyTransaction): the adds first, then the deletes.
//     One mixed batch is not safe. In this Cayley version a batch whose deletes
//     outnumber its adds can silently drop the adds and leave nodes without
//     names (F11), so a mixed diff corrupted the layer in nearly every
//     randomized trial. The split is not atomic, but a layer is derived data and
//     the next run re-diffs against what is stored, so a failure between the two
//     calls leaves a superset that the next run trims;
//   - it reads the layer back after writing and fails the layer if the store
//     does not hold exactly what was written, so a store bug is reported
//     instead of trusted;
//   - after a failed write it refuses all further writes: a failed strict
//     transaction leaves the store's node cache lying about which nodes exist
//     (F8), and only a restart clears that.
type writer struct {
	qs graph.QuadStore

	mu     sync.Mutex
	broken error
}

func newWriter(qs graph.QuadStore) *writer { return &writer{qs: qs} }

// diff is the size of one replace: what the store holds now, and what changed.
type diff struct {
	Quads   int
	Added   int
	Removed int
}

var errWriterBroken = errors.New("graph writer disabled after an earlier write error; restart the server")

// replace makes the quads under label equal to want. Every quad in want must
// carry label.
func (w *writer) replace(label quad.IRI, want []quad.Quad) (diff, error) {
	if !strings.HasPrefix(string(label), labelPrefix) {
		return diff{}, fmt.Errorf("refusing to write under label %q: not a %s* label", label, labelPrefix)
	}
	for _, q := range want {
		if q.Label != quad.Value(label) {
			return diff{}, fmt.Errorf("quad %v does not carry label %s", q, label)
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken != nil {
		return diff{}, fmt.Errorf("%w (first error: %v)", errWriterBroken, w.broken)
	}

	want = sortedUnique(want)
	have, err := w.read(label)
	if err != nil {
		return diff{}, err
	}

	haveKeys := make(map[string]bool, len(have))
	for _, q := range have {
		haveKeys[quadKey(q)] = true
	}
	wantKeys := make(map[string]bool, len(want))
	for _, q := range want {
		wantKeys[quadKey(q)] = true
	}

	var adds, dels []graph.Delta
	d := diff{Quads: len(want)}
	for _, q := range have {
		if !wantKeys[quadKey(q)] {
			dels = append(dels, graph.Delta{Quad: q, Action: graph.Delete})
			d.Removed++
		}
	}
	for _, q := range want {
		if !haveKeys[quadKey(q)] {
			adds = append(adds, graph.Delta{Quad: q, Action: graph.Add})
			d.Added++
		}
	}
	if len(adds)+len(dels) == 0 {
		return d, nil
	}

	for _, batch := range []struct {
		what   string
		deltas []graph.Delta
	}{{"add", adds}, {"delete", dels}} {
		if len(batch.deltas) == 0 {
			continue
		}
		if err := w.qs.ApplyDeltas(batch.deltas, graph.IgnoreOpts{IgnoreDup: true, IgnoreMissing: true}); err != nil {
			w.broken = err
			return diff{}, fmt.Errorf("%s %d quads under %s: %w", batch.what, len(batch.deltas), label, err)
		}
	}

	got, err := w.read(label)
	if err != nil {
		return diff{}, fmt.Errorf("verify %s: %w", label, err)
	}
	if missing, extra := compareKeys(want, got); missing+extra != 0 {
		return diff{}, fmt.Errorf("verify %s: the store holds %d fewer and %d more quads than were written; "+
			"the graph store is damaged, so delete docs/graph/indexes.bolt (derived data; it also drops hand-added quads) and restart",
			label, missing, extra)
	}
	return d, nil
}

// compareKeys counts the quads of want that are absent from got, and the quads
// of got that are not in want. Both inputs are sorted and unique.
func compareKeys(want, got []quad.Quad) (missing, extra int) {
	gotKeys := make(map[string]bool, len(got))
	for _, q := range got {
		gotKeys[quadKey(q)] = true
	}
	for _, q := range want {
		if gotKeys[quadKey(q)] {
			delete(gotKeys, quadKey(q))
		} else {
			missing++
		}
	}
	return missing, len(gotKeys)
}

// read returns the quads currently stored under the IRI label, sorted.
func (w *writer) read(label quad.IRI) ([]quad.Quad, error) {
	it := shape.FilterQuads(nil, nil, nil, []quad.Value{label}).BuildIterator(w.qs)
	rd := graph.NewResultReader(w.qs, it)
	defer rd.Close()

	var out []quad.Quad
	for {
		q, err := rd.ReadQuad()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", label, err)
		}
		if !q.IsValid() {
			// A node whose name cannot be resolved: the F8 symptom.
			return nil, fmt.Errorf("read %s: store returned a quad with an unresolved node; restart the server", label)
		}
		out = append(out, q)
	}
	return sortedUnique(out), nil
}

// sortedUnique orders quads by subject, predicate, object and drops repeats.
// The result does not alias the input.
func sortedUnique(in []quad.Quad) []quad.Quad {
	out := make([]quad.Quad, len(in))
	copy(out, in)
	keys := make([]string, len(out))
	idx := make([]int, len(out))
	for i, q := range out {
		keys[i] = quadKey(q)
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })

	res := make([]quad.Quad, 0, len(out))
	last := ""
	for n, i := range idx {
		if n > 0 && keys[i] == last {
			continue
		}
		last = keys[i]
		res = append(res, out[i])
	}
	return res
}

// quadKey is a comparable, order-defining form of a whole quad, label
// included. Term String() forms keep IRIs, strings and typed literals apart.
func quadKey(q quad.Quad) string {
	return q.Subject.String() + "\x00" + q.Predicate.String() + "\x00" + q.Object.String() + "\x00" + q.Label.String()
}
