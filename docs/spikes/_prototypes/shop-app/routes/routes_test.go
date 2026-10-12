package routes_test

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"example.com/shop/routes"
	"petars-dev/govibe"
	"petars-dev/govibe/govibetest"
)

// A sub-package test cannot call main's newApp(): it builds its own App around the
// package under test, with a MapFS standing in for the embedded env.yml.
func TestRegister(t *testing.T) {
	app := govibe.App{
		Name:   "shop",
		Config: fstest.MapFS{"env.yml": {Data: []byte("shop:\n  greeting: hi\n")}},
		Routes: routes.Register,
	}
	h, _ := govibetest.Boot(t, app, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/hello", nil))
	if rec.Body.String() != "hi" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
