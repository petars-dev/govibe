// Package govibe is the GoVibe web framework. An application is a declaration: it
// hands the framework what it contributes (its settings, migrations, static files,
// routes and commands) and the framework runs it.
//
//	//go:embed env.yml
//	var configFS embed.FS
//
//	func main() {
//		govibe.Main(govibe.App{
//			Name:       "shop",
//			Config:     configFS,
//			Migrations: migrations.FS,
//			Assets:     assets.FS,
//			Routes:     routes.Register,
//		})
//	}
//
// The framework never imports the application, and an application imports only
// this package, config, web and (in tests) govibetest.
//
// The types here are aliases of the framework's boot package, so the one
// definition serves both: the fields of App, Runtime and Command are documented on
// the declarations below, not in the alias list.
package govibe

import (
	"context"
	"io"
	"time"

	"github.com/petars-dev/govibe/internal/devext"
	"github.com/petars-dev/govibe/internal/kernel"
)

// App declares what an application contributes. Every field is optional.
//
//	Name       string                                // logs and tooling titles
//	Config     fs.FS                                 // its root holds env.yml, merged over the framework defaults
//	Migrations fs.FS                                 // holds sql/*.sql (goose)
//	Assets     fs.FS                                 // static files, served at /assets/*
//	Routes     func(rt *Runtime, r chi.Router) error // the app's HTTP surface
//	Commands   func(rt *Runtime) []Command           // the app's CLI commands, some of them scheduled
//	Shutdown   func(ctx context.Context) error       // called after the server has stopped
//	DevTools   func(rt *Runtime, dev *Dev) error     // the app's own dev tools and pages (govibe_dev builds)
//
// Routes and Commands must not open connections or read secrets while they
// register: tooling calls them with no database (rt.DB is nil) to list what exists.
//
// DevTools is called only when the dev tools are mounted: a govibe_dev build outside
// production. Keep it in a file with the govibe_dev build tag and hand it over through
// a variable, so a production binary holds none of it:
//
//	// routes/devtools.go
//	//go:build govibe_dev
//	func init() { DevTools = registerDevTools }
//
//	// routes/console.go: var DevTools func(rt *govibe.Runtime, dev *govibe.Dev) error
//	// main.go: DevTools: routes.DevTools (nil in a default build)
type App = kernel.App

// Dev is the dev panel as the app sees it, handed to App.DevTools: dev.Tool adds an
// MCP tool to /dev/mcp (see DevToolFunc), dev.Page a page at /dev/app/<path> linked
// from /dev, dev.Handle any other handler under /dev/app. A tool with a bad name is
// refused and logged, never a panic.
type Dev = kernel.Dev

// DevTool is an MCP tool of the app's own: a name (lower-case, underscores; the
// framework's graph_, db_ and schedule_ families are refused), a description an agent
// reads, a zero value of the arguments struct (its json and jsonschema tags are the
// schema), whether it mutates, and Run, which gets the arguments as JSON and returns
// the result. DevToolFunc builds one with typed arguments.
type DevTool = devext.Tool

// DevToolFunc is a DevTool whose arguments and result are typed: In is decoded from
// the call's arguments (a struct with json and jsonschema tags, or struct{} for none)
// and Out is returned as the call's structured content.
//
//	dev.Tool(govibe.DevToolFunc("orders_summary", "Count the orders by status.",
//		func(ctx context.Context, in struct{}) (orders.Summary, error) { return svc.Summary(ctx) }))
func DevToolFunc[In, Out any](name, description string, run func(ctx context.Context, in In) (Out, error)) DevTool {
	return devext.ToolFunc(name, description, run)
}

// Runtime is what the framework hands the app: the typed configuration, a logger
// and the one database pool (nil when no DSN is configured). Nothing here is a
// global.
type Runtime = kernel.Runtime

// RunFunc is the body of a command: the context ends on SIGINT or SIGTERM, args are
// what is left after the command's flags.
type RunFunc = kernel.RunFunc

// Command is one CLI command of the app, run as `./app <Use> [flags] [args]`. With a
// Schedule it is also run by the scheduler.
//
//	Use, Short string                  // the name ("noun:verb") and a line of description
//	Run        RunFunc                 // the body, for the CLI and the scheduler alike
//	Flags      func(*flag.FlagSet)     // optional flags, on the standard library's flag set
//	Schedule   *Schedule               // nil: CLI only
type Command = kernel.Command

// Schedule puts a Command on a schedule: the scheduler also runs it, with the flags'
// defaults and no arguments, while the same Run still serves `./app <name>`.
//
//	Spec    string        // "*/5 * * * *", "@hourly", "@every 10m", "CRON_TZ=Europe/Sofia 0 9 * * *"
//	Overlap Overlap       // Skip (the default) or Allow
//	Timeout time.Duration // a context deadline for each run; 0 is none
//
// A schedule has five fields (no seconds), the descriptors @hourly, @daily, @weekly,
// @monthly and @yearly, or "@every <duration>" of at least a minute. It is read in the
// zone of scheduler.timezone (UTC by default) unless it carries a CRON_TZ= prefix. An
// invalid one fails the boot and names the command.
type Schedule = kernel.Schedule

// Overlap says what happens when a scheduled command is due while its previous run is
// still going: Skip (the default) drops the new run and logs it, Allow runs both.
type Overlap = kernel.Overlap

// The two Overlap policies.
const (
	Skip  = kernel.Skip
	Allow = kernel.Allow
)

// Cron is a Schedule for a cron expression or a descriptor: `Schedule: govibe.Cron("0 2 * * *")`.
func Cron(spec string) *Schedule { return kernel.Cron(spec) }

// Every is a Schedule that runs the command every d (at least a minute), counted from
// the moment the scheduler starts: `Schedule: govibe.Every(10 * time.Minute)`.
func Every(d time.Duration) *Schedule { return kernel.Every(d) }

// Main runs the app with the process arguments and exits non-zero on failure. With
// no arguments it serves; otherwise the first argument is a built-in command
// (serve, migrate, routes, schedule, version, inspect, help) or one of the app's own.
func Main(app App) { kernel.Main(app) }

// Run executes one command line against the app and returns its error. It is what
// Main calls, and what tests and tooling call with their own arguments and writers.
func Run(ctx context.Context, app App, args []string, out, errOut io.Writer) error {
	return kernel.Run(ctx, app, args, out, errOut)
}
