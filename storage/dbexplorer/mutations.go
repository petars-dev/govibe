package dbexplorer

import (
	"context"
	"fmt"
	"strings"
)

// InsertResult is returned by InsertTestRow.
type InsertResult struct {
	Table        string         `json:"table"`
	LastInsertID int64          `json:"last_insert_id"`
	RowsAffected int64          `json:"rows_affected"`
	Row          map[string]any `json:"row,omitempty"`
}

// ResetResult is returned by ResetTestData.
type ResetResult struct {
	Table        string `json:"table"`
	RowsAffected int64  `json:"rows_affected"`
}

// InsertTestRow inserts one row into table using the given column -> value
// map, intended for seeding fixtures from the /dev/db UI or an agent. Column
// names are validated against the table's real schema, and values are always
// bound as query parameters. Requires AllowMutations.
func (e *Explorer) InsertTestRow(ctx context.Context, table string, values map[string]any) (*InsertResult, error) {
	if !e.allowMutations {
		return nil, ErrNotAllowed
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("values must not be empty")
	}

	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	schema, err := e.DescribeTable(ctx, table)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	var autoIncCol string
	for _, c := range schema.Columns {
		known[c.Name] = true
		if strings.Contains(strings.ToLower(c.Extra), "auto_increment") {
			autoIncCol = c.Name
		}
	}

	cols := make([]string, 0, len(values))
	for name := range values {
		if !known[name] {
			return nil, fmt.Errorf("unknown column %q on table %q", name, table)
		}
		cols = append(cols, name)
	}

	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	quoted := make([]string, len(cols))
	for i, name := range cols {
		quoted[i] = quoteIdent(name)
		placeholders[i] = "?"
		args[i] = values[name]
	}

	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdent(table), strings.Join(quoted, ", "), strings.Join(placeholders, ", "))

	res, err := e.db.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	lastID, _ := res.LastInsertId()
	affected, _ := res.RowsAffected()

	out := &InsertResult{Table: table, LastInsertID: lastID, RowsAffected: affected}

	if autoIncCol != "" && lastID > 0 {
		if row, err := e.fetchRowByColumn(ctx, table, autoIncCol, lastID); err == nil {
			out.Row = row
		}
	}
	return out, nil
}

// fetchRowByColumn returns a single row as a column -> value map, used to
// echo back a freshly inserted test row.
func (e *Explorer) fetchRowByColumn(ctx context.Context, table, col string, val any) (map[string]any, error) {
	q := fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", quoteIdent(table), quoteIdent(col))
	rows, err := e.db.QueryContext(ctx, q, val)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, fmt.Errorf("row not found")
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(cols))
	for i, c := range cols {
		out[c] = normalizeValue(vals[i])
	}
	return out, nil
}

// ResetTestData deletes every row from table (no WHERE clause) so a test
// fixture can be rebuilt from a known-empty state. Requires AllowMutations.
func (e *Explorer) ResetTestData(ctx context.Context, table string) (*ResetResult, error) {
	if !e.allowMutations {
		return nil, ErrNotAllowed
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	ok, err := e.tableExists(ctx, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("table not found: %s", table)
	}

	res, err := e.db.ExecContext(ctx, "DELETE FROM "+quoteIdent(table))
	if err != nil {
		return nil, err
	}
	affected, _ := res.RowsAffected()
	return &ResetResult{Table: table, RowsAffected: affected}, nil
}
