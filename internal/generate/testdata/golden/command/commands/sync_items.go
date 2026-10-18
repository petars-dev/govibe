// Package commands holds the thin adapters behind the app's CLI commands: like a
// controller for HTTP, a command calls a business action or service and returns.
package commands

import (
	"context"

	"github.com/petars-dev/govibe"
)

// SyncItems is the body of `./shop items:sync`. Call a business action or a service here and
// return; the context ends on SIGINT or SIGTERM.
func SyncItems(ctx context.Context, rt *govibe.Runtime, args []string) error {
	rt.Log.Info("items:sync: nothing to do yet")
	return nil
}
