# GoVibe Guide

GoVibe is a Go web framework you depend on, not a skeleton you fork. An application is its own Go module: it holds only application code
(controllers, business logic, storage, templ views, routes, `env.yml`, docs) and hands the framework a declaration; the framework boots it,
serves it and gives it a dev panel, a project graph and an MCP server for agents.

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
	}
}

func main() { govibe.Main(newApp()) }
```
Every field is optional. The framework calls `Routes` with a chi router that already carries the standard middleware and the asset mounts, so
the app keeps using plain chi. Registration must not open connections or read secrets: tooling calls it with no database to list what exists
(`rt.DB` is nil then).

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
  project graph at `/dev/graph`, the database explorer at `/dev/db` (with a DSN) and the MCP server at `/dev/mcp`.
- A default build (no tag) contains none of it: the dev stack is not linked, `/dev/*` answers 404, and pages that link to the panel render
  no link (wrap such links in `web.IfDev`).
- `./app routes`, `./app version`, `./app migrate up|down|status` and `./app inspect --json` are part of every app binary and work in production.

## The dev panel is about your app
The graph, the findings, the reports, the docs and the agent tools show your app's own code and docs by default. The framework it runs on is
one switch away (the scope: **My app**, **Framework**, **All**) and always labelled. Your docs come first in `/dev/docs`; the framework's
reference docs are a separate group, every title prefixed `GoVibe:`.

## Where to read next
- `GoVibe: Configuration` for `env.yml`, the framework's settings and how they layer.
- `GoVibe: Architecture` for how the framework is built.
- `GoVibe: Graph Schema` for the vocabulary of the project graph and the agent tools.
- `GoVibe: Dev DB Explorer` for the database explorer.
- `GoVibe: Upgrading` for what changes between versions.
