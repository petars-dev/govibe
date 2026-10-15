# GoVibe Memory & Database Schema

This document records the framework's own persistent and in-memory data. The framework has no database tables and owns no migrations: an
application's schema is the application's, documented in the application's own `docs/public/MEMORY_SCHEMA.md` (see `examples/hello` for the shape).

## Embedded graph store (dev-only)
- Cayley quad store persisted to a bolt file in the application's `docs/graph/` directory (bolt allows one process at a time). The file is derived data
  and is git-ignored; it never exists in production, because a build without the `govibe_dev` tag does not contain the code that opens it.
- It holds the graph indexer's output (the `code`, `routes`, `commands`, `templates`, `config`, `mcp`, `db`, `docs`, `externals`, `findings` and `meta` layers, plus the opt-in `calls` and
  `git` layers), rebuilt on every server boot and by `POST /dev/graph/reindex`, and refreshed layer by layer when a `/dev/graph` page load or a graph tool call
  finds a layer's inputs changed, plus any quads added by hand through `/dev/graph`. Indexer quads carry a `layer:<name>` IRI label and are replaced wholesale per
  layer; unlabelled quads and quads with any other label are never touched. Node kinds, ID formats, predicates, labels, and the rules for writing and querying
  them are in `docs/public/GRAPH_SCHEMA.md`.

## In-memory stores
- The dev server keeps the loaded graph model per request (nothing is cached across calls) and the indexer's per-layer fingerprints; both are rebuilt on boot.
