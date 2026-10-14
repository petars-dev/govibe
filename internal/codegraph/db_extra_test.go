package codegraph

import (
	"sort"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"
)

// extraMigration is a second migration that the fixture does not carry: it is
// written into a temporary copy by the tests that need it, so the cases below
// (changes to existing tables, a shared primary key, an implicit reference next
// to a column that names no table, renames and drops that history must follow)
// cost the tree nothing.
const extraMigration = `-- +goose Up
ALTER TABLE orders ADD COLUMN note VARCHAR(20) NULL AFTER status, ADD INDEX ix_orders_status (status);
ALTER TABLE users MODIFY COLUMN email VARCHAR(320) NOT NULL COMMENT 'login name';

-- user_id looks like a reference to users, but nothing enforces it; actor_id names no table.
CREATE TABLE audit_entries (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT UNSIGNED NOT NULL,
  actor_id INT NULL,
  msg VARCHAR(40)
) ENGINE=InnoDB;

-- A shared primary key: one profile per user.
CREATE TABLE profiles (
  user_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  bio TEXT,
  CONSTRAINT fk_profile_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE=InnoDB;

CREATE TABLE scratch (id INT);

ALTER TABLE audit_entries RENAME COLUMN msg TO message;
ALTER TABLE products ADD COLUMN stock INT NOT NULL DEFAULT 0, DROP COLUMN name;
CREATE INDEX ix_audit_user ON audit_entries (user_id);
DROP TABLE scratch;

-- +goose Down
DROP TABLE profiles;
DROP TABLE audit_entries;
ALTER TABLE products ADD COLUMN name VARCHAR(100) NULL, DROP COLUMN stock;
ALTER TABLE users MODIFY COLUMN email VARCHAR(255) NOT NULL COMMENT 'login name';
ALTER TABLE orders DROP INDEX ix_orders_status, DROP COLUMN note;
`

// richRoot is a copy of the fixture with extraMigration added as the second
// migration.
func richRoot(t *testing.T) string {
	t.Helper()
	root := copyTree(t, fixtureRoot)
	writeFile(t, root, "storage/migrations/sql/0002_extras.sql", extraMigration)
	return root
}

// richWorld is a world over richRoot, with the fixture's runtime routes.
func richWorld(t *testing.T, opts Options) *world {
	t.Helper()
	opts.RouteSource = fixtureOptions().RouteSource
	return newTestWorld(t, richRoot(t), opts)
}

func TestDBLayerExtraMigration(t *testing.T) {
	out := buildLayer(t, richWorld(t, Options{}), dbLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	for _, id := range []string{"table:audit_entries", "table:profiles"} {
		if !f.exists(id) {
			t.Errorf("missing %s", id)
		}
	}
	if f.exists("table:scratch") {
		t.Error("a table the migration dropped again must be gone")
	}

	// A foreign key that is itself unique points at one row, and one row points back.
	if got := f.one(t, "col:profiles.user_id", "cardinality"); got != str("one_to_one") {
		t.Errorf("profiles.user_id cardinality = %v", got)
	}
	if !f.has("col:profiles.user_id", "fk_to", iri("col:users.id")) {
		t.Error("profiles.user_id should reference users.id")
	}

	// audit_entries.user_id is implicit (and indexed by a later CREATE INDEX);
	// actor_id names a table that does not exist, so it is not.
	a := string(annID("col:audit_entries.user_id", "implicit_ref", "col:users.id"))
	if !f.has("col:audit_entries.user_id", "implicit_ref", iri("col:users.id")) {
		t.Fatal("audit_entries.user_id should be an implicit reference to users.id")
	}
	if got := f.one(t, a, "confidence"); got != quad.Value(quad.Int(95)) {
		t.Errorf("confidence = %v", got)
	}
	if got := f.out("col:audit_entries.actor_id", "implicit_ref"); len(got) != 0 {
		t.Errorf("actor_id names no table, so it refers to nothing: %v", got)
	}

	// ALTER TABLE changes what is there, and keeps where it came from.
	email := "col:users.email"
	if got := f.one(t, email, "data_type"); got != str("varchar(320)") {
		t.Errorf("email type = %v (MODIFY changed it)", got)
	}
	if got := f.one(t, email, "comment"); got != str("login name") {
		t.Errorf("email comment = %v", got)
	}
	if got := f.one(t, email, "nullable"); got != quad.Value(quad.Bool(false)) {
		t.Errorf("email nullable = %v", got)
	}
	if got := f.one(t, email, "key"); got != str("UNI") {
		t.Errorf("email key = %v", got)
	}
	// A column added with AFTER takes that position.
	if got := f.one(t, "col:orders.note", "ordinal"); got != quad.Value(quad.Int(5)) {
		t.Errorf("note ordinal = %v, want 5 (after status)", got)
	}
	if !f.has("table:orders", "has_index", iri("idx:orders.ix_orders_status")) {
		t.Error("an index added by ALTER should belong to its table")
	}

	// History follows renames and drops: a column renamed in the migration that
	// created its table is introduced by it, and a dropped column is gone.
	for subj, mig := range map[string]string{
		"col:audit_entries.message":       "mig:0002_extras",
		"col:products.stock":              "mig:0002_extras",
		"idx:audit_entries.ix_audit_user": "mig:0002_extras",
		"table:products":                  "mig:0001_init",
		"col:users.email":                 "mig:0001_init", // MODIFY'd later: where it came from does not change
		"col:orders.note":                 "mig:0002_extras",
		"idx:orders.ix_orders_status":     "mig:0002_extras",
	} {
		if got := f.one(t, subj, "introduced_in"); got != iri(mig) {
			t.Errorf("%s introduced_in %v, want %s", subj, got, mig)
		}
	}
	if f.exists("col:audit_entries.msg") || f.exists("col:products.name") {
		t.Error("a renamed or dropped column must be gone")
	}
}

// The SQL a function uses, in every shape the db layer reads it from: package
// constants used by name, concatenated literals, raw strings, joins, a statement
// that writes one table and reads another, and the dynamic and system-schema
// statements that name no table of the app. The project is written by the test,
// so the SQL sits next to what it must produce.
func TestSQLUsageInference(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/proj\n\ngo 1.25\n")
	writeFile(t, root, "storage/migrations/sql/0001_init.sql", `-- +goose Up
CREATE TABLE accounts (id BIGINT PRIMARY KEY, email VARCHAR(100) NOT NULL);
CREATE TABLE items (id BIGINT PRIMARY KEY, name VARCHAR(50));
CREATE TABLE lines (account_id BIGINT NOT NULL, item_id BIGINT NOT NULL, qty INT NOT NULL, PRIMARY KEY (account_id, item_id));
`)
	writeFile(t, root, "storage/store/store.go", "package store\n"+`
import (
	"context"
	"database/sql"
)

// Account is one row of accounts.
type Account struct {
	ID    int64  `+"`db:\"id\"`"+`
	Email string `+"`db:\"email\"`"+`
}

// Line is one row of lines.
type Line struct {
	AccountID int64 `+"`db:\"account_id\"`"+`
	ItemID    int64 `+"`db:\"item_id\"`"+`
	Qty       int64 `+"`db:\"qty\"`"+`
}

// Ref has a tag every table's id column shares, so it maps to no table.
type Ref struct {
	ID int64 `+"`db:\"id\"`"+`
}

const (
	byEmailSQL = "SELECT a.id, a.email FROM accounts a JOIN lines l ON l.account_id = a.id WHERE a.email = ?"
	addLineSQL = "INSERT INTO lines (account_id, item_id, qty) " +
		"SELECT ?, i.id, 1 FROM items i WHERE i.name = ?"
)

type Store struct{ db *sql.DB }

// ByEmail goes through a package constant and a join.
func (s *Store) ByEmail(ctx context.Context, email string) error {
	_, err := s.db.QueryContext(ctx, byEmailSQL, email)
	return err
}

// AddLine writes lines and reads items in one statement, then updates accounts
// with a concatenated literal.
func (s *Store) AddLine(ctx context.Context, id int64, name string) error {
	if _, err := s.db.ExecContext(ctx, addLineSQL, id, name); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "UPDATE accounts SET email = email "+"WHERE id = ?", id)
	return err
}

// Purge uses a raw string.
func (s *Store) Purge(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `+"`DELETE FROM lines\n\t\tWHERE account_id = ?`"+`, id)
	return err
}

// Dump builds its SQL at run time.
func (s *Store) Dump(ctx context.Context, table string) error {
	_, err := s.db.QueryContext(ctx, "SELECT * FROM "+table)
	return err
}

// Schema reads the server's own tables.
func (s *Store) Schema(ctx context.Context) error {
	_, err := s.db.QueryContext(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES")
	return err
}
`)
	out := buildLayer(t, newTestWorld(t, root, Options{}), dbLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	names := func(fn, pred string) string {
		var got []string
		for _, v := range f.out(fn, pred) {
			got = append(got, strings.TrimPrefix(string(v.(quad.IRI)), "table:"))
		}
		sort.Strings(got)
		return strings.Join(got, ",")
	}
	for _, c := range []struct{ fn, reads, writes string }{
		{"func:storage/store.Store.ByEmail", "accounts,lines", ""},
		{"func:storage/store.Store.AddLine", "items", "accounts,lines"},
		{"func:storage/store.Store.Purge", "", "lines"},
		{"func:storage/store.Store.Dump", "", ""},
		{"func:storage/store.Store.Schema", "", ""},
	} {
		if got := names(c.fn, "reads_table"); got != c.reads {
			t.Errorf("%s reads %q, want %q", c.fn, got, c.reads)
		}
		if got := names(c.fn, "writes_table"); got != c.writes {
			t.Errorf("%s writes %q, want %q", c.fn, got, c.writes)
		}
	}

	for ty, table := range map[string]string{"Account": "accounts", "Line": "lines"} {
		if got := f.one(t, "typ:storage/store."+ty, "maps_to"); got != iri("table:"+table) {
			t.Errorf("%s maps to %v, want %s", ty, got, table)
		}
	}
	// db:"id" fits accounts and items, and the name Ref points at neither.
	if got := f.out("typ:storage/store.Ref", "maps_to"); len(got) != 0 {
		t.Errorf("an ambiguous struct maps to nothing: %v", got)
	}
}
