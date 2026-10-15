package codegraph

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/petars-dev/govibe/internal/profile"
)

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

	prof      memo[*profile.Profile]
	modsV     memo[*moduleTable]
	goFiles   memo[*scan]
	goFP      memo[string]
	codeIdx   memo[*codeIndex]
	routesV   memo[*routeTable]
	commandsV memo[*commandTable]
	callIdx   memo[*callIndex]
	gitHist   memo[*gitHistory]
	gitHead   memo[gitHeadInfo]

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

// profile is where the project keeps its settings, migrations and docs: the one the
// caller handed over, else the project's govibe.yml over the default. It is read once
// per run, so an edit to govibe.yml takes effect on the next one.
func (w *world) profile() (*profile.Profile, error) {
	return w.prof.get(func() (*profile.Profile, error) {
		if p := w.x.opts.Profile; p != nil {
			return p, nil
		}
		p, err := profile.Load(w.root)
		if err != nil {
			return nil, err
		}
		return &p, nil
	})
}

// modules is the table that says which module an import path belongs to, and at which
// version.
func (w *world) modules() *moduleTable {
	t, _ := w.modsV.get(func() (*moduleTable, error) {
		goMod := filepath.Join(w.root, "go.mod")
		module, _ := readModulePath(goMod)
		facts := w.x.opts.Build
		if facts.AppModule != "" {
			module = facts.AppModule
		}
		if facts.FrameworkModule == "" {
			if prof, err := w.profile(); err == nil {
				facts.FrameworkModule = prof.Framework // "" is the default
			}
		}
		return newModuleTable(module, facts, goMod), nil
	})
	return t
}

// describeProject names what was indexed: the application (its name when the binary
// said so, else the last element of its module path), its module, and the framework it
// is built on with the version in this build. A project that is the framework itself
// says so instead of giving a version.
func (w *world) describeProject() (app, appModule, frameworkModule, frameworkVersion string) {
	mods := w.modules()
	appModule, frameworkModule = mods.app, mods.framework
	app = w.x.opts.Build.AppName
	if app == "" && appModule != "" {
		app = appModule[strings.LastIndex(appModule, "/")+1:]
	}
	switch {
	case appModule == frameworkModule:
		frameworkVersion = "(this repository)"
	default:
		frameworkVersion = mods.versionOf(frameworkModule)
	}
	return app, appModule, frameworkModule, frameworkVersion
}

// profileFile is govibe.yml as a layer input: every layer reads the project through
// the profile, so an edit to it makes them all stale.
func (w *world) profileFile() []sourceFile {
	if w.x.opts.Profile != nil {
		return nil
	}
	if sf, ok := statRegular(w.root, profile.File, maxConfigBytes); ok {
		return []sourceFile{sf}
	}
	return nil
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
		// The tests are inputs too: an edit to one can change what counts as referenced.
		files := append(append([]sourceFile(nil), sc.files...), sc.extra...)
		if sf, ok := statRegular(w.root, "go.mod", maxConfigBytes); ok {
			files = append(files, sf)
		}
		files = append(files, w.profileFile()...)
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
		prof, err := w.profile()
		if err != nil {
			return nil, err
		}
		ci, err := buildCodeIndex(w.ctx, w.root, sc, prof, w.modules())
		if err != nil {
			return nil, err
		}
		w.x.codeCache.fp, w.x.codeCache.idx = fp, ci
		return ci, nil
	})
}
