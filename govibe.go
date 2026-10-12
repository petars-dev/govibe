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

	"github.com/petars-dev/govibe/internal/kernel"
)

// App declares what an application contributes. Every field is optional.
//
//	Name       string                                // logs and tooling titles
//	Config     fs.FS                                 // its root holds env.yml, merged over the framework defaults
//	Migrations fs.FS                                 // holds sql/*.sql (goose)
//	Assets     fs.FS                                 // static files, served at /assets/*
//	Routes     func(rt *Runtime, r chi.Router) error // the app's HTTP surface
//	Commands   func(rt *Runtime) []Command           // the app's CLI commands
//	Shutdown   func(ctx context.Context) error       // called after the server has stopped
//
// Routes and Commands must not open connections or read secrets while they
// register: tooling calls them with no database (rt.DB is nil) to list what exists.
type App = kernel.App

// Runtime is what the framework hands the app: the typed configuration, a logger
// and the one database pool (nil when no DSN is configured). Nothing here is a
// global.
type Runtime = kernel.Runtime

// RunFunc is the body of a command: the context ends on SIGINT or SIGTERM, args are
// what is left after the command's flags.
type RunFunc = kernel.RunFunc

// Command is one CLI command of the app, run as `./app <Use> [flags] [args]`.
type Command = kernel.Command

// Main runs the app with the process arguments and exits non-zero on failure. With
// no arguments it serves; otherwise the first argument is a built-in command
// (serve, migrate, routes, version, inspect, help) or one of the app's own.
func Main(app App) { kernel.Main(app) }

// Run executes one command line against the app and returns its error. It is what
// Main calls, and what tests and tooling call with their own arguments and writers.
func Run(ctx context.Context, app App, args []string, out, errOut io.Writer) error {
	return kernel.Run(ctx, app, args, out, errOut)
}
