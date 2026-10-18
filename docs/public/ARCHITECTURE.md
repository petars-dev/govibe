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
  internal  kernel  migrate  routeinfo  scheduler  profile  rules  frameworkdocs  scaffold  generate  devtools (devui)  codegraph  dbexplorer  graphdb  devmcp
  tool      cmd/govibe  (new, make, check, doctor, upgrade, graph index, generate, serve, migrate)
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
  `Commands`, `Shutdown`, `DevTools`, all optional); the framework runs it. `Dev`, `DevTool` and `DevToolFunc` are what `DevTools` works with.
- `config`: the layered loader. `config.Load(fs)` merges the framework defaults (`config/defaults.yml`, embedded here) under the
  app's `env.yml`, resolves `${ENV:default}` placeholders from the process environment, and returns a `Config` value (no globals).
- `web`: what an app's controllers and views share with the framework: the vendored htmx 2.0 build served at
  `/_govibe/static/htmx.min.js` and the `web.HTMXScript()` tag that loads it, `web.IfDev` (renders its children only when the dev
  tools are mounted, so a production page links to no `/dev` path), and the `RequestLogger` and `DevNoCache` middleware.
- `govibetest`: boots an app inside a test through the same path as `serve`, on an `httptest` server, without a database and without
  the dev tools; the scheduler runs on a fake clock the test moves (`srv.Clock.AdvanceTo`, `srv.Runs`).
- `internal/kernel`: the one boot path (below), the runtime commands, `inspect --json`, and the small seams that keep the dev stack out
  of production.
- `internal/migrate`: runs goose over the app's embedded migrations through its Provider API, with the dialect (`database.type`) a
  required, parsed argument.
- `internal/scheduler`: the scheduler's engine: validated cron specs (a wrapper over the `robfig/cron/v3` parser, nothing else of it), a runner with overlap, timeout, panic recovery, an in-memory run history and
  an optional cross-instance lock (`GET_LOCK`), and a clock a test can drive (`scheduler.Fake`). It knows nothing of commands, the kernel or the dev tools.
- `internal/devext`: the dev panel as an app sees it (`Dev`: `Tool`, `Page`, `Handle`), a collector with no dependency on the dev stack, so the
  public API can name it and a default build links none of the dev tools through it. The kernel hands one to `App.DevTools`; `devtools.Mount` mounts what it holds.
- `internal/routeinfo`: reads a chi router into plain data (method, path, handler by symbol, file and line, middleware); shared by the
  kernel's `routes` and `inspect` and by the graph's routes layer, and light enough to link into a production binary.
- `internal/devtools` and `internal/devtools/devui`: the dev panel (below). `devui` holds the pages' handlers and their embedded
  files (stylesheets, the graph renderer, cytoscape, the logo), served at `/_govibe/static`.
- `internal/codegraph`, `internal/dbexplorer`, `internal/graphdb`, `internal/devmcp`: the graph indexer and its agent surface, the
  database explorer, the embedded Cayley store and the MCP server. Moved, not rewritten.
- `internal/profile`: the project profile (`govibe.yml`): where a project keeps its settings, migrations, docs and graph store, which
  directory is which architecture layer, and per-rule settings. The indexer, the dev docs and the tool read it instead of constants.
- `internal/rules`: the rules of `SKILL.md` as data: the layering table, which framework packages each layer may import, the sections of a
  spec. The graph's findings enforce them; the same tables render the rules text.
- `internal/frameworkdocs`: the framework's own reference docs as an app sees them: a curated, embedded allowlist, every title prefixed
  `GoVibe:`, never a spec.
- `internal/scaffold`: what `govibe new` writes (below): the embedded template set (the Hello GoVibe demo synced from `examples/hello`, and the
  settings, docs, Makefile, air and workflow files), the rendered `go.mod`, `SKILL.md` and `govibe.yml`, the managed files and their hashes, the
  upgrade plan (`scaffold.PlanUpgrade`: the managed files re-rendered for the pinned version, diffed, refused when edited) and the unified diff.
- `internal/generate`: the generators of `govibe make` (below): a page, a resource, an action, a service, a store, a migration, a middleware, a
  command, a typed config, a spec. Each returns the files to write and the snippets it could not insert; the tool applies them.
- `cmd/govibe`: the tool. `govibe new` (a new application), `govibe make` (the generators), `govibe check` (the managed files, the doctor's static
  checks and the graph gate, in a scratch store), `govibe doctor` (the project's health, with the build checks), `govibe upgrade` (the managed files
  for a new framework version), `govibe graph index` (the offline indexer and CI gate), `govibe generate` (templ), and `govibe serve` and
  `govibe migrate`, which run the app in the current project through `go run -tags govibe_dev .`. An app runs it as `go tool govibe`, pinned in its `go.mod`.
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
5. Build the command table from `App.Commands` and reject an unnamed, duplicate or built-in name. Validate the `scheduler.*` settings and every command's `Schedule` (an invalid one is a boot error that names the
   command), and build the scheduler's runner, which arms nothing yet (`Built.Scheduler`).
6. Mount the dev tools when they are compiled in and the environment is not production.
7. `serve` binds the port, then starts the scheduler (unless `scheduler.enabled` is false or `--no-schedule`), then serves. On SIGINT or SIGTERM it stops the scheduler (nothing new starts; running tasks get
   `scheduler.shutdown_timeout`, then their contexts are cancelled) while the server shuts down gracefully, then calls `App.Shutdown` and closes the pool. `schedule run` does steps 1 to 5, opens the database
   and runs only the scheduler.

The runtime commands of every app binary are `serve` (the default), `migrate up|down|status`, `routes` (every route and who
registered it), `schedule list|run`, `version`, `inspect --json` (the routes, the commands with their schedules and the scheduler's settings as one JSON document, with no database, for the tool) and
`help`, plus the app's own. The read-only ones boot with no database, no dev tools and quiet logging.

## Commands and the scheduler
- A command is a `Command` the app declares in a `console.go` beside its routes; with a `Schedule` it is also run by the scheduler (`internal/scheduler`, built on the cron-spec parser of `robfig/cron/v3` and nothing else of it). The kernel
  turns the scheduled commands into tasks whose body is the very `Run` the CLI calls (with the flags' defaults and no arguments). `Build` validates and builds the runner; `serve` and `schedule run` start it.
- The engine is timer callbacks on a `Clock`, not a goroutine per task: the real clock calls them from its own goroutines and the fake clock (`scheduler.Fake`) calls them from `Advance`, in time order, so a test is
  deterministic and needs no sleeps. After each timer a fake clock waits (real time, bounded) for the runs that timer started to finish; that wait is why `govibetest`'s `AdvanceTo` can be asserted on at once.
- Semantics, all deliberate: overlap `Skip` by default; a timeout is a context deadline; an error or panic is recovered and logged; nothing is caught up after downtime and nothing is run late (a due time that passed, or whose
  timer fired more than 30 seconds late, is skipped and recorded as `missed`); `@every` counts from the start; a spring-forward gap is skipped and each reading of a repeated fall-back hour runs once for a spec that names its
  hours. The run history (20 per task, identical consecutive skips counted as one record) is in memory.
- Several instances each run every due task, unless `--no-schedule` or `scheduler.lock: db` (a `GET_LOCK` per task on a connection of its own, kept until 10 seconds past the due time so a quick run cannot be repeated by a second instance) says otherwise. The lock is never on by default.
- Read-only: `schedule list` computes next runs from the declarations (`Runner.Upcoming`), with no clock started and no database. Live: `/dev/schedule` (dev builds), `GET /dev/schedule/data` and the `schedule_status`
  MCP tool, all from one status document (`internal/devtools/schedview`). "Run now" posts to `/dev/schedule/run/{name}` and goes through the same code path as a scheduled run; it refuses a cross-site request
  (`http.CrossOriginProtection`) and any request whose Host is not loopback (a DNS-rebinding page is "same-origin" with a server that listens on every interface).
- In the graph: a `commands` layer from `inspect --json` (a `command` node per command, `run_by` to the function that runs it, the schedule as attributes), a commands report, the reach from a table to the commands that touch
  it, and the info rule `docs.command_undocumented`. See `GoVibe: Commands and Scheduler`.

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
- **The app's own dev tools** (`App.DevTools`, Phase 7). An app can add MCP tools and pages to the panel: the kernel calls `DevTools(rt, dev)`
  only when it mounts the dev tools (after `Routes` and `Commands`, with the same runtime), `dev.Tool` registers an MCP tool on `/dev/mcp`
  (`govibe.DevToolFunc` gives it typed arguments; the schema comes from the arguments struct the way the framework's own tools get theirs;
  the `graph_`, `db_` and `schedule_` families are refused), `dev.Page` and `dev.Handle` register handlers that the framework mounts under
  `/dev/app` (the one place inside the reserved namespace that is the app's: the graph's routes layer gives those routes origin `app`), and
  `/dev` lists both. A refused registration is logged and shown on `/dev`; an error from `DevTools` leaves the app's part out and the panel up;
  a tool that returns an error or panics is a failed call to the agent, never a dead server. The convention is a variable the app's tagged
  file sets (routes/devtools.go declares `var DevTools func(rt *govibe.Runtime, dev *govibe.Dev) error`, routes/devtools_dev.go, tagged
  `govibe_dev`, sets it in `init` and holds the registrations; `main.go` passes `DevTools: routes.DevTools`), so a default build compiles none of
  it and the field is nil there; `inspect --json` reports `contributes.dev_tools` for the build that answers. The graph's `mcp` layer indexes a
  `DevToolFunc` call or a `DevTool` literal like an `mcp.AddTool`: the tool node, origin `app`, its schema, and `registers` from the function
  that holds it, so `graph_context` and `graph_impact` see the app's tools next to the framework's.

## Dev tooling: what it serves
- `/dev` (the front door), `/dev/docs`, `/dev/graph`, `/dev/db`, `/dev/schedule` (and its `/data` JSON and `POST /dev/schedule/run/{name}`) and `/dev/mcp` are mounted by `devtools.Mount` (`internal/devtools`), which the kernel calls only in a `govibe_dev` build and only when `!cfg.IsProd()`; none of them exist in a production process. The panel's own files (stylesheets, scripts, the graph renderer, cytoscape, the logo) are the framework's: they are embedded in `internal/devtools/devui` and served at `/_govibe/static`, never from the app's `/assets`. The explorer shares the app's one database pool.
- `/dev/db` is a Faucet-like database explorer (schema introspection, table browsing, filtered queries, row sampling, explain plans) with an optional, explicitly-gated mutation surface (`db_insert_test_row`/`db_reset_test_data`, or the equivalent `/dev/db/api` endpoints) for seeding/clearing test fixtures. Gated by `dev.db.allow_mutations` in `config/defaults.yml` (`DEV_DB_ALLOW_MUTATIONS`), which has no effect in production regardless of the flag.
- `/dev/mcp` mounts an MCP server (github.com/modelcontextprotocol/go-sdk) over streamable HTTP so local agents can call the same db_* tools plus the graph tools without going through the UI. The graph tools are `graph_schema` (start here), `graph_context`, `graph_impact`, `graph_path`, `graph_search`, `graph_neighbors`, `graph_findings`, `graph_reindex`, and `graph_query` (a raw read-only Gizmo query); the resources `govibe://graph/overview` and `govibe://graph/schema`; and the prompt `explain-change`. Each call first brings stale graph layers up to date, then reads the graph into memory (`codegraph.Load`, about 20 ms for 5,000 quads) and answers in Go; nothing is cached, so an answer is always from the graph as it is.
- See docs/specs/SPEC_DEV_DB_EXPLORER.md for details and status.
- The graph store's bolt file (`docs/graph/*.bolt`) is derived data and is git-ignored. What goes into the graph and how to query it is defined in `docs/public/GRAPH_SCHEMA.md`; the plan for the indexer is `docs/specs/SPEC_GRAPH_INDEXER.md`.
- **Graph indexer** (`internal/codegraph`, Phases 1-5 of that spec): once the kernel has built the router, `devtools.Mount` starts a goroutine that indexes the application's project (the root it is given: the app's own tree, not the framework's) into the graph store, so every Air rebuild/restart refreshes it. It runs inside the server process because the process owns the bolt lock; it never delays startup and a failure is logged, never fatal. `POST /dev/graph/reindex` rebuilds on demand and returns a JSON report (per-layer status, whether it was rebuilt, quad counts, added/removed vs the previous run). Each layer owns the quads carrying its `layer:<name>` label and replaces only those, so quads added by hand survive. Implemented layers: `code` (packages, files, funcs, types, imports from `go/ast`, and the facts the other layers resolve against), `routes` (the live chi router: route → handler by file and line, middleware), `templates` (`.templ` components and who renders them), `config` (the keys of the settings files, `env.yml` over `config/defaults.yml`, with their defaults, environment variables, sensitive flags, and every use in the code), `mcp` (tools registered with `mcp.AddTool`), `db` (the schema from the migrations in an app's migrations directory, merged with the live database's schema read by `dbexplorer.Explorer.Snapshot` when one is configured: tables, columns, indexes, foreign keys, drift between the two, relations the schema implies, and which code reads or writes which table), `docs` (docs, specs and their status rows, what they mention, and what they name that is gone), `findings` (problems derived from all of the above: layering violations, stale doc references, config declared and never read or read and never declared, a table without a primary key, migration drift, ...; a rule that could not run says so), and `meta` (`build:latest` and one `layer:<name>` node per layer). Each layer has a fingerprint over its inputs and its dependencies' fingerprints; a `/dev/graph` page load and every `graph_query` call first rebuild only the stale layers, which is how an edit to `docs/**` (ignored by Air) shows up without a restart. Two **opt-in** layers (Phase 6) are switched by `dev.graph.calls` / `dev.graph.git` (`GRAPH_CALLS` / `GRAPH_GIT`; `config/defaults.yml` ships `calls` on and `git` off, and the offline `govibe graph index` takes `--calls` / `--git`), or on demand by `graph_reindex` naming the layer, which switches it on until the server restarts: `calls` type-checks the module with `golang.org/x/tools/go/packages` (about a second) and records which function calls which, with calls through an interface the module declares linked to every type that could be on the other end (an annotated edge: method `interface_dispatch`, confidence 80 down to 30), and `git` reads `git log` (newest 1,000 commits, once per `HEAD`) for each file's last commit, churn, number of authors and the files that change in the same commits. A layer that is off has status `off` and holds no quads. Phase 4, the agent surface, is the read side of the same package (`model.go`, `resolve.go`, `agent_*.go`): a `Model` (an in-memory copy of the indexer's quads) with reference resolution (`GET /api/hello`, a `file:line` reference into an app's controller, `itemstore.Store.List`, `items`), search, neighbors, impact, path and context. With the `calls` layer off, reach beyond what a stored edge ties together is estimated from package imports and labelled so (method `package_imports`, confidence at most 60): a route that imports the code that touches a table is possible, not proven. With it on, `graph_impact`, `graph_path` and `graph_context` walk call edges instead: a route's path to a table is a chain of calls with the weakest hop's confidence, `access` is the union over every call path, "no table is reached" is a measured answer, and the request-flow view and the routes report show the tables each route reaches. The live schema can change with no file changing, so a freshness check reads it too (reused for 5 seconds), and an unreachable database only makes the `db` layer fall back to the migrations. Phase 5, the human surface: `/dev/graph` is a page (`internal/devtools/devui/static/graph.js`) that draws bounded views the server builds from the same `Model` (architecture, request flow, database ER, docs against code, config usage, findings, or the neighbourhood of any node) and never loads the whole graph, so it stays quick at any size; its data is `/dev/graph/data/{status,view,node,search,findings,table}` (`devui.GraphUI`). `/dev/graph/reports/{routes,config,schema,layers}` are reference pages generated from the graph on every load, so the route table, config reference, schema listing and package layer map cannot drift from the code. The layering rule the findings enforce is `layering.go`: only business may call storage, storage depends on nothing above it, business does not depend on the HTTP layer; the `wiring` and `dev` packages (an app's `routes`, `main` and `cmd`, the framework's kernel, dev tools and MCP server) may import anything. `govibe graph index` (`cmd/govibe/cmd_graph.go`) builds the same graph offline, into a store of its own (`--dir`), for when no server holds the lock; with `--fail-on error --baseline docs/graph/baseline.json` it exits 1 on a finding the baseline does not accept, which is what `.github/workflows/graph.yml` runs on every push to `main` and every pull request (`make graph-check` locally). Production never opens the graph store, so it never indexes.

## The scaffolder: `govibe new`, managed files, `govibe check`
Phase 5 of `docs/specs/SPEC_FRAMEWORK_EXTRACTION.md`. An application is created with `govibe new <module>` and depends on the framework; the
repository it gets holds only application code.

- **Templates embedded in the tool** (`internal/scaffold`), so the scaffold's version is the framework's, it works offline and it is tested. They render
  with the delimiters `[[` and `]]` (templ's `{ }` and Go's `{{ }}` need no escaping); a `*.tmpl` file is rendered and loses the suffix, any other file
  (an image) is copied. Two sets: `templates/demo` is the Hello GoVibe demo, and `templates/app` the rest (`env.yml`, the two seeded docs, `README.md`,
  `Makefile`, `.air.toml`, `.gitignore`, the workflow). `go.mod`, `SKILL.md` and `govibe.yml` are rendered by code. Rendered Go is gofmt-ed, because the
  module path changes the order of imports.
- **The demo is synced, not written twice.** `examples/hello` is the source of truth; `go generate ./internal/scaffold` (`make scaffold-sync`) copies it
  into `templates/demo` with the module path and the app name templated (go:embed cannot reach a nested module), and writes the example's own `SKILL.md`
  the way a generated app's is rendered. Two tests fail when either copy is stale. The sync skips what the scaffold writes itself (the module files, the
  settings, the docs, the readme) and refuses a source that holds a delimiter.
- **What `new` does.** Validates the module path (the app's name is its last element), refuses a non-empty directory, writes the tree, then runs `go mod
  tidy`, `go tool templ generate`, `git init` and a first commit (`--no-git`), and `go tool govibe check` (`--no-check`). `--db mariadb` declares the
  database in `env.yml`; `--replace <checkout>` builds the app against a checkout of the framework instead of a release (contributors, and the keystone
  job): the tool refuses to proceed without it when it was itself built from a checkout, because the app could not fetch the framework at that version.
  The subprocesses run with `GOWORK=off`, so a new module is built on its own even inside a workspace.
- **Pins (AM1 to AM3 of the spec, applied as defaults).** The generated `go.mod` writes the `go` line explicitly (the framework's own, `1.25.0`; `go mod
  init` would write the local toolchain's), requires the framework at the tool's version, chi, templ at the framework's version (`v0.3.1020`) and air at
  `v1.67.0` (the last release on `go 1.25`; a newer templ or air raises the app's `go` line, and `@latest` would do so silently), and declares templ, air and
  the framework's tool as `tool` directives, so `go tool templ`, `go tool air` and `go tool govibe` run the pinned versions. A test checks the pins
  against the framework's `go.mod`. Air as a tool costs about 80 indirect modules in the app's `go.mod` (hugo among them); none is linked into the app.
- **Managed files** (the app's Makefile, .air.toml, the workflow under .github/workflows, and the block of its SKILL.md between `<!-- govibe:managed:start -->` and
  `<!-- govibe:managed:end -->`) carry a header and a hash in `govibe.yml` (`managed.version`, `managed.files`). Everything else is the app's and is never
  touched again: `env.yml`, `README.md`, the docs, every Go file. `SKILL.md`'s block is rendered from `internal/rules` (the layering table, what each
  layer may import, the spec sections) and the facts of the version (the CLI, the dev tooling); its owned section is seeded once with the Hello GoVibe
  done criteria and the list of demo files to delete. `govibe upgrade` (below) re-renders the block and the files and refuses one the app edited.
- **`govibe check`** is what CI runs in an app (`go tool govibe check --format github` in the generated workflow, `make check`): the managed files
  against the manifest (edited, missing or unlisted ones are reported, not failed), then the gate of `govibe graph index --calls` in a scratch store
  (never the store a dev server holds), with the app's own routes and commands from its `inspect --json`, against `docs/graph/baseline.json` when it
  exists (`--fail-on error` by default; `--fail-on info` is "no finding at any severity"). An app that does not boot (an invalid schedule, say) fails the
  check with the app's own error, which names the command. Exit 0, 1 on a new finding or a baseline entry without a reason, 2 when the check could not run.
- **The keystone** (`internal/scaffold`, `TestKeystone`, run by the `ci` workflow's `keystone` job and `make keystone`; skipped without
  `GOVIBE_KEYSTONE`): builds the tool from the checkout, runs the real `govibe new --replace <checkout>` into a temp directory, applies the overlay in
  `testdata/overlay` (a scheduled command, its thin adapter, a test on the fake clock, a doc that names it), builds the app both ways, vets and tests it,
  runs `govibe check --fail-on info` with the empty baseline, boots the dev build and asks for the demo, `/dev`, the graph status, `graph_schema` and
  `schedule_status` over `/dev/mcp` and `schedule list`, boots the production build and expects 404 on every `/dev` path and no `/dev` link, checks
  the default build links no dev package and the tree holds no `SPEC_*`, and plants a `framework.import` and a `layering` violation that must fail the
  check. Everything runs under a scrubbed environment (no DSN or secret of the shell reaches it) and `GOWORK=off`. Phase 6 added the generators
  (a resource, a scheduled command and a page, then the gate at `--fail-on info`, the dev build serving the new routes, `schedule list`), the
  upgrade (nothing to do, an edited Makefile refused with its diff, `--force`, the owned README untouched, the check green after) and the doctor.

## Generators, upgrade, doctor (`govibe make`, `govibe upgrade`, `govibe doctor`)
Phase 6 of `docs/specs/SPEC_FRAMEWORK_EXTRACTION.md`. The tool adds code to an app, keeps the managed files current across framework versions,
and says what is wrong with a project before it costs time.

- **`govibe make <kind> <name>`** (`internal/generate`) writes files that obey the layering rules, in the layout the project's `govibe.yml`
  declares (the first directory of each layer; the generated app's by default), and registers what it made where it belongs: a route or a
  command at the marker comment in the wiring (`// govibe:routes`, `// govibe:commands`: the file of the wiring layer that holds the line, found by
  searching the wiring directories one level deep; the marker stays, so the next generator inserts after), and a line under the "Routes",
  "Commands" or "Tables" heading of the app's architecture and schema docs, so `docs.route_undocumented` and `docs.command_undocumented` stay
  green. Insertion is textual at the marker, then the file is parsed, the imports added with `astutil.AddImport` and the file gofmt-ed; when a
  marker or a heading is missing the snippet is printed instead of guessed at. A file that exists is refused without `--force`; `--dry-run` writes
  nothing. A generator is a function from a `Project` (go.mod, the app's name from main.go, the profile) to a `Result` (files to create or edit,
  snippets, notes, whether templ must run), so each has a golden test under `internal/generate/testdata/golden`, and one test writes every
  generator's output into a generated app, builds it both ways, vets, tests, and runs the gate at `--fail-on info` and the doctor: zero findings.
  The kinds: `page` (a templ page with `web.HTMXScript`, the controller, the route; `go tool templ generate` runs after), `resource` (the vertical
  slice: a migration creating the table, a store package over `*sqlx.DB` whose queries answer `ErrNoDatabase` while no DSN is set, a service with
  a `Reader` interface and a test on a fake, a JSON controller with list and get, the two routes constructed in the routes function, the doc lines),
  `action` and its test, `service` and its test, `store` and its test, `migration` (numbered after the last; `--table` creates the table and documents
  it), `middleware` and its test (chi wants `Use` before any route, so the snippet is printed, not inserted at the marker), `command` (noun:verb reads
  as VerbNoun; the body in the commands package, the declaration at the marker, the doc line; `--schedule` is validated by `scheduler.Parse`, and a
  test on the fake clock advances to the first due time computed from the spec), `config` (a struct for the app's own section of `env.yml`,
  nested maps as nested structs, types from the placeholders' defaults, with a `Load(cfg)` whose receiver name the config layer recognises as a
  read) and `spec` (the sections of `rules.SpecSections`). Names may be written as `order`, `rate-limit`, `rateLimit` or `RateLimit`.
- **`govibe upgrade`** (`scaffold.PlanUpgrade`, `Upgrade.Apply`): after `go get` of a new framework version, each managed file is rendered for the
  version `go.mod` pins (`v0.0.0` when a `replace` points at a checkout) and compared with the file on disk and with the hash the manifest recorded.
  A file the app never edited is rewritten with its diff shown (`updated`), a missing one restored, one the app edited is refused with its diff
  (`edited`; exit 1, nothing written) unless `--force` (`forced`). `SKILL.md` is special: only the block between the markers is replaced, the rest is
  the app's; a `SKILL.md` without markers counts as edited and `--force` puts the block at the top. The `managed:` section of `govibe.yml` is swapped
  textually (the app's own keys and comments stay) with the new version and hashes, so `check` is green after. The notes of the versions crossed come
  from the embedded `UPGRADING.md` (`scaffold.UpgradeNotes`: every released version after the manifest's up to the pin, and the unreleased section
  when the pin is a commit or a checkout). `--dry-run` shows the plan.
- **`govibe doctor`** (`cmd/govibe/cmd_doctor.go`) reports, as ok, note, warn or fail: the tool's version against the framework version `go.mod`
  pins (a mismatch warns: run it as `go tool govibe`; a `replace` is a note), the managed files against the manifest, every spec against
  `rules.SpecSections`, the baseline's entries each saying why (fail otherwise: the gate fails on them too), the generators' markers, a script tag
  that loads from the network (htmx comes from the framework), and, unless `--no-build`, the app's own `inspect --json` run with the scrubbed
  environment (an invalid schedule or a registration that opens a connection fails here with the app's own words), static files or migrations on
  disk that the declaration does not hand to the framework (from `contributes` in the document), and a dev package in `go list -deps -tags ''`.
  Exit 1 on a failure. `govibe check` runs the static checks before the gate and prints what is not ok.

## Hardening and open source (Phase 7)
Phase 7 of `docs/specs/SPEC_FRAMEWORK_EXTRACTION.md`: the version shim, `App.DevTools` (above), the Option D decision, and the repository as an
open-source project.

- **The version shim** (`cmd/govibe/shim.go`). A globally installed `govibe` is needed once, for `govibe new`. Inside an application, every other
  command runs as the version the app pins: before cobra sees the arguments, `main` reads the nearest `go.mod` (from `--root`, `GOVIBE_ROOT` or the
  working directory up), and when it requires the framework at a version other than this binary's and pins the tool (`tool github.com/petars-dev/govibe/cmd/govibe`),
  it runs `go tool govibe <the same arguments>` in the same directory with `GOVIBE_SHIMMED=1` (the child never shims again), says so on stderr in one
  line, and exits with the child's status. It never delegates for `new`, `version`, `help` and `completion`, when the app replaces the framework with a
  checkout (the pin is `v0.0.0`, and `go tool govibe` would be this flow again), when `go.mod` does not pin the tool, or with `GOVIBE_SHIM=off`. This
  closes AM14 together with the doctor's version check: a stale global tool can no longer judge a project by rules the project does not build against.
  `govibe version` prints the tool's version and, inside a project, the version it pins.
- **Option D, decided: no.** The out-of-process dev host (a supervisor that builds and runs the app, hosts the panel and proxies to the app) was
  evaluated in spike S10 (`docs/spikes/notes/S10.md`): the proxy adds about 60 microseconds and halves the throughput of a trivial handler on
  loopback (tolerable), bolt's file lock means a supervisor could not read the store while the app process holds it (a second open times out, read-only
  included), and with Air's `stop_on_error`, a failing build stops the old binary and with it the panel, so the one real benefit, a panel that survives a
  broken build, is real. It is not worth a supervisor, a proxy and a second process model for v1: what an agent needs when the build is broken is the
  graph, the findings and the docs, and those come from source, not from the process. The answer is in the tool, in the backlog: a `govibe mcp` command
  serving the graph tools over stdio from a scratch store, without the app running. Option C stays.
- **Open source.** `README.md` is written for a visitor; `CONTRIBUTING.md` (how to work on the framework, the tests both ways, the gates, scratch
  copies, the keystone, the release steps), `SECURITY.md` (how to report, what is and is not in scope: the dev tools are not meant to be exposed),
  issue and pull request templates under `.github`, the versioning policy in `docs/public/UPGRADING.md` (semver; before 1.0 a minor may change the four
  public packages, each change listed there; `internal/` changes freely), and `go doc` examples for `govibe`, `config` and `web` (`example_test.go` next to each;
  `govibetest` documents its use in its package comment). The `ci` workflow gained an `api` job (`gorelease` against the last tag, once one exists), and `.github/workflows/release.yml` publishes a GitHub release
  from the `UPGRADING.md` section of the tag when a `v*` tag is pushed. The history and the tree were scanned for private references: none beyond the
  committers' own addresses in the git log.

## The project profile and the app's own graph
Phase 3 of `docs/specs/SPEC_FRAMEWORK_EXTRACTION.md`. The graph, the findings, the reports, the docs and the agent tools are about the
application by default; the framework it runs on is one switch away and always labelled.

- **Profile** (`internal/profile`, `govibe.yml` in the project root). Every key is optional and overrides the default for that key alone;
  an unknown key, an absolute path or one that leaves the project is an error that names the file and the key. Keys: `framework` (the module the
  project is built on), `config` (settings files, lowest first), `migrations`, `docs` (`dirs`, `specs`, `spec_prefix`, `root`), `graph`
  (`dir`, and `derived`, the globs of generated files a clone lacks, so a doc naming one is not stale), `layout` (layer to directories, the most
  specific directory wins, a layer's entry replaces its default; `commands` is a layer), and `rules` (a rule `off` or at a severity). The
  default is a generated app's layout. The indexer takes every path from it, `govibe.yml` is an input of every layer's fingerprint, and a
  `config` layer with no settings file is `skipped` (it has not looked) rather than empty.
- **Origin and scope.** A route (who registered it), a middleware, a setting, an environment variable, an MCP tool and an external node carry
  `origin` (`app` or `framework`); everything else was indexed from the project's own tree and is the app's, and a finding has the origin of the node it is
  about. `Load(ctx, qs, scope...)` applies a scope **once, in the `Model`**: it drops the nodes of the other origin and every edge that touches one,
  and remembers what it dropped (`Model.Hidden`: the banner's "hides N nodes", the inspector's one line `uses framework: web, config`, the error that says a
  node exists in another scope). The gate always reads scope `all`; `graph_query` and the Cayley API are deliberately unscoped. A route's origin is who
  registered it (the kernel snapshots the router before `App.Routes` runs; the running binary reports it as `mounted_by` in `inspect --json`; a path under
  `/dev` or `/_govibe` is the framework's), a middleware is the framework's when it is on the framework's own routes.
- **Externals** (`externals` layer). What the project takes from outside is `external` nodes with a module, a version and an origin: the framework's
  public packages it imports (also from `.templ` headers, whose generated Go is not indexed), the functions it calls (the calls layer), what serves a route
  that is not its own code. Bounded: one leaf per public framework function, one for the framework's `internal/`, one per library package, the standard library
  not at all. The layers that point at them write only the edge (`imports_external`, `calls_external`, `handled_by_external`); this layer owns the nodes,
  because a node's quads belong to one layer. Versions come from the running binary's module table when there is one, else from `go.mod`.
- **The framework manifest** (`devtools.FrameworkManifest`): the framework's settings (its defaults file and its typed `Config` fields, so `cfg.Server.Port` is a
  read of `server.port` and no app is told it never reads `dev.graph.calls`) and its MCP tools, as nodes of origin `framework`. The tool and the library are one version,
  so the offline run uses the same manifest.
- **Offline facts.** `govibe graph index` in an application asks its own binary (`go run -tags govibe_dev . inspect --json`: no database, no dev mount, no
  scheduler) and feeds the route table, the command table (with schedules), who registered each route and the module table to the indexer (`codegraph.OptionsFromInspect`), so the route rules run in CI;
  `--no-inspect` skips it. The document's types are `internal/routeinfo` (`Inspect`), shared by the app binary and the tool.
- **Rules as data and the new findings.** `internal/rules` holds the layering table, the framework-import table and the spec sections. `framework.import` (error)
  judges every package, and every `.templ` file by its directory, against what its layer may import from the framework: business and storage nothing; controllers,
  middleware and views `web`; commands `govibe`; wiring `govibe`, `config` and `web`; nothing from the framework's `internal/`. It does not apply to the framework's own
  repository. `commands` is an architecture layer with the controllers' rule. A baseline entry needs a `reason` (and records the date): `--update-baseline --reason`,
  and a baseline entry without one fails the gate.
- **The agent surface.** Every graph tool takes `scope` (default `app`); `graph_schema` names the app, the framework and its version, the scope and what it hides.
  New resources: `govibe://skill` (the project's `SKILL.md`, else the rules rendered from the tables), `govibe://app/manifest` (what the `App` declaration contributes, from
  `inspect --json`) and `govibe://docs/<slug>` (the framework's docs). The server is titled after `App.Name`.
- **The docs surface.** `/dev/docs` opens on the app's own docs (`/dev/docs/app`) with its specs next to them, from the directories its profile names; the framework's docs
  are a muted group under `/dev/docs/govibe/<slug>`, every title prefixed `GoVibe:`. The set is an allowlist (`internal/frameworkdocs`): the guide, configuration, commands
  and scheduler, architecture, graph schema, dev DB explorer, the rules (rendered), upgrading. The embedded copies are generated from `docs/public` (`make docs-embed`) and a
  test fails when one is stale; the framework's `SPEC_*` files are never embedded or served in an app (a test asks for every one of them under every URL). A contributor sets
  `dev.framework_src` (`GOVIBE_FRAMEWORK_SRC`) to a checkout: the docs are then read from it and its specs are listed in a group of their own.
- **`/dev`** is the front door: the app, the framework version, the environment, what is mounted (and why not, when it is not), counts of the app's own routes, commands,
  packages and findings, and what to do first when the app has no routes.
