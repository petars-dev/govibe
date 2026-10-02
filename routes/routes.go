package routes

import (
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    "github.com/cayleygraph/cayley/graph"
    cayleyhttp "github.com/cayleygraph/cayley/server/http"

    appmw "trace-loop/os/middleware"
    "trace-loop/os/controllers"
    "trace-loop/os/assets"
    "trace-loop/os/config"
    "trace-loop/os/devmcp"
    "trace-loop/os/storage/dbexplorer"
    "trace-loop/os/storage/graphdb"
    "trace-loop/os/storage/mariadb"
    "os"
    "github.com/sirupsen/logrus"
)

// NewRouter wires all app routes in one place.
func NewRouter() http.Handler {
    r := chi.NewRouter()

    // Chi default middleware + our logger.
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Recoverer)
    r.Use(appmw.RequestLogger)

    // Static assets served under /assets/ path.
    if cfg, err := config.Load(); err == nil && (cfg.IsLocal() || cfg.IsDev()) {
        // Prefer local override dir if present, else fall back to tracked assets/ directory.
        var fsPath = "assets"
        if st, err := os.Stat("local/assets"); err == nil && st.IsDir() {
            fsPath = "local/assets"
        }
        fsServer := http.FileServer(http.Dir(fsPath))
        logrus.WithField("path", fsPath).Info("serving assets from filesystem (dev mode)")
        r.Group(func(g chi.Router) {
            g.Use(appmw.DevNoCache)
            g.Handle("/assets/*", http.StripPrefix("/assets", fsServer))
        })
    } else {
        // Embedded assets for staging/prod.
        fileServer := http.FileServer(http.FS(assets.FS))
        logrus.Info("serving embedded assets")
        r.Handle("/assets/*", http.StripPrefix("/assets", fileServer))
    }

    // UI routes
    r.Get("/", controllers.Home)

    // API routes
    r.Route("/api", func(api chi.Router) {
        api.Get("/hello", controllers.HelloAPI)
    })

    // htmx-friendly plain text endpoint (also used by UI button if desired)
    r.Get("/hello", controllers.Hello)

    // Dev docs routes (renders Markdown docs to HTML)
    r.Route("/dev/docs", func(d chi.Router) {
        d.Get("/app", controllers.DevDocsAppIndex)
        d.Get("/app/{slug}", controllers.DevDocsAppFile)
        d.Get("/specs", controllers.DevDocsSpecs)
        d.Get("/specs/{slug}", controllers.DevDocsSpecFile)
    })

    // Dev-only tooling: embedded graph store, database explorer, and the MCP
    // server that fronts both. None of this is mounted in production.
    if cfg, err := config.Load(); err == nil && !cfg.IsProd() {
        var quadStore graph.QuadStore

        // Embedded Cayley graph store + query console. Backed by a bolt file
        // under docs/graph.
        if handle, err := graphdb.Open(graphdb.DefaultDir); err != nil {
            logrus.WithError(err).Error("graph store: failed to open, /dev/graph disabled")
        } else {
            quadStore = handle.QuadStore
            api := cayleyhttp.NewAPIv2(&graph.Handle{QuadStore: handle.QuadStore, QuadWriter: handle.QuadWriter})
            r.Get("/dev/graph", controllers.DevGraphConsole)
            r.Handle("/dev/graph/api/*", http.StripPrefix("/dev/graph", api))
            logrus.WithField("dir", graphdb.DefaultDir).Info("embedded graph store enabled at /dev/graph")
        }

        // Embedded database explorer (schema introspection, browsing,
        // filtered queries, sampling, explain plans, and gated test-data
        // mutations), backed by the same MariaDB/MySQL connection as the app.
        var explorer *dbexplorer.Explorer
        if dbx, err := mariadb.Open(cfg.Database.MariaDB.DSN); err != nil {
            logrus.WithError(err).Error("db explorer: failed to open database, /dev/db disabled")
        } else if dbx != nil {
            explorer = dbexplorer.New(dbx, dbexplorer.Options{AllowMutations: cfg.Dev.DB.AllowMutations})
            r.Get("/dev/db", controllers.DevDBConsole)
            r.Route("/dev/db/api", func(api chi.Router) {
                api.Get("/meta", controllers.DevDBMeta(explorer))
                api.Get("/tables", controllers.DevDBListTables(explorer))
                api.Get("/tables/{table}", controllers.DevDBDescribeTable(explorer))
                api.Get("/tables/{table}/rows", controllers.DevDBBrowseRows(explorer))
                api.Get("/tables/{table}/sample", controllers.DevDBSampleRows(explorer))
                api.Post("/tables/{table}/rows", controllers.DevDBInsertRow(explorer))
                api.Delete("/tables/{table}/rows", controllers.DevDBResetTable(explorer))
                api.Post("/query", controllers.DevDBQuery(explorer))
                api.Post("/explain", controllers.DevDBExplain(explorer))
            })
            logrus.WithField("mutations", explorer.AllowMutations()).Info("embedded database explorer enabled at /dev/db")
        } else {
            logrus.Info("db explorer: no MARIADB_DSN configured, /dev/db disabled")
        }

        // MCP server (streamable HTTP) exposing db_* and graph_query tools to
        // local agents, mounted only once at least one backing store exists.
        if explorer != nil || quadStore != nil {
            r.Handle("/dev/mcp", devmcp.NewHandler(explorer, quadStore))
            logrus.Info("embedded MCP server enabled at /dev/mcp")
        }
    }

    return r
}
