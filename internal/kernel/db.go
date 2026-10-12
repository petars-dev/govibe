package kernel

import (
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

// OpenDB returns a *sqlx.DB if DSN is provided, otherwise nil.
func OpenDB(dsn string) (*sqlx.DB, error) {
	if dsn == "" {
		return nil, nil
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return sqlx.NewDb(db, "mysql"), nil
}
