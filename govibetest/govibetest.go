// Package govibetest boots an application inside a test, the way `serve` boots it
// and without anything a test must not touch: the database is never opened and the
// dev tools are never mounted. The scheduler runs on a fake clock the test moves.
//
//	func TestHome(t *testing.T) {
//		srv := govibetest.Boot(t, newApp())
//		resp, err := http.Get(srv.URL + "/")
//		...
//	}
//
//	func TestNightlyPrune(t *testing.T) {
//		srv := govibetest.Boot(t, newApp(), govibetest.WithTime(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)))
//		srv.Clock.AdvanceTo(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)) // runs "items:prune" at 03:00
//		if runs := srv.Runs("items:prune"); len(runs) != 1 || runs[0].Status != "ok" {
//			t.Fatalf("runs = %+v", runs)
//		}
//	}
package govibetest

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/petars-dev/govibe"
	"github.com/petars-dev/govibe/internal/kernel"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// Start is the time a booted app's clock reads unless WithTime says otherwise.
var Start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Server is a booted application served on a local test server.
type Server struct {
	*httptest.Server
	// Rt is the runtime the app's Routes and Commands were given. Rt.DB is always
	// nil: Boot never opens a database, whatever MARIADB_DSN says in the environment.
	Rt *govibe.Runtime
	// Clock is the fake clock the scheduler runs on. Nothing scheduled runs until the
	// test advances it.
	Clock *Clock

	runner *scheduler.Runner
}

// Clock is a manual clock: time moves only when the test says so.
type Clock struct{ fake *scheduler.Fake }

// Now is the clock's current time.
func (c *Clock) Now() time.Time { return c.fake.Now() }

// Advance moves the clock forward by d. Every scheduled command that falls due on the
// way runs, in time order, and Advance returns once those runs have finished: a command
// that takes no virtual time has completed by then, so the test can assert at once. The
// wait is real time and bounded at five seconds per due run; a command that blocks on
// purpose (to test overlap) is waited for that long, so give such a test a short
// WithSettle.
func (c *Clock) Advance(d time.Duration) { c.fake.Advance(d) }

// AdvanceTo moves the clock to t, as Advance does. It never goes backwards.
func (c *Clock) AdvanceTo(t time.Time) { c.fake.AdvanceTo(t) }

// Run is the record of one scheduled run.
type Run struct {
	Command string
	// Due is the instant the run was scheduled for.
	Due time.Time
	// Status is "ok", "error", "panic", "timeout" (the deadline passed and the command
	// returned an error), "overrun" (it returned success after its deadline), "skipped"
	// (the previous run was still going, or a repeated wall-clock hour) or "missed".
	Status string
	// Err is the error text, or why the run was skipped.
	Err string
	// Count is how many times in a row the record happened, when more than once: a task that
	// is blocked is skipped on every due time, and that is one record with a count.
	Count int
}

// Runs lists the recorded runs of a scheduled command, oldest first (the last 20). It
// is nil for a command that is not scheduled.
func (s *Server) Runs(command string) []Run {
	if s.runner == nil {
		return nil
	}
	var out []Run
	for _, in := range s.runner.Infos(s.Clock.Now()) {
		if in.Name != command {
			continue
		}
		for i := len(in.Runs) - 1; i >= 0; i-- { // newest first, to oldest first
			r := in.Runs[i]
			out = append(out, Run{Command: command, Due: r.Due, Status: r.Status, Err: r.Detail, Count: r.Count})
		}
	}
	return out
}

// Option changes how Boot starts the app.
type Option func(*options)

type options struct {
	start  time.Time
	settle time.Duration
}

// WithTime sets the time the fake clock starts at (Start by default).
func WithTime(t time.Time) Option { return func(o *options) { o.start = t } }

// WithSettle sets how long, in real time, the clock waits for the runs a due time started
// before it moves on to the next (five seconds by default). A test that starts a
// command which blocks on purpose lowers it.
func WithSettle(d time.Duration) Option { return func(o *options) { o.settle = d } }

// Boot builds app through the framework's one boot path (configuration, router,
// command table, schedules) and serves its router on an httptest server that closes
// with the test. The database is not opened and no migration runs, so a test needs no
// database and cannot reach one by accident (a scheduler.lock of db is ignored for the
// same reason); the dev tools are not mounted, so a page renders as it does in
// production. The scheduler is started on a fake clock: see Server.Clock. A boot error,
// an invalid schedule included, fails the test.
func Boot(t testing.TB, app govibe.App, opts ...Option) *Server {
	t.Helper()
	o := options{start: Start}
	for _, opt := range opts {
		opt(&o)
	}
	fake := scheduler.NewFake(o.start)
	if o.settle > 0 {
		fake.SetSettle(o.settle)
	}
	b, err := kernel.Build(context.Background(), app, kernel.Options{Quiet: true, Clock: fake})
	if err != nil {
		t.Fatalf("govibetest: boot %s: %v", app.Name, err)
	}
	b.WithoutLock()
	if _, err := b.StartScheduler(false); err != nil {
		_ = b.Close()
		t.Fatalf("govibetest: start the scheduler of %s: %v", app.Name, err)
	}
	srv := httptest.NewServer(b.Router)
	t.Cleanup(func() {
		srv.Close()
		stopScheduler(b, fake)
		_ = b.Close()
	})
	return &Server{Server: srv, Rt: b.Rt, Clock: &Clock{fake: fake}, runner: b.Scheduler}
}

// stopScheduler stops the scheduler at the end of a test. A command still running would
// make the stop wait on a fake clock nobody advances, so the clock is moved past what the
// stop waits for (the shutdown timeout, then the grace), and nothing is worth waiting for
// in real time: the test is over.
func stopScheduler(b *kernel.Built, fake *scheduler.Fake) {
	fake.SetSettle(time.Millisecond)
	step := b.SchedulerSettings.ShutdownTimeout + time.Minute
	done := make(chan struct{})
	go func() { b.StopScheduler(); close(done) }()
	for {
		select {
		case <-done:
			return
		case <-time.After(10 * time.Millisecond):
			fake.Advance(step)
		}
	}
}
