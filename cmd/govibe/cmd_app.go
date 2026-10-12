package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"petars-dev/govibe/internal/kernel"
)

// serve and migrate run the application, so they live in the application binary
// (its embedded env.yml, migrations and assets are not the tool's to read). The
// tool keeps the muscle memory and delegates: `govibe serve` is `go run . serve`
// in the project root, one boot path for both.

func init() {
	rootCmd.AddCommand(serveCmd, migrateCmd)
	migrateCmd.AddCommand(appSubcommand("migrate", "up", "Apply all up migrations"))
	migrateCmd.AddCommand(appSubcommand("migrate", "status", "Print migration status"))
	migrateCmd.AddCommand(appSubcommand("migrate", "down", "Apply one down migration"))
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the application's HTTP server (go run . serve in the project root)",
	Args:  cobra.NoArgs,
	// The application has said what it had to say; cobra's own error and usage text would only add noise.
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runApp(kernel.FindRoot(os.Getenv("GOVIBE_ROOT")), "serve")
	},
}

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run database migrations (go run . migrate in the project root)",
}

func appSubcommand(parent, name, short string) *cobra.Command {
	return &cobra.Command{
		Use:           name,
		Short:         short,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApp(kernel.FindRoot(os.Getenv("GOVIBE_ROOT")), parent, name)
		},
	}
}

// appCommand is `go run . <args>` in the project root, wired to this process's
// terminal and environment. It runs in a process group of its own (on systems that
// have them), so a signal can reach the application behind `go run`, which does not
// pass SIGTERM on to the program it started.
func appCommand(root string, args ...string) *exec.Cmd {
	c := exec.Command("go", append([]string{"run", "."}, args...)...)
	c.Dir = root
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	ownProcessGroup(c)
	return c
}

// runApp runs the application with args and waits for it. A signal that reaches the
// tool is passed on to the application's whole process group (`go run` and the
// server it started), so Ctrl-C and SIGTERM stop the server gracefully instead of
// killing the tool while the server runs on.
func runApp(root string, args ...string) error {
	c := appCommand(root, args...)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	if err := c.Start(); err != nil {
		return fmt.Errorf("go run: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	stopped := false // a signal was passed on: the user asked for this stop
	for {
		select {
		case s := <-sig:
			stopped = true
			signalApp(c, s)
		case err := <-done:
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				if stopped && ee.ExitCode() == -1 {
					return nil // `go run` itself was ended by the signal; the application stopped as asked
				}
				return &exitError{code: ee.ExitCode()} // the app has said what it had to say
			}
			return err
		}
	}
}
