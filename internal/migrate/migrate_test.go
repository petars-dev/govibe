package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"
)

// What an app's `//go:embed sql/*.sql` embed.FS looks like from the outside.
var appFS = fstest.MapFS{
	"sql/0001_init.sql": {Data: []byte("-- +goose Up\nCREATE TABLE t (id INT);\n-- +goose Down\nDROP TABLE t;\n")},
	"sql/0002_more.sql": {Data: []byte("-- +goose Up\nALTER TABLE t ADD c INT;\n-- +goose Down\nALTER TABLE t DROP c;\n")},
}

// sql.Open does not dial: the provider only needs the handle to exist.
func unopened(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", "nobody:nothing@tcp(127.0.0.1:1)/none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestListFromHandedOverFS(t *testing.T) {
	got, err := list(unopened(t), goose.DialectMySQL, appFS)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "1:0001_init.sql" || got[1] != "2:0002_more.sql" {
		t.Fatalf("got %v", got)
	}
}

func TestDialectCannotBeOmitted(t *testing.T) {
	if _, err := list(unopened(t), "", appFS); err == nil {
		t.Fatal("an empty dialect must be an error (the global goose API silently picks postgres)")
	}
}

func TestMissingSqlDirIsAnError(t *testing.T) {
	if got, err := list(unopened(t), goose.DialectMySQL, fstest.MapFS{"other/x.sql": {}}); err == nil {
		t.Fatalf("want an error for an FS without sql/, got %v", got)
	}
}

func TestParseDialect(t *testing.T) {
	for in, want := range map[string]goose.Dialect{
		"mysql":    goose.DialectMySQL,
		" MySQL ":  goose.DialectMySQL,
		"postgres": goose.DialectPostgres,
		"sqlite3":  goose.DialectSQLite3,
	} {
		if got, err := ParseDialect(in); err != nil || got != want {
			t.Errorf("ParseDialect(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "  ", "mariadb", "oracle"} {
		if got, err := ParseDialect(in); err == nil {
			t.Errorf("ParseDialect(%q) = %q, want an error", in, got)
		}
	}
}

// A real server, when one is given: the whole cycle through the Provider API. The
// test creates a schema of its own and drops it, as the indexer's database tests do:
//
//	GOVIBE_TEST_MARIADB_DSN='root@tcp(127.0.0.1:3306)/' go test ./internal/migrate
func TestUpStatusDownAgainstAServer(t *testing.T) {
	dsn := os.Getenv("GOVIBE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("set GOVIBE_TEST_MARIADB_DSN to run against a real MariaDB/MySQL server")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("govibe_migrate_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP DATABASE `" + name + "`") })
	cfg.DBName = name
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if n, err := Up(ctx, db, goose.DialectMySQL, appFS); err != nil || n != 2 {
		t.Fatalf("Up = %d, %v", n, err)
	}
	if n, err := Up(ctx, db, goose.DialectMySQL, appFS); err != nil || n != 0 {
		t.Fatalf("a second Up must be a no-op: %d, %v", n, err)
	}
	var out bytes.Buffer
	if err := Status(ctx, &out, db, goose.DialectMySQL, appFS); err != nil || strings.Contains(out.String(), "pending") {
		t.Fatalf("Status after Up = %q, %v", out.String(), err)
	}
	if err := Down(ctx, db, goose.DialectMySQL, appFS); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Status(ctx, &out, db, goose.DialectMySQL, appFS); err != nil || !strings.Contains(out.String(), "pending") {
		t.Fatalf("Status after Down = %q, %v", out.String(), err)
	}
}
