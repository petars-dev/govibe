# GoVibe Memory & Database Schema

This document records the persistent and in-memory data models.

## Database (MariaDB/MySQL)
- Baseline goose migration exists (0001_init.sql). Add new migrations under storage/migrations/sql/.
- On startup, embedded migrations are applied (goose Up).

Schema notes:
- Document tables, indexes, constraints here as they are added.
- Any table in the app's database can be introspected live at `/dev/db` (schema, indexes, foreign keys, sample rows) or via the `db_describe_table`/`db_list_tables` MCP tools — prefer that over keeping this file in perfect sync for exploratory work; still update it for schema decisions that need a written record.

## Embedded graph store (dev-only)
- Cayley quad store persisted to `docs/graph/indexes.bolt` (bolt allows one process at a time). The file is derived data and is git-ignored; it never exists in production.
- Today it holds only quads added by hand through `/dev/graph`. The planned contents (node kinds, ID formats, predicates, labels) and the rules for writing and querying them are in `docs/public/GRAPH_SCHEMA.md`.

## In-memory stores
- If any in-memory stores or caches are added, describe their keys and eviction strategy.
