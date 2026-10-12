// Package runner is the spike-S8 candidate for internal/scheduler: our own runner over the robfig/cron/v3 parser.
package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"
)

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type Overlap int

const (
	Skip Overlap = iota
	Allow
)

type Task struct {
	Name    string
	Spec    string
	Overlap Overlap
	Timeout time.Duration
	RawDST  bool // spike control only: no wall-clock dedupe, i.e. plain robfig behavior
	Run     func(ctx context.Context) error
}

// Result is what a run reports (the last N are kept per task for /dev/schedule).
type Result struct {
	Task, Status string // ok | error | panic | timeout | overrun | skipped
	At           time.Time
	Err          error
}

type Runner struct {
	clk     Clock
	loc     *time.Location
	mu      sync.Mutex
	results []Result
	wg      sync.WaitGroup
	cancel  context.CancelFunc
}

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Parse validates a spec without panicking (robfig panics on a bare "CRON_TZ=Zone") and rejects specs that never fire.
func Parse(spec string, from time.Time) (sc cron.Schedule, err error) {
	defer func() {
		if r := recover(); r != nil {
			sc, err = nil, fmt.Errorf("invalid schedule %q: %v", spec, r)
		}
	}()
	if f := strings.Fields(spec); len(f) == 1 && (strings.HasPrefix(f[0], "CRON_TZ=") || strings.HasPrefix(f[0], "TZ=")) {
		return nil, fmt.Errorf("invalid schedule %q: a time zone prefix must be followed by the schedule", spec)
	}
	if sc, err = parser.Parse(spec); err != nil {
		return nil, fmt.Errorf("invalid schedule %q: %w", spec, err)
	}
	if every, ok := sc.(cron.ConstantDelaySchedule); ok && every.Delay < time.Minute {
		return nil, fmt.Errorf("invalid schedule %q: @every must be at least 1m", spec)
	}
	if sc.Next(from).IsZero() {
		return nil, fmt.Errorf("invalid schedule %q: never fires", spec)
	}
	return sc, nil
}

func New(clk Clock, loc *time.Location) *Runner { return &Runner{clk: clk, loc: loc} }

func (r *Runner) Results() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.results...)
}
func (r *Runner) add(res Result) { r.mu.Lock(); r.results = append(r.results, res); r.mu.Unlock() }

// Start validates every spec first (so a bad one fails boot), then runs one loop per task.
func (r *Runner) Start(ctx context.Context, tasks []Task) error {
	scheds := make([]cron.Schedule, len(tasks))
	for i, t := range tasks {
		sc, err := Parse(t.Spec, r.clk.Now())
		if err != nil {
			return fmt.Errorf("command %s: %w", t.Name, err)
		}
		scheds[i] = sc
	}
	ctx, r.cancel = context.WithCancel(ctx)
	for i := range tasks {
		r.wg.Add(1)
		go r.loop(ctx, tasks[i], scheds[i])
	}
	return nil
}

func (r *Runner) loop(ctx context.Context, t Task, sc cron.Schedule) {
	defer r.wg.Done()
	loc := r.loc
	if s, ok := sc.(*cron.SpecSchedule); ok && s.Location != time.Local {
		loc = s.Location // an explicit CRON_TZ wins
	}
	dedupe := !t.RawDST && fixedWallClock(t.Spec) // fixed hour AND minute: fire once per wall-clock reading (fall-back repeats the hour)
	var running atomic.Int32
	var lastWall string
	now := r.clk.Now().In(loc) // @every counts from here: process start
	for {
		next := sc.Next(now)
		if next.IsZero() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-r.clk.After(next.Sub(r.clk.Now())):
		}
		now = next // continue from the scheduled instant, never from wall time: no drift, no double fire
		if w := next.In(loc).Format("2006-01-02 15:04"); dedupe {
			if w == lastWall {
				r.add(Result{Task: t.Name, Status: "skipped", At: next, Err: fmt.Errorf("repeated wall-clock time %s (DST fall-back)", w)})
				continue
			}
			lastWall = w
		}
		if t.Overlap == Skip && !running.CompareAndSwap(0, 1) {
			r.add(Result{Task: t.Name, Status: "skipped", At: next, Err: fmt.Errorf("still running")})
			continue
		} else if t.Overlap == Allow {
			running.Add(1)
		}
		r.wg.Add(1)
		go func(at time.Time) {
			defer r.wg.Done()
			defer running.Add(-1)
			r.exec(ctx, t, at)
		}(next)
	}
}

func (r *Runner) exec(ctx context.Context, t Task, at time.Time) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var timedOut atomic.Bool
	if t.Timeout > 0 {
		go func() {
			select {
			case <-r.clk.After(t.Timeout):
				timedOut.Store(true)
				cancel() // a command that ignores ctx cannot be killed: it is reported as overrun
			case <-done:
			}
		}()
	}
	status, err := "ok", error(nil)
	func() {
		defer func() {
			if p := recover(); p != nil {
				status, err = "panic", fmt.Errorf("panic: %v", p)
			}
		}()
		err = t.Run(runCtx)
	}()
	close(done)
	switch {
	case status == "panic":
	case timedOut.Load() && err == nil:
		status = "overrun" // returned success only after its deadline had already passed
	case timedOut.Load():
		status = "timeout"
	case err != nil:
		status = "error"
	}
	r.add(Result{Task: t.Name, Status: status, At: at, Err: err})
}

func fixedWallClock(spec string) bool {
	f := strings.Fields(spec)
	if len(f) > 0 && (strings.HasPrefix(f[0], "CRON_TZ=") || strings.HasPrefix(f[0], "TZ=")) {
		f = f[1:]
	}
	if len(f) != 5 {
		return false
	}
	return !strings.ContainsAny(f[0]+f[1], "*/?")
}

// Stop ends the loops, then waits for running tasks up to timeout.
func (r *Runner) Stop(timeout time.Duration) (clean bool) {
	r.cancel()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-r.clk.After(timeout):
		return false
	}
}
