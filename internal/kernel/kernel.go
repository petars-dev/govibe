// Package kernel is the one boot path of a GoVibe process: `main`, `serve`, the
// read-only commands and tests all go through Build. The application declares what
// it contributes (App); the kernel loads the configuration once, opens one database
// pool, runs migrations, assembles the router and builds the command table. The
// framework never imports the application.
package kernel

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/migrate"
	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/web"
)

// App declares what an application contributes. Every field is optional.
type App struct {
	Name       string                                // logs and tooling titles
	Config     fs.FS                                 // its root holds env.yml, merged over the framework defaults
	Migrations fs.FS                                 // holds sql/*.sql (goose)
	Assets     fs.FS                                 // static files, served at /assets/*
	Routes     func(rt *Runtime, r chi.Router) error // the app's HTTP surface
	Commands   func(rt *Runtime) []Command           // the app's CLI commands
	Shutdown   func(ctx context.Context) error       // called after the server has stopped
}

// Runtime is what the framework hands the app. Nothing here is a global.
type Runtime struct {
	Config config.Config
	Log    logrus.FieldLogger
	DB     *sqlx.DB // nil when no DSN is configured, or in a read-only command
}

// RunFunc is the body of a command: the context ends on SIGINT or SIGTERM, args
// are the arguments left after the command's flags.
type RunFunc func(ctx context.Context, rt *Runtime, args []string) error

// Command is one CLI command of the app, run as `./app <Use> [flags] [args]`.
type Command struct {
	Use, Short string
	Run        RunFunc
	// Flags declares the command's flags on the standard library's flag set; nil
	// means none.
	Flags func(*flag.FlagSet)
}

// Options say which parts of the boot sequence run.
type Options struct {
	DB      bool   // open the database pool when a DSN is configured
	Migrate bool   // and bring the schema up to date (needs DB)
	Dev     bool   // mount the dev tools, when they are compiled in and the environment is not production
	Quiet   bool   // log warnings only: a command whose stdout carries data
	Root    string // the application's root directory; "" finds it (see FindRoot)
}

// Built is a booted process: everything Main, serve and the tests need.
type Built struct {
	App      App
	Rt       *Runtime
	Router   chi.Router
	Commands []Command
	Root     string

	// FrameworkRoutes holds the key ("METHOD path") of every route the framework
	// registered before the app's Routes ran: the one origin fact only the running
	// process knows (a handler's own origin can differ, see inspect).
	FrameworkRoutes map[string]bool
	// Assets says where /assets is served from: "disk <dir>", "embedded" or "none".
	Assets string
	// DevMounted is true when the dev tools are mounted on Router.
	DevMounted bool

	closers []func() error
}

// Close releases what the boot opened (the database pool). It is safe to call twice.
func (b *Built) Close() error {
	var first error
	for i := len(b.closers) - 1; i >= 0; i-- {
		if err := b.closers[i](); err != nil && first == nil {
			first = err
		}
	}
	b.closers = nil
	return first
}

// Names the framework owns: an application cannot declare a command or a route there.
var builtinCommands = []string{"serve", "migrate", "routes", "version", "inspect", "help"}

// Reserved URL namespaces: /dev is the dev panel, /_govibe is framework runtime.
var reservedPrefixes = []string{"/dev", "/_govibe"}

// openRuntime is steps 1 to 3 of the boot: configuration (loaded once), logging,
// and the one database pool with its migrations. The caller owns the closers.
func openRuntime(ctx context.Context, app App, opt Options) (*Runtime, []func() error, error) {
	cfg, err := config.Load(app.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}
	level := cfg.Server.LogLevel
	if opt.Quiet {
		level = "warn"
	}
	Setup(level)

	var log logrus.FieldLogger = logrus.StandardLogger()
	if app.Name != "" {
		log = logrus.WithField("app", app.Name)
	}
	rt := &Runtime{Config: cfg, Log: log}

	var closers []func() error
	if opt.DB && cfg.Database.MariaDB.DSN != "" {
		dbx, err := OpenDB(cfg.Database.MariaDB.DSN)
		if err != nil {
			return nil, nil, fmt.Errorf("db open: %w", err)
		}
		rt.DB = dbx
		closers = append(closers, dbx.Close)
		if opt.Migrate && app.Migrations != nil {
			dialect, err := migrate.ParseDialect(cfg.Database.Type)
			if err == nil {
				_, err = migrate.Up(ctx, dbx.DB, dialect, app.Migrations)
			}
			if err != nil {
				_ = dbx.Close()
				return nil, nil, fmt.Errorf("db migrate: %w", err)
			}
			logrus.Info("database connected and migrated")
		} else {
			logrus.Info("database connected")
		}
	}
	return rt, closers, nil
}

// Build runs the boot sequence up to a finished router and command table.
func Build(ctx context.Context, app App, opt Options) (*Built, error) {
	rt, closers, err := openRuntime(ctx, app, opt)
	if err != nil {
		return nil, err
	}
	cfg := rt.Config
	b := &Built{App: app, Rt: rt, closers: closers, Root: opt.Root, Assets: "none"}
	if b.Root == "" {
		b.Root = FindRoot(cfg.Root)
	}
	fail := func(err error) (*Built, error) { _ = b.Close(); return nil, err }

	// The router: the standard stack, then the framework's own routes.
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(web.RequestLogger)
	devWanted := opt.Dev && mountDev != nil && !cfg.IsProd()
	if opt.Dev {
		switch {
		case mountDev != nil && cfg.IsProd():
			logrus.Warn("dev tools are compiled in but the environment is production: /dev is not mounted")
		case mountDev == nil && !cfg.IsProd():
			logrus.Info("dev tools are not compiled into this binary, so /dev is off (build with -tags govibe_dev)")
		}
	}
	if devWanted {
		// Pages ask web.IfDev whether to show their links to the dev tools.
		r.Use(web.MarkDev)
	}
	b.Assets = mountAssets(r, cfg, app.Assets, b.Root)
	r.Get(web.HTMXPath, web.ServeHTMX)

	// Everything registered so far is the framework's.
	b.FrameworkRoutes = map[string]bool{}
	for _, fr := range routeinfo.Walk(r) {
		b.FrameworkRoutes[fr.Key()] = true
	}
	if app.Routes != nil {
		if err := app.Routes(rt, r); err != nil {
			return fail(fmt.Errorf("routes: %w", err))
		}
	}
	if err := checkReserved(r, b.FrameworkRoutes); err != nil {
		return fail(err)
	}
	b.Router = r

	if app.Commands != nil {
		b.Commands = app.Commands(rt)
	}
	if err := checkCommands(b.Commands); err != nil {
		return fail(err)
	}

	if devWanted {
		mountDev(b)
		b.DevMounted = true
	}
	return b, nil
}

// checkReserved fails when the app registered a route in a namespace the
// framework owns.
func checkReserved(r chi.Router, framework map[string]bool) error {
	var clash []string
	for _, rt := range routeinfo.Walk(r) {
		if framework[rt.Key()] {
			continue
		}
		for _, p := range reservedPrefixes {
			if rt.Path == p || strings.HasPrefix(rt.Path, p+"/") {
				clash = append(clash, rt.Key())
				break
			}
		}
	}
	if len(clash) == 0 {
		return nil
	}
	return fmt.Errorf("the app registered %s under a path the framework reserves (%s)",
		strings.Join(clash, ", "), strings.Join(reservedPrefixes, ", "))
}

// checkCommands rejects an unnamed, duplicate or built-in command name.
func checkCommands(cmds []Command) error {
	seen := map[string]bool{}
	for _, name := range builtinCommands {
		seen[name] = true
	}
	for _, c := range cmds {
		switch {
		case c.Use == "":
			return fmt.Errorf("command with no name (Use) and the description %q", c.Short)
		case c.Run == nil:
			return fmt.Errorf("command %q has no Run", c.Use)
		case seen[c.Use]:
			return fmt.Errorf("command %q is declared twice, or is a built-in name (%s)", c.Use, strings.Join(builtinCommands, ", "))
		}
		seen[c.Use] = true
	}
	return nil
}
