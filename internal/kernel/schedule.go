package kernel

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// Overlap says what happens when a scheduled command is due while its previous run is
// still going.
type Overlap = scheduler.Overlap

const (
	// Skip drops the new run and logs it. It is the default.
	Skip = scheduler.Skip
	// Allow runs the two at once.
	Allow = scheduler.Allow
)

// Schedule puts a Command on a schedule: the command is then also run by the
// scheduler, with the flags' defaults and no arguments.
type Schedule struct {
	// Spec is a five-field cron expression ("*/5 * * * *"), a descriptor (@hourly,
	// @daily, @weekly, @monthly, @yearly) or "@every <duration>" (at least 1m), with an
	// optional "CRON_TZ=<zone>" prefix ("CRON_TZ=Europe/Sofia 0 9 * * *").
	Spec string
	// Overlap is Skip (the default) or Allow.
	Overlap Overlap
	// Timeout ends a run's context after this long; 0 is none.
	Timeout time.Duration
}

// Cron is a Schedule for a cron expression or descriptor, so a declaration reads on one line.
func Cron(spec string) *Schedule { return &Schedule{Spec: spec} }

// Every is a Schedule that runs the command every d (at least a minute), counted from
// the moment the scheduler starts.
func Every(d time.Duration) *Schedule { return &Schedule{Spec: "@every " + scheduler.ShortDuration(d)} }

// schedulerSettings is the scheduler.* configuration, validated.
type schedulerSettings struct {
	Enabled         bool
	Loc             *time.Location
	ShutdownTimeout time.Duration
	Lock            string // "none" or "db"
}

// readSchedulerSettings validates the scheduler.* keys: a bad zone, lock or timeout is a
// boot error that names the key.
func readSchedulerSettings(cfg config.Config) (schedulerSettings, error) {
	s := schedulerSettings{Enabled: cfg.Scheduler.Enabled, ShutdownTimeout: cfg.Scheduler.ShutdownTimeout, Lock: strings.ToLower(strings.TrimSpace(cfg.Scheduler.Lock))}
	loc, err := time.LoadLocation(strings.TrimSpace(cfg.Scheduler.Timezone))
	if err != nil {
		return s, fmt.Errorf("scheduler.timezone: unknown time zone %q (use an IANA name such as UTC or Europe/Sofia)", cfg.Scheduler.Timezone)
	}
	s.Loc = loc
	switch s.Lock {
	case "", "none":
		s.Lock = "none"
	case "db":
	default:
		return s, fmt.Errorf("scheduler.lock: %q is not one of none, db", cfg.Scheduler.Lock)
	}
	if s.ShutdownTimeout < 0 {
		return s, fmt.Errorf("scheduler.shutdown_timeout: %s is negative", s.ShutdownTimeout)
	}
	return s, nil
}

// buildScheduler is step 5 of the boot for the scheduled commands: it validates the
// settings and every command's Schedule (an invalid one is a boot error that names the
// command), and builds a runner that arms nothing. The caller starts it.
func (b *Built) buildScheduler(clk scheduler.Clock) error {
	set, err := readSchedulerSettings(b.Rt.Config)
	if err != nil {
		return err
	}
	b.SchedulerSettings = set
	var tasks []scheduler.Task
	for _, c := range b.Commands {
		if c.Schedule == nil {
			continue
		}
		tasks = append(tasks, scheduler.Task{
			Name: c.Use, Short: c.Short, Spec: c.Schedule.Spec,
			Overlap: c.Schedule.Overlap, Timeout: c.Schedule.Timeout,
			Run: b.scheduledRun(c),
		})
	}
	if len(tasks) == 0 {
		return nil
	}
	var locker scheduler.Locker
	if set.Lock == "db" && b.Rt.DB != nil {
		locker = scheduler.DBLocker{DB: b.Rt.DB.DB, Prefix: b.App.Name}
	}
	r, err := scheduler.New(tasks, scheduler.Options{Clock: clk, Location: set.Loc, Locker: locker, Log: b.Rt.Log})
	if err != nil {
		return err
	}
	b.Scheduler = r
	return nil
}

// scheduledRun is what the scheduler (and "run now") calls for a command: the same Run
// the CLI calls, with the flags' defaults and no arguments.
func (b *Built) scheduledRun(c Command) func(context.Context) error {
	// The command's flags are declared and parsed, with no arguments, once: every run sees
	// their defaults. Declaring them again on each run would rewrite the variables they bind
	// while an earlier run (overlap Allow) may still be reading them.
	if c.Flags != nil {
		fs := flag.NewFlagSet(c.Use, flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c.Flags(fs)
		_ = fs.Parse(nil)
	}
	return func(ctx context.Context) error {
		return c.Run(ctx, b.Rt, nil)
	}
}

// runCommand parses args against the command's flags and runs it.
func runCommand(ctx context.Context, rt *Runtime, c Command, args []string, errOut io.Writer) error {
	fs := flag.NewFlagSet(c.Use, flag.ContinueOnError)
	fs.SetOutput(errOut)
	if c.Flags != nil {
		c.Flags(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return c.Run(ctx, rt, fs.Args())
}

// scheduled is the number of commands that carry a Schedule.
func (b *Built) scheduled() int {
	n := 0
	for _, c := range b.Commands {
		if c.Schedule != nil {
			n++
		}
	}
	return n
}

// StartScheduler arms the scheduled commands and says whether it did. It starts nothing
// when no command is scheduled, when scheduler.enabled is false or when off is set
// (`serve --no-schedule`), and it says why in the log. A lock of "db" with no database
// is an error: it would run every task on every instance, which is what the lock is for
// preventing.
func (b *Built) StartScheduler(off bool) (bool, error) {
	if b.Scheduler == nil {
		return false, nil
	}
	set := b.SchedulerSettings
	switch {
	case off:
		logrus.Infof("scheduler not started: serve --no-schedule (%d scheduled command(s) will not run here)", b.scheduled())
		return false, nil
	case !set.Enabled:
		logrus.Infof("scheduler not started: scheduler.enabled is false (%d scheduled command(s) will not run here)", b.scheduled())
		return false, nil
	case set.Lock == "db" && b.Rt.DB == nil:
		return false, errors.New("scheduler.lock is db but no database is configured (MARIADB_DSN)")
	}
	if err := b.Scheduler.Start(); err != nil {
		return false, err
	}
	lock := "none: every instance of this app runs every due task, so run the scheduler on one instance (serve --no-schedule on the others) or set scheduler.lock: db"
	if set.Lock == "db" {
		lock = "db: one named MariaDB lock per task, so only one instance runs a task at a time"
	}
	logrus.Infof("scheduler started: %d task(s), timezone %s, lock %s", b.scheduled(), set.Loc, lock)
	return true, nil
}

// WithoutLock turns the cross-instance lock off for this process, whatever
// scheduler.lock says. A test boots with no database, so a lock of "db" could only fail.
func (b *Built) WithoutLock() { b.SchedulerSettings.Lock = "none" }

// StopScheduler ends the schedules and waits up to scheduler.shutdown_timeout for the
// runs in flight, then cancels what is left. It is a no-op when nothing was started.
func (b *Built) StopScheduler() {
	if b.Scheduler == nil {
		return
	}
	// Also for a scheduler that was never started: a manual run ("Run now" under
	// serve --no-schedule) is still waited for, so the database is not closed under it.
	started := b.Scheduler.Started()
	clean := b.Scheduler.Stop(b.SchedulerSettings.ShutdownTimeout)
	switch {
	case clean && started:
		logrus.Info("scheduler stopped")
		return
	case clean:
		return
	}
	logrus.Warnf("scheduler: tasks were still running after scheduler.shutdown_timeout (%s); their contexts were cancelled", b.SchedulerSettings.ShutdownTimeout)
}
