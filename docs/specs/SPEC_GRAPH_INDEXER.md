# SPEC_GRAPH_INDEXER

## Description
Fill the embedded Cayley graph store (`/dev/graph`, `graph_query` MCP tool) automatically on every
build with a queryable model of the project itself: code structure, HTTP request flow, templates,
configuration, docs/specs, MCP tools, and database schema/relationships. Today the store only holds
hand-typed sample quads (`alice knows bob`), so it is not useful to anyone.

Dev-only, like everything under `/dev/*`: indexing never runs when `cfg.IsProd()`.

## What the repo tells us (constraints that shape the design)
1. **Bolt allows one process.** `graphdb.Open` blocks on the file lock (5 s timeout). A separate
   "index at build time" process (e.g. from `make air-build`) is a different process from the
   dev server and cannot reliably write while that server holds the lock. **Index inside the
   server process on boot.** With Air, every rebuild restarts the server, so "on boot" == "on
   every build".
2. **Air ignores `docs/` and `.md`** (`exclude_dir`, `include_ext`). Doc/spec edits never restart
   the server, so a boot-only index goes stale for exactly the layer humans edit most. Needs a cheap
   fingerprint check + on-demand reindex (see Triggers).
3. **`docs/graph/indexes.bolt` is committed.** A file rewritten every build will churn git and
   conflict on merge. It is derived data: untrack and ignore it.
4. **`.gitignore` line `govibe` is unanchored and swallows `cmd/govibe/`** — the whole CLI is
   untracked. Must become `/govibe` before we add `govibe graph index`.
5. **The DB is optional and empty.** `MARIADB_DSN` defaults to empty and `0001_init.sql` creates no
   tables. The DB layer must work from migrations alone and enrich from a live DB when present.
6. **Layering rule exists** (SKILL.md: controllers never touch storage; only actions/services do)
   and is already bent: `controllers/devdb.go` imports `storage/dbexplorer`. The graph can enforce
   the rule instead of a doc merely stating it.
7. **No tests exist.** The indexer needs golden tests from day one, since agents will trust its output.
8. **Existing pieces to reuse:** `chi.Walk` (exact runtime routes), `dbexplorer` (schema
   introspection incl. FKs), `templ/parser/v2` and `goldmark` (already deps), `mcp.AddResource` /
   `AddPrompt` (available in go-sdk v1.8.0), `Store.ApplyDeltas` (atomic add/delete).

## Goals
- A fresh, deterministic snapshot of the project graph after every boot, with zero manual steps.
- **Agents** get a self-describing graph and a few high-level tools, so they need no Gizmo
  knowledge and no repo-wide grep to orient themselves or estimate the blast radius of a change.
- **Humans** get a usable explorer (presets, filters, inspector, freshness), generated reference
  views that cannot drift from the code, and a findings list that catches drift and rule violations.
- Never break server startup, never leak secrets or row data, never touch manually added quads.

## Questions the graph must answer
| Question | Asked by | Answered by |
| --- | --- | --- |
| What does this file/package do, what depends on it? | both | `graph_context`, `imports` edges |
| If I change function/table X, what breaks? | both | `graph_impact` (reverse closure over calls/imports/table edges) |
| What is the full path of `GET /dev/db/api/query`? | both | route → middleware → handler → action → table |
| Which routes/code touch table `t`? | both | `reads_table`/`writes_table` + call closure |
| Where is env var / config key used, what is its default? | both | `config` layer |
| Which spec/doc covers this file? What is skipped and why? | both | `documents`, spec status rows |
| Show me an existing feature slice to copy | agents | `graph_query` slice by route, or `graph_context` on an exemplar |
| How are tables related (FKs, implicit refs, junctions)? | both | `db` layer, ER preset |
| What is stale or broken? | humans | `finding` nodes (dead doc paths, layering, unused config) |
| What changed in the last build? | both | `build` meta node + delta counts |

## Design

### Codex graph contract: what gets inserted and how it is queried

The graph is a derived, metadata-only index of the current source tree and runtime wiring. It is
not an application-data store. Insert one node for each stable code or schema entity, then connect
those nodes with typed, directed edges so an agent can move from an externally visible API to the
storage effects it can cause.

**Minimum indexed entities**

- Go package, source file, declared function/method, and declared type. Keep the repository-relative
  path, package import path, source line, exported flag, and a short doc/signature where available.
- Runtime route, HTTP method, path, and resolved handler; include middleware edges when discoverable.
- Business action/service functions and storage functions/packages, connected by `calls` when the
  call is statically resolvable. Where call resolution is unavailable, keep imports and mark any
  inferred relation with its method/confidence rather than presenting it as certain.
- Database table, column, index, migration, and foreign-key relation. Populate from embedded
  migrations even without a live DB; enrich from `information_schema` when configured. Never store
  row contents, credentials, DSNs, or sensitive config values.
- MCP tool and its implementation function; config keys and declarations/uses; docs/specs and the
  code, route, or table they describe.

**Required directed paths and predicates**

```text
route --handled_by--> controller handler
handler --calls--> business action/service --calls--> storage function
storage function --reads_table|writes_table--> table
table --has_column--> column --fk_to--> referenced column/table
route --uses_mw--> middleware
function --defined_in--> file --in_package--> package
package --imports--> package
doc/spec --covers|mentions--> code, route, config key, or table
```

Use exact, deterministic subject IDs (for example `route:GET /api/hello`,
`func:controllers.HelloAPI`, `table:users`, and `file:controllers/api.go`) and a single vocabulary
module. Structural relations are IRIs; descriptive attributes such as line number, source, SQL
operation, inference confidence, and docs are literals. Each generated quad must carry the owning
layer label. Reindexing replaces only quads with that label, using a sorted diff and atomic deltas;
unlabelled and user-authored quads must survive. Keep generated output stable across identical
builds, with timestamps and git state only on build metadata.

**How the index is refreshed**

Treat a successful local/dev server rebuild and restart as the build boundary: index in the server
process after route construction, asynchronously, because the server owns the Bolt lock. Do not run
a second indexer process from `go build`/Air build hooks against the same Bolt directory. A failed
layer logs a warning and leaves the server usable. Fingerprint source inputs and refresh stale
layers on `/dev/graph` access and MCP graph calls, with an explicit reindex operation for humans and
agents. Production does not open or write this dev graph.

**Agent query acceptance examples**

The graph must support answering these without manually following source files:

1. For `GET /api/hello`, return the route, `controllers.HelloAPI`, its call to
   `business/actions.Hello`, and explicitly report that no table is reached.
2. For a DB-backed API, return the ordered route → handler → action/service → storage call path,
   then the table read/write edges and any table/column foreign-key relationships. If a link cannot
   be resolved, return the known partial path and label the missing hop; do not invent one.
3. For a table, return all routes whose handler call closure reaches a function that reads or writes
   it, distinguishing reads from writes.
4. For a package or file, return direct imports and reverse dependents so an agent can estimate
   change impact.

At present the MCP implementation exposes only `graph_query` for read-only Gizmo queries (plus
`db_*` tools when a MariaDB connection is configured). The higher-level graph tools/resources below
are planned additions, not capabilities an agent should assume exist until implemented.

### Triggers
1. **Boot (default):** after `routes.NewRouter` finishes wiring, run the indexer in a goroutine
   (async, so startup is never delayed; failures are logged as warnings, never fatal).
2. **Lazy refresh:** each layer stores a source fingerprint (sorted path+mtime+size hash). `/dev/graph`
   page load and every graph MCP call compares fingerprints and re-runs only stale layers. This
   covers `docs/**` edits that Air ignores, with no fsnotify dependency.
3. **Explicit:** `POST /dev/graph/reindex` and MCP `graph_reindex`.
4. **Offline CLI:** `govibe graph index [--dir X]` for when no server holds the lock; on lock timeout
   it says to use the HTTP endpoint instead. Also usable in CI with `--dir` on a scratch path.
5. **Not** in `air-build` or `make build`: those are separate processes that can collide with
   the running server's lock, and a production build has no graph at all.

### Writing strategy
- Every managed quad carries a **label** = its layer (`layer:code`, `layer:routes`, `layer:db`, …).
  Quads with any other label (including the `/dev/graph` "Add a quad" form) are never touched.
- Per layer: build the new quad set in memory, read the existing quads for that label, diff, and
  `ApplyDeltas` only the difference. Idempotent, atomic per layer, and the diff gives "what changed".
- Layer output is deterministic (no timestamps, sorted); volatile data lives only on the meta node.

### Vocabulary (prefixed IRIs, defined once in `vocab.go`, documented in `GRAPH_SCHEMA.md`)
| Kind | IRI example | Key attributes |
| --- | --- | --- |
| package | `pkg:controllers` | layer (`controllers`/`business`/`storage`/`wiring`/`dev`), doc |
| file | `file:controllers/devdb.go` | lines, generated |
| func / type | `func:controllers.DevDBMeta` | file, line, exported, doc (first sentence), signature |
| route | `route:GET /dev/db/api/meta` | method, path, dev_only |
| middleware | `mw:middleware.RequestLogger` | |
| template | `tmpl:home.Home` | file |
| mcp_tool | `tool:graph_query` | description, input schema |
| config_key / env_var | `cfg:database.type`, `env:DATABASE_TYPE` | default, sensitive |
| doc / spec / spec_item | `doc:docs/public/ARCHITECTURE.md`, `spec:graph-indexer` | title, status, why_skipped |
| table / column / index | `table:users`, `col:users.email`, `idx:users.ux_email` | type, nullable, unique, engine, comment |
| migration | `mig:0001_init` | version, applied |
| finding | `finding:<rule>:<subject>` | rule, severity, message |
| build | `build:latest` | git_sha, dirty, indexed_at, duration_ms, indexer_version, per-layer counts |

Predicates: `type`, `name`, `defined_in`, `in_package`, `imports`, `calls`, `handled_by`, `uses_mw`,
`renders`, `uses_config`, `reads_env`, `reads_table`, `writes_table`, `maps_to`, `has_column`,
`has_index`, `fk_to`, `implicit_ref`, `many_to_many`, `introduced_in`, `documents`, `mentions`,
`covers`, `about`. Attribute predicates (`line`, `doc`, `default`, …) use literal objects so the
UI can hide them.

**Spike first:** confirm how IRI vs string vs typed-literal objects round-trip through Cayley's
`/v2/read?format=json` and Gizmo (`g.V()`, `.Has`, `.Regex`), because the visualizer and every
agent query depend on it.

### Layers
| Layer | Source | Notes |
| --- | --- | --- |
| `code` | `go/parser` + `go/ast` over repo (skip `_templ.go`, `tmp/`, `.git`) | packages, files, funcs, types, imports, layer classification, godoc first sentence. AST only: fast, no type-check |
| `routes` | runtime `chi.Walk(router)` + `runtime.FuncForPC(...).FileLine` mapped onto `code` funcs | exact truth incl. conditional `/dev/*` routes and per-route middleware chain; closures resolve to their enclosing func by file:line |
| `templates` | `templ/parser/v2` on `*.templ` | components, component→component calls, controller→template `renders` |
| `config` | `yaml.v3` Node on `config/env.yml` + selector scan of `cfg.X.Y` | key → env var → default; key → struct field → usage sites; secrets flagged |
| `docs` | goldmark AST on `docs/public`, `docs/specs`, README, SKILL.md | doc→doc links; backticked paths/symbols → `mentions`; spec status-table rows → `spec_item` with status and `why_skipped` |
| `mcp` | AST match of `mcp.AddTool(... &mcp.Tool{Name:…})` | tool → backing function, so tools are traceable to code |
| `db` | live schema (when DSN set) + static migrations | see below |
| `calls` (Phase 5, opt-in) | `x/tools/go/packages` + static callgraph | enables route→…→table and reverse-impact; type-checks the module, so it is opt-in and cached by fingerprint |
| `findings` | derived from the other layers | see below |
| `git` (Phase 6, opt-in) | `git log` | per-file last commit, churn, co-change |
| `meta` | indexer | `build:latest`, layer fingerprints, counts, deltas |

### Database layer
- **Live** (when `MARIADB_DSN` is set): add `Explorer.Snapshot(ctx)` to `dbexplorer`, using one
  `information_schema` query per kind for the whole schema instead of N `DescribeTable` calls. Also
  read `REFERENTIAL_CONSTRAINTS` (ON DELETE/UPDATE rules, which `ForeignKey` lacks today) and
  table/column comments. Emits table, column (type, nullable, key, default, extra), index (ordered
  columns, unique) and `fk_to` edges at both column and table level.
- **Static** (always): parse embedded `storage/migrations/sql/*.sql` (goose Up sections) for
  `CREATE/ALTER/DROP TABLE`, `REFERENCES`, `CREATE INDEX`. Gives migration nodes, `introduced_in`
  history, and a usable graph with no database running. Best-effort regex parser; live data wins on
  conflict; `applied` comes from `goose_db_version` when live.
- **Inferred** (flagged `inferred=true`, with a `confidence`): `<x>_id` columns matching another
  table's PK without an FK constraint → `implicit_ref`; two FKs plus composite PK → `many_to_many`
  junction; FK column that is unique → one-to-one, else one-to-many.
- **Code ↔ DB:** (a) string literals in `storage/` and `business/` matching `FROM|JOIN|INTO|UPDATE
  <known table>` → `reads_table`/`writes_table` from the enclosing func; (b) `db:"col"` struct tags
  matched against a table's columns → `maps_to` (Go type ↔ table). With the `calls` layer this
  closes the loop route → handler → action → func → table.
- **Never index row data** (dev DBs contain real-looking PII). Schema and metadata only.

### Findings (derived, stored as `finding` nodes, shown to humans and returned to agents)
- Layering violations (controller→storage, storage→business, …), with an allow-list for wiring
  packages (`routes`, `main`, `devmcp`).
- Doc/spec mentions a path or symbol that no longer exists (stale docs).
- Spec item marked implemented whose referenced files are missing.
- Config key or env var declared but never read; read but not declared.
- Route with no doc mention; exported func never referenced.
- DB: table without PK; FK column without index; `_id` column without FK; migration/live drift.

### Agent surface (MCP, `/dev/mcp`)
1. `graph_schema`: kinds, predicates with counts, vocabulary, 5 example queries, and freshness
   (git sha, dirty, indexed_at, stale layers). Agents call this first.
2. `graph_context {path|symbol|route|table}`: one-call briefing: package/layer, symbols, routes
   served, tables touched, config used, docs/specs mentioning it, findings about it, neighbours.
3. `graph_impact {node, depth}`: reverse closure ("what is affected if I change this").
4. `graph_search {text, kinds}`, `graph_neighbors {node, direction, predicates, depth}`,
   `graph_path {from, to}` (BFS in Go; Gizmo has no shortest-path).
5. `graph_findings {severity, rule}` and `graph_reindex {layers}`.
6. Resources: `govibe://graph/overview` (architecture map rendered from the graph) and
   `govibe://graph/schema`. Prompt: `explain-change` (takes a diff/paths, returns impact + docs to
   update, matching the "update ARCHITECTURE/MEMORY_SCHEMA/SPEC after each loop" rule in SKILL.md).
7. Keep `graph_query` for power use, but its description links to `graph_schema`.
All tools return bounded, JSON-structured output (limits and `truncated` flags, like `db_*`).

### Human surface
- `/dev/graph`: preset views (Architecture, Request flow, DB ER, Docs↔code coverage, Config→usage);
  kind filter chips with per-kind colour/shape; hide attribute predicates; cap at N nodes and
  expand on double-click (the current "load every quad + `cose` layout" will not survive
  thousands of quads); search that hits the index endpoint; node inspector with attributes,
  `vscode://file/<abs>:<line>` link, and links to `/dev/docs/*` and `/dev/db`.
- Freshness banner: "indexed 12 s ago @ `405ab1d`+dirty, 1,204 quads, +38/−12 vs previous" and a
  Reindex button.
- Findings panel with severity filter; each finding links to its node.
- `/dev/graph/reports/{routes,config,schema,layers}`: always-fresh generated reference pages
  (route table, env/config reference, DB schema with FK links, package layer map). Replaces
  hand-maintenance of the schema/route parts of MEMORY_SCHEMA.md and ARCHITECTURE.md.
- Cross-links: `/dev/db` table view shows "referenced by" (files, routes, specs) from the graph.

### Guardrails
- Indexer failure never fails boot; every layer is independent and reports its own status.
- Sensitive keys (`/key|secret|password|token|dsn/i`) get **no default or value** in the graph;
  `.env*` files are never read; DSNs never stored.
- Indexer touches only its own labels; user-added quads survive every reindex.
- Path indexing is confined to the repo root (no symlink escapes); size/depth caps per layer.
- Layer timing and counts logged at info; budget: AST layers < 500 ms on this repo.

## Phases & Tasks
0. **Phase 0: Groundwork (≈½ day)**
   - Anchor `.gitignore` (`/govibe`) so `cmd/govibe/` is tracked; untrack and ignore `docs/graph/*.bolt`.
   - Spike: IRI/literal round-trip via `/v2/read` and Gizmo; decide final vocabulary.
   - Write `docs/public/GRAPH_SCHEMA.md` skeleton; fixture mini-module for golden tests.
   - *Done when:* vocabulary agreed, `git status` no longer lists the bolt file or hides `cmd/`.
1. **Phase 1: Indexer core + `code` layer (MVP)**
   - `business/services/codegraph`: `vocab.go`, label-scoped diff writer (`ApplyDeltas`), fingerprints,
     `meta` node, async boot hook in `routes.NewRouter`, `POST /dev/graph/reindex`.
   - `code` layer via `go/ast`; layer classification; `imports` edges.
   - Golden N-Quads test on the fixture; invariant test on the real repo (no dangling refs).
   - *Done when:* `g.V('pkg:controllers').Out('imports').All()` works after `make air`, restarts
     reproduce identical quads, and a manual quad survives a reindex.
2. **Phase 2: Runtime + config + docs layers**
   - `routes` (chi.Walk + middleware + FuncForPC mapping), `templates`, `config`, `mcp`, `docs`
     (incl. spec status rows, path/symbol `mentions`).
   - Lazy refresh by fingerprint (docs edits show up without a restart).
   - *Done when:* a route resolves to its handler's file:line, and editing a spec updates the graph
     on the next page load.
3. **Phase 3: `db` layer**
   - `Explorer.Snapshot`, `REFERENTIAL_CONSTRAINTS`, static migration parser, inferred relations,
     table-literal → `reads_table`/`writes_table`, `db:` tag → `maps_to`.
   - Seed a sample migration (users/orders/order_items) in a scratch DB to exercise FK, implicit ref,
     junction, and no-DB (migration-only) paths.
   - *Done when:* ER preset renders from migrations alone, and live DB adds indexes/comments/drift.
4. **Phase 4: Agent surface**
   - `graph_schema`, `graph_context`, `graph_impact`, `graph_search`, `graph_neighbors`,
     `graph_path`, `graph_findings`, `graph_reindex`, resources, `explain-change` prompt; update
     server `Instructions` to point at `graph_schema` first.
   - *Done when:* an agent can answer "what breaks if I change table X / file Y" in ≤ 2 tool calls.
5. **Phase 5: Findings + human surface**
   - `findings` rules; UI presets, filters, inspector, freshness banner, findings panel, reports,
     `/dev/db` cross-links.
   - *Done when:* the current `controllers → storage` layering violation appears as a finding, and
     the visualizer stays responsive at ≥ 5k quads.
6. **Phase 6: Depth (opt-in)**
   - `calls` layer (`x/tools/go/packages`), full route→table chains, `git` layer (last commit, churn,
     co-change), `govibe graph index` CLI, CI job running the CLI with `--dir` and failing on new
     high-severity findings.

## Backlog
- Last-N-builds changelog (changed subjects per build) for "what changed since I last looked".
- Test→code edges once tests exist; coverage overlay.
- `EXPLAIN` the SQL literals found in code to flag full scans as findings.
- Optional deterministic N-Quads snapshot export for agents without a running server.
- Column-level lineage for `maps_to` beyond tag matching.
- Support Postgres/SQLite dialects in the static migration parser (`DATABASE_TYPE`).

## Decisions defaulted here (flip any of these)
| Decision | Default | Alternative |
| --- | --- | --- |
| Where indexing runs | In-server, async, on boot + lazy + explicit | Offline CLI only (breaks with Air's lock) |
| Commit the bolt file? | No, ignore it; rebuilt every boot | Commit an N-Quads snapshot for offline agents (noisy diffs) |
| Call graph | Opt-in, Phase 6 | Always on (slower boot, more type-check failures) |
| Stale-doc handling | Fingerprint check per request | fsnotify watcher |

## Status Table

| Item | Implemented | Skipped | Why |
| --- | --- | --- | --- |
| Phase 0 groundwork (.gitignore, spike, vocab) | No | | Planned |
| Indexer core + `code` layer | No | | Planned |
| `routes`/`templates`/`config`/`mcp`/`docs` layers | No | | Planned |
| `db` layer (live + static + inferred + code↔table) | No | | Planned |
| Agent MCP tools/resources/prompt | No | | Planned |
| Findings + human UI/reports | No | | Planned |
| `calls`/`git` layers, CLI, CI | No | | Planned, opt-in |
| Indexing in production | No | Skipped | Everything under `/dev/*` is gated on `!cfg.IsProd()` by design |
| Indexing row data | No | Skipped | PII risk; schema and metadata only |
