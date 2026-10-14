# Upgrading GoVibe

GoVibe follows semantic versions. Before `v1.0.0` a minor version may change the public surface (the packages `govibe`, `config`, `web` and
`govibetest`), and every such change is listed here with what to do about it. Everything outside those four packages is `internal/` and can change
in any release.

## How to upgrade
1. Bump the framework: `go get github.com/petars-dev/govibe@<version>`, then `go mod tidy`.
2. Rebuild and run the tests, with and without the dev tag (`go build ./... && go build -tags govibe_dev ./...`).
3. Run the gate: `govibe graph index --fail-on error --baseline docs/graph/baseline.json`. The tool and the library are one version; run the tool the
   `go.mod` pins.

Files the framework generates for you are rewritten by `govibe upgrade` (planned); files that are yours (`env.yml`, `README.md`, every Go file, your docs)
are never touched.

## Unreleased
- The module path is `github.com/petars-dev/govibe` (it was `petars-dev/govibe`, which is not fetchable).
- An application is a module of its own that calls `govibe.Main(govibe.App{...})`; `routes.NewRouter` and the boot code are gone from application code.
- `env.yml` is in the project root and embedded by `main.go`; the framework's defaults are `config/defaults.yml`, layered under it. Settings for mail and WorkOS
  were removed: nothing read them.
- The dev tools are compiled in only with the `govibe_dev` build tag; a default build contains none of them.
- The graph is about your app by default (`scope=app`); pass `scope=framework` or `scope=all` to the graph tools, or use the switch on `/dev/graph`.
- `govibe.yml` is the project profile (see `GoVibe: Configuration`). Baseline entries need a `reason`: add one to each entry of `docs/graph/baseline.json`, or
  regenerate it with `--update-baseline --reason`.
- New rule `framework.import` (error): business logic and storage import nothing from the framework; controllers, middleware and views import only the framework's `web` package;
  commands import the root `govibe` package; wiring imports the root package, `config` and `web`.
