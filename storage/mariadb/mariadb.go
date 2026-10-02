package mariadb

import (
    "database/sql"

    _ "github.com/go-sql-driver/mysql"
    "github.com/jmoiron/sqlx"
)

// Open returns a *sqlx.DB if DSN is provided, otherwise nil.
func Open(dsn string) (*sqlx.DB, error) {
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
