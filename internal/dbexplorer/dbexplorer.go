// Package dbexplorer provides read/introspection access to the app's MariaDB/MySQL
// database for the embedded, dev-only database explorer (see /dev/db) and the
// db_* MCP tools. It never runs outside of local/dev/staging builds; the caller
// (routes.go) is responsible for only constructing an Explorer when the app is
// not running in production.
package dbexplorer

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Explorer exposes safe, read-oriented database introspection plus a small,
// tightly-scoped mutation surface intended for seeding/resetting test data.
type Explorer struct {
	db             *sqlx.DB
	allowMutations bool
}

// Options configures a new Explorer.
type Options struct {
	// AllowMutations enables InsertTestRow/ResetTestData. It should only ever
	// be true outside of production, and is intended for test/dev builds that
	// want agents or the /dev/db UI to seed or clear rows.
	AllowMutations bool
}

// New wraps an existing *sqlx.DB. db must not be nil.
func New(db *sqlx.DB, opts Options) *Explorer {
	return &Explorer{db: db, allowMutations: opts.AllowMutations}
}

// AllowMutations reports whether InsertTestRow/ResetTestData are enabled.
func (e *Explorer) AllowMutations() bool { return e.allowMutations }

const (
	defaultRowLimit = 100
	maxRowLimit     = 1000
	queryTimeout    = 10 * time.Second
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrNotAllowed is returned when a mutation is attempted while disabled.
var ErrNotAllowed = fmt.Errorf("mutations are disabled (set DEV_DB_ALLOW_MUTATIONS=true outside production)")

func validIdent(s string) bool { return identRe.MatchString(s) }

func quoteIdent(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

func clampLimit(n int) int {
	if n <= 0 {
		return defaultRowLimit
	}
	if n > maxRowLimit {
		return maxRowLimit
	}
	return n
}

// Table summarizes one table in the current schema.
type Table struct {
	Name         string `json:"name"`
	Engine       string `json:"engine"`
	RowsEstimate int64  `json:"rows_estimate"`
	Comment      string `json:"comment"`
}

// Column describes one column of a table.
type Column struct {
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Nullable bool    `json:"nullable"`
	Key      string  `json:"key"`
	Default  *string `json:"default"`
	Extra    string  `json:"extra"`
}

// Index describes one index (possibly multi-column) on a table.
type Index struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

// ForeignKey describes one foreign key constraint on a table.
type ForeignKey struct {
	ConstraintName string `json:"constraint_name"`
	Column         string `json:"column"`
	RefTable       string `json:"ref_table"`
	RefColumn      string `json:"ref_column"`
}

// TableSchema is the full description of a table returned by DescribeTable.
type TableSchema struct {
	Table       string       `json:"table"`
	Columns     []Column     `json:"columns"`
	Indexes     []Index      `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreign_keys"`
}

// QueryResult is the generic tabular result of a query, sample, browse, or
// explain-plan call, shaped for direct JSON rendering by the UI or an agent.
type QueryResult struct {
	Columns    []string `json:"columns"`
	Rows       [][]any  `json:"rows"`
	RowCount   int      `json:"row_count"`
	Truncated  bool     `json:"truncated"`
	DurationMS int64    `json:"duration_ms"`
}

// ListTables returns every table in the current database/schema.
func (e *Explorer) ListTables(ctx context.Context) ([]Table, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := e.db.QueryxContext(ctx, `
		SELECT TABLE_NAME, ENGINE, IFNULL(TABLE_ROWS, 0), IFNULL(TABLE_COMMENT, '')
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Table
	for rows.Next() {
		var t Table
		var engine sql.NullString
		if err := rows.Scan(&t.Name, &engine, &t.RowsEstimate, &t.Comment); err != nil {
			return nil, err
		}
		t.Engine = engine.String
		out = append(out, t)
	}
	return out, rows.Err()
}

// tableExists checks that name is a real base table in the current schema,
// which both validates the identifier and confirms it can be safely
// interpolated into SQL built by this package.
func (e *Explorer) tableExists(ctx context.Context, name string) (bool, error) {
	if !validIdent(name) {
		return false, nil
	}
	var n int
	err := e.db.GetContext(ctx, &n, `
		SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND TABLE_TYPE = 'BASE TABLE'`, name)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DescribeTable returns columns, indexes, and foreign keys for table.
func (e *Explorer) DescribeTable(ctx context.Context, table string) (*TableSchema, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	ok, err := e.tableExists(ctx, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("table not found: %s", table)
	}

	schema := &TableSchema{Table: table}

	colRows, err := e.db.QueryxContext(ctx, `
		SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT, EXTRA
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return nil, err
	}
	for colRows.Next() {
		var c Column
		var nullable string
		var def sql.NullString
		if err := colRows.Scan(&c.Name, &c.Type, &nullable, &c.Key, &def, &c.Extra); err != nil {
			colRows.Close()
			return nil, err
		}
		c.Nullable = strings.EqualFold(nullable, "YES")
		if def.Valid {
			c.Default = &def.String
		}
		schema.Columns = append(schema.Columns, c)
	}
	if err := colRows.Err(); err != nil {
		colRows.Close()
		return nil, err
	}
	colRows.Close()

	idxRows, err := e.db.QueryxContext(ctx, `
		SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`, table)
	if err != nil {
		return nil, err
	}
	byName := map[string]*Index{}
	var order []string
	for idxRows.Next() {
		var name string
		var nonUnique int
		var col string
		if err := idxRows.Scan(&name, &nonUnique, &col); err != nil {
			idxRows.Close()
			return nil, err
		}
		idx, ok := byName[name]
		if !ok {
			idx = &Index{Name: name, Unique: nonUnique == 0}
			byName[name] = idx
			order = append(order, name)
		}
		idx.Columns = append(idx.Columns, col)
	}
	if err := idxRows.Err(); err != nil {
		idxRows.Close()
		return nil, err
	}
	idxRows.Close()
	for _, name := range order {
		schema.Indexes = append(schema.Indexes, *byName[name])
	}

	fkRows, err := e.db.QueryxContext(ctx, `
		SELECT CONSTRAINT_NAME, COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		FROM information_schema.KEY_COLUMN_USAGE
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`, table)
	if err != nil {
		return nil, err
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var fk ForeignKey
		if err := fkRows.Scan(&fk.ConstraintName, &fk.Column, &fk.RefTable, &fk.RefColumn); err != nil {
			return nil, err
		}
		schema.ForeignKeys = append(schema.ForeignKeys, fk)
	}
	return schema, fkRows.Err()
}

// BrowseOptions filters/paginates BrowseRows.
type BrowseOptions struct {
	// Where is an optional raw SQL boolean expression appended after WHERE.
	Where string
	// OrderBy is an optional raw column/expression list appended after ORDER BY.
	OrderBy string
	Limit   int
	Offset  int
}

// BrowseRows lists rows from table with optional filtering, ordering, and paging.
//
// Where/OrderBy are raw SQL fragments (there's no portable way to accept an
// arbitrary filter otherwise); this mirrors the risk already accepted by Query
// and Explain, is rejected if it looks like statement-stacking, and is never
// reachable outside of local/dev/staging builds.
func (e *Explorer) BrowseRows(ctx context.Context, table string, opts BrowseOptions) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	ok, err := e.tableExists(ctx, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	if err := rejectUnsafeFragment(opts.Where); err != nil {
		return nil, fmt.Errorf("where: %w", err)
	}
	if err := rejectUnsafeFragment(opts.OrderBy); err != nil {
		return nil, fmt.Errorf("order by: %w", err)
	}

	limit := clampLimit(opts.Limit)
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	q := "SELECT * FROM " + quoteIdent(table)
	if strings.TrimSpace(opts.Where) != "" {
		q += " WHERE " + opts.Where
	}
	if strings.TrimSpace(opts.OrderBy) != "" {
		q += " ORDER BY " + opts.OrderBy
	}
	q += fmt.Sprintf(" LIMIT %d OFFSET %d", limit+1, offset)

	return e.run(ctx, q, limit)
}

// SampleRows returns up to limit rows chosen at random from table.
func (e *Explorer) SampleRows(ctx context.Context, table string, limit int) (*QueryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	ok, err := e.tableExists(ctx, table)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	limit = clampLimit(limit)
	q := fmt.Sprintf("SELECT * FROM %s ORDER BY RAND() LIMIT %d", quoteIdent(table), limit)
	return e.run(ctx, q, limit)
}

// readOnlyKeywords are the statement kinds accepted by Query/Explain. Anything
// else (including DDL and DML) is rejected, regardless of AllowMutations —
// mutations only ever happen through InsertTestRow/ResetTestData.
var readOnlyKeywords = map[string]bool{
	"SELECT": true, "SHOW": true, "DESC": true, "DESCRIBE": true, "WITH": true, "EXPLAIN": true,
}

func firstKeyword(sqlText string) string {
	trimmed := strings.TrimSpace(sqlText)
	i := strings.IndexFunc(trimmed, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '('
	})
	if i < 0 {
		i = len(trimmed)
	}
	return strings.ToUpper(trimmed[:i])
}

func rejectUnsafeFragment(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if strings.Contains(s, ";") {
		return fmt.Errorf("multiple statements are not allowed")
	}
	if strings.Contains(s, "--") || strings.Contains(s, "/*") {
		return fmt.Errorf("comments are not allowed")
	}
	upper := strings.ToUpper(s)
	for _, kw := range []string{"INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "CREATE ", "TRUNCATE ", "GRANT ", "REVOKE ", "CALL ", "EXEC "} {
		if strings.Contains(upper, kw) {
			return fmt.Errorf("keyword %q is not allowed here", strings.TrimSpace(kw))
		}
	}
	return nil
}

// validateReadOnly ensures sqlText is a single, read-only statement.
func validateReadOnly(sqlText string) error {
	if strings.Contains(strings.TrimRight(strings.TrimSpace(sqlText), "; \n\t\r"), ";") {
		return fmt.Errorf("multiple statements are not allowed")
	}
	kw := firstKeyword(sqlText)
	if !readOnlyKeywords[kw] {
		return fmt.Errorf("only read-only statements are allowed (SELECT/SHOW/DESCRIBE/EXPLAIN/WITH), got %q", kw)
	}
	return nil
}

// Query runs an arbitrary read-only statement (SELECT/SHOW/DESCRIBE/EXPLAIN/WITH).
func (e *Explorer) Query(ctx context.Context, sqlText string) (*QueryResult, error) {
	if err := validateReadOnly(sqlText); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	limit := defaultRowLimit
	q := sqlText
	if firstKeyword(sqlText) == "SELECT" || firstKeyword(sqlText) == "WITH" {
		if !strings.Contains(strings.ToUpper(sqlText), "LIMIT") {
			q = strings.TrimRight(strings.TrimSpace(sqlText), "; ") + fmt.Sprintf(" LIMIT %d", limit+1)
		}
	}
	return e.run(ctx, q, limit)
}

// Explain returns the query planner's EXPLAIN output for a SELECT/WITH statement.
func (e *Explorer) Explain(ctx context.Context, sqlText string) (*QueryResult, error) {
	kw := firstKeyword(sqlText)
	if kw != "SELECT" && kw != "WITH" {
		return nil, fmt.Errorf("explain only supports SELECT/WITH statements, got %q", kw)
	}
	if err := validateReadOnly(sqlText); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	return e.run(ctx, "EXPLAIN "+sqlText, maxRowLimit)
}

// run executes q, scans up to limit+1 rows (to detect truncation), and shapes
// the result for JSON.
func (e *Explorer) run(ctx context.Context, q string, limit int) (*QueryResult, error) {
	start := time.Now()
	rows, err := e.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	res := &QueryResult{Columns: cols, Rows: [][]any{}}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(res.Rows) >= limit {
			res.Truncated = true
			break
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cols))
		for i, v := range vals {
			row[i] = normalizeValue(v)
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.RowCount = len(res.Rows)
	res.DurationMS = time.Since(start).Milliseconds()
	return res, nil
}

// normalizeValue converts driver-native values into JSON-friendly ones.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339)
	default:
		return t
	}
}
