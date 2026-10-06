package dbexplorer

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

// snapshotTimeout bounds the whole of a Snapshot, which runs a handful of
// queries against information_schema.
const snapshotTimeout = 15 * time.Second

// gooseTable is goose's bookkeeping table. It is read for which migrations are
// applied and left out of the snapshot's tables: it is not part of the app's
// schema.
const gooseTable = "goose_db_version"

// Snapshot is the whole schema of the current database in one value: every
// table with its columns, indexes and foreign keys, and which goose migrations
// are applied. It is metadata only. It never contains a row, and it leaves out
// the volatile counters information_schema offers (estimated row counts,
// auto-increment positions), so two snapshots of an unchanged schema are equal.
type Snapshot struct {
	Tables []SnapshotTable `json:"tables"`
	// Goose reports whether the database has goose's bookkeeping table;
	// Applied is meaningful only then.
	Goose bool `json:"goose"`
	// Applied lists the goose migration versions currently applied, ascending.
	Applied []int64 `json:"applied_migrations"`
}

// SnapshotTable is one table with everything that describes it.
type SnapshotTable struct {
	Name        string           `json:"name"`
	Engine      string           `json:"engine"`
	Comment     string           `json:"comment"`
	Columns     []SnapshotColumn `json:"columns"` // in ordinal position
	Indexes     []Index          `json:"indexes"` // the primary key is the index named PRIMARY
	ForeignKeys []SnapshotFK     `json:"foreign_keys"`
}

// SnapshotColumn is one column of a table.
type SnapshotColumn struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"` // the full column type, e.g. varchar(255) or int(10) unsigned
	Nullable bool    `json:"nullable"`
	Key      string  `json:"key"` // PRI, UNI, MUL or ""
	Default  *string `json:"default"`
	Extra    string  `json:"extra"`
	Comment  string  `json:"comment"`
}

// SnapshotFK is one foreign key constraint, with its referential actions.
type SnapshotFK struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"` // in constraint order
	RefTable   string   `json:"ref_table"`
	RefColumns []string `json:"ref_columns"` // aligned with Columns
	OnUpdate   string   `json:"on_update"`   // RESTRICT, CASCADE, SET NULL, NO ACTION, SET DEFAULT
	OnDelete   string   `json:"on_delete"`
}

// Snapshot reads the schema of the current database with one query per kind
// (tables, columns, indexes, foreign keys) instead of one DescribeTable per
// table, plus goose's applied versions when its table exists.
func (e *Explorer) Snapshot(ctx context.Context) (*Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, snapshotTimeout)
	defer cancel()

	snap := &Snapshot{}
	byName := map[string]*SnapshotTable{}

	rows, err := e.db.QueryxContext(ctx, `
		SELECT TABLE_NAME, IFNULL(ENGINE, ''), IFNULL(TABLE_COMMENT, '')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, fmt.Errorf("snapshot tables: %w", err)
	}
	for rows.Next() {
		var t SnapshotTable
		if err := rows.Scan(&t.Name, &t.Engine, &t.Comment); err != nil {
			rows.Close()
			return nil, fmt.Errorf("snapshot tables: %w", err)
		}
		if strings.EqualFold(t.Name, gooseTable) {
			snap.Goose = true
			continue
		}
		snap.Tables = append(snap.Tables, t)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("snapshot tables: %w", err)
	}
	// Byte order, not the server's collation, so a snapshot reads the same on
	// every server configuration.
	sort.Slice(snap.Tables, func(i, j int) bool { return snap.Tables[i].Name < snap.Tables[j].Name })
	for i := range snap.Tables {
		byName[snap.Tables[i].Name] = &snap.Tables[i]
	}

	rows, err = e.db.QueryxContext(ctx, `
		SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA, IFNULL(COLUMN_COMMENT, '')
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return nil, fmt.Errorf("snapshot columns: %w", err)
	}
	for rows.Next() {
		var table, nullable string
		var c SnapshotColumn
		var def sql.NullString
		if err := rows.Scan(&table, &c.Name, &c.Type, &nullable, &c.Key, &def, &c.Extra, &c.Comment); err != nil {
			rows.Close()
			return nil, fmt.Errorf("snapshot columns: %w", err)
		}
		c.Nullable = strings.EqualFold(nullable, "YES")
		if def.Valid {
			c.Default = &def.String
		}
		if t := byName[table]; t != nil {
			t.Columns = append(t.Columns, c)
		}
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("snapshot columns: %w", err)
	}

	rows, err = e.db.QueryxContext(ctx, `
		SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		return nil, fmt.Errorf("snapshot indexes: %w", err)
	}
	idxPos := map[string]int{} // table\x00index -> position in that table's Indexes
	for rows.Next() {
		var table, name string
		var nonUnique int
		var col sql.NullString // NULL for a functional key part
		if err := rows.Scan(&table, &name, &nonUnique, &col); err != nil {
			rows.Close()
			return nil, fmt.Errorf("snapshot indexes: %w", err)
		}
		t := byName[table]
		if t == nil {
			continue
		}
		key := table + "\x00" + name
		i, ok := idxPos[key]
		if !ok {
			t.Indexes = append(t.Indexes, Index{Name: name, Unique: nonUnique == 0})
			i = len(t.Indexes) - 1
			idxPos[key] = i
		}
		t.Indexes[i].Columns = append(t.Indexes[i].Columns, col.String)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("snapshot indexes: %w", err)
	}

	rows, err = e.db.QueryxContext(ctx, `
		SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
		       r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
		 AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		 AND r.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = DATABASE() AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`)
	if err != nil {
		return nil, fmt.Errorf("snapshot foreign keys: %w", err)
	}
	fks := map[string]int{} // table\x00constraint -> index into the table's ForeignKeys
	for rows.Next() {
		var table, name, col, refTable, refCol, upd, del string
		if err := rows.Scan(&table, &name, &col, &refTable, &refCol, &upd, &del); err != nil {
			rows.Close()
			return nil, fmt.Errorf("snapshot foreign keys: %w", err)
		}
		t := byName[table]
		if t == nil {
			continue
		}
		key := table + "\x00" + name
		i, ok := fks[key]
		if !ok {
			t.ForeignKeys = append(t.ForeignKeys, SnapshotFK{Name: name, RefTable: refTable, OnUpdate: upd, OnDelete: del})
			i = len(t.ForeignKeys) - 1
			fks[key] = i
		}
		t.ForeignKeys[i].Columns = append(t.ForeignKeys[i].Columns, col)
		t.ForeignKeys[i].RefColumns = append(t.ForeignKeys[i].RefColumns, refCol)
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("snapshot foreign keys: %w", err)
	}

	for i := range snap.Tables {
		t := &snap.Tables[i]
		sort.Slice(t.Indexes, func(a, b int) bool { return t.Indexes[a].Name < t.Indexes[b].Name })
		sort.Slice(t.ForeignKeys, func(a, b int) bool { return t.ForeignKeys[a].Name < t.ForeignKeys[b].Name })
	}

	if snap.Goose {
		applied, err := e.appliedMigrations(ctx)
		if err != nil {
			return nil, err
		}
		snap.Applied = applied
	}
	return snap, nil
}

// closeRows closes rows and reports any iteration error.
func closeRows(rows interface {
	Err() error
	Close() error
}) error {
	err := rows.Err()
	if cerr := rows.Close(); err == nil {
		err = cerr
	}
	return err
}

// appliedMigrations reads goose's bookkeeping table. Goose records one row per
// run; a version is applied when its latest row says so. Version 0 is goose's
// own baseline row.
func (e *Explorer) appliedMigrations(ctx context.Context) ([]int64, error) {
	rows, err := e.db.QueryxContext(ctx, "SELECT version_id, is_applied FROM "+quoteIdent(gooseTable)+" ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("snapshot goose versions: %w", err)
	}
	state := map[int64]bool{}
	for rows.Next() {
		var v int64
		var applied bool
		if err := rows.Scan(&v, &applied); err != nil {
			rows.Close()
			return nil, fmt.Errorf("snapshot goose versions: %w", err)
		}
		state[v] = applied
	}
	if err := closeRows(rows); err != nil {
		return nil, fmt.Errorf("snapshot goose versions: %w", err)
	}
	var out []int64
	for v, ok := range state {
		if ok && v != 0 {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
