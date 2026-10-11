GoVibe Web App Framework Skeleton

Overview
This repository contains a pragmatic Go web app skeleton with:
- templ for typesafe server-rendered HTML
- htmx 2.0 vendored under assets for simple interactivity without node/npm
- chi v5 router with middleware and subroutes
- goose v3 for SQL migrations embedded into the binary
- sqlx over database/sql with mysql driver (MariaDB compatible)
- Tailwind CSS v4 via standalone CLI (binary placed in .tools, no node)
- All assets embedded for single deployable binary

Directory Layout
- assets: htmx, templates (templ), static files, embed.go to embed all assets
- controllers: HTTP handlers that only orchestrate actions/services -> views/API
- middleware: cross-cutting HTTP middleware
- routes: central route wiring (routes.go)
- storage: DB access and migrations (mariadb driver wrapper, goose migrations, dev-only dbexplorer, dev-only graphdb)
- business: actions (single-function business logic) and services (packages)
- business/services/codegraph: dev-only graph indexer that fills the /dev/graph store with the project's own structure (Go code, routes and their handlers, templates, config keys and their use, MCP tools, the database schema and who reads which table, docs and specs) on every boot, and refreshes stale layers on page load and graph_query. Two opt-in layers (off by default) add the call graph (which function calls which, so a route's path to a table is exact) and git history (last commit, churn, files that change together); `govibe graph index` builds the same graph offline and gates CI on its findings
- devmcp: dev-only MCP server (official Go SDK) mounted at /dev/`mcp`, exposing db_* tools and the graph_* agent surface (graph_schema first, then graph_context, graph_impact, graph_path, graph_search, graph_neighbors, graph_findings, graph_reindex, and the raw graph_query) plus two resources and the explain-change prompt to local agents

Key Constraints
- Controllers contain no business logic. They only call actions/services and render responses.
- Only actions/services may call the storage layer.
- All assets (templates, JS, CSS) and migrations are embedded. The binary is self-contained.

Local Development
1. Go version: ensure your environment matches go.mod.
2. Dependencies: `go mod tidy` will fetch required libraries (templ runtime, chi, sqlx, goose, mysql driver).
3. Single dev tool: Air
   - `make air` launches Air to watch .go/.templ/.css/.js files.
   - Air runs templ generate automatically, rebuilds and restarts the server on changes.
4. Tailwind CSS:
   - Download Tailwind v4 standalone to `.tools/tailwind`
   - Example build: `.tools/tailwind -i ./assets/static/input.css -o ./assets/static/tailwind.css --minify`
   - You can also commit a simple pre-built CSS to `assets/static/tailwind.css` for now.
5. Config file: env variables are centralized in `config/env.yml` and resolved from the process ENV.
   - Example overrides: `APP_ENV`, `PORT`, `LOG_LEVEL`, `WORKOS_API_KEY`, `WORKOS_CLIENT_ID`, `MARIADB_DSN`.
6. Run the server: `go run .` or `make dev`
   - Visit http://localhost:8080
   - `/` renders the Hello GoVibe page.
   - `/api/hello` returns `{"message":"Hello GoVibe!"}`.

Configuration
- Centralized in `config/env.yml` and resolved at runtime:
  - APP_ENV: one of local, dev/development, staging, prod/production (defaults to local)
  - PORT: HTTP port (default 8080)
  - LOG_LEVEL: logrus level (panic,fatal,error,warn,info,debug,trace)
  - WORKOS_API_KEY, WORKOS_CLIENT_ID: optional; client is initialized if provided
  - MARIADB_DSN: if set, app connects to MariaDB/MySQL and runs embedded goose migrations on startup
    Example DSN: `user:pass@tcp(127.0.0.1:3306)/traceloop?parseTime=true&multiStatements=true`
  - DATABASE_TYPE: goose migration dialect (default `mysql`; see github.com/pressly/goose/v3 for
    other supported values). Goose defaults to postgres if never told otherwise, which silently
    breaks migrations against any other database — this is always set explicitly instead.
  - DEV_DB_ALLOW_MUTATIONS: if true (and MARIADB_DSN is set, and not production), enables the
    `/dev/db` UI/API and MCP test-data mutation tools (`db_insert_test_row`, `db_reset_test_data`).
  - GRAPH_CALLS, GRAPH_GIT: if true, switch on the opt-in `calls` and `git` layers of the project graph at boot
    (default false; `graph_reindex` naming one of them switches it on without a restart).
  - Example feature: `mail.smtp.*` values can be defined and loaded dynamically.

Dev-Only Tooling (never mounted in production)
- `/dev/graph`: embedded Cayley graph store + query console (bolt file under docs/graph). The store is filled on every boot with a model of the project: Go code, routes and their handlers, templates, config keys and their use, MCP tools, the database schema (from the migrations and, if `MARIADB_DSN` is set, the live database, with drift between them) and which code reads or writes which table, docs and specs (see `docs/public/GRAPH_SCHEMA.md` for node IDs, predicates and example queries, e.g. `g.V(g.IRI('route:GET:/api/hello')).Out(g.IRI('handled_by')).All()`). Edits to docs and specs show up on the next page load or `graph_query` without a restart; `POST /dev/graph/reindex` rebuilds everything on demand. The page draws bounded views (architecture, request flow, database ER, docs against code, config usage, findings) with an inspector, a freshness banner and a findings panel; `/dev/graph/reports/{routes,config,schema,layers}` are reference pages generated from the graph, so prefer them to hand-written route and schema tables. **Findings** (layering violations, docs that name files that are gone, config declared and never read, ...) are in the panel and from `graph_findings`; a rule that did not run is listed as such.
- `/dev/db`: embedded database explorer (schema, browsing, filtered queries, sampling, explain
  plans; gated insert/reset mutations). See `storage/dbexplorer` and `controllers/devdb*.go`.
- `/dev/mcp`: MCP server (official Go SDK, `github.com/modelcontextprotocol/go-sdk`) over
  stateless streamable HTTP, exposing `db_list_tables`, `db_describe_table`, `db_query`,
  `db_explain`, `db_sample_rows`, `db_insert_test_row`, `db_reset_test_data`, and the graph tools
  to local agents. See `devmcp/`.
  - **Agents: start with `graph_schema`** (what the project graph holds, how fresh it is, which tool answers which
    question). Then `graph_context` (a file, package, symbol, route or table, in one call), `graph_impact` (what is
    affected if it changes, and which docs to update), `graph_path` (how a route reaches a table), `graph_search`,
    `graph_neighbors`, `graph_findings`, `graph_reindex`; `graph_query` runs a raw Gizmo query. Every answer carries a
    confidence: 100 is a stored edge or a plain call, lower is a call through an interface or, with the `calls` layer
    off, an estimate from package imports (possible, not proven). With `calls` on (`GRAPH_CALLS=true`, or `graph_reindex`
    with layers `["calls"]`) reach from a route to a table is a chain of calls and "no table is reached" is a measured
    answer; with `git` on, `graph_context` and `graph_impact` also say what usually changes with a file. Resources `govibe://graph/overview` (architecture map) and `govibe://graph/schema`, and
    the prompt `explain-change` (paths or a diff in, impact and docs to update out) come from the same graph.
- All three are wired in `routes/routes.go` behind `!cfg.IsProd()`.

Adding Features
- New pages: add a `.templ` file under assets/templates, run `templ generate`, add a controller that renders it, then map route in routes/routes.go
- New APIs: add an action/service in business/, call it from a controller in controllers/, add the route
- DB changes: create a new SQL file in storage/migrations/sql/ with goose directives, and reference DB from storage packages. On start, Up() runs automatically.
- New configuration blocks: define variables in `config/env.yml` using `${ENV:default}`, then either:
  1) Add fields to the `config.Config` struct to bind statically; or
  2) Use dynamic helpers: `config.Unmarshal("mail.smtp", &smtpCfg)` or `config.Get("mail.smtp.host")`.

CLI
- The repo includes a GoVibe CLI using Cobra in `cmd/govibe`.
- Build it with `go build -o govibe ./cmd/govibe`.
- Commands:
  - `govibe serve` runs the HTTP server using `config/env.yml`.
  - `govibe generate` runs `templ generate`.
  - `govibe migrate up` applies embedded goose migrations.
  - `govibe migrate down` applies one down migration.
  - `govibe migrate status` prints migration status.
  - `govibe graph index` indexes the project into a graph store (`--dir`, a scratch directory while a dev server holds the default one) and prints each layer's status and the findings. `--calls` and `--git` switch on the opt-in layers; `--fail-on error --baseline docs/graph/baseline.json` exits 1 on a finding the baseline does not accept (what CI runs: `.github/workflows/graph.yml`, locally `make graph-check`); `--update-baseline` accepts what is there; `--format json|github`. Exit 2 means indexing itself failed.
- To add new commands: add a `cmd_*.go` file in `cmd/govibe`, define a `cobra.Command`, and register it in `init()` (see `cmd_migrate.go`).

Testing
- Add unit tests next to packages using `_test.go` files.
- For handlers, prefer request/response tests against the chi router.

Deployment
- Build a single binary: `go build -trimpath -ldflags='-s -w' -o app`
- Ensure `templ generate` was run before building.
- The binary embeds assets/migrations; no external runtime files required.

Hello GoVibe Done Criteria
- UI: `/` renders a templ page showing "Hello GoVibe!" and a button triggering htmx to call `/api/hello`.
- API: `/api/hello` returns JSON `{"message":"Hello GoVibe!"}`.
- All assets and migrations embedded; app runs as one binary.

Environment & Local Dev Guidelines
- APP_ENV controls behavior across local/dev/staging/prod. Defaults to local.
- In local/dev:
  - Static assets served with no-cache headers for faster iteration.
  - Air handles templ codegen and server reload; no separate watch commands needed.
- Configuration is centralized in `config/env.yml` with `${ENV:default}` placeholders. Prefer adding new keys there, not ad-hoc env lookups in code.

Spec-Driven Development Guidelines
- Keep human-readable docs in `docs/`:
  - `docs/public/ARCHITECTURE.md`: high-level architecture of GoVibe apps (update on structure changes).
  - `docs/public/MEMORY_SCHEMA.md`: DB schema and any in-memory storage models (update with each migration/shape change).
  - `docs/specs/`: spec-driven process artifacts. For each epic/feature/user story, add `SPEC_{FEATURE_NAME}.md` with:
    1. Description
    2. Goals
    3. Phases and Tasks
    4. Backlog (tasks)
    5. Status Table of implemented vs skipped with rationale
- After each development loop (including AI runs), update:
  - Relevant SPEC_{FEATURE_NAME}.md with progress and deviations.
  - ARCHITECTURE.md and/or MEMORY_SCHEMA.md if architecture or data shapes changed.
- Visualizations: browse `http://localhost:PORT/dev/docs/app` for public docs and `http://localhost:PORT/dev/docs/specs` for specs.
