package devmcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
)

type scheduleIn struct {
	Name string `json:"name,omitempty" jsonschema:"only this scheduled command; omit for all of them"`
}

// registerScheduleTools adds schedule_status. status reads the scheduler as it is now
// (the same document /dev/schedule/data serves); nil means the process has none, and
// the tool then says so and how to declare one.
func registerScheduleTools(srv *mcp.Server, status func() schedview.Status) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "schedule_status",
		Description: "Say what this app has scheduled and how it is going: the scheduler's state (running; not started in this process, e.g. serve --no-schedule; " +
			"disabled by scheduler.enabled; or nothing scheduled), its timezone and cross-instance lock, and for every command that declares a Schedule its spec, " +
			"timezone, next run, overlap policy (skip or allow), timeout, the runs in flight and its last 20 runs (status ok, error, panic, timeout, overrun, skipped or missed; " +
			"trigger schedule or manual; due time, duration and the error). History is kept in memory for the life of this process, so it starts empty after a restart. " +
			"Read-only: it never runs a command (the dev page /dev/schedule has Run now, and `./app <name>` runs one by hand). " +
			"`name` limits the answer to one command. With nothing scheduled it says how to declare a Schedule. " +
			"The same facts as /dev/schedule/data; `./app schedule list` is the offline version, computed from the declarations alone.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in scheduleIn) (*mcp.CallToolResult, schedview.Status, error) {
		var st schedview.Status
		if status != nil {
			st = status()
		} else {
			st = schedview.Build(nil, config.Config{}, 0, time.Now())
		}
		if in.Name == "" {
			return nil, st, nil
		}
		var known []string
		for _, t := range st.Tasks {
			if t.Name == in.Name {
				st.Tasks = []schedview.Task{t}
				return nil, st, nil
			}
			known = append(known, t.Name)
		}
		if len(known) == 0 {
			return nil, schedview.Status{}, fmt.Errorf("no command is scheduled, so there is no %q: declare a Schedule on a command first", in.Name)
		}
		return nil, schedview.Status{}, fmt.Errorf("no scheduled command named %q (scheduled: %s)", in.Name, strings.Join(known, ", "))
	})
}
