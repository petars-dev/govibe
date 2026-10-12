package govibe_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe"
)

// The public entry points keep their shape: an application depends on exactly these.
var (
	_ func(govibe.App)                                                        = govibe.Main
	_ func(context.Context, govibe.App, []string, io.Writer, io.Writer) error = govibe.Run
)

// exported is what orders:export read from the app's own settings.
var exported string

func shop() govibe.App {
	return govibe.App{
		Name:   "shop",
		Config: fstest.MapFS{"env.yml": {Data: []byte("shop:\n  currency: EUR\n")}},
		Routes: func(rt *govibe.Runtime, r chi.Router) error {
			r.Get("/orders", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) })
			return nil
		},
		Commands: func(rt *govibe.Runtime) []govibe.Command {
			return []govibe.Command{{
				Use: "orders:export", Short: "Export yesterday's orders",
				Run: func(ctx context.Context, rt *govibe.Runtime, args []string) error {
					return rt.Config.Unmarshal("shop.currency", &exported)
				},
			}}
		},
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("APP_ENV", "prod")
	t.Setenv("GOVIBE_ROOT", t.TempDir())
	t.Setenv("MARIADB_DSN", "")
	var out, errOut bytes.Buffer
	err := govibe.Run(context.Background(), shop(), args, &out, &errOut)
	return out.String() + errOut.String(), err
}

func TestRunHelpListsBuiltinsAndTheAppsCommands(t *testing.T) {
	out, err := run(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"usage: shop", "serve", "migrate", "routes", "version", "inspect", "orders:export"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestRunRoutesListsWhoRegisteredEach(t *testing.T) {
	out, err := run(t, "routes")
	if err != nil {
		t.Fatal(err)
	}
	var app, framework bool
	for _, line := range strings.Split(out, "\n") {
		app = app || (strings.Contains(line, "/orders") && strings.HasSuffix(strings.TrimSpace(line), "app"))
		framework = framework || (strings.Contains(line, "/_govibe/static/htmx.min.js") && strings.HasSuffix(strings.TrimSpace(line), "framework"))
	}
	if !app || !framework || !strings.HasPrefix(out, "METHOD") {
		t.Errorf("routes:\n%s", out)
	}
}

func TestRunVersionNamesTheAppAndTheFramework(t *testing.T) {
	out, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "shop\n") || !strings.Contains(out, "govibe github.com/petars-dev/govibe ") {
		t.Errorf("version:\n%s", out)
	}
	if _, err := run(t, "version", "extra"); err == nil {
		t.Error("version takes no arguments")
	}
}

func TestRunExecutesTheAppsCommandAndRejectsUnknownOnes(t *testing.T) {
	exported = ""
	if out, err := run(t, "orders:export"); err != nil || exported != "EUR" {
		t.Fatalf("orders:export = %q, %v (read %q from the app's settings)", out, err, exported)
	}
	if _, err := run(t, "orders:nope"); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("an unknown command must fail, got %v", err)
	}
}
