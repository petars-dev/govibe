Hello GoVibe: the rules of a GoVibe application

Overview
This is a GoVibe application: a Go module that imports the framework (github.com/petars-dev/govibe) and contains only application code.
- templ for typesafe server-rendered HTML
- htmx 2.0, vendored in the framework and served at /_govibe/static/htmx.min.js (`web.HTMXScript()` renders the tag) for simple interactivity without node/npm
- chi v5 router with middleware and subroutes
- goose v3 for SQL migrations embedded into the binary
- sqlx over database/sql with the mysql driver (MariaDB compatible)
- All assets embedded for a single deployable binary

Directory Layout
- main.go: the whole boot is one declaration, `govibe.Main(newApp())`; the app hands over its embedded env.yml, migrations and assets and its routes, and the framework runs it
- env.yml: this app's settings (project root, embedded by main.go), layered over the framework defaults
- assets: templates (templ), static files, embed.go to embed them (the dev panel's files and htmx belong to the framework, not here)
- controllers: HTTP handlers that only orchestrate actions/services -> views/API
- routes: the app's route registration (`routes.Register`); the framework builds the router around it
- storage: DB access and migrations (goose SQL files under storage/migrations, embedded)
- business: actions (single-function business logic) and services (packages)
- docs: public docs, specs, and the graph baseline

Key Constraints
- Controllers contain no business logic. They only call actions/services and render responses.
- Only actions/services may call the storage layer.
- Business logic and storage import nothing from the framework; controllers and views import only `govibe/web`; only main.go and routes/ touch `govibe` itself.
- All assets (templates, JS, CSS) and migrations are embedded. The binary is self-contained.
- Never register a route under /dev or /_govibe: the framework owns those prefixes, and the app fails to boot if it does.

Local Development
1. Go version: ensure your environment matches go.mod.
2. Run with the dev tools: `go run -tags govibe_dev .` (the tag compiles the dev panel in, at /dev). Without the tag the app is a production build and serves nothing under /dev.
3. `templ generate` after editing a .templ file.
4. Visit http://localhost:8080: `/` renders the Hello GoVibe page, `/api/hello` returns `{"message":"Hello GoVibe!"}`.

Configuration
- Settings live in `env.yml` with `${ENV:default}` placeholders, resolved from the process environment. Prefer adding keys there, not ad-hoc env lookups in code.
  - APP_ENV: one of local, dev/development, staging, prod/production (defaults to local)
  - PORT: HTTP port (default 8080)
  - MARIADB_DSN: if set, the app connects to MariaDB/MySQL and runs its embedded goose migrations on startup
  - Your own settings: declare them in env.yml under a name of your own, such as `shop.currency`, and read them through the runtime: `rt.Config.Unmarshal("shop", &cfg)` or `rt.Config.Get("shop.currency")`.
- APP_ENV controls behavior across local/dev/staging/prod. In local/dev the app's ./assets directory is served from disk with no-cache headers when it exists.

Adding Features
- New pages: add a .templ file under assets/templates, run `templ generate`, add a controller that renders it, then map the route in routes/routes.go.
- New APIs: add an action/service in business/, call it from a controller in controllers/, add the route.
- DB changes: add a new SQL file in storage/migrations/sql with goose directives; the framework runs it on start.
- New CLI commands: declare a `govibe.Command` in a new file beside `routes/routes.go` (by convention `console.go`) and wire it as `App.Commands` and keep its body thin (call a business action); run it as `./app <name>`. Name it `noun:verb`.
  Put it on a schedule with `Schedule: govibe.Cron("0 3 * * *")` or `govibe.Every(10 * time.Minute)`: the same body then also runs from the framework's scheduler, `./app schedule list` shows when, and
  a test proves it with `govibetest.Boot(...).Clock.AdvanceTo(...)`. See `GoVibe: Commands and Scheduler`. Name every command in a doc (in backticks) or the graph reports it.

Testing
- Add unit tests next to packages using _test.go files. `main_test.go` boots the app with `govibetest.Boot`, without a database and without the dev tools.
- For handlers, prefer request/response tests against the booted app.

Deployment
- Build a single binary without the dev tag: `go build -trimpath -ldflags='-s -w' -o app`
- Ensure `templ generate` was run before building.
- The binary embeds assets, settings and migrations; no external runtime files are required.

Hello GoVibe Done Criteria
- UI: `/` renders a templ page with a button that triggers htmx to call `/hello`.
- API: `/api/hello` returns JSON `{"message":"Hello GoVibe!"}`.
- All assets and migrations embedded; the app runs as one binary, and a build without the govibe_dev tag links no dev tooling.

Spec-Driven Development Guidelines
- Keep human-readable docs in docs/:
  - docs/public/ARCHITECTURE.md: high-level architecture of this app (update on structure changes).
  - docs/public/MEMORY_SCHEMA.md: DB schema and any in-memory storage models (update with each migration/shape change).
  - docs/specs/: spec-driven process artifacts. For each epic/feature/user story, add SPEC_{FEATURE_NAME}.md with:
    1. Description
    2. Goals
    3. Phases and Tasks
    4. Backlog (tasks)
    5. Status Table of implemented vs skipped with rationale
- After each development loop (including AI runs), update the relevant spec with progress and deviations, and ARCHITECTURE.md and/or MEMORY_SCHEMA.md if architecture or data shapes changed.
- In a dev build, browse /dev/docs/app for public docs and /dev/docs/specs for specs.
