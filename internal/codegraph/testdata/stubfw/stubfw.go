// Package stubfw stands in for the framework in the indexer's tests: a module the
// fixture application depends on, with the same shape (a root package with the App
// declaration, public helper packages, an internal package), so the graph can be
// tested against a project that is built on a framework without building the real one.
// It compiles but is never run.
package stubfw

import (
	"context"
	"net/http"
	"time"

	"example.com/stubfw/internal/kernel"
)

// App declares what an application contributes.
type App struct {
	Name     string
	Handler  http.Handler
	Commands func() []Command
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
