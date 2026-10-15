package kernel

import (
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// migrateHint adds to a failed migration the one thing that makes goose fail on a second run
// and reads like something else: a DSN without parseTime=true. Goose reads the timestamps of
// its version table into time.Time; the driver returns bytes unless asked to parse them, the
// read fails, and goose takes that for a missing table and tries to create it again ("Table
// 'goose_db_version' already exists"). The first run works, which is why it goes unnoticed.
// An error that is not that, or a DSN that is fine, is returned as it is.
func migrateHint(dsn string, err error) error {
	if err == nil || dsn == "" {
		return err
	}
	cfg, perr := mysql.ParseDSN(dsn)
	if perr != nil || cfg.ParseTime {
		return err
	}
	if msg := err.Error(); !strings.Contains(msg, "goose_db_version") || !strings.Contains(msg, "already exists") {
		return err
	}
	return fmt.Errorf("%w\nthe database DSN has no parseTime=true: goose cannot read the timestamps of its version table, so migrations fail on every run after the first. Add ?parseTime=true to database.mariadb.dsn (MARIADB_DSN)", err)
}
