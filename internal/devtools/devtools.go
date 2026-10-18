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
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cayleygraph/cayley/graph"
	cayleyhttp "github.com/cayleygraph/cayley/server/http"
	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/dbexplorer"
	"github.com/petars-dev/govibe/internal/devext"
	"github.com/petars-dev/govibe/internal/devmcp"
	"github.com/petars-dev/govibe/internal/devtools/devui"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
	"github.com/petars-dev/govibe/internal/frameworkdocs"
	"github.com/petars-dev/govibe/internal/graphdb"
	"github.com/petars-dev/govibe/internal/profile"
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
	// Inspect is what the running binary reports about itself (the document
	// `inspect --json` prints): the modules it was built from, the framework's version,
	// which routes the framework registered before the application's, and the
	// application's own Routes function, which tells the graph where the binary's
	// sources were compiled, so route handlers resolve to the application's code even
	// when the application is a module of its own and the framework a dependency. Nil
	// when there is none (the tools then fall back to go.mod and to each route's path).
	Inspect *routeinfo.Inspect
	// Scheduler is the app's scheduler (the commands that declare a Schedule): the
	// /dev/schedule page, its "Run now" and the schedule_status tool read and drive it.
	// Nil when no command is scheduled; the page then says so and how to declare one.
	Scheduler schedview.Source
	// App is what the application adds to the panel through App.DevTools: its own MCP
	// tools (registered on /dev/mcp) and pages (served under /dev/app, linked from
	// /dev). Nil, or empty, when it adds nothing.
	App *devext.Dev
}

// Mount registers the dev tools and starts the indexer. It never fails the
// process: a part that cannot start is logged and left out. The returned function
// stops the indexer and releases the graph store; call it when the process shuts
// down (it is safe to call once, after the server has stopped).
func Mount(d Deps) (stop func()) {
	r := d.Router
	cfg := d.Config

	// The scheduler, as the page, the card on /dev and the MCP tool all read it. A nil
	// pointer inside the interface (an app with no scheduled command, handed over
	// carelessly) is no scheduler.
	sched := schedview.Normalize(d.Scheduler)
	commands := 0
	if d.Inspect != nil {
		commands = len(d.Inspect.Commands)
	}
	scheduleStatus := func() schedview.Status { return schedview.Build(sched, cfg, commands, time.Now()) }

	// The panel's own files: stylesheets, scripts, the graph renderer, the logo.
	r.Group(func(g chi.Router) {
		g.Use(web.DevNoCache)
		g.Handle(web.StaticPrefix+"/*", http.StripPrefix(web.StaticPrefix, http.FileServerFS(devui.Static())))
	})

	// Dev docs routes (renders Markdown docs to HTML). The app's own docs come first, from
	// the directories its profile names; the framework's reference docs are a separate group
	// under /dev/docs/govibe, every title prefixed "GoVibe:".
	docs := docsFor(d.Root, cfg)
	r.Route("/dev/docs", func(dd chi.Router) {
		dd.Use(devui.DocsFor(docs)) // read the project's docs, wherever the process started
		dd.Get("/", devui.DevDocsIndex)
		dd.Get("/app", devui.DevDocsAppIndex)
		dd.Get("/app/{slug}", devui.DevDocsAppFile)
		dd.Get("/specs", devui.DevDocsSpecs)
		dd.Get("/specs/{slug}", devui.DevDocsSpecFile)
		dd.Get("/govibe", devui.DevDocsFrameworkIndex)
		dd.Get("/govibe/{slug}", devui.DevDocsFrameworkFile)
		if docs.FrameworkSpecs != nil { // contributors only: never in an application
			dd.Get("/govibe-specs", devui.DevDocsFrameworkSpecsIndex)
			dd.Get("/govibe-specs/{slug}", devui.DevDocsFrameworkSpecFile)
		}
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
		var opts codegraph.Options
		var version string
		if d.Inspect != nil {
			opts = codegraph.OptionsFromInspect(d.Inspect)
			opts.RouteSource = nil // the live router has the dev tools' own routes too
			version = d.Inspect.Framework.Version
		}
		opts.Root, opts.Router, opts.Calls, opts.Git = d.Root, r, cfg.Dev.Graph.Calls, cfg.Dev.Graph.Git
		if mf, err := FrameworkManifest(context.Background(), version); err != nil {
			logrus.WithError(err).Warn("graph: the framework manifest could not be built; the framework's settings and tools are not labelled")
		} else {
			opts.Manifest = mf
		}
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

	// The scheduler page: what is scheduled, when it runs next, how the last runs went,
	// and "Run now", which goes through the scheduler's own code path.
	r.Get("/dev/schedule", devui.DevSchedule(scheduleStatus))
	r.Get("/dev/schedule/data", devui.DevScheduleData(scheduleStatus))
	r.Post("/dev/schedule/run/{name}", devui.DevScheduleRun(sched))

	// The app's own dev pages and handlers, under /dev/app. They are the app's code,
	// mounted here so that they exist only where the panel does.
	appDev := d.App
	if appDev == nil {
		appDev = devext.New()
	}
	if routes := appDev.Routes(); len(routes) > 0 {
		r.Route(devext.PagePrefix, func(a chi.Router) {
			a.Use(web.DevNoCache)
			for _, rt := range routes {
				if rt.Method == "" {
					a.Handle(rt.Pattern, rt.Handler)
				} else {
					a.Method(rt.Method, rt.Pattern, rt.Handler)
				}
			}
		})
		logrus.WithField("pages", len(appDev.Pages())).Info("the app's own dev pages are mounted at " + devext.PagePrefix)
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
		mcpOpts := devmcp.Options{Root: d.Root, Inspect: d.Inspect, Docs: docs.Framework, Schedule: scheduleStatus, AppTools: appDev.Tools()}
		if d.Inspect != nil {
			mcpOpts.AppName = d.Inspect.App.Name
		}
		r.Handle("/dev/mcp", devmcp.NewHandler(explorer, quadStore, graphIndexer, mcpOpts))
		logrus.Info("embedded MCP server enabled at /dev/mcp")
	}

	// The front door: what the app is, which tools are there, and what to do first.
	info := devui.DevInfo{Env: cfg.Env, DB: explorer != nil, Graph: indexer != nil, MCP: explorer != nil || quadStore != nil, FrameworkDocs: docs.Framework != nil, Schedule: scheduleStatus}
	if d.Inspect != nil {
		info.AppName, info.Module, info.FrameworkVersion = d.Inspect.App.Name, d.Inspect.App.Module, d.Inspect.Framework.Version
		for _, c := range d.Inspect.Commands {
			info.Commands = append(info.Commands, devui.DevCommand{Name: c.Name, Short: c.Short})
		}
	}
	for _, p := range appDev.Pages() {
		info.AppPages = append(info.AppPages, devui.DevPage{Path: devext.PagePrefix + p.Path, Title: p.Title})
	}
	for _, t := range appDev.Tools() {
		info.AppTools = append(info.AppTools, t.Name)
	}
	info.AppProblems = appDev.Problems()
	index := devui.DevIndex(info, indexer)
	r.Get("/dev", index)
	r.Get("/dev/", index)

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

// docsFor says where the dev docs read from: the application's docs and specs by its
// profile, the framework's curated docs embedded in the binary, and, for a contributor who
// pointed dev.framework_src at a checkout, the framework's docs from that checkout and its
// development specs as a group of their own.
func docsFor(root string, cfg config.Config) devui.Docs {
	docs := devui.Docs{Root: root, Framework: frameworkdocs.Embedded()}
	if prof, err := profile.Load(root); err != nil {
		logrus.WithError(err).Warn("dev docs: govibe.yml is invalid; reading docs from the default places")
	} else {
		docs.Dirs, docs.Specs, docs.SpecPrefix, docs.FromProfile = prof.Docs.Dirs, prof.Docs.Specs, prof.Docs.SpecPrefix, true
	}
	if src := cfg.Dev.FrameworkSrc; src != "" {
		if st, err := os.Stat(filepath.Join(src, "docs", "public")); err != nil || !st.IsDir() {
			logrus.WithField("dir", src).Warn("dev docs: dev.framework_src is not a checkout of the framework (no docs/public); serving the embedded docs")
		} else {
			docs.Framework = frameworkdocs.FromDisk(src)
			docs.FrameworkSpecs = frameworkdocs.ContributorSpecs(src)
			logrus.WithField("dir", src).Info("dev docs: serving the framework's docs and specs from a checkout (contributor mode)")
		}
	}
	return docs
}
