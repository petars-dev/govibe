package runner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type rec struct {
	mu  sync.Mutex
	at  []time.Time
	clk Clock
}

func (r *rec) hit() { r.mu.Lock(); r.at = append(r.at, r.clk.Now()); r.mu.Unlock() }
func (r *rec) times(loc *time.Location) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var o []string
	for _, t := range r.at {
		o = append(o, t.In(loc).Format("01-02 15:04 MST"))
	}
	return o
}

func utc(y int, m time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, m, d, h, mi, s, 0, time.UTC)
}

func start(t *testing.T, clk *FakeClock, loc *time.Location, tasks ...Task) *Runner {
	r := New(clk, loc)
	if err := r.Start(context.Background(), tasks); err != nil {
		t.Fatal(err)
	}
	clk.BlockUntil(len(tasks))
	return r
}

func TestCronFiresAtExactTimes(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 2, 0))
	rc := &rec{clk: clk}
	r := start(t, clk, time.UTC, Task{Name: "a", Spec: "*/5 * * * *", Run: func(context.Context) error { rc.hit(); return nil }})
	clk.Advance(time.Hour, 1)
	r.Stop(time.Second)
	got := rc.times(time.UTC)
	t.Logf("fires: %v", got)
	if len(got) != 12 || got[0] != "10-11 12:05 UTC" || got[11] != "10-11 13:00 UTC" {
		t.Fatalf("want 12 fires 12:05..13:00, got %d: %v", len(got), got)
	}
}

func TestEveryCountsFromStart(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 2, 30))
	rc := &rec{clk: clk}
	r := start(t, clk, time.UTC, Task{Name: "a", Spec: "@every 10m", Run: func(context.Context) error { rc.hit(); return nil }})
	clk.Advance(35*time.Minute, 1)
	r.Stop(time.Second)
	got := rc.times(time.UTC)
	t.Logf("fires: %v", got)
	if len(got) != 3 || got[0] != "10-11 12:12 UTC" {
		t.Fatalf("got %v", got)
	}
}

func TestOverlapSkipAndAllow(t *testing.T) {
	for _, ov := range []Overlap{Skip, Allow} {
		clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
		release := make(chan struct{})
		var mu sync.Mutex
		started, maxConc, conc := 0, 0, 0
		r := start(t, clk, time.UTC, Task{Name: "slow", Spec: "@every 10m", Overlap: ov, Run: func(context.Context) error {
			mu.Lock()
			started++
			conc++
			if conc > maxConc {
				maxConc = conc
			}
			mu.Unlock()
			<-release
			mu.Lock()
			conc--
			mu.Unlock()
			return nil
		}})
		clk.Advance(35*time.Minute, 1) // due at :10 :20 :30 while the first run (from :10) never returns
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		s, m := started, maxConc
		mu.Unlock()
		skipped := 0
		for _, res := range r.Results() {
			if res.Status == "skipped" {
				skipped++
			}
		}
		t.Logf("overlap=%v started=%d maxConcurrent=%d skipped=%d", ov, s, m, skipped)
		if ov == Skip && (s != 1 || m != 1 || skipped != 2) {
			t.Fatalf("skip: started=%d max=%d skipped=%d", s, m, skipped)
		}
		if ov == Allow && (s != 3 || m != 3 || skipped != 0) {
			t.Fatalf("allow: started=%d max=%d skipped=%d", s, m, skipped)
		}
		close(release)
		r.Stop(time.Second)
	}
}

func TestTimeoutCooperativeAndUncooperative(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	stuck := make(chan struct{})
	r := start(t, clk, time.UTC,
		Task{Name: "coop", Spec: "@every 10m", Timeout: 3 * time.Minute, Run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
		Task{Name: "deaf", Spec: "@every 10m", Timeout: 3 * time.Minute, Run: func(ctx context.Context) error { <-stuck; return nil }},
	)
	clk.Advance(11*time.Minute, 2) // fire at :10, deadline would be :13 -> advance on
	clk.Advance(5*time.Minute, 2)  // :16 -> deadlines passed
	time.Sleep(5 * time.Millisecond)
	st := map[string]string{}
	for _, res := range r.Results() {
		st[res.Task] = res.Status
	}
	t.Logf("after deadline: %v (deaf is still running; it has not reported yet)", st)
	if st["coop"] != "timeout" {
		t.Fatalf("coop: %v", st)
	}
	close(stuck) // the deaf command finally returns, after its deadline
	time.Sleep(5 * time.Millisecond)
	for _, res := range r.Results() {
		st[res.Task] = res.Status
	}
	t.Logf("after deaf returned: %v", st)
	if st["deaf"] != "overrun" {
		t.Fatalf("deaf: %v", st)
	}
	r.Stop(time.Second)
}

func TestPanicIsRecoveredAndSchedulerContinues(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	n := 0
	var mu sync.Mutex
	r := start(t, clk, time.UTC, Task{Name: "boom", Spec: "@every 10m", Run: func(context.Context) error {
		mu.Lock()
		n++
		c := n
		mu.Unlock()
		if c == 1 {
			panic("kaboom")
		}
		if c == 2 {
			return errors.New("plain error")
		}
		return nil
	}})
	clk.Advance(35*time.Minute, 1)
	time.Sleep(5 * time.Millisecond)
	var st []string
	for _, res := range r.Results() {
		st = append(st, res.Status)
	}
	t.Logf("statuses: %v", st)
	if len(st) != 3 || st[0] != "panic" || st[1] != "error" || st[2] != "ok" {
		t.Fatalf("%v", st)
	}
	r.Stop(time.Second)
}

func TestShutdownWaitsForRunningUpToTimeout(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	finish := make(chan struct{})
	r := start(t, clk, time.UTC, Task{Name: "long", Spec: "@every 10m", Run: func(ctx context.Context) error { <-finish; return nil }})
	clk.Advance(11*time.Minute, 1)
	time.Sleep(5 * time.Millisecond)
	res := make(chan bool, 1)
	armed := clk.Waiters()
	go func() { res <- r.Stop(30 * time.Second) }()
	clk.BlockUntil(armed + 1)      // Stop has armed its shutdown_timeout timer (the cancelled loop's stale timer is still listed)
	clk.Advance(31*time.Second, 0) // shutdown_timeout passes with the task still running
	if clean := <-res; clean {
		t.Fatal("expected unclean shutdown (task still running)")
	}
	t.Log("Stop returned unclean after shutdown_timeout while a task was still running")
	close(finish)
}

func TestInvalidSpecsFailStartNamingTheCommand(t *testing.T) {
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	for _, spec := range []string{"* * *", "61 * * * *", "@every", "@weekly2", "CRON_TZ=Nowhere/Land * * * * *", "0 0 9 * * *", "CRON_TZ=Europe/Sofia", "@every 0s", "@every -5m", "@every 30s", "0 0 31 2 *"} {
		err := New(clk, time.UTC).Start(context.Background(), []Task{{Name: "orders:export", Spec: spec, Run: func(context.Context) error { return nil }}})
		t.Logf("%-34q -> %v", spec, err)
		if err == nil {
			t.Fatalf("%q should be rejected", spec)
		}
	}
}

func TestDST(t *testing.T) {
	sof, _ := time.LoadLocation("Europe/Sofia")
	ny, _ := time.LoadLocation("America/New_York")
	raw := false
	run := func(name, spec string, loc *time.Location, from time.Time, span time.Duration) []string {
		clk := NewFake(from)
		rc := &rec{clk: clk}
		r := start(t, clk, loc, Task{Name: name, Spec: spec, RawDST: raw, Run: func(context.Context) error { rc.hit(); return nil }})
		clk.Advance(span, 1)
		r.Stop(time.Second)
		got := rc.times(loc)
		t.Logf("%-52s %v", name+" "+spec, got)
		return got
	}
	// Spring-forward gap: the job whose time does not exist that day is skipped, nothing is caught up.
	g := run("sofia gap daily 03:30 (03-27..03-31)", "30 3 * * *", sof, time.Date(2026, 3, 27, 12, 0, 0, 0, sof), 4*24*time.Hour)
	if len(g) != 3 {
		t.Fatalf("gap day must be skipped: %v", g)
	}
	// Fall-back repeat: a fixed wall-clock time fires once (raw robfig fires twice).
	f := run("sofia repeat daily 03:30 (10-24..10-27)", "30 3 * * *", sof, time.Date(2026, 10, 24, 12, 0, 0, 0, sof), 3*24*time.Hour)
	if len(f) != 3 { // one per day: 10-25, 10-26, 10-27
		t.Fatalf("repeat must fire once: %v", f)
	}
	raw = true
	fr := run("CONTROL raw robfig, same window", "30 3 * * *", sof, time.Date(2026, 10, 24, 12, 0, 0, 0, sof), 3*24*time.Hour)
	if len(fr) != 4 {
		t.Fatalf("control: raw robfig should fire twice on the repeat day: %v", fr)
	}
	raw = false
	n := run("ny repeat daily 01:30 (10-31..11-02)", "30 1 * * *", ny, time.Date(2026, 10, 31, 12, 0, 0, 0, ny), 2*24*time.Hour)
	if len(n) != 2 {
		t.Fatalf("ny repeat: %v", n)
	}
	// Wildcard hour: real elapsed hours, so the repeated hour fires again (that is what "hourly" means).
	h := run("ny hourly :30 across fall-back (11-01 00:45..04:45 local)", "30 * * * *", ny, time.Date(2026, 11, 1, 0, 45, 0, 0, ny), 5*time.Hour)
	if len(h) != 5 {
		t.Fatalf("hourly across repeat: %v", h)
	}
	// Explicit CRON_TZ with a UTC default.
	c := run("CRON_TZ=Europe/Sofia 03:30 with default UTC", "CRON_TZ=Europe/Sofia 30 3 * * *", time.UTC, utc(2026, 10, 24, 12, 0, 0), 3*24*time.Hour)
	if len(c) != 3 {
		t.Fatalf("CRON_TZ: %v", c)
	}
}

func TestOfflineNextForScheduleList(t *testing.T) {
	now := utc(2026, 10, 11, 18, 17, 43)
	for _, spec := range []string{"0 2 * * *", "@every 6h", "CRON_TZ=Europe/Sofia 0 9 * * MON-FRI"} {
		sc, err := Parse(spec, now)
		if err != nil {
			t.Fatal(err)
		}
		n1 := sc.Next(now)
		n2 := sc.Next(n1)
		n3 := sc.Next(n2)
		t.Logf("%-40s next 3 (from now, no engine, no clock): %s | %s | %s", spec, n1.Format("01-02 15:04:05Z07:00"), n2.Format("01-02 15:04:05Z07:00"), n3.Format("01-02 15:04:05Z07:00"))
	}
}
