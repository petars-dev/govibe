# GoVibe Architecture

This document describes the high-level architecture of the GoVibe web framework skeleton.

## Layers
- Router (chi) wires routes and middleware.
- Controllers contain no business logic; they orchestrate actions/services and render views or APIs.
- Business:
  - actions/: single-function units of business logic
  - services/: packages with richer logic
    - codegraph/: dev-only graph indexer; fills the embedded Cayley store with a model of the project itself (see "Dev-only tooling")
- Storage:
  - mariadb/: DB connectivity (sqlx over database/sql)
  - migrations/: goose v3 SQL migrations embedded in binary
  - dbexplorer/: dev-only schema introspection (per table, and `Snapshot` for the whole schema at once, which feeds the graph's db layer), filtered queries, sampling, explain plans, and gated test-data mutations, backing /dev/db and the db_* MCP tools
  - graphdb/: dev-only embedded Cayley graph store, backing /dev/graph and the graph_query MCP tool
- Views:
  - assets/templates: templ components rendered server-side
  - assets/static: static assets (Tailwind output, images, etc.)
  - assets/htmx: vendored htmx 2.0
- devmcp/: dev-only Model Context Protocol server (official Go SDK), mounted at /dev/mcp over streamable HTTP, exposing db_* and graph_query tools to local agents

## Requests
1) chi middleware stack -> controller handler
2) controller -> actions/services -> storage as needed
3) controller renders templ page or returns JSON

## Configuration
- Centralized in config/env.yml using ${ENV:default} placeholders.
- Resolved at runtime; typed access via config.Config or dynamic via config.Unmarshal/config.Get.
- `database.type` (`DATABASE_TYPE`, default `mysql`) selects the goose migration dialect via `storage/migrations.SetDialect`. Goose defaults to postgres if never told otherwise, which silently breaks migrations against any other database — every entry point that runs goose (server boot in main.go, `govibe migrate up/status/down`) sets this explicitly instead of relying on that default.

## Logging
- logrus, text formatter by default.

## Dev-only tooling
- `/dev/docs`, `/dev/graph`, `/dev/db`, and `/dev/mcp` are wired directly in routes.go and are only mounted when `!cfg.IsProd()`; none of them exist in a production build.
- `/dev/db` is a Faucet-like database explorer (schema introspection, table browsing, filtered queries, row sampling, explain plans) with an optional, explicitly-gated mutation surface (`db_insert_test_row`/`db_reset_test_data`, or the equivalent `/dev/db/api` endpoints) for seeding/clearing test fixtures. Gated by `dev.db.allow_mutations` in config/env.yml (`DEV_DB_ALLOW_MUTATIONS`), which has no effect in production regardless of the flag.
- `/dev/mcp` mounts an MCP server (github.com/modelcontextprotocol/go-sdk) over streamable HTTP so local agents can call the same db_* tools plus `graph_query` (a read-only Gizmo query against the embedded graph store) without going through the UI.
- See docs/specs/SPEC_DEV_DB_EXPLORER.md for details and status.
- The graph store's bolt file (`docs/graph/*.bolt`) is derived data and is git-ignored. What goes into the graph and how to query it is defined in `docs/public/GRAPH_SCHEMA.md`; the plan for the indexer is `docs/specs/SPEC_GRAPH_INDEXER.md`.
- **Graph indexer** (`business/services/codegraph`, Phases 1-3 of that spec): once `routes.NewRouter` has wired everything, a goroutine indexes the project into the graph store, so every Air rebuild/restart refreshes it. It runs inside the server process because the process owns the bolt lock; it never delays startup and a failure is logged, never fatal. `POST /dev/graph/reindex` rebuilds on demand and returns a JSON report (per-layer status, whether it was rebuilt, quad counts, added/removed vs the previous run). Each layer owns the quads carrying its `layer:<name>` label and replaces only those, so quads added by hand survive. Implemented layers: `code` (packages, files, funcs, types, imports from `go/ast`, and the facts the other layers resolve against), `routes` (the live chi router: route → handler by file and line, middleware), `templates` (`.templ` components and who renders them), `config` (`config/env.yml` keys, defaults, environment variables, sensitive flags, and every use in the code), `mcp` (tools registered with `mcp.AddTool`), `db` (the schema from the migrations in `storage/migrations/sql`, merged with the live database's schema read by `dbexplorer.Explorer.Snapshot` when one is configured: tables, columns, indexes, foreign keys, drift between the two, relations the schema implies, and which code reads or writes which table), `docs` (docs, specs and their status rows, and what they mention), and `meta` (`build:latest` and one `layer:<name>` node per layer). Each layer has a fingerprint over its inputs and its dependencies' fingerprints; a `/dev/graph` page load and every `graph_query` call first rebuild only the stale layers, which is how an edit to `docs/**` (ignored by Air) shows up without a restart. The `findings`, `calls` and `git` layers are not built yet. The live schema can change with no file changing, so a freshness check reads it too (reused for 5 seconds), and an unreachable database only makes the `db` layer fall back to the migrations. Production never opens the graph store, so it never indexes.
