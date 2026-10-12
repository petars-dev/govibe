package govibetest_test

import (
	"io"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/petars-dev/govibe"
	"github.com/petars-dev/govibe/govibetest"
)

func app() govibe.App {
	return govibe.App{
		Name:   "probe",
		Config: fstest.MapFS{"env.yml": {Data: []byte("probe:\n  greeting: hi\n")}},
		Routes: func(rt *govibe.Runtime, r chi.Router) error {
			r.Get("/greet", func(w http.ResponseWriter, _ *http.Request) {
				var g string
				_ = rt.Config.Unmarshal("probe.greeting", &g)
				_, _ = w.Write([]byte(g))
			})
			return nil
		},
	}
}

func TestBootServesTheAppsRoutesWithItsOwnConfig(t *testing.T) {
	srv := govibetest.Boot(t, app())
	resp, err := http.Get(srv.URL + "/greet")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if b, _ := io.ReadAll(resp.Body); resp.StatusCode != 200 || string(b) != "hi" {
		t.Errorf("GET /greet = %d %q", resp.StatusCode, b)
	}
	// What the framework serves for every app is there too.
	if resp, err := http.Get(srv.URL + "/_govibe/static/htmx.min.js"); err != nil || resp.StatusCode != 200 {
		t.Errorf("htmx: %v %v", resp, err)
	}
}

// A test must not need a database and must not reach one by accident: whatever the
// environment says, Boot opens none.
func TestBootNeverOpensTheDatabase(t *testing.T) {
	t.Setenv("MARIADB_DSN", "nobody:nothing@tcp(127.0.0.1:1)/none") // would fail to connect if it were used
	srv := govibetest.Boot(t, app())
	if srv.Rt.DB != nil {
		t.Fatal("Boot opened a database")
	}
}

// And the dev tools are not mounted, whatever APP_ENV says: a page renders as in production.
func TestBootMountsNoDevTools(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	srv := govibetest.Boot(t, app())
	for _, p := range []string{"/dev/graph", "/dev/docs/app", "/_govibe/static/graph.js"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("GET %s = %d", p, resp.StatusCode)
		}
	}
}
