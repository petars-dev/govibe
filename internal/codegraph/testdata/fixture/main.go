// Command fixture is a miniature GoVibe-shaped app that serves as a stable
// input for the graph indexer's golden tests (see docs/public/GRAPH_SCHEMA.md).
// It is built on a stub framework (testdata/stubfw, imported as example.com/stubfw),
// compiles but is never run, and it is a separate module so the main repository's
// tooling ignores it. Changing any file here changes the golden output, so edit it on
// purpose and regenerate the goldens in the same commit.
package main

import (
	"database/sql"
	"log"
	"os"

	"example.com/fixture/config"
	"example.com/fixture/routes"
	"example.com/stubfw"
)

func main() {
	cfg := config.Load()
	if os.Getenv("FIXTURE_DEBUG") != "" {
		log.Println("debug logging on")
	}
	var db *sql.DB // never opened: the fixture is only ever parsed
	log.Println("database configured:", cfg.Database.DSN != "")
	log.Println("listening on port", cfg.Server.Port)
	stubfw.Main(stubfw.App{
		Name:     "fixture",
		Handler:  routes.New(db),
		Commands: func() []stubfw.Command { return routes.Console(db) },
		DevTools: routes.DevTools,
	})
}
