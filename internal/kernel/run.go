package kernel

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/migrate"
)

// shutdownTimeout is how long a stopping server waits for requests in flight before
// it closes them (a variable so a test can shorten it).
var shutdownTimeout = 10 * time.Second

// Main runs the app with the process arguments and exits non-zero on failure. The
// context ends on SIGINT or SIGTERM.
func Main(app App) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, app, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// Run executes one command line against the app: `serve` (the default), `migrate
// up|down|status`, `routes`, `version`, `inspect --json`, or one of the app's own
// commands. It is what Main calls, and what tests and tooling call with their own
// writers.
func Run(ctx context.Context, app App, args []string, out, errOut io.Writer) error {
	name := "serve"
	switch {
	case len(args) > 0 && (args[0] == "-h" || args[0] == "--help"):
		name, args = "help", args[1:]
	case len(args) > 0 && !strings.HasPrefix(args[0], "-"):
		name, args = args[0], args[1:]
	}
	switch name {
	case "serve":
		if len(args) > 0 {
			return fmt.Errorf("serve takes no arguments, got %q", args)
		}
		b, err := Build(ctx, app, Options{DB: true, Migrate: true, Dev: true})
		if err != nil {
			return err
		}
		return serve(ctx, b)
	case "migrate":
		return runMigrate(ctx, app, args, out)
	case "inspect":
		return runInspect(ctx, app, args, out)
	case "routes":
		return runRoutes(ctx, app, args, out)
	case "version":
		return runVersion(app, args, out)
	case "help":
		return usage(ctx, app, out)
	}

	// An app command opens the database but does not migrate it: serve does that.
	b, err := Build(ctx, app, Options{DB: true})
	if err != nil {
		return err
	}
	defer b.Close()
	for _, c := range b.Commands {
		if c.Use != name {
			continue
		}
		fs := flag.NewFlagSet(c.Use, flag.ContinueOnError)
		fs.SetOutput(errOut)
		if c.Flags != nil {
			c.Flags(fs)
		}
		if err := fs.Parse(args); err != nil {
			return err
		}
		return c.Run(ctx, b.Rt, fs.Args())
	}
	return fmt.Errorf("unknown command %q (try: help)", name)
}

// usage lists the built-in commands and the app's.
func usage(ctx context.Context, app App, out io.Writer) error {
	b, err := Build(ctx, app, Options{Quiet: true})
	if err != nil {
		return err
	}
	defer b.Close()
	fmt.Fprintf(out, "usage: %s <command> [flags]\n\ncommands:\n", strings.TrimSpace(app.Name+" "))
	fmt.Fprintln(out, "  serve                   run the HTTP server (the default)")
	fmt.Fprintln(out, "  migrate up|down|status  apply, roll back or list database migrations")
	fmt.Fprintln(out, "  routes                  list the routes and who registered them, with no database")
	fmt.Fprintln(out, "  version                 print the app and framework versions")
	fmt.Fprintln(out, "  inspect --json          print the routes and commands, with no database")
	cmds := append([]Command(nil), b.Commands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Use < cmds[j].Use })
	for _, c := range cmds {
		fmt.Fprintf(out, "  %-23s %s\n", c.Use, c.Short)
	}
	return nil
}

// serve runs the HTTP server until ctx ends, then stops it gracefully, calls the
// app's Shutdown and releases the database.
func serve(ctx context.Context, b *Built) error {
	defer b.Close()
	cfg := b.Rt.Config
	port := cfg.Server.Port
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      b.Router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	logrus.Infof("listening on http://localhost:%s", port)
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()

	select {
	case err := <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server: %w", err)
		}
	case <-ctx.Done():
		logrus.Info("shutting down")
	}

	// The signal context is done; shutdown needs one of its own.
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	err := srv.Shutdown(sctx)
	if errors.Is(err, context.DeadlineExceeded) {
		// A connection that will not finish (a long-lived stream, say) must not turn a
		// clean stop into a failure or keep the port and the graph store held.
		logrus.Warnf("shutdown: connections still open after %s, closing them", shutdownTimeout)
		err = srv.Close()
	}
	if b.App.Shutdown != nil {
		if serr := b.App.Shutdown(sctx); serr != nil && err == nil {
			err = fmt.Errorf("shutdown: %w", serr)
		}
	}
	return err
}

// runMigrate is `migrate up|down|status`. It opens the database and nothing else:
// the app's routes are not built, so a broken registration cannot block a repair.
func runMigrate(ctx context.Context, app App, args []string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "down" && args[0] != "status") {
		return errors.New("usage: migrate up|down|status")
	}
	if app.Migrations == nil {
		return errors.New("this app declares no Migrations")
	}
	rt, closers, err := openRuntime(ctx, app, Options{DB: true})
	if err != nil {
		return err
	}
	b := &Built{closers: closers}
	defer b.Close()
	if rt.DB == nil {
		return errors.New("no DSN configured (MARIADB_DSN)")
	}
	dialect, err := migrate.ParseDialect(rt.Config.Database.Type)
	if err != nil {
		return err
	}
	switch args[0] {
	case "up":
		logrus.Info("running goose up")
		n, err := migrate.Up(ctx, rt.DB.DB, dialect, app.Migrations)
		if err == nil {
			fmt.Fprintf(out, "applied %d migration(s)\n", n)
		}
		return err
	case "down":
		logrus.Info("running goose down")
		return migrate.Down(ctx, rt.DB.DB, dialect, app.Migrations)
	}
	return migrate.Status(ctx, out, rt.DB.DB, dialect, app.Migrations)
}
