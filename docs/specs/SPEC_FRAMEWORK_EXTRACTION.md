# SPEC_FRAMEWORK_EXTRACTION

## Description
Turn GoVibe from a skeleton repository you clone and edit into a **framework you depend on**. Today
this repository is both: about 21,000 non-test Go lines, of which roughly 100 are the Hello demo
(`controllers/home.go`, `controllers/api.go`, `controllers/hello.go`, `business/actions/hello.go`,
`assets/templates/home.templ`). Everything else is framework: the boot sequence, config loader, router
assembly, dev docs/graph/db pages, the dev MCP server, the graph indexer, and the CLI.

After this spec a real project looks like this:

- It is its own Go module, created with `govibe new`. It `require`s the GoVibe module (public, open source).
- Its repository holds **only application code**: controllers, commands, business actions and services,
  storage stores and migrations, templ views, app routes, `env.yml`, docs and specs. No dev tooling, no boot code,
  no router assembly, no copy of the graph indexer, no mail settings.
- It starts with the **Hello GoVibe demo** (`/`, `/api/hello`, `/hello`): ordinary app code under the usual layers (two
  controllers, an action, a templ page and its own static files) that you keep as a smoke test or delete when you
  build your own.
- Its `main.go` is a declaration: it hands the framework what the app contributes, and the framework runs it
  (inversion of control). The framework never imports the app.
- It can add **CLI commands** with one declaration and put any of them on a **schedule**; the framework runs
  the scheduler when the app starts and `schedule list` shows what is scheduled.
- It still has everything it has today, from the dependency: `/dev/docs`, `/dev/graph`, `/dev/db`, `/dev/mcp`, the
  findings gate, `govibe graph index`, migrations, embedded assets, one binary.
- **The dev panel is about the app.** The graph, the findings, the routes report, the docs and the agent tools show the
  app's own code and docs by default. The framework is one switch away and always labelled; its docs are
  prefixed; its development specs are never shipped.
- It follows the rules of `SKILL.md` (layering, embedded assets, config in `env.yml`, spec-driven docs). The rules ship
  **with the framework as data**, a generated app carries them, and the graph findings enforce them in CI. A fresh
  app is born with zero findings.

This spec answers "how should a Go framework be built" for GoVibe specifically, records the options
considered, and plans the move from the current repository in phases that each leave a working repo.

## What the repo tells us (constraints that shape the design)
1. **The repo is ~99% framework and the two halves are interleaved.** `controllers/` holds `home.go`
   next to `devgraph*.go`; `assets/static/` holds `home.css` next to `graph.js`; the graph indexer lives in
   `business/services/codegraph` (16,700 lines in the layer that is supposed to hold *app* logic).
2. **`routes.NewRouter` is a 160-line god function.** Five lines are the app (`/`, `/api/hello`, `/hello`,
   assets); the rest mounts the dev tools, opens the graph store, opens a **second** database pool for the
   explorer, builds the indexer and starts it. It also calls `config.Load()` itself, twice.
3. **There are two boot paths and they have already drifted.** `main.go` opens the database, runs
   migrations and initializes WorkOS; `govibe serve` (`cmd/govibe/cmd_serve.go`) does none of that. The WorkOS
   client is created and discarded (`_ = workos.NewClient(...)`).
4. **The dev pages are coupled to the app's static directory.** `controllers/devdocs.go`,
   `devgraph.go`, `devgraph_reports.go` and `devdb_page.go` load `/assets/static/theme.css`, `docs.css`,
   `graph.js`, `cytoscape.min.js`; `home.templ` loads `/assets/htmx/htmx.min.js`. An app that owns `assets/`
   would break the panel.
5. **"None of this exists in a production build" is only true at runtime.** `go list -deps .` shows 440
   packages in today's binary, including 28 from cayley, 19 from `golang.org/x/tools`, 10 from the MCP SDK,
   10 from goldmark and 4 from chroma, all gated by `!cfg.IsProd()`. Production needs yaml, logrus,
   mysql, sqlx, goose, chi and the templ runtime.
6. **The indexer hard-codes this repo's layout.** `config/env.yml` (`layer_config.go`),
   `storage/migrations/sql` (`layer_db.go`), `docs/public` + `docs/specs` + `README.md` + `SKILL.md`
   (`layer_docs.go`), the directory-name to layer table (`classifyPackage` in `code.go`), the graph store
   path `docs/graph` (`graphdb.DefaultDir`), and `Root: "."` (`routes.go`). These must become a profile.
7. **The indexer already has the right seams, unexported.** The routes layer takes either a live router or an
   injectable route source (`routeSource`); the db layer takes a schema reader function; `Options.Root` exists.
   The previous spec's backlog already asks to "split `routes.NewRouter` into wiring and side effects".
8. **`go:embed` cannot cross a module boundary** ([embed docs](https://pkg.go.dev/embed)). The framework can embed
   its own files; the app must embed its own `env.yml`, migrations and assets and hand them over as `fs.FS`.
   `env.yml` can be embedded straight from `main.go`, because `main` lives in the project root next to it.
9. **Go 1.24+ can pin command-line tools in `go.mod`** (the `tool` directive, `go get -tool`, `go tool <name>`;
   [proposal 48429](https://go.googlesource.com/proposal/+/master/design/48429-go-tool-modules.md)). This repo
   is on Go 1.25. It makes "the CLI and the library are the same version" a property of the app's `go.mod`.
10. **The module path is not fetchable.** `module petars-dev/govibe` has no host; the remote is
    `github.com/petars-dev/govibe`, and 38 tracked files carry the old path. A dependency needs a real path.
11. **The graph gate and the fixture are reusable as the framework's acceptance test.** `govibe graph index
    --fail-on error` already judges a project against the SKILL rules, and `testdata/fixture` is already a
    small app in the target layout (controllers, a service, a store, migrations).
12. **The framework has findings nobody has addressed.** An index with an empty baseline reports seven on a developer
    machine and ten in a fresh clone (what CI sees: three `docs.stale_ref` warnings appear because the docs name
    `docs/graph/indexes.bolt`, a git-ignored file that exists only after the dev server has run). The seven: one real
    layering error that `docs/graph/baseline.json` accepts (`controllers/devdb.go` imports `storage/dbexplorer`),
    five `config.unused` for a `mail.smtp.*` block that nothing reads and nothing asked for (it is in
    `config/env.yml`, `SKILL.md` in two places and a comment in `config/config.go`; SPEC_GRAPH_INDEXER recorded
    it as an unpredicted finding and it stayed), and one `code.unreferenced` that is a false positive
    (`graphcheck.Check` is only called from tests, which the indexer does not read). See "Findings".
13. **There is no scheduler and an app cannot add a CLI command.** Today a command is a `cmd_*.go` file inside
    `cmd/govibe` registered in an `init()` (`SKILL.md` and `README.md` both say so). Once the CLI is a
    dependency, an app cannot edit it. Commands need an extension point, and scheduling needs a home.
14. **The docs the panel lists are the framework's own.** `/dev/docs/specs` lists every `SPEC_*.md` in the project
    (the indexer, the DB explorer, this spec, a sample) and `/dev/docs/app` lists `GRAPH_SCHEMA.md` (90 KB) and
    `DEV_DB_EXPLORER.md`. They are process artifacts and reference docs of the framework; a scaffold that copied
    `docs/` would hand them to every app, and would bury the app's own docs.

## Goals
- `govibe new <module>` produces a runnable app that depends on the framework, with the panel, MCP server,
  routes report and findings gate working on first `make dev`, and zero findings.
- App code contains no framework internals. **Business logic and storage import nothing from the
  framework**; controllers and views import only the small `web` helpers; only `main.go` and `routes/`
  touch the lifecycle API.
- The framework owns boot, lifecycle, config layering, router assembly, migrations runner, the scheduler,
  dev tooling, the scaffolder and the CLI, and can change them without touching app code.
- `env.yml` lives in the **project root**, not in a `config/` directory.
- Adding a CLI command is one declaration; scheduling it is one more field; `schedule list` shows the result.
- **The dev panel defaults to the app's own connections**: routes, handlers, services, stores, tables, templates,
  config keys, commands, docs, findings. Framework internals appear only on request and are labelled.
- The user's own docs come first. The framework's reference docs are available, grouped and prefixed. The
  framework's development specs are never visible in an app.
- The dev tooling is **impossible to ship by accident**: a default build contains none of it.
- The rules in `SKILL.md` are data in the framework, rendered into every app, and checked by the graph.
- Upgrading is `go get` plus one command, and never rewrites app-owned files.
- Move from the current repo in phases; each phase leaves tests green and the dev panel working.
- Leave the framework repo itself with no unaddressed findings: fixed, or accepted with a written reason.

## Non-goals
- A plugin ecosystem or third-party extension API (v1 has one extension point: the `App` declaration).
- A runtime DI container (fx/dig). Wiring stays explicit constructors.
- Replacing chi, templ, htmx, sqlx or goose. The stack in `SKILL.md` stays.
- Mail, auth, queues, a job queue with retries, production observability. Not asked for, not planned.
- A flag to skip the Hello demo: every new app has it, and its files are ordinary files that can be deleted.
- Out-of-process dev panel in v1 (evaluated in Phase 7, see Options).

## Acceptance scenarios
| # | Scenario | Passes when |
| --- | --- | --- |
| A1 | A new app in a minute | `govibe new github.com/acme/shop && cd shop && make dev`: boot prints the dev URL; `/` renders the Hello GoVibe page and `/api/hello` returns `{"message":"Hello GoVibe!"}`; the htmx button on the page works; `/dev` (the dev index), `/dev/graph`, `/dev/docs`, `/dev/db` (with a DSN) and `/dev/mcp` work; `govibe check` exits 0 with no finding at any severity |
| A2 | The app repo is clean | The generated tree has no file from the dev tooling, no mail keys, no framework spec; the only demo is Hello GoVibe, plain app code in the usual layers with its own static files; `env.yml` is in the root; `main.go` is a declaration of under 30 lines; `go list -deps ./...` of the app, default build, contains none of cayley, the MCP SDK, x/tools, goldmark, chroma |
| A3 | Agents orient themselves | In the app, `graph_schema` names the app and the framework version; `graph_context` on a route returns the app's handler and action, labelled `app`; with scope `all` it also returns the framework's middleware, labelled `framework` |
| A4 | The rules hold | A `business` package that imports `govibe/web` fails `govibe check` with a `framework.import` finding; a controller or a command that imports a store fails with `layering` |
| A5 | Upgrade is boring | Bump the framework version, run `govibe upgrade`: managed files show a diff, app-owned files are untouched, the build and the gate stay green |
| A6 | The framework tests its own output | CI scaffolds an app from the working tree, adds a test scheduled command from an overlay, builds both ways, runs its tests and its gate, boots it and hits `/`, `/api/hello` and the dev endpoints |
| A7 | Production is clean | The default-tag binary answers 404 on `/dev/*`, `go list -deps` has no dev packages, and the Hello page it serves contains no `/dev` link; CI fails if any of that stops being true |
| A8 | The panel is about the app | In an app with a few routes, `/dev/graph` opens on the app map and shows no `/dev/*` route, no framework middleware, no framework package; the scope switch reveals them, labelled `framework`; the findings panel, the reports and every `graph_*` tool default to the same scope |
| A9 | Commands and the scheduler | A command declared with a schedule runs by `./app <name>` and, with `serve` running, at its due time (proved with a fake clock); `./app schedule list` shows it with its next run; `schedule run` runs only the scheduler; an invalid spec fails boot and `govibe check` naming the command; `/dev/schedule` lists tasks and recent runs |
| A10 | Docs: yours first | `/dev/docs` opens on the app's own docs and specs; the framework's docs are a separate group under `/dev/docs/govibe/`, every title prefixed `GoVibe:`; no `SPEC_*` of the framework is reachable in an app, by test |

## How Go frameworks are built (and what to copy)
Not a review of source, a reading of the public docs. Sources are at the end of this file.

| Approach | Examples | What the app owns | Fit for GoVibe |
| --- | --- | --- | --- |
| Toolkit you compose | chi, Gin, Echo, Fiber plus sqlx | everything, including all wiring | No: the 160-line wiring stays in every app |
| Template you copy | `gonew`, boilerplate repos | a full copy of the skeleton | No: every app becomes a fork; upgrades are copy-paste. This is today's state |
| **Framework module plus app skeleton plus CLI** | Goravel (starter app imports the framework, `artisan` console), Buffalo (generators plus tooling), Kratos, GoFrame, go-zero (`kratos new`, `gf init`, `goctl`) | declarations and domain code | **Yes: this is the shape asked for** |
| Compile-time analysis | Encore (annotations, infra derived from code) | annotated code | No: changes how code is written, large investment |
| Runtime DI container | uber fx/dig, google/wire | constructors plus a graph description | No for core: hidden wiring fights an explicit, agent-readable codebase and the indexer's static view |

Lessons that shape the design (a synthesis, not a quote):
1. **The framework runs the app, the app declares itself.** One entry call, one declaration struct. Everything
   the app contributes is visible in one place, which is also what an agent reads first.
2. **A small public surface and a large `internal/`.** The compiler enforces `internal/`, so it is the only
   reliable way to keep apps off the guts and keep the framework free to refactor.
3. **Two binaries with a clean rule.** Commands that need the app's embedded files or config (`serve`,
   `migrate`, `schedule`, the app's own) live in the app binary; commands that edit or read source (`new`, `make`,
   `check`, `graph index`) live in the tool.
4. **Scaffolds rot unless drift is a first-class concept.** Mark which generated files the framework may
   rewrite (managed) and which are the app's (owned), and measure drift.
5. **Pin the tool to the library.** A CLI that disagrees with the library version silently mis-judges the
   project. The `tool` directive removes the possibility.
6. **Generated code must pass the gate.** The scaffolder's output is its own acceptance test.

## Architecture options considered
| Option | Summary | Verdict |
| --- | --- | --- |
| A. Keep the template repo | Fork per project | Rejected: no inversion; upgrades never reach apps |
| B. Library toolkit | App assembles chi, config, dev tools itself | Rejected: app code stays full of boot and dev wiring |
| **C. Framework module, `govibe.Main` inversion of control, scaffolding CLI** | App declares; framework runs; dev tools hosted in the app process, compiled in only with a tag | **Chosen for v1** |
| D. Out-of-process dev host | `govibe dev` builds and runs the app, hosts the panel and MCP itself, app exposes a tiny introspection command | Deferred (Phase 7 evaluation): best resilience (the panel and MCP survive a failing build, which is exactly when an agent needs them) and no dev code near the app, but a supervisor, a proxy and a rewrite of the bolt-lock and live-router assumptions |
| E. Compile-time framework | Encore-style | Rejected |
| F. Runtime DI container | fx/dig | Rejected for core; apps may use wire inside their own wiring |

**Why C now, D later.** C preserves today's behavior exactly (live `chi` router read in-process, one process
owns the bolt lock, one database pool), so the move is mechanical and verifiable. The dev packages depend on
**ports they declare themselves** (a router to walk, a schema reader, a profile, a root directory), never on
the kernel or the app, so moving them behind a supervisor later is additive: the same packages, a different
host.

## Design

### The shape
```text
  +--------------------- app (module github.com/acme/shop) ----------------------+
  | main.go      govibe.Main(App{Config, Migrations, Assets, Routes, Commands})   |
  | env.yml      the app's settings, in the project root, embedded by main.go      |
  | routes/      routes.go: paths -> handlers    console.go: commands, schedules   |
  | controllers/  commands/   ->   business/{actions,services}  ->  storage/<x>    |
  | assets/{templates,static}    storage/migrations/sql    docs/                   |
  +---------------------------+-----------------------------^----------------------+
                              | imports (one way)           | calls back: Routes,
                              v                             | Commands, Shutdown
  +---------------------------+-----------------------------+----------------------+
  | govibe (module github.com/petars-dev/govibe)                                    |
  | public    govibe  config  web  govibetest                                        |
  | internal  kernel  migrate  scheduler  rules  profile  scaffold                  |
  |           devtools (docs, graph, db, mcp, schedule)  codegraph  dbexplorer      |
  | cmd/govibe  the tool: new make check doctor upgrade graph                        |
  +----------------------------------------------------------------------------------+
```
Rules of the picture: the app imports the framework, never the reverse (the module graph makes the reverse
impossible); the app imports only the four public packages; `devtools` is imported by `kernel` only under a
build tag; the tool imports `internal/*` freely because it is in the same module.

### The inversion: three seams
1. **Runtime seam, the `App` declaration.** The framework calls the app's `Routes`, `Commands` and `Shutdown`;
   the app never calls boot code. This replaces `main.go` plus `routes.NewRouter`.
2. **Project seam, the `Profile`.** The tooling (graph, docs pages, doctor, generators) learns where a project
   keeps config, migrations, docs and layers from a profile, not from constants. The default profile is the
   `SKILL.md` layout; `govibe.yml` can override it. This replaces the hard-coded paths in constraint 6.
3. **Tooling seam, the two binaries.** The tool edits and reads source. The app binary runs the app. Each
   command lives where the files it needs live.

### Framework module layout
```text
govibe.go            App, Runtime, Command, Schedule, Main, Run        public
config/              layered loader, Config, Get/Unmarshal; defaults.yml  public
web/                 render, JSON, error, htmx and IfDev helpers, middleware  public
govibetest/          boot an App in a test (httptest, env, fake clock) public
cmd/govibe/          the tool
internal/kernel/     boot, lifecycle, router assembly, reserved namespaces
internal/migrate/    goose runner (dialect always explicit)
internal/scheduler/  cron-spec parsing, runner, clock, overlap/timeout/recover
internal/rules/      SKILL rules as data: layering, framework imports, spec sections
internal/profile/    govibe.yml, layout, default profile
internal/scaffold/   embedded templates, renderer, managed-file manifest
internal/devtools/   Mount(Deps): index, docs, graph, db, mcp, schedule; devui (pages + embedded static);
                     frameworkdocs (the curated, embedded GoVibe docs)
internal/codegraph/  dbexplorer/  graphdb/  devmcp/   (moved, not rewritten)
examples/hello/      the framework's reference app (its own go.mod); CI dogfood; the source of every new app's demo
docs/                framework docs and specs (this directory); specs are never embedded
```
Only `govibe`, `config`, `web` and `govibetest` are public API. Everything else is `internal/`, so it can be
reorganized freely. One Go module for v1 (see Decisions). The framework is public and open source under the
Apache-2.0 license the repository already carries.

### The app contract (sketch, finalized in Phase 1)
```go
// App declares what an application contributes. Every field is optional.
type App struct {
	Name       string                                   // logs, MCP title, panel banner
	Config     fs.FS                                    // holds env.yml, merged over the framework defaults
	Migrations fs.FS                                    // embedded sql/*.sql (goose)
	Assets     fs.FS                                    // embedded static files, served at /assets/*
	Routes     func(rt *Runtime, r chi.Router) error    // the app's HTTP surface
	Commands   func(rt *Runtime) []Command              // the app's CLI commands, some of them scheduled
	Shutdown   func(ctx context.Context) error
}

// Runtime is what the framework hands the app. Nothing here is a global.
type Runtime struct {
	Config config.Config     // typed framework keys plus Get/Unmarshal for the app's own
	Log    logrus.FieldLogger
	DB     *sqlx.DB          // nil when no DSN is configured
}

type RunFunc func(ctx context.Context, rt *Runtime, args []string) error

type Command struct {
	Use, Short string
	Run        RunFunc
	Schedule   *Schedule     // nil: CLI only
}

type Schedule struct {
	Spec    string           // "*/5 * * * *", "@every 10m", "@daily", "CRON_TZ=Europe/Sofia 0 9 * * *"
	Overlap Overlap          // Skip (default) or Allow
	Timeout time.Duration    // 0: none
}
func Cron(spec string) *Schedule       // helpers, so a declaration reads on one line
func Every(d time.Duration) *Schedule

func Main(app App)                                                  // parse os.Args; default command is serve
func Run(ctx context.Context, app App, args []string, out, errOut io.Writer) error // for tests and tooling
```
Why a struct of fields rather than an interface or functional options: it is declarative, zero-value valid,
and the whole contribution surface sits in one literal that an agent (or the indexer) reads in one place.
Behavior that is optional is a nil field, not an optional-interface type assertion.
`Routes` receives a `chi.Router` that already carries the standard middleware and the asset mounts, so the app
keeps using plain chi (`SKILL.md`: "chi v5 router with middleware and subroutes"). `Commands` and `Routes` are
given the same `rt`, and both must tolerate a nil `rt.DB`: they must not open connections or read secrets while
registering, because the tool and `schedule list` call them with no database to list what exists. Command flags
use the standard `flag` package through an optional `Flags func(*flag.FlagSet)` on `Command` (finalized in Phase 1).

### Boot sequence (`internal/kernel`, one path for `Main`, `serve` and tests)
1. Load config once: framework defaults, then the app's `env.yml`, with `${ENV:default}` resolved from the process
   environment, exactly as today. No globals.
2. Set up logging.
3. Open **one** database pool if a DSN is set; run `App.Migrations` with the configured dialect (always
   explicit; goose defaults to postgres otherwise).
4. Build the router: RequestID, RealIP, Recoverer, request logger; asset mounts; `App.Routes`; then check that
   the app registered nothing under the reserved prefixes `/dev/` and `/_govibe/`.
5. Build the command table from `App.Commands`; validate every schedule spec (an invalid one is a boot error
   that names the command).
6. If built with the dev tag and `!IsProd()`: `devtools.Mount(Deps{...})` (below), then start the indexer
   goroutine, and print the dev URL. Never fatal, never delays startup (unchanged from SPEC_GRAPH_INDEXER).
7. Start the scheduler if `scheduler.enabled` and any command has a schedule; serve with the current timeouts;
   on SIGINT/SIGTERM stop starting runs, wait for running ones up to `scheduler.shutdown_timeout`, shut the
   server down gracefully and call `App.Shutdown`.
Read-only commands (`routes`, `schedule list`, `inspect`, `config print`) run steps 1, 2, 4 and 5 with no database,
no dev mount and no scheduler.

### What a generated app looks like
```text
shop/
  main.go                       govibe.Main(newApp()); embeds env.yml; the only boot code
  main_test.go                  boots newApp() with govibetest
  env.yml                       the app's settings, project root; no framework keys, no mail keys (the demo needs none)
  govibe.yml                    project manifest: framework path, layout and rule overrides, graph gate, managed files
  go.mod                        require govibe; tool: govibe, templ, air (pinned)
  SKILL.md                      managed rules block, plus an owned section seeded with the demo's criteria
  README.md                     seeded once; the app's
  Makefile  .air.toml           managed; call `go tool ...`, pass -tags govibe_dev for dev
  .github/workflows/govibe.yml  managed; the gate
  routes/routes.go              the demo's three routes; explicit dependency construction goes here as the app grows
  routes/console.go             command declarations and schedules (empty to start)
  controllers/                  home.go, api.go, hello.go (the demo); thin adapters taking small interfaces
  commands/                     thin command adapters (empty to start)
  business/actions/hello.go     the demo's action; business/services/ empty to start
  storage/<store>/  storage/migrations/{embed.go,sql/}   no store, no migration to start
  middleware/                   app middleware only
  assets/{embed.go,templates/home.templ,static/}   the demo's own page and files (home.css, theme.css, theme.js,
                                                   govibe-logo.png, govibe-hero.png); no dev UI assets, no htmx
  docs/{public,specs,graph}     ARCHITECTURE.md (names the demo routes) and MEMORY_SCHEMA.md seeded once; specs empty; baseline.json
```
There is no `config/` directory: a typed struct for the app's keys is optional and comes from `govibe make config`.
**The demo is plain app code.** Nothing in it is special to the framework except two helpers from `web`: the htmx
script tag (the framework serves htmx) and `IfDev`, which wraps the page's links to `/dev/*` so a production build shows
none (below). Its look comes from its own `theme.css`, `theme.js`, `home.css` and images in the app's `assets/static`,
not from the dev panel's files, so a framework restyle of the panel cannot change the app's page. To start your own app
you delete the files the seeded `SKILL.md` section and `README.md` list; nothing else refers to them.
```go
// main.go
//go:embed env.yml
var configFS embed.FS

func newApp() govibe.App {
	return govibe.App{
		Name:       "shop",
		Config:     configFS,
		Migrations: migrations.FS,
		Assets:     assets.FS,
		Routes:     routes.Register,
		Commands:   routes.Console,
	}
}

func main() { govibe.Main(newApp()) }

// routes/routes.go, as generated: the Hello GoVibe demo
func Register(rt *govibe.Runtime, r chi.Router) error {
	r.Get("/", controllers.Home)
	r.Route("/api", func(api chi.Router) { api.Get("/hello", controllers.HelloAPI) })
	r.Get("/hello", controllers.Hello) // htmx-friendly plain text
	return nil
}

// routes/routes.go, once the app has data (illustrative)
func Register(rt *govibe.Runtime, r chi.Router) error {
	orders := orderstore.New(rt.DB)   // nil DB is fine until a query runs
	svc := ordersvc.New(orders)
	r.Route("/api", func(api chi.Router) { api.Get("/orders", controllers.ListOrders(svc)) })
	return nil
}

// routes/console.go (illustrative; it is empty in a new app)
func Console(rt *govibe.Runtime) []govibe.Command {
	svc := ordersvc.New(orderstore.New(rt.DB))
	return []govibe.Command{{
		Use:      "orders:export",
		Short:    "Export yesterday's orders",
		Schedule: govibe.Cron("0 2 * * *"),
		Run:      commands.ExportOrders(svc),   // thin: calls the service
	}}
}
```
`main.go` and `routes/` are the `wiring` layer: they may import anything, as `routes`, `main` and `cmd` are exempt
today. That is where construction belongs, so `controllers`, `commands`, `business` and `storage` stay clean.
Commands are the CLI twin of controllers: thin adapters over business actions, with the same layering rule. The old
instruction "add a `cmd_*.go` in `cmd/govibe`" is replaced by `govibe make command`.

### Config
- **Where.** The app's `env.yml` is in the project root. `main.go` embeds it, so the binary stays self-contained. The
  framework embeds its own defaults (`server.*`, `database.*`, `scheduler.*`, `dev.*`) in `config/defaults.yml`, a
  different file with a different name so nothing is confused.
- **Layers.** Framework defaults, then the app's `env.yml` (it may override a framework default). Values are resolved
  from the process environment through `${ENV:default}` placeholders, **exactly as today**. Maps merge deeply, the app
  wins, lists are replaced, unknown keys are allowed. There is no override file and no second location: the file in
  the project root is embedded at build time, and the environment is the only deploy-time input. (Decided by the owner.)
- `config.Load` returns a value; the package-level `cfgRaw` global goes. The documented helpers become
  `rt.Config.Get("shop.currency")` and `rt.Config.Unmarshal("shop", &cfg)`.
- The app's optional `config` package keeps the `SKILL.md` convention (declare in `env.yml`, bind in a struct).
  The framework publishes a **manifest** of its own keys, so the config layer does not report framework keys as
  "read but not declared" in the app.
- WorkOS leaves core: the client is unused today. It returns as an optional add-on when asked for.

### Commands and the scheduler
- **Declare once.** A command is a `Command` in `routes/console.go`. With `Schedule` nil it is CLI only; with a
  `Schedule` it is also run by the scheduler. The same `Run` serves `./app orders:export` and the scheduled run, so a
  task is always testable and runnable by hand. Naming convention for apps: `noun:verb`.
- **Spec syntax.** Standard five-field cron, the descriptors `@hourly @daily @weekly @monthly @yearly`, `@every <duration>`,
  and an optional `CRON_TZ=<zone>` prefix. No seconds field in v1 (fewer footguns). Validated at boot, by
  `schedule list`, by `govibe check` and by the graph: an invalid spec is an error that names the command and the
  parse failure, never a silently missing task.
- **Time zone.** `scheduler.timezone` (default `UTC`); a `CRON_TZ=` prefix overrides it per task.
- **Overlap and timeouts.** Default `Skip`: a task that is still running when it is due again is skipped and logged.
  `Allow` runs it concurrently. `Timeout` becomes a context deadline; a command that ignores its context cannot be
  killed and is reported as overrun (documented).
- **Failures.** An error or a panic is recovered and logged with task, duration and status; the scheduler keeps
  going. No automatic retries in v1.
- **Missed runs.** Nothing is caught up after downtime or a restart. `@every` counts from process start.
- **Lifecycle.** `serve` starts the scheduler with the server unless `scheduler.enabled=false` or `serve --no-schedule`;
  `schedule run` runs only the scheduler in the foreground (a worker process); both stop gracefully.
- **Several instances.** Each instance runs every due task. Either run the scheduler on one instance
  (`serve --no-schedule` on the rest), or set `scheduler.lock: db`, which takes a named MariaDB lock per task
  (`GET_LOCK`) for the duration of a run. **The lock is never on by default**, even when a database is configured
  (decided by the owner): it is opt-in, so the default behavior is one anyone can predict. The framework says this
  in the docs and in the log line it prints when the scheduler starts.
- **Seeing what is scheduled.** `./app schedule list` prints every scheduled command, computed from the declarations
  and the current config, so it works in production and with no database. It does not ask a running server.
```text
$ ./shop schedule list
NAME             SCHEDULE      TIMEZONE  NEXT RUN             OVERLAP  TIMEOUT  DESCRIPTION
orders:export    0 2 * * *     UTC       2026-10-11 02:00:00  skip     10m      Export yesterday's orders
reports:digest   @every 6h     UTC       2026-10-10 18:30:00  skip     -        Send the digest
2 scheduled of 4 commands. Scheduler enabled; timezone UTC.
```
  `--json` and `--next N` (the next N fire times per task) are supported. Run history is not persisted in v1: the
  running server keeps the last 20 runs per task in memory and shows them at `/dev/schedule` (dev builds, with a
  "run now" button that goes through the same code path as the CLI) and through an MCP tool `schedule_status`;
  production has the structured log lines.
- **Engine.** A small runner in `internal/scheduler`, built on the cron-spec parser of `robfig/cron/v3` (stable but
  frozen since 2020, which is acceptable for a settled grammar) and nothing else of it. We own the semantics above,
  `Next(t)` works offline for `schedule list`, and a fake clock makes it testable. The alternative is `gocron` v2,
  actively maintained (v2.22.0, July 2026), at the cost of its engine owning overlap and next-run semantics. Spike S8
  chose the own runner (gocron parses with robfig anyway, de-duplicates a repeated DST hour on every spec and has no per-job
  timeout); the choice stays behind `internal/scheduler`.
- **In the graph.** A `commands` layer (from `inspect --json`, below) gives `command` nodes: name, description,
  spec, timezone, the function that runs it. `graph_context` and `graph_impact` on a table list the scheduled commands
  that reach it; `/dev/graph/reports/commands` is the generated table; `docs.command_undocumented` (info) mirrors
  the route rule.

### Dev tooling: hosting, production exclusion, reserved names
- **Hosted in the app process** in v1 (Option C): the live router, the bolt lock and the single database pool
  stay together, as SPEC_GRAPH_INDEXER constraint 1 requires.
- **Excluded from production by construction.** `kernel` reaches `devtools` only through one file tagged
  `govibe_dev` that registers the mount function in its `init` (no untagged twin: the indexer reads every file whatever
  its build constraints, so a twin would declare the same function twice). The default build links none of it. `make dev` and `.air.toml`
  pass `-tags govibe_dev`; the runtime `!IsProd()` gate stays as a second lock. A tagged build that finds
  `APP_ENV=prod` refuses to mount and logs why. When a local build lacks the tag, boot logs one line saying
  `/dev` is not compiled in and how to enable it. `schedule list` and every other runtime command are **not** dev-only.
- **`devtools` depends on ports it declares**: `Deps{Router, DB, Config, Root, Profile, AppName,
  FrameworkVersion}`, and the dev UI takes a small `Explorer` interface instead of the concrete explorer. It does not
  import `kernel` or any app package.
- **`/dev` index.** A new page, the front door to the dev tooling: app name, framework version, environment, links to the
  graph, docs, database, scheduler and the MCP URL, counts of routes, commands and findings, and what to do first
  when the app has no routes (for example after the demo is deleted).
- **Dev links in app pages.** `web.IfDev` is a templ helper that renders its children only when the dev tools are
  mounted. The kernel marks the request context when it mounts them (no global), so the demo's header tabs, calls to
  action and footer links to `/dev/*` appear in a dev build and are simply absent from a production build, never
  dead links. Any app page can use it.
- **Reserved namespaces.** `/dev/*` is the dev panel; `/_govibe/*` is framework runtime. Under
  `/_govibe/static/*` the framework serves its own embedded files: the dev UI's CSS and JS, cytoscape, the logo
  and the pinned htmx (`web.HTMXScript()` renders the tag). App assets stay at `/assets/*` and contain only the
  app's, which includes the demo's own copies of its theme, styles and images. In local/dev the app's
  `./assets` directory is served from disk with no-cache when it exists, else the embedded `App.Assets`
  (today's behavior).
- **Graph store** keeps its location (`docs/graph`), resolved from the app root (`GOVIBE_ROOT`, else the nearest
  directory holding `govibe.yml`).

### Docs: yours first, the framework's labelled
- **The app's docs are the front page.** `/dev/docs` opens on the app's `docs/public` (`/dev/docs/app`) with its
  `docs/specs` next to it (`/dev/docs/specs`). The scaffold seeds an app `ARCHITECTURE.md` and `MEMORY_SCHEMA.md`
  as **owned** files (the app's, never rewritten by an upgrade); specs start empty and come from `govibe make spec`.
- **The framework's docs are a separate, muted group.** They live under `/dev/docs/govibe/<slug>`, every title is
  prefixed `GoVibe:`, and the navigation shows them after the app's own. The set is a **curated, embedded allowlist**:
  `GUIDE`, `CONFIGURATION`, `COMMANDS_AND_SCHEDULER`, `ARCHITECTURE` (the framework's), `GRAPH_SCHEMA`,
  `DEV_DB_EXPLORER`, `SKILL` (the rules), `UPGRADING`. Slugs live in their own namespace, so they cannot collide with
  the app's.
- **The framework's specs are never shipped.** SPEC_GRAPH_INDEXER, SPEC_DEV_DB_EXPLORER, SPEC_SAMPLE and this one stay in
  the framework repository. A test fails if any `SPEC_*` is in the embedded set, and the scaffold test fails if a
  generated tree contains one. `SPEC_SAMPLE` is dropped: `govibe make spec` replaces it.
- **Agents.** The framework docs are served to agents as MCP resources (`govibe://docs/<slug>`), not as graph nodes:
  the docs layer indexes only the app's own `docs/`.
- **Contributors.** With `GOVIBE_FRAMEWORK_SRC=<checkout>` the framework docs are served from disk and a
  "GoVibe specs (contributors)" group lists the framework's `docs/specs`. Never on without the variable.

### CLI split
| Command | Where | Why |
| --- | --- | --- |
| `serve` (default), `migrate up/down/status`, `routes`, `schedule list`, `schedule run`, `config print`, `version`, the app's own commands | the app binary, from `govibe.Main` | needs the app's embedded files and config; works in production |
| `inspect --json` (hidden) | the app binary | one versioned JSON document (routes, commands, schedules) that the tool reads offline |
| `new`, `make`, `check`, `doctor`, `upgrade`, `generate`, `graph index` | the tool, `cmd/govibe` | reads or edits source |
| `govibe serve`, `govibe migrate`, `govibe schedule` inside an app directory | the tool, delegating to `go run -tags govibe_dev .` | same muscle memory, one implementation |

The app invokes the tool as `go tool govibe ...`, pinned in its `go.mod` next to `templ` and `air`. The globally
installed `govibe` is needed once, for `govibe new`. This fixes the version-skew failure mode: the tool that judges
a project is always the library version the project builds against.
**Offline facts.** `govibe graph index` gets the live route table and the command table by running the app's own
`inspect --json` (no database, no dev mount, no scheduler) and feeding it to the indexer's route and command sources.
That closes the previous spec's backlog item "build the real router in `govibe graph index`", so the CI gate can run the
route rules.

### Project profile and an app-only graph
- **Profile** replaces the constants in constraint 6: config file (default `env.yml`), migrations directory, doc
  directories and root docs, spec prefix, directory-to-layer table (now with a `commands` layer), graph directory, the
  `derived` paths that may legitimately be absent from a clone (default `docs/graph/*.bolt`), and per-rule
  on/off/severity overrides. Default values are the generated app's. `govibe.yml` may override them.
- **Origin.** Every package, function, route, config key, tool and command gets `origin` of `app` or `framework`.
  A route whose handler lives outside the app root, or sits under a reserved prefix, is `framework`. Symbols outside
  the app root become external nodes carrying module and version instead of failing to resolve.
- **Scope: the app-only view.** Every read takes a scope: `app` (the default), `framework`, or `all`. It is applied **once,
  in the `Model`**, which drops nodes of other origins and the edges that touch them, so the views, the reports, the
  findings and the agent tools need no per-view logic. What that means in practice, with the default `app`:
  - `/dev/graph` opens on an **app map** (new first preset): routes, handlers, services, stores, tables, templates,
    config keys, commands and their schedules, and the docs that cover them. The existing presets (architecture,
    request flow, database, docs against code, config usage, findings) are the same data, scoped.
  - A route's flow shows the app's middleware, not RequestID, RealIP, Recoverer or the logger. `/dev/*` and `/_govibe/*`
    routes are not listed. No `wiring` or `dev` layer appears in the architecture view.
  - An inspector shows "uses framework: web, config" as a line of text, not as nodes.
  - The config report lists the app's keys; the framework's settings are a link to `GoVibe: Configuration`.
  - The toolbar has a scope switch ("My app" / "Framework" / "All"), the URL carries `scope=`, the choice is remembered
    per browser, and the banner always says which scope is showing and how many nodes it hides.
  - Every `graph_*` tool takes `scope` (default `app`); `graph_query` stays raw.
  Because the framework's code is not in the app's tree, most of this is free; scope exists for the nodes that come
  from the running router and the framework's manifest (its routes, middleware, config keys and MCP tools).
- **Calls layer** type-checks the app with `-tags govibe_dev`, so tagged app files are seen, and records calls
  into the framework as edges to external leaves (bounded, no framework internals).
- **MCP.** `graph_schema` reports app name and module, framework module and version, the scope, and which layers are
  built. New resources: `govibe://skill` (the rules text), `govibe://app/manifest` (what `App` contributes) and
  `govibe://docs/<slug>` (the framework docs). The server title comes from `App.Name`.

| App layer | May import from the framework |
| --- | --- |
| business (actions, services) | nothing |
| storage | nothing (stores take a `*sqlx.DB` by constructor) |
| controllers, middleware, views | `govibe/web` |
| commands | `govibe` (the `RunFunc` and `Runtime` types only) |
| config (support) | `govibe/config` |
| wiring (`main.go`, `routes/`) | `govibe`, `govibe/config`, `govibe/web` |
| tests | `govibetest` |

`commands` follow the controllers' layering rule: only business may call storage.

### Findings: how they work, and what changes for apps
**How they are built today** (in `business/services/codegraph`, moving to `internal/codegraph`):
1. The `findings` layer is built **last**. It reads the other layers back from the store through the same `Model` the
   agent tools use, so a rule sees exactly the graph a person or an agent sees. It has no inputs of its own and goes
   stale when any layer does.
2. Each rule is a small function over that model. There are twelve: `layering`, `docs.stale_ref`, `docs.spec_missing`,
   `docs.route_undocumented`, `config.undeclared`, `config.unused`, `code.unreferenced`, `db.no_primary_key`,
   `db.fk_unindexed`, `db.missing_fk`, `db.drift`, `db.unapplied`. A rule names the layers it needs; if one is not built
   the rule is stored as **not checked, with the reason**, so "no finding" is never mistaken for "not looked".
3. A finding is a node with a stable ID (`finding:<rule>:<subject>#<detail>`), a severity, a message that says what to
   do, and an `about` edge to the thing it concerns, so the inspector shows "Findings about it" on any node.
4. The tab is `GET /dev/graph/data/findings` (severity and rule filters) drawn by `graph.js`; the banner shows the
   count; `graph_findings` returns the same to agents.
5. The **actual** findings are what the gate counts: `govibe graph index --fail-on <severity> --baseline <file>` fails
   only on a finding whose ID the baseline does not list, so accepted debt is visible and new debt is not.

**What changes**
- **Scope.** An app's findings are about the app's code. Framework code is never indexed in an app, so the framework's
  own findings cannot appear there, and the findings tab, banner and `graph_findings` follow the scope switch.
- **Accepting a finding needs a reason.** Baseline entries gain `reason` (required) and `date`;
  `--update-baseline` takes `--reason`, and `govibe check` fails on an entry without one. "Not addressed" becomes
  "fixed" or "accepted, and here is why".
- **Rules are configurable** in `govibe.yml` (`rules: {code.unreferenced: off, config.unused: warning}`), so a team tunes the
  gate without editing the framework, and the framework's own repo turns off the rules that do not suit a library.
- **New rules:** `framework.import` (error) and `docs.command_undocumented` (info).
- **`code.unreferenced` counts references from `_test.go` files**, which removes a known class of false positives.

**The framework's own findings today, and what happens to each**
| Finding | Real? | Disposition |
| --- | --- | --- |
| `layering`: `controllers/devdb.go` imports `storage/dbexplorer` (error, baselined) | Real | Phase 2: the dev UI moves to `internal/devtools/devui` and declares the small `Explorer` interface it needs instead of importing the concrete type; the baseline entry is deleted |
| `config.unused` x5: `mail.smtp.*` | Real: dead config for a feature nobody asked for | Phase 1: the block, the two `SKILL.md` mentions and the `config.go` comment are removed; the fixture's mail keys are renamed to neutral ones |
| `code.unreferenced`: `graphcheck.Check` | False positive: only tests call it | Phase 1: the rule counts test references |
| `docs.stale_ref` x3: `docs/graph/indexes.bolt` in `GRAPH_SCHEMA.md` and `MEMORY_SCHEMA.md` | Environment-dependent: the file is git-ignored derived data, so a fresh clone (CI) reports it missing and a dev machine does not | Phase 1: the docs name the directory `docs/graph/` instead; Phase 3: the profile's `derived` list (default `docs/graph/*.bolt`) tells the rule not to judge such paths, for apps too |

The framework repository holds itself to the gate too, with its own `govibe.yml`: an empty baseline is a Phase 2 exit
criterion, and `examples/hello` is gated as an app.

### `SKILL.md` as an enforceable contract
| SKILL rule | Where it lives after the split | Enforced by |
| --- | --- | --- |
| Controllers hold no business logic | app code, thin by the shape of `web` helpers | `layering`: controllers cannot import storage |
| Commands hold no business logic | app code, thin by the shape of `RunFunc` | `layering`: commands cannot import storage |
| Only actions and services call storage | app code | `layering` (existing) |
| Business logic is clean | app code | `framework.import`: business imports nothing from the framework |
| Assets, migrations and config embedded; one binary | app `embed.go` files, `main.go`'s `//go:embed env.yml`, the `fs.FS` hand-over | `doctor`: a populated `assets/static` or `sql/` with a nil `App` field is reported |
| Config in `env.yml`, no ad-hoc env reads | app `env.yml` in the root; framework keys by manifest | `config.undeclared`, `config.unused` (existing) |
| templ, htmx vendored, no node | `go tool templ`; htmx served from `/_govibe/static` | build; `doctor` flags a CDN script tag |
| chi router with subroutes | `chi.Router` handed to `Routes` | the type |
| goose dialect explicit | `internal/migrate` | the runner cannot be called without a dialect |
| Add a CLI command | `routes/console.go` and `govibe make command` | the type; `docs.command_undocumented` |
| Spec-driven docs, five sections | scaffolded `docs/`, `govibe make spec` | docs layer; `doctor` flags a spec missing a section |
| Dev tooling never in production | build tag plus `!IsProd()` | CI job (A7), `doctor` |
| Air is the single dev tool | `go tool air` from the app's `go.mod` | the Makefile and `.air.toml` it generates |

The generated `SKILL.md` has a **managed block** (the rules above, the framework API, the CLI, commands and
scheduling, the dev tooling, rendered for this version and fenced by `govibe:managed` markers) and an **owned section** for
the app's domain and extra constraints. `govibe upgrade` rewrites only the block. The block has no demo text and no mail
example; the **owned** section is seeded once with the "Hello GoVibe done criteria" and the list of demo files to delete
when you start your own app, and from then on it is the app's to edit. The repository's current root `SKILL.md`
becomes the contributor guide for the framework itself; the app-facing text moves into the scaffold templates.

### Scaffolder, generators, managed files, upgrade
- **Engine:** templates embedded in the tool, so the scaffold version equals the framework version, it works
  offline, and it is testable. Template delimiters are custom, so templ `{ }` and Go template `{{ }}` in generated
  files need no escaping. Not a template repository and not `gonew` (which is, by its own docs, highly
  experimental and copies a module rather than depending on one).
- **`govibe new <module>`:** validates the path, refuses a non-empty directory, renders the tree, runs `go mod
  init`, `go get` of the framework at the tool's version, `go get -tool` for templ, air and the tool, `go mod tidy`,
  `templ generate`, `git init` and a first commit (opt out with `--no-git`), then `govibe check`. Flags: `--db none`
  (default) or `mariadb`, and `--replace <path>` (contributors: point at a framework checkout). There is no flag for
  the demo: every new app contains the Hello GoVibe demo as owned code. Its source of truth is `examples/hello`: a
  `go generate` step copies that app into the template set with the module path templated (`go:embed` cannot reach a
  nested module), and CI fails when the copy is stale, so the demo a user gets is the demo the framework runs and gates.
- **Managed vs owned.** Managed files (the `SKILL.md` block, `Makefile`, `.air.toml`, the workflow) carry a header and a
  hash in `govibe.yml`. `govibe upgrade` re-renders them and, when the app edited one, shows a diff and refuses unless
  `--force`. Everything else is owned, including `env.yml`, `README.md`, `docs/public/*` and every Go file; owned files
  are never touched.
- **Generators** (`govibe make page, resource, action, service, store, migration, middleware, command, config, spec`)
  write files that obey the layering and register at a marker comment in `routes/routes.go` or `routes/console.go`; if the
  marker is missing they print the snippet instead of guessing. `make command <name> --schedule "<spec>"` writes the command,
  its declaration and its schedule. A `resource` is the full vertical slice, which is what `testdata/fixture` already is,
  so the fixture doubles as the generator's golden.
- **Upgrade notes** ship embedded as `UPGRADING.md` entries per version and are printed by `govibe upgrade`.
- **Doctor** reports: tool version against the `go.mod` pin, managed-file drift, missing embeds, an `inspect --json` that
  fails with no environment, a dev package in the default build, an invalid schedule spec, a baseline entry without a
  reason, and the spec section check above.

### Versioning and compatibility
Semver. Before `v1.0.0` a minor version may break the public surface, with an `UPGRADING.md` note. The public
surface is four packages and one struct on purpose. CI runs an API-compatibility check (`gorelease`) against the
last tag once there is one. Go version in generated apps is the framework's (`tool` directives need 1.24+).

### Guardrails
- Verification happens in **scratch copies**: scaffold into `$TMPDIR`, point `--replace` at a scratch copy of the
  framework, run the graph with `--dir` on a scratch store. Never against the working tree's dev server, which
  holds the bolt lock and the real database.
- A scratch copy does not isolate the **environment**. In Phase 0, four scratch boots (two in S1, two in S9) ran from a shell that exported
  `MARIADB_DSN`, `CODE_REVIEW_DB_DSN` and `WORKOS_*` and connected to the real local database (migrations were a no-op and the
  explorer read-only, so nothing is believed to have been written). Boot every scratch app under `env -i` plus only the variables it needs, and give
  scratch runs that fake a published framework version their own `GOMODCACHE` (one run put a fake
  `github.com/petars-dev/govibe@v0.1.0` into the real module cache, where it would have shadowed the real tag). The keystone CI
  job and `govibetest` follow the same rule.
- A phase that moves files changes no logic in the same commit; the test suite and the golden files are the
  proof of a pure move.
- The framework never reads or writes outside the app root (the indexer's confinement rules stay).
- Nothing in `/_govibe/*` reveals configuration, schema or rows.
- The scheduler never runs a task it could not validate at boot, and never runs inside `schedule list`.
- No phase merges with the graph gate red or a baseline entry added to make it green.

## Where today's code goes
| Today | Destination | Visibility |
| --- | --- | --- |
| `main.go` | `examples/hello/main.go` and the scaffold template; the boot logic goes to `internal/kernel` | app / internal |
| `routes/routes.go`, the Hello routes | `examples/hello`, which is also the demo in every generated app | app |
| `routes/routes.go`, dev part and `/dev/docs` routes | `internal/devtools` `Mount` | internal |
| `config/env.yml`, framework keys | `config/defaults.yml` in the framework | public package, embedded |
| `config/env.yml`, `mail.smtp.*` | **deleted** (with the `SKILL.md` and `config.go` mentions) | n/a |
| `config/env.yml`, the rest | the app's `env.yml` in the project root (the scaffold starts nearly empty) | app |
| `config/config.go` | `config` (loader, public); the typed app struct is optional and generated | public / app |
| `logger/` | `internal/kernel` | internal |
| `middleware/logging.go`, `devassets.go` | `web` (`RequestLogger`, `DevNoCache`) | public |
| `storage/mariadb` | `internal/kernel` (opens the one pool) | internal |
| `storage/migrations` | runner to `internal/migrate`; the app keeps `embed.go` plus `sql/` | split |
| `storage/dbexplorer`, `storage/graphdb` | `internal/dbexplorer`, `internal/graphdb` | internal |
| `business/services/codegraph` (with `testdata/fixture`, goldens) | `internal/codegraph` | internal |
| `devmcp/` | `internal/devmcp` | internal |
| `controllers/dev*.go` plus the dev files in `assets/static`, cytoscape, logo | `internal/devtools/devui` with its own embed; the theme files and logo are used by both the dev UI and the Hello page, so each gets its own copy | internal |
| `assets/htmx` | embedded in `web`, served at `/_govibe/static/htmx.min.js` | public path |
| `controllers/{home,api,hello}.go`, `business/actions/hello.go`, `assets/templates/home.templ`, `home.css`, hero image | `examples/hello`, which is also the demo in every generated app (its `/dev` links wrapped in `web.IfDev`, its htmx tag from `web.HTMXScript`) | app |
| `cmd/govibe` | the tool; `serve` and `migrate` become runtime commands in `govibe.Main` | tool / public |
| `.github/workflows/graph.yml`, `Makefile`, `.air.toml` | scaffold templates (managed); the framework repo keeps its own CI | scaffold |
| `docs/public/GRAPH_SCHEMA.md`, `DEV_DB_EXPLORER.md` | stay here; embedded as `GoVibe:` docs | framework docs |
| `docs/public/ARCHITECTURE.md`, `MEMORY_SCHEMA.md` | `ARCHITECTURE.md` rewritten as the framework's and embedded; the app gets its own seeded copies | split |
| `docs/specs/SPEC_*.md` | stay here; **never embedded or scaffolded**; `SPEC_SAMPLE` dropped | framework repo |

## Phases & Tasks
Each phase ends with the test suite green, the graph gate green, and a run from a scratch copy. Each ends by updating
`docs/public/ARCHITECTURE.md` and this spec's status table.

0. **Phase 0 — Spikes and decisions** (no repository moves; scratch copies only)
   - S1 Tag exclusion: move the dev mount behind `govibe_dev`; confirm the default build's `go list -deps` has none
     of cayley, the MCP SDK, x/tools, goldmark, chroma, and the tagged build still serves the panel. Record package
     count and binary size against today's 440 packages.
   - S2 Cross-module routes: a scratch app requiring a scratch framework through `replace`; record what
     `runtime.FuncForPC` reports for handlers in another module with and without `-trimpath`; make them external nodes.
   - S3 Calls layer across modules: run `go/packages` on the scratch app with `-tags govibe_dev`; record time and
     edge shape into framework packages.
   - S4 `tool` directive: `go get -tool` for the CLI, templ and air in a scratch app; record `go.mod` growth and
     cold and warm `go tool govibe version` time. Fallback if cold start is too slow: a pinned `go install`.
   - S5 Embed hand-over: `env.yml` embedded from the root `main.go`, migrations and assets as `fs.FS`; a `main_test.go`
     that boots `newApp()`; the dev disk override for `./assets`; the framework defaults under the app's file with a
     placeholder.
   - S6 Offline facts: `inspect --json` on the scratch app with no environment and no database.
   - S7 Module rename: scripted rewrite of `petars-dev/govibe` to `github.com/petars-dev/govibe` in the 38 files, tests green.
   - S8 Scheduler: the `robfig/cron/v3` parser against `gocron` v2 for next-run offline, `CRON_TZ`, DST edges, descriptors and
     a fake clock; decide the engine.
   - S9 Scope: apply an origin filter in a scratch `Model` and confirm views, reports and tools need no other change.
   - Output: a "Spike results" table here (done, see "Spike results (Phase 0)"), with the amendments it requires.
   - **Status: done 2026-10-11, see "Spike results (Phase 0)".** Sign-off of the amendments is still open (below).
1. **Phase 1 — Invert inside the repo, and clean up** (behavior-preserving, except the removals; everything still at the root)
   - T1.1 `config`: instance-based, layered (framework defaults, app file, environment as today); drop the `cfgRaw`
     global; one load per boot.
   - T1.2 `internal/kernel`: one boot path with one database pool, migrations, graceful shutdown. `main.go` and
     `govibe serve` both call it (ends the divergence in constraint 3). The `App`, `Runtime` and `Command` types are
     defined here (commands as CLI only; scheduling comes in Phase 4).
   - T1.3 Split `routes.NewRouter` into base stack and assets, `routes.Register(rt, r)` for the app part, and
     `devtools.Mount(Deps)` for the rest, with `Deps` declared by `devtools`.
   - T1.4 Dev UI assets embedded in a framework-owned FS, served at `/_govibe/static/*`; the dev pages switch to their
     own copies of the theme and logo there, and the Hello page keeps its own files and takes only its htmx tag from the
     framework; `web.IfDev` wraps the Hello page's `/dev` links; reserved-prefix collision check with a clear boot error.
   - T1.5 **Remove what was never asked for:** the `mail.smtp` block in `config/env.yml`, the two `SKILL.md` mentions, the
     comment in `config/config.go`; the dead WorkOS client in `main.go` and its keys; the `mail.smtp` test keys in
     `testdata/fixture` are renamed to neutral ones and the goldens regenerated (reviewed as a diff).
   - T1.6 `code.unreferenced` counts references from `_test.go` files (clears the `graphcheck.Check` false positive); the docs
     that name `docs/graph/indexes.bolt` name `docs/graph/` instead (clears the fresh-clone warnings).
   - T1.7 Tests: boot, config layering, reserved prefixes, `inspect --json`.
   - Done when: every existing test passes, goldens are unchanged except the fixture's renamed mail keys, the panel and the
     Hello page look the same, the dev pages no longer read anything from `/assets/static`, and an index with an empty
     baseline reports only the layering finding, in a fresh clone as well as on a developer machine.
   - **Status: done 2026-10-11, see "Phase 1 results".**
2. **Phase 2 — Physical split** (mechanical moves, no logic edits in the same commit)
   - T2.1 Rename the module to `github.com/petars-dev/govibe`.
   - T2.2 `git mv` into `internal/*` per the table above; public packages `govibe`, `config`, `web`, `govibetest`.
   - T2.3 Move the Hello app to `examples/hello` (own `go.mod`, `replace ../..`, a root `go.work`) on top of
     `govibe.Main`; the root `make dev` runs it.
   - T2.4 `cmd/govibe` becomes the tool; `serve`, `migrate`, `routes`, `version` become runtime commands in `govibe.Main`.
   - T2.5 Build-tag exclusion (from S1) and a CI job that fails when the default build links a dev package (A7).
   - T2.6 The dev UI declares its `Explorer` interface; the `layering` finding disappears with the move; delete
     `docs/graph/baseline.json` entries; add a `govibe.yml` for the framework itself.
   - T2.7 An architecture test: `internal/devtools` does not import `internal/kernel`, public packages do not import
     `internal/devtools` or `internal/codegraph`, nothing imports `examples/`.
   - T2.8 Rewrite `README.md` and `docs/public/ARCHITECTURE.md` from the framework's point of view; root `SKILL.md`
     becomes the contributor guide; drop `SPEC_SAMPLE`.
   - Done when: `examples/hello` meets the Hello GoVibe done criteria, its default-tag binary has no dev packages, the
     framework's own tests and goldens are identical, and the framework's gate passes with an empty baseline.
   - **Status: done 2026-10-12, see "Phase 2 results".**
3. **Phase 3 — Profile, the app-only graph, findings and the docs surface**
   - T3.1 `Profile` replaces the constants (config file defaults to `env.yml`); `govibe.yml` loader with layout, rule and
     `derived` path overrides; a second fixture with a non-default layout.
   - T3.2 `origin` on nodes; external nodes for symbols outside the root; routes and middleware classified by handler location.
   - T3.3 **Scope in the `Model`**: `app` default; the switch in the toolbar and `scope=` on `/dev/graph/data/*`, the reports and
     every `graph_*` tool; the app map preset; the banner states the scope and what it hides.
   - T3.4 Framework manifest (config keys, routes, middleware, MCP tools, public API symbols) so scope `all` can label them and
     the config and docs rules do not misjudge the framework.
   - T3.5 `internal/rules` as data; the `framework.import` rule; the `commands` layer in the profile; baseline entries require a
     `reason`, `--update-baseline --reason`, and `govibe check` fails on an entry without one.
   - T3.6 Export the route source option; `govibe graph index` runs the app's `inspect --json`; `docs.route_undocumented` becomes
     gateable.
   - T3.7 Calls layer: tag-aware load, bounded edges into the framework.
   - T3.8 MCP: `graph_schema` additions, `scope` parameter, `govibe://skill`, `govibe://app/manifest`, title from `App.Name`.
   - T3.9 **Docs surface:** `/dev/docs` opens on the app's docs; the `GoVibe:` group under `/dev/docs/govibe/*` from the embedded
     allowlist; MCP resources for them; the no-`SPEC_*` test; `GOVIBE_FRAMEWORK_SRC` contributor mode.
   - T3.10 The `/dev` index page.
   - T3.11 Rebuild `testdata/fixture` as an app that depends on a stub framework module, with neutral key names; regenerate
     goldens and review the diff.
   - Done when: indexing `examples/hello` reports zero findings, A3, A8 and A10 pass, and the golden tests are green.
   - **Status: done 2026-10-13, see "Phase 3 results".**
4. **Phase 4 — Commands and the scheduler**
   - T4.1 `internal/scheduler`: spec parsing, runner, clock, overlap, timeout, recover, the optional DB lock; unit tests on a fake clock
     (due times, DST, overlap, timeout, panic, shutdown).
   - T4.2 `Command.Schedule`, `Cron`, `Every`; kernel builds the command table, validates specs at boot, starts and stops the scheduler;
     config keys `scheduler.enabled`, `scheduler.timezone`, `scheduler.shutdown_timeout`, `scheduler.lock`; `serve --no-schedule`.
   - T4.3 Runtime commands `schedule list` (`--json`, `--next N`) and `schedule run`; the structured log lines.
   - T4.4 `inspect --json` carries commands and schedules; the `commands` graph layer, the commands report, reach from a table to
     scheduled commands in `graph_context` and `graph_impact`, `docs.command_undocumented`.
   - T4.5 `/dev/schedule` (list, next run, last 20 runs, "run now") and the `schedule_status` MCP tool.
   - T4.6 `govibetest`: a fake clock and `AdvanceTo` so an app can test its own scheduled commands.
   - T4.7 `COMMANDS_AND_SCHEDULER.md` for the embedded framework docs.
   - Done when: A9 passes in CI.
   - **Status: done 2026-10-15, see "Phase 4 results".**
5. **Phase 5 — Scaffolder**
   - T5.1 Embedded template set (custom delimiters); `--db` variants; the Hello demo synced from `examples/hello` by
     `go generate` with the module path templated, and a CI check that fails on a stale copy.
   - T5.2 `govibe new` with the steps above; `--replace` for contributors.
   - T5.3 App `SKILL.md` (managed block plus an owned section seeded with the demo's criteria and its deletion list), `govibe.yml`,
     seeded `ARCHITECTURE.md` (it names `GET /`, `GET /api/hello` and `GET /hello`, so a new app has no `docs.route_undocumented`
     finding) and `MEMORY_SCHEMA.md`, `README.md`, workflow, Makefile, `.air.toml`, `.gitignore`.
   - T5.4 `govibe check` (gate plus doctor).
   - T5.5 `govibetest` and the scaffold's `main_test.go`.
   - T5.6 **The keystone CI job**: scaffold from the working tree into a temp dir; add the overlay (a test scheduled command);
     `go build ./...` and `go build -tags govibe_dev ./...`; `go test ./...`; `govibe check` must exit 0 with an empty
     baseline; boot with the tag and request `/` and `/api/hello` (the demo), `/dev`, `/dev/graph/data/status`, and
     `graph_schema` over `/dev/mcp`; run `schedule list`; boot without the tag and expect 404 on `/dev/graph` and no `/dev`
     link in the `/` page; assert the default build has no dev packages and the tree has no `SPEC_*`.
   - Done when: A1, A2, A4, A6 and A7 pass in CI.
   - **Status: done 2026-10-17, see "Phase 5 results".**
6. **Phase 6 — Generators and upgrade**
   - T6.1 `govibe make` generators (including `command --schedule`) with marker insertion; one golden test each, and the gate must
     pass on the output.
   - T6.2 `govibe upgrade` (managed re-render, diff, refuse on edits, upgrade notes); standalone `govibe doctor`.
   - Done when: A5 passes, and generating a `resource` and a scheduled `command` in a scaffolded app leaves the gate green.
   - **Status: done 2026-10-17, see "Phase 6 results".**
7. **Phase 7 — Hardening, open-source readiness, options**
   - T7.1 Version shim: the global `govibe` delegates to the version pinned in the app's `go.mod`.
   - T7.2 Evaluate Option D (supervisor-hosted panel) with a spike: failing-build behavior, proxy cost, bolt lock. Output is a
     decision record, not code.
   - T7.3 `App.DevTools`: app-contributed MCP tools and dev pages, in files tagged `govibe_dev`, indexed by the existing `mcp` layer.
   - T7.4 Open source: rewritten README, CONTRIBUTING, SECURITY, issue and PR templates, the versioning policy, documentation for
     the four public packages on pkg.go.dev, a scan of the history and docs for private references (none found in the current tree).
   - T7.5 Tag `v0.1.0` and publish.

## Spike results (Phase 0)
All nine spikes ran in scratch copies of this repository (2026-10-11); the working tree, its dev server and its database were
not used on purpose (one slip, recorded under Guardrails). The notes, the rename script, patches and reference prototypes are
kept under `docs/spikes/` (its README maps each spike to its files); none of it is built or shipped, and raw outputs and caches
are not kept. Every spike supports the chosen architecture (Option C); the amendments below change details, not the shape.

| Spike | Result | Verdict |
| --- | --- | --- |
| S1 Tag exclusion | Default build `go list -deps .`: **440 to 240 packages**, binary **30.0 MB to 8.9 MB**; none of cayley, MCP SDK, x/tools, goldmark, chroma linked. The tagged build is unchanged (440 packages) and serves `/dev/docs`, `/dev/graph`, `/dev/mcp`. The default binary answers 404 on `/dev/*` under `APP_ENV=dev` and `prod` and serves the Hello routes. Nine files had to be tagged because `controllers/dev*.go` share a package with the Hello controllers. About 490 KB of dev JS and CSS (graph.js, cytoscape) stays embedded and served from `/assets/static` until T1.4 moves it | Holds, better than expected |
| S2 Cross-module routes | A hostless module path works with `require` plus `replace`. `FuncForPC` resolves handlers in another module in all four variants (replace, replace with `-trimpath`, module cache, module cache with `-trimpath`); after the changes below all four give identical results: 17 routes, 0 unresolved. `Name()` is **wrong when the compiler inlines a factory closure** (`web.Health(...)` reports an app symbol and a framework file), so symbol prefixes cannot decide origin | Holds, with changes (AM7, AM8) |
| S3 Calls layer across modules | `go/packages` on a 9-package app: **8.5 s cold** by default, **13.0 s cold** with `-tags govibe_dev`, about **0.2 s warm**. Calls into the framework resolve; 32 of 45 call sites leave the app (9 are stdlib). Module info is empty for packages the app does not import directly (needs a `packages.Visit` index); a method on an aliased type reports `internal/kernel` and would leak internals into edges | Holds, with changes (AM6) |
| S4 `tool` directive | `go get -tool` works with `replace` and from inside the app. Cold build, fresh cache per tool: govibe **41.7 s**, templ 23 s, air 21 s; **about 48 s for all three** on a fully cold machine (13.9 s of it downloads); **0.3 s** steady, 7 s after editing the replaced source. A 2-core CI runner was estimated at 30 to 60 s, not measured. `go.mod` 9 to 98 lines, `go.sum` 4 to 664, **82 indirect modules**; the default build stays at 70 packages. `go get -tool templ@latest` **bumped the app's `go` line from 1.25.0 to 1.26.0**; latest air needs 1.26 too; templ v0.3.1070 fails on go1.25.14. Pinning templ v0.3.1020 and air v1.67.0 keeps 1.25.0. `go.sum` is incomplete until `go mod tidy`. air pulls in Hugo and about 15 other modules. 17 shared dependencies resolve newer than the framework's own `go.mod`; codegraph (goldens included) and controllers tests pass at those versions | Holds; **decision needed** (AM1) |
| S5 Embed hand-over | Works as drawn: `env.yml` embedded from the root `main.go`, migrations and assets as `fs.FS`, defaults beneath the app's file, one boot path for `Main`, `serve` and tests, `main_test.go` booting `newApp()`. goose runs over the handed-over `fs.FS` through its Provider API with no connection. Found: the dev `./assets` override depends on the working directory (from `/tmp` it silently serves the embedded copy); `${V:1.10}` becomes `1.1` and `00123` becomes `123`, even when quoted | Holds, with changes (AM11) |
| S6 Offline facts | `env -i ./shop inspect --json`: median **13 ms**, no database connection (a bogus DSN was set), no dev mount, no indexer. Fed to the indexer's `routeSource` it makes `docs.route_undocumented` run in a CLI index. A root-level `env.yml` makes the config layer report `ok` with 0 quads, so `config.*` rules would pass vacuously until the Profile (T3.1) lands | Holds, with changes (AM7, AM8, AM10) |
| S7 Module rename | **38 files** (37 `.go` plus `go.mod`; 39 with this spec, which the script skips). Build, vet and tests pass, the 9 golden files are byte-identical, only `gofmt` re-sorting and re-alignment appear beyond the substitution (6 files). The script is idempotent and leaves existing `github.com/petars-dev/govibe` references alone. `testdata/fixture` is its own module and does not change | Holds, no spec change (AM13) |
| S8 Scheduler engine | gocron v2.22.0 parses with `robfig/cron/v3`, so the grammar and its gaps are identical. gocron de-duplicates a repeated wall-clock hour on every spec with no off switch: an hourly `30 * * * *` fired **4 times in 5 real hours** across New York's fall-back (own runner: 5). gocron has no per-job timeout, and `Job.NextRun()` is zero before `Start`. Own runner over the parser: **183 lines plus a 57-line fake clock**, 9 tests pass under `-race` (10 repeats), +0.61 MB and 1 module against +1.06 MB and 4 modules. Parser traps: a bare `CRON_TZ=Zone` panics; `@every 0s`, negative and sub-second values are silently accepted; `0 0 31 2 *` parses but never fires; a job in a spring-forward gap is skipped that day; a fixed-time job in a fall-back repeat fires twice in raw robfig | **D14 holds**, with a validation wrapper (AM12) |
| S9 Scope | Filtering by origin in the `Model` makes every `/dev/graph/data/*` endpoint, all four reports, 7 of 8 `graph_*` tools and the CLI gate scope-correct with no edits. App scope is **387 of 11,073 quads (3.5%)**, no dangling edges in any scope, filter cost about 0.4 ms on a 44 ms load; "n hidden" comes from the same pass. **Fails for findings:** run on an app-filtered model the real layering error vanished, a false `docs.route_undocumented` appeared, and the framework-filtered run added 5 false `config.unused`. Raw paths bypass the `Model`: `graph_query`, the Cayley HTTP API (accepts writes; not mentioned in this spec) and `ManualView` | **Partly holds**: the one-place claim is true for views, reports and tools, false for findings (AM9) |

### Amendments the spikes require (for sign-off)
Not yet folded into the design text above; each names the task it changes. Sign-off means accepting these (or flipping them) and
deciding AM1.

| # | Amendment | Changes |
| --- | --- | --- |
| AM1 | **Decision: Go version.** Pin tool versions in the scaffold (`go get -tool` at templ v0.3.1020, air v1.67.0, the tool at the framework's own version) so the app keeps `go 1.25.0`, **or** move the framework to `go 1.26.0` and say so. Not `@latest`: it silently raises the app's `go` line. Always set the `go` line explicitly (`go mod init` writes the local toolchain) | T5.2, D7, D8, Versioning |
| AM2 | Run `go mod tidy` after every `go get -tool` (go.sum is incomplete otherwise). The keystone job builds the generated app with the versions the app resolves, not the framework's `go.mod` | T5.2, T5.6 |
| AM3 | D8 is heavier than assumed: air adds Hugo and about 15 modules to every app's `go.mod`. Keep it pinned as a tool for v1, or install air from the Makefile instead; decide at Phase 5 | D8, Risks |
| AM4 | Acceptance scenario A7 also asserts that no dev asset ships: `/assets/static/graph.js` is 404 and the app's `assets.FS` holds no dev file. Package counting alone passes while 490 KB of dev JS and CSS still ships. The dev UI's embed must be reachable only through the tagged import | A7, T1.4, T2.5 |
| AM5 | Tag only the kernel's import file (the one that calls `devtools.Mount`), never the dev packages: tagged files inside a shared package broke default `go test ./...` for codegraph and hid code from the calls layer | T2.5, Design: dev tooling |
| AM6 | T3.7 is required, not optional: the calls layer loads with `-tags govibe_dev` (the tags come from `inspect --json` `build.tags`, default `govibe_dev`) and states them in its layer note. Edge bound: stop at the first package outside the app module; one function-level leaf per public framework package (`govibe`, `config`, `web`, `govibetest`) with module and version; anything in the framework's `internal/` collapses to a single module-level leaf flagged `internal`; third-party packages are one leaf per package; stdlib is not recorded. Build the module index from `packages.Visit` | T3.7 |
| AM7 | Classify routes by **who registered them** (snapshot the route table before `App.Routes` runs, record `mounted_by`) plus the handler's file mapped through a module table, never by symbol prefix. `/assets/*` is a framework route with a stdlib handler; `/files/*` can be an app route with a stdlib handler. `inspect --json` carries `root_hint`, `build.modules` (from `ReadBuildInfo`, with replace directories), `build.trimpath`, `build.tags`, `mounted_by`, and no symbol-derived origin. The indexer's app root must come from that hint, not from where the indexer was compiled. The snapshot also gives the reserved-prefix check | T1.3, T3.2, T3.6 |
| AM8 | New light package `internal/routeinfo` (chi walk and `FuncForPC`, chi and reflect only): the app binary needs it for `inspect --json` and must not link cayley. Add it to the module layout; `kernel` and `codegraph` both use it. Export the route-source option with `BuildRoot`, `Modules` and `FrameworkModule` filled from the document | Module layout, T3.6 |
| AM9 | **Scope.** `Load(ctx, qs, scope)` takes scope as a parameter, never a global. Findings are computed once on `all`; a finding's origin is its `about` target's; the gate and the baseline always use `all`. A cross-origin finding is an app finding whose message and `path:line` carry the evidence, because the framework target is hidden. Layers write `origin` as a quad for leaf kinds so raw queries can filter. The `Model` keeps a hidden index (id to origin, counts by kind) for "n hidden" and "exists in another scope" errors. Replace the four "empty means layer not built" inferences with an explicit layer status; show layer and build counters only for `all`; build `graph_schema` examples from the scoped model; retire the seven `dev_only` sites in favour of origin. `graph_query`, the Cayley HTTP API and `ManualView` are deliberately unscoped and say so; `/dev/docs` needs its own switch (T3.9) | T3.3, T3.5, T3.8 |
| AM10 | `docs.route_undocumented` skips framework-origin routes (`/_govibe/*`, `/assets/*`), or acceptance scenario A1 ("no finding at any severity") cannot hold. Profile statuses distinguish "not configured or not found" from "empty" | T3.1, T3.5 |
| AM11 | Config and boot: a quoted `${V:1.10}` stays a string (a test in T1.1; today it becomes `1.1`). `internal/migrate` uses goose's Provider API with the dialect as a required type, not the global `SetBaseFS` and `SetDialect`. The dev assets directory is resolved against the app root, not the working directory, and the resolved path is logged. `govibetest` disables the database by default and says so | T1.1, T1.2, T1.4, T4.6 |
| AM12 | Scheduler: wrap the parser (recover its panic on a bare `CRON_TZ=Zone`; reject a spec that never fires; set a minimum for `@every`, since 0 s, negative and sub-second values silently become 1 s). `schedule list` prints "6h after start" for `@every`, not a date. DST: a run in a spring-forward gap is skipped and not caught up; in a fall-back repeat a spec with a fixed hour and minute fires once and a wildcard hour follows real hours. Document dom/dow OR (stepped fields included), Sunday as 0, no `L`, and that `CRON_TZ=` beats `scheduler.timezone`. gocron's latest is v2.22.0 (2026-07-09); robfig's last release is 2020-01-04. The runner prototype (`docs/spikes/_prototypes/s8-scheduler-runner`) is a starting point for T4.1 | Scheduler design, T4.1, T4.3 |
| AM13 | Rename (T2.1): the count is 38 excluding this spec. Do it as its own commit and **before** the `git mv` moves, so the byte-identical proof applies to each step. Delete `docs/graph/*.bolt` first. Fix the two files that were already gofmt-unclean (`cmd/govibe/cmd_generate.go`, `root.go`) in a separate earlier commit so the rename diff stays pure. The script is `docs/spikes/scripts/s7-rename.sh` | T2.1 |
| AM14 | Nothing warns when a global `govibe` differs from the version pinned in the app's `go.mod`; `doctor` and the T7.1 shim must do it | T6.2, T7.1 |

**Where the amendments stand after Phase 3.** Applied: AM4, AM5 (Phase 2), AM6 (the calls layer is tag-aware and bounded; the module index comes from the module table, not
`packages.Visit`), AM7 (routes are classified by who registered them; `inspect --json` carries `mounted_by`, the module table, `build.tags` and `build.trimpath`; the indexer's root comes from
the hint), AM8 (`internal/routeinfo`, and `codegraph.OptionsFromInspect` exports the route source with the modules and the framework), AM9 (scope is a parameter of `Load`, the gate and baseline read
`all`, findings are computed once on `all` with the origin of the node they are about, `Hidden` accounts for what a scope drops; the one part not applied is retiring `dev_only`, see "Phase 3 results"),
AM10 (framework-origin routes are skipped by `docs.route_undocumented`; a profile with no settings file makes the `config` layer `skipped`), AM11, AM13. Still open: AM1 to AM3 (Go version and tool pins,
Phase 5) and AM12 (scheduler, Phase 4), AM14 (version skew, Phases 6 and 7), and the owner's sign-off of all fourteen. (After Phase 4: AM12 is applied, see "Phase 4 results".
After Phase 5: AM1 to AM3 are applied as defaults, see "Phase 5 results"; only AM14 and the sign-off remain.)

## Phase 1 results (inside the repo, behavior-preserving)
Done 2026-10-11. Everything is still at the repository root and one module; the framework's public packages (`govibe`,
`web`, `govibetest`) are Phase 2, so the `App`, `Runtime` and `Command` types live in `internal/kernel` for now and `main.go`
calls `kernel.Main`. Verification ran in scratch copies under `env -i`: the full suite (all packages green, goldens as
described below), the CI gate command with `--calls` (exit 0, one finding, accepted by the baseline), and the old (HEAD) and
new binaries side by side.

| Task | Result |
| --- | --- |
| T1.1 `config` | `config.Load(fs.FS)` returns a `Config` value: `defaults.yml` (embedded in the package, framework keys only) under the app's `env.yml`, resolved from the environment as before; the `cfgRaw` global is gone and `Get`, `String` and `Unmarshal` are methods. Maps merge deeply, lists are replaced, unknown keys pass. A quoted placeholder stays a string (AM11). New key `root` (`GOVIBE_ROOT`). The app's `env.yml` is now `env.yml` in the project root, embedded by `main.go`; it holds no settings (the demo needs none) |
| T1.2 `internal/kernel` | One boot path (`Build`) for `main`, `serve`, the read-only commands and tests: config once, logging, one database pool with migrations, router, command table, optional dev mount. `Run` handles `serve` (default, graceful shutdown on SIGINT/SIGTERM, then `App.Shutdown`, then the pool), `migrate up\|down\|status`, `inspect --json`, `help` and the app's commands (with `Flags`). `internal/migrate` uses goose's Provider API with the dialect a required, parsed type (AM11). `govibe serve` and `govibe migrate` delegate to `go run .` in the project root, which ends the two-boot-paths divergence (an app command opens the database but does not migrate it; `serve` does) |
| T1.3 router split | `routes.NewRouter` is gone: `routes.Register(rt, r)` is the demo's three routes; the kernel builds the base stack and the asset mounts; `devtools.Mount(Deps)` (in `internal/devtools`) holds the rest, shares the app's one pool with the explorer, and returns a stop function the kernel calls on shutdown (the indexer stops, the bolt store closes). The route table is snapshotted before `App.Routes` runs (`Built.FrameworkRoutes`, AM7) and the reserved-prefix check uses it. `internal/routeinfo` is the light chi walk, shared with the indexer's routes layer (AM8; a pure extraction: the layer keeps its names as aliases) |
| T1.4 dev assets | The panel's files (theme, docs and graph styles, graph.js, cytoscape, logo) are embedded in `internal/devtools/devui` and served at `/_govibe/static`; the dev pages read nothing from `/assets` (a test asserts it); the Hello page keeps its own `theme.*`, `home.css` and images and takes htmx from `web.HTMXScript()`; `web.IfDev` wraps its `/dev` links (the kernel marks the request context only when it mounts the tools). `/assets` no longer serves htmx or any dev file, and the dev assets directory is resolved against the application root, with the resolved path logged (AM11) |
| T1.5 removals | The `mail.smtp` block and the `workos` keys, the dead WorkOS client, the two `SKILL.md` mentions and the `config.go` comment are gone (`go mod tidy` drops workos and two modules it alone needed); the fixture's mail keys became `queue.broker.*` and its goldens were regenerated |
| T1.6 findings | `code.unreferenced` counts references from `_test.go` files (qualified references from any test package, bare names from in-package tests), and a test edit makes the code layer stale; the docs name `docs/graph/` instead of the git-ignored bolt file |
| T1.7 tests | `config` (layering, typing, independence), `internal/kernel` (boot, zero app, assets from disk against the root, reserved prefixes, command checks, dev marker and mount, `inspect --json` with a bogus DSN, commands with flags, `migrate` errors, serve and graceful stop, port in use), `internal/migrate`, `web`, the tool's delegation, and the indexer's new behaviour |

**Done-when, checked.** Existing tests pass; the goldens differ from HEAD in exactly the renamed fixture keys (with the
environment nodes re-sorted) and the two lines that described `code.unreferenced` as "tests are not indexed"; the Hello page's HTML in
dev is byte-identical to HEAD's except the htmx script line and its comment, and the htmx button works in a browser; the dev
pages differ only in their static paths; the dev pages read nothing from `/assets/static`; an index with an empty baseline
reports only the layering finding, on a developer machine (live router: 7 findings before, 1 now) and in a fresh copy with no bolt file.

**Deviations and things to know**
- **`/dev/docs` is no longer served in production.** `routes.NewRouter` mounted it unconditionally, outside the `!IsProd()` guard that covered the rest; it now goes through `devtools.Mount` like the other dev pages (this is what A7 asks for).
- **The dev stack is still linked into the default binary** (untagged: `devCompiled` in `internal/kernel/devmount.go` is the one seam Phase 2 turns into the `govibe_dev` build tag; `go list -deps .` is 445 packages, up from 440, from the new packages minus workos).
- **The indexer learned three things the spec put in Phase 3, because Phase 1 would otherwise have broken its gate:** the config layer reads `config/defaults.yml`, a legacy `config/env.yml` and the root `env.yml` (the Profile replaces this list); `internal/kernel` and `internal/devtools` are classified `wiring` and `dev`, other `internal/*` `support`; and `rt.Config.Get` / `cfg.Unmarshal` count as config reads, recognised by receiver name (`cfg`, `conf`, or a field `Config`).
- **Finding and report messages still name `config/env.yml`** (they are goldens); the Profile (T3.1) will make them name the configured file.
- **No `govibe.yml`, no `routes/console.go`, no `web.RequestLogger`/`DevNoCache` yet:** the kernel imports `middleware` as it is; moving it into `web` is Phase 2's `git mv`.
- **The kernel's dev test cannot run under `-race`:** the bolt backend of the graph store fails Go's `checkptr` (an upstream issue of `github.com/boltdb/bolt`); the rest of the kernel tests pass with `-race`.
- **Not run here:** `TestUpStatusDownAgainstAServer` (`internal/migrate`) needs `GOVIBE_TEST_MARIADB_DSN`; it creates and drops a schema of its own, like the indexer's database tests. The Provider-API migration path was exercised without a database only (listing, dialect and FS errors).
- **`make templ` / `templ generate` with no path also regenerates the fixture's `.templ` files under `testdata/`** (it overwrote the committed stand-in output): generate one file at a time (`templ generate -f assets/templates/home.templ`) or restore the fixture afterwards. Not changed here.
- `assets.FS` still embeds `templates/**`, so `.templ` sources are served under `/assets/templates/` (as before); worth dropping when the scaffold is written.
- **An independent review of the Phase 1 diff** found nine things; seven were fixed and tested: `govibe serve` now runs `go run .` in its own process group and signals the group (`go run` exits on SIGTERM without telling the server it started, which would have left the server running); the docs pages read `docs/public` and `docs/specs` from the application root (`controllers.DocsRoot`, set by `devtools.Mount`) and no longer from the working directory; a connection that will not finish no longer fails a stop (after the graceful window the server closes it); `govibe graph index` in a project without a root `env.yml` still reads `MARIADB_DSN` from the environment; test files have a cap of their own and cannot fail the code layer; the test-reference scan ignores composite-literal keys and declared field names; `migrate down` with nothing applied says so (it was an error before as well). Two were left as designed: the dev panel keeps its own copies of `theme.css`, `theme.js` and the logo (D20: the demo and the panel must not share a file), and an unquoted placeholder is still typed as YAML would type it ("exactly as today"; quote it to keep a string, which the generated `env.yml` comments say).
- Phase 0's amendments AM1 to AM14 are still unsigned: Phase 1 used the parts it needed (AM4, AM7, AM8 and AM11 for the kernel, the route snapshot, `routeinfo`, goose's Provider API and the root-resolved assets directory) and nothing that touches Go versions or tool pins (AM1 to AM3 belong to Phase 5).

## Phase 2 results (physical split)
Done 2026-10-12, on top of the committed Phase 1. The framework is the module `github.com/petars-dev/govibe` with the public
packages `govibe`, `config`, `web` and `govibetest`; everything else is `internal/`; the Hello GoVibe demo is its own module in
`examples/hello`, running on `govibe.Main`; a default build links no dev tooling. Every step was verified in a scratch copy under
`env -i`: build, vet, the full suite twice (default, and `-tags govibe_dev`), both gates, and the apps run for real (below).

| Task | Result |
| --- | --- |
| T2.1 rename | `petars-dev/govibe` is now `github.com/petars-dev/govibe` in 48 files (the spec's 38 plus Phase 1's new ones), as its own step before any move (AM13), after a separate gofmt fix of `cmd/govibe/cmd_generate.go` and `root.go`. The goldens did not change. `docs/spikes/scripts/s7-rename.sh` needed two fixes it now has: it only saw tracked files (Phase 1's new files would have kept the old path and its own check would not have noticed), and it rewrote the spike evidence under `docs/spikes` (patches, notes, even its own defaults), which records the old path on purpose |
| T2.2 moves | `business/services/codegraph`, `storage/dbexplorer`, `storage/graphdb`, `devmcp` to `internal/{codegraph,dbexplorer,graphdb,devmcp}`; `controllers/dev*.go` and their tests into `internal/devtools/devui` (which already held the panel's files); `middleware` into `web` (`RequestLogger`, `DevNoCache`); `logger` and `storage/mariadb` into `internal/kernel`. The public `govibe` package (`govibe.go`: `App`, `Runtime`, `RunFunc` and `Command` as aliases of the kernel's types, plus `Main` and `Run`) and a minimal `govibetest.Boot` (no database, no dev tools, closes with the test) are new. Fixture and golden files moved untouched |
| T2.3 `examples/hello` | The Hello app is a module of its own (`github.com/petars-dev/govibe/examples/hello`): `main.go` on `govibe.Main`, `env.yml`, `routes`, three controllers, the action, the templ page with its own static files, the embedded migration, its own `docs/` (it names the demo routes and the framework's), `README.md` and the app-facing `SKILL.md` (the rules the framework's old root `SKILL.md` held, updated; Phase 5 templates it). `main_test.go` boots it through `govibetest` and checks the done criteria. A root `go.work` joins the modules; `replace => ../..` in the example's `go.mod` coexists with it. `make dev` runs it; `.air.toml` rebuilds it when the framework changes too |
| T2.4 commands | `routes` (every route and who registered it) and `version` are runtime commands next to `serve`, `migrate`, `inspect` and `help`; the tool's `govibe serve` and `govibe migrate` run `go run -tags govibe_dev .` in the project root |
| T2.5 tag | `internal/kernel/devmount.go` declares the hook, `devmount_dev.go` (`govibe_dev`) sets it; the kernel names the dev tools nowhere else (AM5). **Reference app, default build: 241 packages, 8.7 MB; with the tag: 443 packages, 30.3 MB** (S1 predicted 240 and 8.9 MB). A dev build refuses `APP_ENV=prod` and says so; a default build says once that `/dev` is not compiled in. Tests: `TestDefaultBuildLinksNoDevTools` (in `examples/hello`), the kernel's tagged and untagged dev-mount tests, and a `ci` workflow (`test` both ways, `production` boots the default binary under `APP_ENV=dev` and finds nothing under `/dev`, `/_govibe/static/graph.js` or `/assets/static/graph.js`, and no `/dev` link on `/` (AM4), `devpanel`, below) |
| T2.6 findings | The dev UI declares `devui.Explorer` (the explorer satisfies it). The layering finding is gone, **through reclassification**: the handlers are in a `dev` package and the explorer is a `support` package, and `layerVerdict` allows every pair of framework layers (wiring, dev, support), so the empty baseline says nothing about the framework's own layering until the Profile (Phase 3) gives it rules of its own; `devui` still imports `internal/dbexplorer` for its types. `docs/graph/baseline.json` is empty, `govibe.yml` exists (comments only: the loader is T3.1; it already marks the project root). **Both gates report 0 findings of any severity** with an empty baseline, at `--fail-on info`: the framework, and `examples/hello` judged as an app (`--root examples/hello`). An offline run does not run `db.drift`, `db.unapplied` or `docs.route_undocumented` (no router, no database): of those only the `devpanel` CI job, which asks the booted app's live panel, exercises the last, until `inspect --json` feeds the CLI (T3.6) |
| T2.7 architecture test | `arch_test.go`, by `go list`: `internal/devtools` does not import the kernel; the public packages import no dev package, directly or in a default build; nothing imports `examples/`; the example imports only the four public packages (the compiler would allow its `internal/`, since the paths share a prefix). Checked by planting one violation of each in a scratch copy: all four are caught |
| T2.8 docs | `README.md`, `docs/public/ARCHITECTURE.md` and `docs/public/MEMORY_SCHEMA.md` are the framework's; the root `SKILL.md` is the contributor guide; `SPEC_SAMPLE.md` is gone; `GRAPH_SCHEMA.md` and `DEV_DB_EXPLORER.md` name the new paths |

**Verified by running it** (scratch copy, clean environment): the reference app with the tag, in a real browser (the htmx button returns "Hello GoVibe!", the
graph page draws the app's own architecture, no console errors), over MCP (`graph_context` on `GET /api/hello` resolves to `func:controllers.HelloAPI`,
`graph_impact` on the action lists both routes), under the tool's `govibe serve` (and its graceful stop), and under Air (`make air`); the default binary under
`APP_ENV=dev` (404 on every `/dev` path, no dev link, `routes` and `version` work); `make templ` leaves the committed templ output byte-identical; the CI
`devpanel` script run as written, from a clean graph store.

**Deviations and things to know**
- **A regression the split caused, found by running the panel, and fixed: handlers of an app that is a module of its own were "not application code".** The routes
  layer found where the binary was compiled from where the *indexer's own package* was compiled, which only works when the indexer and the app are one module. It
  now takes the hint the app supplies (`codegraph.Options.RootHint`, the app's `Routes` function as the runtime reports it, passed by the kernel through
  `devtools.Deps`), as AM7 prescribes; the framework's own tests index the framework, so no test saw it. A unit test covers the root math (absolute, `-trimpath`,
  module cache, root package, foreign symbols), an integration test shows a handler spelled as in another checkout resolving only with the hint, and the `devpanel`
  CI job asks a booted app. The rest of AM7 (the `inspect --json` fields, scope) stays in Phase 3.
- **Tag seam: one tagged file with an `init` hook, not a tagged file plus an untagged twin** (see Design): the indexer ignores build constraints, so a twin would
  have defined `mountDev` twice and broken the graph's invariants.
- **`go.work` also lists the indexer's fixture module.** A `go.work` above a nested module that is not a member breaks every `go` command run inside it
  ("directory prefix . does not contain modules listed in go.work"), which the calls layer does on the fixture. `go.work.sum` is committed.
- **The public types are aliases of the kernel's** (`type App = kernel.App`), so one definition serves the kernel and the app; pkg.go.dev shows an alias
  without its fields, so the fields are repeated in the doc comment of `govibe.App`. Revisit in T7.4.
- **Tests that indexed "this repository" as an app were retargeted at the framework.** The repository no longer contains the Hello app, so the live-router
  tests use real framework handlers (`GET /_govibe/static/htmx.min.js` is `web.ServeHTMX`; the dev pages) and assert framework facts: no layering violation, no
  migrations, no templates, `internal/*` package IDs. The app-shaped assertions (route to controller to action, templates, tables, the layering finding) were
  already covered on the fixture, which mirrors the Hello app and whose goldens did not change. The graph's `graph_schema` examples still name the Hello demo's
  route and IDs, which every app born from the scaffold has.
- **The indexer's `classifyPackage` learned one thing:** `internal/devmcp` is `dev`, as the top-level `devmcp` was (the documented table is updated). Nothing
  else about its layout assumptions changed: the Profile is Phase 3.
- **Two info findings the reference app showed in its own live panel** (`docs.route_undocumented` for the framework's `/_govibe/static/*` routes, which a doc of
  the framework used to mention) were fixed by documenting those routes in the app's `ARCHITECTURE.md` (an app does want to know which URLs the framework
  occupies) rather than by a rule change; AM10 (skip framework-origin routes) still belongs to Phase 3, and removes the need.
- **`make` changes:** `dev`, `run`, `build`, `air`, `graph-*` work on the example; new `test`, `test-dev`, `check` and `graph-check-app`; `cli-migrate`,
  `cli-serve` and the `tailwind` target are gone (the tool's `serve` and `migrate` run in an app directory, and there is no Tailwind input file left at the root). `make templ`
  runs inside `examples/hello` only, so it can no longer overwrite the indexer's fixture.
- **Not run here:** the database tests (`GOVIBE_TEST_MARIADB_DSN`), `-race` on the kernel's dev test (the upstream `checkptr` issue of Phase 1), the GitHub Actions
  workflows themselves (their scripts were run locally, but `ci.yml` has not been through GitHub), and `go install` of the tool from the module path (no tag is published).
- The Hello page links to `/dev/db` in a dev build even with no database configured (it answers 404): pre-existing, from Phase 1's page.
- **An independent review of the Phase 2 diff** found seven things; six were fixed and tested, one is noted: (1) the root hint did not work when `App.Routes` is written in package
  `main` (an inline closure in `newApp`, say), because the runtime names such a function `main.f` with no import path; the kernel now completes it from the binary's build
  information (`rootHint`, `qualifyMain`; `inspect --json` reports the same), checked by re-running the reviewer's mutation end to end; (2) the example's test and the CI `production` job could not
  catch a broken asset embed, because in a local environment the framework serves `./assets` from disk: the test now boots under `APP_ENV=prod`, the CI job serves from outside the repository and
  requests the app's own files (both shown to fail when the embed is removed); (3) `explain-change` had lost its "update GRAPH_SCHEMA.md and SPEC_GRAPH_INDEXER.md" hint and its ARCHITECTURE.md hint in the
  move (they matched the old paths, and an existing test used the old path too): restored, with a test; (4) `arch_test.go` passed with nothing checked when `go.work` was absent (`go list -e`
  returns a placeholder): it now fails loudly; (5) the retargeted e2e test had lost its one positive wire-level finding: restored on a small violating module; (6) smaller items: `version` shows a local
  `replace`, `make build`'s output is ignored, the README's "start your own app" list now builds (a `.gitkeep` keeps each embed pattern matching) and no longer suggests copying the example out of the
  repository, "three handlers" not "two controllers", and stale test-run comments. (7) `TestOpenGivesUpOnALockedStore` (`internal/graphdb`, untouched) failed once on temp-directory cleanup in a
  dev-tag run while other jobs were running, and passed 15 of 15 alone: an existing flake, not addressed here.
- **A mismatch Phase 2 did not cause and leaves for the owner:** `config/defaults.yml` ships `dev.graph.calls: ${GRAPH_CALLS:true}`, while `GRAPH_SCHEMA.md` and `SPEC_GRAPH_INDEXER.md` call the calls layer opt-in
  and off by default. `ARCHITECTURE.md` now states what the config does; decide which is meant.
- Amendments: AM4, AM5 and AM13 are applied as written (AM13 with the script fixes above), AM8 was Phase 1's, AM7's root hint is in; AM1 to AM3 (Go version and tool pins) and the
  sign-off of all fourteen are still open.

## Phase 3 results (profile, the app-only graph, findings and the docs surface)
Done 2026-10-13, on top of the committed Phase 2. The graph, the findings, the reports, the docs and the agent tools are about the application by default; the framework is one switch away and
labelled. Verified in tests (the whole suite, with and without the `govibe_dev` tag, the example module's own tests), by both CI gates at `--fail-on info` (the framework and `examples/hello`: 0 findings, empty
baselines), and by running the reference app in a scratch copy under `env -i` (A3 over MCP, A8 in a browser and over HTTP, A10 over HTTP).

| Task | Result |
| --- | --- |
| T3.1 profile | `internal/profile`: `Default` (a generated app's layout), `Load` (`govibe.yml` over it, unknown keys, absolute and escaping paths, a directory claimed by two layers, bad rule values are errors that name the key), `Classify` (longest directory match), `IsDerived`. The indexer reads settings files, migrations, docs, spec prefix, layer table, derived paths and rule settings from it; `govibe.yml` is an input of every layer's fingerprint; an invalid profile fails the run; a `config` layer with no settings file is `skipped` with the names it looked for. The framework's own `govibe.yml` keeps its layout (`internal/kernel` wiring, the dev packages `dev`, settings `config/defaults.yml`). Added the `framework` key (the spec's "framework path" of `govibe.yml`). Second fixture `testdata/fixture_layout` (non-default layout, two settings files, migrations and docs elsewhere, a prefix, a derived path, rule overrides) |
| T3.2 origin | `origin` quads on routes (who registered them: `Build.FrameworkRoutes` from the kernel's snapshot or `inspect --json` `mounted_by`, plus the reserved prefixes), middleware (the framework's when on the framework's own routes or in its module), settings, environment variables, MCP tools and externals. `external` nodes (kind, module, version, origin, `internal`) with `imports_external`, `calls_external` and `handled_by_external` edges |
| T3.3 scope | `Load(ctx, qs, scope...)`, `Scope`, `ParseScope`, `Model.Hidden`, `NodeRef.Origin`; the filter drops other-origin nodes, their edges, findings about them and annotations with an end gone, once, in the model. App map preset (first, the default view), `scope=` on every `/dev/graph/data/*` endpoint and report (400 for a bad one), the toolbar switch (URL `?scope=`, remembered per browser), the banner and the report strip say the scope and what it hides, framework nodes have a double outline and an origin badge, links carry the scope |
| T3.4 manifest | `devtools.FrameworkManifest`: the defaults file (`config.Defaults`), the typed `Config` fields by reflection (so `cfg.Server.Port` is a read of `server.port`) and the MCP tools (`devmcp.Tools`, read through an in-memory client). The config and mcp layers add them with origin `framework`; `config.unused` and `docs.route_undocumented` skip the framework's |
| T3.5 rules | `internal/rules` (layering table, framework-import table, spec sections, `Markdown()` for the rendered text); `framework.import` (error; packages and `.templ` files; where the import is written; not applicable in the framework's own repository); `commands` is an architecture layer with the controllers' rule; baseline entries have `reason` and `date`, `--update-baseline --reason`, an entry without a reason fails the gate; rules can be switched `off` or re-severitied in `govibe.yml` (an unknown rule name fails the findings layer) |
| T3.6 offline facts | `routeinfo.Inspect` (moved from the kernel, with what the `App` contributes), `codegraph.OptionsFromInspect`, exported `Options.RouteSource`; `govibe graph index` in an application runs its `inspect --json` (`--no-inspect` skips it) and the tool's manifest, so `docs.route_undocumented` runs in CI; `--dir` defaults to the profile's store directory |
| T3.7 calls | type-checked with `-tags govibe_dev` (or `build.tags`), stated in the layer's note; calls out of the module are bounded edges to external leaves (one per public framework function, one for `internal/`, one per library package, the standard library not at all) |
| T3.8 MCP | `scope` on every graph tool but `graph_query`/`graph_reindex`; `graph_schema` names the app and the framework version, the scope and what it hides, with examples from the scoped model; resources `govibe://skill`, `govibe://app/manifest`, `govibe://docs/<slug>`; the server is titled after `App.Name`; the overview and the prompt follow the scope |
| T3.9 docs | `/dev/docs` redirects to `/dev/docs/app`; the app's docs and specs come from the directories its profile names; the framework's are a muted group under `/dev/docs/govibe/<slug>` (`internal/frameworkdocs`: an allowlist, titles prefixed `GoVibe:`, copies of `docs/public` generated for `go:embed` by `make docs-embed` with a staleness test, the rules doc rendered from the tables); contributor mode (`dev.framework_src`, `GOVIBE_FRAMEWORK_SRC`) reads them from a checkout and lists its specs; a test asks for every framework spec under every URL in an app. New docs: Guide, Configuration, Upgrading |
| T3.10 `/dev` | the front door (`devui.DevIndex`): app, module, framework version, environment, what is mounted and why not, counts of the app's own routes, commands, packages and findings (in the app's scope), the first step when there are no routes; a Dev tab on every dev page |
| T3.11 fixture | `testdata/fixture` is an app built on a stub framework (`testdata/stubfw`, `framework: example.com/stubfw` in its `govibe.yml`), settings in a root `env.yml`; goldens regenerated and the diff reviewed (origin quads, the settings path, the external edges); new `externals.nq` golden and tests that every external edge has its node |

**Deviations and things to know**
- **A new indexer layer, `externals`.** The spec had each layer write `origin` and its externals; but the code, templates, routes and calls layers all point at the same external node, and a node's quads belong to one layer (the
  well-formedness test said so). The `externals` layer writes every external node from the indexes of the layers that point at them, and they write only the edge. The graph now has eleven layers; `findings` depends on it.
- **`Load` takes the scope as an optional argument** (`Load(ctx, qs, scope...)`, no scope meaning everything) instead of a required one, so the findings layer, the gate and the existing tests read the whole graph; every page and tool passes one,
  defaulting to `app`.
- **T3.4 is partly folded into other tasks.** The framework's routes and middleware come from the live router and `inspect --json` (with who registered them), not from a manifest, and the public API appears as the external nodes the project's
  imports and calls point at, not as a symbol list. The manifest carries the settings and the tools.
- **`dev_only` was kept**, next to `origin` (AM9 said to retire it): the rules and the views use the origin; the attribute is still on routes under `/dev` for the people and tools that read it.
- **`framework.import` does not judge test files** (tests add no nodes), so the table's "tests: `govibetest`" row is documented, not enforced; `doctor` (Phase 6) can. The `commands` layer is only an architecture layer here: `command` nodes, the
  commands report and `docs.command_undocumented` are T4.4.
- **`govibe check` is Phase 5.** Its parts exist: the gate (now with reasons) and the rules. The `skill` doc is rendered from the rules tables until Phase 5 generates the app-facing `SKILL.md`.
- **The `/dev` index has no scheduler card and `COMMANDS_AND_SCHEDULER` is not in the framework docs yet** (Phase 4: T4.5 and T4.7); the allowlist already names it and leaves it out until the file exists.
- **Run a framework gate by the same command as an app's**: with the framework's own `govibe.yml` and no binary, its routes layer is skipped and `framework.import` is not applicable, both reported as rules that did not run; the example app's run has the route table and
  runs every rule but the two that need a live database.
- **Tests that indexed this repository as "the app"** (the MCP e2e tests, the reports tests) now ask for scope `all`, because the repository's own `/_govibe/*` routes are the framework's by their path; separate tests cover the default scope.
- **Not run here:** the database tests (`GOVIBE_TEST_MARIADB_DSN`), `-race` on the kernel's dev test (the upstream `checkptr` issue of Phase 1), and the GitHub Actions workflows themselves (their scripts were run locally).
- **Fixture domain kept out of what an app reads.** The test fixtures are directories of their own modules (`testdata/fixture`, `fixture_layout`, `stubfw`): no build embeds them, and the framework's module zip omits them (checked with `golang.org/x/mod/zip`). What did reach apps
  was text: examples in tool descriptions, `graph_schema` and the embedded `GRAPH_SCHEMA.md`/`ARCHITECTURE.md` that named the shop domain and the fixture files. Those use neutral names now (`items`), the contributor-only parts of a doc sit between
  `framework-only` markers and are stripped from the embedded copy, and `TestNoShippedDocNamesTheFrameworksFixtures` fails if a served doc names a fixture. The Phase 0 prototype app `docs/spikes/_prototypes/shop-app` was deleted (its result is `examples/hello`).
- **An independent review of the Phase 3 diff** found thirteen things; ten were fixed and tested, three are noted. Fixed: (1) a `govibe.yml` key with no value (`layout:` and only comments under it) emptied the default
  and made every package `support`; a null value is now "not set" at every depth; (2) an app that overrides a framework default was told its `server.port` was "declared and read by nothing" (the config key now records that the
  framework declares it too); (3) a package of the app's own module under the framework's path (the generated templ package) was an external node of the framework in the calls layer (`moduleOf` now excludes the project's module);
  (4) a failed calls layer made the externals layer drop the leaves its old edges still point at (the layer now fails with it and keeps its nodes), and a skipped config layer still let docs resolve manifest keys (nothing resolves
  against settings the layer did not write; a typeless node belongs to no scope); (5) `--update-baseline --reason` did not fill an entry that had no reason; (7) with an invalid `govibe.yml` the CLI created the store in the working
  directory before failing, and doubled its error prefix; (8) a template or route edit with the calls layer on type-checked the whole module again (the call index is cached until the Go sources, `go.sum` or the tags change);
  (9) libraries were called "framework" in the inspector's line (it now says `uses framework: web; uses libraries: chi/v5`) and `chi/v5` was named `v5`; (10) a data race on the commands list of the `/dev` index; (11) the dev docs could
  not express "no specs" or "no docs directory" (`Docs.FromProfile`); (12) the config report now links to `GoVibe: Configuration`; (13) tests for the internal leaf, a library leaf, the calls failure, the cache and the hello module path were added, and one vacuous
  assertion was made real. (6) `govibe graph index` of an application compiles and runs its code: kept as the default (the route rules need the binary), now with a scrubbed environment (only the toolchain's and the path: no secret of the job
  reaches it) and a stated way out (`--no-inspect`). Noted, not changed: the banner's "framework nodes" count includes the libraries' externals; a few empty-state texts ("add Markdown files to docs/public", the `/dev` first-step text) name the
  default layout rather than the profile's; and an application with no `go` on `PATH` cannot be asked for its routes (reported with the exit status 2 and `--no-inspect`).

## Phase 4 results (commands and the scheduler)
Done 2026-10-15, on top of the committed Phase 3. A command can be declared, put on a schedule and run by the framework; `schedule list` says what is due when; the graph, the reports, the docs and the dev panel know about commands.
Verified in tests (the whole suite, default build and `-tags govibe_dev`, the scheduler's and kernel's under `-race` and stressed in 24 to 32 parallel processes), by both CI gates at `--fail-on info` (the framework and
`examples/hello`: 0 findings, empty baselines), and by running a scratch variant of the reference app (a store, a service and three commands) against a throwaway MariaDB, in a browser, over MCP and as two real instances (below).

| Task | Result |
| --- | --- |
| T4.1 `internal/scheduler` | `Parse` (a validating wrapper over the `robfig/cron/v3` parser, AM12: a bare `CRON_TZ=Zone` is an error instead of a panic, `@every` under a minute, a spec that never fires and a six-field spec are rejected, each error quotes the spec); `Runner` (overlap `Skip`/`Allow`, timeout as a context deadline, panic and error recovery, status `ok` `error` `panic` `timeout` `overrun` `skipped` `missed`, a history of 20 runs per task with identical consecutive skips counted as one record, `RunNow`, `Infos`, `Upcoming`, a graceful `Stop` that a second caller waits for); `Locker` and `DBLocker` (`GET_LOCK` per task on a connection of its own, names shortened to MariaDB's 64 characters); `Clock` and `Fake`. 35 tests on the parser and the fake clock (due times, DST gap and repeat, overlap, timeout both ways, panic and `Goexit`, a suspended machine, the lock and its hold, history, stop), plus 3 `GET_LOCK` tests against a real server (`GOVIBE_TEST_MARIADB_DSN`; run against a throwaway instance, skipped without the variable) |
| T4.2 declaration, kernel, settings | `govibe.Command.Schedule`, `Schedule`, `Overlap` (`Skip`, `Allow`), `Cron`, `Every`; `Build` validates the `scheduler.*` settings and every schedule (a boot error that names the command, every problem at once) and builds the runner without arming it; `serve` and `schedule run` start it; `serve --no-schedule`; settings `scheduler.enabled`, `.timezone`, `.shutdown_timeout`, `.lock` (typed `time.Duration`; `defaults.yml`; a bad zone, lock or duration fails the boot) |
| T4.3 runtime commands, log lines | `schedule list` (`--json`, `--next N`; computed from the declarations and the configuration, no database, no clock started, works in production), `schedule run` (the scheduler alone; refuses with nothing scheduled or `scheduler.enabled` false), one structured log line per run (`task`, `trigger`, `status`, `duration`, `detail`; a panic with its stack), and the line printed at start that says which multi-instance mode is in force |
| T4.4 graph | a `commands` layer (the 12th; `IndexerVersion` 6) from `inspect --json`: `command` nodes with `run_by` to the function that runs them (a closure resolves to its factory), `scheduled`, `schedule`, `timezone`, `overlap`, `timeout`, `every`; docs name a command in backticks or as typed (`./app name`); the info rule `docs.command_undocumented`; reach from a table to the commands that touch it in `graph_impact`, `graph_context` and the database explorer's references (scheduled first, with access and confidence); a `command` context, the commands report, the app map and the docs-coverage view; `graph_path`, search aliases (`cmd`, `task`, `job`, `cron`), the overview, `graph_schema`. `inspect --json` carries each command's schedule and the scheduler's settings |
| T4.5 dev surface (by a forked agent, reviewed here) | `/dev/schedule` (state, settings, tasks with next run, 20 runs each, "Run now"), `GET /dev/schedule/data`, `POST /dev/schedule/run/{name}` (refuses a cross-site request with Go's `http.CrossOriginProtection`; 202, 404 unknown, 409 running or stopped), the `schedule_status` MCP tool, and the card on `/dev`; page, JSON and tool read one document (`internal/devtools/schedview`) |
| T4.6 `govibetest` | `Boot` takes options (`WithTime`, `WithSettle`) and starts the scheduler on a fake clock: `srv.Clock.Now/Advance/AdvanceTo` fire every due time in order and return when the runs they started have finished, `srv.Runs(name)` reads the history; the database lock setting is ignored (no database), `scheduler.enabled: false` is respected, and the cleanup cannot hang on a task that is still running |
| T4.7 docs | `COMMANDS_AND_SCHEDULER.md` (new, in the framework docs an app serves), and the guide, configuration, architecture, upgrading and graph-schema docs, both `SKILL.md` files and the CI scripts brought up to date |

**Done-when, checked (A9).** *A command declared with a schedule runs by `./app <name>` and, with `serve` running, at its due time, proved with a fake clock:* `TestServeRunsScheduledCommandsAtTheirDueTime` runs `Run(ctx, app, nil)` (serve) on a fake clock,
runs the command by hand, advances to 02:00 and sees it run at exactly 02:00:00. *`schedule list` shows it with its next run; `schedule run` runs only the scheduler:* tested (output, JSON, `--next`, disabled and nothing-scheduled refusals; no port is bound).
*An invalid spec fails boot naming the command:* tested for each kind of bad spec and setting (`govibe check` itself is Phase 5). *`/dev/schedule` lists tasks and recent runs:* tested, and seen in a browser. Run for real, outside the tests (scratch copy under `env -i`):
a scheduled command fired on the minute against a throwaway MariaDB and changed its rows; a `@every` task with a 2s timeout was stopped at the deadline and logged `status=timeout`; "Run now" answered 202, then 409 while the task ran, 404 for an unknown name and 403 for a cross-site post;
**two instances** (`serve` and `schedule run`) with `scheduler.lock: db` ran each of two simultaneous tasks exactly once and logged the other's skip ("another instance holds the lock"); SIGTERM during a running task waited for it (3 s of the 30 s allowed), then stopped; the production build answered 404 on `/dev/schedule`.
`graph_impact` on the table answered with both commands that touch it and the full chain from the command down to the table; `graph_context` listed them, scheduled first, with read or write; the one undocumented command was the only finding.

**Deviations and things to know**
- **The engine is timer callbacks, not a goroutine per task** (the S8 prototype was loops): a fake clock then fires callbacks itself, in time order, inside `Advance`, with no sleeps and no handshakes, and after each timer it waits (real time, bounded, five seconds by default) for the runs that timer started. The same determinism makes `govibetest`'s `AdvanceTo` assertable at once. A test that starts a command which blocks on purpose lowers the wait (`WithSettle`).
- **Stop has a grace after the cancel.** The spec says to wait up to `scheduler.shutdown_timeout`; it also cancels what is left and now waits up to five more seconds for it to return (found by a stress run: `App.Shutdown` could start while a cancelled task was still unwinding). A task that ignores its context is abandoned after that.
- **The repeated-hour rule is read from the parsed spec and applies per reading.** A spec de-duplicates a repeated fall-back hour when its **hour field is not a wildcard**: `*/15 3 * * *` runs 4 times on that day, `0,30 3 * * *` twice, `* 3 * * *` sixty times, where AM12 said "a fixed hour and minute". "Not a wildcard" is read from the parser's hour bit set, so `*`, `*/1`, `0-23` and `@hourly` all follow real hours. The runner remembers the readings it ran in the last three hours (the first version remembered only the last one, which handled specs that fire once an hour and no others: found in review), a reading refused by overlap is not remembered, and `schedule list` and the dev page leave the repeated readings out.
- **A late timer does not run its due time.** A timer that fires more than 30 seconds after its due time (a machine that slept through the night) skips that due time and records it as `missed`, with the ones after it that also passed; the first version ran the armed due time however late (found in review: a daily 03:00 job ran at noon).
- **The database lock is held until 10 seconds past the due time.** The spec says the lock is held "for the duration of a run"; given back at the end of a quick run it would let a second instance a few milliseconds behind run the same due time again (found in review, shown with two runners on a real server, and run for real with two processes). A finished scheduled run now keeps it until `due + 10s` (`Options.LockHold`), a manual run gives it back at once, and `Stop` gives back what is held. Instances whose clocks differ by more than the hold can still both run: it is a guard, not a guarantee, and the docs say so.
- **A task's flags are declared once, a run that exits its goroutine is still counted, and the fake clock is linear.** Declaring `Flags` on every scheduled run rewrote the variables they bind under overlapping runs (a data race under `Allow`); a command that ends its goroutine (`t.FailNow` inside a command under test) skipped the bookkeeping, left the task "running" and a lock held; the fake clock scanned every fired timer on each step (40 days of a minutely task took seven seconds). All three were found by the review and have regression tests.
- **`scheduler.shutdown_timeout` takes a Go duration or a number of seconds, and `0` means do not wait.** The placeholder is quoted in `defaults.yml` so a bare `0` or `1` is not read as false or true; an error names the key (it used to report a line of the merged tree), and the old fall-back that turned `0s` into 30 s is gone.
- **`@every` has a minimum of one minute** (AM12 asked for a minimum; one minute is the cron granularity and keeps seconds out of v1), and the zone database is embedded (`time/tzdata`) so `CRON_TZ` works in a minimal container.
- **Cost.** The reference app's default binary grows by 0.56 MB (12.44 to 13.0 MB, 241 to 244 packages: the parser, the zone database and the scheduler), as S8 estimated; no dev package is linked (a test and the CI job check).
- **`serve` binds the port before starting the scheduler** (a process that cannot serve runs nothing), stops the scheduler and the server together, and gives `App.Shutdown` a context of its own (the scheduler's wait may have used the server's window).
- **`inspect --json` gained fields, the schema number did not change.** It now carries the scheduler's settings (timezone, lock, enabled): the one part of configuration the document holds, because a command's next run depends on it.
- **A pre-existing gap, found and fixed:** with the calls layer on (the default) the database explorer's "Referenced by" panel listed **no routes and no tools** (it asked for one hop; an entry point is four or five hops from its table). It now asks for entry points at the calls depth and keeps one hop for the code group; a regression test covers the routes, and commands use the same path.
- **No existing dev POST endpoint has a cross-site guard** (graph reindex, the database explorer's insert, reset and query accept a post from any site). The new "Run now" has one, because it runs the app's code. The others were not changed here: worth a decision.
- **Docs resolve a command by `noun:verb` in backticks or `./app noun:verb`**; a plain-word name (`cleanup`) is not taken from running text, so it needs the `./app cleanup` form. A command is never judged a stale reference.
- **The fixture grew a commands package** (`testdata/fixture/commands`, `routes/console.go`, `Command` types in `testdata/stubfw`); the goldens were regenerated and every changed line read (new files, the new `commands.nq`, two doc mentions, the `stubfw` externals, one new finding and one stale-reference line that moved because a doc line was added).
- **`docs.command_undocumented` is "did not run"** where there is no command table (the framework's own repository, `--no-inspect`), like `docs.route_undocumented`; with a table, an app that declares no command has a built, empty layer.
- **An independent review (three reviewers on fresh contexts, review-only) raised 35 numbered findings; 33 were fixed, each behavior with a regression test, and 2 are noted.** Beyond the ones named above: DNS rebinding on "Run now" (a page on a hostile name that resolves to 127.0.0.1 is "same-origin" with a dev server that listens on every interface: the endpoint now also refuses a Host that is not loopback); command names with a space, slash, accent or `%` made "Run now" answer 404 or run another task (the name is escaped on the page and unescaped only when chi routed on the raw path); `Until` said "in 0s", "in 60s", "in 60m" and "in 2d" for 2d11h; a `?refused=` address could make the page claim a task was running; the sample log lines in the doc were not what the app prints; a commands-only app (no `Routes`) gave the graph no root hint, so none of its commands resolved to code; the app map could drop every scheduled command at a node limit; a bare command name did not resolve in the graph tools; `graph_impact` did not list scheduled commands first or say which are scheduled; a command whose `Run` is a closure inside a wiring function was reported at confidence 100 (now 60, method `wiring_closure`); the docs layer said nothing when there was no command table; `CRON_TZ=Local`, an empty zone and a step wider than its field were accepted; a second Ctrl-C was swallowed during a long drain; a second concurrent `Stop` returned at once; `StopScheduler` ignored a runner that was never started, so a manual run could lose its database; and `govibetest`'s cleanup could take minutes for a stuck task. Noted, not changed: the durations in `schedule list --json`, `inspect` and the graph are Go's (`10m0s`) where the page and the MCP tool shorten them (`10m`), and the dev page refreshes every 3 seconds while a task runs (re-fetching its assets, which the request logger lists); also the commands layer records `scheduled=true` from the declaration, so it does not follow `scheduler.enabled=false` or a different timezone in the environment of the process that runs `inspect --json`.
- **A pre-existing bug found by running a gated test, not fixed beyond a hint:** `TestUpStatusDownAgainstAServer` (never run before, because it needs a real server) fails against one: with a DSN that lacks `parseTime=true`, a second `migrate up` (so the second boot of `serve`) fails with "Table 'goose_db_version' already exists", because goose reads the timestamps of its version table into `time.Time` and takes the failed read for a missing table. The DSN in `defaults.yml`'s own example has the parameter, so it goes unnoticed. The kernel now says so when that is the cause, and the configuration doc asks for the parameter; whether `OpenDB` should add it itself (it changes how every app's `DATETIME` columns scan) is a decision for the owner. The gated test itself still uses a bare DSN and so still fails against a real server.
- **No `/dev` endpoint has authentication, and the dev server listens on every interface.** "Run now" now checks the Host; graph reindex and the database explorer's insert, reset and query still take a post from any site or host. Binding the dev listener to loopback by default would close the whole class: a decision for the owner.
- **Not done here:** the CI overlay scenario (a generated app plus a test scheduled command, A6) is Phase 5's keystone job; the new lines added to `ci.yml` were run as scripts against real binaries, not through GitHub. `govibe check` and the doctor's "invalid schedule" report are Phases 5 and 6. The kernel's dev test and the `devui` and `devmcp` suites cannot run under `-race` because of the upstream `checkptr` issue in `github.com/boltdb/bolt`; with `-gcflags=all=-d=checkptr=0` they run, and are race-clean.
- Amendments: AM12 is applied (parser wrapper, `@every` minimum, DST rules, the offline `Next`); AM1 to AM3 (Phase 5), AM14 (Phases 6 and 7) and the sign-off of all fourteen are still open.

## Phase 5 results (scaffolder)
Done 2026-10-17, on top of the committed Phase 4. `govibe new <module>` writes an application that depends on the framework and holds only application code,
`govibe check` is the one command an app's CI runs, and the keystone proves the framework's own output. Verified by the suite (default build and
`-tags govibe_dev`), by both gates at `--fail-on info` (the framework and `examples/hello`: 0 findings, empty baselines), by a real `govibe new` from this checkout in a
scratch directory under `env -i` (56 s wall on a warm module cache, with air's dependencies downloaded), and by the keystone run locally (`make keystone`, 36 s warm).

| Task | Result |
| --- | --- |
| T5.1 templates | `internal/scaffold`: templates embedded with `all:templates`, delimiters `[[` `]]`, a `*.tmpl` file rendered and any other copied (the images), rendered Go gofmt-ed (the module path changes the import order). Two sets: `templates/demo`, synced from `examples/hello` by `go generate ./internal/scaffold` (`make scaffold-sync`) with the module path and the name templated (`SyncDemo` skips the module files, the settings, the docs and the readme, refuses a source holding a delimiter, and the generated `home_templ.go` ships too); `templates/app`: `env.yml` (the `--db none` and `mariadb` variants), `README.md`, `ARCHITECTURE.md` (it names `GET /`, `GET /api/hello`, `GET /hello`), `MEMORY_SCHEMA.md`, `Makefile`, `.air.toml`, `.gitignore`, the workflow. `go.mod`, `SKILL.md` and `govibe.yml` are rendered by code. `TestTheDemoCopyIsCurrent` fails on a stale copy, `TestTheRenderedAppBuildsAndTests` builds, vets and tests the rendered tree under another module path (offline, against this checkout and the example's `go.sum`), then deletes the demo as `SKILL.md` says and builds again |
| T5.2 `govibe new` | Validates the module path (`module.CheckImportPath`, so a local `shop` is allowed), derives the name (the last element, without a major-version suffix), refuses a non-empty directory, writes the tree, then `go mod tidy`, `go tool templ generate`, `git init` and a first commit (`--no-git`; an identity of its own when git has none), and `go tool govibe check` (`--no-check`). `--db none|mariadb`, `--replace <checkout>`, `--dir`. The subprocesses run with `GOWORK=off`. The tool reads its own version from its build information: a release or a commit is required as is; built from a checkout (`(devel)`, `+dirty`, `v0.0.0`) it refuses without `--replace` and says why. 35 files; the generated `go.mod` keeps `go 1.25.0` (98 lines, `go.sum` 664, as S4 predicted); `main.go` is 29 lines |
| T5.3 the app's files | `SKILL.md`: a managed block between `<!-- govibe:managed:start -->` and `<!-- govibe:managed:end -->`, rendered from `internal/rules` (principles, what each layer may import, the spec sections) and the facts of the version (layout, constraints, the CLI, the dev tooling, how to add things, testing, deployment, spec-driven docs), no demo text in it; an owned section seeded with the Hello GoVibe done criteria and the list of demo files to delete (`DemoDeletable`, computed from the demo set). `govibe.yml`: the profile keys as comments, and `managed: {version, files: {<file>: sha256:...}}` (`profile.Managed`, validated). The example's own `SKILL.md` is rendered by the same code (`TestTheExampleSkillIsCurrent`) |
| T5.4 `govibe check` | `cmd/govibe/cmd_check.go`: the managed files against the manifest (`scaffold.CheckManaged`: edited, missing, unlisted; reported, not failed), then `runGraphIndex` with `--calls` in a scratch store (`os.MkdirTemp`, removed after) and the app's `inspect --json`, against `<graph.dir>/baseline.json` when it exists; `--fail-on` (default `error`), `--format text|json|github`, `--no-inspect`, `--git`, `--root`. Exit 0, 1 (a new finding, a baseline entry without a reason), 2 (the check could not run: an app that does not boot fails here with the app's own error, which names an invalid schedule's command). The generated workflow runs `go tool govibe check --format github`; `make check` runs it locally |
| T5.5 tests | `govibetest` is Phase 4's; the scaffold's `main_test.go` and `prod_test.go` are the demo's, synced. The overlay's `schedule_test.go` tests a scheduled command on the fake clock in a generated app |
| T5.6 keystone | `TestKeystone` (`internal/scaffold/keystone_test.go`, skipped without `GOVIBE_KEYSTONE`; the `ci` workflow's `keystone` job and `make keystone`): builds the tool from the checkout; the real `govibe new github.com/keystone/shop --replace <checkout>`; the tree is clean (no `SPEC_*`, no dev file, `env.yml` at the root with no mail key, `main.go` under 30 lines, the first commit covers the tree); the overlay (`testdata/overlay`: `routes/console.go` declaring `greeting:log` on `*/5 * * * *`, `commands/greeting.go`, the clock test, a doc naming the command); `go build ./...`, `go build -tags govibe_dev ./...`, `go vet`, `go test`; `go tool govibe check --fail-on info` exits 0 with the empty baseline and reports `commands: 1 (1 scheduled)`; the dev build: `/`, `/api/hello`, `/hello`, `/dev` 200, `/dev/docs/govibe-specs` 404, `/dev/graph/data/status` lists the `commands` layer, `graph_schema` and `schedule_status` over `/dev/mcp` (the app is `shop`, scope `app`, the task is listed), `schedule list` shows the command; the production build: 404 on every `/dev` path and the dev files, no `/dev` link on `/`, the demo and its files served; `go list -deps` of the default build has no dev package; a `business` package importing `govibe/web` fails the check with `framework.import` and a controller importing a store with `layering` (A4). A scrubbed environment and `GOWORK=off` throughout |

**Done-when, checked.** A1: `govibe new` then `govibe check` exits 0 with 0 findings at any severity (run for real and in the keystone); the demo, `/dev`, `/dev/graph`, `/dev/docs`, `/dev/schedule` and `/dev/mcp` answer in the booted dev build (`/dev/db` needs a DSN and is not asserted). A2: the keystone's tree check. A4: the two planted violations. A6: the keystone itself. A7: the production boot and the dependency list. A9's missing piece (the overlay) is in A6.

**Decisions taken here (AM1 to AM3, as defaults; sign-off still open)**
- **AM1, the Go version:** the scaffold writes `go 1.25.0` explicitly (the framework's own; a test checks the constant against `go.mod`), templ at the framework's `v0.3.1020` and air at `v1.67.0`, the last release on `go 1.25` (`v1.67.4` and templ `v0.3.1070` need `1.26.0`); the framework's tool at the tool's own version. Nothing is fetched at `@latest`.
- **AM2:** the generated `go.mod` is rendered with every version pinned (require and `tool` lines) and `go mod tidy` runs once, instead of `go mod init` plus four `go get`s: the same result, one resolution pass, and nothing can raise the `go` line on the way. The keystone builds the app with the versions the app resolves (`GOWORK=off`).
- **AM3, air:** kept as a `tool` (D8). The cost is as measured: about 80 indirect modules in the app's `go.mod` (hugo among them), none linked into the app's binary; `go mod tidy` marks air `// indirect`, which is harmless. Flip to a Makefile install if the weight bothers.

**Deviations and things to know**
- **`README.md`, `env.yml` and the two docs are scaffold-owned templates, not synced from the example:** the example's versions describe its place in this repository. What is synced is the demo code, its tests, its assets, the migration, the baseline and the routes files. The example's `SKILL.md` goes the other way: it is rendered by the scaffold (`go generate` writes it), so the rules the example follows are the rules every app reads.
- **The example gained `routes/console.go`** (an empty `Console` with a `// govibe:commands` marker) and `Commands: routes.Console` in `main.go` (29 lines, the spec's "under 30"); `routes/routes.go` has a `// govibe:routes` marker. Phase 6's generators insert there. The leftover `assets/static/tailwind.css` (a 27-byte placeholder nothing referenced) is gone, and `assets/.DS_Store` is untracked (`.gitignore` now lists it).
- **The keystone is a Go test, not a shell job:** one implementation for CI and `make keystone`, exact assertions, the overlay rendered by the scaffold's own renderer, a scrubbed environment by construction. The `ci.yml` job runs it; it has been run locally, not through GitHub.
- **`check`'s doctor half is the managed-file report alone;** the rest of the doctor (the tool version against the pin, missing embeds, spec sections, an `inspect --json` with no environment) is Phase 6 with `govibe doctor`. Drift is reported, never failed: a user may edit the Makefile, and the hash exists so that `upgrade` does not clobber it. `check` defaults to `--fail-on error` like the workflow; "no finding at any severity" is `--fail-on info`.
- **A framework doc cannot name an app-relative path in backticks** when that path does not exist in the framework's own repository (`.github/workflows/govibe.yml`, `routes/console.go`): the stale-reference rule judges it there. Such mentions in `docs/public` are written without backticks. The generated app's docs name its files freely; its gate is clean at `info`.
- **The owned section of `SKILL.md` lists the demo files in backticks,** so once the demo is deleted the list becomes a stale-reference finding until the section is edited, which is what the seed says to do ("then delete this list"). The spec asked for the list; this is the consequence.
- **The templ-generated `home_templ.go` is part of the template set** and `go tool templ generate` runs anyway: the step proves the pinned tool works, and the keystone checks that the first commit still covers the tree (templ's output is identical).
- **Not run here:** the database tests, the GitHub Actions workflows themselves (the keystone was run as `go test` locally). `TestLockDBWithoutADatabaseIsRefused` (kernel, untouched) runs `serve`, which binds port 8080 before the lock check it tests, so it fails with "address already in use" whenever something listens there, as the reference app's dev server did on this machine during the suite runs (every other package passed both ways). Pre-existing; the test could bind a free port.
- Amendments: AM1 to AM3 applied as the defaults above; AM14 (Phases 6 and 7) and the owner's sign-off of all fourteen are still open.

## Phase 6 results (generators and upgrade)
Done 2026-10-17, on top of the committed Phase 5. `govibe make` adds code that obeys the rules and registers itself, `govibe upgrade` keeps the managed files
current across framework versions without touching the app's own, and `govibe doctor` says what is wrong before it costs time. Verified by the suite (default
build and `-tags govibe_dev`; every package green except the pre-existing `TestLockDBWithoutADatabaseIsRefused`, which binds port 8080 while a `hello` dev
server holds it on this machine), by both gates at `--fail-on info` (the framework and `examples/hello`: 0 findings, empty baselines, with the docs of this
phase indexed), by an offline test that writes every generator's output into a generated app and builds, vets, tests, gates and doctors it, and by the keystone
(`make keystone`, 64 s warm), extended with the generators, the upgrade and the doctor.

| Task | Result |
| --- | --- |
| T6.1 generators | `internal/generate`: a `Project` (go.mod, the app's name from `main.go`, the profile: each kind writes into the first directory of its layer, so a non-default layout is honoured) and a `Result` (files to create or edit, snippets, notes, whether templ must run); the tool applies it. Templates with the scaffold's delimiters, rendered Go gofmt-ed. Insertion at `// govibe:routes` and `// govibe:commands`: textual at the marker (found by searching the wiring directories one level deep), then parsed, imports added with `astutil.AddImport`, formatted; the marker stays. Doc lines under the "Routes", "Commands" and "Tables" headings of the app's `ARCHITECTURE.md` and `MEMORY_SCHEMA.md`, so the docs rules stay at zero. A missing marker or heading prints the snippet; an existing file is refused without `--force`; `--dry-run`. Kinds: `page` (templ page with `web.HTMXScript`, controller, route, `go tool templ generate` after), `resource` (migration creating the table, store over `*sqlx.DB` answering `ErrNoDatabase` while no DSN is set, service with a `Reader` interface and a fake-backed test, JSON controller with list and get, both routes constructed in the routes function, doc lines), `action`, `service`, `store`, `migration` (numbered after the last, `--table`), `middleware` (chi wants `Use` before any route: the snippet is printed), `command` (noun:verb reads as VerbNoun, body, declaration, doc line; `--schedule` validated by `scheduler.Parse` and a test on the fake clock that advances to the first due time), `config` (a struct for the app's section of `env.yml`, nested, typed from the placeholders' defaults, `Load(cfg)` named so the config layer sees the read), `spec` (the sections of `rules.SpecSections`). Each test kind has a golden tree under `testdata/golden` (16 cases, `-update`); `TestGeneratedCodeBuildsAndPassesTheGate` in `cmd/govibe` writes all of them into a generated app and runs `go build` both ways, `go vet`, `go test`, `check --fail-on info` (0 findings; 2 commands, 1 scheduled) and `doctor` (0 failures), offline against the checkout |
| T6.2 upgrade and doctor | `scaffold.PlanUpgrade`: the managed files rendered for the version `go.mod` pins (`modfile.Parse`; `v0.0.0` under a `replace`), compared with disk and with the manifest's hash: `unchanged`, `updated` (diff shown), `restored`, `created`, `edited` (refused, exit 1, nothing written) or `forced`; `SKILL.md`'s block alone is replaced, a file without markers counts as edited and `--force` puts the block at the top; the `managed:` section of `govibe.yml` is swapped textually, the app's keys and comments kept; a unified diff of the framework's own (`scaffold.Diff`); the notes of the versions crossed from the embedded `UPGRADING.md` (`UpgradeNotes`: released versions after the manifest's up to the pin, plus the unreleased section when the pin is a commit or a checkout); `--dry-run`, `--no-notes`. `govibe doctor`: the tool's version against the pin, the managed files, every spec's sections, baseline entries without a reason (fail), the markers, a script tag loading from the network, and with the build: `inspect --json` under the scrubbed environment (fail with the app's own words), embeds not handed over (`contributes`), a dev package in `go list -deps -tags ''` (fail); `--no-build`; exit 1 on a failure. `check` prints the doctor's static findings before the gate |
| Keystone | After the existing sections: `make resource order`, `make command items:prune --schedule "0 3 * * *"`, `make page about` (templ runs); build both ways, vet, test; `check --fail-on info` reports 0 findings and `commands: 2 (2 scheduled)`; the dev build serves `/about`, answers 500 with `no database is configured` on `/api/orders` and 400 on `/api/orders/x`, `schedule list` shows the command. Then `upgrade` with nothing to do, the Makefile edited and refused (exit 1, the diff shows `-lint:`), `--force`, the README unchanged, `check` green with `managed files: 4 as generated`. Then `doctor` with the real build checks: the checkout is a note, inspect, embeds and build ok, 0 failures |
| Docs | `GoVibe: Guide` ("Adding things", doctor and upgrade under "What you run"), `GoVibe: Upgrading` (step 4 and the unreleased entry), `GoVibe: Commands and Scheduler` (`make command`), the framework's `ARCHITECTURE.md` (a section for this phase), the app-facing `SKILL.md` block (the generators under "Adding features", doctor and upgrade under "Development"; the example's `SKILL.md` regenerated, so its managed hash changes), `README.md` and the contributor guide |

**Done-when, checked.** A5: the keystone bumps nothing (no release exists) but proves the mechanism on the real app: an edited managed file shows its diff and is refused, `--force` rewrites it, owned files are untouched, the build and the gate stay green; the unit tests cover a manifest at an older version (`updated`), a missing file (`restored`), a `replace` (`v0.0.0`) and the notes for a pseudo-version. The resource and the scheduled command in a scaffolded app leave the gate green at `--fail-on info`, in the keystone and offline.

**Decisions taken here**
- **The generators insert, the docs follow.** A route or command the generator writes is also named in the app's docs (a bullet under the heading), because the alternative, an info finding on every generated route, would teach users to accept findings. The heading is looked up case-insensitively; without it the line is a printed snippet.
- **Middleware is not inserted at the routes marker:** chi refuses a `Use` after a route, and the marker is after the routes. The snippet says where it goes.
- **A generated store refuses queries with no database** (`ErrNoDatabase`) instead of letting a nil pool panic: the demo app has no DSN, and the generated routes must answer something sensible there.
- **`upgrade` reads the version from `go.mod`, not from the tool:** "the version the app builds against" is the pin, and the tool is that version anyway when run as `go tool govibe`; the doctor warns when it is not.
- **`check` keeps its managed-file lines as they were** (the keystone and the workflow parse them) and prints only the doctor's findings that are not ok under a `doctor:` line; the checks that build the app stay in `doctor`, because `check` already builds it for `inspect --json` and `--no-inspect` must keep working on a tree that cannot build.

**Deviations and things to know**
- **`make config` binds through `cfg.Unmarshal("<app>", &out)`:** the config layer recognises a read only on a receiver named `cfg` or `conf` (or a `.Config` field), so the generated `Load` names its parameter `cfg`; with any other name the keys count as declared and unused.
- **The offline build-and-gate test generates the page only when templ is installed** (the test's module has no `tool` directive; it reuses the example's `go.mod`); the keystone always does, through `go tool templ`.
- **`upgrade` shows a diff for every file it rewrites,** not only for refused ones: "upgrade is boring" is easier to believe when the change is visible.
- **No release exists yet,** so the version flow was proved with a manifest at `v0.1.0` against pins of `v0.2.0` and a pseudo-version, and with the keystone's `replace`. The first tag (Phase 7) will be the first real `go get` upgrade.
- **Not run here:** the database tests, the GitHub Actions workflows themselves (the keystone was run as `go test` locally). `TestLockDBWithoutADatabaseIsRefused` failed only because a `hello` dev server was listening on 8080 during the runs (pre-existing, noted in Phase 5).

## Risks
| Risk | Mitigation |
| --- | --- |
| The public surface churns before 1.0 | Four packages and one struct; everything else `internal/`; `UPGRADING.md`; `gorelease` after the first tag |
| Moving 16,700 lines of indexer breaks tests or goldens | Phase 2 is moves only, goldens must be byte-identical; logic changes wait for Phase 3 |
| Handlers in another module resolve to unusable paths | Spike S2 before any design depends on it; external nodes as the fallback |
| `go tool govibe` cold start is slow (large binary) | Spike S4 measures it; a pinned `go install` is the fallback; the shim in T7.1 |
| A forgotten tag means no `/dev` locally | One boot log line, `make dev` passes it, the docs say so; the opposite default would ship dev code by accident |
| Scaffolded files drift from the framework | Managed files with hashes, `doctor`, the keystone CI job |
| Registration touches the database or secrets, breaking offline facts | The contract says so, `doctor` runs `inspect --json` with an empty environment |
| The app's `go.mod` lists the dev dependencies as indirect | Accepted for v1 (they are not linked in a default build); only Option D removes them |
| The scope filter hides something a user needs | The banner always names the scope and counts what it hides; one switch; `all` is complete |
| The scheduler runs a task twice on several instances | Documented prominently; `--no-schedule`, `scheduler.lock: db`, a log line at start; default behavior stated in `schedule list` |
| `robfig/cron/v3` is frozen | Only its parser is used, behind `internal/scheduler`; S8 compares it with `gocron` v2 |
| Users read "scheduled" as "live state" | `schedule list` says it is computed from declarations; live history is in `/dev/schedule` and the logs |
| A framework doc leaks into every app by accident | An allowlist, a test that fails on any `SPEC_*`, and the scaffold test |
| The demo a user gets drifts from the demo the framework gates | `examples/hello` is the source; `go generate` syncs it into the template set; CI fails on a stale copy; the keystone job runs the generated demo |
| The demo's pages show dead `/dev` links in production | Every such link sits inside `web.IfDev`; A7 and the keystone job fetch `/` from the default build and look for `/dev` |
| The demo's look breaks when the framework restyles its dev panel | The demo owns copies of its theme, styles and images; only htmx comes from the framework |

## Backlog
- Out-of-process dev panel that survives a failing build (Option D), if Phase 7 says go.
- Persisted scheduler run history and retries; a real job queue is a separate feature.
- `RunOnStart`, jitter and seconds-granularity schedules, if asked for.
- Distributed task locks for databases other than MariaDB.
- Split the dev stack into its own module if dependency weight or release cadence demands it.
- `govibe dev` replacing Air with a built-in supervisor.
- `govibe add workos` and other add-ons, when someone asks for them.
- Postgres and SQLite scaffolds (`--db`), with the migration parser support the previous spec already lists.
- A template for a JSON-API-only app (no views, no htmx).
- `AGENTS.md` / `CLAUDE.md` pointer file in the scaffold, if wanted.
- Promote `internal/codegraph` to a public library once its API settles.
- Multi-app repositories (several `govibe.yml`).
- Generated API reference for the public packages inside the `GoVibe:` docs.

## Decisions defaulted here (flip any of these)
| Decision | Default | Alternative |
| --- | --- | --- |
| D1 Module path | **Decided by the owner:** public and open source, `github.com/petars-dev/govibe` (renamed from `petars-dev/govibe`), Apache-2.0 | none |
| D2 Dev tooling build tag | **Decided by the owner:** opt-in `govibe_dev`, so a default build cannot contain it | opt-out `govibe_prod` |
| D3 Where the panel runs | inside the app process (Option C) | supervisor-hosted (Option D), Phase 7 |
| D4 Modules | one module, `internal/` for everything not public | separate dev module (the app's `go.mod` still lists it) |
| D5 App layout | top-level directories as in `SKILL.md` (no `internal/`), plus `commands/` | `internal/` prefix through `govibe.yml` layout overrides |
| D6 Composition root | `main.go` plus `routes/` (`routes.go` for HTTP, `console.go` for commands) | a dedicated `app/` package |
| D7 Tool delivery | `tool` directives pinned in the app's `go.mod`; global install only for `new` | global install with a version shim only |
| D8 Air | pinned as a `tool` | global install, as the Makefile does today |
| D9 htmx | served by the framework at `/_govibe/static`, version pinned per release | vendored copy under the app's `assets/` |
| D10 Scaffold engine | templates embedded in the tool | template repository, or `gonew` |
| D11 WorkOS | out of core; optional add-on later | stays in core |
| D12 Generators | Phase 6, after the scaffold and the gate are solid | Phase 5, with the scaffold |
| D13 `env.yml` | **Decided by the owner:** project root, embedded by `main.go`, values resolved from the process environment through `${ENV:default}`, as today; no override file | none (an override file or an `env.yml` beside the binary was considered and rejected) |
| D14 Scheduler engine | own runner over the `robfig/cron/v3` parser; five-field cron, no seconds | `gocron` v2 as the engine; seconds field |
| D15 Where commands are wired | `routes/console.go`, with thin command bodies in `commands/` (a layer with the controllers' rules) | commands declared in `main.go`, or in a separate `app/` package |
| D16 Default graph scope | `app`, remembered per browser; `framework` and `all` one switch away | `all` by default |
| D17 Framework docs | embedded allowlist under `/dev/docs/govibe/`, prefixed `GoVibe:`; framework specs never shipped | ship everything; or no framework docs in the panel at all |
| D18 Several app instances | **Decided by the owner:** no lock by default; the scheduler runs on every instance unless `--no-schedule`; `scheduler.lock: db` is opt-in | lock on by default when a database is configured (rejected) |
| D19 Hello demo | **Decided by the owner:** every generated app contains the Hello GoVibe demo as plain owned code; `examples/hello` is its source of truth, synced into the template by `go generate`, CI fails on a stale copy | no demo, or an opt-in or opt-out flag (rejected) |
| D20 The demo's assets and dev links | the demo owns copies of its theme, styles, logo and hero image; htmx comes from the framework (`web.HTMXScript`); its `/dev` links sit inside `web.IfDev` | the demo loads the dev panel's theme from `/_govibe/static`, which couples an app page to the panel's CSS |

## Status Table

| Item | Implemented | Skipped | Why |
| --- | --- | --- | --- |
| Phase 0 spikes (S1 to S9) | Yes | | Run 2026-10-11 in scratch copies; results and amendments AM1 to AM14 are under "Spike results (Phase 0)" |
| Phase 0 decision sign-off | No | | Pending the owner: accept or flip AM1 to AM14. AM1 to AM3 were applied as defaults in Phase 5 (go 1.25.0, templ v0.3.1020, air v1.67.0 as a tool), see "Phase 5 results" |
| Phase 1 kernel, layered config, router split, framework-owned dev assets | Yes | | Done 2026-10-11, see "Phase 1 results"; the kernel's types are in `internal/kernel` until the public `govibe` package (Phase 2) |
| Removal of mail settings, dead WorkOS client, `code.unreferenced` false positive | Yes | | Phase 1: keys, client, mentions and the `go.mod` entries are gone; the rule counts references from tests |
| Phase 2 physical split, public API, module rename, `examples/hello` | Yes | | Done 2026-10-12, see "Phase 2 results"; one logic fix was needed (the routes layer's root hint) |
| Production exclusion of dev code (tag plus CI job) | Yes | | Phase 2: `govibe_dev`, 241 against 443 packages, a test and a CI job (A7) |
| The framework's own layering finding resolved, empty baseline | Yes | | Phase 2: both gates (framework, `examples/hello`) report 0 findings at any severity |
| `env.yml` in the project root (embedded, environment-resolved as today) | Yes | | Phase 1 for this repository (embedded by `main.go`, over `config/defaults.yml`); the scaffold's copy (with a `--db mariadb` variant): Phase 5 |
| Hello GoVibe demo in every generated app (`web.IfDev`, synced from `examples/hello`) | Yes | | `examples/hello` is a module of its own (Phase 2); Phase 5 syncs it into the scaffold's templates (`make scaffold-sync`), a test fails on a stale copy, and the keystone runs the generated demo |
| Phase 3 profile, origin, `framework.import` rule, offline facts, fixture rebuild | Yes | | Done 2026-10-13, see "Phase 3 results" (one extra indexer layer, `externals`; the profile has a `framework` key) |
| App-only graph view (scope in the `Model`, app map, switch, tools) | Yes | | Phase 3: scope in the `Model`, the app map as the default view, the switch and banner, `scope` on every graph tool and report |
| Findings: reasons in the baseline, rule overrides, test references | Yes | | Test references: Phase 1. Reasons in the baseline (`reason`, `date`, `--reason`, the gate fails without one), `rules:` overrides in `govibe.yml`, `framework.import`: Phase 3. `docs.command_undocumented`: Phase 4 (done) |
| Docs surface (app first, `GoVibe:` group, no framework specs, contributor mode) | Yes | | Phase 3: `/dev/docs` opens on the app's docs, the framework's are `GoVibe:` docs under `/dev/docs/govibe`, no spec is embedded or served in an app (tested), contributor mode through `dev.framework_src`. `COMMANDS_AND_SCHEDULER` followed in Phase 4 (done) |
| `/dev` index page | Yes | | Phase 3 (T3.10); its scheduler card came with `/dev/schedule` in Phase 4 |
| `govibe.yml` project profile (layout, rules, derived paths, framework) | Yes | | Phase 3; the manifest half (`managed`: version and managed-file hashes) is Phase 5 |
| Phase 4 commands and the scheduler (`schedule list`, `schedule run`, dev page, graph layer) | Yes | | Done 2026-10-15, see "Phase 4 results"; A9 is proved by tests on a fake clock and by real processes (the CI overlay job is Phase 5's keystone) |
| Phase 5 `govibe new`, managed files, app `SKILL.md`, `govibe check`, keystone CI job | Yes | | Done 2026-10-17, see "Phase 5 results"; `govibe upgrade` (which rewrites the managed files) followed in Phase 6 (done) |
| Phase 6 generators, `govibe upgrade`, `govibe doctor` | Yes | | Done 2026-10-17, see "Phase 6 results"; ten generators with golden tests, the upgrade with diffs and refusal, the doctor with build checks, the keystone extended |
| Phase 7 version shim, Option D evaluation, `App.DevTools`, open-source readiness, first release | No | | Planned |
| Out-of-process dev panel in v1 | No | Skipped | Deferred to the Phase 7 evaluation; in-process keeps today's behavior and a mechanical move |
| Separate dev module | No | Skipped | Does not shrink the app's `go.mod`; adds release overhead |
| Runtime DI container | No | Skipped | Explicit constructors fit the layering rules and the indexer's static view |
| Plugin or third-party extension API | No | Skipped | One extension point (`App`) in v1 |
| Compile-time analysis framework | No | Skipped | Changes how code is written; out of scope |
| Mail support or mail settings in a generated app | No | Skipped | Not asked for: nothing in the repo reads them |
| Deploy-time config override file, scheduler lock on by default | No | Skipped | Decided by the owner: configuration stays as it is today, and the lock is opt-in |
| Persisted scheduler history, retries, a job queue | No | Skipped | Not asked for; logs and the dev page cover v1 (Backlog) |

## Sources
- [Go modules: tool dependencies proposal 48429](https://go.googlesource.com/proposal/+/master/design/48429-go-tool-modules.md) and [Go 1.24's tool directive](https://rednafi.com/go/tool-directive/)
- [Package embed](https://pkg.go.dev/embed) (module-boundary rule for `go:embed`)
- [gonew](https://pkg.go.dev/golang.org/x/tools/cmd/gonew) (copies a template module and rewrites import paths; experimental)
- [Goravel compared with Laravel](https://goravel.dev/prologue/compare-with-laravel.html)
- [Best Go backend frameworks (Encore)](https://encore.dev/articles/best-go-backend-frameworks)
- [Kratos, go-zero, GoFrame and sponge compared](https://dev.to/zhufuyi/comparative-analysis-of-go-frameworks-kratos-go-zero-goframe-and-sponge-3a2l)
- [Cron libraries for Go: robfig/cron v3 and gocron v2 compared](https://cronuru.com/guides/go-cron) and [gocron releases](https://upd.dev/go-co-op/gocron/releases)
