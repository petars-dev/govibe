package kernel

import (
	"errors"
	"strings"
	"testing"
)

func TestMigrateHintNamesTheMissingParseTime(t *testing.T) {
	goose := errors.New(`failed to initialize: failed to create version table: failed to create version table "goose_db_version": Error 1050 (42S01): Table 'goose_db_version' already exists`)
	for dsn, wantHint := range map[string]bool{
		"root@tcp(127.0.0.1:3306)/app":                                     true,  // the bare DSN that triggers it
		"root@tcp(127.0.0.1:3306)/app?multiStatements=true":                true,  // another parameter is not parseTime
		"root@tcp(127.0.0.1:3306)/app?parseTime=true":                      false, // fine: not the cause, say nothing about it
		"root@tcp(127.0.0.1:3306)/app?multiStatements=true&parseTime=true": false,
		"":                   false, // no database: nothing to say
		"not a dsn at all (": false, // an unparsable DSN is reported by the driver, not here
	} {
		got := migrateHint(dsn, goose)
		if hinted := strings.Contains(got.Error(), "parseTime=true"); hinted != wantHint {
			t.Errorf("dsn %q: hint=%v, want %v:\n%v", dsn, hinted, wantHint, got)
		}
		if !errors.Is(got, goose) {
			t.Errorf("dsn %q: the original error must stay in the chain", dsn)
		}
	}
	// An error that is not goose's "already exists" is left alone, whatever the DSN.
	other := errors.New("dial tcp: connection refused")
	if got := migrateHint("root@tcp(127.0.0.1:3306)/app", other); got != other {
		t.Errorf("an unrelated error was changed: %v", got)
	}
	if migrateHint("root@tcp(127.0.0.1:3306)/app", nil) != nil {
		t.Error("no error stays no error")
	}
}
