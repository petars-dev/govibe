package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// day0 is "now" for the listing tests: Sunday 2026-10-11, 18:17:43 UTC.
var day0 = time.Date(2026, 10, 11, 18, 17, 43, 0, time.UTC)

// withClock makes every Run in a test read, and be driven by, a fake clock.
func withClock(t *testing.T, start time.Time) *scheduler.Fake {
	t.Helper()
	fake := scheduler.NewFake(start)
	old := clock
	clock = fake
	t.Cleanup(func() { clock = old })
	return fake
}

// recorder notes when each command ran, by the fake clock's reading.
type recorder struct {
	mu   sync.Mutex
	runs map[string][]string
	clk  scheduler.Clock
}

func (r *recorder) note(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.runs == nil {
		r.runs = map[string][]string{}
	}
	r.runs[name] = append(r.runs[name], r.clk.Now().UTC().Format("2006-01-02 15:04:05"))
}

func (r *recorder) got(name string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs[name]...)
}

// scheduledApp is an app with two scheduled commands and one that is CLI only.
func scheduledApp(rec *recorder) App {
	app := shopApp()
	app.Commands = func(rt *Runtime) []Command {
		return []Command{
			{Use: "orders:export", Short: "Export yesterday's orders", Schedule: &Schedule{Spec: "0 2 * * *", Timeout: 10 * time.Minute},
				Run: func(ctx context.Context, rt *Runtime, args []string) error { rec.note("orders:export"); return nil }},
			{Use: "reports:digest", Short: "Send the digest", Schedule: &Schedule{Spec: "@every 6h", Overlap: Allow},
				Run: func(ctx context.Context, rt *Runtime, args []string) error { rec.note("reports:digest"); return nil }},
			{Use: "cache:warm", Short: "Warm the cache",
				Run: func(ctx context.Context, rt *Runtime, args []string) error { rec.note("cache:warm"); return nil }},
		}
	}
	return app
}

func TestScheduleHelpers(t *testing.T) {
	if s := Cron("0 2 * * *"); s.Spec != "0 2 * * *" || s.Overlap != Skip || s.Timeout != 0 {
		t.Errorf("Cron = %+v", s)
	}
	if s := Every(10 * time.Minute); s.Spec != "@every 10m" {
		t.Errorf("Every = %+v", s)
	}
	// What Every writes parses back to the same interval, for any duration.
	for _, d := range []time.Duration{time.Minute, 90 * time.Minute, 6 * time.Hour, 24 * time.Hour, 6*time.Hour + 30*time.Second, 61 * time.Minute, 100 * time.Hour} {
		s, err := scheduler.Parse(Every(d).Spec, nil, day0)
		if err != nil {
			t.Errorf("Every(%s) = %q: %v", d, Every(d).Spec, err)
			continue
		}
		if got, _ := s.Every(); got != d {
			t.Errorf("Every(%s) = %q reads back as %s", d, Every(d).Spec, got)
		}
	}
	if got := scheduler.ShortDuration(6 * time.Hour); got != "6h" {
		t.Errorf("ShortDuration(6h) = %q", got)
	}
}

// An invalid schedule or setting is a boot error that names what is wrong, and it never
// reaches the point of running anything.
func TestSchedulesAreValidatedAtBoot(t *testing.T) {
	run := func(context.Context, *Runtime, []string) error { return nil }
	cmd := func(s *Schedule) App {
		return App{Commands: func(*Runtime) []Command {
			return []Command{{Use: "orders:export", Short: "x", Run: run, Schedule: s}}
		}}
	}
	for name, tc := range map[string]struct {
		app  App
		env  map[string]string
		want string
	}{
		"bad cron":          {app: cmd(Cron("61 * * * *")), want: `command orders:export: invalid schedule "61 * * * *"`},
		"six fields":        {app: cmd(Cron("0 0 2 * * *")), want: `command orders:export: invalid schedule`},
		"@every too short":  {app: cmd(Every(10 * time.Second)), want: `command orders:export: invalid schedule "@every 10s"`},
		"empty spec":        {app: cmd(&Schedule{}), want: `command orders:export: invalid schedule: it is empty`},
		"never fires":       {app: cmd(Cron("0 0 31 2 *")), want: `command orders:export: invalid schedule "0 0 31 2 *": it never fires`},
		"bare zone":         {app: cmd(Cron("CRON_TZ=Europe/Sofia")), want: `must be followed by the schedule`},
		"unknown zone":      {app: cmd(Cron("CRON_TZ=Nowhere/Land 0 9 * * *")), want: `command orders:export`},
		"bad overlap":       {app: cmd(&Schedule{Spec: "@daily", Overlap: 7}), want: `command orders:export: unknown overlap`},
		"negative timeout":  {app: cmd(&Schedule{Spec: "@daily", Timeout: -time.Second}), want: `command orders:export: negative timeout`},
		"bad timezone key":  {app: cmd(Cron("@daily")), env: map[string]string{"SCHEDULER_TIMEZONE": "Mars/Base"}, want: `scheduler.timezone: unknown time zone "Mars/Base"`},
		"bad lock key":      {app: cmd(Cron("@daily")), env: map[string]string{"SCHEDULER_LOCK": "redis"}, want: `scheduler.lock: "redis" is not one of none, db`},
		"bad timeout key":   {app: cmd(Cron("@daily")), env: map[string]string{"SCHEDULER_SHUTDOWN_TIMEOUT": "soon"}, want: `config`},
		"setting, no tasks": {app: App{}, env: map[string]string{"SCHEDULER_TIMEZONE": "Mars/Base"}, want: `scheduler.timezone`},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Build(context.Background(), tc.app, Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}

	// And every problem is reported at once, so one run fixes them all.
	isolate(t)
	app := App{Commands: func(*Runtime) []Command {
		return []Command{
			{Use: "a:one", Run: run, Schedule: Cron("nope")},
			{Use: "b:two", Run: run, Schedule: Every(time.Second)},
		}
	}}
	_, err := Build(context.Background(), app, Options{})
	if err == nil || !strings.Contains(err.Error(), "a:one") || !strings.Contains(err.Error(), "b:two") {
		t.Fatalf("both commands must be named: %v", err)
	}
}

func TestSchedulerSettingsDefaultsAndOverrides(t *testing.T) {
	isolate(t)
	b, err := Build(context.Background(), App{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	set := b.SchedulerSettings
	if !set.Enabled || set.Loc.String() != "UTC" || set.ShutdownTimeout != 30*time.Second || set.Lock != "none" {
		t.Errorf("defaults = %+v", set)
	}
	if b.Scheduler != nil {
		t.Error("an app with no scheduled command has no scheduler")
	}
	t.Setenv("SCHEDULER_TIMEZONE", "Europe/Sofia")
	t.Setenv("SCHEDULER_SHUTDOWN_TIMEOUT", "2m")
	t.Setenv("SCHEDULER_LOCK", "DB")
	t.Setenv("SCHEDULER_ENABLED", "false")
	b, err = Build(context.Background(), App{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	set = b.SchedulerSettings
	if set.Enabled || set.Loc.String() != "Europe/Sofia" || set.ShutdownTimeout != 2*time.Minute || set.Lock != "db" {
		t.Errorf("overrides = %+v", set)
	}
}

func TestScheduleListIsComputedFromTheDeclarations(t *testing.T) {
	isolate(t)
	// No database and no server: a DSN that cannot work must not even be tried.
	t.Setenv("MARIADB_DSN", "nobody:nothing@tcp(127.0.0.1:1)/none")
	withClock(t, day0)
	var rec recorder

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), scheduledApp(&rec), []string{"schedule", "list"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"NAME            SCHEDULE    TIMEZONE  NEXT RUN             OVERLAP  TIMEOUT  DESCRIPTION",
		"orders:export   0 2 * * *   UTC       2026-10-12 02:00:00  skip     10m      Export yesterday's orders",
		"reports:digest  @every 6h  UTC       6h after start       allow    -        Send the digest",
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("output:\n%s", out.String())
	}
	// Compare by columns, not by the padding the tabwriter chose.
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for i, w := range want {
		if norm(lines[i]) != norm(w) {
			t.Errorf("line %d:\n got %q\nwant %q", i, norm(lines[i]), norm(w))
		}
	}
	if got, w := lines[3], "2 scheduled of 3 commands. Scheduler enabled; timezone UTC."; got != w {
		t.Errorf("footer:\n got %q\nwant %q", got, w)
	}
	if errOut.Len() != 0 || len(rec.got("cache:warm"))+len(rec.got("orders:export")) != 0 {
		t.Errorf("listing wrote %q to stderr or ran a command", errOut.String())
	}
}

func TestScheduleListJSONAndNext(t *testing.T) {
	isolate(t)
	withClock(t, day0)
	var rec recorder

	var out bytes.Buffer
	if err := Run(context.Background(), scheduledApp(&rec), []string{"schedule", "list", "--json", "--next", "3"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var doc scheduleDoc
	dec := json.NewDecoder(&out)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !doc.Scheduler.Enabled || doc.Scheduler.Timezone != "UTC" || doc.Scheduler.Lock != "none" || doc.Commands != 3 || len(doc.Tasks) != 2 {
		t.Fatalf("doc = %+v", doc)
	}
	orders, digest := doc.Tasks[0], doc.Tasks[1]
	if orders.Name != "orders:export" || orders.Schedule != "0 2 * * *" || orders.Overlap != "skip" || orders.Timeout != "10m0s" || orders.Description != "Export yesterday's orders" {
		t.Errorf("orders = %+v", orders)
	}
	if orders.Next == nil || orders.Next.Format(time.DateTime) != "2026-10-12 02:00:00" || len(orders.NextRuns) != 3 || orders.NextRuns[2].Format(time.DateTime) != "2026-10-14 02:00:00" {
		t.Errorf("orders next = %v %v", orders.Next, orders.NextRuns)
	}
	if digest.Every != "6h0m0s" || digest.Next != nil || len(digest.NextRuns) != 0 || digest.Overlap != "allow" {
		t.Errorf("an @every task counts from the start, so it has no date: %+v", digest)
	}

	// The same, as text: the next N times per task, and the relative one for @every.
	out.Reset()
	if err := Run(context.Background(), scheduledApp(&rec), []string{"schedule", "list", "--next", "3"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"next 3 runs of orders:export:", "  2026-10-13 02:00:00", "  2026-10-14 02:00:00", "every 6h, counted from the start of the process"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("--next output lacks %q:\n%s", w, out.String())
		}
	}
}

func TestScheduleListHonorsTheConfiguredZoneAndAnExplicitOne(t *testing.T) {
	isolate(t)
	t.Setenv("SCHEDULER_TIMEZONE", "Europe/Sofia")
	withClock(t, day0)
	app := App{Commands: func(*Runtime) []Command {
		run := func(context.Context, *Runtime, []string) error { return nil }
		return []Command{
			{Use: "a:default-zone", Short: "x", Run: run, Schedule: Cron("0 9 * * *")},
			{Use: "b:own-zone", Short: "y", Run: run, Schedule: Cron("CRON_TZ=Asia/Tokyo 0 9 * * *")},
		}
	}}
	var out bytes.Buffer
	if err := Run(context.Background(), app, []string{"schedule", "list", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var doc scheduleDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	a, b := doc.Tasks[0], doc.Tasks[1]
	if a.Timezone != "Europe/Sofia" || a.Next.Format(time.RFC3339) != "2026-10-12T09:00:00+03:00" {
		t.Errorf("default zone: %+v next %v", a, a.Next)
	}
	if b.Timezone != "Asia/Tokyo" || b.Next.Format(time.RFC3339) != "2026-10-12T09:00:00+09:00" {
		t.Errorf("CRON_TZ wins over scheduler.timezone: %+v next %v", b, b.Next)
	}
}

func TestScheduleListWithNothingScheduledOrScheduleOff(t *testing.T) {
	isolate(t)
	withClock(t, day0)
	var out bytes.Buffer
	if err := Run(context.Background(), shopApp(), []string{"schedule", "list"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "No command declares a Schedule (1 command(s)). Scheduler enabled; timezone UTC." {
		t.Errorf("got %q", got)
	}

	t.Setenv("SCHEDULER_ENABLED", "false")
	t.Setenv("SCHEDULER_LOCK", "db")
	out.Reset()
	var rec recorder
	if err := Run(context.Background(), scheduledApp(&rec), []string{"schedule", "list"}, &out, io.Discard); err != nil {
		t.Fatal(err) // a lock of db is not an error for a listing: it opens no database
	}
	if !strings.Contains(out.String(), "Scheduler disabled (scheduler.enabled is false: serve does not run them)") || !strings.Contains(out.String(), "lock db") {
		t.Errorf("the footer must say it is off and which lock is set:\n%s", out.String())
	}
}

func TestScheduleCommandUsage(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{{"schedule"}, {"schedule", "nope"}, {"schedule", "list", "extra"}, {"schedule", "list", "--next", "0"}, {"schedule", "run", "extra"}, {"schedule", "list", "--bogus"}} {
		if err := Run(context.Background(), App{}, args, io.Discard, io.Discard); err == nil {
			t.Errorf("%v must be an error", args)
		}
	}
	var out bytes.Buffer
	if err := Run(context.Background(), App{}, []string{"help"}, &out, io.Discard); err != nil || !strings.Contains(out.String(), "schedule list|run") || !strings.Contains(out.String(), "serve [--no-schedule]") {
		t.Errorf("help must list the scheduler commands (err %v):\n%s", err, out.String())
	}
	// "schedule" is a built-in name: an app cannot declare it.
	app := App{Commands: func(*Runtime) []Command {
		return []Command{{Use: "schedule", Run: func(context.Context, *Runtime, []string) error { return nil }}}
	}}
	if _, err := Build(context.Background(), app, Options{}); err == nil {
		t.Error("an app command named schedule must be refused")
	}
}

// One declaration serves the CLI and the scheduler: the same Run, and a scheduled run
// gets the flags' defaults and no arguments.
func TestAScheduledCommandRunsTheSameBodyAsTheCLI(t *testing.T) {
	isolate(t)
	fake := withClock(t, day0)
	type call struct {
		day  string
		args []string
	}
	var mu sync.Mutex
	var calls []call
	app := App{Commands: func(rt *Runtime) []Command {
		var day string
		return []Command{{
			Use: "orders:export", Short: "Export", Schedule: Cron("0 2 * * *"),
			Flags: func(fs *flag.FlagSet) { fs.StringVar(&day, "day", "yesterday", "which day") },
			Run: func(ctx context.Context, rt *Runtime, args []string) error {
				mu.Lock()
				calls = append(calls, call{day, args})
				mu.Unlock()
				return nil
			},
		}}
	}}
	if err := Run(context.Background(), app, []string{"orders:export", "--day", "2026-10-01", "extra"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}

	b, err := Build(context.Background(), app, Options{Clock: fake})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if started, err := b.StartScheduler(false); err != nil || !started {
		t.Fatalf("started=%v err=%v", started, err)
	}
	defer b.StopScheduler()
	fake.AdvanceTo(time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC))

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0].day != "2026-10-01" || len(calls[0].args) != 1 || calls[1].day != "yesterday" || len(calls[1].args) != 0 {
		t.Fatalf("calls = %+v", calls)
	}
}

// freePort is a TCP port nothing listens on.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:")
}

// startServe runs `serve` (with extra arguments) in the background and returns once it
// answers, with a stop function that returns serve's result.
func startServe(t *testing.T, app App, args ...string) (port string, stop func() error) {
	t.Helper()
	port = freePort(t)
	t.Setenv("PORT", port)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, app, append([]string{"serve"}, args...), io.Discard, io.Discard) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if resp, err := http.Get("http://127.0.0.1:" + port + "/hello"); err == nil {
			resp.Body.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve stopped before it answered: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never came up")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var once sync.Once
	var result error
	stop = func() error {
		once.Do(func() {
			cancel()
			select {
			case result = <-done:
			case <-time.After(15 * time.Second):
				t.Error("serve did not stop")
			}
		})
		return result
	}
	t.Cleanup(func() { _ = stop() })
	return port, stop
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 4000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A9, the heart of it: a command declared with a schedule runs by name and, with serve
// running, at its due time, proved with a fake clock.
func TestServeRunsScheduledCommandsAtTheirDueTime(t *testing.T) {
	isolate(t)
	fake := withClock(t, day0)
	rec := &recorder{clk: fake}
	_, stop := startServe(t, scheduledApp(rec))
	waitFor(t, "the scheduler to arm its timers", func() bool { return fake.Pending() >= 2 })

	// By hand, through the CLI path: the same body.
	if err := Run(context.Background(), scheduledApp(rec), []string{"orders:export"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.got("orders:export")); n != 1 {
		t.Fatalf("by hand: %d runs", n)
	}

	fake.AdvanceTo(time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC)) // 02:00 tomorrow
	if got := rec.got("orders:export"); len(got) != 2 || got[1] != "2026-10-12 02:00:00" {
		t.Errorf("orders:export ran at %v; want the manual run and then exactly 02:00:00", got)
	}
	if got := rec.got("reports:digest"); len(got) != 1 || got[0] != "2026-10-12 00:17:43" {
		t.Errorf("@every 6h counts from the start of serve: ran at %v", got)
	}
	if got := rec.got("cache:warm"); len(got) != 0 {
		t.Errorf("a command with no Schedule must never run by itself: %v", got)
	}

	fake.Advance(24 * time.Hour)
	if n := len(rec.got("orders:export")); n != 3 {
		t.Errorf("orders:export ran %d times in all, want the manual run and 2 scheduled", n)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	fake.Advance(48 * time.Hour)
	if n := len(rec.got("orders:export")); n != 3 {
		t.Errorf("a stopped serve ran a task (%d runs)", n)
	}
}

func TestServeNoScheduleAndSchedulerDisabledRunNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"--no-schedule":     {args: []string{"--no-schedule"}},
		"scheduler.enabled": {env: map[string]string{"SCHEDULER_ENABLED": "false"}},
	} {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			fake := withClock(t, day0)
			rec := &recorder{clk: fake}
			_, stop := startServe(t, scheduledApp(rec), tc.args...)
			if fake.Pending() != 0 {
				t.Errorf("the scheduler armed %d timer(s) although it is off", fake.Pending())
			}
			fake.Advance(48 * time.Hour)
			if got := rec.got("orders:export"); len(got) != 0 {
				t.Errorf("ran %v", got)
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServeStillTakesNothingElse(t *testing.T) {
	isolate(t)
	for _, args := range [][]string{{"serve", "extra"}, {"serve", "--bogus"}, {"serve", "--no-schedule", "extra"}} {
		if err := Run(context.Background(), App{}, args, io.Discard, io.Discard); err == nil {
			t.Errorf("%v must be refused", args)
		}
	}
}

// A task that is running when serve stops is waited for, up to scheduler.shutdown_timeout,
// and what is left after that is cancelled.
func TestServeWaitsForRunningTasksThenCancelsThem(t *testing.T) {
	isolate(t)
	t.Setenv("SCHEDULER_SHUTDOWN_TIMEOUT", "40s")
	fake := withClock(t, day0)
	fake.SetSettle(10 * time.Millisecond)

	release := make(chan struct{})
	var started, finished atomic.Int32
	var sawCancel atomic.Bool
	app := shopApp()
	app.Commands = func(*Runtime) []Command {
		return []Command{{Use: "slow:job", Short: "x", Schedule: Every(10 * time.Minute),
			Run: func(ctx context.Context, rt *Runtime, args []string) error {
				started.Add(1)
				select {
				case <-release:
				case <-ctx.Done():
					sawCancel.Store(true)
				}
				finished.Add(1)
				return nil
			}}}
	}
	var shutdownAt atomic.Int32
	app.Shutdown = func(context.Context) error { shutdownAt.Store(finished.Load() + 1); return nil }

	_, stop := startServe(t, app)
	waitFor(t, "the scheduler to arm", func() bool { return fake.Pending() >= 1 })
	fake.Advance(11 * time.Minute) // the task starts and keeps running
	waitFor(t, "the task to start", func() bool { return started.Load() == 1 })

	stopped := make(chan error, 1)
	go func() { stopped <- stop() }()
	select {
	case err := <-stopped:
		t.Fatalf("serve stopped while a task was still running and the timeout had not passed (%v)", err)
	case <-time.After(300 * time.Millisecond):
	}
	// shutdown_timeout passes on the (fake) clock with the task still running: it is cancelled.
	for done := false; !done; {
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
			done = true
		case <-time.After(20 * time.Millisecond):
			fake.Advance(time.Minute)
		}
	}
	if !sawCancel.Load() || finished.Load() != 1 {
		t.Errorf("the task's context must be cancelled after shutdown_timeout (cancelled=%v finished=%d)", sawCancel.Load(), finished.Load())
	}
	if shutdownAt.Load() != 2 {
		t.Errorf("App.Shutdown must run after the scheduler has stopped (saw %d finished tasks first)", shutdownAt.Load()-1)
	}
	close(release)
}

func TestScheduleRunRunsOnlyTheScheduler(t *testing.T) {
	isolate(t)
	fake := withClock(t, day0)
	rec := &recorder{clk: fake}
	port := freePort(t)
	t.Setenv("PORT", port)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, scheduledApp(rec), []string{"schedule", "run"}, io.Discard, io.Discard) }()
	waitFor(t, "the scheduler to arm", func() bool { return fake.Pending() >= 2 })

	fake.AdvanceTo(time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC))
	if got := rec.got("orders:export"); len(got) != 1 || got[0] != "2026-10-12 02:00:00" {
		t.Errorf("orders:export ran at %v", got)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond); err == nil {
		conn.Close()
		t.Error("`schedule run` must not serve HTTP")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("schedule run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("schedule run did not stop on its context")
	}
}

func TestScheduleRunRefusesWhenThereIsNothingToRun(t *testing.T) {
	isolate(t)
	withClock(t, day0)
	if err := Run(context.Background(), shopApp(), []string{"schedule", "run"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "no command declares a Schedule") {
		t.Errorf("no scheduled command: %v", err)
	}
	t.Setenv("SCHEDULER_ENABLED", "false")
	var rec recorder
	if err := Run(context.Background(), scheduledApp(&rec), []string{"schedule", "run"}, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "scheduler.enabled is false") {
		t.Errorf("scheduler disabled: %v", err)
	}
}

// A lock of db with no database would run a task on every instance, which is what the
// lock is there to prevent: serve and schedule run refuse, loudly.
func TestLockDBWithoutADatabaseIsRefused(t *testing.T) {
	isolate(t)
	t.Setenv("SCHEDULER_LOCK", "db")
	t.Setenv("PORT", freePort(t)) // serve binds before the lock check; never the developer's :8080
	withClock(t, day0)
	var rec recorder
	for _, args := range [][]string{{"serve"}, {"schedule", "run"}} {
		err := Run(context.Background(), scheduledApp(&rec), args, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "scheduler.lock is db but no database is configured") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestInspectCarriesCommandsSchedulesAndSchedulerSettings(t *testing.T) {
	isolate(t)
	t.Setenv("SCHEDULER_TIMEZONE", "Europe/Sofia")
	var rec recorder
	var out bytes.Buffer
	if err := Run(context.Background(), scheduledApp(&rec), []string{"inspect", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	dec.DisallowUnknownFields()
	var doc Inspect
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != routeinfo.InspectSchema || len(doc.Commands) != 3 {
		t.Fatalf("doc: %+v", doc.Commands)
	}
	byName := map[string]InspectCmd{}
	for _, c := range doc.Commands {
		byName[c.Name] = c
	}
	o := byName["orders:export"]
	if o.Schedule == nil || o.Schedule.Spec != "0 2 * * *" || o.Schedule.Timezone != "Europe/Sofia" || o.Schedule.Overlap != "skip" || o.Schedule.Timeout != "10m0s" || o.Schedule.Every != "" {
		t.Errorf("orders:export schedule = %+v", o.Schedule)
	}
	d := byName["reports:digest"]
	if d.Schedule == nil || d.Schedule.Every != "6h0m0s" || d.Schedule.Overlap != "allow" || d.Schedule.Timeout != "" {
		t.Errorf("reports:digest schedule = %+v", d.Schedule)
	}
	if byName["cache:warm"].Schedule != nil {
		t.Errorf("cache:warm has no schedule")
	}
	if o.Handler.Symbol == "" || !strings.HasSuffix(o.Handler.File, "schedule_test.go") {
		t.Errorf("the handler of a command is the function that runs it: %+v", o.Handler)
	}
	if !doc.Scheduler.Enabled || doc.Scheduler.Timezone != "Europe/Sofia" || doc.Scheduler.Lock != "none" {
		t.Errorf("scheduler = %+v", doc.Scheduler)
	}
	// inspect must not run anything or start a scheduler.
	if len(rec.got("orders:export"))+len(rec.got("reports:digest")) != 0 {
		t.Error("inspect ran a command")
	}
}

// A command with flags and overlap Allow runs twice at once: its flags are declared once, so
// the runs do not rewrite the variables they bind under each other (-race caught it).
func TestScheduledRunsDoNotRedeclareTheFlagsUnderOverlap(t *testing.T) {
	isolate(t)
	fake := withClock(t, day0)
	fake.SetSettle(5 * time.Millisecond)
	release := make(chan struct{})
	var started atomic.Int32
	var seen atomic.Value
	app := App{Commands: func(rt *Runtime) []Command {
		var day string
		return []Command{{
			Use: "items:prune", Short: "x", Schedule: &Schedule{Spec: "@every 1m", Overlap: Allow},
			Flags: func(fs *flag.FlagSet) { fs.StringVar(&day, "day", "yesterday", "which day") },
			Run: func(ctx context.Context, rt *Runtime, args []string) error {
				seen.Store(day) // read while another run may be in flight
				started.Add(1)
				<-release
				return nil
			},
		}}
	}}
	b, err := Build(context.Background(), app, Options{Clock: fake})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.StartScheduler(false); err != nil {
		t.Fatal(err)
	}
	fake.Advance(3 * time.Minute) // three due times while the first run never returns
	waitFor(t, "three overlapping runs", func() bool { return started.Load() == 3 })
	if got := seen.Load(); got != "yesterday" {
		t.Errorf("a scheduled run sees the flags' defaults: %v", got)
	}
	close(release)
	b.StopScheduler()
}

// A manual run under serve --no-schedule (the scheduler never started) is still waited for.
func TestStopSchedulerWaitsForAManualRunOfASchedulerThatWasNeverStarted(t *testing.T) {
	isolate(t)
	fake := withClock(t, day0)
	release := make(chan struct{})
	var running atomic.Bool
	app := App{Commands: func(rt *Runtime) []Command {
		return []Command{{Use: "items:prune", Short: "x", Schedule: Cron("0 3 * * *"),
			Run: func(ctx context.Context, rt *Runtime, args []string) error {
				running.Store(true)
				<-release
				running.Store(false)
				return nil
			}}}
	}}
	b, err := Build(context.Background(), app, Options{Clock: fake})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Scheduler.RunNow("items:prune"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the manual run to start", running.Load)
	stopped := make(chan struct{})
	go func() { b.StopScheduler(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("StopScheduler returned while a manual run was going: the database would be closed under it")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("StopScheduler did not return after the run finished")
	}
}

// An app with no routes (a worker that only runs commands) still tells the graph where its
// sources were compiled, from a command's Run.
func TestRootHintOfACommandsOnlyApp(t *testing.T) {
	isolate(t)
	app := App{Commands: func(rt *Runtime) []Command {
		return []Command{{Use: "items:prune", Short: "x", Run: func(context.Context, *Runtime, []string) error { return nil }}}
	}}
	var out bytes.Buffer
	if err := Run(context.Background(), app, []string{"inspect", "--json"}, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var doc Inspect
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.App.RootHint.Symbol, "TestRootHintOfACommandsOnlyApp") || !strings.HasSuffix(doc.App.RootHint.File, "schedule_test.go") || doc.App.RootHint.Line == 0 {
		t.Errorf("a commands-only app has no root hint: %+v", doc.App.RootHint)
	}
}
