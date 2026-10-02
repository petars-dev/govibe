package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed sql/*.sql
var migrations embed.FS

// SetDialect configures goose for the given database dialect (e.g. "mysql",
// "postgres", "sqlite3" — see github.com/pressly/goose/v3 for supported
// values). Goose otherwise defaults to postgres, which silently breaks
// migrations against any other database, so every entry point that runs
// goose (Up here, and the migrate CLI commands) calls this first rather than
// relying on that default.
func SetDialect(dialect string) error {
	if dialect == "" {
		dialect = "mysql"
	}
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("unsupported database type %q: %w", dialect, err)
	}
	return nil
}

// Up runs up migrations embedded into the binary against the given dialect.
func Up(ctx context.Context, db *sql.DB, dialect string) error {
	if err := SetDialect(dialect); err != nil {
		return err
	}
	goose.SetBaseFS(migrations)
	return goose.UpContext(ctx, db, "sql")
}

// MigrationsFS exposes the embed.FS for external tooling (e.g., CLI).
func MigrationsFS() embed.FS { return migrations }
