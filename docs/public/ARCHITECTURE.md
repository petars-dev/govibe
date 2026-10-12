# GoVibe Architecture

How the GoVibe framework is built. A GoVibe application is a separate Go module that imports the framework and contains only
application code; this repository holds the framework, the tool that works on a project's source, and one reference
application. (The rules an application follows, and the plan that produced this layout, are in `SKILL.md` and
`docs/specs/SPEC_FRAMEWORK_EXTRACTION.md`.)

## The shape
```text
  app (its own module)                 imports (one way)            calls back
  main.go   govibe.Main(App{...})  --------------------->  govibe  (Routes, Commands, Shutdown)
  env.yml, routes/, controllers/, business/, storage/, assets/, docs/

  framework (module github.com/petars-dev/govibe, this repository)
  public    govibe  config  web  govibetest
  internal  kernel  migrate  routeinfo  devtools (devui)  codegraph  dbexplorer  graphdb  devmcp
  tool      cmd/govibe
  example   examples/hello  (its own module; go.work joins it to this one)
```
- The app imports the framework, never the reverse (the module graph makes the reverse impossible). An app imports only the four
  public packages; everything else is `internal/`, so it can be reorganized freely.
- `internal/devtools` is a leaf: it declares the ports it needs (`devtools.Deps`) and imports neither the kernel nor an app.
  `internal/kernel` reaches it from exactly one file, behind a build tag (see "Dev tooling and the build tag").
- `arch_test.go` enforces these rules: devtools does not import the kernel, the public packages do not reach the dev stack, nothing
  imports `examples/`, and the example app imports only the public packages.

## Packages
- `govibe` (the module root, `govibe.go`): the public entry point. `App`, `Runtime`, `Command` and `RunFunc` are aliases of the
  kernel's types; `Main` and `Run` run an app. The app declares what it contributes (`Config`, `Migrations`, `Assets`, `Routes`,
  `Commands`, `Shutdown`, all optional); the framework runs it.
- `config`: the layered loader. `config.Load(fs)` merges the framework defaults (`config/defaults.yml`, embedded here) under the
  app's `env.yml`, resolves `${ENV:default}` placeholders from the process environment, and returns a `Config` value (no globals).
- `web`: what an app's controllers and views share with the framework: the vendored htmx 2.0 build served at
  `/_govibe/static/htmx.min.js` and the `web.HTMXScript()` tag that loads it, `web.IfDev` (renders its children only when the dev
  tools are mounted, so a production page links to no `/dev` path), and the `RequestLogger` and `DevNoCache` middleware.
- `govibetest`: boots an app inside a test through the same path as `serve`, on an `httptest` server, without a database and without
  the dev tools.
- `internal/kernel`: the one boot path (below), the runtime commands, `inspect --json`, and the small seams that keep the dev stack out
  of production.
- `internal/migrate`: runs goose over the app's embedded migrations through its Provider API, with the dialect (`database.type`) a
  required, parsed argument.
- `internal/routeinfo`: reads a chi router into plain data (method, path, handler by symbol, file and line, middleware); shared by the
  kernel's `routes` and `inspect` and by the graph's routes layer, and light enough to link into a production binary.
- `internal/devtools` and `internal/devtools/devui`: the dev panel (below). `devui` holds the pages' handlers and their embedded
  files (stylesheets, the graph renderer, cytoscape, the logo), served at `/_govibe/static`.
- `internal/codegraph`, `internal/dbexplorer`, `internal/graphdb`, `internal/devmcp`: the graph indexer and its agent surface, the
  database explorer, the embedded Cayley store and the MCP server. Moved, not rewritten.
- `cmd/govibe`: the tool. `govibe graph index` (the offline indexer and CI gate), `govibe generate` (templ), and `govibe serve` and
  `govibe migrate`, which run the app in the current project through `go run -tags govibe_dev .`.
- `examples/hello`: the reference app, the Hello GoVibe demo. It is plain application code (three handlers, an action, a templ
  page with its own static files, the routes) that the framework runs and that this repository tests and gates as an app.

## Boot sequence (`internal/kernel`)
One path for `main`, `serve`, the read-only commands and tests (`kernel.Build`):
1. Load the configuration once: the framework defaults, then the app's `env.yml`.
2. Set up logging (logrus).
3. Open one database pool if a DSN is set, and run the app's migrations with the configured dialect.
4. Build the chi router: RequestID, RealIP, Recoverer and the request logger; the dev marker (`web.MarkDev`) when the dev tools will be
   mounted; the asset mounts (the app's `./assets` from disk in local and dev, else the embedded `App.Assets`, at `/assets/*`);
   `GET /_govibe/static/htmx.min.js`. Everything registered up to here is the framework's (snapshotted as `Built.FrameworkRoutes`);
   then the app's `Routes`; then a check that the app registered nothing under the reserved prefixes `/dev` and `/_govibe`.
5. Build the command table from `App.Commands` and reject an unnamed, duplicate or built-in name.
6. Mount the dev tools when they are compiled in and the environment is not production.
7. `serve` listens, and on SIGINT or SIGTERM shuts the server down gracefully, calls `App.Shutdown` and closes the pool.

The runtime commands of every app binary are `serve` (the default), `migrate up|down|status`, `routes` (every route and who
registered it), `version`, `inspect --json` (the routes and commands as one JSON document, with no database, for the tool) and
`help`, plus the app's own. The read-only ones boot with no database, no dev tools and quiet logging.

## Requests (in an app)
1) chi middleware stack -> controller handler
2) controller -> actions/services -> storage as needed
3) controller renders a templ page or returns JSON

## Configuration
- Layered, loaded once per boot (`config.Load`, no globals): the framework defaults in `config/defaults.yml`, then the app's `env.yml`
  in its project root (embedded by its `main.go`; its file wins, maps merge deeply, lists are replaced, unknown keys are allowed).
  Values are `${ENV:default}` placeholders resolved from the process environment; a quoted placeholder keeps its value a string.
- Typed access via the `config.Config` the runtime carries (`rt.Config`), or dynamic via `rt.Config.Unmarshal` / `rt.Config.Get` for
  the app's own keys.
- `root` (`GOVIBE_ROOT`) is the application's root directory: the dev assets directory and the graph store resolve against it, not
  against the working directory. By default it is the nearest directory above the working directory that holds `govibe.yml` or `go.mod`.
- `database.type` (`DATABASE_TYPE`, default `mysql`) selects the goose migration dialect, parsed by `migrate.ParseDialect`: an empty or
  unknown value is an error. Goose defaults to postgres if never told otherwise, which silently breaks migrations against any other
  database, so every entry point that runs goose passes the dialect explicitly.

## Logging
- logrus, text formatter by default.

## Dev tooling and the build tag
- **Excluded from production by construction.** The kernel names the dev tools in exactly one file, `internal/kernel/devmount_dev.go`,
  which carries the `govibe_dev` build tag and registers the mount function in its `init`. A default build does not compile that
  file, so it links none of the dev stack (the graph store, the indexer, the MCP server, the database explorer, the panel's files, and
  the libraries only they use): for the reference app, 241 packages and an 8.7 MB binary against 443 packages and 30 MB with the
  tag. `make dev` and the tool's `govibe serve` pass the tag; a production build never does. The runtime check stays as a second lock:
  a dev build that finds `APP_ENV=prod` refuses to mount and logs why, and a default build says once that `/dev` is not compiled in.
- `TestDefaultBuildLinksNoDevTools` (in `examples/hello`) fails if a default build links a dev package, and the `ci` workflow's
  `production` job builds the reference app without the tag and checks that it serves nothing under `/dev` and links to none.
- Only the kernel's import file is tagged, never the dev packages themselves: tagged files inside a shared package would hide code from
  the graph's calls layer and break a default `go test`. The kernel's dev tests are tagged to match, and CI runs the suite both ways.
- **Reserved namespaces.** `/dev/*` is the dev panel; `/_govibe/*` is framework runtime (htmx for every app, and the panel's files in
  a dev build). An app's own files live under `/assets`, and an app that registers a route in a reserved prefix fails to boot.
- `web.IfDev` wraps links to `/dev/*` in app pages: the kernel marks the request context when it mounts the tools, so a link is
  rendered only where it is not dead.

## Dev tooling: what it serves
- `/dev/docs`, `/dev/graph`, `/dev/db`, and `/dev/mcp` are mounted by `devtools.Mount` (`internal/devtools`), which the kernel calls only in a `govibe_dev` build and only when `!cfg.IsProd()`; none of them exist in a production process. The panel's own files (stylesheets, scripts, the graph renderer, cytoscape, the logo) are the framework's: they are embedded in `internal/devtools/devui` and served at `/_govibe/static`, never from the app's `/assets`. The explorer shares the app's one database pool.
- `/dev/db` is a Faucet-like database explorer (schema introspection, table browsing, filtered queries, row sampling, explain plans) with an optional, explicitly-gated mutation surface (`db_insert_test_row`/`db_reset_test_data`, or the equivalent `/dev/db/api` endpoints) for seeding/clearing test fixtures. Gated by `dev.db.allow_mutations` in `config/defaults.yml` (`DEV_DB_ALLOW_MUTATIONS`), which has no effect in production regardless of the flag.
- `/dev/mcp` mounts an MCP server (github.com/modelcontextprotocol/go-sdk) over streamable HTTP so local agents can call the same db_* tools plus the graph tools without going through the UI. The graph tools are `graph_schema` (start here), `graph_context`, `graph_impact`, `graph_path`, `graph_search`, `graph_neighbors`, `graph_findings`, `graph_reindex`, and `graph_query` (a raw read-only Gizmo query); the resources `govibe://graph/overview` and `govibe://graph/schema`; and the prompt `explain-change`. Each call first brings stale graph layers up to date, then reads the graph into memory (`codegraph.Load`, about 20 ms for 5,000 quads) and answers in Go; nothing is cached, so an answer is always from the graph as it is.
- See docs/specs/SPEC_DEV_DB_EXPLORER.md for details and status.
- The graph store's bolt file (`docs/graph/*.bolt`) is derived data and is git-ignored. What goes into the graph and how to query it is defined in `docs/public/GRAPH_SCHEMA.md`; the plan for the indexer is `docs/specs/SPEC_GRAPH_INDEXER.md`.
- **Graph indexer** (`internal/codegraph`, Phases 1-5 of that spec): once the kernel has built the router, `devtools.Mount` starts a goroutine that indexes the application's project (the root it is given: the app's own tree, not the framework's) into the graph store, so every Air rebuild/restart refreshes it. It runs inside the server process because the process owns the bolt lock; it never delays startup and a failure is logged, never fatal. `POST /dev/graph/reindex` rebuilds on demand and returns a JSON report (per-layer status, whether it was rebuilt, quad counts, added/removed vs the previous run). Each layer owns the quads carrying its `layer:<name>` label and replaces only those, so quads added by hand survive. Implemented layers: `code` (packages, files, funcs, types, imports from `go/ast`, and the facts the other layers resolve against), `routes` (the live chi router: route → handler by file and line, middleware), `templates` (`.templ` components and who renders them), `config` (the keys of the settings files, `env.yml` over `config/defaults.yml`, with their defaults, environment variables, sensitive flags, and every use in the code), `mcp` (tools registered with `mcp.AddTool`), `db` (the schema from the migrations in an app's migrations directory, merged with the live database's schema read by `dbexplorer.Explorer.Snapshot` when one is configured: tables, columns, indexes, foreign keys, drift between the two, relations the schema implies, and which code reads or writes which table), `docs` (docs, specs and their status rows, what they mention, and what they name that is gone), `findings` (problems derived from all of the above: layering violations, stale doc references, config declared and never read or read and never declared, a table without a primary key, migration drift, ...; a rule that could not run says so), and `meta` (`build:latest` and one `layer:<name>` node per layer). Each layer has a fingerprint over its inputs and its dependencies' fingerprints; a `/dev/graph` page load and every `graph_query` call first rebuild only the stale layers, which is how an edit to `docs/**` (ignored by Air) shows up without a restart. Two **opt-in** layers (Phase 6) are switched by `dev.graph.calls` / `dev.graph.git` (`GRAPH_CALLS` / `GRAPH_GIT`; `config/defaults.yml` ships `calls` on and `git` off, and the offline `govibe graph index` takes `--calls` / `--git`), or on demand by `graph_reindex` naming the layer, which switches it on until the server restarts: `calls` type-checks the module with `golang.org/x/tools/go/packages` (about a second) and records which function calls which, with calls through an interface the module declares linked to every type that could be on the other end (an annotated edge: method `interface_dispatch`, confidence 80 down to 30), and `git` reads `git log` (newest 1,000 commits, once per `HEAD`) for each file's last commit, churn, number of authors and the files that change in the same commits. A layer that is off has status `off` and holds no quads. Phase 4, the agent surface, is the read side of the same package (`model.go`, `resolve.go`, `agent_*.go`): a `Model` (an in-memory copy of the indexer's quads) with reference resolution (`GET /api/hello`, a `file:line` reference into an app's controller, `orderstore.Store.List`, `orders`), search, neighbors, impact, path and context. With the `calls` layer off, reach beyond what a stored edge ties together is estimated from package imports and labelled so (method `package_imports`, confidence at most 60): a route that imports the code that touches a table is possible, not proven. With it on, `graph_impact`, `graph_path` and `graph_context` walk call edges instead: a route's path to a table is a chain of calls with the weakest hop's confidence, `access` is the union over every call path, "no table is reached" is a measured answer, and the request-flow view and the routes report show the tables each route reaches. The live schema can change with no file changing, so a freshness check reads it too (reused for 5 seconds), and an unreachable database only makes the `db` layer fall back to the migrations. Phase 5, the human surface: `/dev/graph` is a page (`internal/devtools/devui/static/graph.js`) that draws bounded views the server builds from the same `Model` (architecture, request flow, database ER, docs against code, config usage, findings, or the neighbourhood of any node) and never loads the whole graph, so it stays quick at any size; its data is `/dev/graph/data/{status,view,node,search,findings,table}` (`devui.GraphUI`). `/dev/graph/reports/{routes,config,schema,layers}` are reference pages generated from the graph on every load, so the route table, config reference, schema listing and package layer map cannot drift from the code. The layering rule the findings enforce is `layering.go`: only business may call storage, storage depends on nothing above it, business does not depend on the HTTP layer; the `wiring` and `dev` packages (an app's `routes`, `main` and `cmd`, the framework's kernel, dev tools and MCP server) may import anything. `govibe graph index` (`cmd/govibe/cmd_graph.go`) builds the same graph offline, into a store of its own (`--dir`), for when no server holds the lock; with `--fail-on error --baseline docs/graph/baseline.json` it exits 1 on a finding the baseline does not accept, which is what `.github/workflows/graph.yml` runs on every push to `main` and every pull request (`make graph-check` locally). Production never opens the graph store, so it never indexes.
