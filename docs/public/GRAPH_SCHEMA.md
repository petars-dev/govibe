# Graph Schema

The vocabulary of the embedded Cayley graph (`/dev/graph`, the `graph_query` MCP tool): what gets
inserted, how nodes are named, and how to query them. The plan lives in
[SPEC_GRAPH_INDEXER](/dev/docs/specs/graph-indexer); this file is the reference the indexer, its tests,
and agents work from.

> **Status: Phase 0 (groundwork).** The conventions below are decided and the graph-store behaviour
> they rest on is pinned by tests, but **nothing is indexed yet**: the store holds only hand-added
> quads. Don't assume a node or predicate exists until its layer is marked *implemented* in
> [Layers](#layers). Rows marked *provisional* have a name from the spec but get their final semantics
> in the phase that first emits them.

## Spike results

Phase 0 ran the IRI / literal / label round trip against the Cayley version in `go.mod` (v0.7.7). The
findings are pinned by `storage/graphdb/contract_test.go`; if that test fails after a dependency
upgrade, read this section before touching the indexer.

| # | Finding | Consequence |
| --- | --- | --- |
| F1 | An IRI and a string are different terms: `<pkg:x>` is not `"pkg:x"`. The "Add a quad" form on `/dev/graph` writes strings, so hand-added quads can never collide with generated nodes. | Nodes, predicates, labels and kinds are IRIs. Descriptive attributes are literals. |
| F2 | Gizmo turns a bare string argument into a *string* term, never an IRI. `g.V('pkg:controllers').Out('imports')` finds nothing in IRI space; predicates need the same treatment. | Query IRIs with `g.IRI('x')` or `'<x>'`. The spec's Phase 1 acceptance query was wrong and is corrected there. See [Querying](#querying). |
| F3 | Typed literals come back from Gizmo as native values (`12`, `true`) and `Has(p, 12)` / `Has(p, true)` match; `Has(p, '12')` does not. `/v2/read` prints them as `"12"^^<schema:Integer>`, `"True"^^<schema:Boolean>`, `"…Z"^^<schema:DateTime>`. Floats print as `"5E-01"^^<schema:Float>`. | Use strings, ints, bools and times. No floats: confidence is an int from 0 to 100. |
| F4 | The JSON format (`/v2/read`, JSON write, `?sub=` filters) cannot tell a string like `<x>` from the IRI `x`. N-Quads can. `quad.StringToValue` also reinterprets `_:x`, `"x"^^<t>` and `"x"@en`. | The vocabulary's string helper wraps any string for which `quad.StringToValue(s) != quad.String(s)` in backticks. |
| F5 | N-Quads, the default format of `/v2/read`, `/v2/write` and `/v2/delete`, cannot carry ASCII control characters, space, `"` or `\` inside an IRI (a backslash survives only before `" ' \ b f n r t`). With a label it fails loudly. Without one a space **silently splits** the IRI: `<route:GET /x> <handled_by> <func:x.X> .` is stored as subject `"<route:GET"`, predicate `"/x>"`. JSON write/delete, the Go API and Gizmo accept any IRI. | IDs never contain those characters (see [IDs](#ids)). Route IDs use `route:GET:/path`, not the spec's original `route:GET /path`. |
| F6 | Every other printable ASCII character round-trips through N-Quads inside an IRI (`{ } \| ^ # ? * : /` included), as do non-ASCII letters. | Chi patterns like `/tables/{table}` and `/assets/*` need no escaping. |
| F7 | A label is a term like any other: the IRI `<layer:code>` and the string `"layer:code"` are different labels. Reads filtered by an IRI label return exactly that label. Deleting a quad requires its exact label (`quad does not exist` otherwise). The same subject, predicate and object under two labels are two quads, and Gizmo `Out()` returns both. | Layers own labels and never touch other quads. Each fact has exactly one owning layer (see [Layers and labels](#layers-and-labels)). |
| F8 | `ApplyDeltas` with `IgnoreDup` and `IgnoreMissing` applies a mix of deletes, adds, already-gone quads and duplicates in one atomic step. The strict path (`ApplyTransaction`, or `ApplyDeltas` without the ignore options) errors on a duplicate or missing quad, rolls back on disk, **and leaves the store's in-process node cache believing the rolled-back nodes exist**: a retry looks successful but resolves to nodes with no names, and the write is gone after a restart. | The writer always uses tolerant `ApplyDeltas` and never retries in-process after an error. See [Writer contract](#writer-contract). |
| F9 | This Gizmo has no `Regex`, `Like`, `Order` or `Select` methods. Pattern matching is `Filter(regex(p, true))` (the `true` is what makes it see IRIs) or `Filter(like(p))`. `Count()` yields a single `null` through both the MCP tool and `/v2/query`; counting is `g.Emit(<path>.ToArray().length)`. | The spec's `.Regex` is corrected, and the agent tools (Phase 4) return counts from Go. |
| F10 | An N-Quads export imports into a fresh store losslessly for strings (quotes, newlines), ints, bools, times and IRIs. | A deterministic N-Quads snapshot (spec backlog) and N-Quads goldens are viable. |

## Conventions

### Terms

| Term | Go | Gizmo | `/v2/read?format=json` | Use for |
| --- | --- | --- | --- | --- |
| IRI | `quad.IRI("pkg:controllers")` | `g.IRI('pkg:controllers')` or `'<pkg:controllers>'` | `<pkg:controllers>` | node IDs, predicates, labels, kinds |
| string | `quad.String("controllers")` | `'controllers'` (a string term) | `controllers` | names, docs, signatures, messages |
| int | `quad.Int(12)` | `12` | `"12"^^<schema:Integer>` | lines, counts, durations |
| bool | `quad.Bool(true)` | `true` | `"True"^^<schema:Boolean>` | flags |
| time | `quad.Time(t)` | RFC 3339 string | `"2026-10-01T12:00:00Z"^^<schema:DateTime>` | `indexed_at` only |

Structural relations (anything the explorer should draw as an edge) have an IRI object. Attribute
predicates have a literal object, so a viewer can hide them by object type.

### IDs

An ID is the IRI `<prefix>:<key>`. IDs are deterministic (the same source gives the same ID on every
run) and **opaque**: never parse one, follow edges and attributes instead.

- **Escaping.** Before an ID is built, percent-encode `%` (as `%25`), ASCII control characters
  (`%00`–`%1F`, `%7F`), space (`%20`), `"` (`%22`) and `\` (`%5C`) in every key segment. That is exactly
  the set F5 shows N-Quads cannot carry, plus `%` so the encoding stays one-to-one. Nothing else is
  escaped.
- **Directories** are repo-relative with `/` separators; the module root is `.`. So the root package is
  `pkg:.` and its `main` is `func:..main`.
- **Methods** include the receiver type without `*`: `func:storage/dbexplorer.Explorer.DescribeTable`.
- **Closures** get no node. A handler that is a closure resolves to its enclosing function.

| Kind | `type` object | ID | Example | Owner layer | Phase |
| --- | --- | --- | --- | --- | --- |
| package | `kind:package` | `pkg:<dir>` | `pkg:business/actions` | code | 1 |
| file | `kind:file` | `file:<path>` | `file:controllers/devdb.go` | code | 1 |
| function, method | `kind:func` | `func:<dir>.<Name>` or `func:<dir>.<Recv>.<Name>` | `func:controllers.HelloAPI` | code | 1 |
| type | `kind:type` | `typ:<dir>.<Name>` | `typ:storage/dbexplorer.Explorer` | code | 1 |
| build | `kind:build` | `build:latest` | `build:latest` | meta | 1 |
| layer run | `kind:layer` | `layer:<name>` | `layer:code` | meta | 1 |
| route | `kind:route` | `route:<METHOD>:<path>` | `route:GET:/dev/db/api/tables/{table}` | routes | 2 |
| middleware | `kind:middleware` | `mw:<dir>.<Name>` | `mw:middleware.RequestLogger` | routes | 2 |
| template | `kind:template` | `tmpl:<pkg>.<Name>` | `tmpl:templates.Home` | templates | 2 |
| MCP tool | `kind:mcp_tool` | `tool:<name>` | `tool:graph_query` | mcp | 2 |
| config key | `kind:config_key` | `cfg:<dotted.key>` | `cfg:database.type` | config | 2 |
| env var | `kind:env_var` | `env:<NAME>` | `env:DATABASE_TYPE` | config | 2 |
| doc | `kind:doc` | `doc:<path>` | `doc:docs/public/ARCHITECTURE.md` | docs | 2 |
| spec | `kind:spec` | `spec:<slug>` | `spec:graph-indexer` | docs | 2 |
| spec item | `kind:spec_item` | `item:<slug>/<row>` (provisional) | `item:graph-indexer/1` | docs | 2 |
| table | `kind:table` | `table:<name>` | `table:orders` | db | 3 |
| column | `kind:column` | `col:<table>.<column>` | `col:orders.user_id` | db | 3 |
| index | `kind:index` | `idx:<table>.<name>` | `idx:orders.ix_user` | db | 3 |
| migration | `kind:migration` | `mig:<version>` | `mig:0001_init` | db | 3 |
| finding | `kind:finding` | `finding:<rule>:<subject id>` | `finding:layering:func:controllers.GetOrder` | findings | 5 |

Go types use `typ:` rather than `type:` so the prefix, the `type` predicate and the `kind:type` object
are three different IRIs.

### Layers and labels

Every quad the indexer writes carries one label: the IRI `layer:<name>` of the layer that owns it.

- The `layer:` namespace is reserved. A reindex deletes and adds quads **only** under the layer's own
  label. Unlabelled quads, quads with any other IRI label, and quads with a *string* label are never
  read for deletion, even one whose text is `layer:code` (F7).
- **One owner per (subject, predicate).** A fact is emitted by exactly one layer, so Gizmo never returns
  it twice. A layer may point at another layer's node (`routes` emits `route --handled_by--> func`), but
  the node's `type`, `name` and attributes belong to the layer that owns its kind.
- **No dangling references.** Every IRI used as the object of a structural predicate has a `type` quad
  in some layer. Phase 1 adds an invariant test for it.
- `layer:<name>` is also a node. It carries that layer's `quads`, `fingerprint`, `status`,
  `duration_ms`, `indexed_at`, `added` and `removed`, owned by `layer:meta`, so
  `g.V('<layer:code>')` is the freshness record for the code layer.
- Output is deterministic and sorted. Timestamps and git state exist only on `build:latest` and the
  `layer:<name>` nodes, under `layer:meta`, which golden tests exclude.

### Structural predicates

All predicates are bare IRIs (`<imports>`, not `<gv:imports>`).

| Predicate | Subject → object | Owner | Phase | Status |
| --- | --- | --- | --- | --- |
| `type` | any node → kind | the node's owner | 1 | final |
| `defined_in` | func, type → file | code | 1 | final |
| `in_package` | file → package | code | 1 | final |
| `imports` | package → package (module-internal) | code | 1 | final |
| `has_layer` | build → layer run | meta | 1 | final (new) |
| `handled_by` | route → func | routes | 2 | provisional |
| `uses_mw` | route → middleware | routes | 2 | provisional |
| `renders` | func → template | templates | 2 | provisional |
| `uses_config` | func, package → config key | config | 2 | provisional |
| `reads_env` | config key, func → env var | config | 2 | provisional |
| `registers` | func → MCP tool | mcp | 2 | provisional (spec calls this link "tool → backing function"; direction fixed in Phase 2) |
| `has_item` | spec → spec item | docs | 2 | provisional (new) |
| `documents`, `mentions`, `covers` | doc, spec, spec item → code, route, config key, table | docs | 2 | provisional |
| `reads_table`, `writes_table` | func → table | db | 3 | provisional |
| `maps_to` | type → table | db | 3 | provisional |
| `has_column`, `has_index` | table → column, index | db | 3 | provisional |
| `fk_to` | column → column, table → table | db | 3 | provisional |
| `implicit_ref`, `many_to_many` | column → column, table → table (inferred) | db | 3 | provisional |
| `introduced_in` | table, column, index → migration | db | 3 | provisional |
| `calls` | func → func | calls | 6 | provisional |
| `about` | finding → subject node | findings | 5 | provisional |

### Attribute predicates

| Predicate | Literal | On | Phase |
| --- | --- | --- | --- |
| `name` | string: short display name (Go package name, symbol name, tool name) | every node | 1 |
| `doc` | string: first sentence of the godoc, at most 240 characters | package, func, type | 1 |
| `signature` | string | func, type | 1 |
| `line` | int | func, type | 1 |
| `exported` | bool | func, type | 1 |
| `path` | string: repo-relative file or directory (for a route: the URL path) | package, file, route | 1 |
| `import_path` | string | package | 1 |
| `arch_layer` | string, see [Package classification](#package-classification) | package | 1 |
| `lines` | int | file | 1 |
| `generated` | bool | file | 1 |
| `git_sha`, `dirty`, `indexed_at`, `duration_ms`, `indexer_version` | string, bool, time, int, string | build | 1 |
| `quads`, `fingerprint`, `status`, `duration_ms`, `indexed_at`, `added`, `removed`, `error` | int, string, string, int, time, int, int, string | layer run | 1 |
| `method`, `dev_only` | string, bool | route | 2 |
| `description`, `input_schema` | string, string (JSON) | MCP tool | 2 |
| `default`, `sensitive` | string (absent when `sensitive`), bool | config key, env var | 2 |
| `title`, `status`, `why_skipped` | string, string, string | doc, spec, spec item | 2 |
| `data_type`, `nullable`, `unique`, `engine`, `comment`, `key`, `extra`, `ordinal` | string, bool, bool, string, string, string, string, int | table, column, index | 3 |
| `version`, `applied` | string, bool | migration | 3 |
| `rule`, `severity`, `message` | string, string, string | finding | 5 |

`data_type` is not `type`, which is taken by the kind predicate. Sensitive keys (`key`, `secret`,
`password`, `token`, `dsn`, case-insensitive) get no `default`, no value, and no DSN, ever.

### Package classification

`arch_layer` is the architecture layer of a package, from its directory; the first matching row wins.
It is not an indexer layer despite the shared word.

| Directory | `arch_layer` |
| --- | --- |
| `controllers/**` | `controllers` |
| `business/**` | `business` |
| `storage/**` | `storage` |
| `middleware/**` | `middleware` |
| `routes/**`, the module root `.`, `cmd/**` | `wiring` |
| `devmcp/**` | `dev` |
| `assets/**` | `views` |
| anything else (`config`, `logger`, …) | `support` |

The layering rule (provisional, finalized in Phase 5): `controllers` must not import `storage`;
`storage` must not import `business` or `controllers`; `wiring` and `dev` may import anything. This is
how SPEC_GRAPH_INDEXER's "allow-list for wiring packages (`routes`, `main`, `devmcp`)" is expressed.

## Layers

| Layer | Label | Owns | Source | Phase | Status |
| --- | --- | --- | --- | --- | --- |
| code | `layer:code` | package, file, func, type | `go/parser` over the repo | 1 | planned |
| meta | `layer:meta` | build, layer run | the indexer itself | 1 | planned |
| routes | `layer:routes` | route, middleware | `chi.Walk` at runtime | 2 | planned |
| templates | `layer:templates` | template | `templ/parser/v2` | 2 | planned |
| config | `layer:config` | config key, env var | `config/env.yml` and selector scan | 2 | planned |
| mcp | `layer:mcp` | MCP tool | `mcp.AddTool` calls | 2 | planned |
| docs | `layer:docs` | doc, spec, spec item | goldmark over `docs/`, README, SKILL.md | 2 | planned |
| db | `layer:db` | table, column, index, migration | migrations plus live `information_schema` | 3 | planned |
| findings | `layer:findings` | finding | derived from the other layers | 5 | planned |
| calls | `layer:calls` | (`calls` edges) | `x/tools/go/packages` | 6, opt-in | planned |
| git | `layer:git` | (file attributes) | `git log` | 6, opt-in | planned |

The code layer never walks `tmp/`, `.git/`, `vendor/`, any `testdata/` directory, or a directory
containing its own `go.mod`. Without that rule the [fixture](#fixture) would leak into the real graph.
Generated `*_templ.go` files are skipped entirely, so a package containing only those has no node.
Other generated Go files are indexed with `generated=true`.

## Writer contract

What the Phase 1 writer does, given F7 and F8:

1. Build the layer's quads in memory as typed terms (`quad.IRI`, `quad.String`, …), never through the
   JSON or N-Quads parsers.
2. Sort and deduplicate by (subject, predicate, object).
3. Read the quads currently under the layer's label with
   `shape.FilterQuads(nil, nil, nil, []quad.Value{label})` and `graph.NewResultReader`.
4. Diff on the full quad, label included.
5. Apply the deletes and adds with a single
   `QuadStore.ApplyDeltas(deltas, graph.IgnoreOpts{IgnoreDup: true, IgnoreMissing: true})`: atomic,
   idempotent, and the diff's size is the "+38/−12" the UI shows. Never `ApplyTransaction`.
6. If `ApplyDeltas` returns an error, log it, mark the layer `status=error`, and do not retry in this
   process (F8). The server keeps running; a restart clears the cache.
7. Never read for deletion, or delete, a quad whose label is not a `layer:*` IRI.

## Querying

Verified against hand-written IRI quads in `contract_test.go`. Nothing here returns real project data
until the code layer lands in Phase 1.

```js
// outgoing and incoming edges
g.V('<pkg:controllers>').Out('<imports>').All()
g.V(g.IRI('pkg:business/actions')).In(g.IRI('imports')).All()

// every function, every exported function, by name pattern
g.V().Has(g.IRI('type'), g.IRI('kind:func')).All()
g.V().Has(g.IRI('exported'), true).All()
g.V().Filter(like('func:controllers.%')).All()
g.V().Filter(regex('^func:controllers[.]', true)).All()

// what do we know about a node: its predicates, then one with the predicate tagged
g.V(g.IRI('pkg:controllers')).OutPredicates().All()
g.V(g.IRI('pkg:controllers')).Out(null, 'predicate').All()

// counting (Count() returns null in this Gizmo)
g.Emit(g.V().Has(g.IRI('type'), g.IRI('kind:func')).ToArray().length)
```

- Both IRI spellings are equivalent: `g.IRI('x')` or `'<x>'`. A bare `'x'` is a string.
- Over HTTP, `GET /dev/graph/api/v2/read?format=json&sub=%3Cpkg:controllers%3E` returns one node's
  outgoing quads. `sub`, `pred`, `obj` and `label` take comma-separated terms, IRIs in angle brackets,
  strings bare. The visualizer can use this to expand a node instead of loading every quad.
- A result without an `id` (`{}`) means a node whose name could not be resolved: see F8.
- `/v2/read` JSON escapes `<` as `<`; a JSON parser undoes it.

## Fixture

`business/services/codegraph/testdata/fixture/` is a miniature GoVibe-shaped app (module
`example.com/fixture`, standard library only) that the indexer's golden tests will run against. It
compiles, is never run, and is invisible to the main module (`go list ./...` and `go vet ./...` skip
`testdata`). Golden N-Quads arrive with the code layer in Phase 1; the fixture's non-Go inputs
(migrations, config, docs) arrive with the phase that indexes them.

| Fixture file | What it exercises |
| --- | --- |
| `main.go` | root package `pkg:.`, `arch_layer=wiring`, package doc |
| `routes/routes.go` | wiring; imports every other layer; four routes (`GET /api/hello`, `GET /api/orders`, `GET /api/orders/{id}`, `/dev/mcp`) and a middleware wrapper |
| `controllers/hello.go` | acceptance query 1: `HelloAPI` calls `actions.Hello`, and no table is reachable |
| `controllers/orders.go` | acceptance query 2: `ListOrders` goes handler → service → store (clean); `GetOrder` calls the store directly (**layering violation**, on purpose); handler factories returning closures; unexported `writeJSON` |
| `business/actions/hello.go` | single-function action; a two-sentence doc for first-sentence extraction |
| `business/services/orders/orders.go` | interface type, struct type, pointer and value receivers, an unexported method |
| `storage/orderstore/orderstore.go` | SQL string literals (read and write) and `db:"…"` struct tags for the Phase 3 table edges |
| `storage/orderstore/zz_generated.go` | a `// Code generated … DO NOT EDIT.` file that is indexed with `generated=true` |
| `middleware/logging.go` | middleware function |
| `devmcp/devmcp.go` | `arch_layer=dev`; a dev package importing storage |
| `assets/templates/home_templ.go` | generated templ output that must be skipped; the package has no other Go file, so it must not appear |

Edit the fixture only together with its goldens, in one commit.

## Open decisions

Decided no later than the phase named.

| Phase | Question | Leading option |
| --- | --- | --- |
| 1 | How are imports of packages outside the module represented? | Count them in an `external_imports` int on the package; add `pkg:ext/<import path>` nodes only if agents ask "who imports X". |
| 2 | Method token for routes registered with `Handle` and no method | Use what `chi.Walk` reports, normalised to uppercase. |
| 2 | Direction and predicate for tool → backing function | `tool --registers--> func`, flipped if the query reads worse. |
| 3 | How is an *inferred* edge marked? A triple has no room for method or confidence. | The edge stays a plain triple so traversals are simple; inferred ones are recorded on an annotation node `ann:<hash>` with `subject`, `predicate`, `object`, `method` and `confidence` (0–100). Phase 1 and 2 emit no inferred edges. |
| 5 | Final layering rule and allow-list | The rule under [Package classification](#package-classification). |

When Phase 1 lands, also update the `graph_query` tool description to show the `'<iri>'` idiom: today
its example (`g.V('alice').Out('knows')`) only works for hand-added string quads.
