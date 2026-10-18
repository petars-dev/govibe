# GoVibe

GoVibe is a pragmatic Go web framework for the AI era. An application is a small Go module that depends on it and contains only
application code; the framework runs it. It embraces:

- Server-rendered HTML with [templ] and progressive interactivity via vendored [htmx]
- A minimal router ([chi]) and thin DB access ([sqlx] over `database/sql`)
- Plain SQL migrations ([goose]) embedded into the binary
- A single deployable binary embedding assets, settings and migrations (no Node runtime)
- Explicit layering (controllers -> actions/services -> storage) designed for AI-assisted changes, with a dev panel, a project graph and an MCP server
  that let an agent see the code it is changing

## What an app looks like
```go
//go:embed env.yml
var configFS embed.FS

func main() {
	govibe.Main(govibe.App{
		Name:       "shop",
		Config:     configFS,         // env.yml, layered over the framework defaults
		Migrations: migrations.FS,    // embedded goose SQL
		Assets:     assets.FS,        // embedded static files, served at /assets
		Routes:     routes.Register,  // func(rt *govibe.Runtime, r chi.Router) error
	})
}
```
The framework never imports the app, and the app imports only four packages: `govibe`, `config`, `web` and (in tests) `govibetest`.
The reference app, [`examples/hello`](examples/hello), is exactly this: the Hello GoVibe demo, which every new app starts with.

## Start an app
```sh
go install github.com/petars-dev/govibe/cmd/govibe@latest
govibe new github.com/acme/shop && cd shop && make dev
```
`govibe new` writes a module of your own that depends on the framework, runs `go mod tidy`, `templ generate`, a first commit and `govibe check`,
and `make dev` serves the Hello GoVibe page at `http://localhost:8080` with the dev panel at `/dev`. The app's `SKILL.md` holds the rules and what to
delete to start your own. (No version of the framework is published yet: until the first tag, create apps against this checkout with
`make new MODULE=github.com/acme/shop DIR=../shop`, which passes `--replace`.)

## This repository
- the framework module, `github.com/petars-dev/govibe`: public packages `govibe`, `config`, `web`, `govibetest`; everything else is `internal/`
- the tool, `cmd/govibe`: `govibe new` (a new application), `govibe make` (generators: page, resource, action, service, store, migration, middleware,
  command, config, spec), `govibe check` (what an app's CI runs), `govibe doctor`, `govibe upgrade` (the managed files for a new version), `govibe graph index`
  (the offline indexer and gate), `govibe generate`, and `govibe serve` / `govibe migrate`, which run the app in the current project; an app runs it as `go tool govibe`
- `examples/hello`: the reference app, its own module (`go.work` joins it to the framework)
- `docs`: how it is built (`docs/public/ARCHITECTURE.md`) and the specs that produced it (`docs/specs`)

## Try it
1. `make dev` runs the reference app with the dev tools compiled in (`go run -tags govibe_dev .`): `http://localhost:8080/` is the Hello GoVibe page.
2. Explore the dev panel (it exists only in a build with the `govibe_dev` tag, never in production):
   - The front door, `http://localhost:8080/dev`: the app, what is mounted and what to do first
   - Docs: `http://localhost:8080/dev/docs/app` and `/dev/docs/specs` come first (your docs and specs); the framework's reference docs are a separate group under `/dev/docs/govibe`, every title prefixed `GoVibe:`
   - Project graph: `http://localhost:8080/dev/graph` shows the app's own routes, code, tables and docs by default; the switch (My app, Framework, All) shows the framework it runs on, labelled
   - Database explorer: `http://localhost:8080/dev/db` (needs `MARIADB_DSN`; see `docs/public/DEV_DB_EXPLORER.md`)
   - Scheduler: `http://localhost:8080/dev/schedule` (scheduled commands, their next run and history, and a "Run now" button; see `docs/public/COMMANDS_AND_SCHEDULER.md`)
   - MCP server for local agents: `http://localhost:8080/dev/mcp`
3. `make test` and `make test-dev` run the tests without and with the dev tools; `make graph-check` and `make graph-check-app` are the CI gates;
   `make keystone` scaffolds an app from the working tree and exercises it the way the `ci` workflow does.
4. `make build` builds the reference app as a production binary (no dev tag): one file, no dev tooling linked.

## Configuration
- An app's `env.yml` (its project root, embedded by `main.go`) is layered over the framework's defaults; values are `${ENV:default}` placeholders
  resolved from the environment, e.g. `${PORT:8080}`. Your own settings go under a name of your own and are read with `rt.Config.Get` or `rt.Config.Unmarshal`.
- `APP_ENV` controls behavior: local, dev, staging, prod/production. In local and dev the app's `./assets` directory is served from disk with no-cache headers.
- `DATABASE_TYPE` (default `mysql`) selects the goose dialect; it is always passed explicitly, because goose defaults to postgres.
- `govibe.yml` in the project root is the project profile: where the tools find settings, migrations and docs, which directory is which architecture layer, and per-rule switches (see `docs/public/CONFIGURATION.md`).
- `DEV_DB_ALLOW_MUTATIONS` (default `false`) enables the `/dev/db` and MCP test-data mutation tools. It never takes effect in production.

## Development loop with AI
- `make air` runs the reference app with live reload (Air), rebuilding when the framework or the app changes.
- After each change, update `docs/public/ARCHITECTURE.md` and the relevant `docs/specs/SPEC_{FEATURE}.md` with the status and any deviation. `SKILL.md` is the
  contributor guide; the rules an application follows are in `examples/hello/SKILL.md`.

[templ]: https://templ.guide
[htmx]: https://htmx.org
[chi]: https://github.com/go-chi/chi
[sqlx]: https://github.com/jmoiron/sqlx
[goose]: https://github.com/pressly/goose
