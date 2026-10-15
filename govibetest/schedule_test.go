package govibetest_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/petars-dev/govibe"
	"github.com/petars-dev/govibe/govibetest"
)

// nightly is an app with one command on a schedule, the way an app declares it.
func nightly(ran *[]time.Time, mu *sync.Mutex, clock func() time.Time, env string) govibe.App {
	return govibe.App{
		Name:   "nightly",
		Config: fstest.MapFS{"env.yml": {Data: []byte(env)}},
		Commands: func(rt *govibe.Runtime) []govibe.Command {
			return []govibe.Command{
				{Use: "orders:export", Short: "Export yesterday's orders", Schedule: govibe.Cron("0 2 * * *"),
					Run: func(ctx context.Context, rt *govibe.Runtime, args []string) error {
						mu.Lock()
						defer mu.Unlock()
						*ran = append(*ran, clock())
						return nil
					}},
				{Use: "reports:digest", Short: "Send the digest", Schedule: govibe.Every(6 * time.Hour),
					Run: func(context.Context, *govibe.Runtime, []string) error { return errors.New("smtp is down") }},
			}
		},
	}
}

func TestAScheduledCommandRunsWhenTheTestMovesTheClock(t *testing.T) {
	var mu sync.Mutex
	var ran []time.Time
	var srv *govibetest.Server
	srv = govibetest.Boot(t, nightly(&ran, &mu, func() time.Time { return srv.Clock.Now() }, ""),
		govibetest.WithTime(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)))

	if got := srv.Clock.Now(); !got.Equal(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("WithTime: the clock reads %s", got)
	}
	srv.Clock.Advance(time.Hour) // 13:00: nothing is due
	mu.Lock()
	n := len(ran)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("ran %d times before it was due", n)
	}

	srv.Clock.AdvanceTo(time.Date(2026, 3, 3, 2, 0, 0, 0, time.UTC)) // two nights
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || !ran[0].Equal(time.Date(2026, 3, 2, 2, 0, 0, 0, time.UTC)) || !ran[1].Equal(time.Date(2026, 3, 3, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("ran at %v, want 02:00 on the 2nd and the 3rd, exactly", ran)
	}
	// By the time Advance returned the runs had finished, so the test can assert at once.
	runs := srv.Runs("orders:export")
	if len(runs) != 2 || runs[0].Status != "ok" || !runs[0].Due.Equal(ran[0]) {
		t.Errorf("runs = %+v", runs)
	}
	// A failing command is recorded, and does not stop the schedule.
	failing := srv.Runs("reports:digest")
	if len(failing) != 6 || failing[0].Status != "error" || !strings.Contains(failing[0].Err, "smtp is down") {
		t.Errorf("reports:digest (%d runs) = %+v", len(failing), failing)
	}
	if srv.Runs("nope") != nil {
		t.Error("an unknown command has no runs")
	}
}

func TestTheClockStartsAtADocumentedTime(t *testing.T) {
	var mu sync.Mutex
	var ran []time.Time
	srv := govibetest.Boot(t, nightly(&ran, &mu, time.Now, ""))
	if !srv.Clock.Now().Equal(govibetest.Start) {
		t.Errorf("the default start is %s, not govibetest.Start (%s)", srv.Clock.Now(), govibetest.Start)
	}
	if govibetest.Start.Year() != 2026 || govibetest.Start.Location() != time.UTC {
		t.Errorf("Start = %s", govibetest.Start)
	}
}

// A test has no database, so a production lock setting must not stop it from booting, and
// the scheduler settings of the app's own env.yml apply.
func TestSchedulerSettingsOfTheAppApplyAndALockOfDBDoesNotBreakABoot(t *testing.T) {
	var mu sync.Mutex
	var ran []time.Time
	var srv *govibetest.Server
	srv = govibetest.Boot(t, nightly(&ran, &mu, func() time.Time { return srv.Clock.Now() },
		"scheduler:\n  timezone: Europe/Sofia\n  lock: db\n"), govibetest.WithTime(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)))
	srv.Clock.AdvanceTo(time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC))
	mu.Lock()
	defer mu.Unlock()
	// 02:00 in Sofia (UTC+2 in March) is 00:00 UTC.
	if len(ran) != 1 || !ran[0].Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ran at %v; want 00:00 UTC, which is 02:00 in Europe/Sofia", ran)
	}
}

func TestSchedulerDisabledInTheAppsConfigRunsNothing(t *testing.T) {
	var mu sync.Mutex
	var ran []time.Time
	srv := govibetest.Boot(t, nightly(&ran, &mu, time.Now, "scheduler:\n  enabled: false\n"))
	srv.Clock.Advance(72 * time.Hour)
	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 0 || len(srv.Runs("orders:export")) != 0 {
		t.Errorf("ran %v with the scheduler disabled", ran)
	}
}

// A command that is still running when the test ends must not hang the cleanup.
func TestATaskStillRunningAtTheEndOfATestDoesNotHangTheCleanup(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // runs after Boot's own cleanup (last in, first out)
	started := make(chan struct{}, 1)
	app := govibe.App{Commands: func(*govibe.Runtime) []govibe.Command {
		return []govibe.Command{{Use: "slow:job", Schedule: govibe.Every(time.Minute),
			Run: func(ctx context.Context, rt *govibe.Runtime, args []string) error {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release // ignores its context on purpose
				return nil
			}}}
	}}
	srv := govibetest.Boot(t, app, govibetest.WithSettle(5*time.Millisecond))
	srv.Clock.Advance(2 * time.Minute)
	<-started
	// The test ends here with the command stuck: Boot's cleanup must still return.
}
