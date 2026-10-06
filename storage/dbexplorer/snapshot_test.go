package dbexplorer

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// testDSNEnv names the environment variable that points these tests at a
// MariaDB/MySQL server. The account needs CREATE and DROP on new schemas: each
// test creates a uniquely named schema of its own, uses only that, and drops it,
// so it can be pointed at a server that also holds real dev data. Without the
// variable the tests are skipped.
//
//	GOVIBE_TEST_MARIADB_DSN='root@tcp(127.0.0.1:3306)/' go test ./storage/dbexplorer
const testDSNEnv = "GOVIBE_TEST_MARIADB_DSN"

// scratchSchema creates a new schema on the server named by testDSNEnv, runs
// ddl in it, and returns an Explorer over it. The schema is dropped when the
// test ends.
func scratchSchema(t *testing.T, ddl ...string) (*Explorer, *sqlx.DB) {
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

	name := fmt.Sprintf("govibe_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + quoteIdent(name)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE " + quoteIdent(name)) })

	cfg.DBName = name
	db, err := sqlx.Connect("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatalf("connect to %s: %v", name, err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return New(db, Options{}), db
}

var snapshotDDL = []string{
	"CREATE TABLE users (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, email VARCHAR(255) NOT NULL COMMENT 'login name', " +
		"role ENUM('admin','member') NOT NULL DEFAULT 'member', created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, " +
		"UNIQUE KEY ux_email (email)) ENGINE=InnoDB COMMENT='accounts'",
	"CREATE TABLE products (id INT NOT NULL AUTO_INCREMENT, sku VARCHAR(40) NOT NULL, name VARCHAR(100) NULL DEFAULT NULL, PRIMARY KEY (id), KEY ix_name (name))",
	"CREATE TABLE orders (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, " +
		"total DECIMAL(10,2) NOT NULL DEFAULT 0.00, " +
		"CONSTRAINT fk_orders_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE ON UPDATE RESTRICT)",
	"CREATE TABLE order_items (order_id BIGINT UNSIGNED NOT NULL, product_id INT NOT NULL, quantity INT NOT NULL DEFAULT 1, " +
		"PRIMARY KEY (order_id, product_id), KEY ix_product (product_id), " +
		"CONSTRAINT fk_oi_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE, " +
		"CONSTRAINT fk_oi_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE RESTRICT ON UPDATE CASCADE)",
	"CREATE VIEW order_totals AS SELECT user_id, SUM(total) AS total FROM orders GROUP BY user_id",
}

func tableOf(snap *Snapshot, name string) *SnapshotTable {
	for i := range snap.Tables {
		if snap.Tables[i].Name == name {
			return &snap.Tables[i]
		}
	}
	return nil
}

func TestSnapshotReadsTheWholeSchema(t *testing.T) {
	ex, _ := scratchSchema(t, snapshotDDL...)
	snap, err := ex.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tb := range snap.Tables {
		names = append(names, tb.Name)
	}
	if want := []string{"order_items", "orders", "products", "users"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tables = %v, want %v (a view is not a table)", names, want)
	}
	if snap.Goose {
		t.Error("no goose table here")
	}

	users := tableOf(snap, "users")
	if users.Engine != "InnoDB" || users.Comment != "accounts" {
		t.Errorf("users: engine %q comment %q", users.Engine, users.Comment)
	}
	cols := map[string]SnapshotColumn{}
	var order []string
	for _, c := range users.Columns {
		cols[c.Name] = c
		order = append(order, c.Name)
	}
	if want := []string{"id", "email", "role", "created_at"}; !reflect.DeepEqual(order, want) {
		t.Errorf("column order = %v, want %v", order, want)
	}
	if c := cols["email"]; c.Comment != "login name" || c.Nullable || c.Key != "UNI" || c.Type != "varchar(255)" {
		t.Errorf("email = %+v", c)
	}
	if c := cols["id"]; c.Key != "PRI" || c.Extra != "auto_increment" {
		t.Errorf("id = %+v", c)
	}
	if c := cols["role"]; c.Default == nil || *c.Default != "'member'" {
		t.Errorf("role default = %v, want 'member' as MariaDB spells it", c.Default)
	}
	if c := tableOf(snap, "products").Columns[2]; !c.Nullable {
		t.Errorf("products.name should be nullable: %+v", c)
	}

	// Indexes: the primary key is an index named PRIMARY; column order is kept.
	oi := tableOf(snap, "order_items")
	idx := map[string]Index{}
	for _, ix := range oi.Indexes {
		idx[ix.Name] = ix
	}
	if ix := idx["PRIMARY"]; !ix.Unique || !reflect.DeepEqual(ix.Columns, []string{"order_id", "product_id"}) {
		t.Errorf("order_items PRIMARY = %+v", ix)
	}
	if ix := idx["ix_product"]; ix.Unique || !reflect.DeepEqual(ix.Columns, []string{"product_id"}) {
		t.Errorf("order_items ix_product = %+v", ix)
	}
	if ix := idx["fk_oi_order"]; len(ix.Columns) != 0 {
		// The PK already covers order_id, so InnoDB creates no separate index for it.
		t.Errorf("unexpected implicit index: %+v", ix)
	}

	// Foreign keys carry their referential actions, which DescribeTable lacks.
	byName := map[string]SnapshotFK{}
	for _, fk := range oi.ForeignKeys {
		byName[fk.Name] = fk
	}
	if fk := byName["fk_oi_order"]; fk.RefTable != "orders" || fk.OnDelete != "CASCADE" || fk.OnUpdate != "RESTRICT" ||
		!reflect.DeepEqual(fk.Columns, []string{"order_id"}) || !reflect.DeepEqual(fk.RefColumns, []string{"id"}) {
		t.Errorf("fk_oi_order = %+v", fk)
	}
	if fk := byName["fk_oi_product"]; fk.OnDelete != "RESTRICT" || fk.OnUpdate != "CASCADE" {
		t.Errorf("fk_oi_product = %+v", fk)
	}
	// InnoDB made an index for orders.user_id, named after the constraint.
	var found bool
	for _, ix := range tableOf(snap, "orders").Indexes {
		found = found || ix.Name == "fk_orders_user"
	}
	if !found {
		t.Errorf("orders should have the index InnoDB created for its foreign key: %+v", tableOf(snap, "orders").Indexes)
	}
}

// A snapshot is metadata only and stable: rows and counters never appear in it,
// so inserting data does not change it.
func TestSnapshotHoldsNoRowDataAndIsStable(t *testing.T) {
	ex, db := scratchSchema(t, snapshotDDL...)
	before, err := ex.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"INSERT INTO users (email) VALUES ('ada.lovelace@example.com'), ('grace@example.com')",
		"INSERT INTO products (sku, name) VALUES ('SKU-1', 'a very secret product name')",
		"INSERT INTO orders (user_id, total) VALUES (1, 99.50)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	after, err := ex.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("inserting rows changed the snapshot:\n before %+v\n after  %+v", before, after)
	}
	for _, tb := range after.Tables {
		for _, c := range tb.Columns {
			if c.Default != nil && (*c.Default == "ada.lovelace@example.com" || *c.Default == "a very secret product name") {
				t.Errorf("row data in the snapshot: %+v", c)
			}
		}
	}
}

// goose's bookkeeping table is read for which migrations are applied and is not
// itself a table of the schema.
func TestSnapshotReadsGooseVersions(t *testing.T) {
	ex, db := scratchSchema(t, snapshotDDL...)
	for _, stmt := range []string{
		"CREATE TABLE goose_db_version (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, version_id BIGINT NOT NULL, is_applied TINYINT(1) NOT NULL, tstamp TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP)",
		"INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (1, 1), (2, 1), (3, 1), (3, 0), (4, 1)",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := ex.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Goose || !reflect.DeepEqual(snap.Applied, []int64{1, 2, 4}) {
		t.Errorf("goose = %v, applied = %v; want true, [1 2 4] (3 was rolled back, 0 is the baseline)", snap.Goose, snap.Applied)
	}
	if tableOf(snap, "goose_db_version") != nil {
		t.Error("the bookkeeping table is not part of the schema")
	}
}

func TestSnapshotOfAnEmptySchema(t *testing.T) {
	ex, _ := scratchSchema(t)
	snap, err := ex.Snapshot(context.Background())
	if err != nil || len(snap.Tables) != 0 || snap.Goose {
		t.Errorf("empty schema: %+v, %v", snap, err)
	}
}
