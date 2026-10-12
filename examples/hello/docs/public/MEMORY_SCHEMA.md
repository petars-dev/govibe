# Hello GoVibe: memory and database schema

This app keeps no state: it has no tables and no in-memory stores. Its one migration (`storage/migrations/sql/0001_init.sql`)
creates nothing; it only keeps goose's bookkeeping consistent for the migrations that come later.

Record every table, index and constraint here as migrations add them, and any in-memory cache with its keys and eviction.
