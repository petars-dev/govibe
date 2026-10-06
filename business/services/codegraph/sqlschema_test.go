package codegraph

import (
	"reflect"
	"strings"
	"testing"
)

// parseMigrations applies migrations (stem -> goose SQL file) in order.
func parseMigrations(t *testing.T, migs ...[2]string) *sqlSchema {
	t.Helper()
	sch := newSQLSchema()
	for _, m := range migs {
		up, _ := gooseUp(m[1])
		for _, st := range splitStmts(up, lexSQL(up)) {
			sch.apply(st, m[0])
		}
	}
	return sch
}

func parseOne(t *testing.T, sql string) *sqlSchema {
	t.Helper()
	return parseMigrations(t, [2]string{"0001_t", sql})
}

func tbl(t *testing.T, s *sqlSchema, name string) *sqlTable {
	t.Helper()
	tb := s.tables[strings.ToLower(name)]
	if tb == nil {
		t.Fatalf("no table %s; have %v", name, keysOf(s.tables))
	}
	return tb
}

func keysOf(m map[string]*sqlTable) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	return k
}

func colNames(tb *sqlTable) []string {
	var out []string
	for _, c := range tb.columns {
		out = append(out, c.name)
	}
	return out
}

func idxSummary(tb *sqlTable) []string {
	var out []string
	for _, ix := range tb.indexes {
		kind := ""
		if ix.primary {
			kind = "P"
		} else if ix.unique {
			kind = "U"
		}
		if ix.implicit {
			kind += "i"
		}
		out = append(out, ix.name+"["+kind+"]("+strings.Join(ix.columns, ",")+")")
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %#v\n want %#v", what, got, want)
	}
}

// What MariaDB 12.3 does with the same statements, verified against a real
// server (see the db layer's live test): index names, the constraint name of a
// foreign key, and the index the server adds for one.
func TestIndexAndForeignKeyNaming(t *testing.T) {
	s := parseOne(t, `
-- +goose Up
CREATE TABLE parent (id INT PRIMARY KEY, code VARCHAR(10));
CREATE TABLE kid1 (id INT PRIMARY KEY, parent_id INT, FOREIGN KEY idx_p (parent_id) REFERENCES parent(id));
CREATE TABLE kid2 (id INT PRIMARY KEY, parent_id INT, FOREIGN KEY (parent_id) REFERENCES parent(id));
CREATE TABLE kid3 (id INT PRIMARY KEY, parent_id INT REFERENCES parent(id));
CREATE TABLE kid4 (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_k4 FOREIGN KEY (parent_id) REFERENCES parent(id), KEY ix_later (parent_id));
CREATE TABLE kid5 (id INT PRIMARY KEY, a INT, b INT, UNIQUE (a), UNIQUE (a, b), UNIQUE (a), KEY (b), KEY (b));
CREATE TABLE kid6 (id INT PRIMARY KEY, parent_id INT, CONSTRAINT fk_k6 FOREIGN KEY idx_six (parent_id) REFERENCES parent(id));
`)
	// A FOREIGN KEY index name names the constraint and its index.
	eq(t, "kid1 indexes", idxSummary(tbl(t, s, "kid1")), []string{"PRIMARY[P](id)", "idx_p[i](parent_id)"})
	eq(t, "kid1 fk name", tbl(t, s, "kid1").fks[0].name, "idx_p")
	// An unnamed foreign key's index is named after its column; its constraint name is
	// not reported (the servers disagree), though the parser tracks it.
	eq(t, "kid2 indexes", idxSummary(tbl(t, s, "kid2")), []string{"PRIMARY[P](id)", "parent_id[i](parent_id)"})
	if !tbl(t, s, "kid2").fks[0].autoNamed {
		t.Error("an unnamed foreign key should be marked auto-named")
	}
	// An inline REFERENCES creates the key (MariaDB accepts it, unlike MySQL).
	eq(t, "kid3 indexes", idxSummary(tbl(t, s, "kid3")), []string{"PRIMARY[P](id)", "parent_id[i](parent_id)"})
	if len(tbl(t, s, "kid3").fks) != 1 {
		t.Error("an inline REFERENCES should make a foreign key")
	}
	// An index declared after the foreign key still covers it: the server adds none.
	eq(t, "kid4 indexes", idxSummary(tbl(t, s, "kid4")), []string{"PRIMARY[P](id)", "ix_later[](parent_id)"})
	// Unnamed keys are named after their first column, then _2, _3.
	eq(t, "kid5 indexes", idxSummary(tbl(t, s, "kid5")), []string{"PRIMARY[P](id)", "a[U](a)", "a_2[U](a,b)", "a_3[U](a)", "b[](b)", "b_2[](b)"})
	// The constraint symbol names the implicit index, ahead of the FK index name.
	eq(t, "kid6 indexes", idxSummary(tbl(t, s, "kid6")), []string{"PRIMARY[P](id)", "fk_k6[i](parent_id)"})
	eq(t, "kid6 fk name", tbl(t, s, "kid6").fks[0].name, "fk_k6")
}

func TestColumnTypesAndAttributes(t *testing.T) {
	s := parseOne(t, "-- +goose Up\n"+
		"CREATE TABLE t7 (id INT PRIMARY KEY, ts TIMESTAMP, ts2 TIMESTAMP NOT NULL, flag BOOL, big INTEGER UNSIGNED, price NUMERIC(8,2), dd DECIMAL, "+
		"s VARCHAR(5) DEFAULT 'it''s', n INT DEFAULT NULL, c CHAR(3) NOT NULL DEFAULT 'ab', "+
		"u TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, e ENUM('a','b c') NOT NULL DEFAULT 'a' COMMENT 'one; two', "+
		"d2 DOUBLE PRECISION, g INT GENERATED ALWAYS AS (id * 2) VIRTUAL, sr SERIAL) ENGINE=InnoDB COMMENT='a table; with semicolon' AUTO_INCREMENT=5;")
	tb := tbl(t, s, "t7")
	want := map[string]struct {
		typ      string
		nullable bool
		def      string
		hasDef   bool
		extra    string
	}{
		"id":    {"int", false, "", false, ""},
		"ts":    {"timestamp", true, "", false, ""},
		"ts2":   {"timestamp", false, "", false, ""},
		"flag":  {"tinyint(1)", true, "", false, ""},
		"big":   {"int unsigned", true, "", false, ""},
		"price": {"decimal(8,2)", true, "", false, ""},
		"dd":    {"decimal", true, "", false, ""},
		"s":     {"varchar(5)", true, "'it''s'", true, ""},
		"n":     {"int", true, "", false, ""},
		"c":     {"char(3)", false, "'ab'", true, ""},
		"u":     {"timestamp", true, "current_timestamp()", true, "on update current_timestamp()"},
		"e":     {"enum('a','b c')", false, "'a'", true, ""},
		"d2":    {"double", true, "", false, ""},
		"g":     {"int", true, "", false, "generated"},
		"sr":    {"bigint unsigned", false, "", false, "auto_increment"},
	}
	for _, c := range tb.columns {
		w, ok := want[c.name]
		if !ok {
			t.Errorf("unexpected column %s", c.name)
			continue
		}
		if c.typ != w.typ || c.nullable != w.nullable || c.def != w.def || c.hasDefault != w.hasDef || c.extra != w.extra {
			t.Errorf("%s = {%q null=%v def=%q has=%v extra=%q}, want %+v", c.name, c.typ, c.nullable, c.def, c.hasDefault, c.extra, w)
		}
	}
	if len(tb.columns) != len(want) {
		t.Errorf("%d columns, want %d: %v", len(tb.columns), len(want), colNames(tb))
	}
	if tb.column("e").comment != "one; two" || tb.comment != "a table; with semicolon" || tb.engine != "InnoDB" {
		t.Errorf("comments/engine: %q %q %q", tb.column("e").comment, tb.comment, tb.engine)
	}
	// SERIAL is also a unique key, and the primary key column is NOT NULL.
	if ix := tb.index("sr"); ix == nil || !ix.unique {
		t.Errorf("SERIAL should add a unique key: %v", idxSummary(tb))
	}
}

// Strings, quoted identifiers and comments cannot split or hide a statement.
func TestLexingTraps(t *testing.T) {
	s := parseOne(t, "-- +goose Up\n"+
		"-- a comment; with a semicolon\n"+
		"CREATE TABLE `odd ``name` (`id` INT PRIMARY KEY, `sel;ect` VARCHAR(10) DEFAULT ';'); # trailing; comment\n"+
		"/* block; comment */ CREATE TABLE b (id INT); -- another\n"+
		"/*!40101 SET @x = 1 */;\n"+
		"INSERT INTO b VALUES (1); INSERT INTO b VALUES (2);\n"+
		"CREATE TABLE \"dq\" (id INT);\n")
	eq(t, "tables", len(s.tables), 2) // `odd name` and b; "dq" is a string in MySQL's default mode, not an identifier
	odd := tbl(t, s, "odd `name")
	eq(t, "odd columns", colNames(odd), []string{"id", "sel;ect"})
	if len(s.warnings) != 1 {
		t.Errorf("only the \"dq\" table should warn: %v", s.warnings)
	}
}

func TestGooseUpSection(t *testing.T) {
	up, skipped := gooseUp(`-- +goose Up
CREATE TABLE a (id INT);
-- +goose StatementBegin
CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END;
-- +goose StatementEnd
CREATE TABLE b (id INT);
-- +goose Down
DROP TABLE b;
DROP TABLE a;
`)
	if skipped != 1 || strings.Contains(up, "DROP") || strings.Contains(up, "PROCEDURE") || !strings.Contains(up, "TABLE b") {
		t.Errorf("up = %q skipped %d", up, skipped)
	}
	// No directives at all: the whole file is Up.
	if up, _ := gooseUp("CREATE TABLE a (id INT);"); !strings.Contains(up, "CREATE TABLE a") {
		t.Errorf("a file without directives is all Up: %q", up)
	}
	// Directives are case-insensitive, and a file with only Down has no Up.
	if up, _ := gooseUp("-- +GOOSE DOWN\nDROP TABLE a;"); strings.TrimSpace(up) != "" {
		t.Errorf("down only: %q", up)
	}
}

func TestAlterTable(t *testing.T) {
	s := parseMigrations(t,
		[2]string{"0001_base", `-- +goose Up
CREATE TABLE users (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, email VARCHAR(100) NOT NULL, nick VARCHAR(20));
CREATE TABLE orders (id INT PRIMARY KEY, user_id BIGINT UNSIGNED NOT NULL, status VARCHAR(10), KEY ix_user (user_id), KEY ix_status (status));
CREATE TABLE tmp_x (id INT);
`},
		[2]string{"0002_changes", `-- +goose Up
ALTER TABLE users ADD COLUMN age INT NOT NULL DEFAULT 0 AFTER email, ADD COLUMN first_col INT FIRST, ADD UNIQUE KEY ux_email (email);
ALTER TABLE users MODIFY COLUMN email VARCHAR(320) NOT NULL COMMENT 'login';
ALTER TABLE users CHANGE nick nickname VARCHAR(40);
ALTER TABLE orders ADD CONSTRAINT fk_o_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE orders DROP INDEX ix_status, DROP COLUMN status;
ALTER TABLE orders RENAME COLUMN user_id TO customer_id;
DROP TABLE IF EXISTS tmp_x, nope;
CREATE INDEX ix_o_cust ON orders (customer_id, id);
`},
		[2]string{"0003_more", `-- +goose Up
RENAME TABLE users TO accounts;
ALTER TABLE orders ENGINE=InnoDB, ALGORITHM=INPLACE, LOCK=NONE;
DROP INDEX ix_o_cust ON orders;
`})

	acc := tbl(t, s, "accounts")
	eq(t, "accounts columns", colNames(acc), []string{"first_col", "id", "email", "age", "nickname"})
	if c := acc.column("email"); c.typ != "varchar(320)" || c.comment != "login" || c.introduced != "0001_base" {
		t.Errorf("a MODIFY keeps where the column came from: %+v", c)
	}
	if c := acc.column("age"); c.introduced != "0002_changes" {
		t.Errorf("age introduced in %s", c.introduced)
	}
	if c := acc.column("nickname"); c.typ != "varchar(40)" || c.introduced != "0001_base" {
		t.Errorf("CHANGE renames and keeps history: %+v", c)
	}
	if ix := acc.index("ux_email"); ix == nil || ix.introduced != "0002_changes" || !ix.unique {
		t.Errorf("ux_email = %+v", ix)
	}
	if acc.introduced != "0001_base" {
		t.Errorf("a renamed table keeps its history: %s", acc.introduced)
	}
	if _, old := s.tables["users"]; old {
		t.Error("the old table name must be gone")
	}

	ord := tbl(t, s, "orders")
	eq(t, "orders columns", colNames(ord), []string{"id", "customer_id"})
	// The foreign key follows the rename of the referenced table, and the column rename.
	if len(ord.fks) != 1 || ord.fks[0].refTable != "accounts" || !reflect.DeepEqual(ord.fks[0].columns, []string{"customer_id"}) ||
		ord.fks[0].onDelete != "CASCADE" || ord.fks[0].name != "fk_o_user" {
		t.Errorf("fk = %+v", ord.fks)
	}
	// Dropping a column takes it out of the indexes, and an index it empties goes.
	eq(t, "orders indexes", idxSummary(ord), []string{"PRIMARY[P](id)", "ix_user[](customer_id)"})
	if _, gone := s.tables["tmp_x"]; gone {
		t.Error("tmp_x was dropped")
	}
	if len(s.warnings) != 0 {
		t.Errorf("warnings: %v", s.warnings)
	}
}

func TestPrimaryKeyAndFKManagement(t *testing.T) {
	s := parseOne(t, `-- +goose Up
CREATE TABLE p (id INT PRIMARY KEY);
CREATE TABLE c (a INT, b INT, v INT, CONSTRAINT fk1 FOREIGN KEY (a) REFERENCES p (id), CONSTRAINT fk2 FOREIGN KEY (b) REFERENCES p (id));
ALTER TABLE c ADD PRIMARY KEY (a, b);
ALTER TABLE c DROP FOREIGN KEY fk1;
CREATE TABLE copy LIKE c;
CREATE TABLE IF NOT EXISTS c (zzz INT);
`)
	c := tbl(t, s, "c")
	eq(t, "pk", c.primary().columns, []string{"a", "b"})
	if c.column("a").nullable || c.column("b").nullable || !c.column("v").nullable {
		t.Error("primary key columns become NOT NULL, and only those")
	}
	if len(c.fks) != 1 || c.fks[0].name != "fk2" {
		t.Errorf("fks = %+v", c.fks)
	}
	if c.column("zzz") != nil {
		t.Error("CREATE TABLE IF NOT EXISTS on an existing table does nothing")
	}
	if cp := tbl(t, s, "copy"); !reflect.DeepEqual(colNames(cp), []string{"a", "b", "v"}) || cp.primary() == nil {
		t.Errorf("CREATE TABLE LIKE copies the shape: %v", colNames(cp))
	}
}

// A table dropped and created again is new: it was introduced by the later migration.
func TestHistoryAfterDropAndRecreate(t *testing.T) {
	s := parseMigrations(t,
		[2]string{"0001_a", "-- +goose Up\nCREATE TABLE t (id INT, old_col INT);"},
		[2]string{"0002_b", "-- +goose Up\nDROP TABLE t;"},
		[2]string{"0003_c", "-- +goose Up\nCREATE TABLE t (id INT);"})
	tb := tbl(t, s, "t")
	if tb.introduced != "0003_c" || tb.column("id").introduced != "0003_c" || tb.column("old_col") != nil {
		t.Errorf("t = %+v", tb)
	}
}

// DDL that is recognised but cannot be read is reported, not guessed at; SQL that
// is not table DDL is ignored without comment.
func TestWarningsAndIgnoredStatements(t *testing.T) {
	s := parseOne(t, `-- +goose Up
CREATE TABLE ok (id INT);
CREATE TABLE ctas AS SELECT 1;
ALTER TABLE ok PARTITION BY HASH(id) PARTITIONS 4;
ALTER TABLE ghost ADD COLUMN x INT;
CREATE INDEX ix ON ghost (x);
CREATE VIEW v AS SELECT * FROM ok;
CREATE TRIGGER tr BEFORE INSERT ON ok FOR EACH ROW SET NEW.id = 1;
INSERT INTO ok VALUES (1);
UPDATE ok SET id = 2;
DELETE FROM ok;
SET FOREIGN_KEY_CHECKS = 0;
CREATE DATABASE other;
`)
	if len(s.tables) != 1 {
		t.Errorf("tables: %v", keysOf(s.tables))
	}
	var kinds []string
	for _, w := range s.warnings {
		switch {
		case strings.Contains(w, "AS SELECT") || strings.Contains(w, "no column list"):
			kinds = append(kinds, "ctas")
		case strings.Contains(w, "PARTITION"):
			kinds = append(kinds, "partition")
		case strings.Contains(w, "unknown table ghost"):
			kinds = append(kinds, "ghost")
		}
		if !strings.HasPrefix(w, "0001_t: ") {
			t.Errorf("a warning names its migration: %q", w)
		}
	}
	eq(t, "warnings", kinds, []string{"ctas", "partition", "ghost", "ghost"})
}

func TestScanSQLTables(t *testing.T) {
	for _, c := range []struct {
		sql           string
		reads, writes []string
	}{
		{"SELECT id FROM orders WHERE id = ?", []string{"orders"}, nil},
		{"select * from `orders` o join users u on u.id = o.user_id left join order_items i on i.order_id = o.id", []string{"orders", "users", "order_items"}, nil},
		{"INSERT INTO orders (user_id, total) VALUES (?, ?)", nil, []string{"orders"}},
		{"INSERT IGNORE INTO orders SELECT * FROM staging_orders", []string{"staging_orders"}, []string{"orders"}},
		{"REPLACE INTO products (id) VALUES (1)", nil, []string{"products"}},
		{"UPDATE orders SET total = 0 WHERE id = 1", nil, []string{"orders"}},
		{"UPDATE orders o JOIN users u ON u.id = o.user_id SET o.total = 0", []string{"users"}, []string{"orders"}},
		{"UPDATE a, b SET a.x = b.y", nil, []string{"a", "b"}},
		{"DELETE FROM orders WHERE id = 1", nil, []string{"orders"}},
		{"DELETE o FROM orders o JOIN users u ON u.id = o.user_id", []string{"users"}, []string{"orders"}},
		{"TRUNCATE TABLE audit_log", nil, []string{"audit_log"}},
		{"SELECT a.x FROM a, b WHERE a.id = b.id", []string{"a", "b"}, nil},
		{"SELECT x FROM (SELECT y FROM inner_t) s JOIN outer_t ON 1", []string{"inner_t", "outer_t"}, nil},
		{"SELECT x FROM main.orders", []string{"orders"}, nil},
		{"SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()", nil, nil},
		{"SELECT * FROM %s WHERE id = ?", nil, nil},
		{"SELECT 'FROM orders' AS s, \"JOIN users\" AS t", nil, nil},
		{"SELECT EXTRACT(YEAR FROM created_at) FROM orders", []string{"created_at", "orders"}, nil}, // the caller keeps only real tables
		{"-- FROM hidden\nSELECT 1 /* JOIN also_hidden */", nil, nil},
		{"WITH recent AS (SELECT * FROM orders) SELECT * FROM recent", []string{"orders", "recent"}, nil},
	} {
		reads, writes := scanSQLTables(c.sql)
		if !reflect.DeepEqual(reads, c.reads) || !reflect.DeepEqual(writes, c.writes) {
			t.Errorf("%s\n got  reads %v writes %v\n want reads %v writes %v", c.sql, reads, writes, c.reads, c.writes)
		}
	}
}

func TestNormType(t *testing.T) {
	for _, c := range [][2]string{
		{"int(11)", "int"}, {"INT", "int"}, {"integer", "int"}, {"int(10) unsigned", "int unsigned"}, {"bigint(20) unsigned", "bigint unsigned"},
		{"BIGINT UNSIGNED", "bigint unsigned"}, {"tinyint(1)", "tinyint(1)"}, {"tinyint(4)", "tinyint"}, {"bool", "tinyint(1)"},
		{"decimal", "decimal(10,0)"}, {"decimal(8)", "decimal(8,0)"}, {"decimal(10, 2)", "decimal(10,2)"}, {"numeric(8,2)", "decimal(8,2)"},
		{"varchar(255)", "varchar(255)"}, {"VARCHAR(255)", "varchar(255)"}, {"enum('a','b')", "enum('a','b')"},
	} {
		if got := normType(c[0]); got != c[1] {
			t.Errorf("normType(%q) = %q, want %q", c[0], got, c[1])
		}
	}
	// What a migration spells and what the server reports are the same type.
	for _, same := range [][2]string{{"int", "int(11)"}, {"bigint unsigned", "bigint(20) unsigned"}, {"decimal", "decimal(10,0)"}, {"integer unsigned", "int(10) unsigned"}} {
		if normType(same[0]) != normType(same[1]) {
			t.Errorf("%q and %q should be the same type", same[0], same[1])
		}
	}
	if normType("int") == normType("bigint") || normType("int") == normType("int unsigned") || normType("varchar(10)") == normType("varchar(20)") {
		t.Error("different types must stay different")
	}
}
