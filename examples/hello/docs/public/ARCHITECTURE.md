# Hello GoVibe: architecture

The framework's reference app. It is an ordinary GoVibe application: a module of its own that imports the
framework (`github.com/petars-dev/govibe`) and contains only application code. Every new app starts as a copy of it.

## What it declares
`main.go` hands the framework what the app contributes and the framework runs it: the settings in `env.yml` (embedded),
the migrations in `storage/migrations` (embedded), the static files in `assets` (embedded) and the routes in
`routes/routes.go`. `main.go` never boots anything itself.

## Layers
- `routes/routes.go`: maps paths to handlers. It is the wiring layer: construct dependencies here as the app grows.
- `controllers/`: thin HTTP handlers (`home.go`, `api.go`, `hello.go`) that call business actions and render a view or JSON.
- `business/actions/`: single-function business logic (`hello.go`).
- `assets/templates/`: the templ page (`home.templ`), rendered server-side; `assets/static/`: its stylesheet, script and images.
- `storage/migrations/`: goose SQL migrations, embedded; this app has no tables yet.

## Routes
- `GET /`: the landing page, with a button that uses htmx to call `GET /hello`.
- `GET /api/hello`: JSON `{"message":"Hello GoVibe!"}`.
- `GET /hello`: the same greeting as plain text, for htmx swaps.

## Routes the framework adds
These are not the app's; they are listed so nobody registers over them (an app that registers under `/dev` or `/_govibe` fails to boot).
- `ANY /assets/*`: the app's own static files (`assets/static`), from disk in local and dev, embedded otherwise.
- `GET /_govibe/static/htmx.min.js`: the htmx build the framework ships; the page loads it with `web.HTMXScript()`.
- `ANY /_govibe/static/*`: in a dev build, the dev panel's own files (stylesheets, scripts, the graph renderer).
- `/dev/*`: in a dev build, the dev panel (see below).

## Dev tooling
A build with the `govibe_dev` tag (`make dev` from the framework repository, or `go run -tags govibe_dev .` here) mounts the
dev panel: `/dev/graph`, `/dev/docs/app`, `/dev/db` and `/dev/mcp`. A default build contains none of it, and the page's links to
`/dev` appear only when the panel is mounted.
