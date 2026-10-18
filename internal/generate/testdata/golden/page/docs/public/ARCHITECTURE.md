# shop: architecture

A GoVibe application: a module of its own that imports the framework (`github.com/petars-dev/govibe`) and contains only
application code. Update this document when the structure changes; every route is named here.

## What it declares
`main.go` hands the framework what the app contributes and the framework runs it: the settings in `env.yml` (embedded),
the migrations in `storage/migrations` (embedded), the static files in `assets` (embedded), the routes in
`routes/routes.go` and the commands in `routes/console.go`. `main.go` never boots anything itself.

## Layers
- `routes/routes.go` and `routes/console.go`: map paths to handlers, and declare commands. They are the wiring layer: construct dependencies here as the app grows.
- `controllers/`: thin HTTP handlers (`home.go`, `api.go`, `hello.go`) that call business actions and render a view or JSON.
- `business/actions/`: single-function business logic (`hello.go`); services, as packages, go next to it.
- `assets/templates/`: the templ page (`home.templ`), rendered server-side; `assets/static/`: its stylesheet, script and images.
- `storage/migrations/`: goose SQL migrations, embedded; this app has no tables yet (see `MEMORY_SCHEMA.md`).

## Routes
- `GET /`: the landing page, with a button that uses htmx to call `GET /hello`.
- `GET /api/hello`: JSON `{"message":"Hello GoVibe!"}`.
- `GET /hello`: the same greeting as plain text, for htmx swaps.
- `GET /about`: the about page.

## Commands
None yet. A command declared in `routes/console.go` is named here, in backticks, with what it does and when it is scheduled.

## Routes the framework adds
These are not the app's; they are listed so nobody registers over them (an app that registers under `/dev` or `/_govibe` fails to boot).
- `ANY /assets/*`: the app's own static files (`assets/static`), from disk in local and dev, embedded otherwise.
- `GET /_govibe/static/htmx.min.js`: the htmx build the framework ships; the page loads it with `web.HTMXScript()`.
- `ANY /_govibe/static/*`: in a dev build, the dev panel's own files.
- `/dev/*`: in a dev build, the dev panel: `/dev/graph`, `/dev/docs`, `/dev/db`, `/dev/schedule`, `/dev/mcp`.

## Dev tooling
A build with the `govibe_dev` tag (`make dev`, or `go run -tags govibe_dev .`) mounts the dev panel at `/dev`. A default
build contains none of it, and the page's links to `/dev` appear only when the panel is mounted.
