// Command fixture is a miniature GoVibe-shaped app that serves as a stable
// input for the graph indexer's golden tests (see docs/public/GRAPH_SCHEMA.md).
// It compiles but is never run, and it is a separate module so the main
// repository's tooling ignores it. Changing any file here changes the golden
// output, so edit it on purpose and regenerate the goldens in the same commit.
package main

import (
	"database/sql"
	"log"
	"net/http"

	"example.com/fixture/routes"
)

func main() {
	var db *sql.DB // never opened: the fixture is only ever parsed
	log.Fatal(http.ListenAndServe(":8080", routes.New(db)))
}
