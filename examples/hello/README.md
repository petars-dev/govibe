# Hello GoVibe

The framework's reference app and the demo every new GoVibe app starts with. It is plain application code, in its own module
that depends on the framework: three handlers, an action, a templ page with its own static files and the three routes below.
Keep it as a smoke test, or delete it when you build your own.

- `GET /` renders the landing page, with a button that uses htmx to call `/hello`.
- `GET /api/hello` returns `{"message":"Hello GoVibe!"}`.
- `GET /hello` returns the same greeting as plain text.

## Run
From the framework repository: `make dev`. In this directory: `go run -tags govibe_dev .` (the tag mounts the dev panel at
`/dev`; without it the app is the production build, with no dev tooling). `go test ./...` boots the app through `govibetest`.

## Done criteria
- UI: `/` renders a templ page with a button that triggers htmx to call `/hello`.
- API: `/api/hello` returns JSON `{"message":"Hello GoVibe!"}`.
- Assets, settings and migrations are embedded: the app is one binary.
- A build without the `govibe_dev` tag links no dev tooling and serves nothing under `/dev`.

## To start your own app
Delete `controllers/home.go`, `controllers/api.go`, `controllers/hello.go`, `business/actions/hello.go`, `assets/templates/home.templ` (and its generated
`home_templ.go`) and the files in `assets/static` except `.gitkeep` (keep the `.gitkeep` files in `assets/static` and `assets/templates`: the embed patterns in
`assets/embed.go` need something to match). Then replace the three routes in `routes/routes.go` with your own, and replace `TestHelloGoVibe` in `main_test.go`, which
asserts the demo. Nothing else refers to the demo. (The app builds without it: `go build ./...` passes with those files gone.)

This directory is built against the framework next to it (`replace ../..` in `go.mod`, joined by the repository's `go.work`), because no version of the framework
is published yet. An app outside this repository cannot depend on the framework until the first tag; `govibe new` will generate one against a published version.
