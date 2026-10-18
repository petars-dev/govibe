package routes

import (
	"github.com/acme/shop/commands"
	"github.com/petars-dev/govibe"
)

// Console declares the app's CLI commands, and which of them run on a schedule. It
// is empty to start: a command is one govibe.Command here, with a thin body in a
// commands/ package that calls a business action or service. Like Register, it must
// not open connections or read secrets (rt.DB is nil when tooling lists what exists).
func Console(rt *govibe.Runtime) []govibe.Command {
	return []govibe.Command{
		{Use: "cleanup", Short: "Clean up", Schedule: govibe.Cron("@every 10m"), Run: commands.Cleanup},
		// govibe:commands
	}
}
