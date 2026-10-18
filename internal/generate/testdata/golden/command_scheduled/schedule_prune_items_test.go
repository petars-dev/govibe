package main

import (
	"testing"
	"time"

	"github.com/petars-dev/govibe/govibetest"
)

// items:prune runs at its due time (0 3 * * *): the app is booted the way serve boots
// it, on a fake clock the test moves.
func TestPruneItemsRunsOnSchedule(t *testing.T) {
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	srv := govibetest.Boot(t, newApp(), govibetest.WithTime(start))
	srv.Clock.AdvanceTo(time.Date(2026, time.March, 2, 3, 0, 0, 0, time.UTC))
	runs := srv.Runs("items:prune")
	if len(runs) != 1 || runs[0].Status != "ok" {
		t.Fatalf("runs = %+v, want one ok run", runs)
	}
}
