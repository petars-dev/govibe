package routes

import (
	"database/sql"
	"time"

	"example.com/fixture/business/services/orders"
	"example.com/fixture/commands"
	"example.com/fixture/storage/orderstore"
	"example.com/stubfw"
)

// Console declares the CLI commands and which of them run on a schedule.
func Console(db *sql.DB) []stubfw.Command {
	svc := orders.New(orderstore.New(db))
	return []stubfw.Command{
		{Use: "orders:export", Short: "Export yesterday's orders", Schedule: stubfw.Cron("0 2 * * *"), Run: commands.ExportOrders(svc)},
		{Use: "reports:digest", Short: "Send the digest", Schedule: stubfw.Every(6 * time.Hour), Run: commands.Digest(svc)},
		{Use: "cache:warm", Short: "Warm the cache", Run: commands.WarmCache},
	}
}
