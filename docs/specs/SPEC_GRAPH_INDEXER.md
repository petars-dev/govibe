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
   `AddPrompt` (available in go-sdk v1.8.0), `Store.ApplyDeltas` (atomic batch; tolerant
   `IgnoreDup`/`IgnoreMissing` mode only: a failed strict transaction corrupts the in-process node
   cache. A **mixed add+delete batch is also unsafe**: it silently loses the adds, see F11 and the
   Writer contract in `docs/public/GRAPH_SCHEMA.md`).

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

Use exact, deterministic subject IDs (for example `route:GET:/api/hello`,
`func:controllers.HelloAPI`, `table:users`, and `file:controllers/api.go`) and a single vocabulary
module. IDs never contain spaces, control characters, `"` or `\` (percent-encoded): Cayley's default
N-Quads wire format mis-parses them, which is why the route ID separator is `:` and not a space. Structural relations are IRIs; descriptive attributes such as line number, source, SQL
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
  write only the difference. Idempotent, and the diff gives "what changed". Deletes must carry the
  exact label, and writes use tolerant `ApplyDeltas` (`IgnoreDup` and `IgnoreMissing`, never
  `ApplyTransaction`). **Phase 1 deviation:** the diff is applied as two batches, adds first, then
  deletes, and the layer is read back and verified afterwards. A single mixed batch corrupted the
  store in 147 of 150 randomized trials (F11), so "atomic per layer" became "converges on the next
  run": a failure between the two batches leaves a superset that the next run trims. The full
  recipe is the Writer contract in `GRAPH_SCHEMA.md`.
- Layer output is deterministic (no timestamps, sorted); volatile data lives only on the meta node.

### Vocabulary (prefixed IRIs, defined once in `vocab.go`, documented in `GRAPH_SCHEMA.md`)
**`docs/public/GRAPH_SCHEMA.md` is the authority** (decided in Phase 0); where it and the table below
differ, it wins. Differences so far: route IDs are `route:GET:/path`; Go types are `typ:`; the package
attribute is `arch_layer` (values `controllers`, `business`, `storage`, `middleware`, `wiring`, `dev`,
`views`, `support`); the column data-type attribute is `data_type` because `type` is the kind
predicate; new predicates `has_layer`, `has_item`, `registers`.

| Kind | IRI example | Key attributes |
| --- | --- | --- |
| package | `pkg:controllers` | arch_layer (`controllers`/`business`/`storage`/`wiring`/`dev`/…), doc |
| file | `file:controllers/devdb.go` | lines, generated |
| func / type | `func:controllers.DevDBMeta` | file, line, exported, doc (first sentence), signature |
| route | `route:GET:/dev/db/api/meta` | method, path, dev_only |
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

**Spike (done, Phase 0):** IRI vs string vs typed-literal round trips through `/v2/read?format=json`,
N-Quads and Gizmo are recorded in `GRAPH_SCHEMA.md` ("Spike results") and pinned by
`storage/graphdb/contract_test.go`. Corrections to this spec: Gizmo treats a bare `'x'` as a string, so
IRIs are queried as `g.V('<pkg:controllers>').Out('<imports>')` or with `g.IRI(...)`; pattern matching
is `Filter(regex(p, true))` / `Filter(like(p))`, not `.Regex`; `Count()` returns null, so counts use
`g.Emit(...ToArray().length)`.

### Layers
| Layer | Source | Notes |
| --- | --- | --- |
| `code` | `go/parser` + `go/ast` over repo (skip `_templ.go`, `tmp/`, `.git`, `vendor/`, every `testdata/`, and any directory with its own `go.mod`) | packages, files, funcs, types, imports, layer classification, godoc first sentence. AST only: fast, no type-check |
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
0. **Phase 0: Groundwork (≈½ day), done**
   - Anchor `.gitignore` (`/govibe`) so `cmd/govibe/` is tracked; untrack and ignore `docs/graph/*.bolt`.
   - Spike: IRI/literal round-trip via `/v2/read` and Gizmo; decide final vocabulary.
   - Write `docs/public/GRAPH_SCHEMA.md` skeleton; fixture mini-module for golden tests.
   - *Done when:* vocabulary agreed, `git status` no longer lists the bolt file or hides `cmd/`.
   - *Delivered:* `.gitignore`; `storage/graphdb/contract_test.go` (the spike, as a regression test);
     `docs/public/GRAPH_SCHEMA.md`; `business/services/codegraph/testdata/fixture/`. The vocabulary
     still needs the owner's sign-off, and `GRAPH_SCHEMA.md` ends with the decisions deferred to later
     phases.
1. **Phase 1: Indexer core + `code` layer (MVP), done**
   - `business/services/codegraph`: `vocab.go`, label-scoped diff writer (`ApplyDeltas`), fingerprints,
     `meta` node, async boot hook in `routes.NewRouter`, `POST /dev/graph/reindex`.
   - `code` layer via `go/ast`; layer classification; `imports` edges.
   - Golden N-Quads test on the fixture; invariant test on the real repo (no dangling refs).
   - *Done when:* `g.V('<pkg:controllers>').Out('<imports>').All()` works after `make air`, restarts
     reproduce identical quads, and a manual quad survives a reindex.
   - *Delivered:* `business/services/codegraph/` (`vocab.go`, `writer.go`, `scan.go`, `code.go`,
     `meta.go`, `indexer.go`, tests, `testdata/golden/code.nq`); `controllers.DevGraphReindex`;
     the boot hook in `routes/routes.go`; the `graph_query` description now shows the `'<iri>'`
     idiom; F11 pinned in `storage/graphdb/contract_test.go`. On this repo the layer is ~1,500 quads
     and builds in ~20 ms; a whole first run into an empty store takes ~0.2 s. Verified end to end against a running server in a
     scratch copy: the acceptance query, identical quads across a restart, a manual quad surviving
     `POST /dev/graph/reindex`, an edited source reflected after a reindex, and `graph_query` over MCP.
   - *Deviations from the plan:*
     - **Two-batch writer plus read-back verification** instead of one `ApplyDeltas` (F11, above).
     - Fingerprints are computed and stored on `layer:code`, but nothing consumes them yet; a run
       always rebuilds every layer. Skipping fresh layers is the Phase 2 lazy refresh.
     - `*_test.go` files are not indexed (test -> code edges are in the backlog).
     - Module-internal imports of a package with no node (`assets/templates`, which holds only
       generated `*_templ.go`) produce no `imports` edge; they count toward `external_imports`. The
       templates layer (Phase 2) covers that relation with `renders`.
     - A file that does not parse is skipped with a warning and the layer reports `partial`.
     - Once an `ApplyDeltas` call fails, the writer refuses every later write in that process
       (restart to clear), per the F8 contract.
     - `/dev/graph` still loads every quad. With ~1,500 quads (about 950 nodes) the old `cose` visualizer
       takes 10-15 s to lay out and produces an unreadable blob. That is the Phase 5 item
       ("cap at N nodes, expand on double-click"); use the Gizmo console or `graph_query` until then.
2. **Phase 2: Runtime + config + docs layers, done**
   - `routes` (chi.Walk + middleware + FuncForPC mapping), `templates`, `config`, `mcp`, `docs`
     (incl. spec status rows, path/symbol `mentions`).
   - Lazy refresh by fingerprint (docs edits show up without a restart).
   - *Done when:* a route resolves to its handler's file:line, and editing a spec updates the graph
     on the next page load.
   - *Delivered:* `layer_routes.go`, `layer_templates.go`, `layer_config.go`, `layer_mcp.go`,
     `layer_docs.go` and `world.go` in `business/services/codegraph/`; `Indexer.Refresh` and
     `Indexer.Stale`; `controllers.DevGraphPage` (the page load refreshes first); `devmcp.NewHandler`
     takes a refresh hook that every `graph_query` call runs first; the boot indexer is handed the live
     router. The fixture grew config, templates, an MCP stub, docs and a spec (goldens per layer under
     `testdata/golden/`). Verified end to end against a running server in a scratch copy: `GET /api/hello`
     resolves to `func:controllers.HelloAPI`, `controllers/api.go`, line 14; `GET /` → handler → the
     `Home` template; after editing a spec on disk, `GET /dev/graph` (0.17 s, only `docs` rebuilt) puts the
     new row in the graph, and a later `graph_query` over MCP picks up a doc edit the same way; a freshness
     check with nothing stale costs 3-5 ms. All six layers index this repo (~3,300 quads) in ~0.27 s on
     boot.
   - *Deviations from the plan:*
     - **A route registered for every method is one `ANY` route**, not the nine `chi.Walk` reports, so
       the layer walks the router itself (same traversal, one difference).
     - **Handlers resolve by file and line only.** Under inlining Go names a closure after its caller
       (`routes.NewRouter.func2.StripPrefix` for `net/http.StripPrefix`), so a symbol name can point at
       the wrong function; only method-value wrappers (`x.Method-fm`, which have no source) resolve by
       name. A handler that is not application code gets a `handler` label instead of a link.
     - `func --registers--> tool` (the registering function), `tmpl:<dir>.<Name>`, and
       `item:<spec>/<item text slug>` settle the three provisional IDs (reasons in `GRAPH_SCHEMA.md`).
     - **A layer's fingerprint includes its dependencies'**, not only its own inputs, so a layer reads as
       stale whenever what it resolves against changed; a layer that failed is retried only when its
       fingerprint changes, so a persistent error does not re-run on every page load.
     - The `routes` layer is `skipped` (not emptied) when no router is wired, so an offline run cannot
       erase what a running server derived.
     - `documents` is not emitted, and a doc mention that resolves to nothing is not recorded; both are
       listed in `GRAPH_SCHEMA.md`'s open decisions (Phase 5 needs the second).
     - A new dependency: `github.com/a-h/parse` (indirect, templ's parser needs it). The spec called
       `templ/parser/v2` "already a dep", but only templ's runtime was.
     - `input_schema` is derived from the handler's struct tags, not from the SDK's own inference.
     - `/dev/graph` still loads every quad: with ~3,300 quads the old `cose` visualizer is slower than it
       was at ~1,500 (10-15 s then; not re-measured). That is Phase 5; use the Gizmo console or
       `graph_query` until then.
3. **Phase 3: `db` layer, done**
   - `Explorer.Snapshot`, `REFERENTIAL_CONSTRAINTS`, static migration parser, inferred relations,
     table-literal → `reads_table`/`writes_table`, `db:` tag → `maps_to`.
   - Seed a sample migration (users/orders/order_items) in a scratch DB to exercise FK, implicit ref,
     junction, and no-DB (migration-only) paths.
   - *Done when:* ER preset renders from migrations alone, and live DB adds indexes/comments/drift.
   - *Delivered:* `storage/dbexplorer/snapshot.go` (`Explorer.Snapshot`); `sqlschema.go` (tokenizer and DDL
     parser), `dbmodel.go` (merge, drift, inference), `sqlscan.go` (tables out of SQL), `layer_db.go` in
     `business/services/codegraph/`; SQL string facts in the code index; mentions of tables, columns and
     migrations in the docs layer; `internal/graphcheck` (the cross-layer invariants, now shared by the
     repo and fixture tests); `routes.go` hands the indexer `explorer.Snapshot`. The fixture grew the
     spec's sample (one migration: users, products, orders, order_items, with an unenforced `user_id` and
     a composite-key junction) and SQL in `orderstore`; the further cases (`ALTER`s of existing tables, a
     shared primary key, renames and drops, an implicit reference beside a column that names no table,
     drift) are an inline second migration in the tests, so the tree carries no schema beyond that sample. Verified
     against a real MariaDB 12.3: the static parser reproduces what the server makes of those migrations
     exactly (no drift, same index and constraint structure), `Explorer.Snapshot` tests, a running server (goose migrates on boot, 301 db
     quads, all seven layers in ~0.7 s), a database changed by hand with no file changing (the next page
     load shows it as `source=live`, a type `drift`, and a foreign key replacing an inferred reference), a
     database that goes away (the layer falls back to the migrations, `partial`, and heals when it returns),
     and that no row data reaches the graph.
   - *Deviations from the plan:*
     - **No UI preset.** "ER preset renders from migrations alone" is Phase 5's visualizer; here the ER
       data is in the graph and queryable (`has_column`, `fk_to`, the inferred edges), from migrations alone
       and merged with a live database. The old `/dev/graph` page still loads every quad.
     - **The static parser is a tokenizer, not regexes**, and emulates MariaDB's naming (unnamed keys,
       the index InnoDB adds for a foreign key). It reads MySQL/MariaDB DDL only.
     - **Drift is recorded on the elements** (`source` on tables, columns and indexes; `drift` where both
       sides have it and differ), not as separate nodes; the Phase 5 findings derive from those. Type
       comparison is best effort (widths, aliases and bare `decimal` are normalised).
     - **Inferred edges** use the Phase 0 annotation decision (`ann:<hash>`, with `subject`, `object`,
       `predicate`, `method`, `confidence`, `via`); an unnamed foreign key's constraint name is reported only
       when a live database supplies it (MySQL and MariaDB name them differently).
     - **`reads_table`/`writes_table` come from SQL in `storage` and `business` functions**, including
       package-level string constants used by name, but not SQL built at run time. The full
       route → handler → action → function → table chain needs the `calls` layer (Phase 6), so
       acceptance query 2 and 3 are answered only as far as the handler and the functions that touch the
       table until then.
     - The live schema is re-read at most every 5 seconds by freshness checks (an explicit reindex always
       reads afresh), since it can change with no file changing; a database that cannot be reached makes
       the layer `partial`, not failed.
     - Row estimates and auto-increment positions are left out of the snapshot (volatile, and row-derived).
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
| Phase 0 groundwork (.gitignore, spike, vocab) | Yes | | Vocabulary awaits sign-off; spike corrected route IDs, Gizmo query forms, and the writer API (see `GRAPH_SCHEMA.md`) |
| Indexer core + `code` layer | Yes | | Writer applies adds then deletes (two batches) and verifies by read-back because of Cayley F11; fingerprints are stored but the skip-if-fresh logic waits for Phase 2; test files and templ-only packages are not indexed (see Phase 1 deviations) |
| `routes`/`templates`/`config`/`mcp`/`docs` layers | Yes | | Routes collapse catch-alls to `ANY` and resolve by file:line; fingerprints include dependencies; `documents` and unresolved doc references are deferred (see Phase 2 deviations) |
| `db` layer (live + static + inferred + code↔table) | Yes | | No ER preset yet (Phase 5 UI); drift recorded as `source`/`drift` on elements; route → table needs the `calls` layer (see Phase 3 deviations) |
| Agent MCP tools/resources/prompt | No | | Planned |
| Findings + human UI/reports | No | | Planned |
| `calls`/`git` layers, CLI, CI | No | | Planned, opt-in |
| Indexing in production | No | Skipped | Everything under `/dev/*` is gated on `!cfg.IsProd()` by design |
| Indexing row data | No | Skipped | PII risk; schema and metadata only |
