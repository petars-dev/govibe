// Package govibetest boots an application inside a test, the way `serve` boots it
// and without anything a test must not touch: the database is never opened and the
// dev tools are never mounted.
//
//	func TestHome(t *testing.T) {
//		srv := govibetest.Boot(t, newApp())
//		resp, err := http.Get(srv.URL + "/")
//		...
//	}
package govibetest

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/petars-dev/govibe"
	"github.com/petars-dev/govibe/internal/kernel"
)

// Server is a booted application served on a local test server.
type Server struct {
	*httptest.Server
	// Rt is the runtime the app's Routes and Commands were given. Rt.DB is always
	// nil: Boot never opens a database, whatever MARIADB_DSN says in the environment.
	Rt *govibe.Runtime
}

// Boot builds app through the framework's one boot path (configuration, router,
// command table) and serves its router on an httptest server that closes with the
// test. The database is not opened and no migration runs, so a test needs no
// database and cannot reach one by accident; the dev tools are not mounted, so a
// page renders as it does in production. A boot error fails the test.
func Boot(t testing.TB, app govibe.App) *Server {
	t.Helper()
	b, err := kernel.Build(context.Background(), app, kernel.Options{Quiet: true})
	if err != nil {
		t.Fatalf("govibetest: boot %s: %v", app.Name, err)
	}
	srv := httptest.NewServer(b.Router)
	t.Cleanup(func() {
		srv.Close()
		_ = b.Close()
	})
	return &Server{Server: srv, Rt: b.Rt}
}
