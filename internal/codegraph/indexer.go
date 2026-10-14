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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/quad"
	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/dbexplorer"
	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// IndexerVersion is recorded on the build node. Bump it when the shape of any
// layer's output changes.
const IndexerVersion = "5"

// Layer run statuses, as stored on `layer:<name>`.
const (
	statusOK      = "ok"      // indexed every input
	statusPartial = "partial" // indexed, but some inputs were skipped (see warnings)
	statusError   = "error"   // nothing written; the layer's previous quads are untouched
	statusSkipped = "skipped" // not applicable here (e.g. no router wired); previous quads untouched
	statusOff     = "off"     // an opt-in layer that is not switched on: it holds no quads
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

	// Calls and Git switch on the opt-in layers: `calls` type-checks the module to
	// index which function calls which, `git` reads `git log`. Both cost more than
	// the layers that only parse, so they are off unless asked for; an explicit
	// RunLayers of one switches it on for the life of the Indexer. A layer that is
	// off holds no quads (whatever an earlier run left is removed) and reports
	// the status `off`.
	Calls bool
	Git   bool

	// Profile says where the project keeps its settings, migrations and docs and which
	// directory is which architecture layer. Nil reads <Root>/govibe.yml over the
	// default profile (a generated app's layout).
	Profile *profile.Profile

	// Build is what the running binary (or its `inspect --json`) knows about itself: the
	// modules it was built from, and which routes the framework registered. Optional:
	// without it versions come from go.mod and a route's origin from its path.
	Build BuildFacts

	// Manifest is what the framework publishes about itself (its settings, its MCP
	// tools): it lets the graph of a project that depends on the framework name the
	// framework's side without indexing it. Nil in the framework's own repository.
	Manifest *Manifest

	// RootHint is a function of the application as the Go runtime reports it,
	// normally its own Routes function. It says where the running binary's sources
	// were compiled, which the routes layer needs to map a handler's file onto the
	// code layer. Without it the indexer assumes it was itself compiled in the
	// module being indexed (this repository's own tests); an application that is a
	// module of its own has the framework, and so the indexer, as a dependency, so
	// the process hosting the dev tools must pass the hint.
	RootHint routeinfo.Ref

	// liveTTL overrides how long a live schema read is reused (tests).
	liveTTL time.Duration

	// RouteSource replaces the router walk: it feeds the routes layer the routes of a
	// project that is not being run here, as its own binary reported them (see
	// OptionsFromInspect), or, in tests, a table made up for the purpose.
	RouteSource func() ([]routeinfo.Route, error)
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

// optInLayer is implemented by a layer that is off unless it is asked for: it
// costs more than parsing (see Options.Calls and Options.Git). optIn says how to
// switch it on, for the note a layer that is off carries.
type optInLayer interface{ optIn() string }

// settlingLayer is implemented by a layer whose fingerprint also covers
// something only its own build discovers (the docs layer: which files the
// documents name, and whether they exist). The fingerprint taken before the
// build cannot include that, so after the layer builds the indexer takes it
// again, from what the build recorded, which is what the next freshness check
// will compute if nothing has changed since.
type settlingLayer interface{ settles() }

type layerOutput struct {
	quads    []quad.Quad
	warnings []string // inputs that were skipped; makes the run "partial"
	skipped  string   // why the layer does not apply; nothing is written, status "skipped"
	clear    bool     // with skipped: also remove what the layer wrote before
	note     string   // what a reader of the layer should know about how it was built (the calls layer's build tags)
}

// Indexer builds the graph layers and writes them into one quad store.
type Indexer struct {
	opts   Options
	writer *writer
	layers []layer

	mu       sync.Mutex // one run at a time: boot, HTTP reindex, page loads and agents overlap
	state    map[string]LayerReport
	enabled  map[string]bool // the opt-in layers that are switched on
	gitCache struct {
		head string
		hist *gitHistory
	}
	codeCache struct {
		fp  string
		idx *codeIndex
	}
	callCache struct {
		fp  string
		idx *callIndex
	}
	liveCache struct {
		at  time.Time
		res *liveSchema
	}
	// docProbed is what the docs layer's last build learned about the disk: the
	// repo-relative paths its documents name, and whether each was there.
	docProbed map[string]bool
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
		layers: []layer{codeLayer{}, routesLayer{}, templatesLayer{}, configLayer{}, mcpLayer{}, dbLayer{}, docsLayer{}, callsLayer{}, gitLayer{}, externalsLayer{}, findingsLayer{}},
		state:  map[string]LayerReport{},
		enabled: map[string]bool{
			callsLayerName: opts.Calls,
			gitLayerName:   opts.Git,
		},
	}
}

// on reports whether a layer takes part in runs: every layer does, except an
// opt-in one that has not been switched on.
func (x *Indexer) on(l layer) bool {
	if _, optIn := l.(optInLayer); !optIn {
		return true
	}
	return x.enabled[l.name()]
}

// Enabled reports whether the named layer takes part in runs: false for an
// opt-in layer (`calls`, `git`) that has not been switched on, and for a name
// that is no layer.
func (x *Indexer) Enabled(name string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, l := range x.layers {
		if l.name() == name {
			return x.on(l)
		}
	}
	return false
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
	// What was indexed: the application, and the framework it is built on.
	App              string `json:"app,omitempty"`
	AppModule        string `json:"app_module,omitempty"`
	FrameworkModule  string `json:"framework_module,omitempty"`
	FrameworkVersion string `json:"framework_version,omitempty"`

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
	return x.run(ctx, true, nil)
}

// Model reads the graph the indexer wrote into a Model, as the agent tools and
// the generated reports work from it, for a scope (everything when none is given). It
// does not bring stale layers up to date; call Refresh first for that.
func (x *Indexer) Model(ctx context.Context, scope ...Scope) (*Model, error) {
	return Load(ctx, x.writer.qs, scope...)
}

// RootDir is the absolute, symlink-free repository root the indexer reads, or ""
// when the configured root cannot be resolved.
func (x *Indexer) RootDir() string {
	root, err := resolveRoot(x.opts.Root)
	if err != nil {
		return ""
	}
	return root
}

// LayerNames are the layers the indexer builds, in build order (the `meta` layer
// that records the runs is written after every run and is not one of them).
func (x *Indexer) LayerNames() []string {
	names := make([]string, len(x.layers))
	for i, l := range x.layers {
		names[i] = l.name()
	}
	return names
}

// RunLayers rebuilds the named layers whatever their fingerprints say, the
// layers built on them (they resolve against what the named ones wrote, so a
// rebuilt `code` layer leaves `routes` pointing at functions that may be gone),
// and any other layer that is stale. With no names it is Run. A name may carry
// the `layer:` prefix; an unknown name is an error that lists the real ones, and
// nothing runs.
func (x *Indexer) RunLayers(ctx context.Context, names []string) (*Report, error) {
	if len(names) == 0 {
		return x.run(ctx, true, nil)
	}
	only := map[string]bool{}
	for _, n := range names {
		name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(n), labelPrefix))
		if !slices.Contains(x.LayerNames(), name) {
			return nil, fmt.Errorf("unknown layer %q; layers: %s", n, strings.Join(x.LayerNames(), ", "))
		}
		only[name] = true
	}
	// Asking for an opt-in layer by name is how it is switched on.
	x.mu.Lock()
	for _, l := range x.layers {
		if _, optIn := l.(optInLayer); optIn && only[l.name()] {
			x.enabled[l.name()] = true
		}
	}
	x.mu.Unlock()
	// Layers are listed in build order, so one pass finds every dependent.
	for _, l := range x.layers {
		for _, d := range l.deps() {
			if only[d] {
				only[l.name()] = true
			}
		}
	}
	return x.run(ctx, false, only)
}

// Refresh rebuilds only the layers that are stale: those whose inputs, or
// whose dependencies' inputs, changed since they were last built, and any
// layer not built yet. It returns (nil, nil) when everything is fresh. Calling
// it on every /dev/graph page load and graph MCP call is what makes edits to
// docs/**, which the dev server's file watcher ignores, show up without a
// restart. A layer that failed is retried only once its inputs change; an
// explicit Run retries it at once.
func (x *Indexer) Refresh(ctx context.Context) (*Report, error) {
	return x.run(ctx, false, nil)
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
	if _, err := w.profile(); err != nil {
		return nil, fmt.Errorf("graph index: %w", err)
	}
	fps := x.fingerprints(w)
	var stale []string
	for _, l := range x.layers {
		if x.isStale(l, fps[l.name()]) {
			stale = append(stale, l.name())
		}
	}
	return stale, nil
}

// run rebuilds every layer when force is set, else the layers in only and the
// stale ones. A run that names layers ignores cached indexes, as an explicit
// reindex does.
func (x *Indexer) run(ctx context.Context, force bool, only map[string]bool) (*Report, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	start := time.Now()
	root, err := resolveRoot(x.opts.Root)
	if err != nil {
		return nil, fmt.Errorf("graph index: root %q: %w", x.opts.Root, err)
	}

	w := newWorld(ctx, x, root, force || only != nil)
	if _, err := w.profile(); err != nil {
		return nil, fmt.Errorf("graph index: %w", err)
	}
	// Fingerprints are taken before any input is read, so an edit that lands
	// mid-run leaves the stored fingerprint older than the content and the
	// layer reads as stale on the next check, never falsely fresh.
	fps := x.fingerprints(w)
	toRun := map[string]bool{}
	for _, l := range x.layers {
		if !x.on(l) {
			// A layer that is off is recorded (and its leftovers removed) once per
			// process, then left alone: it has nothing to rebuild, and a reindex of
			// what it would depend on is not news about it.
			if _, recorded := x.state[l.name()]; !recorded {
				toRun[l.name()] = true
			}
			continue
		}
		if force || only[l.name()] || x.isStale(l, fps[l.name()]) {
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
		if _, ok := l.(settlingLayer); ok && (lr.Status == statusOK || lr.Status == statusPartial) {
			// Every scan is memoized in the world, so this reuses the stats taken
			// before any input was read, and adds what the build found out.
			w.settling = true
			fps = x.fingerprints(w)
			w.settling = false
			lr.Fingerprint = fps[l.name()].fp
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
	rep.Build.App, rep.Build.AppModule, rep.Build.FrameworkModule, rep.Build.FrameworkVersion = w.describeProject()
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
		if !x.on(l) {
			// Off is one state, whatever the sources do: nothing to go stale.
			out[l.name()] = fpResult{fp: statusOff}
			continue
		}
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
		// Every layer reads the project through the profile, so govibe.yml is an input of all.
		h := sha256.New()
		fmt.Fprintf(h, "%s\nprofile=%s\n", own, fingerprint(w.profileFile()))
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

	if oi, optIn := l.(optInLayer); optIn && !x.on(l) {
		// An opt-in layer that is off holds nothing: an earlier run, or an earlier
		// process that had it on, may have left quads that no longer describe the
		// code, and a reader must not mistake them for current ones.
		d, err := x.writer.replace(LayerLabel(l.name()), nil)
		if err != nil {
			lr.Error = err.Error()
			return lr
		}
		lr.Status, lr.Note, lr.Removed = statusOff, oi.optIn(), d.Removed
		return lr
	}
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
		if out.clear {
			// The layer has nothing to say here (say, the root is not a git work
			// tree), and what it said before is no longer true.
			d, err := x.writer.replace(LayerLabel(l.name()), nil)
			if err != nil {
				lr.Status, lr.Error = statusError, err.Error()
				return lr
			}
			lr.Removed = d.Removed
		}
		return lr
	}

	d, err := x.writer.replace(LayerLabel(l.name()), out.quads)
	if err != nil {
		lr.Error = err.Error()
		return lr
	}
	lr.Status, lr.Quads, lr.Added, lr.Removed = statusOK, d.Quads, d.Added, d.Removed
	lr.Note = out.note
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
