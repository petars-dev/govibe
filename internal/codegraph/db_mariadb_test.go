package codegraph

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cayleygraph/quad"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/petars-dev/govibe/internal/dbexplorer"
)

// testDSNEnv points these tests at a MariaDB/MySQL server; without it they are
// skipped. Each test creates a uniquely named schema of its own and drops it,
// so the server may also hold real dev data:
//
//	GOVIBE_TEST_MARIADB_DSN='root@tcp(127.0.0.1:3306)/' go test ./internal/codegraph
const testDSNEnv = "GOVIBE_TEST_MARIADB_DSN"

// scratchDB creates a schema, applies the fixture's migrations plus extraMigration
// to it the way goose would (the Up sections, statement by statement), and
// returns an Explorer over it, and the root those migrations came from.
func scratchDB(t *testing.T) (*dbexplorer.Explorer, *sqlx.DB, string) {
	t.Helper()
	dsn := os.Getenv(testDSNEnv)
	if dsn == "" {
		t.Skipf("set %s to run against a real MariaDB/MySQL server", testDSNEnv)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("%s: %v", testDSNEnv, err)
	}
	cfg.DBName = ""
	admin, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("govibe_idx_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	db, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	root := richRoot(t)
	sc, err := newTestWorld(t, root, Options{}).migrationScan()
	if err != nil {
		t.Fatal(err)
	}
	for _, sf := range sc.files {
		src, err := os.ReadFile(sf.abs)
		if err != nil {
			t.Fatal(err)
		}
		up, _ := gooseUp(string(src))
		for _, st := range splitStmts(up, lexSQL(up)) {
			if _, err := db.Exec(st.text); err != nil {
				t.Fatalf("%s: %s\n%v", sf.rel, abbreviate(st.text), err)
			}
		}
	}
	return dbexplorer.New(db, dbexplorer.Options{}), db, root
}

// The static parser against the truth: the fixture's migrations, applied to a
// real server, describe the same schema the parser derived without one. Types,
// nullability, index names (including the ones the server makes up), the index
// InnoDB adds for a foreign key, and which keys exist all agree, so merging the
// two shows no drift and every element on both sides.
func TestStaticSchemaMatchesARealServer(t *testing.T) {
	ex, _, root := scratchDB(t)
	snap, err := ex.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	opts := fixtureOptions()
	opts.Schema = func(context.Context) (*dbexplorer.Snapshot, error) { return snap, nil }
	opts.liveTTL = -1
	merged := buildLayer(t, newTestWorld(t, root, opts), dbLayer{})
	static := buildLayer(t, newTestWorld(t, root, fixtureOptions()), dbLayer{})
	if len(merged.warnings) != 0 {
		t.Errorf("warnings: %v", merged.warnings)
	}

	// Nothing is drift, and nothing exists on one side only.
	for _, q := range merged.quads {
		switch q.Predicate.(quad.IRI) {
		case "drift":
			t.Errorf("the parser and the server disagree: %v", q)
		case "source":
			if q.Object != quad.Value(quad.String("both")) {
				t.Errorf("only one side has %v", q)
			}
		}
	}

	// The two graphs name the same tables, columns, indexes and edges. (They
	// differ only in what a server alone knows, which the check below lists.)
	structure := func(quads []quad.Quad) []string {
		var out []string
		for _, q := range quads {
			switch q.Predicate.(quad.IRI) {
			case "type", "has_column", "has_index", "fk_to", "implicit_ref", "many_to_many", "name", "introduced_in":
				out = append(out, q.Subject.String()+" "+q.Predicate.String()+" "+q.Object.String())
			}
		}
		sort.Strings(out)
		return out
	}
	got, want := structure(merged.quads), structure(static.quads)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the schema differs between the server and the parser:\n%s", lineDiff(want, got))
	}

	// What only a server knows.
	f := facts{merged.quads}
	if got := f.one(t, "col:order_items.order_id", "fk_name"); got != str("fk_items_order") {
		t.Errorf("fk_name = %v", got)
	}
	if got := f.one(t, "col:order_items.order_id", "on_delete"); got != str("CASCADE") {
		t.Errorf("on_delete = %v", got)
	}
	if got := f.one(t, "table:users", "comment"); got != str("Accounts") {
		t.Errorf("table comment = %v", got)
	}
	if got := f.one(t, "col:users.email", "comment"); got != str("login name") {
		t.Errorf("column comment = %v", got)
	}
	if got := f.one(t, "col:users.email", "data_type"); got != str("varchar(320)") {
		t.Errorf("type = %v", got)
	}
}

// goose's own table says which migrations are applied.
func TestAppliedMigrationsFromGoose(t *testing.T) {
	ex, db, root := scratchDB(t)
	for _, stmt := range []string{
		"CREATE TABLE goose_db_version (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, version_id BIGINT NOT NULL, is_applied TINYINT(1) NOT NULL, tstamp TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP)",
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (1, 1)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	opts := fixtureOptions()
	opts.Schema = ex.Snapshot
	opts.liveTTL = -1
	out := buildLayer(t, newTestWorld(t, root, opts), dbLayer{})
	f := facts{out.quads}
	for mig, want := range map[string]bool{"mig:0001_init": true, "mig:0002_extras": false} {
		if got := f.one(t, mig, "applied"); got != quad.Value(quad.Bool(want)) {
			t.Errorf("%s applied = %v, want %v", mig, got, want)
		}
	}
	if f.exists("table:goose_db_version") {
		t.Error("the bookkeeping table is not part of the schema")
	}
}

// Rows never reach the graph, whatever the tables hold.
func TestNoRowDataReachesTheGraph(t *testing.T) {
	ex, db, root := scratchDB(t)
	for _, stmt := range []string{
		"INSERT INTO users (email, password_hash) VALUES ('ada.lovelace@example.com', 'hunter2-secret-hash')",
		"INSERT INTO products (sku, price) VALUES ('SKU-ULTRA-RARE', 12.50)",
		"INSERT INTO orders (user_id, total) VALUES (1, 424242)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	opts := fixtureOptions()
	opts.Schema = ex.Snapshot
	opts.liveTTL = -1
	out := buildLayer(t, newTestWorld(t, root, opts), dbLayer{})
	all := nquads(t, out.quads)
	for _, secret := range []string{"ada.lovelace", "hunter2", "SKU-ULTRA-RARE", "424242", "12.50"} {
		if strings.Contains(all, secret) {
			t.Errorf("row data %q reached the graph", secret)
		}
	}
}
