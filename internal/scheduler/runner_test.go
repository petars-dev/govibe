package scheduler

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// rec records when a task ran, by the clock's reading.
type rec struct {
	mu  sync.Mutex
	at  []time.Time
	clk Clock
}

func (r *rec) hit() {
	r.mu.Lock()
	r.at = append(r.at, r.clk.Now())
	r.mu.Unlock()
}

func (r *rec) times(loc *time.Location) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, t := range r.at {
		out = append(out, t.In(loc).Format("01-02 15:04 MST"))
	}
	return out
}

func quiet() logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

// start builds and starts a runner on a fake clock.
func start(t *testing.T, clk *Fake, loc *time.Location, tasks ...Task) *Runner {
	t.Helper()
	r, err := New(tasks, Options{Clock: clk, Location: loc, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopAndMove(r, clk) })
	return r
}

// stopAndMove stops r at the end of a test. A task a failed assertion left blocked would
// make Stop wait on a fake clock nobody advances, turning a failure into a hang: so the
// clock is moved on until Stop gives up.
func stopAndMove(r *Runner, clk *Fake) {
	done := make(chan struct{})
	go func() { r.Stop(time.Second); close(done) }()
	for {
		select {
		case <-done:
			return
		case <-time.After(20 * time.Millisecond):
			clk.Advance(2 * time.Second)
		}
	}
}

func statuses(runs []Run) []string {
	var out []string
	for i := len(runs) - 1; i >= 0; i-- { // runs are newest first
		out = append(out, runs[i].Status)
	}
	return out
}

func infoOf(t *testing.T, r *Runner, name string) Info {
	t.Helper()
	for _, i := range r.Infos(r.clk.Now()) {
		if i.Name == name {
			return i
		}
	}
	t.Fatalf("no task %q", name)
	return Info{}
}

func TestCronFiresAtExactTimes(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 2, 0))
	rc := &rec{clk: clk}
	start(t, clk, time.UTC, Task{Name: "a", Spec: "*/5 * * * *", Run: func(context.Context) error { rc.hit(); return nil }})
	clk.Advance(time.Hour)
	got := rc.times(time.UTC)
	if len(got) != 12 || got[0] != "10-11 12:05 UTC" || got[11] != "10-11 13:00 UTC" {
		t.Fatalf("want 12 runs from 12:05 to 13:00, got %d: %v", len(got), got)
	}
}

func TestNothingRunsBeforeItIsDueOrBeforeStart(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 2, 0))
	rc := &rec{clk: clk}
	r, err := New([]Task{{Name: "a", Spec: "*/5 * * * *", Run: func(context.Context) error { rc.hit(); return nil }}}, Options{Clock: clk, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Hour) // not started: the schedule is not armed
	if n := len(rc.times(time.UTC)); n != 0 {
		t.Fatalf("a runner that was not started ran %d times", n)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	clk.Advance(4 * time.Minute) // 13:02 -> 13:06: one due time, 13:05
	if got := rc.times(time.UTC); len(got) != 1 || got[0] != "10-11 13:05 UTC" {
		t.Fatalf("got %v", got)
	}
	if err := r.Start(); err == nil {
		t.Error("starting twice must fail")
	}
}

func TestEveryCountsFromStart(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 2, 30))
	rc := &rec{clk: clk}
	start(t, clk, time.UTC, Task{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { rc.hit(); return nil }})
	clk.Advance(35 * time.Minute)
	got := rc.times(time.UTC)
	if len(got) != 3 || got[0] != "10-11 12:12 UTC" || got[2] != "10-11 12:32 UTC" {
		t.Fatalf("got %v", got)
	}
}

func TestOverlapSkipAndAllow(t *testing.T) {
	for _, ov := range []Overlap{Skip, Allow} {
		clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
		clk.SetSettle(5 * time.Millisecond) // the task blocks on purpose
		release := make(chan struct{})
		var mu sync.Mutex
		started, maxConc, conc := 0, 0, 0
		r := start(t, clk, time.UTC, Task{Name: "slow", Spec: "@every 10m", Overlap: ov, Run: func(context.Context) error {
			mu.Lock()
			started++
			conc++
			maxConc = max(maxConc, conc)
			mu.Unlock()
			<-release
			mu.Lock()
			conc--
			mu.Unlock()
			return nil
		}})
		clk.Advance(35 * time.Minute) // due at :10 :20 :30 while the first run never returns
		wantStarted := map[Overlap]int{Skip: 1, Allow: 3}[ov]
		eventually(t, "the runs to start", func() bool { mu.Lock(); defer mu.Unlock(); return started == wantStarted })
		mu.Lock()
		s, m := started, maxConc
		mu.Unlock()
		skipped := 0
		for _, run := range infoOf(t, r, "slow").Runs {
			if run.Status == StatusSkipped {
				skipped += max(run.Count, 1) // consecutive identical skips are one counted record
			}
		}
		switch ov {
		case Skip:
			if s != 1 || m != 1 || skipped != 2 {
				t.Fatalf("skip: started=%d maxConcurrent=%d skipped=%d", s, m, skipped)
			}
		case Allow:
			if s != 3 || m != 3 || skipped != 0 {
				t.Fatalf("allow: started=%d maxConcurrent=%d skipped=%d", s, m, skipped)
			}
		}
		close(release)
	}
}

func TestTimeoutCooperativeAndUncooperative(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	clk.SetSettle(5 * time.Millisecond)
	stuck := make(chan struct{})
	r := start(t, clk, time.UTC,
		Task{Name: "coop", Spec: "@every 10m", Timeout: 3 * time.Minute, Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
		Task{Name: "deaf", Spec: "@every 10m", Timeout: 3 * time.Minute, Run: func(context.Context) error { <-stuck; return nil }},
	)
	clk.Advance(16 * time.Minute) // both start at :10, their deadlines pass at :13
	eventually(t, "coop to report its timeout", func() bool { return len(infoOf(t, r, "coop").Runs) == 1 })
	if st := statuses(infoOf(t, r, "coop").Runs); len(st) != 1 || st[0] != StatusTimeout {
		t.Fatalf("coop: %v", st)
	}
	if run := infoOf(t, r, "coop").Runs[0]; !strings.Contains(run.Detail, "timeout") {
		t.Errorf("the record should say it timed out: %+v", run)
	}
	// The deaf task is still running: it ignores its context and cannot be killed.
	if got := infoOf(t, r, "deaf"); got.Running != 1 || len(got.Runs) != 0 {
		t.Fatalf("deaf should still be running, unreported: %+v", got)
	}
	close(stuck) // it returns, long after its deadline
	r.waitIdle(time.Second)
	if st := statuses(infoOf(t, r, "deaf").Runs); len(st) < 1 || st[0] != StatusOverrun {
		t.Fatalf("deaf: %v", st)
	}
}

func TestPanicAndErrorAreRecordedAndTheSchedulerContinues(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	var mu sync.Mutex
	n := 0
	logger, hook := test.NewNullLogger()
	r, err := New([]Task{{Name: "boom", Spec: "@every 10m", Run: func(context.Context) error {
		mu.Lock()
		n++
		c := n
		mu.Unlock()
		switch c {
		case 1:
			panic("kaboom")
		case 2:
			return errors.New("plain error")
		}
		return nil
	}}}, Options{Clock: clk, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopAndMove(r, clk)
	clk.Advance(35 * time.Minute)
	runs := infoOf(t, r, "boom").Runs
	if st := statuses(runs); strings.Join(st, ",") != "panic,error,ok" {
		t.Fatalf("statuses: %v", st)
	}
	if !strings.Contains(runs[2].Detail, "kaboom") || runs[1].Detail != "plain error" {
		t.Errorf("details: %q / %q", runs[2].Detail, runs[1].Detail)
	}
	// Structured log lines: task, status and duration on every run; a stack for the panic.
	var sawPanicStack, sawOK bool
	for _, e := range hook.AllEntries() {
		if e.Data["task"] != "boom" || e.Data["status"] == nil || e.Data["duration"] == nil || e.Data["trigger"] != TriggerSchedule {
			t.Errorf("log line without its fields: %q %v", e.Message, e.Data)
		}
		if e.Data["status"] == StatusPanic && e.Level == logrus.ErrorLevel && e.Data["stack"] != nil {
			sawPanicStack = true
		}
		if e.Data["status"] == StatusOK && e.Level == logrus.InfoLevel {
			sawOK = true
		}
	}
	if !sawPanicStack || !sawOK {
		t.Errorf("panic with stack logged at error: %v; success at info: %v", sawPanicStack, sawOK)
	}
}

func TestStopWaitsForRunningTasksUpToTheTimeout(t *testing.T) {
	clk := &afterCounter{Fake: NewFake(utc(2026, 10, 11, 12, 0, 0))}
	clk.SetSettle(5 * time.Millisecond)
	finish := make(chan struct{})
	var sawCancel sync.WaitGroup
	sawCancel.Add(1)
	r, _ := New([]Task{{Name: "long", Spec: "@every 10m", Run: func(ctx context.Context) error {
		<-finish
		if ctx.Err() != nil {
			sawCancel.Done()
		}
		return nil
	}}}, Options{Clock: clk, Log: quiet()})
	_ = r.Start()
	clk.Advance(11 * time.Minute)
	if got := infoOf(t, r, "long").Running; got != 1 {
		t.Fatalf("running = %d", got)
	}
	res := make(chan bool, 1)
	go func() { res <- r.Stop(30 * time.Second) }()
	eventually(t, "Stop to arm its wait", func() bool { return clk.n.Load() == 1 })
	select {
	case <-res:
		t.Fatal("Stop returned while a task was still running and the timeout had not passed")
	default:
	}
	clk.Advance(31 * time.Second) // shutdown_timeout passes with the task still running: it is cancelled
	eventually(t, "Stop to start its grace", func() bool { return clk.n.Load() == 2 })
	close(finish) // the task notices the cancellation and returns within the grace
	if clean := <-res; clean {
		t.Fatal("a stop that had to cancel a task is not clean")
	}
	sawCancel.Wait() // and its context was cancelled before it returned
	if got := infoOf(t, r, "long").Running; got != 0 {
		t.Errorf("Stop returned with the cancelled task still running (%d)", got)
	}
}

func TestStopGivesUpOnATaskThatIgnoresItsContext(t *testing.T) {
	clk := &afterCounter{Fake: NewFake(utc(2026, 10, 11, 12, 0, 0))}
	clk.SetSettle(5 * time.Millisecond)
	stuck := make(chan struct{})
	r, _ := New([]Task{{Name: "deaf", Spec: "@every 10m", Run: func(context.Context) error { <-stuck; return nil }}},
		Options{Clock: clk, Log: quiet(), CancelGrace: 3 * time.Second})
	_ = r.Start()
	clk.Advance(11 * time.Minute)
	res := make(chan bool, 1)
	go func() { res <- r.Stop(30 * time.Second) }()
	eventually(t, "Stop to arm its wait", func() bool { return clk.n.Load() == 1 })
	clk.Advance(31 * time.Second)
	eventually(t, "Stop to start its grace", func() bool { return clk.n.Load() == 2 })
	select {
	case <-res:
		t.Fatal("Stop gave up before the grace had passed")
	default:
	}
	clk.Advance(4 * time.Second) // the grace passes and the task still has not returned
	if clean := <-res; clean {
		t.Fatal("expected an unclean stop")
	}
	close(stuck)
}

func TestStopIsCleanWhenTasksFinishAndNothingStartsAfterIt(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	rc := &rec{clk: clk}
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { rc.hit(); return nil }}}, Options{Clock: clk, Log: quiet()})
	_ = r.Start()
	clk.Advance(25 * time.Minute)
	if !r.Stop(time.Second) {
		t.Fatal("unclean stop with nothing running")
	}
	clk.Advance(time.Hour)
	if n := len(rc.times(time.UTC)); n != 2 {
		t.Fatalf("ran %d times; want only the 2 before the stop", n)
	}
	if _, err := r.RunNow("a"); !errors.Is(err, ErrStopped) {
		t.Errorf("RunNow after Stop: %v", err)
	}
	if !r.Stop(time.Second) {
		t.Error("a second Stop is a no-op")
	}
}

func TestInvalidTasksAreAllReportedAndNameTheCommand(t *testing.T) {
	ok := func(context.Context) error { return nil }
	_, err := New([]Task{
		{Name: "orders:export", Spec: "61 * * * *", Run: ok},
		{Name: "reports:digest", Spec: "@every 10s", Run: ok},
		{Name: "fine", Spec: "@daily", Run: ok},
		{Name: "fine", Spec: "@daily", Run: ok},
		{Name: "norun", Spec: "@daily"},
		{Name: "", Spec: "@daily", Run: ok},
		{Name: "neg", Spec: "@daily", Timeout: -time.Second, Run: ok},
	}, Options{Clock: NewFake(utc(2026, 10, 11, 12, 0, 0))})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		`command orders:export: invalid schedule "61 * * * *"`,
		`command reports:digest: invalid schedule "@every 10s"`,
		`command fine: scheduled twice`,
		`command norun: no Run`,
		`has no name`,
		`command neg: negative timeout`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not contain %q:\n%v", want, err)
		}
	}
}

// A machine that was suspended, or a process paused, wakes up late: the due times that
// passed are not run, one record says so, and the schedule resumes in the future.
func TestDueTimesPassedWhileSuspendedAreNotRunLateOrCaughtUp(t *testing.T) {
	fake := NewFake(utc(2026, 10, 11, 12, 0, 0))
	clk := &skewClock{Fake: fake}
	rc := &rec{clk: clk}
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { rc.hit(); return nil }}}, Options{Clock: clk, Log: quiet()})
	_ = r.Start()
	defer stopAndMove(r, fake)
	clk.skew.Store(int64(47 * time.Minute)) // the wall clock jumps while the timers stand still
	fake.Advance(10 * time.Minute)          // the :10 timer fires, now reads 12:57
	runs := infoOf(t, r, "a").Runs
	// 12:10 itself fired 47 minutes late: it is not run at 12:57, it is one of the due times
	// that passed (12:10 to 12:50), and the schedule resumes at 13:00.
	if st := statuses(runs); strings.Join(st, ",") != "missed" {
		t.Fatalf("statuses %v, want a single missed record and no run", st)
	}
	if !strings.Contains(runs[0].Detail, "5 due time(s)") || !strings.Contains(runs[0].Detail, "nothing is caught up") || !runs[0].Due.Equal(utc(2026, 10, 11, 12, 10, 0)) {
		t.Errorf("missed record: %+v", runs[0])
	}
	if n := len(rc.times(time.UTC)); n != 0 {
		t.Errorf("a due time that fired 47 minutes late ran (%v)", rc.times(time.UTC))
	}
	if next := infoOf(t, r, "a").Next; !next.After(clk.Now()) {
		t.Errorf("the next run %s is not in the future (%s)", next, clk.Now())
	}
}

// skewClock reads ahead of its timers, as a machine does after a suspend. Timer
// durations are relative, so only Now is skewed.
type skewClock struct {
	*Fake
	skew atomic.Int64
}

func (c *skewClock) Now() time.Time { return c.Fake.Now().Add(time.Duration(c.skew.Load())) }

// afterCounter counts the After calls, which is how a test knows Stop has armed its wait.
type afterCounter struct {
	*Fake
	n atomic.Int32
}

func (c *afterCounter) After(d time.Duration) <-chan time.Time {
	ch := c.Fake.After(d)
	c.n.Add(1) // after the timer exists, so a test that sees the count can advance the clock safely
	return ch
}

// eventually polls cond for up to a second of real time.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(500 * time.Microsecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDST(t *testing.T) {
	sof, _ := time.LoadLocation("Europe/Sofia")
	ny, _ := time.LoadLocation("America/New_York")
	run := func(spec string, loc *time.Location, from time.Time, span time.Duration) ([]string, Info) {
		clk := NewFake(from)
		rc := &rec{clk: clk}
		r := start(t, clk, loc, Task{Name: "t", Spec: spec, Run: func(context.Context) error { rc.hit(); return nil }})
		clk.Advance(span)
		return rc.times(loc), infoOf(t, r, "t")
	}
	// Spring-forward gap: the job whose time does not exist that day is skipped, nothing is caught up.
	if got, _ := run("30 3 * * *", sof, time.Date(2026, 3, 27, 12, 0, 0, 0, sof), 4*24*time.Hour); len(got) != 3 {
		t.Errorf("the gap day must be skipped (03-29): %v", got)
	}
	// Fall-back repeat: a fixed wall-clock time runs once, and says why it did not run twice.
	got, info := run("30 3 * * *", sof, time.Date(2026, 10, 24, 12, 0, 0, 0, sof), 3*24*time.Hour)
	if len(got) != 3 {
		t.Errorf("the repeated 03:30 must run once: %v", got)
	}
	var repeats int
	for _, r := range info.Runs {
		if r.Status == StatusSkipped && strings.Contains(r.Detail, "repeats") {
			repeats++
		}
	}
	if repeats != 1 {
		t.Errorf("expected one skipped record for the repeated reading, got %d: %+v", repeats, info.Runs)
	}
	if got, _ := run("30 1 * * *", ny, time.Date(2026, 10, 31, 12, 0, 0, 0, ny), 2*24*time.Hour); len(got) != 2 {
		t.Errorf("new york 01:30 across the repeat: %v", got)
	}
	// A spec that fires several times in the repeated hour runs each reading once, not the
	// second pass over it (the first version only remembered the last reading).
	for spec, want := range map[string]int{"*/15 3 * * *": 4, "0,30 3 * * *": 2, "*/20 3 * * *": 3} {
		if got, _ := run(spec, sof, time.Date(2026, 10, 24, 12, 0, 0, 0, sof), 36*time.Hour); len(got) != want {
			t.Errorf("%s across the repeated hour ran %d times %v, want %d", spec, len(got), got, want)
		}
	}
	// A spec that allows every hour, written out, follows real hours like @hourly.
	if got, _ := run("0 0-23 * * *", ny, time.Date(2026, 11, 1, 0, 30, 0, 0, ny), 4*time.Hour); len(got) != 4 {
		t.Errorf("0 0-23 across the repeat must follow real hours: %v", got)
	}
	// A wildcard hour follows real hours: the repeated hour runs again.
	if got, _ := run("30 * * * *", ny, time.Date(2026, 11, 1, 0, 45, 0, 0, ny), 5*time.Hour); len(got) != 5 {
		t.Errorf("hourly across the repeat must follow real hours: %v", got)
	}
	// A CRON_TZ prefix beats the default zone.
	if got, _ := run("CRON_TZ=Europe/Sofia 30 3 * * *", time.UTC, utc(2026, 10, 24, 12, 0, 0), 3*24*time.Hour); len(got) != 3 {
		t.Errorf("CRON_TZ: %v", got)
	}
}

func TestRunNowGoesThroughTheSamePathAndRespectsOverlap(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	release := make(chan struct{})
	calls := 0
	var mu sync.Mutex
	r, _ := New([]Task{{Name: "a", Spec: "@daily", Timeout: time.Minute, Run: func(ctx context.Context) error {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			<-release
		}
		return nil
	}}}, Options{Clock: clk, Log: quiet()})
	// It works with the scheduler not started: "run now" does not need the engine.
	done, err := r.RunNow("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunNow("a"); !errors.Is(err, ErrRunning) {
		t.Errorf("a second run while one is going: %v", err)
	}
	if _, err := r.RunNow("nope"); !errors.Is(err, ErrUnknownTask) {
		t.Errorf("unknown task: %v", err)
	}
	close(release)
	run := <-done
	if run.Status != StatusOK || run.Trigger != TriggerManual {
		t.Errorf("run = %+v", run)
	}
	st := statuses(infoOf(t, r, "a").Runs)
	if strings.Join(st, ",") != "skipped,ok" { // the refusal is in the history too
		t.Errorf("history: %v", st)
	}
}

func TestHistoryKeepsTheLast20Runs(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	r := start(t, clk, time.UTC, Task{Name: "a", Spec: "@every 1m", Run: func(context.Context) error { return nil }})
	clk.Advance(30 * time.Minute)
	runs := infoOf(t, r, "a").Runs
	if len(runs) != 20 {
		t.Fatalf("kept %d runs", len(runs))
	}
	if !runs[0].Due.After(runs[19].Due) {
		t.Error("newest first")
	}
	if got := runs[0].Due.Format("15:04"); got != "12:30" {
		t.Errorf("newest run is %s", got)
	}
}

func TestInfosAndUpcomingWorkOfflineWithNothingStarted(t *testing.T) {
	from := utc(2026, 10, 11, 18, 17, 43)
	clk := NewFake(from)
	ok := func(context.Context) error { return nil }
	r, err := New([]Task{
		{Name: "orders:export", Short: "Export", Spec: "0 2 * * *", Timeout: 10 * time.Minute, Run: ok},
		{Name: "reports:digest", Spec: "@every 6h", Overlap: Allow, Run: ok},
	}, Options{Clock: clk, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	infos := r.Infos(from)
	if infos[0].Next.Format(time.DateTime) != "2026-10-12 02:00:00" || infos[0].Timezone != "UTC" || infos[0].Timeout != 10*time.Minute || infos[0].Overlap != Skip {
		t.Errorf("%+v", infos[0])
	}
	if !infos[1].Next.IsZero() || infos[1].Every != 6*time.Hour || infos[1].Overlap != Allow {
		t.Errorf("an @every task has no next run before the start: %+v", infos[1])
	}
	up, _ := r.Upcoming("orders:export", from, 3)
	if len(up) != 3 || up[2].Format(time.DateTime) != "2026-10-14 02:00:00" {
		t.Errorf("upcoming: %v", up)
	}
	if clk.Pending() != 0 {
		t.Error("listing must arm nothing")
	}
	if r.Started() {
		t.Error("not started")
	}
	_ = r.Start()
	if !r.Started() || infoOf(t, r, "reports:digest").Next.Format(time.DateTime) != "2026-10-12 00:17:43" {
		t.Errorf("started: next of the @every task = %v", infoOf(t, r, "reports:digest").Next)
	}
	r.Stop(time.Second)
}

type fakeLocker struct {
	mu       sync.Mutex
	held     map[string]bool
	err      error
	released []string
}

func (l *fakeLocker) TryLock(_ context.Context, name string) (func(), bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return nil, false, l.err
	}
	if l.held[name] {
		return nil, false, nil
	}
	if l.held == nil {
		l.held = map[string]bool{}
	}
	l.held[name] = true
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.held, name)
		l.released = append(l.released, name)
	}, true, nil
}

func TestTheLockSkipsARunAnotherInstanceHoldsAndIsReleased(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	lk := &fakeLocker{held: map[string]bool{"a": true}} // another instance is running "a"
	calls := 0
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { calls++; return nil }}},
		Options{Clock: clk, Locker: lk, Log: quiet()})
	_ = r.Start()
	defer stopAndMove(r, clk)
	clk.Advance(10 * time.Minute)
	runs := infoOf(t, r, "a").Runs
	if calls != 0 || len(runs) != 1 || runs[0].Status != StatusSkipped || !strings.Contains(runs[0].Detail, "another instance") {
		t.Fatalf("calls=%d runs=%+v", calls, runs)
	}
	delete(lk.held, "a") // the other instance finished
	clk.Advance(10 * time.Minute)
	if calls != 1 {
		t.Fatalf("calls=%d: with the lock free the task runs", calls)
	}
	// The lock is kept until due+hold, then given back.
	if len(lk.released) != 0 || !lk.held["a"] {
		t.Errorf("released=%v held=%v: a finished run keeps the lock until its hold has passed", lk.released, lk.held)
	}
	clk.Advance(11 * time.Second)
	if len(lk.released) != 1 || lk.held["a"] {
		t.Errorf("released=%v held=%v: the lock must be given back after the hold", lk.released, lk.held)
	}
}

// The lock makes one instance run a due time. If it were given back the moment a quick run
// ended, a second instance a few milliseconds behind would take it and run the same due
// time again: with the hold it finds it taken.
func TestAQuickRunStillBlocksASecondInstanceForTheSameDueTime(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	lk := &fakeLocker{}
	var mu sync.Mutex
	runs := map[string]int{}
	mk := func(name string) *Runner {
		r, err := New([]Task{{Name: "once", Spec: "@every 10m", Run: func(context.Context) error {
			mu.Lock()
			runs[name]++
			mu.Unlock()
			return nil
		}}}, Options{Clock: clk, Locker: lk, Log: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		_ = r.Start()
		return r
	}
	a, b := mk("a"), mk("b")
	defer stopAndMove(a, clk)
	defer stopAndMove(b, clk)
	clk.Advance(10 * time.Minute) // both due; A runs and finishes before B looks
	mu.Lock()
	total := runs["a"] + runs["b"]
	mu.Unlock()
	if total != 1 {
		t.Fatalf("one due time ran %d times across two instances (%v)", total, runs)
	}
	clk.Advance(10 * time.Minute) // the next due time is taken again, the first lock long since given back
	mu.Lock()
	total = runs["a"] + runs["b"]
	mu.Unlock()
	if total != 2 {
		t.Errorf("the second due time ran %d times in all, want 2 (%v)", total, runs)
	}
}

func TestAManualRunDoesNotHoldTheLockAndStopGivesHeldLocksBack(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	lk := &fakeLocker{}
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { return nil }}}, Options{Clock: clk, Locker: lk, Log: quiet()})
	done, err := r.RunNow("a")
	if err != nil {
		t.Fatal(err)
	}
	<-done
	if lk.held["a"] || len(lk.released) != 1 {
		t.Errorf("a manual run gives the lock back at once: held=%v released=%v", lk.held, lk.released)
	}
	_ = r.Start()
	clk.Advance(10 * time.Minute)
	if !lk.held["a"] {
		t.Fatal("a scheduled run holds past its end")
	}
	if !r.Stop(time.Second) || lk.held["a"] {
		t.Errorf("Stop must give back what is held (held=%v)", lk.held)
	}
}

func TestALockErrorIsAnErrorRunNotASilentSkip(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	lk := &fakeLocker{err: errors.New("connection refused")}
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { t.Error("must not run"); return nil }}},
		Options{Clock: clk, Locker: lk, Log: quiet()})
	_ = r.Start()
	defer stopAndMove(r, clk)
	clk.Advance(10 * time.Minute)
	runs := infoOf(t, r, "a").Runs
	if len(runs) != 1 || runs[0].Status != StatusError || !strings.Contains(runs[0].Detail, "connection refused") {
		t.Fatalf("%+v", runs)
	}
}

func TestLockNamesFitMariaDBsLimit(t *testing.T) {
	l := DBLocker{Prefix: "shop"}
	if got := l.lockName("orders:export"); got != "govibe:shop:orders:export" {
		t.Errorf("short name: %q", got)
	}
	if got := (DBLocker{}).lockName("a"); got != "govibe:a" {
		t.Errorf("no prefix: %q", got)
	}
	long := strings.Repeat("x", 100)
	a, b := l.lockName(long+"1"), l.lockName(long+"2")
	if len(a) > 64 || len(b) > 64 || a == b {
		t.Errorf("long names must be 64 characters at most and still differ: %q %q", a, b)
	}
	if l.lockName(long+"1") != a {
		t.Error("shortening is deterministic")
	}
}

func TestFakeClockFiresInOrderAndStopsTimers(t *testing.T) {
	clk := NewFake(utc(2026, 1, 1, 0, 0, 0))
	var got []string
	clk.AfterFunc(3*time.Minute, func() { got = append(got, "c@"+clk.Now().Format("04")) })
	clk.AfterFunc(time.Minute, func() {
		got = append(got, "a@"+clk.Now().Format("04"))
		clk.AfterFunc(time.Minute, func() { got = append(got, "re-armed@"+clk.Now().Format("04")) }) // armed from inside, due at :02
	})
	stopped := clk.AfterFunc(2*time.Minute, func() { got = append(got, "stopped") })
	if !stopped.Stop() || stopped.Stop() {
		t.Error("Stop reports whether it prevented the call, once")
	}
	ch := clk.After(150 * time.Second)
	clk.Advance(10 * time.Minute)
	if want := "a@01 re-armed@02 c@03"; strings.Join(got, " ") != want {
		t.Errorf("fired %q, want %q", strings.Join(got, " "), want)
	}
	select {
	case at := <-ch:
		if at.Format("04:05") != "02:30" {
			t.Errorf("After delivered %s", at)
		}
	default:
		t.Error("After did not fire")
	}
	if !clk.Now().Equal(utc(2026, 1, 1, 0, 10, 0)) || clk.Pending() != 0 {
		t.Errorf("now=%s pending=%d", clk.Now(), clk.Pending())
	}
	clk.AdvanceTo(utc(2026, 1, 1, 0, 5, 0)) // never backwards
	if !clk.Now().Equal(utc(2026, 1, 1, 0, 10, 0)) {
		t.Errorf("went back to %s", clk.Now())
	}
}

// A due time is skipped, and recorded, when its timer fires long after it: a daily 03:00 job is
// not run at noon because the machine slept through the night.
func TestADueTimeThatFiresHoursLateIsMissedNotRunLate(t *testing.T) {
	fake := NewFake(utc(2026, 10, 11, 2, 0, 0))
	clk := &skewClock{Fake: fake}
	rc := &rec{clk: clk}
	r, _ := New([]Task{{Name: "daily", Spec: "0 3 * * *", Run: func(context.Context) error { rc.hit(); return nil }}}, Options{Clock: clk, Log: quiet()})
	_ = r.Start()
	defer stopAndMove(r, fake)
	clk.skew.Store(int64(9 * time.Hour)) // the machine slept: the wall clock is 9 hours ahead of the timer
	fake.Advance(time.Hour)              // the 03:00 timer fires, and the wall clock reads 12:00
	if n := len(rc.times(time.UTC)); n != 0 {
		t.Fatalf("the 03:00 job ran at noon: %v", rc.times(time.UTC))
	}
	runs := infoOf(t, r, "daily").Runs
	if len(runs) != 1 || runs[0].Status != StatusMissed || !runs[0].Due.Equal(utc(2026, 10, 11, 3, 0, 0)) {
		t.Fatalf("runs = %+v", runs)
	}
	// A timer that is only a little late still runs.
	clk.skew.Store(int64(10 * time.Second))
	fake.AdvanceTo(utc(2026, 10, 12, 3, 0, 0))
	eventually(t, "the on-time run", func() bool { return len(rc.times(time.UTC)) == 1 })
}

// The same refusal again is one counted record, so a blocked task does not push the run that
// blocked it out of the history.
func TestRepeatedSkipsAreOneCountedRecord(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	clk.SetSettle(5 * time.Millisecond)
	release := make(chan struct{})
	r := start(t, clk, time.UTC, Task{Name: "stuck", Spec: "@every 1m", Run: func(context.Context) error { <-release; return nil }})
	clk.Advance(30 * time.Minute) // the first run blocks; 29 due times are refused
	runs := infoOf(t, r, "stuck").Runs
	if len(runs) != 1 || runs[0].Status != StatusSkipped || runs[0].Count != 29 {
		t.Fatalf("29 refusals must be one record with a count: %+v", runs)
	}
	if !runs[0].Due.Equal(utc(2026, 10, 11, 12, 30, 0)) {
		t.Errorf("the record shows the latest refusal: %v", runs[0].Due)
	}
	if infoOf(t, r, "stuck").Running != 1 {
		t.Error("the run that blocks is still going")
	}
	close(release)
	eventually(t, "the blocking run to finish", func() bool { return len(infoOf(t, r, "stuck").Runs) == 2 })
	runs = infoOf(t, r, "stuck").Runs
	if runs[0].Status != StatusOK || runs[1].Status != StatusSkipped {
		t.Errorf("history (newest first): %v", statuses(runs))
	}
}

// A task that ends its goroutine (t.FailNow from a command under test) is still counted out.
func TestATaskThatExitsItsGoroutineDoesNotWedgeTheTask(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	lk := &fakeLocker{}
	n := 0
	r, _ := New([]Task{{Name: "a", Spec: "@every 10m", Run: func(context.Context) error {
		n++
		if n == 1 {
			runtime.Goexit()
		}
		return nil
	}}}, Options{Clock: clk, Locker: lk, Log: quiet(), LockHold: time.Nanosecond})
	_ = r.Start()
	defer stopAndMove(r, clk)
	clk.Advance(10 * time.Minute)
	eventually(t, "the exited run to be recorded", func() bool { return len(infoOf(t, r, "a").Runs) == 1 })
	in := infoOf(t, r, "a")
	if in.Running != 0 || in.Runs[0].Status != StatusError || !strings.Contains(in.Runs[0].Detail, "Goexit") {
		t.Fatalf("running=%d runs=%+v", in.Running, in.Runs)
	}
	clk.Advance(time.Millisecond) // past the (tiny) hold
	if lk.held["a"] {
		t.Error("the lock of the exited run was not given back")
	}
	clk.Advance(10 * time.Minute)
	if got := statuses(infoOf(t, r, "a").Runs); strings.Join(got, ",") != "error,ok" {
		t.Errorf("the task must run again after the exit: %v", got)
	}
}

func TestASecondConcurrentStopWaitsForTheFirst(t *testing.T) {
	clk := &afterCounter{Fake: NewFake(utc(2026, 10, 11, 12, 0, 0))}
	clk.SetSettle(5 * time.Millisecond)
	finish := make(chan struct{})
	r, _ := New([]Task{{Name: "long", Spec: "@every 10m", Run: func(context.Context) error { <-finish; return nil }}}, Options{Clock: clk, Log: quiet()})
	_ = r.Start()
	clk.Advance(11 * time.Minute)
	first := make(chan bool, 1)
	go func() { first <- r.Stop(time.Hour) }()
	eventually(t, "the first Stop to wait", func() bool { return clk.n.Load() == 1 })
	second := make(chan bool, 1)
	go func() { second <- r.Stop(time.Hour) }()
	select {
	case <-second:
		t.Fatal("a second Stop returned while the first was still waiting for a running task")
	case <-time.After(50 * time.Millisecond):
	}
	close(finish)
	if !<-first || !<-second {
		t.Error("both Stops report the first one's answer: clean")
	}
}

// Advancing a fake clock over a long span stays linear in the number of due times (it was
// quadratic: 40 days of a task that fires every minute took seven seconds).
func TestFakeClockOverManyDueTimesStaysLinear(t *testing.T) {
	clk := NewFake(utc(2026, 1, 1, 0, 0, 0))
	n := 0
	r, _ := New([]Task{{Name: "m", Spec: "@every 1m", Run: func(context.Context) error { n++; return nil }}}, Options{Clock: clk, Log: quiet(), History: 1})
	_ = r.Start()
	start := time.Now()
	clk.Advance(14 * 24 * time.Hour) // 20,160 due times
	if took := time.Since(start); took > 12*time.Second {
		t.Errorf("20,160 due times took %s", took)
	}
	if n != 20160 {
		t.Errorf("ran %d times, want 20160", n)
	}
	stopAndMove(r, clk)
}
