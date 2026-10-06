# Fixture Architecture

The fixture follows the GoVibe layering: handlers in `controllers/`, rules in `business/`, SQL in `storage/orderstore`.

- `GET /api/hello` is served by `controllers.HelloAPI` and never touches a table.
- `GET /api/orders` goes through `orders.Service.List` down to `Store.List`.
- `GET /dev/mcp` mounts the dev tooling; agents call `fixture_echo` or `fixture_ping` there.
- `controllers.GetOrder` calls `orderstore.Store` directly, on purpose; the feature status is in [the hello spec](/dev/docs/specs/hello).
- The port comes from `server.port` (`PORT`) and the database from `FIXTURE_DSN`; mail settings live under `mail.smtp.*`.
- `FIXTURE_DEBUG` turns on debug logging. It is read in `main.go` but never declared.
- The landing page is `templates.Home`, defined in `assets/templates/home.templ`.
- Orders live in the `orders` table (`orders.user_id` references `users`), and `order_items` joins `orders` and `products`; the history is in `storage/migrations/sql/0001_init.sql`.
- A stale reference: `legacy/handler.go` was removed long ago.

More in the [guide](GUIDE.md), and the project [README](../../README.md).
