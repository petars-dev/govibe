# shop: memory and database schema

This app keeps no state yet: it has no tables and no in-memory stores. Its one migration (`storage/migrations/sql/0001_init.sql`)
creates nothing; it only keeps goose's bookkeeping consistent for the migrations that come later.

Record every table, index and constraint here as migrations add them, and any in-memory cache with its keys and eviction.

## Tables
- `categories`: created by `storage/migrations/sql/0002_create_categories.sql`; columns `id` (the primary key) and `created_at`. Add every column, index and constraint here as migrations change it.
