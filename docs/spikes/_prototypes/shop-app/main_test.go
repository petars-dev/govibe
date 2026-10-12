package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"petars-dev/govibe"
	"petars-dev/govibe/govibetest"
)

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String()
}

// The app boots from newApp(): env.yml embedded from the root main.go.
func TestBootsNewApp(t *testing.T) {
	h, b := govibetest.Boot(t, newApp(), map[string]string{"APP_ENV": "prod", "SHOP_CURRENCY": "BGN"})
	if code, body := get(t, h, "/hello"); code != 200 || body != "Hello from env.yml" {
		t.Fatalf("/hello = %d %q", code, body)
	}
	if code, body := get(t, h, "/api/hello"); code != 200 || !strings.Contains(body, "Hello GoVibe!") {
		t.Fatalf("/api/hello = %d %q", code, body)
	}
	if got := b.Rt.Config.String("shop.currency"); got != "BGN" {
		t.Fatalf("shop.currency = %q (env placeholder in the app file)", got)
	}
	if got := b.Rt.Config.String("scheduler.timezone"); got != "UTC" {
		t.Fatalf("framework default scheduler.timezone = %q", got)
	}
	// prod: embedded assets, not the ./assets directory next to the test.
	if !contains(b.Notes, "assets: embedded") {
		t.Fatalf("notes = %v", b.Notes)
	}
	if code, body := get(t, h, "/assets/static/home.css"); code != 200 || !strings.Contains(body, "sans-serif") {
		t.Fatalf("embedded asset = %d %q", code, body)
	}
}

// Local env + the test's cwd (the package dir, which here is the app root): disk assets.
func TestDevDiskOverride(t *testing.T) {
	_, b := govibetest.Boot(t, newApp(), map[string]string{"APP_ENV": "local"})
	if !contains(b.Notes, "assets: disk assets") {
		t.Fatalf("notes = %v", b.Notes)
	}
}

// Layering rules: the app's file wins; maps merge deeply; lists are replaced; unknown keys pass.
func TestConfigLayering(t *testing.T) {
	app := govibe.App{Name: "t", Config: fstest.MapFS{"env.yml": {Data: []byte(`
database:
  mariadb:
    pool: 5
server:
  port: ${APP_PORT:7777}
tags: [a, b]
extra:
  deep:
    key: ${DEEP:dflt}
`)}}}
	_, b := govibetest.Boot(t, app, map[string]string{"PORT": "9999", "DEEP": "set"})
	c := b.Rt.Config
	for k, want := range map[string]string{
		"database.mariadb.pool": "5",     // new key inside a framework map
		"database.type":         "mysql", // framework default survives the merge
		"server.port":           "7777",  // app overrides the framework placeholder; PORT=9999 is ignored
		"extra.deep.key":        "set",   // unknown (app-only) key, env resolved
		"server.log_level":      "info",  // framework default placeholder
	} {
		if got := c.String(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// A nil Config is valid (every App field is optional).
func TestZeroApp(t *testing.T) {
	h, _ := govibetest.Boot(t, govibe.App{}, nil)
	if code, _ := get(t, h, "/_govibe/ping"); code != 200 {
		t.Fatalf("ping = %d", code)
	}
}

func TestMigrationsFS(t *testing.T) {
	entries, err := fs.ReadDir(newApp().Migrations, "sql")
	if err != nil || len(entries) != 1 {
		t.Fatalf("sql/ in the handed-over FS: %v %v", entries, err)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// Documents what the carried-over ${ENV:default} resolver does to string-like values.
func TestPlaceholderCoercion(t *testing.T) {
	app := govibe.App{Config: fstest.MapFS{"env.yml": {Data: []byte(`
v:
  version: ${V_VERSION:1.10}
  zip: ${V_ZIP:00123}
  flag: ${V_FLAG:yes}
  big: ${V_BIG:12345678901234567890}
  quoted: "${V_QUOTED:1.10}"
`)}}}
	_, b := govibetest.Boot(t, app, nil)
	for _, k := range []string{"v.version", "v.zip", "v.flag", "v.big", "v.quoted"} {
		v, _ := b.Rt.Config.Get(k)
		t.Logf("%-10s = %v (%T)", k, v, v)
	}
}
