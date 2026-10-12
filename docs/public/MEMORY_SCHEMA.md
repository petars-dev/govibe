# GoVibe Memory & Database Schema

This document records the persistent and in-memory data models.

## Database (MariaDB/MySQL)
- Baseline goose migration exists (0001_init.sql). Add new migrations under storage/migrations/sql/.
- On startup, embedded migrations are applied (goose Up).

Schema notes:
- Document tables, indexes, constraints here as they are added; `/dev/graph/reports/schema` lists the live schema as the graph sees it, generated on every load. (The graph's `db` layer also derives the schema from the migrations, and from the live database when one is configured, and flags where the two disagree: query `g.V().Has(g.IRI('source'), 'live')` or see `docs/public/GRAPH_SCHEMA.md`.)
- Any table in the app's database can be introspected live at `/dev/db` (schema, indexes, foreign keys, sample rows) or via the `db_describe_table`/`db_list_tables` MCP tools — prefer that over keeping this file in perfect sync for exploratory work; still update it for schema decisions that need a written record.

## Embedded graph store (dev-only)
- Cayley quad store persisted to a bolt file in `docs/graph/` (bolt allows one process at a time). The file is derived data and is git-ignored; it never exists in production.
- It holds the graph indexer's output (currently the `code`, `routes`, `templates`, `config`, `mcp`, `db`, `docs`, `findings` and `meta` layers, about 7,300 quads on this repo), rebuilt on every server boot and by `POST /dev/graph/reindex`, and refreshed layer by layer when a `/dev/graph` page load or a graph tool call (`graph_query`, `graph_context`, ...) finds a layer's inputs changed, plus any quads added by hand through `/dev/graph`. Indexer quads carry a `layer:<name>` IRI label and are replaced wholesale per layer; unlabelled quads and quads with any other label are never touched. Node kinds, ID formats, predicates, labels, and the rules for writing and querying them are in `docs/public/GRAPH_SCHEMA.md`.

## In-memory stores
- If any in-memory stores or caches are added, describe their keys and eviction strategy.
