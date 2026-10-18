package govibe_test

import (
	"context"
	"net/http"
	"os"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe"
)

// An application is a declaration: it hands the framework what it contributes, and the
// framework boots and runs it. Routes receives a router that already carries the standard
// middleware and the asset mounts; Commands declares the CLI commands, some of them scheduled.
func Example() {
	// In an app the settings are the embedded env.yml next to main.go:
	//
	//	//go:embed env.yml
	//	var configFS embed.FS
	configFS := fstest.MapFS{"env.yml": {Data: []byte("shop:\n  currency: ${SHOP_CURRENCY:EUR}\n")}}
	app := govibe.App{
		Name:   "shop",
		Config: configFS,
		Routes: func(rt *govibe.Runtime, r chi.Router) error {
			r.Get("/api/hello", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"message":"Hello GoVibe!"}`))
			})
			return nil
		},
		Commands: func(rt *govibe.Runtime) []govibe.Command {
			return []govibe.Command{{
				Use: "items:prune", Short: "Delete archived items",
				Schedule: govibe.Cron("0 3 * * *"),
				Run: func(ctx context.Context, rt *govibe.Runtime, args []string) error {
					rt.Log.Info("pruned")
					return nil
				},
			}}
		},
	}
	// main() would be govibe.Main(app). Run executes one command line, here the route
	// listing, against the same declaration.
	_ = govibe.Run(context.Background(), app, []string{"routes"}, os.Stdout, os.Stderr)
}

// The app's own dev tools: an MCP tool and a page for the dev panel of a govibe_dev
// build. In an app this function lives in a file with the govibe_dev build tag and is
// handed over through a variable, so a production binary holds none of it.
func ExampleDevToolFunc() {
	registerDevTools := func(rt *govibe.Runtime, dev *govibe.Dev) error {
		dev.Tool(govibe.DevToolFunc("orders_summary", "Count the orders, by status when one is given.",
			func(ctx context.Context, in struct {
				Status string `json:"status,omitempty" jsonschema:"only orders in this status"`
			}) (map[string]int, error) {
				return map[string]int{"count": 3}, nil
			}))
		dev.Page("/orders", "Orders", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<h1>Orders</h1>"))
		}))
		return nil
	}
	_ = govibe.App{Name: "shop", DevTools: registerDevTools}
}
