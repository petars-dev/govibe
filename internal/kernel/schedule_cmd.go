package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/scheduler"
)

// runSchedule is `schedule list|run`.
func runSchedule(ctx context.Context, app App, args []string, out, errOut io.Writer) error {
	const usage = "usage: schedule list [--json] [--next N] | schedule run"
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "list":
		return scheduleList(ctx, app, args[1:], out, errOut)
	case "run":
		return scheduleRun(ctx, app, args[1:])
	}
	return fmt.Errorf("unknown schedule command %q\n%s", args[0], usage)
}

// scheduleDoc is `schedule list --json`.
type scheduleDoc struct {
	Scheduler struct {
		Enabled  bool   `json:"enabled"`
		Timezone string `json:"timezone"`
		Lock     string `json:"lock"`
	} `json:"scheduler"`
	Commands int            `json:"commands"`
	Tasks    []scheduleTask `json:"tasks"`
}

type scheduleTask struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schedule    string `json:"schedule"`
	Timezone    string `json:"timezone"`
	Overlap     string `json:"overlap"`
	Timeout     string `json:"timeout,omitempty"`
	// Every is the interval of an "@every" task. Such a task counts from the start of the
	// process, so it has no next run to compute and Next is null.
	Every    string      `json:"every,omitempty"`
	Next     *time.Time  `json:"next"`
	NextRuns []time.Time `json:"next_runs"`
}

// scheduleList prints every scheduled command, computed from the declarations and the
// current configuration: no database, no dev tools, no scheduler, and no question put to
// a running server, so it works in production.
func scheduleList(ctx context.Context, app App, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("schedule list", flag.ContinueOnError)
	fs.SetOutput(errOut)
	asJSON := fs.Bool("json", false, "print a JSON document")
	next := fs.Int("next", 1, "show the next N due times of every task")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("schedule list takes no arguments, got %q", fs.Args())
	}
	if *next < 1 {
		return errors.New("--next must be at least 1")
	}
	b, err := Build(ctx, app, Options{Quiet: true, Clock: clock})
	if err != nil {
		return err
	}
	defer b.Close()
	set := b.SchedulerSettings
	now := clock.Now()

	var doc scheduleDoc
	doc.Scheduler.Enabled, doc.Scheduler.Timezone, doc.Scheduler.Lock = set.Enabled, set.Loc.String(), set.Lock
	doc.Commands = len(b.Commands)
	doc.Tasks = []scheduleTask{}
	if b.Scheduler != nil {
		for _, in := range b.Scheduler.Infos(now) {
			t := scheduleTask{
				Name: in.Name, Description: in.Short, Schedule: in.Spec, Timezone: in.Timezone,
				Overlap: in.Overlap.String(), NextRuns: []time.Time{},
			}
			if in.Timeout > 0 {
				t.Timeout = in.Timeout.String()
			}
			if in.Every > 0 {
				t.Every = in.Every.String()
			} else {
				runs, err := b.Scheduler.Upcoming(in.Name, now, *next)
				if err != nil {
					return err
				}
				t.NextRuns = runs
				if len(runs) > 0 {
					t.Next = &runs[0]
				}
			}
			doc.Tasks = append(doc.Tasks, t)
		}
	}

	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if len(doc.Tasks) > 0 {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSCHEDULE\tTIMEZONE\tNEXT RUN\tOVERLAP\tTIMEOUT\tDESCRIPTION")
		for _, t := range doc.Tasks {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, t.Schedule, t.Timezone, nextCell(t), t.Overlap, timeoutCell(t), t.Description)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if *next > 1 {
		for _, t := range doc.Tasks {
			fmt.Fprintf(out, "\nnext %d runs of %s:\n", *next, t.Name)
			if t.Every != "" {
				fmt.Fprintf(out, "  every %s, counted from the start of the process\n", scheduler.ShortDuration(mustDuration(t.Every)))
				continue
			}
			for _, r := range t.NextRuns {
				fmt.Fprintf(out, "  %s\n", r.Format(time.DateTime))
			}
		}
		fmt.Fprintln(out)
	}
	state := "Scheduler enabled"
	if !set.Enabled {
		state = "Scheduler disabled (scheduler.enabled is false: serve does not run them)"
	}
	lock := ""
	if set.Lock == "db" {
		lock = "; lock db"
	}
	if len(doc.Tasks) == 0 {
		fmt.Fprintf(out, "No command declares a Schedule (%d command(s)). %s; timezone %s%s.\n", doc.Commands, state, set.Loc, lock)
		return nil
	}
	fmt.Fprintf(out, "%d scheduled of %d commands. %s; timezone %s%s.\n", len(doc.Tasks), doc.Commands, state, set.Loc, lock)
	return nil
}

func nextCell(t scheduleTask) string {
	if t.Every != "" {
		return scheduler.ShortDuration(mustDuration(t.Every)) + " after start"
	}
	if t.Next == nil {
		return "-"
	}
	return t.Next.Format(time.DateTime)
}

func timeoutCell(t scheduleTask) string {
	if t.Timeout == "" {
		return "-"
	}
	return scheduler.ShortDuration(mustDuration(t.Timeout))
}

func mustDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

// scheduleRun is `schedule run`: the scheduler alone, in the foreground, until SIGINT or
// SIGTERM. It is the process for a worker: no HTTP server, no dev tools, no migrations
// (like any command, it expects `serve` or `migrate up` to have prepared the schema).
func scheduleRun(ctx context.Context, app App, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("schedule run takes no arguments, got %q", args)
	}
	b, err := Build(ctx, app, Options{DB: true, Clock: clock})
	if err != nil {
		return err
	}
	defer b.Close()
	switch {
	case b.Scheduler == nil:
		return errors.New("no command declares a Schedule, so there is nothing to run")
	case !b.SchedulerSettings.Enabled:
		return errors.New("scheduler.enabled is false, so `schedule run` has nothing to start")
	}
	if _, err := b.StartScheduler(false); err != nil {
		return err
	}
	logrus.Info("running the scheduler only (no HTTP server); stop with SIGINT or SIGTERM")
	<-ctx.Done()
	b.StopScheduler()
	return nil
}
