# GoVibe Guide

GoVibe is a Go web framework you depend on, not a skeleton you fork. An application is its own Go module: it holds only application code
(controllers, business logic, storage, templ views, routes, `env.yml`, docs) and hands the framework a declaration; the framework boots it,
serves it and gives it a dev panel, a project graph and an MCP server for agents.

## Starting an app
```sh
go install github.com/petars-dev/govibe/cmd/govibe@latest   # once, for `new`
govibe new github.com/acme/shop && cd shop && make dev          # http://localhost:8080, the dev panel at /dev
```
`govibe new` writes a module of your own that requires the framework at the tool's version, runs `go mod tidy`, `go tool templ generate`, `git init`
with a first commit and `go tool govibe check` (which finds nothing in a new app). It starts as the Hello GoVibe demo (`GET /`, `GET /api/hello`,
`GET /hello`): plain app code in the usual layers that you keep as a smoke test or delete when you build your own; `SKILL.md` says what to delete.
The tools (templ, air and the framework's own `govibe`) are pinned in `go.mod` and run with `go tool`, so the tool that judges your project is
the version it builds against. `--db mariadb` declares the database in `env.yml`; `--replace <checkout>` builds against a checkout of the framework.

A few files are **managed**: the Makefile, .air.toml, the workflow under .github/workflows and the block of SKILL.md between the `govibe:managed`
markers. They carry a hash in `govibe.yml`, so an upgrade can rewrite them and knows when you edited one. Everything else is yours and is never
touched again.

## Adding things
`go tool govibe make` writes code in the right layer and registers it: a route or a command at the marker comments in the wiring, a line in
your architecture doc, so the gate stays green. A file that exists is never overwritten (`--force`), and when a marker is missing the snippet is
printed instead of guessed at.
```sh
go tool govibe make page about                       # a templ page, its controller, GET /about
go tool govibe make resource order                   # migration, store, service, JSON controller, GET /api/orders and /api/orders/{id}, docs
go tool govibe make command items:prune --schedule "0 3 * * *" --short "Delete archived items"   # body, declaration, doc line, a test on the fake clock
go tool govibe make action notify | service billing | store invoice | migration add_status --table invoices | middleware rate-limit
go tool govibe make config                           # a typed struct for the "<app>" section of env.yml
go tool govibe make spec payments                    # a spec with the required sections
```

## The declaration
```go
//go:embed env.yml
var configFS embed.FS

func newApp() govibe.App {
	return govibe.App{
		Name:       "shop",
		Config:     configFS,         // env.yml, layered over the framework's defaults
		Migrations: migrations.FS,    // embedded goose SQL
		Assets:     assets.FS,        // embedded static files, served at /assets
		Routes:     routes.Register,  // func(rt *govibe.Runtime, r chi.Router) error
		Commands:   routes.Console,   // func(rt *govibe.Runtime) []govibe.Command: CLI commands, some of them scheduled
	}
}

func main() { govibe.Main(newApp()) }
```
Every field is optional. The framework calls `Routes` with a chi router that already carries the standard middleware and the asset mounts, so
the app keeps using plain chi. Registration must not open connections or read secrets: tooling calls it with no database to list what exists
(`rt.DB` is nil then).

## Commands and the scheduler
A CLI command is one `govibe.Command`, declared in a `console.go` beside your routes; give it a `Schedule` (`govibe.Cron("0 3 * * *")`, `govibe.Every(10 * time.Minute)`) and the framework also runs it, while
the app is up. The same function serves `./app items:prune` and the scheduled run. `./app schedule list` shows what is scheduled and when it is next due, with no database; `serve --no-schedule`
keeps the scheduler off an instance; `./app schedule run` runs it alone as a worker. See `GoVibe: Commands and Scheduler`.

## Layers
```text
controllers, commands  ->  business/{actions,services}  ->  storage/<store>
```
- Controllers are thin adapters: they parse the request, call a business action or service and render a view. They import only the framework's `web` package.
- Business logic is plain Go and imports nothing from the framework. Only business code calls storage.
- Storage holds one package per store; a store takes a `*sqlx.DB` from its constructor and imports nothing from the framework.
- `main.go` and `routes/` are the wiring: they may import anything, and construct the dependencies.
- The rules are data (`GoVibe: Rules`), the graph's findings enforce them (`govibe graph index --fail-on error`), and a package that breaks
  them is a finding that names the import and where it is written.

## What you run
- `make dev` (or `go run -tags govibe_dev .`) runs the app with the dev tools compiled in: the panel at `/dev`, the docs at `/dev/docs`, the
  project graph at `/dev/graph`, the database explorer at `/dev/db` (with a DSN), the scheduler at `/dev/schedule` and the MCP server at `/dev/mcp`.
- A default build (no tag) contains none of it: the dev stack is not linked, `/dev/*` answers 404, and pages that link to the panel render
  no link (wrap such links in `web.IfDev`).
- `./app routes`, `./app version`, `./app migrate up|down|status`, `./app schedule list|run` and `./app inspect --json` are part of every app binary and work in production, and so
  is every command you declare.
- `make check` (`go tool govibe check`) is what CI runs: the managed files against their manifest, the doctor's static checks, then the project
  graph's findings against `docs/graph/baseline.json`, in a scratch store. A finding is fixed, or accepted in the baseline with a reason.
- `go tool govibe doctor` says what is wrong before it costs time: the tool against the pinned version, managed files, specs missing a section, a
  baseline entry without a reason, a script loaded from the network, and (building the app) `inspect --json` with no environment, files not handed
  to the framework, a dev package in the default build.
- `go tool govibe upgrade`, after `go get github.com/petars-dev/govibe@<version> && go mod tidy`: the managed files are rewritten for the new
  version with their diffs shown, one you edited is refused unless `--force`, your own files are never touched, and the upgrade notes are printed.

## The dev panel is about your app
The graph, the findings, the reports, the docs and the agent tools show your app's own code and docs by default. The framework it runs on is
one switch away (the scope: **My app**, **Framework**, **All**) and always labelled. Your docs come first in `/dev/docs`; the framework's
reference docs are a separate group, every title prefixed `GoVibe:`.

## Where to read next
- `GoVibe: Configuration` for `env.yml`, the framework's settings and how they layer.
- `GoVibe: Commands and Scheduler` for declaring a command, scheduling it, and testing both.
- `GoVibe: Architecture` for how the framework is built.
- `GoVibe: Graph Schema` for the vocabulary of the project graph and the agent tools.
- `GoVibe: Dev DB Explorer` for the database explorer.
- `GoVibe: Upgrading` for what changes between versions.
