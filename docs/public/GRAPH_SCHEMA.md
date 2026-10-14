# Graph Schema

The vocabulary of the embedded Cayley graph (`/dev/graph`, the `graph_query` MCP tool): what gets
inserted, how nodes are named, and how to query them. The plan lives in
[SPEC_GRAPH_INDEXER](/dev/docs/specs/graph-indexer); this file is the reference the indexer, its tests,
and agents work from.

> **Status: framework extraction, Phase 3 (the app's own graph), on top of Phase 6 (depth).** The graph is about the
> project it indexes: its own nodes are the default, what it takes from the framework it is built on is labelled
> (`origin`, [Origin and scope](#origin-and-scope)), and where the project keeps things comes from its
> [profile](#the-project-profile) (`govibe.yml`), not from constants.
>
> **Phase 6 (depth).** Every boot, every `POST /dev/graph/reindex`, and
> (for stale layers only) every `/dev/graph` page load, data call and `graph_query` call fills the store
> with the `code`, `routes`, `templates`, `config`, `mcp`, `db`, `docs` and `findings` layers plus the
> `meta` layer: packages, files, functions, types and imports; routes, middleware and their handlers; templ
> components; config keys and environment variables with their usage; MCP tools; the database schema
> (tables, columns, indexes, foreign keys, migrations, relations the schema implies but does not declare)
> and which code reads and writes which table; docs, specs and their status rows, with what they mention
> and what they name that no longer exists; and the findings derived from all of it. Two **opt-in**
> layers cost more than parsing and are off unless asked for: `calls` (which function calls which, so a
> route's path to a table is exact: [the call graph](#the-phase-6-layers)) and `git` (last commit, churn and
> the files that change together). A layer that is off holds no quads and says so (status `off`). Don't
> assume a node or predicate exists until its layer is marked *implemented* in [Layers](#layers). Hand-added
> quads (the "Add a quad" form) are never touched by a reindex. Agents do not need Gizmo for the common
> questions: see [Agent tools](#agent-tools). `govibe graph index` builds the same graph offline, and gates
> CI on its findings: see [The CLI and the CI gate](#the-cli-and-the-ci-gate).

## Spike results

Phase 0 ran the IRI / literal / label round trip against the Cayley version in `go.mod` (v0.7.7). The
findings are pinned by `internal/graphdb/contract_test.go`; if that test fails after a dependency
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

The store is Cayley's `bolt` backend: one bolt file in `docs/graph/`, inside the dev server's process
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
- **Methods** include the receiver type without `*`: `func:internal/dbexplorer.Explorer.DescribeTable`.
- **Closures** get no node. A handler that is a closure resolves to its enclosing function.
- **Duplicate names.** Two declarations can share an ID (several `init` functions, or one function in
  files with different build tags). The first keeps the plain ID; the second and later get `#2`, `#3`,
  ... in file-path then source order, so `func:..init`, `func:..init#2`. The suffix is stable for
  identical sources, but inserting an earlier duplicate renumbers the later ones.

| Kind | `type` object | ID | Example | Owner layer | Phase |
| --- | --- | --- | --- | --- | --- |
| package | `kind:package` | `pkg:<dir>` | `pkg:business/actions` | code | 1 |
| file | `kind:file` | `file:<path>` | `file:controllers/items.go`, `file:assets/templates/home.templ` | code (`.go`), templates (`.templ`) | 1, 2 |
| function, method | `kind:func` | `func:<dir>.<Name>` or `func:<dir>.<Recv>.<Name>` | `func:controllers.HelloAPI` | code | 1 |
| type | `kind:type` | `typ:<dir>.<Name>` | `typ:internal/dbexplorer.Explorer` | code | 1 |
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
| table | `kind:table` | `table:<name>` | `table:items` | db | 3 |
| column | `kind:column` | `col:<table>.<column>` | `col:items.owner_id` | db | 3 |
| index | `kind:index` | `idx:<table>.<name>`; the primary key is `idx:<table>.PRIMARY` | `idx:items.ix_items_owner` | db | 3 |
| migration | `kind:migration` | `mig:<file name without .sql>` (decided: the version alone, `0001`, is not what people call it) | `mig:0001_init` | db | 3 |
| annotation | `kind:annotation` | `ann:<12 hex characters>`: a hash of the subject, predicate and object of the inferred edge it describes | `ann:31db341e624c` | db, calls, git | 3, 6 |
| finding | `kind:finding` | `finding:<rule>:<subject id>`, and `#<detail>` when one rule finds several problems on one node (the detail is what tells them apart: the other package of a bad import, the line and text of a stale reference) | `finding:layering:pkg:controllers#pkg:storage/dbexplorer` | findings | 5 |
| rule | `kind:rule` | `rule:<name>` | `rule:layering` | findings | 5 (new) |
| external | `kind:external` | `ext:<import path>` for a package, `ext:<import path>.<Name>` for a function (`Recv.Name` for a method), `ext:<framework module>/internal` for everything in the framework's `internal/` | `ext:github.com/petars-dev/govibe/web`, `ext:github.com/petars-dev/govibe/web.JSON` | externals | 3 (framework extraction) |

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
| `subject`, `object` | annotation → the column, table, function or file an inferred edge starts and ends at | db, calls, git | 3, 6 | final (new) |
| `via` | annotation → the junction table of a `many_to_many` edge, or the interface type an `interface_dispatch` call goes through | db, calls | 3, 6 | final (new) |
| `calls` | func → func. A plain call (the compiler resolves it) is a bare edge; a call through an interface the module declares is one edge per type that could implement it, each with an annotation (method `interface_dispatch`, `confidence` 80 down to 30, `via` the interface). Calls inside a closure belong to the declared function that contains it | calls | 6 | final |
| `co_changed_with` | file, doc, spec → file, doc, spec: the other changed in commits that changed this one, in both directions as each end sees it. Always annotated (method `co_change`, `confidence` the percentage of the subject's commits that also touched the object, `together` the number of commits that touched both) | git | 6 | final (new) |
| `about` | finding → the node at fault (exactly one) | findings | 5 | final |
| `imports_external` | package, `.templ` file → external package it imports (the framework's public packages; libraries and the standard library are only counted in `external_imports`) | code, templates | 3 (framework extraction) | final |
| `calls_external` | func → external function or package it calls (the calls layer: a function of the framework's public API, the one leaf for its `internal/`, a library package; never the standard library) | calls | 3 (framework extraction) | final |
| `handled_by_external` | route → external function that serves it (absent when the handler is the project's own code, or in the standard library) | routes | 3 (framework extraction) | final |

### Attribute predicates

| Predicate | Literal | On | Phase |
| --- | --- | --- | --- |
| `name` | string: short display name: Go package name, file base name, symbol name (`Recv.Method` for a method), tool name, `latest` for the build | every node | 1 |
| `doc` | string: first sentence of the godoc (for a template, of the `//` comment directly above it), at most 240 characters | package, func, type, template | 1, 2 |
| `signature` | string, one line, at most 500 characters. A template is `templ Name(params)`. A func is its declaration without body (`func (s *Service) List(ctx context.Context) ([]Item, error)`); a type is `type Name`, plus `struct`, `interface`, or the underlying type (`type Handler func(string) error`, `type Set[T comparable] struct`) | func, type | 1 |
| `line` | int: for a func or type its declaration; template: the `templ` line; config key: its line in `env.yml`; MCP tool: the `mcp.AddTool` call; spec item: its row in the markdown | func, type, template, config key, MCP tool, spec item | 1, 2 |
| `exported` | bool: the name starts with an upper-case letter (a method counts by its own name, not its receiver type's) | func, type | 1 |
| `path` | string: repo-relative file or directory (for a route: the URL path; for a config key: the settings file that declares it; for a tool, template or spec item: the file that defines it) | package, file, route, template, config key, MCP tool, doc, spec, spec item | 1, 2 |
| `import_path` | string | package, external | 1, 3 |
| `origin` | string, `app` or `framework`: whose a node is. Written on route (who registered it), middleware, config key, env var, MCP tool and external; every other node was indexed from the project's own tree and is the app's; a finding has the origin of the node it is about. See [Origin and scope](#origin-and-scope) | route, middleware, config key, env var, MCP tool, external | 3 (framework extraction) |
| `module`, `module_version` | string: the module an external belongs to, and its version in the indexed build (`(devel)` for a local `replace`) | external | 3 (framework extraction) |
| `internal` | bool: the external is inside the framework's `internal/`, not API | external | 3 (framework extraction) |
| `off` | bool: the rule is switched off in `govibe.yml`, a choice and not a rule that could not look | rule | 3 (framework extraction) |
| `app_name`, `app_module`, `framework_module`, `framework_version` | string: what was indexed, and the framework it is built on with its version in this build (`(this repository)` when the project is the framework) | build | 3 (framework extraction) |
| `arch_layer` | string, see [Package classification](#package-classification) | package | 1 |
| `lines` | int: number of lines (a final line without a newline counts) | file | 1 |
| `external_imports` | int: distinct import paths with no package node: the standard library, third-party modules, and module packages the code layer does not index (a package holding only generated `*_templ.go`) | package | 1 |
| `generated` | bool: the file carries the `// Code generated ... DO NOT EDIT.` marker | file | 1 |
| `git_sha`, `dirty`, `indexed_at`, `duration_ms`, `indexer_version`, `quads`, `added`, `removed` | string, bool, time, int, string, int, int, int. `git_sha` and `dirty` are absent when git is unavailable. `quads`, `added` and `removed` total the data layers (not `meta`) and are relative to the previous run | build | 1 |
| `quads`, `fingerprint`, `status`, `duration_ms`, `indexed_at`, `added`, `removed`, `error` | int, string, string, int, time, int, int, string. `status` is `ok`, `partial` (indexed, but some inputs were skipped, e.g. a file that does not parse), `skipped` (not applicable here: the `routes` layer with no router wired; nothing was written or deleted, and `note` says why), `off` (an opt-in layer that is not switched on: it holds no quads, whatever an earlier run left is removed, and `note` says how to switch it on) or `error` (nothing was written; the layer's previous quads stay). `quads`, `added` and `removed` are absent on `error` and `skipped`; `note` appears only on `skipped`. `fingerprint` is 16 hex characters over the sorted path, mtime and size of the layer's inputs, taken before they are read | layer run | 1 |
| `method`, `dev_only` | string (`GET`, `POST`, ..., or `ANY`), bool (the path is `/dev` or under `/dev/`) | route | 2 |
| `handler` | string: the Go runtime's name for a handler that is **not** application code (`net/http.StripPrefix`, `github.com/modelcontextprotocol/go-sdk/mcp.(*StreamableHTTPHandler).ServeHTTP`); present exactly when `handled_by` is absent, so a route with no code link still says what serves it | route | 2 |
| `description`, `input_schema` | string, string (JSON). The schema is derived from the handler's input struct: `json` names, `omitempty` makes a property optional, the `jsonschema` tag is its description; `{"type":"object"}` for `struct{}`; absent when the handler does not name a struct of the registering package | MCP tool | 2 |
| `default`, `sensitive` | string (absent when `sensitive`, and absent when empty), bool | config key, env var | 2 |
| `declared` | bool: the key is a leaf of a settings file (`env.yml`, `config/defaults.yml`), or the variable appears as `${NAME}` in one. `false` means the code reads it but nothing declares it | config key, env var | 2 (new) |
| `field` | string: the Go field the key binds to, from the yaml tags of `Config` (`Config.Database.MariaDB.DSN`); absent for keys only reachable through the dynamic `Get` and `Unmarshal` helpers | config key | 2 (new) |
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
| `predicate`, `method`, `confidence` | string (the inferred predicate), string, int 0 to 100: how an inferred edge was inferred and how sure that is. `method` is `name_match`, `junction_pk`, `junction_unique`, `interface_dispatch` or `co_change` | annotation | 3, 6 (new) |
| `together` | int: commits that touched both ends of a `co_changed_with` edge | annotation | 6 (new) |
| `last_commit`, `last_commit_at`, `last_subject` | string (the first 12 characters of the newest commit's hash), time, string (its subject line, at most 120 characters): the newest commit within the history window that touched it | file, doc, spec | 6 (new) |
| `commits`, `churn`, `authors` | int, int, int: commits within the window that touched it, lines added plus lines deleted by them, and how many distinct people made them. Author names and addresses are read to count and **never stored** | file, doc, spec | 6 (new) |
| `rule`, `severity`, `message` | string, string, string: the rule's name, `error` / `warning` / `info`, and what is wrong and what to do about it. A finding also has `name` (its rule's title) and, when it points at a place, `path` and `line` | finding | 5 |
| `severity`, `title`, `description`, `checked`, `note` | string, string, string, bool, string: on a rule node, its default severity, what a finding of it is called, what it looks for, whether it ran on this graph, and (when it did not) why: a layer it reads is not built, or `not applicable: ...` | rule | 5 (new) |
| `unresolved_ref` | string, `"<line>:<text>"`, repeatable: something the document names that does not exist (see [what the docs layer reads](#what-the-phase-2-layers-read)). Only on a doc, and on a spec's status rows | doc, spec item | 5 (new) |

`data_type` is not `type`, which is taken by the kind predicate. A config key or environment variable
whose name matches `key`, `secret`, `password`, `token` or `dsn` (case-insensitive) is `sensitive`: it
gets no `default`, no value, and no DSN, ever, and neither does any variable bound to it. `.env*` files
are never read.

### Package classification

`arch_layer` is the architecture layer of a package, from its directory, by the project's [profile](#the-project-profile):
the layer whose `layout` entry is the longest match (a directory and everything below it; `.` is the root package alone),
and `support` for a package no entry claims. It is not an indexer layer despite the shared word. The default profile is
a generated app's layout:

| Directory | `arch_layer` |
| --- | --- |
| `controllers/**` | `controllers` |
| `commands/**` | `commands` |
| `business/**` | `business` |
| `storage/**` | `storage` |
| `middleware/**` | `middleware` |
| `routes/**`, the module root `.`, `cmd/**` | `wiring` |
| `assets/**` | `views` |
| anything else (`config`, packages under `internal/`, ...) | `support` |

The framework's own repository overrides it in its `govibe.yml` (`internal/kernel` is `wiring`; `internal/devtools` and `internal/devmcp` are `dev`), and a
project with another layout says so there. The rule itself is data (`internal/rules`).

The layering rule (final since Phase 5, `layering.go`, and the one thing the `layering` findings, the
architecture overview and the layers report all read): SKILL.md's two constraints written as data, plus what
they imply.

| A package of layer | must not import | Because |
| --- | --- | --- |
| `controllers`, `commands`, `middleware`, `views`, `support` | `storage` | only business actions and services may call storage |
| `business` | `controllers`, `commands`, `middleware`, `views`, `wiring`, `dev` | business logic does not depend on the HTTP layer, on commands (the CLI twin of a controller) or on wiring |
| `storage` | everything but `storage` and `support` | storage is the lowest layer |
| `wiring`, `dev` | nothing | they assemble the application: this is the allow-list (`routes`, `main`, `cmd`, and in the framework's repository `internal/kernel`, `internal/devmcp`, `internal/devtools`) |

A package a controller (or a middleware) uses to reach storage is therefore a finding, whatever it is used
for. (This repository has none: the dev stack's controllers live in `internal/devtools/devui`, a `dev` package, and the stores they read are `support` packages of the framework.)

## Layers

| Layer | Label | Owns | Source | Phase | Status |
| --- | --- | --- | --- | --- | --- |
| code | `layer:code` | package, file, func, type | `go/parser` over the repo | 1 | **implemented** |
| meta | `layer:meta` | build, layer run | the indexer itself | 1 | **implemented** |
| routes | `layer:routes` | route, middleware | the live chi router, walked at runtime | 2 | **implemented** |
| templates | `layer:templates` | template, `.templ` file | `templ/parser/v2` | 2 | **implemented** |
| config | `layer:config` | config key, env var | the settings files the profile lists (`env.yml` by default; the framework's repository lists `config/defaults.yml`), the framework's manifest (its settings, origin `framework`), and the code index. With no settings file the layer is `skipped`, not empty | 2, 3 | **implemented** |
| mcp | `layer:mcp` | MCP tool | `mcp.AddTool` calls, from the code index, and the framework's own tools from its manifest (origin `framework`) | 2, 3 | **implemented** |
| docs | `layer:docs` | doc, spec, spec item | goldmark over the profile's doc directories (`docs/public`, `docs/specs`) and root docs (`README.md`, `SKILL.md`) | 2 | **implemented** |
| db | `layer:db` | table, column, index, migration, annotation | the migrations in the profile's migrations directory (`storage/migrations/sql`), merged with the live database's schema when one is configured | 3 | **implemented** |
| externals | `layer:externals` | external | the indexes of the layers that point at externals: the project's imports of the framework (from Go and from `.templ` headers), what serves its routes, and, with `calls` on, what it calls. The module table comes from the running binary (`inspect --json`) or `go.mod` | 3 (framework extraction) | **implemented** |
| findings | `layer:findings` | finding, rule | derived from the other layers (it reads what they wrote) | 5 | **implemented** |
| calls | `layer:calls` | (`calls` edges, and the annotations of the ones through an interface) | `golang.org/x/tools/go/packages`: the module, type-checked | 6, opt-in | **implemented** |
| git | `layer:git` | (file, doc and spec attributes, `co_changed_with` edges and their annotations) | `git log` | 6, opt-in | **implemented** |

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
struct fields, qualified references, `cfg.A.B` chains, `os.Getenv` calls, calls of the dynamic config helpers (`Get`, `String` and `Unmarshal` with a literal key, as functions or as methods), `mcp.AddTool`
calls), so nothing is parsed twice. A layer points only at nodes it can resolve from those same indexes.

| Layer | Depends on | Own inputs (what its fingerprint covers) |
| --- | --- | --- |
| every layer | | `govibe.yml` (the profile) |
| code | | every indexed `.go` file, and `go.mod` |
| routes | code | the route table: method, path, handler and middleware identity of every route |
| templates | code | every `.templ` file |
| config | code | the settings files the profile lists |
| mcp | code | none (it reads the code index) |
| db | code | every `.sql` under the profile's migrations directory, and the live schema (see below) |
| docs | code, routes, templates, config, mcp, db | `README.md`, `SKILL.md`, every `.md` under `docs/public` and `docs/specs`, and whether each file those documents name is on disk (see below) |
| calls (opt-in) | code | `go.sum` (type-checking reads the Go sources the code layer stands for, and the dependencies' checksums) |
| git (opt-in) | code, templates, docs | the commit `HEAD` points at, and whether the clone is shallow (the work tree is not part of the history) |
| externals | code, routes, templates, calls | none: it is built from those layers' indexes, so it is stale whenever one is |
| findings | every layer above | none: the rules read what the other layers wrote, so it is stale whenever any of them is |

A layer's fingerprint (16 hex characters, on `layer:<name>`) hashes its own inputs (path, mtime, size)
together with its dependencies' fingerprints, so it changes exactly when something the layer was built
from changed. A layer is **stale** when it has never been built or its current fingerprint differs from
the stored one; a layer that failed is not retried until its fingerprint changes (an explicit reindex
retries at once). Fingerprints are taken before any input is read, so an edit that lands mid-run reads as
stale next time, never falsely fresh.

One input is only known after a build: which files the documents name (a stylesheet or
script the graph has no node for, say), because that is what finding a stale reference needs to know. The docs layer
therefore remembers which paths it looked for and whether each was there, covers that in its fingerprint,
and after each build the indexer takes the fingerprint again from what the build recorded (the layer
*settles*). Creating a file a document names, or removing one, makes the docs layer, and so the findings,
stale; no document has to change.

| Trigger | What runs |
| --- | --- |
| server boot (after the router is wired, in a goroutine) | every layer |
| `POST /dev/graph/reindex`, `graph_reindex` with no layers | every layer that is on, ignoring caches |
| `graph_reindex` naming `calls` or `git` | that layer, **and it stays on** for as long as the server runs: asking for an opt-in layer by name is how it is switched on without a restart |
| `govibe graph index` | every layer that is on, into the store `--dir` names (offline: no router, so `routes` is skipped) |
| `GET /dev/graph` (the page), every `/dev/graph/data/*` and `/dev/graph/reports/*` call, and every `graph_query` and graph tool call | only the stale layers, usually none: a freshness check is a few milliseconds |

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

**config.** Every scalar leaf of the settings files is a key: `config/defaults.yml` (the framework's defaults), then the `env.yml` inside a `config` directory (a project that predates the split), then `env.yml` in the project root, each declaration replacing an earlier one of the same key, as the loader merges them. `${NAME:default}` is read the way the loader
reads it (the first colon splits), a scalar without a placeholder is a static `default` with no
environment variable. The Go side comes from the struct named `Config` in the package named `config`:
its `yaml` tags give each key its `field`. Usage is found in three ways, each attributed to the enclosing
function and its package: a selector chain rooted at an identifier named `cfg` or `conf`
(`cfg.Database.MariaDB.DSN`), which is mapped through the struct (the longest prefix that names a field
wins, and a chain that stops at a group uses every key below it); `config.Get("a.b")` and
`config.Unmarshal("a.b", ...)` with a literal key, or the same helpers as methods of a config value
(`rt.Config.Get("a.b")`, `cfg.Unmarshal("a", &v)`: the receiver is recognised by its name, a variable called
`cfg` or `conf`, or a field called `Config`), where a group again means every key below it; and
`os.Getenv("X")` / `os.LookupEnv("X")` with a literal name. A key or variable the code reads that
no settings file declares is still a node, with `declared=false`. Reads through a variable name are not
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
5. a config key, a group, or `prefix.*` (`` `queue.broker.*` `` is every key below it); needs a dot;
6. an MCP tool name (`` `db_query` ``);
7. a symbol: `pkg.Name` (one package by that name), `Type.Method` (one type by that name),
   `pkg.Type.Method`, or `pkg.Template` for a component. A bare identifier is **never** resolved.

8. `dir.Name`: a function or type of the package in a directory (`storage/migrations.FS`, the way a
   function's ID is written).

Links are resolved the same way, and `/dev/docs/specs/<slug>` and `/dev/docs/app/<slug>` resolve to the
spec or doc with that slug. Anything that does not resolve is not a `mentions` edge, but it is no longer
dropped silently: a reference that is **unambiguously a pointer into this repository** and has nothing
behind it is recorded as `unresolved_ref` (`"<line>:<text>"`) on the doc, and the `docs.stale_ref` finding
reads that. It has to be conservative, because most backticked words are commands, library types or
examples. A reference is recorded only when it is:

- a path with a slash that ends in a source extension (`.go .templ .sql .md .yml .yaml .json .toml .css
  .js .html .sh`, so a path into a directory that has since been removed counts, wherever that directory
  used to be) or starts at something that exists at the repository root, that is neither a node of the
  graph nor on disk, relative to the document or to the root. `net/http`, `application/json`, `example.com/x/y.go`, URLs, patterns (`docs/*.md`) and
  placeholders (`controllers/<name>.go`) are not;
- `pkg.Name` where `pkg` is the one package of that name, no library the module imports shares the name
  (`middleware.RequestID` is chi's), `Name` is exported, and nothing called `Name` is declared at package
  level there; `Type.Member` of a struct that embeds nothing and has no such field or method; `dir.Name`
  where the directory holds a package and `Name` is gone;
- a link to a `/dev/docs/...` page or to a relative file that is not there.

Routes, node IDs, config keys and environment variables are never judged (documents use them as examples,
and for things not built yet). The prose of a spec is not judged either, because it describes what is
planned as well as what is done; only its status rows are, and the `docs.spec_missing` finding reads the
ones marked implemented.

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
    different tables whose columns together are exactly its primary key, e.g. `item_tags` between `items`
    and `tags`; or (`junction_unique`, 70) exactly a unique index. Emitted in both directions, with
    `via` the junction table.
  - A foreign key's `cardinality` (not inferred; derived from the keys the schema declares).
- *Code and schema:* `reads_table` and `writes_table` come from the SQL a function uses. The code layer
  records the string literals (also `+` concatenations of literals, and package-level `const`/`var` strings used
  by name) inside functions of the `storage` and `business` layers, and the db layer reads the tables out of
  them with a token walk: `FROM` and `JOIN` read; `INSERT`/`REPLACE INTO`, `UPDATE`, `DELETE FROM` and
  `TRUNCATE` write; `INSERT ... SELECT ... FROM` does both; comma joins, aliases, subqueries and
  `schema.table` work, and only names that are real tables count. `maps_to` joins a struct to the table
  whose columns include every `db:"col"` tag of its fields: the one table that fits, or, when several do,
  the one the type's name points at (`Item` to `items`, `ItemTag` to `item_tags`); a struct that fits
  several tables and is named for none of them maps to nothing. Not seen: SQL built at run time
  (`"SELECT * FROM " + table`, `fmt.Sprintf`), SQL held in a package-level map or struct, and SQL outside
  `storage` and `business`. The route-to-table path needs the opt-in `calls` layer (see [the Phase 6
  layers](#the-phase-6-layers)): without it a route reaches its handler (`handled_by`), and the tables are
  reached from the functions that use them.
- *The tables mentioned in docs* resolve too: a backticked table name, `table.column`, a migration ID, or
  the path of a migration file.

### The Phase 6 layers

Both are opt-in: `Options.Calls` / `Options.Git` (config `dev.graph.calls` / `dev.graph.git`, environment
`GRAPH_CALLS` / `GRAPH_GIT`, flags `--calls` / `--git`), or `graph_reindex` naming the layer, which switches it
on until the server restarts. A layer that is off has status `off`, no quads and a note that says how to switch
it on; whatever a run in which it was on left in the store is removed, so a reader never mistakes old call
edges for the code as it is. An off layer is recorded once per process and is not "rebuilt" by a reindex of
what it would depend on.

**calls.** The module is loaded and type-checked with `golang.org/x/tools/go/packages` (one `go list`, then the
type checker over the module's own sources; the dependencies are read as export data, so a warm build cache
makes it about a second for this repository, and the Go toolchain must be on the `PATH`). Each call
expression in the body of a declared function or method is resolved by the compiler, and an edge is recorded
when both ends are functions the code layer indexed:

- A call the compiler resolves (a function, a method on a concrete type, a method expression, an
  instantiated generic function, a promoted method of an embedded struct, `go f()`, `defer f()`) is a plain
  `calls` edge: certain, with no annotation.
- A call through an interface **declared in the module** is linked to the method of every module type that
  could be on the other end (the named, non-generic types whose pointer implements the interface: class
  hierarchy analysis). Each such edge has an annotation: method `interface_dispatch`, `via` the interface
  type, and a `confidence` that falls with the number of candidates (the caller's own type counts, though an
  edge to the caller itself is dropped): 1 candidate 80, 2 candidates 70, 3 or 4 candidates 60, 5 to 8 candidates 50,
  more 30. An interface with more than 24 candidates is not expanded, and the layer is `partial` and names it.
  Interfaces the module does not declare (`error`, `io.Reader`, `http.Handler`) are not expanded: nothing in
  the module is worth linking to.
- A closure has no node, so a call inside one belongs to the declared function that contains it, as a
  handler closure's route does. A handler factory `ListItems(svc)` therefore calls whatever the handler
  it returns calls.
- A pair that is called both ways is stored once, as the surer evidence (the plain call). A function that
  calls itself has no edge to itself.
- Not seen, and not guessed: calls through function values (a callback, a handler in a variable or a map,
  a method value), reflection, calls from package-level initializers (they have no caller node), calls into
  generated templ output (it has no node; `renders` covers it), and files that do not match the build
  context. The layer is `partial` and names every package that did not type-check, so a package that does not
  compile is missing edges, not a clean bill; it fails (and keeps its previous quads) only when none of the
  module's packages type-checked or `go list` cannot run.

**git.** `git log` over the newest 1,000 non-merge commits (renames are not followed), read once per `HEAD`
and reused while only other layers' inputs change. Each file the graph has a node for (a `.go` or `.templ`
file, a doc or a spec) gets its `last_commit`, `last_commit_at`, `last_subject`, `commits`, `churn` and
`authors`. Two files **co-change** when at least 3 commits touched both and those are at least 30% of the
first file's commits; a commit that touches more than 40 files is a bulk change (a format, a move) and counts
as no evidence about any pair. Each file keeps its 5 strongest partners, as `co_changed_with` edges annotated
with the percentage and the commit count; the relation is directional (`a.go` changing with `b.go` in 80%
of its commits does not mean the reverse). Authors' addresses are used to count people and are not stored, and
a path git quotes is left out. In a shallow clone the layer is `partial` (the history is cut: CI needs
`fetch-depth: 0`); outside a git work tree, or with no commit yet, it is `skipped` and removes what it wrote.

## Origin and scope

Whose a node is, and what a page or a tool shows by default.

- **Origin.** `origin` (`app` or `framework`) is written on the kinds whose source is not always the project's tree: a route is the framework's when the framework
  registered it (before the application's `Routes` ran, which only the running process knows: `inspect --json` reports it as `mounted_by`; a path under `/dev` or
  `/_govibe` is the framework's too), a middleware is the framework's when it is on the framework's own routes or lives in its module, a config key or environment variable is
  the framework's when only its defaults file (the manifest) declares it, an MCP tool is the framework's when it is served by its dev server, and every external is
  non-app (the framework's, or a library's). Everything else (packages, files, functions, types, templates, tables, docs, specs) was indexed from the project's own tree and is the app's. A finding has the
  origin of the node it is about; the build, layer and rule nodes have none and are in every scope. Because the origin is a quad on those kinds, a raw query can filter:
  `g.V().Has(g.IRI('origin'), 'framework')`.
- **Scope.** `codegraph.Load(ctx, qs, scope...)` returns the model for a scope, `app` (the default for every page and tool), `framework` or `all` (no scope given is `all`: the
  findings layer, the gate and the baseline need everything). It is applied once, in the model: nodes of the other origin are dropped, and so is every edge that touches
  one, so views, reports, findings and agent tools need no logic of their own. The model remembers what it dropped (`Hidden`: counts by origin and kind, and for each kept node the
  framework nodes it was connected to), which gives the banner's "hides N nodes", the inspector's `uses` line, and the error a lookup of a hidden node returns (`it is a route of the
  framework, which scope app hides; pass scope=all`). The gate refuses a scoped model. `graph_query`, the Cayley HTTP API and hand-added quads are not scoped: they read quads, not a model.
- **Labels.** Every node a tool returns (`NodeRef`) carries its `origin`. `graph_schema` states the scope and what it hides, names the application and the framework
  version, and, for a scope other than `all`, leaves out the run's quad counts (they are of the whole graph).
- **Externals.** An external node is what the project reaches outside itself: a package of the framework's public API (`ext:<module>/web`), a function of it (`ext:<module>/web.JSON`,
  `ext:<module>/config.Config.Get`), the one leaf for the framework's `internal/`, a library's package. A node's quads belong to one layer, so the `externals` layer writes every
  external node and the layers that point at it write only the edge (`imports_external`, `calls_external`, `handled_by_external`). The calls layer is bounded the same way: one leaf per
  public framework function, one per library package, none for the standard library. Versions come from the running binary's module table (`inspect --json`) when there is one,
  else from `go.mod` (`(devel)` for a local `replace`).

## The project profile

`govibe.yml` in the project root tells the tools where things are (`internal/profile`). Every key is optional and overrides the default for that key alone; an unknown key, an
absolute path or one that leaves the project is an error that names the file and the key (a run with an invalid profile fails; a typo must not leave the default in force).
`govibe.yml` is an input of every layer's fingerprint.

```yaml
framework: github.com/petars-dev/govibe   # the module the project is built on (default: this one)
config: [env.yml]                         # settings files, lowest first; a later declaration of a key replaces an earlier one
migrations: storage/migrations/sql
docs:
  dirs: [docs/public]
  specs: docs/specs
  spec_prefix: SPEC_
  root: [README.md, SKILL.md]
graph:
  dir: docs/graph
  derived: [docs/graph/*.bolt]            # generated, git-ignored, absent from a clone: a doc that names one is not stale
layout:                                   # layer -> directories; the longest match wins; a layer's entry replaces its default
  controllers: [controllers]
rules:
  code.unreferenced: off                  # off, error, warning or info; a rule that does not exist is an error
```

A directory claimed by two layers is an error. The profile also changes the verdict of the layers: with no settings file the `config` layer is `skipped` ("no settings file found
(looked for env.yml)"), so the config rules say they did not look instead of passing on nothing.

## Writer contract

What the Phase 1 writer (`internal/codegraph/writer.go`) does, given F7, F8 and F11:

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
   that says how to recover (delete the bolt file in `docs/graph/`, which is derived data but also holds any
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
g.V(g.IRI('table:items')).Out(g.IRI('has_column')).All()
g.V(g.IRI('table:items')).Out(g.IRI('fk_to')).All()               // tables it references
g.V(g.IRI('table:owners')).In(g.IRI('fk_to')).Has(g.IRI('type'), g.IRI('kind:table')).All()  // tables that reference it
g.V(g.IRI('col:item_tags.item_id')).Out(g.IRI('on_delete')).All()

// which code touches a table, and which struct is its row
g.V(g.IRI('table:items')).In(g.IRI('reads_table')).All()
g.V(g.IRI('table:items')).In(g.IRI('writes_table')).All()
g.V(g.IRI('table:items')).In(g.IRI('maps_to')).All()

// relations the schema implies, and how sure we are
g.V(g.IRI('table:items')).Out(g.IRI('many_to_many')).All()
g.V(g.IRI('col:items.owner_id')).Out(g.IRI('implicit_ref')).All()
g.V().Has(g.IRI('type'), g.IRI('kind:annotation')).Has(g.IRI('subject'), g.IRI('col:items.owner_id')).Out(g.IRI('confidence')).All()

// drift between the migrations and the live database, and migrations not yet applied
g.V().Has(g.IRI('source'), 'live').All()                           // made outside the migrations
g.V().Has(g.IRI('source'), 'migration').Has(g.IRI('type'), g.IRI('kind:table')).All()  // never applied (when a database is configured)
g.V().Out(g.IRI('drift')).All()
g.V().Has(g.IRI('applied'), false).All()

// where a table came from
g.V(g.IRI('table:items')).Out(g.IRI('introduced_in')).All()
```

```js
// Phase 6 (the opt-in layers): who calls whom, from a route down to a table
g.V(g.IRI('route:GET:/api/items')).Out(g.IRI('handled_by')).Out(g.IRI('calls')).All()
g.V(g.IRI('func:storage/itemstore.Store.List')).In(g.IRI('calls')).All()
g.V(g.IRI('route:GET:/api/items')).Out(g.IRI('handled_by')).Out(g.IRI('calls')).Out(g.IRI('calls')).Out(g.IRI('reads_table')).All()

// the calls that go through an interface, how sure each is, and which interface
g.V().Has(g.IRI('type'), g.IRI('kind:annotation')).Has(g.IRI('method'), 'interface_dispatch').Out(g.IRI('confidence')).All()
g.V().Has(g.IRI('subject'), g.IRI('func:business/services/items.Service.List')).Out(g.IRI('via')).All()

// the git layer: history of a file, and what changes with it
g.V(g.IRI('file:business/actions/hello.go')).Out(g.IRI('commits')).All()
g.V(g.IRI('file:business/actions/hello.go')).Out(g.IRI('co_changed_with')).All()

// which layers are switched off
g.V().Has(g.IRI('status'), 'off').All()
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

## Agent tools

The MCP server at `/dev/mcp` answers the common questions without Gizmo. The tools do not query the
store: each call first brings stale layers up to date, then reads every quad under a `layer:*` label into
an in-memory `Model` (`codegraph.Load`: about 20 ms for 4,500 quads, so nothing is cached) and works in Go.
Hand-added quads are not part of the model (`graph_query` still sees them). Every answer says what graph it
came from (`as_of`: when it was indexed, at which git sha, dirty or not).

| Tool | Answers | Inputs |
| --- | --- | --- |
| `graph_schema` | what the graph holds, how fresh it is, which tool answers what, example queries | none |
| `graph_context` | one-call briefing on a file, package, symbol, route, table, template, config key, spec... | exactly one of `path`, `symbol`, `route`, `table`, `node` |
| `graph_impact` | what is affected if it changes, which docs mention it, and (git layer) what usually changes with it | `node`, `depth` (exact hops, default 3, max 6; with the calls layer 6 and 10), `limit`, `kinds` |
| `graph_path` | how one node reaches another | `from`, `to`, `max_depth` |
| `graph_search` | nodes by words in their ID, name, path, title, signature or doc | `text`, `kinds`, `limit` |
| `graph_neighbors` | the nodes and edges around a node, to depth 3 | `node`, `direction`, `predicates`, `depth`, `limit` |
| `graph_findings` | findings (layering, stale docs, unused config...) | `severity`, `rule` (a trailing `*` matches a prefix), `limit` |
| `graph_reindex` | rebuild layers now | `layers` (those and the layers built on them; none means every layer that is on; naming `calls` or `git` switches it on) |
| `graph_query` | a raw Gizmo query | `query`, `limit` |

Also: the resources `govibe://graph/overview` (the architecture rendered from the graph: layers and their
packages, imports between layers, routes with handlers, tools, tables, config, specs, what is not covered)
and `govibe://graph/schema` (what `graph_schema` returns), and the prompt `explain-change` (changed `paths`
and/or a unified `diff` in; per file what it defines and affects, the docs that mention it, and the docs the
SKILL.md rule says to update, out). All output is bounded and says when it is cut (`truncated`, `total`).

### Confidence and method

What the graph says about reach, it says with a confidence from 0 to 100 and the `method` that produced it, so
an estimate is never read as a fact:

| Method | Confidence | Found how |
| --- | --- | --- |
| `edge` | 100 | a stored edge (`handled_by`, `imports` of a package, `renders`, `uses_config`, `fk_to`, a plain `calls` edge, ...) |
| `sql_literal` | 90 | `reads_table` / `writes_table`: SQL strings in `storage` and `business` code |
| `db_tag` | 90 | `maps_to`: `db:"col"` tags |
| `inferred` | the annotation's own figure | `implicit_ref` (see the `ann:` node: `name_match`, ...) |
| `interface_dispatch` | 80, 70, 60, 50 or 30, by the number of candidates | a `calls` edge through an interface the module declares: the callee is one of the types that could be on the other end |
| `table_level` | 70 | a column changed: the readers of its table, since SQL is matched to tables, not columns |
| `package_imports` | 60 for a package, 50 for a route or tool | the package that holds the affected code is imported by this one, or the route / tool is registered in it: **possible, not proven**, with the import chain in `through` (only when the calls layer is off, or for a changed type) |
| `package_scope` | 30 | same package as an affected helper function, which its neighbours may call (only when the calls layer is off) |

A chain is as sure as its weakest hop.

**With the calls layer off** the graph cannot say which function calls which. `graph_impact` reports the exact
dependents (reverse dependency edges) to the requested depth (default 3), then adds the package-level reach:
the importers of every package that holds affected code that outside code can use (an unexported function has
none), and the routes and tools registered in those packages. Same-package routes appear only when a touched
*function* is neither a handler nor a tool's registrar. For a table, `access` on a route is `read`, `write` or
`read+write`; it is the union over the functions of the packages it reaches. `graph_path` runs in two tiers:
exact dependency edges first, then, if that finds nothing, hops through a function's file and package
(`kind: containment`) and package imports (`kind: imports`), flagged `inferred` with confidence 50. A
`graph_context` of a route or function says what its package reaches (`reach`, method `package_imports`): a
table listed there is possible; no table listed means none is reached by anything the package imports, apart
from SQL built at run time. Imports are known per package, not per file, so a handler in a package that also
holds a database-touching handler shows that table too.

**With the calls layer on** reach is a walk over stored edges, and those false positives are gone (on the
fixture, `GET /api/hello` no longer lists `items`; on this repository a change to `actions.Hello` names the
two routes that call it, where the package-level estimate named 31):

- `graph_impact` follows `calls` backwards. The default depth is 6 (maximum 10), since a route is typically
  four or five hops from a table (table, store function, service, handler, route). Each route and tool carries
  the chain that reaches it in `through` (entry point first, the changed node last). `access` on a route, tool
  or function is the union over **every** call path that reaches the table, not the first one the search found.
  Wiring packages (`routes`, `main`, `cmd`) are listed when they call something affected and not followed
  further, since everything is eventually called from them. A changed type is followed through its methods;
  where the type itself is used is still estimated from package imports (a use of a type is not a call).
- `graph_path` between a route, function, tool or table is a chain of stored edges or nothing: the package-level
  tier is not tried, and "not found" is an answer (`No chain of calls ...`). Each step carries its `confidence`,
  `method` and, for an interface call, `via`; the path's `confidence` is its weakest step's. The surest
  chain wins (plain calls, then 90, 60, and the rest), then the shortest.
- `graph_context` of a function or a route lists what it `calls` and who it is `called_by` (surest first, with
  `via`), and its `reach` is the tables its calls reach: method `calls`, each table with the chain of
  functions that gets there (`via`), the functions that touch it, its `access` and `confidence`;
  `functions_reached` says how many functions the walk covered, so "no table is reached" is a measured
  statement ("neither do the 2 functions it calls"), not an absence of data.
- The request-flow view runs from each handler down the calls that lead to a table (and only those: a helper
  that touches no table is left out), with an interface call dashed and labelled with its confidence. The routes
  report has a "Tables reached" column.

With the git layer on, `graph_context` of a file, doc, spec, function or type has `history` (its file's last
commit, commits, churn, authors and `co_changed`), and `graph_impact` has `co_changed`: what usually changes with
the files that hold the changed code, which is where to look for the docs and files to update.

### Reference resolution

A tool that takes a node accepts what an agent has in hand, tried in this order, the first rule that finds
anything winning: a node ID (also in angle brackets); a route (`GET /api/hello`, `GET:/api/hello`, a bare
path for all methods, or a concrete URL, matched against `{param}` and `*` patterns; a route registered for
every method answers any method); a repository path (file, package directory, doc, spec or migration; a
`:line` suffix, `./`, an absolute path inside the repo or a Go import path also work); a bare name with an ID
prefix implied (`items` is `table:items`, `server.port` is `cfg:server.port`, `items.owner_id` is a column);
a Go symbol (`pkg.Name`, `dir/pkg.Name`, `Type.Method`, or a bare function, type, method or component name);
the same ignoring case. A reference that fits several nodes is an error that lists them; one that fits none
comes back with the nearest names.

## Findings

The `findings` layer derives problems from what the other layers wrote. It is built last, reads the store
exactly as the agent tools do (`codegraph.Load`, without its own previous output), and is stale whenever any
layer is. A finding is a node, so it is queryable, appears in `graph_context` of the node it is about, and
survives only as long as its cause.

```text
finding:<rule>:<subject id>[#<detail>]   type kind:finding
  rule, severity, message                literals; message says what is wrong and what to do
  name                                   the rule's title
  path, line                             where to look, when the rule knows
  about  -->  the node at fault          exactly one
rule:<name>                              type kind:rule
  name, title, severity, description     what it looks for
  checked                                it ran on this graph
  off                                    switched off in govibe.yml (a choice, not a rule that could not look)
  note                                   when it did not: why
```

Severities are `error` (breaks a rule the project states), `warning` (wrong or stale) and `info` (a smell).
`graph_findings` ranks `error`/`high`/`critical`, then `warning`/`medium`, then `info`/`low`. Each finding
carries the `path` and `line` the rule knows (a layering finding: where the forbidden import is first used).

**A rule that did not run has not looked.** A rule whose layers are not built (say, `docs.route_undocumented`
in an offline run with no router) is recorded with `checked=false` and the reason, and the layer is
`partial`. A rule that has nothing to look at (`db.drift` and `db.unapplied` with no live database) is
recorded the same way with `not applicable: ...` and does not make the layer partial. `graph_findings`
returns every rule with its count and whether it ran (a rule the profile switches off is `off`, not a gap), and says so in `note`, so "no findings" is never read
as "clean". When there is no findings layer at all it answers `available: false`.

| Rule | Severity | About | Finds |
| --- | --- | --- | --- |
| `layering` | error | the importing package (detail: the imported one) | an import the [layering rule](#package-classification) forbids; `path` and `line` are where the import is first used (a call, or a type in a signature or struct) |
| `framework.import` | error | the importing package, or `.templ` file (detail: the framework package) | an import its architecture layer may not take from the framework: business and storage nothing; controllers, middleware and views `web`; commands the root `govibe` package; wiring `govibe`, `config` and `web`; nothing from `internal/`, and `govibetest` only in tests. `path` and `line` are where it is written. Not applicable in the framework's own repository (`internal/rules` holds the table) |
| `docs.stale_ref` | warning | the doc (detail: `line:text`) | a doc names a file, symbol or page that does not exist; a path the profile lists as `derived` (generated, ignored by git, absent from a clone: `docs/graph/*.bolt`) is not judged |
| `docs.spec_missing` | warning | the spec row (detail: `line:text`) | a row marked implemented names something that does not exist |
| `docs.route_undocumented` | info | the route | an application route that no doc or spec mentions (a route of the framework, `/dev/*`, `/_govibe/*` and `/assets/*`, is not the project's to document) |
| `config.undeclared` | warning | the config key or environment variable | code reads it, no settings file declares it |
| `config.unused` | info | the config key | declared, and no code reads it (a read through a variable name is not seen) |
| `code.unreferenced` | info | the function | an exported package-level function no other indexed code references (tests are not indexed, methods are not checked, a name a `.templ` file uses counts) |
| `db.no_primary_key` | warning | the table | no primary key |
| `db.fk_unindexed` | warning | the column | a foreign key that no index starts with |
| `db.missing_fk` | info | the column (detail: the target column) | an `x_id` column that names a table (`implicit_ref`) but has no foreign key; the message gives the confidence |
| `db.drift` | warning | the table, column or index | with a live database: only one side has it, or the two describe it differently. A table that only an unapplied migration introduces is that migration's finding instead |
| `db.unapplied` | warning | the migration | with a live database: goose does not list it as applied |

Finding the file and line of a layering violation needs two facts the graph does not store as quads, which
the code index keeps (`ci.refs`, `ci.typeRefs`, `ci.identUses`, `ci.topLevel`): the findings layer reads the
code index for those, nothing else.

```js
// the findings, worst first, and what is wrong with one node
g.V().Has(g.IRI('type'), g.IRI('kind:finding')).Out(g.IRI('message')).All()
g.V(g.IRI('pkg:controllers')).In(g.IRI('about')).Out(g.IRI('message')).All()
// the rules, and which did not run
g.V().Has(g.IRI('checked'), false).Out(g.IRI('note')).All()
```

## Human surface

`/dev/graph` does not load every quad. The page is a shell (`internal/devtools/devui/static/graph.js` and `graph.css`, served at `/_govibe/static`) that
asks the server for **bounded views** and draws them with Cytoscape, so it stays quick however many quads
the store holds (a view is at most 300 nodes by default, 1,500 at most, and says when it left some out; the
server builds one in a few milliseconds from 90,000 quads). Data comes from `/dev/graph/data/*`, each call
first bringing stale layers up to date:

Every endpoint takes `scope=app|framework|all` (default `app`; any other value is a 400 that says what is valid).

| Endpoint | Returns |
| --- | --- |
| `GET /dev/graph/data/status` | the freshness banner: when and at which git sha the graph was indexed, quads, `+added/-removed` against the previous run, each layer's status and which are stale, the finding counts and which rules did not run |
| `GET /dev/graph/data/view?preset=&focus=&depth=&kinds=&limit=` | a drawable subgraph: nodes (kind, label, finding count and worst severity, a flag), edges (violating imports and inferred relations marked), the kinds present, `total`, `truncated`, notes. `preset=manual` is the hand-added quads (the one thing the model does not hold) |
| `GET /dev/graph/data/node?id=` | the inspector of any node, by ID or any reference `graph_context` takes: attributes, edges grouped by predicate (25 per group), findings about it, links |
| `GET /dev/graph/data/search?q=&kinds=&limit=` | `graph_search` |
| `GET /dev/graph/data/findings?severity=&rule=&limit=` | `graph_findings` |
| `GET /dev/graph/data/table?name=` | what refers to a table (code that reads or writes it, routes and tools that may reach it, docs): `graph_impact` of the table grouped for the database explorer, with the same confidence labels |

The page opens on the **App map** (the first preset): each route, the handler that serves it, the actions, services and
stores it reaches (through the calls layer, else estimated from package imports and drawn dashed), the tables, and the templates,
config keys and docs on the way. The toolbar has the **scope switch** (My app, Framework, All): the URL carries `scope=`, the
choice is remembered per browser, every data call passes it, and the banner always says which scope is showing and how many nodes it
hides; in scope Framework or All the framework's nodes have a double outline and an `origin` badge in the inspector, and in the app's scope the
inspector says what a node uses of the framework as one line of text (`uses framework: web, config`), not as nodes. Reports and their links carry the scope too.

Other presets: **Architecture** (packages in columns by architecture layer, a red edge for each import that breaks
the rule), **Request flow** (route, middleware, handler, what it renders), **Database ER** (tables; declared
foreign keys solid, inferred relations dashed with their confidence), **Docs ↔ code** (which docs mention
which packages, routes, tables and tools; an orange dashed outline means nothing mentions it), **Config →
usage**, **Findings**, and **Hand-added quads**. Any node opens as a focus view (its neighbourhood, a few
edges per predicate, `more` says what was left out); double-clicking a node adds its neighbours to the
canvas. A node's border is red, amber or blue when a finding is about it. The kind chips hide a kind
(colour and shape per kind); the inspector links to the editor (`vscode://file/<abs path>:<line>`), to
`/dev/docs/*`, to `/dev/db#table=<name>` (only when the database explorer is mounted) and to the node's place
on a report. `/dev/graph#node=<id>` and `#preset=<id>` are deep links.

Four generated reference pages, rebuilt from the graph on every load (so they cannot drift from the code),
replace the hand-kept route table and schema listing of ARCHITECTURE.md and MEMORY_SCHEMA.md:
`/dev/graph/reports/routes` (every route, its handler, middleware and docs), `/config` (every key, its
environment variable, default, whether it is declared, who reads it; secrets are never shown),
`/schema` (tables, columns, indexes, foreign keys, inferred relations with confidence, the code that reads
and writes each table, migrations) and `/layers` (the packages of each architecture layer, the imports between
layers, the rule, and the violations). Report rows carry stable anchors (`Anchor(id)`), so the inspector and
the findings link straight to them. The database explorer's table view shows a "Referenced by" panel from
`/dev/graph/data/table`.

## The CLI and the CI gate

`govibe graph index` is the offline counterpart of the boot indexer and of `POST /dev/graph/reindex`: it
indexes the project into a store, prints what each layer did, and can turn the findings into an exit status.

```sh
go build -o govibe ./cmd/govibe
./govibe graph index --dir /tmp/govibe-graph --calls --git     # what is wrong with the project?
./govibe graph index --dir /tmp/govibe-graph --fail-on error --baseline docs/graph/baseline.json   # the gate
./govibe graph index --fail-on error --baseline docs/graph/baseline.json --update-baseline         # accept what is there
make graph-check                                                # the same gate as CI, locally
```

| Flag | Meaning |
| --- | --- |
| `--root` | the repository root (the directory holding `go.mod`), default `.` |
| `--dir` | the store's directory, default the profile's `graph.dir` under `--root` (`docs/graph`). **Use a scratch directory** while a dev server runs: it holds the default store's lock, and the command then says so and points at `POST /dev/graph/reindex` |
| `--calls`, `--git` | switch on the opt-in layers (`--git` needs the full history: a shallow clone makes the layer `partial`) |
| `--fail-on` | `error`, `warning`, `info` or `none` (default): exit 1 when a finding at or above that severity is not in the baseline |
| `--baseline` | a JSON file of accepted findings (IDs, with their rule and message so a review shows what is accepted) |
| `--update-baseline` | write the findings at or above `--fail-on` (`error` if none) to `--baseline` and exit 0. Each entry records a `reason` and the date; an entry the file already has keeps both, and a finding new to it needs `--reason` |
| `--reason` | with `--update-baseline`: why the findings new to the baseline are accepted instead of fixed |
| `--no-inspect` | do not ask the application for its routes (see below): index without a route table |
| `--format` | `text`, `json` (the report and the gate verdict), or `github` (text plus a workflow annotation, `::error file=...,line=...`, for each new finding) |
| `--verbose` | log every layer as it is indexed (quiet by default: the report says it) |

**Applications.** A project whose `go.mod` requires the framework is an application, and has a binary to ask: `govibe graph index` runs its own
`go run -tags govibe_dev . inspect --json` (no database, no dev mount, no scheduler), and the route table, who registered each route and the modules of the build go
to the indexer, so the route rules (`docs.route_undocumented`) run offline. The framework's own settings and tools come from this tool's manifest (the tool and the
library are one version). This compiles and runs the project's own code (as `go run` does, with a scrubbed environment: the toolchain's and the path, no other variable), so a project that is not trusted is indexed with `--no-inspect`. A build that fails is status 2, with how to go on. Any other project has no router: the routes layer is skipped.

**Exit status**: 0 on success, 1 when the gate finds a new finding, 2 when indexing itself failed (a layer
ended in `error`, the store is locked, the baseline file is missing, a flag is wrong). A run in which a layer
failed never passes: a project that was not checked is not a clean one. Rules that did not run (offline there
is no router, so `docs.route_undocumented` cannot) are listed in the report, and a graph with no findings layer
is an error.

**The baseline.** Only a *new* finding fails: a project is rarely clean on the day a gate is switched on, and a
gate that fails on day one gets switched off. `docs/graph/baseline.json` is the known debt (the framework's own
baseline is empty: a finding is fixed, or accepted with a reason), committed and reviewed like any
other change. The gate keys on finding IDs, so a finding that moves to another line stays accepted, and a
baseline entry that no finding matches any more is reported as paid debt (`--update-baseline` drops it). Accepting a finding means saying why: an entry has a
`reason` (required) and a `date`, and the gate fails, whatever the findings, while an entry has no reason. A
finding whose ID carries a line (a stale reference in a doc) is a new finding when the line moves: gate on
`error`, whose rule has stable IDs, and keep `warning` for the report.

**CI** (`.github/workflows/graph.yml`): on every push to `main` and every pull request, check out, set up Go
from `go.mod`, and run `go run ./cmd/govibe graph index --dir "$RUNNER_TEMP/govibe-graph" --calls --fail-on
error --baseline docs/graph/baseline.json --format github`. The scratch `--dir` cannot collide with a dev
server's lock; `--calls` makes the job also check that the module type-checks; each new finding shows up as an
annotation on the line it is about. Add `--git` and `fetch-depth: 0` to index history too.

<!-- framework-only:start -->
## Fixture

`internal/codegraph/testdata/fixture/` is a miniature GoVibe-shaped app (module
`example.com/fixture`) built on a stub framework, `internal/codegraph/testdata/stubfw` (module `example.com/stubfw`: a root package with the `App` declaration, a `web` and a `config`
package, an `internal` one; the fixture's `govibe.yml` says `framework: example.com/stubfw`, and its `go.mod` replaces the stub with the directory next to it), that the indexer's golden tests run against.
`internal/codegraph/testdata/fixture_layout/` is a second app whose layout is not the default (everything under `internal/`, settings in `config/`, migrations in `db/`, docs in `doc/`,
specs `SP_*.md`), described by its `govibe.yml`; it tests the profile. It
compiles, is never run, and is invisible to the main module (`go list ./...` and `go vet ./...` skip
`testdata`). Each layer's golden N-Quads are `testdata/golden/<layer>.nq`; regenerate them with
`go test ./internal/codegraph -update` after an intended change and review the diff. The
fixture is never run, so
the `routes` layer is fed the runtime facts its router would report (`fixtureRoutes` in `layers_test.go`);
`routes_chi_test.go` covers the real chi traversal on a router built from this repository's handlers.

| Fixture file | What it exercises |
| --- | --- |
| `internal/codegraph/testdata/fixture/main.go` | root package `pkg:.`, `arch_layer=wiring`, package doc; `cfg.Server.Port` and `cfg.Database.DSN` reads, and an `os.Getenv` of a variable `env.yml` does not declare |
| `internal/codegraph/testdata/fixture/routes/routes.go` | wiring; imports every other layer; six routes (`GET /`, `GET /api/hello`, `GET /api/orders`, `GET /api/orders/{id}`, `/dev/mcp`, `/assets/`) and a middleware wrapper |
| `internal/codegraph/testdata/fixture/env.yml`, `internal/codegraph/testdata/fixture/config/config.go` | placeholders with defaults, a default containing a colon, sensitive keys whose defaults must not appear, a static value, a key bound to a struct field that nothing reads, keys reachable only through the dynamic `Get` |
| `internal/codegraph/testdata/fixture/business/actions/notify.go` | `config.Get("queue.broker")` of a whole group, and of an undeclared key |
| `internal/codegraph/testdata/fixture/assets/templates/*.templ` | components, a `children...` layout, `@Layout(...)` and `@Footer()` calls, a doc comment, a component whose Go package has only generated output |
| `internal/codegraph/testdata/fixture/controllers/home.go` | a handler that renders a templ component (`func --renders--> tmpl`), and calls the stub framework's `web.HTMXScript` (`imports_external` and `calls_external` edges, an `externals` node) |
| `internal/codegraph/testdata/stubfw/**` | the stub framework: `stubfw.Main` (called by the fixture's `main.go`), the helpers of its `web` package, a `config` package, an `internal/kernel` the stub's own root calls |
| `internal/codegraph/testdata/fixture_layout/**` | an app with a non-default layout and its `govibe.yml`: layers by directory, settings in two files (the later wins), migrations in `db/migrations`, specs by prefix `SP_`, a derived path a doc names, a rule switched off and one raised in severity, a layering violation |
| `internal/codegraph/testdata/fixture/mcp/mcp.go` | a stand-in for the MCP SDK (the fixture is standard-library only) |
| `internal/codegraph/testdata/fixture/docs/**`, `internal/codegraph/testdata/fixture/README.md` | an architecture page with every kind of mention (path, route, symbol, `Type.Method`, config key and group, environment variable, tool, template, links, a stale reference), a guide, and `SPEC_HELLO.md` whose status table has implemented, planned, partial and skipped rows, and one implemented row (`Audit trail`) that names a file that does not exist (`docs.spec_missing`) |
| `internal/codegraph/testdata/fixture/controllers/hello.go` | acceptance query 1: `HelloAPI` calls `actions.Hello`, and no table is reachable |
| `internal/codegraph/testdata/fixture/controllers/orders.go` | acceptance query 2: `ListOrders` goes handler → service → store (clean); `GetOrder` calls the store directly (**layering violation**, on purpose); handler factories returning closures; unexported `writeJSON`. For the calls layer: a call inside each closure, a call through the `Lister` interface (two candidates, the store and the service itself: confidence 70), and `HomePage`'s call into generated output (no edge) |
| `internal/codegraph/testdata/fixture/business/actions/hello.go` | single-function action; a two-sentence doc for first-sentence extraction |
| `internal/codegraph/testdata/fixture/business/services/orders/orders.go` | interface type, struct type, pointer and value receivers, an unexported method |
| `internal/codegraph/testdata/fixture/storage/orderstore/orderstore.go` | SQL as package constants and as literals in function bodies (a join, an `INSERT ... SELECT`, a `+` concatenation, a raw string, a `DELETE`) and one dynamic statement that names no table; `db:"…"` struct tags (a struct that maps to one table, one that fits several and maps to none) |
| `internal/codegraph/testdata/fixture/storage/migrations/sql/0001_init.sql` | one goose migration, the sample from the spec (users, products, orders, order_items): a sensitive column default, unnamed and named keys and foreign keys, a composite-primary-key junction, and an implicit reference (`orders.user_id`, no foreign key). The cases beyond this (`ALTER` of existing tables, a shared primary key, renames and drops, a column that names no table, drift against a live database, unapplied migrations) are an inline second migration in `db_extra_test.go` (and `db_live_test.go`), so the tree carries no schema it does not need |
| `internal/codegraph/testdata/fixture/storage/orderstore/zz_generated.go` | a `// Code generated … DO NOT EDIT.` file that is indexed with `generated=true` |
| `internal/codegraph/testdata/fixture/middleware/logging.go` | middleware function |
| `internal/codegraph/testdata/fixture/devmcp/devmcp.go` | `arch_layer=dev`; a dev package importing storage; two `mcp.AddTool` registrations (a handler struct with `omitempty` and `jsonschema` tags, and `struct{}`) |
| `internal/codegraph/testdata/fixture/assets/templates/home_templ.go` | generated templ output that must be skipped; the package has no other Go file, so it must not appear |

Edit the fixture only together with its goldens, in one commit. Facts asserted independently of the
golden (so a wrongly regenerated golden is still caught) live in `Test<Layer>LayerFixtureFacts`; the
same invariants run over this repository in `TestRepoInvariants` and, for every layer at once,
`TestWholeGraphInvariants`.
<!-- framework-only:end -->

## Open decisions

Decided no later than the phase named.

| Phase | Question | Leading option |
| --- | --- | --- |
| ~~1~~ | ~~How are imports of packages outside the module represented?~~ **Decided (Phase 1):** counted in an `external_imports` int on the package; add `pkg:ext/<import path>` nodes only if agents ask "who imports X". | |
| ~~2~~ | ~~Method token for routes registered with `Handle` and no method~~ **Decided (Phase 2):** one route with method `ANY`. `chi.Walk` reports such a route nine times (once per method), which is nine nodes for one registration. | |
| ~~2~~ | ~~Direction and predicate for tool → backing function~~ **Decided (Phase 2):** `func --registers--> tool`, the function that calls `mcp.AddTool`; a handler literal is a closure and resolves to it. | |
| ~~2~~ | ~~`tmpl:` ID: package name or directory~~ **Decided (Phase 2):** directory, like `func:` and `typ:`, because two packages can share a name (`tmpl:assets/templates.Home`). | |
| ~~2~~ | ~~Spec item ID: row number or text~~ **Decided (Phase 2):** a slug of the Item cell. A row number shifts whenever a row is inserted, which churns every mention of the items below it. | |
| 2 | `documents` and unresolved doc references | `documents` is not emitted: it needs an explicit convention to mean more than `mentions`. **Decided (Phase 5):** a reference that is unambiguously a pointer into this repository and resolves to nothing is recorded as an `unresolved_ref` literal on the doc (or the spec row), conservatively: see [what the docs layer reads](#what-the-phase-2-layers-read). |
| ~~3~~ | ~~How is an *inferred* edge marked? A triple has no room for method or confidence.~~ **Decided (Phase 3):** the edge stays a plain triple; each inferred edge has an annotation node `ann:<hash>` with `subject` and `object` (edges to the nodes), `predicate` (the predicate's name, a string), `method` and `confidence` (0 to 100), and `via` for a junction. | |
| ~~5~~ | ~~Drift as findings~~ **Decided (Phase 5):** `db.no_primary_key`, `db.fk_unindexed`, `db.missing_fk`, `db.drift` and `db.unapplied`; the last two apply only when a live database was read. | |
| ~~5~~ | ~~Final layering rule and allow-list~~ **Decided (Phase 5):** the table under [Package classification](#package-classification), in `layering.go`; `wiring` and `dev` are the allow-list. The overview and the layers report read the same function. | |
| ~~5~~ | ~~The `finding` shape~~ **Decided (Phase 5):** [Findings](#findings). One deviation from the first draft: a finding whose rule has several problems on one node carries a `#<detail>` suffix, and there is a `rule` kind so that "did not run" is part of the graph. | |
| ~~6~~ | ~~Sharper reach before the `calls` layer~~ **Decided (Phase 6):** not done. File-level import edges would change the code layer's output and goldens, and the `calls` layer (opt-in) supersedes them: with it a route's reach is exact. Without it the package-level estimate stays, labelled. | |
| ~~6~~ | ~~How is a call through an interface represented?~~ **Decided (Phase 6):** one `calls` edge per candidate type, each annotated (`interface_dispatch`, a confidence that falls with the number of candidates, `via` the interface), using the Phase 0 annotation decision. Interfaces the module does not declare are not expanded. | |
| ~~6~~ | ~~What happens to an opt-in layer that is off?~~ **Decided (Phase 6):** it has status `off`, holds no quads (an earlier run's are removed), is never stale, and asking for it by name (`graph_reindex`) switches it on for the life of the server. | |
| 6 | Findings from the opt-in layers | None yet: no rule reads `calls` or `git`, so the findings layer does not depend on them. Candidates (a handler that reaches storage through a call, a hot file with open findings) are in the spec's backlog. |

The `graph_query` tool description now shows the `'<iri>'` idiom (done in Phase 1).
