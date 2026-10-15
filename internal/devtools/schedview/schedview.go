// Package schedview is what the dev tools say about the scheduler: one document, built
// from the scheduler's own facts, that the /dev/schedule page, its JSON and the
// schedule_status MCP tool all show, so the three cannot drift apart. It reads the
// scheduler through a port it declares; it imports neither the kernel nor the pages.
package schedview

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// Source is what the dev tools need from the scheduler. *scheduler.Runner is the
// implementation; a process with no scheduled command has none (a nil Source).
type Source interface {
	Infos(from time.Time) []scheduler.Info
	// RunNow starts a task through the code path a scheduled run takes.
	RunNow(name string) (<-chan scheduler.Run, error)
	Started() bool
}

// Normalize turns a nil pointer wrapped in a Source (a *scheduler.Runner that is nil
// because no command is scheduled, handed over as an interface) into no Source at all: an
// interface holding a nil pointer is not == nil, and would be asked for tasks.
func Normalize(src Source) Source {
	if src == nil {
		return nil
	}
	if v := reflect.ValueOf(src); v.Kind() == reflect.Pointer && v.IsNil() {
		return nil
	}
	return src
}

// States of the scheduler in this process.
const (
	StateRunning  = "running"  // armed: tasks run at their due times
	StateStopped  = "stopped"  // scheduled commands exist, but this process did not start the scheduler
	StateDisabled = "disabled" // scheduler.enabled is false
	StateNone     = "none"     // no command declares a Schedule
)

// Declare is the one line that puts a command on a schedule, shown wherever none is.
const Declare = `{Use: "items:sync", Short: "Sync the items", Schedule: govibe.Cron("0 2 * * *"), Run: commands.SyncItems(svc)}`

// Status is the scheduler as the dev tools show it.
type Status struct {
	State string `json:"state"`
	// Reason says, in a sentence, why the scheduler is not running here; empty when it is.
	Reason   string `json:"reason,omitempty"`
	Timezone string `json:"timezone"`
	Lock     string `json:"lock"`
	// Commands counts every command of the app; Scheduled those with a Schedule.
	Commands  int       `json:"commands"`
	Scheduled int       `json:"scheduled"`
	Now       time.Time `json:"now"`
	Tasks     []Task    `json:"tasks"`
	// HowToDeclare is how to schedule a command, set when none is scheduled.
	HowToDeclare string `json:"how_to_declare,omitempty"`
}

// Task is one scheduled command.
type Task struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone"`
	Overlap     string `json:"overlap"`
	Timeout     string `json:"timeout,omitempty"`
	// Every is the interval of an "@every" task, which counts from the start of the
	// scheduler: it has a Next only while the scheduler runs.
	Every string `json:"every,omitempty"`
	// Next is the next due time, in the task's zone; nil when there is none to say.
	Next *time.Time `json:"next"`
	// NextIn is Next relative to Now ("in 3h12m"); NextNote explains a missing Next
	// ("6h after start").
	NextIn   string `json:"next_in,omitempty"`
	NextNote string `json:"next_note,omitempty"`
	// Running is the number of runs in flight.
	Running int `json:"running"`
	// Runs are the last runs, newest first.
	Runs []Run `json:"runs"`
}

// Run is one run (or one due time that did not run).
type Run struct {
	Trigger string    `json:"trigger"` // "schedule" or "manual"
	Status  string    `json:"status"`  // ok, error, panic, timeout, overrun, skipped, missed
	Due     time.Time `json:"due"`
	Started time.Time `json:"started,omitzero"`
	// Duration is how long the run took, "" when it never started.
	Duration string `json:"duration,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Count is how many times in a row this record happened, when more than once: a task that
	// is blocked is skipped on every due time, and that is one record.
	Count int `json:"count,omitempty"`
}

// Build reads the scheduler at time now. src may be nil (no scheduled command). commands
// is the number of commands the app declares, scheduled or not (0 when unknown).
func Build(src Source, cfg config.Config, commands int, now time.Time) Status {
	st := Status{
		Timezone: cfg.Scheduler.Timezone, Lock: strings.ToLower(strings.TrimSpace(cfg.Scheduler.Lock)),
		Commands: commands, Now: now, Tasks: []Task{},
	}
	if st.Timezone == "" {
		st.Timezone = "UTC"
	}
	st.Now = inZone(now, st.Timezone)
	if st.Lock == "" {
		st.Lock = "none"
	}
	if src == nil {
		st.State, st.HowToDeclare = StateNone, "Declare a Schedule on a command in routes/console.go: "+Declare
		return st
	}
	started := src.Started()
	for _, in := range src.Infos(now) {
		st.Tasks = append(st.Tasks, task(in, now, started))
	}
	st.Scheduled = len(st.Tasks)
	if st.Commands < st.Scheduled {
		st.Commands = st.Scheduled
	}
	switch {
	case st.Scheduled == 0:
		st.State, st.HowToDeclare = StateNone, "Declare a Schedule on a command in routes/console.go: "+Declare
	case !cfg.Scheduler.Enabled:
		st.State = StateDisabled
		st.Reason = "scheduler.enabled is false, so serve does not start the scheduler: nothing runs at its due time in this process (\"Run now\" still works)"
	case !started:
		st.State = StateStopped
		st.Reason = "the scheduler was not started in this process (serve --no-schedule): nothing runs at its due time here; schedule run runs it in a worker process"
	default:
		st.State = StateRunning
	}
	return st
}

// inZone shows t in the named zone, so every time in the document reads in one: a run the
// scheduler started is in the task's zone, a manual one in the machine's.
func inZone(t time.Time, zone string) time.Time {
	if t.IsZero() {
		return t
	}
	if loc, err := time.LoadLocation(zone); err == nil {
		return t.In(loc)
	}
	return t
}

func task(in scheduler.Info, now time.Time, started bool) Task {
	loc := time.UTC
	if l, err := time.LoadLocation(in.Timezone); err == nil {
		loc = l
	}
	t := Task{
		Name: in.Name, Description: in.Short, Schedule: in.Spec, Timezone: in.Timezone,
		Overlap: in.Overlap.String(), Running: in.Running, Runs: []Run{},
	}
	if in.Timeout > 0 {
		t.Timeout = scheduler.ShortDuration(in.Timeout)
	}
	if in.Every > 0 {
		t.Every = scheduler.ShortDuration(in.Every)
	}
	switch {
	case !in.Next.IsZero():
		next := in.Next.In(loc)
		t.Next = &next
		t.NextIn = Until(now, next)
	case in.Every > 0 && !started:
		t.NextNote = scheduler.ShortDuration(in.Every) + " after start"
	default:
		t.NextNote = "no more runs"
	}
	for _, r := range in.Runs {
		run := Run{Trigger: r.Trigger, Status: r.Status, Due: r.Due.In(loc), Started: r.Started.In(loc), Detail: r.Detail, Count: r.Count}
		if !r.Started.IsZero() {
			run.Duration = r.Duration.Round(time.Millisecond).String()
		}
		t.Runs = append(t.Runs, run)
	}
	return t
}

// Until says how long from now a time is: "in 3h12m", "in 45s", "now". It rounds to the
// nearest second, then to the nearest minute once a minute has passed, and never says
// "in 60s" or "in 60m": 59.6 seconds is "in 1m" and 59m40s is "in 1h".
func Until(now, t time.Time) string {
	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}
	secs := max(int((d+500*time.Millisecond)/time.Second), 1)
	if secs < 60 {
		return fmt.Sprintf("in %ds", secs)
	}
	mins := (secs + 30) / 60
	switch {
	case mins < 60:
		return fmt.Sprintf("in %dm", mins)
	case mins < 48*60:
		if h, m := mins/60, mins%60; m != 0 {
			return fmt.Sprintf("in %dh%02dm", h, m)
		} else {
			return fmt.Sprintf("in %dh", h)
		}
	}
	days, hours := mins/(24*60), mins%(24*60)/60
	if hours != 0 {
		return fmt.Sprintf("in %dd%dh", days, hours)
	}
	return fmt.Sprintf("in %dd", days)
}
