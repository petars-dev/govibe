// Package migrate runs the application's goose migrations. It is instance-based
// (the goose Provider API, no process-wide state), takes the dialect as a required
// type (goose silently picks postgres when it is never told), and reads the
// application's handed-over fs.FS, so it works for an app whose migrations are
// embedded in another module.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
)

// Dir is the directory the app's embed holds: `//go:embed sql/*.sql`.
const Dir = "sql"

// ParseDialect turns the `database.type` setting into a goose dialect. An empty or
// unknown value is an error, never a default.
func ParseDialect(s string) (goose.Dialect, error) {
	d := goose.Dialect(strings.ToLower(strings.TrimSpace(s)))
	switch d {
	case goose.DialectMySQL, goose.DialectPostgres, goose.DialectSQLite3, goose.DialectTiDB,
		goose.DialectClickHouse, goose.DialectMSSQL, goose.DialectRedshift, goose.DialectVertica, goose.DialectYdB:
		return d, nil
	case "":
		return "", fmt.Errorf("database.type is empty: set it to the goose dialect (for example mysql)")
	}
	return "", fmt.Errorf("unsupported database type %q: see github.com/pressly/goose/v3 for the dialects", s)
}

func provider(db *sql.DB, dialect goose.Dialect, migrations fs.FS) (*goose.Provider, error) {
	sub, err := fs.Sub(migrations, Dir)
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(dialect, db, sub)
}

// Up applies every pending migration and reports how many ran.
func Up(ctx context.Context, db *sql.DB, dialect goose.Dialect, migrations fs.FS) (int, error) {
	p, err := provider(db, dialect, migrations)
	if err != nil {
		return 0, fmt.Errorf("migrations: %w", err)
	}
	res, err := p.Up(ctx)
	return len(res), err
}

// Down rolls back the most recent migration.
func Down(ctx context.Context, db *sql.DB, dialect goose.Dialect, migrations fs.FS) error {
	p, err := provider(db, dialect, migrations)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err = p.Down(ctx); errors.Is(err, goose.ErrNoNextVersion) {
		return errors.New("no applied migration to roll back")
	}
	return err
}

// Status writes one line per migration: when it was applied, or "pending".
func Status(ctx context.Context, w io.Writer, db *sql.DB, dialect goose.Dialect, migrations fs.FS) error {
	p, err := provider(db, dialect, migrations)
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	rows, err := p.Status(ctx)
	if err != nil {
		return err
	}
	for _, s := range rows {
		applied := "pending"
		if s.State == goose.StateApplied {
			applied = s.AppliedAt.Format("Mon Jan _2 15:04:05 2006")
		}
		fmt.Fprintf(w, "%-26s %s\n", applied, s.Source.Path)
	}
	return nil
}

// list names the migrations the handed-over FS holds; it touches no database.
func list(db *sql.DB, dialect goose.Dialect, migrations fs.FS) ([]string, error) {
	p, err := provider(db, dialect, migrations)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range p.ListSources() {
		out = append(out, fmt.Sprintf("%d:%s", s.Version, s.Path))
	}
	return out, nil
}
