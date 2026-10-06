# Dev DB Explorer & MCP Server

GoVibe embeds a Faucet-like database explorer and an MCP server for local agents. Both are
dev-only: they're mounted in `routes.go` behind `!cfg.IsProd()`, so neither exists in a
production build regardless of configuration.

## `/dev/db` — the UI

Visit `/dev/db` for a single-page explorer:

- **Schema** — columns, indexes, and foreign keys for the selected table.
- **Rows** — browse with an optional `WHERE` fragment, `ORDER BY`, and limit/offset paging.
- **Sample** — a random sample of rows (`ORDER BY RAND() LIMIT n`).
- **Query** / **Explain** — ad-hoc, read-only SQL (`SELECT`/`SHOW`/`DESCRIBE`/`EXPLAIN`/`WITH`)
  and its query plan.
- **Mutate** *(only shown when mutations are enabled)* — insert a test row from a form built
  from the live schema, or delete every row in a table (double-click to confirm; no native
  browser dialogs are used).

## `/dev/db/api/*` — the REST/JSON API

| Method & path | Purpose |
| --- | --- |
| `GET /dev/db/api/meta` | `{"allow_mutations": bool}` |
| `GET /dev/db/api/tables` | List tables |
| `GET /dev/db/api/tables/{table}` | Describe a table |
| `GET /dev/db/api/tables/{table}/rows?where=&order=&limit=&offset=` | Browse rows |
| `GET /dev/db/api/tables/{table}/sample?limit=` | Sample rows |
| `POST /dev/db/api/query` `{"sql": "..."}` | Run a read-only statement |
| `POST /dev/db/api/explain` `{"sql": "..."}` | Explain a `SELECT`/`WITH` statement |
| `POST /dev/db/api/tables/{table}/rows` `{"values": {...}}` | Insert a test row (gated) |
| `DELETE /dev/db/api/tables/{table}/rows` | Delete every row in a table (gated) |

## The whole schema at once

`Explorer.Snapshot` (in `storage/dbexplorer`) reads the entire schema of the connected database with
one `information_schema` query per kind (tables, columns, indexes, foreign keys with their `ON
DELETE`/`ON UPDATE` actions, comments) plus the goose migrations that are applied. It reads no rows and
leaves out volatile counters, so two snapshots of an unchanged schema are equal. It is not exposed over
HTTP or MCP: the embedded graph indexer uses it to merge the live schema with the one the migrations
describe and to flag drift (see `GRAPH_SCHEMA.md`, "db"). To run its tests against a real server,
set `GOVIBE_TEST_MARIADB_DSN='user:pass@tcp(host:port)/'`: each test creates and drops a schema of its
own.

## `/dev/mcp` — the MCP server

A [Model Context Protocol](https://modelcontextprotocol.io) server (built with the official
[Go SDK](https://github.com/modelcontextprotocol/go-sdk)) is mounted at `/dev/mcp` over
stateless streamable HTTP, so any MCP-capable agent can point at
`http://localhost:<port>/dev/mcp` and call:

- `db_list_tables`, `db_describe_table`, `db_query`, `db_explain`, `db_sample_rows`
- `db_insert_test_row`, `db_reset_test_data` (gated, see below)
- `graph_query` — a read-only Gizmo query against the embedded Cayley graph store that also
  backs `/dev/graph` (e.g. `g.V().All()`)
- (`/dev/db#table=<name>` opens a table; its Schema tab ends with a "Referenced by" panel from the project graph: the code that reads or writes it, the routes and tools that may reach it, and the docs that mention it, each marked certain or possible.)
- `graph_schema`, `graph_context`, `graph_impact`, `graph_path`, `graph_search`, `graph_neighbors`,
  `graph_findings`, `graph_reindex` — the higher-level agent tools over the same graph, which holds a
  model of the project including the database schema and which code touches which table; start with
  `graph_schema` (see `GRAPH_SCHEMA.md`)

## Enabling test-data mutations

`db_insert_test_row`, `db_reset_test_data`, and the equivalent `/dev/db/api` mutation
endpoints are disabled by default. Enable them for a test/dev build with:

```
DEV_DB_ALLOW_MUTATIONS=true
```

This has no effect when `APP_ENV` is `prod`/`production` — the mutation surface is refused at
the storage layer even if the flag is set.

## Safety notes

- All read paths (`Query`, `Explain`, `BrowseRows`) accept only a single, read-only statement
  (`SELECT`/`SHOW`/`DESCRIBE`/`EXPLAIN`/`WITH`); anything else is rejected before it reaches
  the database.
- `db_insert_test_row` binds values as query parameters and validates column names against the
  table's real schema; `db_reset_test_data` validates the table name the same way.
- None of `/dev/docs`, `/dev/graph`, `/dev/db`, or `/dev/mcp` are reachable in production.
