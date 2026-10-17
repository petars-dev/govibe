GoVibe: contributor guide

Overview
This repository is the GoVibe framework: the module github.com/petars-dev/govibe, the tool in cmd/govibe, and one reference
application in examples/hello. Applications are separate modules that depend on the framework; the rules they follow are in
examples/hello/SKILL.md (the app-facing rules, rendered into every app by `govibe new`). This file is for working on the
framework itself. The plan behind the layout is docs/specs/SPEC_FRAMEWORK_EXTRACTION.md; how it is built is docs/public/ARCHITECTURE.md.

Directory Layout
- govibe.go: the public entry point (`App`, `Runtime`, `Command`, `Main`, `Run`); arch_test.go enforces the dependency rules
- config, web, govibetest: the other public packages. Together with govibe they are the whole public API; everything else is internal/
- internal/kernel: the one boot path, the runtime commands, `inspect --json`; devmount.go and devmount_dev.go are the only seam to the dev tools
- internal/devtools, internal/devtools/devui: the dev panel (docs, graph, db pages and their embedded files); internal/codegraph, internal/dbexplorer, internal/graphdb, internal/devmcp: the indexer, the database explorer, the Cayley store, the MCP server
- internal/migrate, internal/routeinfo, internal/scheduler: the goose runner, the chi router read into plain data (and the `inspect --json` document the app binary and the tool share), and the scheduler's engine (validated cron specs, the runner, a fake clock)
- internal/profile, internal/rules, internal/frameworkdocs: the project profile (`govibe.yml`), the rules of the app-facing SKILL.md as data, and the framework docs an app serves under /dev/docs/govibe
- internal/scaffold: what `govibe new` writes (the embedded templates, the demo synced from examples/hello, go.mod, SKILL.md, govibe.yml, the managed files) and the keystone test
- cmd/govibe: the tool (`new`, `check`, `graph index`, `generate`, `serve`, `migrate`)
- examples/hello: the reference app, its own module (go.work joins it to the framework); it is also the demo every new app starts with
- docs: framework docs (public) and specs; the specs are process artifacts and are never shipped or embedded

Key Constraints
- The app imports the framework, never the reverse. An application imports only govibe, config, web and govibetest; examples/hello must too.
- internal/devtools does not import internal/kernel; the public packages do not reach the dev stack; nothing imports examples/ (arch_test.go).
- The dev tooling is excluded from production by construction: the kernel names it in one file behind the govibe_dev build tag. Never import
  internal/devtools, internal/codegraph, internal/devmcp, internal/dbexplorer or internal/graphdb from a file without that tag, and never
  tag a dev package itself (only the kernel's import file is tagged).
- Registration (Routes, Commands) must not open connections or read secrets: tooling calls it with no database.
- Goose never runs without an explicit dialect (`internal/migrate`).

Working on it
- `make dev` runs examples/hello with the dev panel at /dev; `make air` does the same with live reload. `make test` runs the suite for the framework
  and the example, `make test-dev` runs it with the dev tools compiled in (some tests exist only under the tag), `make check` runs both plus the gates.
- Verify in a scratch copy, started under a clean environment (`env -i HOME="$HOME" PATH="$PATH" ...`): a running dev server holds the graph store's
  lock, and a shell that exports MARIADB_DSN would make a boot open the real database. `go test ./...` indexes this repository, so do not edit the
  tree while it runs.
- A change that moves files changes no logic in the same step; the tests and the golden files in internal/codegraph/testdata/golden are the proof.
  Regenerate goldens only on an intended change (`go test ./internal/codegraph -update`) and review the diff.
- `templ generate` inside examples/hello only: with no path it also rewrites the fixture's .templ stand-ins under internal/codegraph/testdata.
- The framework's gate is `make graph-check` (and `make graph-check-app` for the example): an empty baseline, so a finding is fixed, or accepted
  with a written reason. The three docs of the framework are indexed too: a doc that names a file that is gone is a finding.
- The graph is about the project it indexes (scope `app` by default, the framework labelled): new node kinds say their `origin`, and a node's quads belong to one layer (an `external` node is
  written by the `externals` layer; the layers that point at it write only the edge). Where a project keeps things comes from internal/profile, never from a constant in the indexer.
- Text that is for the framework's contributors only (the test fixtures, the writer contract of the store) goes between `<!-- framework-only:start -->` and `<!-- framework-only:end -->` in a doc of docs/public: it
  stays in the repository's docs and is stripped from the copy an app serves. Examples in anything an app reads (docs, tool descriptions, graph_schema) use neutral names, never the fixtures' domain; a test fails if a served doc names a fixture.
- The framework docs an app sees are copies of docs/public made for go:embed (`make docs-embed`; a test fails when one is stale), chosen by the allowlist in internal/frameworkdocs. A spec is never on it.
  Set `GOVIBE_FRAMEWORK_SRC=<this checkout>` to read them from disk and list the specs under /dev/docs/govibe-specs.
- The Hello GoVibe demo a new app gets is examples/hello, synced into internal/scaffold/templates/demo by `make scaffold-sync` (`go generate ./internal/scaffold`), which also renders
  examples/hello/SKILL.md; a test fails when either is stale, so edit the example (never the copy) and run the sync. The scaffold's own templates are internal/scaffold/templates/app, rendered with
  `[[` and `]]`; a change to anything an app gets is a change to the managed files' hashes, so say so in docs/public/UPGRADING.md. `make new MODULE=github.com/acme/shop DIR=../shop` creates an app
  against this checkout; `make keystone` runs the keystone (the real `govibe new` plus an overlay, builds, tests, the gate and two boots, in a temp directory; minutes).
- The indexer's tests run against testdata/fixture (an app built on the stub framework testdata/stubfw) and testdata/fixture_layout (a non-default layout); `go.work` lists the stub and the fixture.
- Add a runtime command: a case in `kernel.Run`, its name in `builtinCommands`, a line in `usage`, and a test in govibe_test.go. The scheduler is tested on `scheduler.NewFake`, never with sleeps; a test that starts a
  task which blocks on purpose lowers `Fake.SetSettle`, and a test's cleanup moves the fake clock on so a stuck task cannot hang it. Add a dev page:
  a handler in internal/devtools/devui, its route in `devtools.Mount`, and a test that runs under the govibe_dev tag if it needs the mount.
- Anything an application sees is public API and costs a note in docs/public/ARCHITECTURE.md and, before 1.0, in the spec.

Spec-Driven Development Guidelines
- Keep human-readable docs in docs/:
  - docs/public/ARCHITECTURE.md: how the framework is built (update on structure changes).
  - docs/public/MEMORY_SCHEMA.md: the framework's own stored data (the graph store) and where an application's schema is documented.
  - docs/specs/: spec-driven process artifacts. For each epic/feature/user story, add SPEC_{FEATURE_NAME}.md with:
    1. Description
    2. Goals
    3. Phases and Tasks
    4. Backlog (tasks)
    5. Status Table of implemented vs skipped with rationale
- After each development loop (including AI runs), update the relevant SPEC_{FEATURE_NAME}.md with progress and deviations, and ARCHITECTURE.md
  if the architecture changed.
- Visualizations: run `make dev` and browse http://localhost:8080/dev/docs/app for the reference app's docs and /dev/docs/specs for its specs.
