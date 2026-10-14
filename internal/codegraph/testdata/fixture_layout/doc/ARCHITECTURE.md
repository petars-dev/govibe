# Shop architecture

Handlers live in `internal/http/handlers`, rules in `internal/core/orders`, SQL in `internal/store/orderstore`.

- `GET /orders` is the list; the `orders` table holds the rows.
- The nightly report is written to `build/report.out`, which is generated and not in a clone.
- `internal/core/gone.go` was removed long ago: a stale reference.
