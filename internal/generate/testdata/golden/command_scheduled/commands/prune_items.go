// Package commands holds the thin adapters behind the app's CLI commands: like a
// controller for HTTP, a command calls a business action or service and returns.
package commands

import (
	"context"

	"github.com/petars-dev/govibe"
)

// PruneItems is the body of `./shop items:prune`, which the scheduler also runs
// (0 3 * * *) while the app serves. Call a business action or a service here and
// return; the context ends on SIGINT or SIGTERM, or at the schedule's timeout.
func PruneItems(ctx context.Context, rt *govibe.Runtime, args []string) error {
	rt.Log.Info("items:prune: nothing to do yet")
	return nil
}
