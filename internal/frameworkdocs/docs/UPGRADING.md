# Upgrading GoVibe

## Versioning policy
GoVibe follows [semantic versioning](https://semver.org). The public surface is four packages, `govibe`, `config`, `web` and `govibetest`, plus what
a generated app is given (the managed files, the CLI of the tool and the runtime commands of an app binary, the `inspect --json` document's schema,
the settings in `config/defaults.yml`). Everything under `internal/` can change in any release.

- **Before `v1.0.0`**, a minor version (`v0.2.0`, `v0.3.0`) may change the public surface. Every such change is listed below under its version with what
  to do about it, and `go tool govibe upgrade` prints the notes of the versions you cross. A patch version (`v0.1.1`) changes behaviour only to fix it.
- **From `v1.0.0`**, the public surface changes only with a major version; a minor adds, a patch fixes.
- **One version.** The tool and the library are released together and must match: `go.mod` pins both (`require` and `tool`), `govibe doctor` warns
  when they differ, and a global `govibe` of another version hands its commands to the pinned one.
- **Dependencies.** The Go version a generated app gets is the framework's own (`go 1.25.0`); the tools it pins (templ, air) are the versions this
  release tested; htmx is served by the framework and its version is per release.
- **Releases** are git tags on `main`, each with a section below and a GitHub release that carries it. The `ci` workflow checks the API against the
  last tag with `gorelease` before a release is cut.

## How to upgrade
1. Bump the framework: `go get github.com/petars-dev/govibe@<version>`, then `go mod tidy`.
2. Rebuild and run the tests, with and without the dev tag (`go build ./... && go build -tags govibe_dev ./...`).
3. Run the gate: `go tool govibe check`. The tool and the library are one version: `go.mod` pins the tool next to the framework, so bump them
   together (`go get github.com/petars-dev/govibe@<version> github.com/petars-dev/govibe/cmd/govibe@<version>`).

4. Run `go tool govibe upgrade`. Files the framework generates for you (the Makefile, .air.toml, the workflow under .github/workflows, the managed block of
   SKILL.md; their hashes are in `govibe.yml`) are rewritten for the new version, each with its diff shown; one you edited is refused unless `--force`;
   files that are yours (`env.yml`, `README.md`, every Go file, your docs, the rest of SKILL.md) are never touched. The notes below for the versions you
   crossed are printed. `--dry-run` shows the plan first.

## Unreleased
- Nothing yet.

## v0.1.1
- The scheduler's fake clock no longer polls for a finished run between timers: the runner wakes it. Tests that advance a `govibetest` clock over
  many due times are faster, and the scheduler's own test of that no longer fails on a slow CI runner (it took a millisecond per due time there).
  Nothing to do; no public surface changed.

## v0.1.0
The first release: the framework as a dependency, with the Hello GoVibe demo as every new app's start. Nothing to upgrade from; the notes record what
the extraction changed for anyone who was tracking the repository before it.
- An app can add its own MCP tools and pages to the dev panel: `App.DevTools` (`func(rt *govibe.Runtime, dev *govibe.Dev) error`), with `dev.Tool`
  (`govibe.DevToolFunc` for typed arguments) and `dev.Page`; the pages are served under /dev/app and the tools on /dev/mcp, both only in a `govibe_dev`
  build. Declare it in a tagged file (see "GoVibe: Guide"). `inspect --json` reports `contributes.dev_tools`; the schema number is unchanged. Existing apps
  need nothing.
- A globally installed `govibe` run inside an app that pins another version hands every command but `new`, `version` and `help` to `go tool govibe`
  (`GOVIBE_SHIM=off` keeps the global one); `govibe version` prints both. Nothing to do.
- `govibe make <kind> <name>` generates a page, a resource (table, store, service, JSON controller, routes, docs), an action, a service, a store, a migration, a
  middleware, a command (`--schedule` adds the schedule and a test on the fake clock), a typed config (`make config`) or a spec, in the layers and at the markers
  a generated app has (`// govibe:routes`, `// govibe:commands`; the snippet is printed when a marker is missing). `govibe upgrade` rewrites the managed files for
  the version go.mod pins and prints these notes; `govibe doctor` checks the project's health, and `govibe check` runs its static checks before the gate. An existing
  app needs nothing: add the two markers to use the generators' insertion, and a `## Routes`, `## Commands` section in docs/public/ARCHITECTURE.md and a
  `## Tables` section in docs/public/MEMORY_SCHEMA.md to have them documented for you.
- `govibe new <module>` creates an application (the Hello GoVibe demo, in a module of its own that requires the framework); `govibe check` runs the
  managed-file check and the graph gate in a scratch store, and is what a generated app's workflow and `make check` run. `govibe.yml` gained a `managed`
  section (the manifest of generated files); an app that was not generated has none and needs none.
- A generated app declares `Commands: routes.Console` from a routes/console.go that starts empty, with a `// govibe:commands` marker (and a
  `// govibe:routes` marker in routes/routes.go) where the generators of `govibe make` will insert. An existing app needs neither.
- The module path is `github.com/petars-dev/govibe` (it was `petars-dev/govibe`, which is not fetchable).
- An application is a module of its own that calls `govibe.Main(govibe.App{...})`; `routes.NewRouter` and the boot code are gone from application code.
- `env.yml` is in the project root and embedded by `main.go`; the framework's defaults are `config/defaults.yml`, layered under it. Settings for mail and WorkOS
  were removed: nothing read them.
- The dev tools are compiled in only with the `govibe_dev` build tag; a default build contains none of them.
- The graph is about your app by default (`scope=app`); pass `scope=framework` or `scope=all` to the graph tools, or use the switch on `/dev/graph`.
- `govibe.yml` is the project profile (see `GoVibe: Configuration`). Baseline entries need a `reason`: add one to each entry of `docs/graph/baseline.json`, or
  regenerate it with `--update-baseline --reason`.
- Commands can be scheduled: `govibe.Command` has a `Schedule` field (`govibe.Cron`, `govibe.Every`, `govibe.Schedule`, `govibe.Skip` and `govibe.Allow`), `serve` runs the scheduler (`serve --no-schedule` and
  `scheduler.enabled` turn it off), and `./app schedule list|run` are runtime commands. A command named `schedule` is now refused, as are the other built-in names. New settings `scheduler.enabled`,
  `scheduler.timezone`, `scheduler.shutdown_timeout` (a Go duration or a number of seconds) and `scheduler.lock` (see `GoVibe: Configuration`); an invalid schedule or setting fails the boot and names what is wrong. Nothing to do unless an app already has a
  command called `schedule`.
- `govibetest.Boot` takes options (`WithTime`, `WithSettle`) and starts the scheduler on a fake clock: `srv.Clock.AdvanceTo(...)` and `srv.Runs("name")` test a scheduled command. Existing calls compile unchanged.
- `inspect --json` carries each command's schedule and the scheduler's settings (new fields; the schema number is unchanged), and the project graph has a `commands` layer, a `command` kind, a commands report and the info rule
  `docs.command_undocumented`: name each command in a doc (in backticks; a name with no colon needs the typed form, `./app cleanup`) or accept the finding with a reason. A graph that lists the rule files (`rules:` in `govibe.yml`) can switch it off.
- A migration that fails on the second boot because the DSN has no `parseTime=true` (goose reads its version table's timestamps) now says so, instead of "table goose_db_version already exists".
- The database explorer's "Referenced by" panel lists the routes and tools that reach a table through call chains again when the calls layer is on (it listed none), and lists the commands that reach it.
- New rule `framework.import` (error): business logic and storage import nothing from the framework; controllers, middleware and views import only the framework's `web` package;
  commands import the root `govibe` package; wiring imports the root package, `config` and `web`.
