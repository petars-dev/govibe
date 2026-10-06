package codegraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cayleygraph/quad"

	"petars-dev/govibe/storage/dbexplorer"
)

// liveType spells a type the way MariaDB reports it: integers with their display
// width.
func liveType(t string) string {
	switch t {
	case "int":
		return "int(11)"
	case "int unsigned":
		return "int(10) unsigned"
	case "bigint":
		return "bigint(20)"
	case "bigint unsigned":
		return "bigint(20) unsigned"
	}
	return t
}

// snapshotOf renders a model as the snapshot a MariaDB holding exactly that
// schema would return: widths on integer types, the text NULL for a nullable
// column with no default, RESTRICT where no rule was written, and `1`, `2`, ...
// for foreign keys the migrations left unnamed.
func snapshotOf(m *dbModel) *dbexplorer.Snapshot {
	snap := &dbexplorer.Snapshot{Goose: true}
	for _, mg := range m.migrations {
		snap.Applied = append(snap.Applied, mg.num)
	}
	for _, t := range m.tables {
		st := dbexplorer.SnapshotTable{Name: t.name, Engine: t.engine, Comment: t.comment}
		for _, c := range t.columns {
			sc := dbexplorer.SnapshotColumn{Name: c.name, Type: liveType(c.typ), Nullable: c.nullable, Key: c.key, Extra: c.extra, Comment: c.comment}
			switch {
			case c.hasDef:
				d := c.def
				sc.Default = &d
			case c.nullable:
				d := "NULL"
				sc.Default = &d
			}
			st.Columns = append(st.Columns, sc)
		}
		for _, ix := range t.indexes {
			st.Indexes = append(st.Indexes, dbexplorer.Index{Name: ix.name, Unique: ix.unique, Columns: append([]string(nil), ix.columns...)})
		}
		auto := 0
		for _, f := range t.fks {
			name := f.name
			if name == "" {
				auto++
				name = fmt.Sprint(auto)
			}
			upd, del := f.onUpdate, f.onDelete
			if upd == "" {
				upd = "RESTRICT"
			}
			if del == "" {
				del = "RESTRICT"
			}
			st.ForeignKeys = append(st.ForeignKeys, dbexplorer.SnapshotFK{
				Name: name, Columns: append([]string(nil), f.columns...), RefTable: f.refTable,
				RefColumns: append([]string(nil), f.refColumns...), OnUpdate: upd, OnDelete: del,
			})
		}
		snap.Tables = append(snap.Tables, st)
	}
	return snap
}

func fixtureModel(t *testing.T) *dbModel {
	t.Helper()
	m, err := fixtureWorld(t).dbModel()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func worldWithSchema(t *testing.T, fn func(context.Context) (*dbexplorer.Snapshot, error)) *world {
	t.Helper()
	opts := fixtureOptions()
	opts.Schema = fn
	opts.liveTTL = -1 // never reuse a read: every call asks
	return newTestWorld(t, fixtureRoot, opts)
}

func staticSnapshot(t *testing.T) *dbexplorer.Snapshot { return snapshotOf(fixtureModel(t)) }

// richWorldWithSchema is worldWithSchema over the fixture plus extraMigration.
func richWorldWithSchema(t *testing.T, fn func(context.Context) (*dbexplorer.Snapshot, error)) *world {
	t.Helper()
	opts := Options{Schema: fn, liveTTL: -1}
	return richWorld(t, opts)
}

func richModel(t *testing.T) *dbModel {
	t.Helper()
	m, err := richWorld(t, Options{}).dbModel()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func tableOfSnap(snap *dbexplorer.Snapshot, name string) *dbexplorer.SnapshotTable {
	for i := range snap.Tables {
		if snap.Tables[i].Name == name {
			return &snap.Tables[i]
		}
	}
	return nil
}

// A database that holds exactly what the migrations describe adds nothing but
// what only a server knows: constraint names, rules, applied migrations. Nothing
// is drift.
func TestLiveSchemaThatMatchesTheMigrations(t *testing.T) {
	snap := staticSnapshot(t)
	w := worldWithSchema(t, func(context.Context) (*dbexplorer.Snapshot, error) { return snap, nil })
	out := buildLayer(t, w, dbLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	for _, q := range out.quads {
		p := q.Predicate.(quad.IRI)
		if p == "drift" {
			t.Errorf("a matching schema has no drift: %v", q)
		}
		if p == "source" && q.Object != quad.Value(quad.String("both")) {
			t.Errorf("everything exists on both sides: %v", q)
		}
	}
	// Constraint names and referential rules come from the server.
	if got := f.one(t, "col:order_items.product_id", "fk_name"); got != str("1") {
		t.Errorf("an unnamed constraint now has the server's name: %v", got)
	}
	if got := f.one(t, "col:order_items.product_id", "on_delete"); got != str("RESTRICT") {
		t.Errorf("the server reports the rule the DDL left out: %v", got)
	}
	// Applied migrations come from goose's table.
	for _, mig := range []string{"mig:0001_init"} {
		if got := f.one(t, mig, "applied"); got != quad.Value(quad.Bool(true)) {
			t.Errorf("%s applied = %v", mig, got)
		}
	}
	// Types are the server's spelling; the history still comes from the migrations.
	if got := f.one(t, "col:orders.user_id", "data_type"); got != str("bigint(20) unsigned") {
		t.Errorf("live type wins: %v", got)
	}
	if got := f.one(t, "col:orders.user_id", "introduced_in"); got != iri("mig:0001_init") {
		t.Errorf("history survives the merge: %v", got)
	}
	// The graph is otherwise the one the migrations alone give.
	static := buildLayer(t, fixtureWorld(t), dbLayer{})
	if n := len(out.quads) - len(static.quads); n < 0 || n > 12 {
		t.Errorf("live added %d quads over the static %d", n, len(static.quads))
	}
}

// A database that has drifted from the migrations says where, and the live
// database wins on what exists.
func TestLiveSchemaDrift(t *testing.T) {
	snap := snapshotOf(richModel(t))
	// Only the database has audit_log; only the migrations have profiles.
	snap.Tables = append(snap.Tables, dbexplorer.SnapshotTable{
		Name: "audit_log", Engine: "Aria", Comment: "made by hand",
		Columns: []dbexplorer.SnapshotColumn{{Name: "id", Type: "int(11)", Key: "PRI", Extra: "auto_increment"}, {Name: "order_id", Type: "bigint(20) unsigned", Nullable: true}},
		Indexes: []dbexplorer.Index{{Name: "PRIMARY", Unique: true, Columns: []string{"id"}}},
	})
	for i, tb := range snap.Tables {
		if tb.Name == "profiles" {
			snap.Tables = append(snap.Tables[:i], snap.Tables[i+1:]...)
			break
		}
	}
	orders := tableOfSnap(snap, "orders")
	orders.Comment = "customer orders"
	orders.Engine = "Aria"
	for i, c := range orders.Columns {
		switch c.Name {
		case "total": // changed type
			orders.Columns[i].Type = "decimal(12,2)"
		case "note": // dropped by hand
			orders.Columns = append(orders.Columns[:i], orders.Columns[i+1:]...)
		}
	}
	orders.Columns = append(orders.Columns, dbexplorer.SnapshotColumn{Name: "remark", Type: "text", Nullable: true}) // added by hand
	for i, ix := range orders.Indexes {
		if ix.Name == "ix_orders_user" {
			orders.Indexes[i].Columns = []string{"user_id", "status"} // a different index of the same name
		}
	}
	orders.Indexes = append(orders.Indexes, dbexplorer.Index{Name: "ix_orders_total", Columns: []string{"total"}})
	for i, c := range tableOfSnap(snap, "users").Columns {
		if c.Name == "email" {
			tableOfSnap(snap, "users").Columns[i].Nullable = true
		}
	}
	// A foreign key the migrations never made: audit_entries.user_id is no longer
	// just an implicit reference.
	audit := tableOfSnap(snap, "audit_entries")
	audit.ForeignKeys = append(audit.ForeignKeys, dbexplorer.SnapshotFK{
		Name: "fk_audit_user", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnUpdate: "RESTRICT", OnDelete: "CASCADE",
	})
	snap.Applied = []int64{1} // 0002 is not applied

	w := richWorldWithSchema(t, func(context.Context) (*dbexplorer.Snapshot, error) { return snap, nil })
	out := buildLayer(t, w, dbLayer{})
	f := facts{out.quads}

	srcOf := func(id string) string {
		v, ok := f.one(t, id, "source").(quad.String)
		if !ok {
			t.Fatalf("%s has no source", id)
		}
		return string(v)
	}
	for id, want := range map[string]string{
		"table:audit_log": "live", "col:audit_log.id": "live", "idx:audit_log.PRIMARY": "live",
		"table:profiles": "migration", "col:profiles.bio": "migration", "idx:profiles.PRIMARY": "migration",
		"table:orders": "both", "col:orders.total": "both", "col:orders.remark": "live", "col:orders.note": "migration",
		"idx:orders.ix_orders_total": "live", "idx:orders.ix_orders_user": "both", "idx:orders.ix_orders_status": "both",
	} {
		if got := srcOf(id); got != want {
			t.Errorf("%s source = %s, want %s", id, got, want)
		}
	}

	// Live wins on what exists.
	if got := f.one(t, "table:orders", "comment"); got != str("customer orders") {
		t.Errorf("table comment = %v", got)
	}
	if got := f.one(t, "table:orders", "engine"); got != str("Aria") {
		t.Errorf("engine = %v", got)
	}
	if got := f.one(t, "col:orders.total", "data_type"); got != str("decimal(12,2)") {
		t.Errorf("type = %v", got)
	}
	// And the disagreement is recorded where it is.
	drift := func(id string) string {
		v := f.out(id, "drift")
		if len(v) == 0 {
			return ""
		}
		return string(v[0].(quad.String))
	}
	if got := drift("col:orders.total"); !strings.Contains(got, "data_type: migration bigint, live decimal(12,2)") {
		t.Errorf("total drift = %q", got)
	}
	if got := drift("col:users.email"); got != "nullable: migration false, live true" {
		t.Errorf("email drift = %q", got)
	}
	if got := drift("idx:orders.ix_orders_user"); got != "columns: migration user_id, live user_id,status" {
		t.Errorf("index drift = %q", got)
	}
	if got := drift("col:audit_entries.user_id"); got != "foreign key to users only in live" {
		t.Errorf("fk drift = %q", got)
	}
	for _, id := range []string{"col:orders.status", "col:orders.id", "table:orders", "idx:orders.PRIMARY"} {
		if got := drift(id); got != "" {
			t.Errorf("%s is the same on both sides, drift %q", id, got)
		}
	}

	// The live-only foreign key is an edge, and audit_entries.user_id is no longer inferred.
	if !f.has("col:audit_entries.user_id", "fk_to", iri("col:users.id")) {
		t.Error("the live foreign key should be a fk_to edge")
	}
	if f.has("col:audit_entries.user_id", "implicit_ref", iri("col:users.id")) {
		t.Error("a declared foreign key is not an implicit reference")
	}
	if got := f.one(t, "col:audit_entries.user_id", "on_delete"); got != str("CASCADE") {
		t.Errorf("rule of the live key = %v", got)
	}
	// A live-only element was introduced by no migration; a migration that is not
	// applied says so.
	if got := f.out("table:audit_log", "introduced_in"); len(got) != 0 {
		t.Errorf("audit_log has no migration: %v", got)
	}
	if got := f.one(t, "mig:0002_extras", "applied"); got != quad.Value(quad.Bool(false)) {
		t.Errorf("0002 applied = %v", got)
	}
	if got := f.one(t, "mig:0001_init", "applied"); got != quad.Value(quad.Bool(true)) {
		t.Errorf("0001 applied = %v", got)
	}
}

// A database that cannot be reached must not take the layer down: the migrations
// still describe the schema, and the layer says it fell back.
func TestLiveSchemaUnavailable(t *testing.T) {
	w := worldWithSchema(t, func(context.Context) (*dbexplorer.Snapshot, error) { return nil, errors.New("connection refused") })
	out := buildLayer(t, w, dbLayer{})
	if len(out.warnings) != 1 || !strings.Contains(out.warnings[0], "live schema unavailable") || !strings.Contains(out.warnings[0], "connection refused") {
		t.Errorf("warnings = %v", out.warnings)
	}
	static := buildLayer(t, fixtureWorld(t), dbLayer{})
	if nquads(t, out.quads) != nquads(t, static.quads) {
		t.Error("with the database down the graph is the migrations' graph")
	}
}

// The live database can change with no file changing, so freshness checks look
// at it, but a read is reused for a few seconds.
func TestLiveSchemaChangesMakeTheLayerStale(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	snap := staticSnapshot(t)
	var calls atomic.Int32
	ix.opts.Schema = func(context.Context) (*dbexplorer.Snapshot, error) {
		calls.Add(1)
		cp := *snap
		return &cp, nil
	}
	ix.opts.liveTTL = -1
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if stale, _ := ix.Stale(ctx); len(stale) != 0 {
		t.Fatalf("stale right after a run: %v", stale)
	}

	// Someone adds a column to the live database.
	users := tableOfSnap(snap, "users")
	users.Columns = append(users.Columns, dbexplorer.SnapshotColumn{Name: "nickname", Type: "varchar(40)", Nullable: true})
	stale, err := ix.Stale(ctx)
	if err != nil || !reflect.DeepEqual(stale, []string{"db", "docs", "findings"}) {
		t.Fatalf("Stale = %v (%v), want db and the layers that resolve against it", stale, err)
	}
	rep, err := ix.Refresh(ctx)
	if err != nil || rep == nil || !reflect.DeepEqual(ran(rep), []string{"db", "docs", "findings"}) {
		t.Fatalf("Refresh = %v %v", rep, err)
	}
	wantIDs(t, "the column only the live database has", gizmoIDs(t, h, `g.V(g.IRI('col:users.nickname')).Out(g.IRI('source')).All()`), "live")

	// A new migration file is stale too.
	writeFile(t, root, "storage/migrations/sql/0004_more.sql", "-- +goose Up\nCREATE TABLE extra (id INT PRIMARY KEY);\n")
	if stale, _ := ix.Stale(ctx); !reflect.DeepEqual(stale, []string{"db", "docs", "findings"}) {
		t.Errorf("a new migration: Stale = %v", stale)
	}
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "the new table", gizmoIDs(t, h, `g.V(g.IRI('table:extra')).Out(g.IRI('source')).All()`), "migration")

	// The live read is reused within its lifetime: many freshness checks, one read.
	ix.opts.liveTTL = time.Hour
	if _, err := ix.Run(ctx); err != nil { // an explicit run always asks
		t.Fatal(err)
	}
	calls.Store(0)
	for i := 0; i < 5; i++ {
		if _, err := ix.Stale(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("%d live reads for five checks inside the lifetime of the last read", n)
	}
	if _, err := ix.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("an explicit run must ask the database afresh: %d reads", n)
	}
}

// With no database configured the layer says so by what it does not claim.
func TestNoDatabase(t *testing.T) {
	m := fixtureModel(t)
	if m.live {
		t.Error("no schema source, no live merge")
	}
	for _, mg := range m.migrations {
		if mg.applied != nil {
			t.Errorf("%s: nothing to say whether it was applied", mg.stem)
		}
	}
}
