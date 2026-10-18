package main

import (
	"testing"
	"time"

	"github.com/petars-dev/govibe/govibetest"
)

// cleanup runs at its due time (@every 10m): the app is booted the way serve boots
// it, on a fake clock the test moves.
func TestCleanupRunsOnSchedule(t *testing.T) {
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	srv := govibetest.Boot(t, newApp(), govibetest.WithTime(start))
	srv.Clock.AdvanceTo(time.Date(2026, time.March, 1, 12, 10, 0, 0, time.UTC))
	runs := srv.Runs("cleanup")
	if len(runs) != 1 || runs[0].Status != "ok" {
		t.Fatalf("runs = %+v, want one ok run", runs)
	}
}
