# GoVibe Architecture

This document describes the high-level architecture of the GoVibe web framework skeleton.

## Layers
- Router (chi) wires routes and middleware.
- Controllers contain no business logic; they orchestrate actions/services and render views or APIs.
- Business:
  - actions/: single-function units of business logic
  - services/: packages with richer logic
- Storage:
  - mariadb/: DB connectivity (sqlx over database/sql)
  - migrations/: goose v3 SQL migrations embedded in binary
  - dbexplorer/: dev-only schema introspection, filtered queries, sampling, explain plans, and gated test-data mutations, backing /dev/db and the db_* MCP tools
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
