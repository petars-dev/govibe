# GoVibe

GoVibe is a pragmatic Go web framework skeleton for the new AI era. It embraces:

- Server-rendered HTML with [templ] and progressive interactivity via vendored [htmx]
- Minimal router ([chi]) and thin DB access ([sqlx] over `database/sql`)
- Plain SQL migrations ([goose]) embedded into the binary
- Single deployable binary embedding assets and migrations (no Node runtime)
- Explicit layering (controllers -> actions/services -> storage) designed for AI-assisted changes

Why GoVibe
- Deterministic, typed templates: mistakes are compile-time errors with templ
- No bundlers or duplicated state: HTML is the source of truth, enhanced by htmx
- Clear seams: business logic isolated in actions/services; controllers orchestrate
- Reproducible deploys: everything embedded; configure via env.yml placeholders

Quick Start
1. Install templ and tidy deps:
   - `go install github.com/a-h/templ/cmd/templ@latest`
   - `make tidy templ`
2. Run locally:
   - `make dev` (APP_ENV=dev, LOG_LEVEL=debug)
   - Visit `http://localhost:8080/`
3. Explore docs:
   - App docs: `http://localhost:8080/dev/docs/app`
   - Specs: `http://localhost:8080/dev/docs/specs`
   - Graph store: `http://localhost:8080/dev/graph`
   - Database explorer: `http://localhost:8080/dev/db` (dev-only; see
     `docs/public/DEV_DB_EXPLORER.md`). An MCP server for local agents is mounted at
     `http://localhost:8080/dev/mcp`.

CLI (govibe)
- Build the CLI: `go build -o govibe ./cmd/govibe`
- Usage: `./govibe`
  - Serve: `./govibe serve` (runs the application with `go run . serve`; port and env come from `env.yml` and the environment)
  - Generate templ code: `./govibe generate`
  - Migrations: `./govibe migrate up`, `./govibe migrate down`, `./govibe migrate status`
  - Extending CLI: add `cmd_*.go` under `cmd/govibe/`, define a `cobra.Command`, and register it in `init()`.

Configuration
- Centralized in `env.yml` (project root, embedded by `main.go`) over the framework defaults in `config/defaults.yml`; values are resolved from the environment at runtime (e.g. `${PORT:8080}`).
- APP_ENV controls behavior: local, dev, staging, prod/production.
- In local/dev, static assets are served from `local/assets` or `assets` under the application root if present (fallback to embedded assets) and are returned with no-cache headers for fast iteration.
- Add new settings by editing `env.yml`. Access via typed `config.Config` or dynamic `rt.Config.Unmarshal("path", &v)` / `rt.Config.Get("path.key")`.
- `DATABASE_TYPE` (default `mysql`) selects the goose migration dialect. Goose defaults to postgres if never told otherwise, which silently breaks migrations against any other database, so this is always set explicitly — see `github.com/pressly/goose/v3` for other supported values.
- `DEV_DB_ALLOW_MUTATIONS` (default `false`) enables the `/dev/db` and MCP test-data mutation tools (`db_insert_test_row`, `db_reset_test_data`). Never takes effect in production.

Development Loop with AI
- For full live-reload in dev, use Air (single tool):
  - `make air` (installs air if needed) — watches .go/.templ/.css/.js, runs templ generate, rebuilds and restarts server.
- After each development loop, update:
  - `docs/public/ARCHITECTURE.md` or `docs/public/MEMORY_SCHEMA.md` if structure or schema changed.
  - Relevant `docs/specs/SPEC_{FEATURE}.md` with current status and deviations from plan.

Build
- `make build` produces a single binary `govibe` with embedded assets/migrations.

[templ]: https://templ.guide
[htmx]: https://htmx.org
[chi]: https://github.com/go-chi/chi
[sqlx]: https://github.com/jmoiron/sqlx
[goose]: https://github.com/pressly/goose
