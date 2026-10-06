# Graph Schema

The vocabulary of the embedded Cayley graph (`/dev/graph`, the `graph_query` MCP tool): what gets
inserted, how nodes are named, and how to query them. The plan lives in
[SPEC_GRAPH_INDEXER](/dev/docs/specs/graph-indexer); this file is the reference the indexer, its tests,
and agents work from.

> **Status: Phase 3 (the `db` layer).** Every boot, every `POST /dev/graph/reindex`, and (for stale
> layers only) every `/dev/graph` page load and `graph_query` call fills the store with the `code`,
> `routes`, `templates`, `config`, `mcp`, `db` and `docs` layers plus the `meta` layer: packages, files,
> functions, types and imports; routes, middleware and their handlers; templ components; config keys and
> environment variables with their usage; MCP tools; the database schema (tables, columns, indexes,
> foreign keys, migrations, relations the schema implies but does not declare) and which code reads and
> writes which table; docs, specs and their status rows, with what they mention. The `findings`, `calls`
> and `git` layers are not built yet. Don't assume a node or predicate exists until its layer is marked
> *implemented* in [Layers](#layers). Hand-added quads (the "Add a quad" form) are never touched by a
> reindex.

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
| F11 | **A single `ApplyDeltas` batch that mixes deletes and adds silently loses data**, even in the tolerant mode and with no error. Deleting two `type kind:func` quads and adding a third in one batch drops the add; a plain label scan no longer sees it. In 150 randomized rewrites of a small layer, 147 left the store missing quads or holding nameless nodes. The same diffs applied as two batches, in either order, were never wrong (0 of 150). Found in Phase 1 when a rename plus a file delete lost two functions from `Has(type, kind:func)`. | The writer applies a diff as two batches, **adds first, then deletes**, and reads the layer back afterwards (see [Writer contract](#writer-contract)). Pinned by `TestMixedBatchLosesAdds` and `TestReplaceNeverCorruptsTheStore`. |

## The storage backend

The store is Cayley's `bolt` backend: one file, `docs/graph/indexes.bolt`, inside the dev server's process
(bolt allows one process at a time, and `graphdb.Open` gives up after 5 seconds instead of waiting for the
lock, pinned by `TestOpenGivesUpOnALockedStore`). That backend wraps the archived `github.com/boltdb/bolt`,
which casts pointers in ways Go's `checkptr` check rejects. `go test -race` turns `checkptr` on, and the
first bucket creation then aborts with `fatal error: checkptr: converted pointer straddles multiple
allocations`. Without `-race` nothing is wrong. To race the suite, switch only that check off:

```sh
go test -race -gcflags=all=-d=checkptr=0 ./...
```

Tried and not adopted: Cayley's untagged `master` has a `bbolt` backend over the maintained
`go.etcd.io/bbolt` that makes plain `-race` pass and opens existing store files, but it needs an untagged
Cayley commit, `quad` v1.3.0 (typed literals then print as `xsd:integer`, not `schema:Integer`) and source
changes for Cayley's new iterator API, and it fixes neither Cayley bug below (F8, F11).

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
- **Duplicate names.** Two declarations can share an ID (several `init` functions, or one function in
  files with different build tags). The first keeps the plain ID; the second and later get `#2`, `#3`,
  ... in file-path then source order, so `func:..init`, `func:..init#2`. The suffix is stable for
  identical sources, but inserting an earlier duplicate renumbers the later ones.

| Kind | `type` object | ID | Example | Owner layer | Phase |
| --- | --- | --- | --- | --- | --- |
| package | `kind:package` | `pkg:<dir>` | `pkg:business/actions` | code | 1 |
| file | `kind:file` | `file:<path>` | `file:controllers/devdb.go`, `file:assets/templates/home.templ` | code (`.go`), templates (`.templ`) | 1, 2 |
| function, method | `kind:func` | `func:<dir>.<Name>` or `func:<dir>.<Recv>.<Name>` | `func:controllers.HelloAPI` | code | 1 |
| type | `kind:type` | `typ:<dir>.<Name>` | `typ:storage/dbexplorer.Explorer` | code | 1 |
| build | `kind:build` | `build:latest` | `build:latest` | meta | 1 |
| layer run | `kind:layer` | `layer:<name>` | `layer:code` | meta | 1 |
| route | `kind:route` | `route:<METHOD>:<path>`; `<METHOD>` is `ANY` for a route registered for every method | `route:GET:/dev/db/api/tables/{table}`, `route:ANY:/dev/mcp` | routes | 2 |
| middleware | `kind:middleware` | `mw:<dir>.<Name>` for application code, `mw:<import path>.<Name>` for a library | `mw:middleware.RequestLogger`, `mw:github.com/go-chi/chi/v5/middleware.RequestID` | routes | 2 |
| template | `kind:template` | `tmpl:<dir>.<Name>`, or `tmpl:<dir>.<Recv>.<Name>` for a method component | `tmpl:assets/templates.Home` | templates | 2 |
| MCP tool | `kind:mcp_tool` | `tool:<name>` | `tool:graph_query` | mcp | 2 |
| config key | `kind:config_key` | `cfg:<dotted.key>` | `cfg:database.type` | config | 2 |
| env var | `kind:env_var` | `env:<NAME>` | `env:DATABASE_TYPE` | config | 2 |
| doc | `kind:doc` | `doc:<path>` | `doc:docs/public/ARCHITECTURE.md`, `doc:README.md` | docs | 2 |
| spec | `kind:spec` | `spec:<slug>`: `docs/specs/SPEC_<NAME>.md` becomes the slug of `<NAME>`, the same slug `/dev/docs/specs/<slug>` uses | `spec:graph-indexer` | docs | 2 |
| spec item | `kind:spec_item` | `item:<spec slug>/<slug of the Item cell>`, at most 60 characters, `-2`, `-3` on a repeat | `item:graph-indexer/indexer-core-code-layer` | docs | 2 |
| table | `kind:table` | `table:<name>` | `table:orders` | db | 3 |
| column | `kind:column` | `col:<table>.<column>` | `col:orders.user_id` | db | 3 |
| index | `kind:index` | `idx:<table>.<name>`; the primary key is `idx:<table>.PRIMARY` | `idx:orders.ix_orders_user` | db | 3 |
| migration | `kind:migration` | `mig:<file name without .sql>` (decided: the version alone, `0001`, is not what people call it) | `mig:0001_init` | db | 3 |
| annotation | `kind:annotation` | `ann:<12 hex characters>`: a hash of the subject, predicate and object of the inferred edge it describes | `ann:31db341e624c` | db | 3 |
| finding | `kind:finding` | `finding:<rule>:<subject id>` | `finding:layering:func:controllers.GetOrder` | findings | 5 |

Go types use `typ:` rather than `type:` so the prefix, the `type` predicate and the `kind:type` object
are three different IRIs.

### Layers and labels

Every quad the indexer writes carries one label: the IRI `layer:<name>` of the layer that owns it.

- The `layer:` namespace is reserved. A reindex deletes and adds quads **only** under the layer's own
  label. Unlabelled quads, quads with any other IRI label, and quads with a *string* label are never
  read for deletion, even one whose text is `layer:code` (F7).
- **One owner per (subject, predicate).** A fact is emitted by exactly one layer, so Gizmo never returns
  it twice. A layer may point at another layer's node (`routes` emits `route --handled_by--> func`) and
  may use its own predicate on another layer's node as the subject (`config` emits
  `func --uses_config--> cfg`, `templates` emits `func --renders--> tmpl`, `mcp` emits
  `func --registers--> tool`), but a node's `type`, `name` and attributes belong to the layer that owns
  its kind. The kind `file` has two owners, split by subject: the `code` layer owns `.go` files and the
  `templates` layer owns `.templ` files.
- **No dangling references.** Every IRI used as the object of a structural predicate has a `type` quad
  in some layer. A layer only points at nodes it resolved from the same index the other layer is built
  from, so this holds without layers reading each other's quads. `TestWholeGraphInvariants` checks it,
  and that every edge joins the kinds it should, over this repository with every layer running.
- `layer:<name>` is also a node. It carries that layer's `quads`, `fingerprint`, `status`,
  `duration_ms`, `indexed_at`, `added` and `removed`, owned by `layer:meta`, so
  `g.V('<layer:code>')` is the freshness record for the code layer. It describes the layer's own last
  run, which may be older than the build's when only other layers were stale.
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
| `handled_by` | route → func (absent when the handler is not application code) | routes | 2 | final |
| `uses_mw` | route → middleware (set semantics: the order is not represented) | routes | 2 | final |
| `implemented_by` | middleware → func (absent for a library middleware) | routes | 2 | final (new) |
| `renders` | func, template → template | templates | 2 | final |
| `uses_config` | func, package → config key | config | 2 | final |
| `reads_env` | config key, func → env var | config | 2 | final |
| `registers` | func → MCP tool | mcp | 2 | final: the function that calls `mcp.AddTool`, reversed from the spec's "tool → backing function" so it reads "this function registers that tool"; `g.V(tool).In('<registers>')` answers the spec's question |
| `has_item` | spec → spec item | docs | 2 | final (new) |
| `mentions` | doc, spec → any node the text names; doc, spec → doc, spec for a link | docs | 2 | final |
| `covers` | spec item → any node its row's backticks name | docs | 2 | final |
| `documents` | doc, spec → code, route, config key, table | docs | | **not emitted**: no signal says "this doc is *the* doc of X" without inventing a convention; `mentions` and `covers` answer "which doc covers this file" |
| `reads_table`, `writes_table` | func → table | db | 3 | final |
| `maps_to` | type → table | db | 3 | final |
| `has_column` | table → column, index → column (an index's columns, which are the table's own) | db | 3 | final |
| `has_index` | table → index | db | 3 | final |
| `fk_to` | column → column, and table → table (once per pair, however many columns) | db | 3 | final |
| `implicit_ref` | column → column, table → table (inferred; the edge's annotation says how sure) | db | 3 | final |
| `many_to_many` | table → table, in both directions, through a junction table (inferred; see its annotation) | db | 3 | final |
| `introduced_in` | table, column, index → migration (absent for something only a live database has) | db | 3 | final |
| `subject`, `object` | annotation → the column or table an inferred edge starts and ends at | db | 3 | final (new) |
| `via` | annotation → the junction table of a `many_to_many` edge | db | 3 | final (new) |
| `calls` | func → func | calls | 6 | provisional |
| `about` | finding → subject node | findings | 5 | provisional |

### Attribute predicates

| Predicate | Literal | On | Phase |
| --- | --- | --- | --- |
| `name` | string: short display name: Go package name, file base name, symbol name (`Recv.Method` for a method), tool name, `latest` for the build | every node | 1 |
| `doc` | string: first sentence of the godoc (for a template, of the `//` comment directly above it), at most 240 characters | package, func, type, template | 1, 2 |
| `signature` | string, one line, at most 500 characters. A template is `templ Name(params)`. A func is its declaration without body (`func (s *Service) List(ctx context.Context) ([]Order, error)`); a type is `type Name`, plus `struct`, `interface`, or the underlying type (`type Handler func(string) error`, `type Set[T comparable] struct`) | func, type | 1 |
| `line` | int: for a func or type its declaration; template: the `templ` line; config key: its line in `env.yml`; MCP tool: the `mcp.AddTool` call; spec item: its row in the markdown | func, type, template, config key, MCP tool, spec item | 1, 2 |
| `exported` | bool: the name starts with an upper-case letter (a method counts by its own name, not its receiver type's) | func, type | 1 |
| `path` | string: repo-relative file or directory (for a route: the URL path; for a config key: `config/env.yml`; for a tool, template or spec item: the file that defines it) | package, file, route, template, config key, MCP tool, doc, spec, spec item | 1, 2 |
| `import_path` | string | package | 1 |
| `arch_layer` | string, see [Package classification](#package-classification) | package | 1 |
| `lines` | int: number of lines (a final line without a newline counts) | file | 1 |
| `external_imports` | int: distinct import paths with no package node: the standard library, third-party modules, and module packages the code layer does not index (a package holding only generated `*_templ.go`) | package | 1 |
| `generated` | bool: the file carries the `// Code generated ... DO NOT EDIT.` marker | file | 1 |
| `git_sha`, `dirty`, `indexed_at`, `duration_ms`, `indexer_version`, `quads`, `added`, `removed` | string, bool, time, int, string, int, int, int. `git_sha` and `dirty` are absent when git is unavailable. `quads`, `added` and `removed` total the data layers (not `meta`) and are relative to the previous run | build | 1 |
| `quads`, `fingerprint`, `status`, `duration_ms`, `indexed_at`, `added`, `removed`, `error` | int, string, string, int, time, int, int, string. `status` is `ok`, `partial` (indexed, but some inputs were skipped, e.g. a file that does not parse), `skipped` (not applicable here: the `routes` layer with no router wired; nothing was written or deleted, and `note` says why) or `error` (nothing was written; the layer's previous quads stay). `quads`, `added` and `removed` are absent on `error` and `skipped`; `note` appears only on `skipped`. `fingerprint` is 16 hex characters over the sorted path, mtime and size of the layer's inputs, taken before they are read | layer run | 1 |
| `method`, `dev_only` | string (`GET`, `POST`, ..., or `ANY`), bool (the path is `/dev` or under `/dev/`) | route | 2 |
| `handler` | string: the Go runtime's name for a handler that is **not** application code (`net/http.StripPrefix`, `github.com/modelcontextprotocol/go-sdk/mcp.(*StreamableHTTPHandler).ServeHTTP`); present exactly when `handled_by` is absent, so a route with no code link still says what serves it | route | 2 |
| `description`, `input_schema` | string, string (JSON). The schema is derived from the handler's input struct: `json` names, `omitempty` makes a property optional, the `jsonschema` tag is its description; `{"type":"object"}` for `struct{}`; absent when the handler does not name a struct of the registering package | MCP tool | 2 |
| `default`, `sensitive` | string (absent when `sensitive`, and absent when empty), bool | config key, env var | 2 |
| `declared` | bool: the key is a leaf of `config/env.yml`, or the variable appears as `${NAME}` there. `false` means the code reads it but nothing declares it | config key, env var | 2 (new) |
| `field` | string: the Go field the key binds to, from the yaml tags of `Config` (`Config.Database.MariaDB.DSN`); absent for keys only reachable through `config.Get` | config key | 2 (new) |
| `title`, `status`, `why_skipped`, `note` | string, string, string, string. A doc's `title` is its first `#` heading (the file name if none). A spec's `status` is `complete` (every row implemented or skipped), `planned` (none implemented) or `in_progress`. A spec item's `status` is `implemented`, `partial`, `skipped` or `planned`, read from the Implemented and Skipped cells; `why_skipped` is the Why cell of a skipped row and `note` the Why cell of any other row | doc, spec, spec item | 2 |
| `data_type` | string: the column type, lower case (`varchar(255)`, `bigint(20) unsigned` from a live database, `bigint unsigned` from a migration) | column | 3 |
| `nullable` | bool | column | 3 |
| `key` | string: `PRI` (in the primary key), `UNI` (the only column of a unique index) or `MUL` (first column of another index), else absent; the server's word when live | column | 3 |
| `extra` | string: `auto_increment`, `on update current_timestamp()`, `generated` | column | 3 |
| `default` | string, as the server spells it (`'new'`, `0.00`, `current_timestamp()`); absent when there is none and **always absent for a column whose name matches the sensitive pattern** | column | 3 |
| `ordinal` | int: 1-based position in the table | column | 3 |
| `comment` | string: the table's or column's `COMMENT` | table, column | 3 |
| `engine` | string | table | 3 |
| `unique`, `primary` | bool | index | 3 |
| `columns` | string: the index's columns in order, comma-separated (the `has_column` edges are the same set, unordered) | index | 3 |
| `implicit` | bool, present (true) only for an index the server added for a foreign key | index | 3 |
| `source` | string: `migration` (only the migrations describe it), `live` (only the database has it) or `both`; with no database configured everything is `migration` | table, column, index | 3 (new) |
| `drift` | string, present only when both sides have it and disagree: `data_type: migration int, live bigint(20)`, `nullable: ...`, `columns: ...`, `unique: ...`; on a column also `foreign key to <t> only in migration` or `only in live` | column, index | 3 (new) |
| `cardinality` | string: `one_to_one` when the column is a single-column foreign key that is itself unique (a primary key or unique index of that column alone), else `one_to_many`. Only on single-column foreign keys | column | 3 (new) |
| `fk_name`, `on_delete`, `on_update` | string, string, string: the constraint's name and referential actions, on the foreign key's (first) source column. `fk_name` is absent for a constraint the migrations left unnamed and no database has named; the rules are absent when a migration wrote none and no database reports one | column | 3 (new) |
| `version`, `applied` | string (`0002`, as in the file name), bool. `applied` is read from goose's `goose_db_version` and absent when there is no database or it has no such table | migration | 3 |
| `predicate`, `method`, `confidence` | string (the inferred predicate), string, int 0 to 100: how an inferred edge was inferred and how sure that is. `method` is `name_match`, `junction_pk` or `junction_unique` | annotation | 3 (new) |
| `rule`, `severity`, `message` | string, string, string | finding | 5 |

`data_type` is not `type`, which is taken by the kind predicate. A config key or environment variable
whose name matches `key`, `secret`, `password`, `token` or `dsn` (case-insensitive) is `sensitive`: it
gets no `default`, no value, and no DSN, ever, and neither does any variable bound to it. `.env*` files
are never read.

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
| code | `layer:code` | package, file, func, type | `go/parser` over the repo | 1 | **implemented** |
| meta | `layer:meta` | build, layer run | the indexer itself | 1 | **implemented** |
| routes | `layer:routes` | route, middleware | the live chi router, walked at runtime | 2 | **implemented** |
| templates | `layer:templates` | template, `.templ` file | `templ/parser/v2` | 2 | **implemented** |
| config | `layer:config` | config key, env var | `config/env.yml` and the code index | 2 | **implemented** |
| mcp | `layer:mcp` | MCP tool | `mcp.AddTool` calls, from the code index | 2 | **implemented** |
| docs | `layer:docs` | doc, spec, spec item | goldmark over `docs/public`, `docs/specs`, `README.md`, `SKILL.md` | 2 | **implemented** |
| db | `layer:db` | table, column, index, migration, annotation | the migrations in `storage/migrations/sql`, merged with the live database's schema when one is configured | 3 | **implemented** |
| findings | `layer:findings` | finding | derived from the other layers | 5 | planned |
| calls | `layer:calls` | (`calls` edges) | `x/tools/go/packages` | 6, opt-in | planned |
| git | `layer:git` | (file attributes) | `git log` | 6, opt-in | planned |

The code layer never walks `tmp/`, `.git/`, `vendor/`, any `testdata/` directory, or a directory
containing its own `go.mod`. Without that rule the [fixture](#fixture) would leak into the real graph.
Generated `*_templ.go` files are skipped entirely, so a package containing only those has no node,
and an import of such a package produces no `imports` edge (it counts toward `external_imports`).
Other generated Go files are indexed with `generated=true`. Test files (`*_test.go`), files whose name
starts with `.` or `_`, dot and underscore directories, and anything reached through a symlink are not
indexed; a root-level `tmp/` and `vendor/` are skipped, as is `node_modules`. A file that does not parse
is skipped with a warning and the layer's status becomes `partial`. Caps: 2 MiB per file, 24 directory
levels, 20,000 files (more fails the layer rather than index half the repo).

### Dependencies and freshness

The code layer's single parse also records what the other layers resolve against (function spans,
struct fields, qualified references, `cfg.A.B` chains, `os.Getenv` and `config.Get` calls, `mcp.AddTool`
calls), so nothing is parsed twice. A layer points only at nodes it can resolve from those same indexes.

| Layer | Depends on | Own inputs (what its fingerprint covers) |
| --- | --- | --- |
| code | | every indexed `.go` file, and `go.mod` |
| routes | code | the route table: method, path, handler and middleware identity of every route |
| templates | code | every `.templ` file |
| config | code | `config/env.yml` |
| mcp | code | none (it reads the code index) |
| db | code | every `.sql` under `storage/migrations/sql`, and the live schema (see below) |
| docs | code, routes, templates, config, mcp, db | `README.md`, `SKILL.md`, every `.md` under `docs/public` and `docs/specs` |

A layer's fingerprint (16 hex characters, on `layer:<name>`) hashes its own inputs (path, mtime, size)
together with its dependencies' fingerprints, so it changes exactly when something the layer was built
from changed. A layer is **stale** when it has never been built or its current fingerprint differs from
the stored one; a layer that failed is not retried until its fingerprint changes (an explicit reindex
retries at once). Fingerprints are taken before any input is read, so an edit that lands mid-run reads as
stale next time, never falsely fresh.

| Trigger | What runs |
| --- | --- |
| server boot (after the router is wired, in a goroutine) | every layer |
| `POST /dev/graph/reindex` | every layer, ignoring caches |
| `GET /dev/graph` (the page) and every `graph_query` call | only the stale layers, usually none: a freshness check is a few milliseconds |

This is what makes an edit to `docs/**`, which the dev server's file watcher ignores, show up on the next
page load without a restart. The `db` layer is the one whose input can change with no file changing: its
fingerprint includes a hash of the live schema, which a freshness check reads, but a read is reused for 5
seconds (an explicit reindex always reads afresh), so a burst of page loads or tool calls asks the
database once. If the database cannot be reached its fingerprint is the constant `unavailable`: the
layer is rebuilt once from the migrations alone, reported `partial` with the reason, and left alone until
the database answers again.

### What the Phase 2 layers read

**routes.** The router is walked the way `chi.Walk` walks it (sub-routers, inline middleware groups, the
middleware stack of every level), with one difference: a route registered for every method (`Handle`)
is one route with method `ANY`, where `chi.Walk` reports nine. Each handler is resolved through the Go
runtime: its function's file and line are mapped onto the code index, so a closure resolves to the
function that contains it (`api.Get("/meta", controllers.DevDBMeta(ex))` is handled by
`func:controllers.DevDBMeta`). Resolution is by file and line, never by symbol name, because the
compiler names an inlined closure after its caller. The path the binary was compiled from is recovered
from where the indexer itself was compiled, so it works for absolute paths and under `-trimpath`. A
handler that is not application code (a stdlib or library handler, or a wrapper such as
`http.StripPrefix(...)`, or a constructor that returns a library type such as `devmcp.NewHandler`)
gets no `handled_by`, and a `handler` attribute names it instead. Middleware is a set: the order of
the stack is not represented. With no router wired (an offline run) the layer is `skipped` and its stored
quads are left alone, and the docs layer reports `partial` because route mentions cannot be resolved.

**templates.** `.templ` files are parsed with `templ/parser/v2`. Components become `tmpl:` nodes, with the
comment directly above as `doc`. `@Other()` and `@pkg.Other()` inside a component body give
`tmpl --renders--> tmpl`; a `pkg.Name` call is resolved by the package clause of the `.templ` files, and
only when exactly one component answers. A Go function that references `pkg.Component` (a call, or an
argument) gets `func --renders--> tmpl`; an unqualified call from Go code in the component's own package
is not detected. `css` and `script` components are not indexed. A file that does not parse is skipped
with a warning (`partial`).

**config.** Every scalar leaf of `config/env.yml` is a key. `${NAME:default}` is read the way the loader
reads it (the first colon splits), a scalar without a placeholder is a static `default` with no
environment variable. The Go side comes from the struct named `Config` in the package named `config`:
its `yaml` tags give each key its `field`. Usage is found in three ways, each attributed to the enclosing
function and its package: a selector chain rooted at an identifier named `cfg` or `conf`
(`cfg.Database.MariaDB.DSN`), which is mapped through the struct (the longest prefix that names a field
wins, and a chain that stops at a group uses every key below it); `config.Get("a.b")` and
`config.Unmarshal("a.b", ...)` with a literal key (a group again means every key below it); and
`os.Getenv("X")` / `os.LookupEnv("X")` with a literal name. A key or variable the code reads that
`env.yml` does not declare is still a node, with `declared=false`. Reads through a variable name are not
seen. A use inside a closure in a package-level `var` has no enclosing function, so only the package
gets the edge.

**mcp.** Every `mcp.AddTool(srv, &mcp.Tool{Name: "...", Description: "..."}, handler)` call whose name is
a string constant (a `+` of literals is evaluated). The function containing the call
`registers` the tool. A call whose tool literal or name is not constant is skipped with a warning.

**docs.** Markdown files are parsed with goldmark (GFM). `docs/specs/SPEC_*.md` are specs; everything
else is a doc. A table whose header has `Item` and `Implemented` columns (plus optional `Skipped` and
`Why`, or `Rationale`, `Reason`, `Notes`) becomes one `spec_item` per row. A `mentions` edge is made
from each backticked span and each link target that resolves **exactly** to a node; a row's own
backticked spans make its `covers` edges. Resolution, in order (the first rule that gives a result wins):

1. a node ID written out (`` `pkg:controllers` ``, `` `route:GET:/api/hello` ``);
2. a route: `` `GET /api/hello` ``, or a bare path (`` `/api/hello` ``, all its methods). A route registered
   for every method (`ANY`) answers a mention of any method;
3. a repository path, relative to the doc then to the root: a Go or `.templ` file, a directory with a
   package, another doc or spec; a `:line` suffix is ignored; a bare `routes.go` counts only if no other
   file has that name;
4. an environment variable (`` `PORT` ``) the graph holds;
5. a config key, a group, or `prefix.*` (`` `mail.smtp.*` `` is every key below it); needs a dot;
6. an MCP tool name (`` `db_query` ``);
7. a symbol: `pkg.Name` (one package by that name), `Type.Method` (one type by that name),
   `pkg.Type.Method`, or `pkg.Template` for a component. A bare identifier is **never** resolved.

Links are resolved the same way, and `/dev/docs/specs/<slug>` and `/dev/docs/app/<slug>` resolve to the
spec or doc with that slug. Anything that does not resolve is dropped, not recorded: a stale reference
leaves no trace in the graph yet (the Phase 5 stale-docs finding will need one).

**db.** The schema, from two sources that are merged when both exist, and never a row of data.

- *The migrations* (always): every `.sql` file of `storage/migrations/sql` with a numeric prefix
  (`0002_add_index.sql`), in version order. Only the goose **Up** section is read, and `StatementBegin` /
  `StatementEnd` blocks (stored programs) are skipped with a warning. The parser tokenizes (so strings,
  comments, quoted identifiers and `/*! ... */` conditional comments cannot confuse it) and understands
  `CREATE TABLE` (including `IF NOT EXISTS` and `LIKE`), `ALTER TABLE` (`ADD`, `DROP`, `MODIFY`, `CHANGE`,
  `RENAME`, `ALTER ... DEFAULT`, several actions at once, `FIRST`/`AFTER`), `DROP TABLE`, `RENAME TABLE`,
  `CREATE`/`DROP INDEX`, and inside them columns, primary, unique and plain keys, foreign keys (also the
  inline `REFERENCES` form) with their `ON DELETE`/`ON UPDATE` actions, `COMMENT`, `ENGINE`. It applies them in
  order, so a column renamed by a later migration is the renamed one, and it remembers which migration
  introduced each table, column and index (`introduced_in`; a table dropped and created again was
  introduced by the later migration). What a migration leaves unnamed it names the way MariaDB does: an
  unnamed unique or plain key after its first column (`a`, then `a_2`, `a_3`), a foreign key's index after
  the constraint symbol, else the `FOREIGN KEY` index name, else the column, and that index is added only if
  no declared index covers the foreign key's columns (even one declared later in the statement). This
  emulation is checked against a real server (`TestStaticSchemaMatchesARealServer`): the fixture's
  migrations applied to MariaDB produce a schema the parser reproduces exactly, with no drift. It reads
  MySQL/MariaDB DDL only (the Postgres and SQLite dialects are in the spec's backlog). DDL it recognises
  but cannot read (`CREATE TABLE ... AS SELECT`, partitioning, an unknown `ALTER` action) is reported as a
  warning with the migration it came from, never guessed at; other statements (inserts, views, triggers)
  are ignored without comment.
- *The live database* (when `MARIADB_DSN` is set, so the explorer exists): `Explorer.Snapshot` reads the whole
  schema with one `information_schema` query per kind (tables, columns, indexes, foreign keys joined to
  `REFERENTIAL_CONSTRAINTS` for the actions), plus which goose versions are applied. It reads no rows, and
  leaves out every volatile counter (estimated row counts, auto-increment positions), so two snapshots of an
  unchanged schema are equal. Goose's own table is read for `applied` and is not part of the schema.
- *Merged:* tables, columns, indexes and foreign keys are matched by name (case-insensitively; a foreign
  key by its columns and target). The live database is the truth about what exists, so its attributes win
  (types as the server spells them, comments, engine, constraint names, referential actions, the `key`),
  while history (`introduced_in`) comes from the migrations. Each element says where it comes from
  (`source`), and a column or index both sides have but describe differently gets a `drift` string. Types are
  compared after normalisation (integer display widths, `integer` and `int`, `bool`, bare `decimal`), so
  `INT` against `int(11)` is not drift; the comparison is best effort. A server-added index a migration
  emulates but the live database lacks is dropped. A migration the live goose table does not list has
  `applied=false`.
- *Inferred relations*, each recorded as a plain edge plus an annotation node (the decision of Phase 0):
  - `implicit_ref` (method `name_match`): a column named `x_id` (or `xId`) that no foreign key covers, next to
    a table called `x`, `xs` or `xes` (or `...ies` for an `x` ending in `y`) with a single-column primary key. Confidence starts at 70 for the
    name, plus 20 if the two columns have the same type or 10 for the same integer family, minus 30 if they
    cannot hold the same values, plus 5 if the target is called `id`, plus 5 if the column is indexed; capped
    at 95, and below 50 nothing is emitted. The best candidate wins. It is emitted for the column and for
    its table.
  - `many_to_many` (method `junction_pk`, confidence 90): a table with two single-column foreign keys to two
    different tables whose columns together are exactly its primary key, e.g. `order_items` between `orders`
    and `products`; or (`junction_unique`, 70) exactly a unique index. Emitted in both directions, with
    `via` the junction table.
  - A foreign key's `cardinality` (not inferred; derived from the keys the schema declares).
- *Code and schema:* `reads_table` and `writes_table` come from the SQL a function uses. The code layer
  records the string literals (also `+` concatenations of literals, and package-level `const`/`var` strings used
  by name) inside functions of the `storage` and `business` layers, and the db layer reads the tables out of
  them with a token walk: `FROM` and `JOIN` read; `INSERT`/`REPLACE INTO`, `UPDATE`, `DELETE FROM` and
  `TRUNCATE` write; `INSERT ... SELECT ... FROM` does both; comma joins, aliases, subqueries and
  `schema.table` work, and only names that are real tables count. `maps_to` joins a struct to the table
  whose columns include every `db:"col"` tag of its fields: the one table that fits, or, when several do,
  the one the type's name points at (`Order` to `orders`, `OrderItem` to `order_items`); a struct that fits
  several tables and is named for none of them maps to nothing. Not seen: SQL built at run time
  (`"SELECT * FROM " + table`, `fmt.Sprintf`), SQL held in a package-level map or struct, and SQL outside
  `storage` and `business`. The full route-to-table path needs the `calls` layer (Phase 6): until then a
  route reaches its handler (`handled_by`), and the tables are reached from the functions that use them.
- *The tables mentioned in docs* resolve too: a backticked table name, `table.column`, a migration ID, or
  the path of a migration file.

## Writer contract

What the Phase 1 writer (`business/services/codegraph/writer.go`) does, given F7, F8 and F11:

1. Build the layer's quads in memory as typed terms (`quad.IRI`, `quad.String`, ...), never through the
   JSON or N-Quads parsers. Every quad carries the layer's label.
2. Sort and deduplicate by (subject, predicate, object).
3. Read the quads currently under the layer's label with
   `shape.FilterQuads(nil, nil, nil, []quad.Value{label})` and `graph.NewResultReader`. A quad that
   comes back with an unresolved node is the F8 symptom and fails the layer.
4. Diff on the full quad, label included. The sizes are the "+38/-12" the UI will show.
5. Apply the **adds** with one tolerant `QuadStore.ApplyDeltas(deltas, graph.IgnoreOpts{IgnoreDup: true,
   IgnoreMissing: true})`, then the **deletes** with a second one. Never one mixed batch (F11), never
   `ApplyTransaction` (F8). The two calls are not atomic together; adds go first so that a failure in
   between leaves a superset of the right quads, which the next run trims, never a hole.
6. Read the layer back and compare it with what was written. A mismatch fails the layer with a message
   that says how to recover (delete `docs/graph/indexes.bolt`, which is derived data but also holds any
   hand-added quads) and the next run re-diffs against what is really stored.
7. If `ApplyDeltas` returns an error, log it, mark the layer `status=error`, and refuse every later
   write in this process (F8). The server keeps running; a restart clears the cache.
8. Never read for deletion, or delete, a quad whose label is not a `layer:*` IRI. The writer refuses
   any other label outright.

## Querying

The queries below are verified against hand-written IRI quads in `contract_test.go`. Since Phase 1 the
`code` layer fills the store with real project data, so they work against it as written. Runnable
examples on this repo:

```js
// who does the controllers package import? (and so: what does it depend on)
g.V('<pkg:controllers>').Out('<imports>').All()

// who imports a package? (change impact)
g.V(g.IRI('pkg:business/actions')).In(g.IRI('imports')).All()

// everything defined in a file, and everything we know about a function
g.V(g.IRI('file:controllers/api.go')).In(g.IRI('defined_in')).All()
g.V(g.IRI('func:controllers.HelloAPI')).Out(null, 'predicate').All()

// when was the graph last built, and did every layer succeed?
g.V(g.IRI('build:latest')).Out(g.IRI('has_layer')).All()
g.V(g.IRI('layer:code')).Out(g.IRI('status')).All()
```

```js
// Phase 2: the path from an HTTP route to its code, and to the template it renders
g.V(g.IRI('route:GET:/api/hello')).Out(g.IRI('handled_by')).All()
g.V(g.IRI('route:GET:/')).Out(g.IRI('handled_by')).Out(g.IRI('renders')).All()
g.V(g.IRI('route:GET:/api/hello')).Out(g.IRI('uses_mw')).All()

// every route registered for all methods, and every handler that is not application code
g.V().Has(g.IRI('method'), 'ANY').All()
g.V().Has(g.IRI('type'), g.IRI('kind:route')).Out(g.IRI('handler')).All()

// who uses a config key, where it is declared, what it defaults to, and what backs it
g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).All()
g.V(g.IRI('cfg:server.port')).Out(g.IRI('default')).All()
g.V(g.IRI('cfg:server.port')).Out(g.IRI('reads_env')).All()

// read but not declared: undeclared keys and variables
g.V().Has(g.IRI('declared'), false).All()

// which function registers an MCP tool, and what the tool takes
g.V(g.IRI('tool:graph_query')).In(g.IRI('registers')).All()
g.V(g.IRI('tool:graph_query')).Out(g.IRI('input_schema')).All()

// which docs and specs mention a file, and what a spec skipped
g.V(g.IRI('file:routes/routes.go')).In(g.IRI('mentions')).All()
g.V(g.IRI('spec:dev-db-explorer')).Out(g.IRI('has_item')).Has(g.IRI('status'), 'skipped').Out(g.IRI('why_skipped')).All()
```

```js
// Phase 3: the schema (an ER diagram is these edges), from the migrations alone or merged with the live database
g.V().Has(g.IRI('type'), g.IRI('kind:table')).All()
g.V(g.IRI('table:orders')).Out(g.IRI('has_column')).All()
g.V(g.IRI('table:orders')).Out(g.IRI('fk_to')).All()               // tables it references
g.V(g.IRI('table:users')).In(g.IRI('fk_to')).Has(g.IRI('type'), g.IRI('kind:table')).All()  // tables that reference it
g.V(g.IRI('col:order_items.order_id')).Out(g.IRI('on_delete')).All()

// which code touches a table, and which struct is its row
g.V(g.IRI('table:orders')).In(g.IRI('reads_table')).All()
g.V(g.IRI('table:orders')).In(g.IRI('writes_table')).All()
g.V(g.IRI('table:orders')).In(g.IRI('maps_to')).All()

// relations the schema implies, and how sure we are
g.V(g.IRI('table:orders')).Out(g.IRI('many_to_many')).All()
g.V(g.IRI('col:orders.user_id')).Out(g.IRI('implicit_ref')).All()
g.V().Has(g.IRI('type'), g.IRI('kind:annotation')).Has(g.IRI('subject'), g.IRI('col:orders.user_id')).Out(g.IRI('confidence')).All()

// drift between the migrations and the live database, and migrations not yet applied
g.V().Has(g.IRI('source'), 'live').All()                           // made outside the migrations
g.V().Has(g.IRI('source'), 'migration').Has(g.IRI('type'), g.IRI('kind:table')).All()  // never applied (when a database is configured)
g.V().Out(g.IRI('drift')).All()
g.V().Has(g.IRI('applied'), false).All()

// where a table came from
g.V(g.IRI('table:orders')).Out(g.IRI('introduced_in')).All()
```

Rebuild on demand with `curl -X POST localhost:8080/dev/graph/reindex` (the response lists each layer's
status, whether it was rebuilt, and its quad counts).

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
`example.com/fixture`, standard library only) that the indexer's golden tests run against. It
compiles, is never run, and is invisible to the main module (`go list ./...` and `go vet ./...` skip
`testdata`). Each layer's golden N-Quads are `testdata/golden/<layer>.nq`; regenerate them with
`go test ./business/services/codegraph -update` after an intended change and review the diff. The
fixture is never run, so
the `routes` layer is fed the runtime facts its router would report (`fixtureRoutes` in `layers_test.go`);
`routes_chi_test.go` covers the real chi traversal on a router built from this repository's handlers.

| Fixture file | What it exercises |
| --- | --- |
| `main.go` | root package `pkg:.`, `arch_layer=wiring`, package doc; `cfg.Server.Port` and `cfg.Database.DSN` reads, and an `os.Getenv` of a variable `env.yml` does not declare |
| `routes/routes.go` | wiring; imports every other layer; six routes (`GET /`, `GET /api/hello`, `GET /api/orders`, `GET /api/orders/{id}`, `/dev/mcp`, `/assets/`) and a middleware wrapper |
| `config/env.yml`, `config/config.go` | placeholders with defaults, a default containing a colon, sensitive keys whose defaults must not appear, a static value, a key bound to a struct field that nothing reads, keys reachable only through `config.Get` |
| `business/actions/notify.go` | `config.Get` of a whole group (`mail.smtp`) and of an undeclared key |
| `assets/templates/*.templ` | components, a `children...` layout, `@Layout(...)` and `@Footer()` calls, a doc comment, a component whose Go package has only generated output |
| `controllers/home.go` | a handler that renders a templ component (`func --renders--> tmpl`) |
| `mcp/mcp.go` | a stand-in for the MCP SDK (the fixture is standard-library only) |
| `docs/**`, `README.md` | an architecture page with every kind of mention (path, route, symbol, `Type.Method`, config key and group, environment variable, tool, template, links, a stale reference), a guide, and `SPEC_HELLO.md` whose status table has implemented, planned, partial and skipped rows |
| `controllers/hello.go` | acceptance query 1: `HelloAPI` calls `actions.Hello`, and no table is reachable |
| `controllers/orders.go` | acceptance query 2: `ListOrders` goes handler → service → store (clean); `GetOrder` calls the store directly (**layering violation**, on purpose); handler factories returning closures; unexported `writeJSON` |
| `business/actions/hello.go` | single-function action; a two-sentence doc for first-sentence extraction |
| `business/services/orders/orders.go` | interface type, struct type, pointer and value receivers, an unexported method |
| `storage/orderstore/orderstore.go` | SQL as package constants and as literals in function bodies (a join, an `INSERT ... SELECT`, a `+` concatenation, a raw string, a `DELETE`) and one dynamic statement that names no table; `db:"…"` struct tags (a struct that maps to one table, one that fits several and maps to none) |
| `storage/migrations/sql/0001_init.sql` | one goose migration, the sample from the spec (users, products, orders, order_items): a sensitive column default, unnamed and named keys and foreign keys, a composite-primary-key junction, and an implicit reference (`orders.user_id`, no foreign key). The cases beyond this (`ALTER` of existing tables, a shared primary key, renames and drops, a column that names no table, drift against a live database, unapplied migrations) are an inline second migration in `db_extra_test.go` (and `db_live_test.go`), so the tree carries no schema it does not need |
| `storage/orderstore/zz_generated.go` | a `// Code generated … DO NOT EDIT.` file that is indexed with `generated=true` |
| `middleware/logging.go` | middleware function |
| `devmcp/devmcp.go` | `arch_layer=dev`; a dev package importing storage; two `mcp.AddTool` registrations (a handler struct with `omitempty` and `jsonschema` tags, and `struct{}`) |
| `assets/templates/home_templ.go` | generated templ output that must be skipped; the package has no other Go file, so it must not appear |

Edit the fixture only together with its goldens, in one commit. Facts asserted independently of the
golden (so a wrongly regenerated golden is still caught) live in `Test<Layer>LayerFixtureFacts`; the
same invariants run over this repository in `TestRepoInvariants` and, for every layer at once,
`TestWholeGraphInvariants`.

## Open decisions

Decided no later than the phase named.

| Phase | Question | Leading option |
| --- | --- | --- |
| ~~1~~ | ~~How are imports of packages outside the module represented?~~ **Decided (Phase 1):** counted in an `external_imports` int on the package; add `pkg:ext/<import path>` nodes only if agents ask "who imports X". | |
| ~~2~~ | ~~Method token for routes registered with `Handle` and no method~~ **Decided (Phase 2):** one route with method `ANY`. `chi.Walk` reports such a route nine times (once per method), which is nine nodes for one registration. | |
| ~~2~~ | ~~Direction and predicate for tool → backing function~~ **Decided (Phase 2):** `func --registers--> tool`, the function that calls `mcp.AddTool`; a handler literal is a closure and resolves to it. | |
| ~~2~~ | ~~`tmpl:` ID: package name or directory~~ **Decided (Phase 2):** directory, like `func:` and `typ:`, because two packages can share a name (`tmpl:assets/templates.Home`). | |
| ~~2~~ | ~~Spec item ID: row number or text~~ **Decided (Phase 2):** a slug of the Item cell. A row number shifts whenever a row is inserted, which churns every mention of the items below it. | |
| 2 | `documents` and unresolved doc references | Neither is emitted. `documents` needs an explicit convention to mean more than `mentions`. A doc mention that resolves to nothing leaves no trace, so the Phase 5 stale-docs finding needs the docs layer to record it (for example an `unresolved_ref` literal on the doc) before it can be derived. |
| ~~3~~ | ~~How is an *inferred* edge marked? A triple has no room for method or confidence.~~ **Decided (Phase 3):** the edge stays a plain triple; each inferred edge has an annotation node `ann:<hash>` with `subject` and `object` (edges to the nodes), `predicate` (the predicate's name, a string), `method` and `confidence` (0 to 100), and `via` for a junction. | |
| 5 | Drift as findings | The graph holds the facts a finding needs (`source`, `drift`, `applied`, `key`, the indexes, `implicit_ref`); the findings layer (Phase 5) turns them into rules: table without a primary key, foreign key column without an index, `_id` column with no foreign key, migration and live drift. |
| 5 | Final layering rule and allow-list | The rule under [Package classification](#package-classification). |

The `graph_query` tool description now shows the `'<iri>'` idiom (done in Phase 1).
