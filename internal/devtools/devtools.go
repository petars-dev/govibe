// Package devtools mounts the development tooling on a finished router: the docs
// pages, the project graph and its explorer, the database explorer, the MCP server
// that fronts them, and the indexer that fills the graph. None of it exists in a
// production process.
//
// It depends on the ports it declares itself (Deps), never on the kernel or an
// application package, so the same code can be hosted by another process later.
package devtools

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/cayleygraph/cayley/graph"
	cayleyhttp "github.com/cayleygraph/cayley/server/http"
	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/dbexplorer"
	"github.com/petars-dev/govibe/internal/devmcp"
	"github.com/petars-dev/govibe/internal/devtools/devui"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/web"
)

// Deps is everything the dev tools need from the process that hosts them.
type Deps struct {
	// Router is the application's finished router: the tools register their own
	// routes on it and the graph's routes layer reads it.
	Router chi.Router
	// DB is the application's one database pool, nil when no DSN is configured.
	// The explorer shares it.
	DB *sqlx.DB
	// Config supplies the dev.* settings.
	Config config.Config
	// Root is the application's root directory: the graph indexes it and the graph
	// store lives under it.
	Root string
	// RootHint is the application's own Routes function as the Go runtime reports it
	// (zero when it has none): it tells the graph where the running binary's sources
	// were compiled, so route handlers resolve to the application's code even when
	// the application is a module of its own and the framework a dependency.
	RootHint routeinfo.Ref
}

// Mount registers the dev tools and starts the indexer. It never fails the
// process: a part that cannot start is logged and left out. The returned function
// stops the indexer and releases the graph store; call it when the process shuts
// down (it is safe to call once, after the server has stopped).
func Mount(d Deps) (stop func()) {
	r := d.Router
	cfg := d.Config

	// The panel's own files: stylesheets, scripts, the graph renderer, the logo.
	r.Group(func(g chi.Router) {
		g.Use(web.DevNoCache)
		g.Handle(web.StaticPrefix+"/*", http.StripPrefix(web.StaticPrefix, http.FileServerFS(devui.Static())))
	})

	// Dev docs routes (renders Markdown docs to HTML)
	r.Route("/dev/docs", func(dd chi.Router) {
		dd.Use(devui.DocsRoot(d.Root)) // read the project's docs, wherever the process started
		dd.Get("/app", devui.DevDocsAppIndex)
		dd.Get("/app/{slug}", devui.DevDocsAppFile)
		dd.Get("/specs", devui.DevDocsSpecs)
		dd.Get("/specs/{slug}", devui.DevDocsSpecFile)
	})

	var quadStore graph.QuadStore
	var closeStore func()

	// Embedded Cayley graph store + query console. Backed by a bolt file under
	// docs/graph.
	graphDir := filepath.Join(d.Root, graphdb.DefaultDir)
	if handle, err := graphdb.Open(graphDir); err != nil {
		logrus.WithError(err).Error("graph store: failed to open, /dev/graph disabled")
	} else {
		quadStore = handle.QuadStore
		closeStore = func() { _ = handle.Close() }
		api := cayleyhttp.NewAPIv2(&graph.Handle{QuadStore: handle.QuadStore, QuadWriter: handle.QuadWriter})
		r.Handle("/dev/graph/api/*", http.StripPrefix("/dev/graph", api))
		logrus.WithField("dir", graphDir).Info("embedded graph store enabled at /dev/graph")
	}

	// Embedded database explorer (schema introspection, browsing, filtered
	// queries, sampling, explain plans, and gated test-data mutations), over the
	// same MariaDB/MySQL pool as the app.
	var explorer *dbexplorer.Explorer
	if d.DB != nil {
		explorer = dbexplorer.New(d.DB, dbexplorer.Options{AllowMutations: cfg.Dev.DB.AllowMutations})
		r.Get("/dev/db", devui.DevDBConsole)
		r.Route("/dev/db/api", func(api chi.Router) {
			api.Get("/meta", devui.DevDBMeta(explorer))
			api.Get("/tables", devui.DevDBListTables(explorer))
			api.Get("/tables/{table}", devui.DevDBDescribeTable(explorer))
			api.Get("/tables/{table}/rows", devui.DevDBBrowseRows(explorer))
			api.Get("/tables/{table}/sample", devui.DevDBSampleRows(explorer))
			api.Post("/tables/{table}/rows", devui.DevDBInsertRow(explorer))
			api.Delete("/tables/{table}/rows", devui.DevDBResetTable(explorer))
			api.Post("/query", devui.DevDBQuery(explorer))
			api.Post("/explain", devui.DevDBExplain(explorer))
		})
		logrus.WithField("mutations", explorer.AllowMutations()).Info("embedded database explorer enabled at /dev/db")
	} else {
		logrus.Info("db explorer: no database configured (MARIADB_DSN), /dev/db disabled")
	}

	// The graph indexer fills the graph store with a model of the project. It is
	// created here, after the explorer, because it reads the live database schema
	// through it, and it is handed this very router for the routes layer (read
	// only once the router is fully wired).
	var indexer *codegraph.Indexer
	if quadStore != nil {
		// The opt-in layers (the call graph, git history) cost more than parsing, so
		// they are on only when config asks; graph_reindex can switch one on later.
		opts := codegraph.Options{Root: d.Root, Router: r, RootHint: d.RootHint, Calls: cfg.Dev.Graph.Calls, Git: cfg.Dev.Graph.Git}
		if explorer != nil {
			opts.Schema = explorer.Snapshot
		}
		indexer = codegraph.New(quadStore, opts)
		// The explorer page draws bounded views of the graph; its data comes from
		// /dev/graph/data/*. Links into /dev/db exist only when that is mounted.
		ui := &devui.GraphUI{Indexer: indexer, Store: quadStore, DB: explorer != nil}
		r.Get("/dev/graph", devui.DevGraphPage(indexer))
		r.Post("/dev/graph/reindex", devui.DevGraphReindex(indexer))
		r.Route("/dev/graph/data", func(dd chi.Router) {
			dd.Get("/status", ui.Status())
			dd.Get("/view", ui.View())
			dd.Get("/node", ui.Node())
			dd.Get("/search", ui.Search())
			dd.Get("/findings", ui.Findings())
			dd.Get("/table", ui.TableRefs())
		})
		// Generated reference pages: routes, config, schema and layers, always read
		// from the graph as it is now.
		reports := devui.DevGraphReports(indexer, explorer != nil)
		r.Get("/dev/graph/reports", reports)
		r.Get("/dev/graph/reports/{name}", reports)
	}

	// MCP server (streamable HTTP) exposing the db_* and graph_* tools to local
	// agents, mounted only once at least one backing store exists.
	if explorer != nil || quadStore != nil {
		// With an indexer, every graph tool first brings stale layers up to date,
		// and graph_reindex can rebuild them. (A typed nil must not become a
		// non-nil interface, hence the explicit variable.)
		var graphIndexer devmcp.GraphIndexer
		if indexer != nil {
			graphIndexer = indexer
		}
		r.Handle("/dev/mcp", devmcp.NewHandler(explorer, quadStore, graphIndexer))
		logrus.Info("embedded MCP server enabled at /dev/mcp")
	}

	// Fill the graph store with the project's own structure. Async, so startup is
	// never delayed, and never fatal: a failed layer is logged and the server stays
	// up. With Air every rebuild restarts the server, so this runs on every build.
	// The indexer runs in this process because it owns the bolt lock.
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	if indexer != nil {
		running.Add(1)
		go func() {
			defer running.Done()
			if _, err := indexer.Run(ctx); err != nil && ctx.Err() == nil {
				logrus.WithError(err).Warn("graph index: boot run failed; POST /dev/graph/reindex to retry")
			}
		}()
	}
	return func() {
		cancel()
		running.Wait()
		if closeStore != nil {
			closeStore()
		}
	}
}
