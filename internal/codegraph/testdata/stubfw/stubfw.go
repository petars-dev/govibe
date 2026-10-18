// Package stubfw stands in for the framework in the indexer's tests: a module the
// fixture application depends on, with the same shape (a root package with the App
// declaration, public helper packages, an internal package), so the graph can be
// tested against a project that is built on a framework without building the real one.
// It compiles but is never run.
package stubfw

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"example.com/stubfw/internal/kernel"
)

// App declares what an application contributes.
type App struct {
	Name     string
	Handler  http.Handler
	Commands func() []Command
	DevTools func(rt *Runtime, dev *Dev) error
}

// Dev collects what an application adds to the dev panel.
type Dev struct {
	tools []DevTool
	pages []string
}

// Tool adds an MCP tool to the dev server.
func (d *Dev) Tool(t DevTool) { d.tools = append(d.tools, t) }

// Page adds a page under /dev/app.
func (d *Dev) Page(path, title string, h http.Handler) { d.pages = append(d.pages, path) }

// DevTool is an MCP tool of the application's own.
type DevTool struct {
	Name, Description string
	Input             any
	Run               func(ctx context.Context, args json.RawMessage) (any, error)
}

// DevToolFunc is a DevTool with typed arguments and result.
func DevToolFunc[In, Out any](name, description string, run func(ctx context.Context, in In) (Out, error)) DevTool {
	var zero In
	return DevTool{Name: name, Description: description, Input: zero, Run: func(ctx context.Context, args json.RawMessage) (any, error) {
		var in In
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, err
			}
		}
		return run(ctx, in)
	}}
}

// Runtime is what the framework hands a command.
type Runtime struct{}

// RunFunc is the body of a command.
type RunFunc func(ctx context.Context, rt *Runtime, args []string) error

// Command is one CLI command of the application, which may be on a schedule.
type Command struct {
	Use, Short string
	Run        RunFunc
	Schedule   *Schedule
}

// Schedule puts a command on a schedule.
type Schedule struct {
	Spec    string
	Timeout time.Duration
}

// Cron is a schedule for a cron expression.
func Cron(spec string) *Schedule { return &Schedule{Spec: spec} }

// Every is a schedule that runs a command at an interval.
func Every(d time.Duration) *Schedule { return &Schedule{Spec: "@every " + d.String()} }

// Main runs an application.
func Main(app App) { kernel.Run(app.Name, app.Handler) }
