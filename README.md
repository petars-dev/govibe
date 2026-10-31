# GoVibe

[![ci](https://github.com/petars-dev/govibe/actions/workflows/ci.yml/badge.svg)](https://github.com/petars-dev/govibe/actions/workflows/ci.yml)
[![graph](https://github.com/petars-dev/govibe/actions/workflows/graph.yml/badge.svg)](https://github.com/petars-dev/govibe/actions/workflows/graph.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/petars-dev/govibe.svg)](https://pkg.go.dev/github.com/petars-dev/govibe)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

GoVibe is a pragmatic Go web framework for the AI era. An application is a small Go module that depends on it and holds only
application code; the framework runs it. It ships with a dev panel, a queryable graph of your project and an MCP server, so the
agents working on your code can see what they are changing, and a gate that enforces the project's own rules in CI.

- **Server-rendered HTML** with [templ] and progressive interactivity with [htmx] (served by the framework; no Node)
- **A small stack**: [chi] for routing, [sqlx] over `database/sql`, plain SQL migrations with [goose], all embedded in **one binary**
- **Explicit layering** (controllers and commands → actions and services → storage), written down in your app's `SKILL.md` and checked by the graph
- **CLI commands and a scheduler**: a command is one declaration; a `Schedule` on it and the framework runs it while the app serves
- **A dev panel about your app**: `/dev` with your docs, the project graph and its findings, the database explorer, the scheduler, and
  `/dev/mcp` for local agents; compiled in only with a build tag, so production contains none of it
- **Tooling that knows the project**: `govibe new`, `govibe make`, `govibe check`, `govibe doctor`, `govibe upgrade`

## Quick start
```sh
go install github.com/petars-dev/govibe/cmd/govibe@latest
govibe new github.com/acme/shop
cd shop && make dev
```
`http://localhost:8080` is the Hello GoVibe page, `http://localhost:8080/dev` the dev panel. The new app's `SKILL.md` holds the rules it
follows and what to delete to start your own. Inside the app the tool runs as `go tool govibe`, pinned in `go.mod` next to the framework.

## What an app looks like
```go
//go:embed env.yml
var configFS embed.FS

func main() {
	govibe.Main(govibe.App{
		Name:       "shop",
		Config:     configFS,         // env.yml, layered over the framework's defaults
		Migrations: migrations.FS,    // embedded goose SQL
		Assets:     assets.FS,        // embedded static files, served at /assets
		Routes:     routes.Register,  // func(rt *govibe.Runtime, r chi.Router) error
		Commands:   routes.Console,   // func(rt *govibe.Runtime) []govibe.Command, some of them scheduled
	})
}
```
Every field is optional. The framework never imports the app, and the app imports only four packages of the framework: `govibe`,
`config`, `web` and (in tests) `govibetest`. Business logic and storage import nothing from it. The reference app,
[`examples/hello`](examples/hello), is exactly this.

```sh
go tool govibe make resource order                                   # migration, store, service, JSON controller, routes, docs
go tool govibe make command items:prune --schedule "0 3 * * *"       # a scheduled command and a test on a fake clock
go tool govibe check                                                 # the gate CI runs: zero findings in a new app
./shop schedule list                                                 # what is scheduled and when it runs next, no database needed
```

## Documentation
- [Guide](docs/public/GUIDE.md): starting an app, the declaration, adding things, your own dev tools, layers, what you run
- [Configuration](docs/public/CONFIGURATION.md): `env.yml`, the framework's keys, the project profile `govibe.yml`
- [Commands and Scheduler](docs/public/COMMANDS_AND_SCHEDULER.md)
- [Upgrading](docs/public/UPGRADING.md): the versioning policy and the notes per version
- [Architecture](docs/public/ARCHITECTURE.md): how the framework is built; [Graph Schema](docs/public/GRAPH_SCHEMA.md) and
  [Dev DB Explorer](docs/public/DEV_DB_EXPLORER.md) for the dev tooling
- API reference on [pkg.go.dev](https://pkg.go.dev/github.com/petars-dev/govibe)

In a dev build the same docs are served to you under `/dev/docs/govibe` and to agents as MCP resources; your own docs come first.

## Status
Pre-1.0: the public surface is four packages and one struct on purpose, and a minor version may still change it, with a note in
[Upgrading](docs/public/UPGRADING.md). Releases are tags on `main`; the latest is on the
[releases page](https://github.com/petars-dev/govibe/releases).

## This repository
- the framework module, `github.com/petars-dev/govibe`: the public packages `govibe`, `config`, `web`, `govibetest`; everything else is `internal/`
- the tool, `cmd/govibe`
- `examples/hello`: the reference app and the demo every new app starts with (its own module; `go.work` joins it to the framework)
- `docs/public`: the framework's docs; `docs/specs`: the specs that produced it (never shipped to apps)

`make dev` runs the reference app with the dev tools compiled in; `make test-short` is the quick loop, `make test` and `make test-dev` run the full suite both ways; `make check` adds
the graph gates; `make keystone` scaffolds an app from the working tree and exercises it the way CI does. Working on the framework itself:
[CONTRIBUTING.md](CONTRIBUTING.md) and [SKILL.md](SKILL.md). Reporting a vulnerability: [SECURITY.md](SECURITY.md).

## License
[Apache-2.0](LICENSE).

[templ]: https://templ.guide
[htmx]: https://htmx.org
[chi]: https://github.com/go-chi/chi
[sqlx]: https://github.com/jmoiron/sqlx
[goose]: https://github.com/pressly/goose
