# hello: the rules of this GoVibe application

<!-- govibe:managed:start -->
<!-- Rendered by the framework from the rules it enforces. govibe upgrade rewrites this block; everything after the end marker is yours. -->

## What this is
A GoVibe application: a Go module that depends on the framework (`github.com/petars-dev/govibe`) and contains only application code. `main.go` is a declaration, `govibe.Main(newApp())`: it hands the framework the embedded settings, migrations and assets, the routes and the commands, and the framework boots and runs it. The framework never imports the app.

## Stack
- templ for type-safe server-rendered HTML (`go tool templ generate` after editing a .templ file).
- htmx 2, served by the framework at `/_govibe/static/htmx.min.js` (`web.HTMXScript()` renders the tag): interactivity without node or npm.
- chi v5 router with middleware and subroutes; the framework builds the router around `routes.Register`.
- goose v3 SQL migrations under storage/migrations/sql, embedded; sqlx over database/sql with the mysql driver (MariaDB compatible).
- One binary: assets, templates, settings and migrations are embedded. No external runtime files.

## Layout
- `main.go`: the declaration, nothing else (keep it under 30 lines). `main_test.go` boots it with `govibetest.Boot`.
- `env.yml`: this app's settings, in the project root, embedded by main.go and layered over the framework's defaults. `govibe.yml`: the project profile and the manifest of managed files.
- `routes/routes.go` (HTTP, `routes.Register`) and `routes/console.go` (CLI commands and their schedules, `routes.Console`): the wiring layer. Construct dependencies here (stores, services) and hand them to controllers and commands; nothing else constructs anything.
- controllers: thin HTTP adapters that parse the request, call a business action or service and render a view or JSON.
- commands: thin CLI adapters with the same rule (a command is the CLI twin of a controller).
- business/actions (single-function logic) and business/services (packages): plain Go, no framework.
- storage: one package per store, taking a *sqlx.DB from its constructor, and storage/migrations (`storage/migrations/embed.go`, sql/).
- middleware: app middleware only. assets: templates (templ) and static files, embedded by `assets/embed.go`, served at /assets. docs: public docs, specs and the graph baseline.

## Rules (checked by `go tool govibe check`)
### Layering
- Only business actions and services may call the storage layer.
- Storage is the lowest layer: it depends on nothing above it, only on support code.
- Business logic does not depend on the HTTP layer or on commands (controllers, commands, middleware, views) or on wiring and dev code.
- Packages of the wiring and dev layers may import anything: they assemble the application.

### What each layer may import from the framework
- business: nothing
- storage: nothing
- controllers: govibe/web
- middleware: govibe/web
- views: govibe/web
- commands: govibe
- support: govibe/config
- wiring: govibe, govibe/config, govibe/web

No application package imports `govibe/internal/...` (not API) or `govibetest` (tests only).

### Specs
Every `SPEC_*.md` in docs/specs has these sections, in this order:
1. Description
2. Goals
3. Phases and Tasks
4. Backlog
5. Status Table

### Other constraints
- Controllers and commands hold no business logic; business logic and storage import nothing from the framework (they stay testable without it).
- All settings live in `env.yml` as `${ENV:default}` placeholders resolved from the process environment: no ad-hoc environment reads in code. Your own keys go under a name of your own and are read through the runtime (`rt.Config.Get("hello.key")`, `rt.Config.Unmarshal("hello", &cfg)`). The framework's keys (`server.*`, `database.*`, `scheduler.*`, `dev.*`) are documented in "GoVibe: Configuration" under /dev/docs.
- Registration (`routes.Register`, `routes.Console`) must not open connections or read secrets: tooling calls it with no database to list what exists (rt.DB is nil then).
- Never register a route under /dev or /_govibe: the framework owns those prefixes, and the app fails to boot if it does. Links to /dev pages sit inside `web.IfDev`, so a production build shows none.
- Every route is named in a doc as `GET /path` (docs/public/ARCHITECTURE.md), every command in backticks (or as typed, `./hello name`); the graph reports the rest.
- A finding is fixed, or accepted in `docs/graph/baseline.json` with a written reason (`go tool govibe graph index --update-baseline --reason "..."`), never silenced.

## Development
- `make dev` runs the app with the dev tools compiled in (`go run -tags govibe_dev .`): the panel at /dev, the docs at /dev/docs, the project graph at /dev/graph, the database explorer at /dev/db (with a DSN), the scheduler at /dev/schedule and the MCP server at /dev/mcp for agents. Without the tag the app is a production build and serves nothing under /dev.
- `make air` does the same with live reload; `make test` and `make check` are what CI runs (`.github/workflows/govibe.yml`). The tools (templ, air, govibe) are pinned in go.mod and run with `go tool`, so everyone runs the same versions.
- Environment: APP_ENV (local, dev, staging, prod; local by default), PORT (8080), LOG_LEVEL, MARIADB_DSN (set it and the app connects and runs its migrations on start). In local and dev the ./assets directory is served from disk with no-cache headers.
- `go tool govibe doctor` reports what is wrong (versions, managed files, specs, the baseline, embeds, a dev package in the default build). After `go get github.com/petars-dev/govibe@<version> && go mod tidy`, `go tool govibe upgrade` rewrites the managed files for that version (it refuses one you edited unless --force, never touches yours) and prints the upgrade notes.
- Runtime commands of the binary: `serve` (the default), `migrate up|down|status`, `routes`, `schedule list|run`, `version`, `inspect --json`, `help`, plus the app's own.

## Adding features
- A page: a .templ file under assets/templates, `go tool templ generate`, a controller that renders it, a route in `routes/routes.go`.
- An API: a business action or service, a controller that calls it, a route.
- A table: a goose SQL file in storage/migrations/sql (the framework applies it on start), a store package under storage, documented in `docs/public/MEMORY_SCHEMA.md`.
- A CLI command: a `govibe.Command` in `routes/console.go` (name it noun:verb) with a thin body in a commands package that calls business code; run it as `./hello noun:verb`. Put it on a schedule with `Schedule: govibe.Cron("0 3 * * *")` or `govibe.Every(10 * time.Minute)`: the scheduler then runs the same body while the app serves, `./hello schedule list` shows when, and a test proves it with `govibetest.Boot(...).Clock.AdvanceTo(...)`. See "GoVibe: Commands and Scheduler".
- Or let the tool do it: `go tool govibe make page|resource|action|service|store|migration|middleware|command|config|spec <name>` writes the files in the right layer, inserts at the marker comments (`// govibe:routes`, `// govibe:commands`; it prints the snippet when a marker is missing) and names the route or command in `docs/public/ARCHITECTURE.md`. `make command <noun:verb> --schedule "<spec>"` adds the schedule and a test on the fake clock.

## Testing
- `govibetest.Boot(t, newApp())` boots the app the way serve does, on a test server, with no database and no dev tools; the scheduler runs on a fake clock the test moves. Prefer request/response tests against the booted app for handlers, and unit tests next to business packages.

## Deployment
- `make build` (`go build -trimpath -ldflags='-s -w' -o hello .`, no dev tag) after `go tool templ generate`. The binary embeds everything; the environment is the only deploy-time input.
- Several instances each run every scheduled command unless `serve --no-schedule` or `scheduler.lock: db` says otherwise.

## Spec-driven development
- Keep `docs/public/ARCHITECTURE.md` (the architecture, the routes) and `docs/public/MEMORY_SCHEMA.md` (every table, index and in-memory store) current. For each feature add docs/specs/SPEC_{FEATURE}.md with the sections above, and update it after each development loop (AI runs included) with progress and deviations.
- In a dev build, /dev/docs/app shows these docs and /dev/docs/specs the specs; the framework's own docs are the "GoVibe:" group.
<!-- govibe:managed:end -->

## This application
This section is yours: the framework never rewrites it. Replace it with what an agent must know about your domain and your extra constraints.

### Hello GoVibe done criteria
The app starts as the Hello GoVibe demo, plain application code in the usual layers:
- UI: `GET /` renders a templ page with a button that triggers htmx to call `GET /hello`.
- API: `GET /api/hello` returns JSON `{"message":"Hello GoVibe!"}`.
- Assets, settings and migrations embedded: the app runs as one binary, and a build without the govibe_dev tag links no dev tooling.

### Starting your own app
Delete the demo's files: `assets/static/govibe-hero.png`, `assets/static/govibe-logo.png`, `assets/static/home.css`, `assets/static/theme.css`, `assets/static/theme.js`, `assets/templates/home.templ`, `business/actions/hello.go`, `controllers/api.go`, `controllers/hello.go`, `controllers/home.go`. Keep the .gitkeep files in assets/static and assets/templates (the embed patterns need something to match). Then replace the three routes in `routes/routes.go` with your own, replace TestHelloGoVibe in `main_test.go`, which asserts the demo, and update `docs/public/ARCHITECTURE.md`. Nothing else refers to the demo. Then delete this list.
