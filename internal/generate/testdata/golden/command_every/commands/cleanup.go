// Package commands holds the thin adapters behind the app's CLI commands: like a
// controller for HTTP, a command calls a business action or service and returns.
package commands

import (
	"context"

	"github.com/petars-dev/govibe"
)

// Cleanup is the body of `./shop cleanup`, which the scheduler also runs
// (@every 10m) while the app serves. Call a business action or a service here and
// return; the context ends on SIGINT or SIGTERM, or at the schedule's timeout.
func Cleanup(ctx context.Context, rt *govibe.Runtime, args []string) error {
	rt.Log.Info("cleanup: nothing to do yet")
	return nil
}
