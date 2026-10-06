// Package codegraph fills the embedded Cayley graph store (/dev/graph, the
// graph_query MCP tool) with a derived, metadata-only model of the project
// itself. Each layer (code, routes, templates, config, mcp, docs, ...) builds
// its quads in memory and the writer replaces only the quads carrying that
// layer's label, so manual quads survive every reindex. The vocabulary is
// defined in vocab.go and documented in docs/public/GRAPH_SCHEMA.md; the plan is
// docs/specs/SPEC_GRAPH_INDEXER.md.
//
// A layer is rebuilt on boot, on an explicit reindex, and lazily when its
// fingerprint (its own inputs plus those of the layers it depends on) no longer
// matches the one it was last built from; see Refresh.
//
// Dev-only: nothing here is wired outside routes.go's !cfg.IsProd() block.
package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"petars-dev/govibe/storage/dbexplorer"
)

// IndexerVersion is recorded on the build node. Bump it when the shape of any
// layer's output changes.
const IndexerVersion = "3"

// Layer run statuses, as stored on `layer:<name>`.
const (
	statusOK      = "ok"      // indexed every input
	statusPartial = "partial" // indexed, but some inputs were skipped (see warnings)
	statusError   = "error"   // nothing written; the layer's previous quads are untouched
	statusSkipped = "skipped" // not applicable here (e.g. no router wired); previous quads untouched
)

// Options configure an Indexer.
type Options struct {
	// Root is the repository root: the directory holding go.mod. Default ".".
	Root string

	// Router is the live router the routes layer walks. It is read when a
	// layer is built, so it may still be empty when the Indexer is created as
	// long as it is fully wired before the first run. Nil means no router is
	// available (an offline run): the routes layer is then skipped and its
	// stored quads are left alone.
	Router chi.Routes

	// Schema reads the live database's schema (dbexplorer's Explorer.Snapshot).
	// Nil means there is no database: the db layer then describes the
	// migrations alone. A failing Schema does not fail the layer; it falls back
	// to the migrations and says so.
	Schema func(ctx context.Context) (*dbexplorer.Snapshot, error)

	// liveTTL overrides how long a live schema read is reused (tests).
	liveTTL time.Duration

	// routeSource replaces the router walk. Tests use it to feed the routes
	// layer runtime facts for a project that cannot be run.
	routeSource func() ([]runtimeRoute, error)
	// buildRoot overrides where the running binary's sources were compiled.
	buildRoot *string
}

// layer is one independently indexed slice of the graph. Layers never see each
// other's quads: a layer that points at another layer's nodes resolves them
// through the world, from the same indexes the other layer is built from.
type layer interface {
	name() string
	// deps are the layers whose nodes this layer points at. A layer is stale
	// when it or any dependency is.
	deps() []string
	// fingerprint cheaply identifies the layer's own inputs (path, mtime and
	// size, no content reads); it changes exactly when an input is added,
	// removed or edited.
	fingerprint(w *world) (string, error)
	// build returns the layer's complete quad set, all under LayerLabel(name()).
	build(ctx context.Context, w *world) (*layerOutput, error)
}

type layerOutput struct {
	quads    []quad.Quad
	warnings []string // inputs that were skipped; makes the run "partial"
	skipped  string   // why the layer does not apply; nothing is written, status "skipped"
}

// Indexer builds the graph layers and writes them into one quad store.
type Indexer struct {
	opts   Options
	writer *writer
	layers []layer

	mu        sync.Mutex // one run at a time: boot, HTTP reindex, page loads and agents overlap
	state     map[string]LayerReport
	codeCache struct {
		fp  string
		idx *codeIndex
	}
	liveCache struct {
		at  time.Time
		res *liveSchema
	}
}

// New returns an Indexer over qs, which it only ever writes under `layer:*`
// labels.
func New(qs graph.QuadStore, opts Options) *Indexer {
	if opts.Root == "" {
		opts.Root = "."
	}
	return &Indexer{
		opts:   opts,
		writer: newWriter(qs),
		layers: []layer{codeLayer{}, routesLayer{}, templatesLayer{}, configLayer{}, mcpLayer{}, dbLayer{}, docsLayer{}},
		state:  map[string]LayerReport{},
	}
}

// Report is the outcome of one run.
type Report struct {
	Build   BuildReport   `json:"build"`
	Quads   int           `json:"quads"`   // quads across the data layers as of now
	Added   int           `json:"added"`   // written by the layers that ran
	Removed int           `json:"removed"` // deleted by the layers that ran
	Layers  []LayerReport `json:"layers"`  // every layer; Ran says which this call rebuilt
}

// BuildReport mirrors the `build:latest` node.
type BuildReport struct {
	GitSHA         string    `json:"git_sha,omitempty"`
	Dirty          bool      `json:"dirty,omitempty"`
	IndexedAt      time.Time `json:"indexed_at"`
	DurationMS     int       `json:"duration_ms"`
	IndexerVersion string    `json:"indexer_version"`
}

// LayerReport mirrors one `layer:<name>` node.
type LayerReport struct {
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Ran         bool      `json:"ran"` // rebuilt by this call (false: still fresh, left as it was)
	Quads       int       `json:"quads"`
	Added       int       `json:"added"`
	Removed     int       `json:"removed"`
	DurationMS  int       `json:"duration_ms"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	IndexedAt   time.Time `json:"indexed_at"`
	Warnings    []string  `json:"warnings,omitempty"`
	Note        string    `json:"note,omitempty"` // why a skipped layer does not apply
	Error       string    `json:"error,omitempty"`
}

// Failed reports whether any layer ended in error.
func (r *Report) Failed() bool {
	for _, l := range r.Layers {
		if l.Status == statusError {
			return true
		}
	}
	return false
}

// Run rebuilds every layer and then the meta layer. A layer that fails is
// reported and logged and leaves its previous quads in place; the other layers
// still run. The returned error is non-nil only when the run could not be
// completed at all (the root is unreadable, the context is cancelled, or the
// meta layer could not be written), and the Report is then partial or nil.
func (x *Indexer) Run(ctx context.Context) (*Report, error) {
	return x.run(ctx, true)
}

// Refresh rebuilds only the layers that are stale: those whose inputs, or
// whose dependencies' inputs, changed since they were last built, and any
// layer not built yet. It returns (nil, nil) when everything is fresh. Calling
// it on every /dev/graph page load and graph MCP call is what makes edits to
// docs/**, which the dev server's file watcher ignores, show up without a
// restart. A layer that failed is retried only once its inputs change; an
// explicit Run retries it at once.
func (x *Indexer) Refresh(ctx context.Context) (*Report, error) {
	return x.run(ctx, false)
}

// Stale lists the layers Refresh would rebuild, in build order.
func (x *Indexer) Stale(ctx context.Context) ([]string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	root, err := resolveRoot(x.opts.Root)
	if err != nil {
		return nil, fmt.Errorf("graph index: root %q: %w", x.opts.Root, err)
	}
	w := newWorld(ctx, x, root, false)
	fps := x.fingerprints(w)
	var stale []string
	for _, l := range x.layers {
		if x.isStale(l, fps[l.name()]) {
			stale = append(stale, l.name())
		}
	}
	return stale, nil
}

func (x *Indexer) run(ctx context.Context, force bool) (*Report, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	start := time.Now()
	root, err := resolveRoot(x.opts.Root)
	if err != nil {
		return nil, fmt.Errorf("graph index: root %q: %w", x.opts.Root, err)
	}

	w := newWorld(ctx, x, root, force)
	// Fingerprints are taken before any input is read, so an edit that lands
	// mid-run leaves the stored fingerprint older than the content and the
	// layer reads as stale on the next check, never falsely fresh.
	fps := x.fingerprints(w)
	toRun := map[string]bool{}
	for _, l := range x.layers {
		if force || x.isStale(l, fps[l.name()]) {
			toRun[l.name()] = true
		}
	}
	if len(toRun) == 0 {
		return nil, nil
	}

	rep := &Report{}
	for _, l := range x.layers {
		if !toRun[l.name()] {
			continue
		}
		lr := x.runLayer(ctx, l, w, fps[l.name()])
		if err := ctx.Err(); err != nil {
			return rep, err // cancelled: do not record a half-finished run
		}
		x.state[l.name()] = lr
		rep.Added += lr.Added
		rep.Removed += lr.Removed
	}
	for _, l := range x.layers {
		lr := x.state[l.name()]
		if lr.Status == statusOK || lr.Status == statusPartial {
			rep.Quads += lr.Quads
		}
		if !toRun[l.name()] {
			lr.Added, lr.Removed = 0, 0 // unchanged by this call
		}
		lr.Ran = toRun[l.name()]
		rep.Layers = append(rep.Layers, lr)
	}

	git := readGit(ctx, root)
	rep.Build = BuildReport{
		GitSHA:         git.SHA,
		Dirty:          git.Dirty,
		IndexedAt:      now(),
		DurationMS:     int(time.Since(start).Milliseconds()),
		IndexerVersion: IndexerVersion,
	}
	// The meta layer records each layer's own last run, not this call's view.
	var layers []LayerReport
	for _, l := range x.layers {
		layers = append(layers, x.state[l.name()])
	}
	if _, err := x.writer.replace(LayerLabel(metaLayer), metaQuads(rep, layers, git)); err != nil {
		return rep, fmt.Errorf("graph index: write meta layer: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"quads": rep.Quads, "added": rep.Added, "removed": rep.Removed,
		"duration_ms": rep.Build.DurationMS, "failed": rep.Failed(), "full": force,
	}).Info("graph index: done")
	return rep, nil
}

// fpResult is a layer's combined fingerprint, or why it could not be taken.
type fpResult struct {
	fp  string
	err error
}

// fingerprints computes every layer's combined fingerprint: its own inputs plus
// the fingerprints of the layers it depends on, in build order.
func (x *Indexer) fingerprints(w *world) map[string]fpResult {
	out := map[string]fpResult{}
	for _, l := range x.layers {
		own, err := safeFingerprint(l, w)
		deps := l.deps()
		if err == nil {
			for _, d := range deps {
				if r := out[d]; r.err != nil {
					err = fmt.Errorf("dependency %s: %w", d, r.err)
					break
				}
			}
		}
		if err != nil {
			out[l.name()] = fpResult{err: err}
			continue
		}
		if len(deps) == 0 {
			out[l.name()] = fpResult{fp: own}
			continue
		}
		h := sha256.New()
		fmt.Fprintf(h, "%s\n", own)
		for _, d := range deps {
			fmt.Fprintf(h, "%s=%s\n", d, out[d].fp)
		}
		out[l.name()] = fpResult{fp: hex.EncodeToString(h.Sum(nil))[:16]}
	}
	return out
}

func safeFingerprint(l layer, w *world) (fp string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return l.fingerprint(w)
}

// isStale reports whether a layer must be rebuilt: it has not been built, or
// its inputs changed. A layer that failed is not retried until they do.
func (x *Indexer) isStale(l layer, fp fpResult) bool {
	st, ok := x.state[l.name()]
	if !ok {
		return true
	}
	if fp.err != nil {
		return st.Status != statusError
	}
	return fp.fp != st.Fingerprint
}

// runLayer builds and writes one layer. It never panics and never returns an
// error: failure is the returned report's status.
func (x *Indexer) runLayer(ctx context.Context, l layer, w *world, fp fpResult) (lr LayerReport) {
	start := time.Now()
	lr = LayerReport{Name: l.name(), Status: statusError, Fingerprint: fp.fp}
	log := logrus.WithField("layer", l.name())
	defer func() {
		if r := recover(); r != nil {
			lr.Status, lr.Error = statusError, fmt.Sprintf("panic: %v", r)
		}
		lr.DurationMS = int(time.Since(start).Milliseconds())
		lr.IndexedAt = now()
		switch {
		case ctx.Err() != nil:
			// Cancelled by the caller; run discards this report.
		case lr.Status == statusError:
			log.WithField("duration_ms", lr.DurationMS).Warnf("graph index: layer failed, previous quads kept: %s", lr.Error)
		case lr.Status == statusSkipped:
			log.Infof("graph index: layer skipped: %s", lr.Note)
		default:
			f := log.WithFields(logrus.Fields{
				"quads": lr.Quads, "added": lr.Added, "removed": lr.Removed, "duration_ms": lr.DurationMS,
			})
			if lr.Status == statusPartial {
				f.Warnf("graph index: layer indexed with %d skipped inputs: %s", len(lr.Warnings), summarize(lr.Warnings))
			} else {
				f.Info("graph index: layer indexed")
			}
		}
	}()

	if fp.err != nil {
		lr.Error = fp.err.Error()
		return lr
	}
	out, err := l.build(ctx, w)
	if err != nil {
		lr.Error = err.Error()
		return lr
	}
	if err := ctx.Err(); err != nil {
		lr.Error = err.Error()
		return lr
	}
	if out.skipped != "" {
		lr.Status, lr.Note = statusSkipped, out.skipped
		return lr
	}

	d, err := x.writer.replace(LayerLabel(l.name()), out.quads)
	if err != nil {
		lr.Error = err.Error()
		return lr
	}
	lr.Status, lr.Quads, lr.Added, lr.Removed = statusOK, d.Quads, d.Added, d.Removed
	if len(out.warnings) > 0 {
		lr.Status, lr.Warnings = statusPartial, out.warnings
	}
	return lr
}

// now is the clock for recorded timestamps: UTC and whole seconds, so a value
// read back from the store compares equal to the one written.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func summarize(msgs []string) string {
	const show = 3
	if len(msgs) <= show {
		return strings.Join(msgs, "; ")
	}
	return strings.Join(msgs[:show], "; ") + fmt.Sprintf("; and %d more", len(msgs)-show)
}
