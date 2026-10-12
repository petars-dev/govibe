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
The reference app, [`examples/hello`](examples/hello), is exactly this: the Hello GoVibe demo, which every new app will start with.
(`govibe new`, which scaffolds an app from it, is planned. No version of the framework is published yet, so an app outside this repository cannot depend on it
until the first tag; until then, work inside `examples/`, where `go.work` and a `replace` directive point the app at the framework next to it.)

## This repository
- the framework module, `github.com/petars-dev/govibe`: public packages `govibe`, `config`, `web`, `govibetest`; everything else is `internal/`
- the tool, `cmd/govibe`: `govibe graph index` (the offline indexer and CI gate), `govibe generate`, and `govibe serve` / `govibe migrate`,
  which run the app in the current project
- `examples/hello`: the reference app, its own module (`go.work` joins it to the framework)
- `docs`: how it is built (`docs/public/ARCHITECTURE.md`) and the specs that produced it (`docs/specs`)

## Try it
1. `make dev` runs the reference app with the dev tools compiled in (`go run -tags govibe_dev .`): `http://localhost:8080/` is the Hello GoVibe page.
2. Explore the dev panel (it exists only in a build with the `govibe_dev` tag, never in production):
   - Docs: `http://localhost:8080/dev/docs/app` and `/dev/docs/specs`
   - Project graph: `http://localhost:8080/dev/graph`
   - Database explorer: `http://localhost:8080/dev/db` (needs `MARIADB_DSN`; see `docs/public/DEV_DB_EXPLORER.md`)
   - MCP server for local agents: `http://localhost:8080/dev/mcp`
3. `make test` and `make test-dev` run the tests without and with the dev tools; `make graph-check` and `make graph-check-app` are the CI gates.
4. `make build` builds the reference app as a production binary (no dev tag): one file, no dev tooling linked.

## Configuration
- An app's `env.yml` (its project root, embedded by `main.go`) is layered over the framework's defaults; values are `${ENV:default}` placeholders
  resolved from the environment, e.g. `${PORT:8080}`. Your own settings go under a name of your own and are read with `rt.Config.Get` or `rt.Config.Unmarshal`.
- `APP_ENV` controls behavior: local, dev, staging, prod/production. In local and dev the app's `./assets` directory is served from disk with no-cache headers.
- `DATABASE_TYPE` (default `mysql`) selects the goose dialect; it is always passed explicitly, because goose defaults to postgres.
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
