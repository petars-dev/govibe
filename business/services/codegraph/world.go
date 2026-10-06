package codegraph

import "context"

// memo caches the result of one computation for the lifetime of a world.
type memo[T any] struct {
	done bool
	v    T
	err  error
}

func (m *memo[T]) get(f func() (T, error)) (T, error) {
	if !m.done {
		m.v, m.err = f()
		m.done = true
	}
	return m.v, m.err
}

// world is the shared, lazily computed input of one run. Layers ask it for the
// indexes of the layers they depend on (the code index, the route table, the
// templates, the config keys), so each is built at most once per run and a
// dependent layer always resolves against exactly what its dependency wrote.
// A world is used by one goroutine and discarded after the run.
type world struct {
	ctx   context.Context
	x     *Indexer
	root  string
	force bool // an explicit reindex: ignore cached indexes

	// settling is set while the indexer re-takes the fingerprints of layers that
	// learn something by building (see settlingLayer): they then report what the
	// build recorded instead of looking at the disk again.
	settling bool

	goFiles memo[*scan]
	goFP    memo[string]
	codeIdx memo[*codeIndex]
	routesV memo[*routeTable]
	callIdx memo[*callIndex]
	gitHist memo[*gitHistory]
	gitHead memo[gitHeadInfo]

	templFiles memo[*scan]
	templIdx   memo[*templIndex]
	cfgIdx     memo[*configIndex]
	docFiles   memo[*scan]
	docIdx     memo[*docsIndex]

	migFiles memo[*scan]
	liveV    memo[*liveSchema]
	dbIdx    memo[*dbModel]
}

func newWorld(ctx context.Context, x *Indexer, root string, force bool) *world {
	return &world{ctx: ctx, x: x, root: root, force: force}
}

// goScan lists the Go sources of the repo, once per run.
func (w *world) goScan() (*scan, error) {
	return w.goFiles.get(func() (*scan, error) { return scanGo(w.root) })
}

// codeFingerprint is the code layer's own fingerprint: the Go sources plus
// go.mod, which names the module.
func (w *world) codeFingerprint() (string, error) {
	return w.goFP.get(func() (string, error) {
		sc, err := w.goScan()
		if err != nil {
			return "", err
		}
		files := append([]sourceFile(nil), sc.files...)
		if sf, ok := statRegular(w.root, "go.mod", maxConfigBytes); ok {
			files = append(files, sf)
		}
		return fingerprint(files), nil
	})
}

// code returns the code index. An index built for the same fingerprint by an
// earlier run is reused, except on an explicit reindex.
func (w *world) code() (*codeIndex, error) {
	return w.codeIdx.get(func() (*codeIndex, error) {
		fp, err := w.codeFingerprint()
		if err != nil {
			return nil, err
		}
		if c := w.x.codeCache; !w.force && c.idx != nil && c.fp == fp {
			return c.idx, nil
		}
		sc, err := w.goScan()
		if err != nil {
			return nil, err
		}
		ci, err := buildCodeIndex(w.ctx, w.root, sc)
		if err != nil {
			return nil, err
		}
		w.x.codeCache.fp, w.x.codeCache.idx = fp, ci
		return ci, nil
	})
}
