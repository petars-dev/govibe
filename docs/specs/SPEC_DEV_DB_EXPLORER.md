# SPEC_DEV_DB_EXPLORER

## Description
A Faucet-like, embedded database explorer for GoVibe's MariaDB/MySQL database, plus an MCP
(Model Context Protocol) server exposing the same capabilities to local agents. Both are
dev-only: they are wired in routes.go behind `!cfg.IsProd()` and simply don't exist in a
production build.

## Goals
- Let a developer inspect the app's database from the browser at `/dev/db`, with no separate
  DB client: schema introspection, table browsing, filtered queries, row sampling, explain
  plans, and an optional, explicitly-gated mutation surface for test fixtures.
- Let an agent (or script) do the same programmatically, either over the REST/JSON API at
  `/dev/db/api/*` or over MCP at `/dev/mcp`.
- Reuse the embedded Cayley graph store already mounted at `/dev/graph` for a `graph_query`
  MCP tool, so agents can inspect graph data the same way they inspect the SQL database.
- Never expose any of this, or any mutation capability, in production, regardless of config.

## Phases & Tasks
1. Phase 1 — Core introspection/query engine
   - Task: `storage/dbexplorer` — ListTables, DescribeTable (columns/indexes/foreign keys),
     BrowseRows (filtered/paginated), SampleRows, Query (read-only), Explain.
   - Task: Statement-safety guards — single-statement only, read-only keyword allowlist for
     `Query`/`Explain`/`BrowseRows` fragments, identifiers validated against live schema.
2. Phase 2 — REST/JSON API + UI
   - Task: `controllers/devdb.go` — JSON handlers mounted at `/dev/db/api/*`.
   - Task: `controllers/devdb_page.go` — single-page UI at `/dev/db` (sidebar table list,
     Schema/Rows/Sample/Mutate tabs, ad-hoc Query/Explain), themed to match `/dev/docs` and
     `/dev/graph`.
3. Phase 3 — Gated test-data mutations
   - Task: `InsertTestRow`/`ResetTestData` in `storage/dbexplorer`, gated by
     `dev.db.allow_mutations` (`DEV_DB_ALLOW_MUTATIONS`), off by default.
   - Task: Mutate tab in the UI (insert form built from live schema, double-click-to-confirm
     reset — no native `confirm()` dialogs).
4. Phase 4 — MCP server
   - Task: `devmcp` package using the official Go SDK
     (github.com/modelcontextprotocol/go-sdk), mounted at `/dev/mcp` over stateless
     streamable HTTP.
   - Task: Tools `db_list_tables`, `db_describe_table`, `db_query`, `db_explain`,
     `db_sample_rows`, `db_insert_test_row`, `db_reset_test_data`, `graph_query`.
5. Phase 5 — Nav/docs integration
   - Task: Add a "DB" tab across `/`, `/dev/docs/*`, and `/dev/graph`, plus a home-page CTA
     and feature card.
   - Task: This spec + ARCHITECTURE.md/MEMORY_SCHEMA.md updates.

## Backlog
- Pagination cursor for very large tables (current Rows tab uses offset/limit).
- Optional per-table allowlist for mutations (currently a single global flag).
- Surface `EXPLAIN FORMAT=JSON` as a structured tree instead of the raw tabular plan.

## Status Table

| Item | Implemented | Skipped | Why |
| --- | --- | --- | --- |
| Schema introspection (tables/columns/indexes/FKs) | Yes | | |
| Table browsing with filter/order/paging | Yes | | |
| Row sampling | Yes | | `ORDER BY RAND() LIMIT n`; fine at dev-data scale |
| Explain plans | Yes | | Read-only SELECT/WITH statements only |
| Ad-hoc read-only query console | Yes | | SELECT/SHOW/DESCRIBE/EXPLAIN/WITH only |
| Gated insert/reset test-data mutations | Yes | | Off by default; requires `DEV_DB_ALLOW_MUTATIONS=true` and non-production |
| Arbitrary mutation via raw SQL (`db_query`) | No | Skipped | Kept `db_query`/`Query()` strictly read-only; all mutation goes through the safer, parameterized insert/reset paths |
| MCP server (db_* tools) | Yes | | Official Go SDK, streamable HTTP, stateless |
| MCP graph inspection tool (`graph_query`) | Yes | | Runs Gizmo queries via the same query engine `/dev/graph` uses |
| Nav buttons in docs/app | Yes | | "DB" tab added to home, docs, specs, graph pages; home CTA + feature card |
| Production exposure | No | Skipped | Everything under `/dev/*` is gated on `!cfg.IsProd()` by design |
