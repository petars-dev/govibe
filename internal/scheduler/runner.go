// Package scheduler runs a set of tasks on schedules: cron-spec parsing with the
// framework's validation, a runner with overlap, timeout and panic handling, an
// optional cross-instance lock, and a clock a test can drive. It knows nothing of
// commands, the kernel or the dev tools; the kernel turns the app's scheduled
// commands into Tasks.
//
// Semantics, all of them deliberate and documented in COMMANDS_AND_SCHEDULER.md:
// a run that is still going when its task is due again is skipped (or allowed, per
// task); a timeout is a context deadline, and a task that ignores its context cannot
// be killed and is reported as overrun; an error or a panic is logged and the
// scheduler goes on; nothing is caught up after downtime, a restart or a suspended
// machine; "@every" counts from the moment the runner starts.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// Overlap says what happens when a task is due while its previous run is still going.
type Overlap int

const (
	// Skip drops the new run and logs it (the default).
	Skip Overlap = iota
	// Allow runs the two at once.
	Allow
)

func (o Overlap) String() string {
	if o == Allow {
		return "allow"
	}
	return "skip"
}

// Task is one scheduled unit of work.
type Task struct {
	Name    string
	Short   string // a line of description, shown by the listings
	Spec    string
	Overlap Overlap
	Timeout time.Duration // 0: none
	Run     func(ctx context.Context) error
}

// How a run ended.
const (
	StatusOK      = "ok"
	StatusError   = "error"
	StatusPanic   = "panic"
	StatusTimeout = "timeout" // the deadline passed and the task returned an error
	StatusOverrun = "overrun" // the deadline passed and the task still finished "successfully"
	StatusSkipped = "skipped" // still running, a repeated wall-clock time, or another instance holds the lock
	StatusMissed  = "missed"  // due while the process was not looking (a suspended machine, a long pause)
)

// What started a run.
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual" // "run now" on the dev page
)

// Run is the record of one run (or of a due time that did not run).
type Run struct {
	Task     string        `json:"task"`
	Trigger  string        `json:"trigger"`
	Due      time.Time     `json:"due"`     // the scheduled instant; the start time for a manual run
	Started  time.Time     `json:"started"` // zero when the run never started
	Duration time.Duration `json:"duration"`
	Status   string        `json:"status"`
	Detail   string        `json:"detail,omitempty"` // the error, or why it was skipped
	// Count is how many times in a row this record happened, when it is more than once: a
	// task that is blocked for an hour is skipped sixty times, and that is one record, so
	// the history still shows the run that blocked it.
	Count int `json:"count,omitempty"`
}

// Info is what a listing shows about a task.
type Info struct {
	Name, Short, Spec string
	Timezone          string
	Overlap           Overlap
	Timeout           time.Duration
	// Every is the interval of an "@every" task, 0 for any other.
	Every time.Duration
	// Next is the next due time. It is zero when there is none, and for an "@every"
	// task that has not been started, because it counts from the start.
	Next    time.Time
	Running int   // runs in flight
	Runs    []Run // the most recent, newest first
}

// Options configure a Runner.
type Options struct {
	Clock    Clock          // default Real
	Location *time.Location // the zone a spec is read in unless it names one; default UTC
	// Locker, when set, serialises each task across instances: a run takes the named
	// lock first and is skipped when another holder has it.
	Locker  Locker
	Log     logrus.FieldLogger // default logrus' standard logger
	History int                // runs kept per task; default 20
	// CancelGrace is how long Stop waits for the runs it has just cancelled to return
	// (default 5s). A task that ignores its context is abandoned after that.
	CancelGrace time.Duration
	// LockHold is how long past its due time a scheduled run keeps the task's lock even
	// when it has finished (default 10s). The lock is what makes one instance run a due
	// time; released at the end of a quick run, it would let a second instance, a few
	// milliseconds behind, take it and run the same due time again. A manual run does not
	// hold. It must stay under the shortest interval, a minute.
	LockHold time.Duration
}

// Errors a caller can test for.
var (
	ErrUnknownTask = errors.New("unknown task")
	ErrRunning     = errors.New("already running")
	ErrStopped     = errors.New("the scheduler is stopped")
)

// Runner owns the schedules. Build it with New (which validates every spec), arm it
// with Start, end it with Stop. RunNow and Infos work whether or not it is started.
type Runner struct {
	clk     Clock
	loc     *time.Location
	locker  Locker
	log     logrus.FieldLogger
	history int
	grace   time.Duration
	hold    time.Duration

	tasks  []*entry
	byName map[string]*entry

	mu      sync.Mutex // guards started and closing, and orders wg.Add against Stop's Wait
	started bool
	closing bool
	wg      sync.WaitGroup

	idleMu   sync.Mutex
	inFlight int           // runs started and not finished
	idle     chan struct{} // closed when inFlight falls to zero; a new one when it rises again

	runCtx    context.Context // the parent of every run; cancelled when a stop gives up waiting
	cancelRun context.CancelFunc

	holdMu  sync.Mutex
	holds   map[*lockHold]struct{} // locks kept past the end of a run, until due+hold
	holdEnd bool                   // Stop has run: nothing is held any more

	stopped   chan struct{} // closed when the first Stop has finished
	stopClean bool
}

// lockHold is a lock a finished run keeps until its hold has passed.
type lockHold struct {
	once    sync.Once
	release func()
}

func (h *lockHold) free() { h.once.Do(h.release) }

const (
	// lateTolerance is how late a timer may fire and still run its due time. A timer that
	// fires later than this (a suspended machine, a stopped process) did not wake at the
	// time: the due time is skipped and reported, never run at some other hour.
	lateTolerance = 30 * time.Second
	// firedWindow is how far back the wall-clock readings a spec has fired at are kept, to
	// recognise a reading the clocks going back brings round again.
	firedWindow = 3 * time.Hour
)

type entry struct {
	task Task
	spec *Spec

	mu      sync.Mutex
	next    time.Time
	timer   Timer
	stopped bool
	// fired holds the wall-clock readings ("2006-01-02 15:04") this task ran at lately, for
	// a spec that names its hours: when the clocks go back and the hour repeats, a reading
	// that already ran is not run again.
	fired   map[string]time.Time
	running int
	runs    []Run // oldest first, capped
}

func wallOf(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02 15:04") }

// hasFired reports whether the wall-clock reading of due already ran. The caller holds e.mu.
func (e *entry) hasFired(due time.Time) bool {
	if !e.spec.fixed {
		return false
	}
	_, ok := e.fired[wallOf(due, e.spec.loc)]
	return ok
}

// markFired records that the reading of due ran. The caller holds e.mu.
func (e *entry) markFired(due time.Time) {
	if !e.spec.fixed {
		return
	}
	if e.fired == nil {
		e.fired = map[string]time.Time{}
	}
	e.fired[wallOf(due, e.spec.loc)] = due
	for w, at := range e.fired {
		if due.Sub(at) > firedWindow {
			delete(e.fired, w)
		}
	}
}

// skipRepeats moves t past the readings that already ran. The caller holds e.mu.
func (e *entry) skipRepeats(t time.Time) time.Time {
	for i := 0; i < 200 && !t.IsZero() && e.hasFired(t); i++ {
		t = e.spec.Next(t)
	}
	return t
}

// New validates tasks and returns a runner that has armed nothing. An invalid spec, a
// repeated or empty name or a missing Run is an error that names the task; every
// problem is reported, not only the first.
func New(tasks []Task, opt Options) (*Runner, error) {
	r := &Runner{
		clk: opt.Clock, loc: opt.Location, locker: opt.Locker, log: opt.Log, history: opt.History, grace: opt.CancelGrace,
		byName: map[string]*entry{},
	}
	if r.clk == nil {
		r.clk = Real
	}
	if r.loc == nil {
		r.loc = time.UTC
	}
	if r.log == nil {
		r.log = logrus.StandardLogger()
	}
	if r.history <= 0 {
		r.history = 20
	}
	if r.grace <= 0 {
		r.grace = 5 * time.Second
	}
	r.hold = opt.LockHold
	if r.hold <= 0 {
		r.hold = 10 * time.Second
	}
	r.holds = map[*lockHold]struct{}{}
	r.stopped = make(chan struct{})
	r.runCtx, r.cancelRun = context.WithCancel(context.Background())

	var errs []error
	now := r.clk.Now()
	for _, t := range tasks {
		switch {
		case t.Name == "":
			errs = append(errs, errors.New("a scheduled task has no name"))
			continue
		case t.Run == nil:
			errs = append(errs, fmt.Errorf("command %s: no Run", t.Name))
			continue
		case r.byName[t.Name] != nil:
			errs = append(errs, fmt.Errorf("command %s: scheduled twice", t.Name))
			continue
		case t.Overlap != Skip && t.Overlap != Allow:
			errs = append(errs, fmt.Errorf("command %s: unknown overlap %d", t.Name, int(t.Overlap)))
			continue
		case t.Timeout < 0:
			errs = append(errs, fmt.Errorf("command %s: negative timeout %s", t.Name, t.Timeout))
			continue
		}
		spec, err := Parse(t.Spec, r.loc, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("command %s: %w", t.Name, err))
			continue
		}
		e := &entry{task: t, spec: spec}
		r.tasks = append(r.tasks, e)
		r.byName[t.Name] = e
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if fk, ok := r.clk.(*Fake); ok {
		fk.addSettle(r.waitIdle)
	}
	return r, nil
}

// Len is the number of tasks.
func (r *Runner) Len() int { return len(r.tasks) }

// Location is the default zone specs are read in.
func (r *Runner) Location() *time.Location { return r.loc }

// Started reports whether Start has armed the schedules and Stop has not ended them.
func (r *Runner) Started() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.started && !r.closing
}

// Start arms every schedule. "@every" counts from now. It is an error to start twice.
func (r *Runner) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing {
		return ErrStopped
	}
	if r.started {
		return errors.New("the scheduler is already started")
	}
	r.started = true
	now := r.clk.Now()
	for _, e := range r.tasks {
		e.mu.Lock()
		e.next = e.spec.Next(now)
		r.arm(e, now)
		e.mu.Unlock()
	}
	return nil
}

// arm sets the timer for e.next. The caller holds e.mu.
func (r *Runner) arm(e *entry, now time.Time) {
	if e.next.IsZero() {
		return
	}
	e.timer = r.clk.AfterFunc(e.next.Sub(now), func() { r.fire(e) })
}

// maxCatchUp bounds the loop that skips due times already in the past.
const maxCatchUp = 100000

// fire is the timer callback: it arms the next due time first, then starts the run.
func (r *Runner) fire(e *entry) {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	due, now := e.next, r.clk.Now()
	// A timer that fires long after its time did not wake at it: the machine slept or the
	// process was paused. That due time is not run late (a daily 03:00 job at noon is not
	// what anyone scheduled): it is skipped and reported, like every other one that passed.
	missed, firstMissed := 0, time.Time{}
	late := now.Sub(due) > lateTolerance
	if late {
		missed, firstMissed = 1, due
	}
	// Continue from the scheduled instant, never from wall time: no drift, and no double
	// fire when the clocks go back. Due times already past are skipped the same way.
	next := e.spec.Next(due)
	for !next.IsZero() && !next.After(now) && missed < maxCatchUp {
		if missed == 0 {
			firstMissed = next
		}
		missed++
		next = e.spec.Next(next)
	}
	if missed == maxCatchUp {
		next = e.spec.Next(now)
	}
	e.next = next
	r.arm(e, now)
	repeated := !late && e.hasFired(due)
	e.mu.Unlock()

	if missed > 0 {
		r.record(e, Run{
			Task: e.task.Name, Trigger: TriggerSchedule, Due: firstMissed, Status: StatusMissed,
			Detail: fmt.Sprintf("%d due time(s) passed while the process was not running tasks; the first was %s, and nothing is caught up", missed, firstMissed.In(e.spec.loc).Format(time.DateTime)),
		})
	}
	if late {
		return
	}
	if repeated {
		r.record(e, Run{
			Task: e.task.Name, Trigger: TriggerSchedule, Due: due, Status: StatusSkipped,
			Detail: fmt.Sprintf("the wall-clock time %s repeats (the clocks went back) and already ran", wallOf(due, e.spec.loc)),
		})
		return
	}
	// A run that is refused (the previous one is still going) did not run the reading, so the
	// repeat of it may.
	if _, err := r.dispatch(e, due, TriggerSchedule); err == nil {
		e.mu.Lock()
		e.markFired(due)
		e.mu.Unlock()
	}
}

// RunNow starts task name now, through the code path a scheduled run takes (overlap
// policy, timeout, lock, panic recovery, history). It returns at once; the channel
// delivers the run's record when it ends.
func (r *Runner) RunNow(name string) (<-chan Run, error) {
	e := r.byName[name]
	if e == nil {
		return nil, fmt.Errorf("%w %q", ErrUnknownTask, name)
	}
	return r.dispatch(e, r.clk.Now(), TriggerManual)
}

// dispatch starts one run unless the overlap policy, or a stopping runner, forbids it.
func (r *Runner) dispatch(e *entry, due time.Time, trigger string) (<-chan Run, error) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return nil, ErrStopped
	}
	e.mu.Lock()
	if e.task.Overlap == Skip && e.running > 0 {
		e.mu.Unlock()
		r.mu.Unlock()
		r.record(e, Run{Task: e.task.Name, Trigger: trigger, Due: due, Status: StatusSkipped, Detail: "the previous run is still going"})
		return nil, fmt.Errorf("task %q: %w", e.task.Name, ErrRunning)
	}
	e.running++
	e.mu.Unlock()
	r.wg.Add(1)
	r.runStarted()
	r.mu.Unlock()

	ctx, cancel := context.WithCancel(r.runCtx)
	var timedOut atomic.Bool
	var deadline Timer
	if e.task.Timeout > 0 {
		// Armed here, not in the goroutine, so a fake clock that advances past the
		// deadline right away still fires it.
		deadline = r.clk.AfterFunc(e.task.Timeout, func() { timedOut.Store(true); cancel() })
	}
	done := make(chan Run, 1)
	go r.exec(e, due, trigger, ctx, cancel, &timedOut, deadline, done)
	return done, nil
}

// exec runs the task body: lock, run, classify, record. Everything after the body is in a
// defer, so it happens even when the task ends its goroutine (runtime.Goexit: a t.FailNow
// inside a command under test): the run is counted out, the lock given back, a record kept.
func (r *Runner) exec(e *entry, due time.Time, trigger string, ctx context.Context, cancel context.CancelFunc, timedOut *atomic.Bool, deadline Timer, done chan<- Run) {
	defer r.wg.Done()
	defer r.runEnded()
	run := Run{Task: e.task.Name, Trigger: trigger, Due: due, Started: r.clk.Now()}
	r.log.WithFields(logrus.Fields{"task": run.Task, "trigger": trigger}).Debug("task started")

	var stack []byte
	var release func()
	var locked, finished bool
	defer func() {
		if !finished {
			run.Status, run.Detail = StatusError, "the task ended its goroutine without returning (runtime.Goexit, for example t.FailNow called from a command)"
		}
		if deadline != nil {
			deadline.Stop()
		}
		cancel()
		run.Duration = r.clk.Now().Sub(run.Started)
		switch {
		case run.Status == StatusPanic || run.Status == StatusSkipped:
		case timedOut.Load() && run.Status == StatusOK:
			run.Status, run.Detail = StatusOverrun, fmt.Sprintf("returned success after its %s timeout: the task ignored its context, or swallowed the cancellation", e.task.Timeout)
		case timedOut.Load():
			run.Status = StatusTimeout
			run.Detail = fmt.Sprintf("stopped after its %s timeout: %s", e.task.Timeout, run.Detail)
		}
		if locked {
			r.finishLock(release, due, trigger)
		}
		e.mu.Lock()
		e.running--
		e.mu.Unlock()
		r.record(e, run, stack)
		done <- run
	}()

	var lerr error
	release, locked, lerr = r.lock(ctx, e.task.Name)
	switch {
	case lerr != nil:
		run.Status, run.Detail = StatusError, "lock: "+lerr.Error()
		finished = true
	case !locked:
		run.Status, run.Detail = StatusSkipped, "another instance holds the lock (scheduler.lock: db)"
		finished = true
	default:
		func() {
			defer func() {
				if p := recover(); p != nil {
					run.Status, run.Detail, stack = StatusPanic, fmt.Sprintf("panic: %v", p), debug.Stack()
					finished = true
				}
			}()
			run.Status = StatusOK
			if err := e.task.Run(ctx); err != nil {
				run.Status, run.Detail = StatusError, err.Error()
			}
			finished = true
		}()
	}
}

// finishLock gives the lock back: at once for a manual run or when no hold is wanted, else
// when due+hold has passed, so that a second instance a few milliseconds behind finds the
// due time taken instead of running it again.
func (r *Runner) finishLock(release func(), due time.Time, trigger string) {
	remaining := time.Duration(0)
	if trigger == TriggerSchedule {
		remaining = due.Add(r.hold).Sub(r.clk.Now())
	}
	if remaining <= 0 {
		release()
		return
	}
	h := &lockHold{release: release}
	r.holdMu.Lock()
	if r.holdEnd {
		r.holdMu.Unlock()
		release()
		return
	}
	r.holds[h] = struct{}{}
	r.holdMu.Unlock()
	r.clk.AfterFunc(remaining, func() {
		h.free()
		r.holdMu.Lock()
		delete(r.holds, h)
		r.holdMu.Unlock()
	})
}

// releaseHolds gives back every lock still held past the end of its run, and from then on
// nothing is held.
func (r *Runner) releaseHolds() {
	r.holdMu.Lock()
	r.holdEnd = true
	held := make([]*lockHold, 0, len(r.holds))
	for h := range r.holds {
		held = append(held, h)
	}
	r.holds = map[*lockHold]struct{}{}
	r.holdMu.Unlock()
	for _, h := range held {
		h.free()
	}
}

// lock takes the task's cross-instance lock when a Locker is configured. With none it
// is a no-op that always succeeds.
func (r *Runner) lock(ctx context.Context, name string) (release func(), ok bool, err error) {
	if r.locker == nil {
		return func() {}, true, nil
	}
	release, ok, err = r.locker.TryLock(ctx, name)
	if release == nil {
		release = func() {}
	}
	return release, ok, err
}

// record keeps the run in the task's history and writes its log line.
func (r *Runner) record(e *entry, run Run, stack ...[]byte) {
	e.mu.Lock()
	if n := len(e.runs); n > 0 && (run.Status == StatusSkipped || run.Status == StatusMissed) {
		// The same refusal again is one record, counted: a task blocked for an hour is skipped
		// sixty times, and sixty records would push the run that blocked it out of the history.
		if last := &e.runs[n-1]; last.Status == run.Status && last.Trigger == run.Trigger && last.Detail == run.Detail {
			last.Count = max(last.Count, 1) + 1
			last.Due, last.Started, last.Duration = run.Due, run.Started, run.Duration
			e.mu.Unlock()
			r.logRecord(e, run, stack)
			return
		}
	}
	e.runs = append(e.runs, run)
	if len(e.runs) > r.history {
		e.runs = append([]Run(nil), e.runs[len(e.runs)-r.history:]...)
	}
	e.mu.Unlock()
	r.logRecord(e, run, stack)
}

// logRecord writes the structured log line of a run.
func (r *Runner) logRecord(e *entry, run Run, stack [][]byte) {
	l := r.log.WithFields(logrus.Fields{
		"task": run.Task, "trigger": run.Trigger, "status": run.Status, "duration": run.Duration.Round(time.Millisecond).String(),
	})
	if run.Detail != "" {
		l = l.WithField("detail", run.Detail)
	}
	switch run.Status {
	case StatusOK:
		l.Info("task finished")
	case StatusSkipped, StatusMissed:
		l.Warn("task not run")
	case StatusOverrun:
		l.Warn("task overran its timeout")
	default:
		if len(stack) > 0 && len(stack[0]) > 0 {
			l = l.WithField("stack", string(stack[0]))
		}
		l.Error("task failed")
	}
}

// Infos describes every task, from the given time: the next due time of a started
// task is the armed one, of a task not started it is computed from the declaration.
func (r *Runner) Infos(from time.Time) []Info {
	out := make([]Info, 0, len(r.tasks))
	for _, e := range r.tasks {
		e.mu.Lock()
		info := Info{
			Name: e.task.Name, Short: e.task.Short, Spec: e.spec.Expr, Timezone: e.spec.loc.String(),
			Overlap: e.task.Overlap, Timeout: e.task.Timeout, Running: e.running,
		}
		if d, ok := e.spec.Every(); ok {
			info.Every = d
		}
		switch {
		case !e.next.IsZero():
			info.Next = e.skipRepeats(e.next)
		case info.Every == 0:
			if ups := e.spec.Upcoming(from, 1); len(ups) > 0 {
				info.Next = ups[0]
			}
		}
		for i := len(e.runs) - 1; i >= 0; i-- {
			info.Runs = append(info.Runs, e.runs[i])
		}
		e.mu.Unlock()
		out = append(out, info)
	}
	return out
}

// Upcoming is the next n due times of a task after from, computed from its declaration
// alone: no clock, no engine, nothing started. For an "@every" task they are from+k*every.
func (r *Runner) Upcoming(name string, from time.Time, n int) ([]time.Time, error) {
	e := r.byName[name]
	if e == nil {
		return nil, fmt.Errorf("%w %q", ErrUnknownTask, name)
	}
	return e.spec.Upcoming(from, n), nil
}

// runStarted counts a run in; the first one in flight opens a fresh idle channel.
func (r *Runner) runStarted() {
	r.idleMu.Lock()
	if r.inFlight == 0 {
		r.idle = make(chan struct{})
	}
	r.inFlight++
	r.idleMu.Unlock()
}

// runEnded counts a run out; the last one wakes whoever waits in waitIdle.
func (r *Runner) runEnded() {
	r.idleMu.Lock()
	r.inFlight--
	if r.inFlight == 0 {
		close(r.idle)
	}
	r.idleMu.Unlock()
}

// waitIdle blocks, in real time, until no run is in flight or the budget is spent. A
// Fake calls it between timers. It waits on the idle channel, not by polling: a run
// that takes no virtual time wakes it at once, so advancing a fake clock over
// thousands of due times costs microseconds each, on a slow CI runner too.
func (r *Runner) waitIdle(budget time.Duration) {
	var deadline <-chan time.Time
	for {
		r.idleMu.Lock()
		if r.inFlight == 0 {
			r.idleMu.Unlock()
			return
		}
		idle := r.idle
		r.idleMu.Unlock()
		if deadline == nil {
			t := time.NewTimer(budget)
			defer t.Stop()
			deadline = t.C
		}
		select {
		case <-idle:
		case <-deadline:
			return
		}
	}
}

// Stop ends the schedules (nothing new starts) and waits up to timeout, on the
// runner's clock, for the runs in flight to finish. When that wait ends with runs still
// going it cancels their contexts, gives them a short grace (Options.CancelGrace) to
// notice and return, and reports false either way: the process is about to exit, and a
// task that ignores its context cannot be waited for any longer. It works on a runner that
// was never started (it then waits for manual runs), and a second Stop, from another
// goroutine or later, waits for the first and returns its answer.
func (r *Runner) Stop(timeout time.Duration) (clean bool) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		<-r.stopped
		return r.stopClean
	}
	r.closing = true
	r.mu.Unlock()
	clean = r.stop(timeout)
	r.stopClean = clean
	close(r.stopped)
	return clean
}

func (r *Runner) stop(timeout time.Duration) bool {
	for _, e := range r.tasks {
		e.mu.Lock()
		e.stopped = true
		if e.timer != nil {
			e.timer.Stop()
		}
		e.mu.Unlock()
	}
	idle := make(chan struct{})
	go func() { r.wg.Wait(); close(idle) }()
	select {
	case <-idle:
		r.cancelRun()
		r.releaseHolds()
		return true
	case <-r.clk.After(timeout):
	}
	r.cancelRun()
	select {
	case <-idle:
	case <-r.clk.After(r.grace):
	}
	r.releaseHolds()
	return false
}
