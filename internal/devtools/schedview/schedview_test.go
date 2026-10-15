package schedview

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/scheduler"
)

var day0 = time.Date(2026, 10, 11, 18, 17, 43, 0, time.UTC)

func quiet() logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

func cfg(t *testing.T, yml string) config.Config {
	t.Helper()
	c, err := config.Load(mapFS(yml))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// runner is a real scheduler on a fake clock with two tasks and one failing run behind it.
func runner(t *testing.T) (*scheduler.Runner, *scheduler.Fake) {
	t.Helper()
	clk := scheduler.NewFake(day0)
	r, err := scheduler.New([]scheduler.Task{
		{Name: "items:sync", Short: "Sync the items", Spec: "0 2 * * *", Timeout: 10 * time.Minute, Run: func(context.Context) error { return nil }},
		{Name: "reports:digest", Spec: "@every 6h", Overlap: scheduler.Allow, Run: func(context.Context) error { return errors.New("smtp is down") }},
	}, scheduler.Options{Clock: clk, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	return r, clk
}

func TestBuildDescribesARunningScheduler(t *testing.T) {
	r, clk := runner(t)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(time.Second)
	clk.Advance(7 * time.Hour) // the digest ran at 00:17:43 and failed

	st := Build(r, cfg(t, ""), 4, clk.Now())
	if st.State != StateRunning || st.Reason != "" || st.Timezone != "UTC" || st.Lock != "none" || st.Commands != 4 || st.Scheduled != 2 || st.HowToDeclare != "" {
		t.Fatalf("status = %+v", st)
	}
	sync, digest := st.Tasks[0], st.Tasks[1]
	if sync.Name != "items:sync" || sync.Description != "Sync the items" || sync.Schedule != "0 2 * * *" || sync.Overlap != "skip" || sync.Timeout != "10m" || sync.Every != "" {
		t.Errorf("sync = %+v", sync)
	}
	if sync.Next == nil || sync.Next.Format(time.DateTime) != "2026-10-12 02:00:00" || sync.NextIn != "in 42m" {
		t.Errorf("sync next = %v %q", sync.Next, sync.NextIn)
	}
	if digest.Every != "6h" || digest.Overlap != "allow" || digest.Next == nil || digest.Next.Format(time.DateTime) != "2026-10-12 06:17:43" {
		t.Errorf("a started @every task has a real next run: %+v", digest)
	}
	if len(digest.Runs) != 1 || digest.Runs[0].Status != scheduler.StatusError || digest.Runs[0].Detail != "smtp is down" || digest.Runs[0].Trigger != "schedule" || digest.Runs[0].Duration == "" {
		t.Errorf("digest runs = %+v", digest.Runs)
	}
	if sync.Runs == nil || len(sync.Runs) != 0 {
		t.Errorf("a task with no run has an empty list, not null: %#v", sync.Runs)
	}
}

func TestBuildExplainsWhyTheSchedulerIsNotRunning(t *testing.T) {
	r, clk := runner(t)

	// Built but not started (serve --no-schedule): scheduled commands exist, nothing runs here.
	st := Build(r, cfg(t, ""), 2, clk.Now())
	if st.State != StateStopped || !strings.Contains(st.Reason, "serve --no-schedule") || !strings.Contains(st.Reason, "schedule run") {
		t.Errorf("stopped: %+v", st)
	}
	if n := st.Tasks[1]; n.Next != nil || n.NextNote != "6h after start" {
		t.Errorf("an @every task that is not started has no date: %+v", n)
	}
	if n := st.Tasks[0]; n.Next == nil {
		t.Errorf("a cron task has a next run even when the scheduler is off: %+v", n)
	}

	// Disabled by configuration.
	st = Build(r, cfg(t, "scheduler:\n  enabled: false\n  timezone: Europe/Sofia\n  lock: DB\n"), 2, clk.Now())
	if st.State != StateDisabled || !strings.Contains(st.Reason, "scheduler.enabled is false") || st.Timezone != "Europe/Sofia" || st.Lock != "db" {
		t.Errorf("disabled: %+v", st)
	}
}

func TestBuildWithNothingScheduled(t *testing.T) {
	for name, src := range map[string]Source{"no source": nil, "a typed nil": Normalize((*scheduler.Runner)(nil))} {
		st := Build(src, cfg(t, ""), 3, day0)
		if st.State != StateNone || st.Scheduled != 0 || st.Tasks == nil || len(st.Tasks) != 0 || st.Commands != 3 {
			t.Errorf("%s: %+v", name, st)
		}
		if !strings.Contains(st.HowToDeclare, "govibe.Cron(") || !strings.Contains(st.HowToDeclare, "routes/console.go") {
			t.Errorf("%s: it must say how to declare a schedule: %q", name, st.HowToDeclare)
		}
	}
	// A runner with no task at all is the same as none.
	r, err := scheduler.New(nil, scheduler.Options{Clock: scheduler.NewFake(day0)})
	if err != nil {
		t.Fatal(err)
	}
	if st := Build(r, cfg(t, ""), 0, day0); st.State != StateNone {
		t.Errorf("%+v", st)
	}
}

func TestNormalizeTurnsANilPointerIntoNoSource(t *testing.T) {
	var none *scheduler.Runner
	if Normalize(none) != nil {
		t.Error("a nil *Runner in the interface is not a source")
	}
	if Normalize(nil) != nil {
		t.Error("nil stays nil")
	}
	r, _ := runner(t)
	if Normalize(r) == nil {
		t.Error("a real runner is a source")
	}
}

func TestTheDocumentIsStableJSON(t *testing.T) {
	r, clk := runner(t)
	_ = r.Start()
	defer r.Stop(time.Second)
	b, err := json.Marshal(Build(r, cfg(t, ""), 2, clk.Now()))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"state", "timezone", "lock", "commands", "scheduled", "now", "tasks"} {
		if _, ok := m[key]; !ok {
			t.Errorf("the document lacks %q: %s", key, b)
		}
	}
	task := m["tasks"].([]any)[0].(map[string]any)
	for _, key := range []string{"name", "schedule", "timezone", "overlap", "next", "running", "runs"} {
		if _, ok := task[key]; !ok {
			t.Errorf("a task lacks %q: %v", key, task)
		}
	}
}

func TestUntilAndShort(t *testing.T) {
	now := day0
	for d, want := range map[time.Duration]string{
		-time.Second:                          "now",
		0:                                     "now",
		20 * time.Second:                      "in 20s",
		90 * time.Second:                      "in 2m",
		45 * time.Minute:                      "in 45m",
		time.Hour:                             "in 1h",
		3*time.Hour + 12*time.Minute:          "in 3h12m",
		3*time.Hour + 5*time.Minute:           "in 3h05m",
		47 * time.Hour:                        "in 47h",
		5 * 24 * time.Hour:                    "in 5d",
		300 * time.Millisecond:                "in 1s", // never "in 0s"
		59*time.Second + 600*time.Millisecond: "in 1m", // never "in 60s"
		59*time.Minute + 40*time.Second:       "in 1h", // never "in 60m"
		59 * time.Hour:                        "in 2d11h",
		48 * time.Hour:                        "in 2d",
	} {
		if got := Until(now, now.Add(d)); got != want {
			t.Errorf("Until(+%s) = %q, want %q", d, got, want)
		}
	}
}

// Every time in the document reads in one zone: the task's. A manual run is stamped by the
// machine's clock and a scheduled one in the task's zone; side by side they would disagree.
func TestEveryTimeReadsInTheTasksZone(t *testing.T) {
	sofia, err := time.LoadLocation("Europe/Sofia")
	if err != nil {
		t.Fatal(err)
	}
	clk := scheduler.NewFake(day0) // the fake clock reads UTC, as a machine in another zone would not
	r, err := scheduler.New([]scheduler.Task{
		{Name: "items:sync", Spec: "0 2 * * *", Run: func(context.Context) error { return nil }},
	}, scheduler.Options{Clock: clk, Location: sofia, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Start()
	defer r.Stop(time.Second)
	done, err := r.RunNow("items:sync") // manual: stamped with the clock's own (UTC) reading
	if err != nil {
		t.Fatal(err)
	}
	<-done
	clk.Advance(9 * time.Hour) // the scheduled run, due 02:00 Sofia

	st := Build(r, cfg(t, "scheduler:\n  timezone: Europe/Sofia\n"), 1, clk.Now())
	if st.Now.Location().String() != "Europe/Sofia" {
		t.Errorf("now reads in %s", st.Now.Location())
	}
	tk := st.Tasks[0]
	if tk.Timezone != "Europe/Sofia" || tk.Next == nil || tk.Next.Location().String() != "Europe/Sofia" {
		t.Errorf("next = %v", tk.Next)
	}
	if len(tk.Runs) != 2 {
		t.Fatalf("runs = %+v", tk.Runs)
	}
	for _, run := range tk.Runs {
		if run.Due.Location().String() != "Europe/Sofia" || run.Started.Location().String() != "Europe/Sofia" {
			t.Errorf("a %s run reads in %s / %s", run.Trigger, run.Due.Location(), run.Started.Location())
		}
	}
	if got := tk.Runs[0].Due.Format("2006-01-02 15:04"); got != "2026-10-12 02:00" { // newest first: the scheduled one
		t.Errorf("scheduled due = %s", got)
	}
	if got := tk.Runs[1].Due.Format("2006-01-02 15:04"); got != "2026-10-11 21:17" { // 18:17 UTC is 21:17 in Sofia (EEST)
		t.Errorf("manual due = %s", got)
	}
}
