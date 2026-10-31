package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// app is an fs.FS whose root holds an env.yml, as an app's `//go:embed env.yml` does.
func app(yml string) fstest.MapFS {
	return fstest.MapFS{File: {Data: []byte(yml)}}
}

func TestDefaultsAlone(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("APP_ENV", "")
	t.Setenv("MARIADB_DSN", "") // a developer's shell may export a real one
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "local" || cfg.Server.Port != "8080" || cfg.Server.LogLevel != "info" || cfg.Database.Type != "mysql" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if !cfg.IsLocal() || cfg.IsProd() {
		t.Fatalf("env helpers wrong for %q", cfg.Env)
	}
	if cfg.Database.MariaDB.DSN != "" || cfg.Dev.DB.AllowMutations {
		t.Fatalf("a default must not enable a database or mutations: %+v", cfg)
	}
}

// The framework defaults sit under the app's file: the app wins, maps merge deeply,
// lists are replaced, unknown keys pass, and environment placeholders resolve in
// app-only keys too.
func TestLayering(t *testing.T) {
	t.Setenv("PORT", "9999")
	t.Setenv("DEEP", "set")
	cfg, err := Load(app(`
database:
  mariadb:
    pool: 5
server:
  port: ${APP_PORT:7777}
tags: [a, b]
extra:
  deep:
    key: ${DEEP:dflt}
`))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"database.mariadb.pool": "5",     // a new key inside a framework map
		"database.type":         "mysql", // a framework default survives the merge
		"server.port":           "7777",  // the app overrides the framework placeholder, so PORT=9999 is ignored
		"extra.deep.key":        "set",   // an app-only key, resolved from the environment
		"server.log_level":      "info",  // a framework default placeholder
	} {
		if got := cfg.String(path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if cfg.Server.Port != "7777" {
		t.Errorf("the typed key follows the layering: Server.Port = %q", cfg.Server.Port)
	}
	var tags []string
	if err := cfg.Unmarshal("tags", &tags); err != nil || strings.Join(tags, ",") != "a,b" {
		t.Errorf("tags = %v, %v", tags, err)
	}
}

func TestListsAreReplaced(t *testing.T) {
	cfg, err := Load(app("x:\n  list: [1, 2, 3]\n"))
	if err != nil {
		t.Fatal(err)
	}
	over, err := Load(app("x:\n  list: [9]\n"))
	if err != nil {
		t.Fatal(err)
	}
	var a, b []int
	_ = cfg.Unmarshal("x.list", &a)
	_ = over.Unmarshal("x.list", &b)
	if len(a) != 3 || len(b) != 1 || b[0] != 9 {
		t.Fatalf("a=%v b=%v", a, b)
	}
}

// An app that declares no settings (comments only, or an empty file) is valid.
func TestEmptyAppFile(t *testing.T) {
	for name, yml := range map[string]string{"empty": "", "comments only": "# nothing here\n"} {
		cfg, err := Load(app(yml))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if cfg.Server.Port == "" {
			t.Fatalf("%s: lost the defaults", name)
		}
	}
}

func TestBadAppFile(t *testing.T) {
	if _, err := Load(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), File) {
		t.Fatalf("an app FS without %s must be an error that names it, got %v", File, err)
	}
	if _, err := Load(app("broken: [unclosed\n")); err == nil || !strings.Contains(err.Error(), File) {
		t.Fatalf("a malformed %s must be an error that names it, got %v", File, err)
	}
	if _, err := Load(app("- not\n- a mapping\n")); err == nil {
		t.Fatal("a top-level list is not a settings file")
	}
}

// Placeholders type their values the way they always have, so they bind to int and
// bool fields; a quoted placeholder keeps its value a string (it used to become
// 1.1 for ${V:1.10}).
func TestPlaceholderTyping(t *testing.T) {
	t.Setenv("V_FLAG", "")
	cfg, err := Load(app(`
v:
  version: ${V_VERSION:1.10}
  zip: ${V_ZIP:00123}
  flag: ${V_FLAG:yes}
  on: ${V_ON:true}
  count: ${V_COUNT:12}
  quoted: "${V_QUOTED:1.10}"
  single: '${V_ZIP2:00123}'
  text: plain ${NOT_A_PLACEHOLDER} text
`))
	if err != nil {
		t.Fatal(err)
	}
	for path, w := range map[string]any{
		"v.version": 1.1,        // unquoted: as today
		"v.zip":     int64(123), // unquoted: as today
		"v.flag":    "yes",      // not a Go bool
		"v.on":      true,       // bool
		"v.count":   int64(12),  // int
		"v.quoted":  "1.10",     // quoted: stays text
		"v.single":  "00123",    // quoted: stays text
		"v.text":    "plain ${NOT_A_PLACEHOLDER} text",
	} {
		got, ok := cfg.Get(path)
		if !ok || got != w {
			t.Errorf("%s = %#v (%v), want %#v", path, got, ok, w)
		}
	}
}

func TestEnvironmentWinsOverDefaultAndEmptyDoesNot(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DATABASE_TYPE", "") // set but empty: the default applies
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.LogLevel != "debug" || cfg.Database.Type != "mysql" {
		t.Fatalf("log_level=%q type=%q", cfg.Server.LogLevel, cfg.Database.Type)
	}
}

// No package state: loading again with another environment and another app file
// leaves the first Config as it was.
func TestLoadsAreIndependent(t *testing.T) {
	t.Setenv("SHOP_CURRENCY", "BGN")
	a, err := Load(app("shop:\n  currency: ${SHOP_CURRENCY:EUR}\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHOP_CURRENCY", "USD")
	b, err := Load(app("other:\n  key: v\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.String("shop.currency") != "BGN" {
		t.Fatalf("first config changed: %q", a.String("shop.currency"))
	}
	if _, ok := b.Get("shop.currency"); ok {
		t.Fatal("second config sees the first one's keys")
	}
	var zero Config
	if _, ok := zero.Get("env"); ok {
		t.Fatal("the zero Config has no keys")
	}
}

func TestGetAndUnmarshal(t *testing.T) {
	cfg, err := Load(app("shop:\n  currency: EUR\n  limits:\n    max: 5\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := cfg.Get("shop.currency"); !ok || v != "EUR" {
		t.Fatalf("Get = %v %v", v, ok)
	}
	if _, ok := cfg.Get("shop.missing"); ok {
		t.Fatal("a missing key is not found")
	}
	if _, ok := cfg.Get("shop.currency.deeper"); ok {
		t.Fatal("a path through a scalar is not found")
	}
	var shop struct {
		Currency string `yaml:"currency"`
		Limits   struct {
			Max int `yaml:"max"`
		} `yaml:"limits"`
	}
	if err := cfg.Unmarshal("shop", &shop); err != nil || shop.Currency != "EUR" || shop.Limits.Max != 5 {
		t.Fatalf("Unmarshal = %+v, %v", shop, err)
	}
	if err := cfg.Unmarshal("nope", &shop); err == nil {
		t.Fatal("a missing path must be an error")
	}
}

// The scheduler's shutdown timeout takes a Go duration or a number of seconds, and a mistake
// names the key. 0 means "do not wait", and is not mistaken for unset.
func TestShutdownTimeoutIsADurationOrSeconds(t *testing.T) {
	for env, want := range map[string]time.Duration{
		"":      30 * time.Second, // the default
		"2m":    2 * time.Minute,
		"45s":   45 * time.Second,
		"30":    30 * time.Second, // seconds
		"0":     0,                // do not wait
		"0s":    0,
		"1":     time.Second, // not read as true
		"1.5":   1500 * time.Millisecond,
		"1h30m": 90 * time.Minute,
	} {
		t.Setenv("SCHEDULER_SHUTDOWN_TIMEOUT", env)
		cfg, err := Load(nil)
		if err != nil || cfg.Scheduler.ShutdownTimeout != want {
			t.Errorf("SCHEDULER_SHUTDOWN_TIMEOUT=%q: %v, %v; want %v", env, cfg.Scheduler.ShutdownTimeout, err, want)
		}
	}
	for env, want := range map[string]string{
		"soon": `scheduler.shutdown_timeout: "soon" is not a duration`,
		"-5s":  `scheduler.shutdown_timeout: "-5s"`,
	} {
		t.Setenv("SCHEDULER_SHUTDOWN_TIMEOUT", env)
		if _, err := Load(nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("SCHEDULER_SHUTDOWN_TIMEOUT=%q: %v, want an error containing %q", env, err, want)
		}
	}
	// An app's own file: a bare number is seconds, an unquoted 0 (read as false) is an error that says to quote it.
	t.Setenv("SCHEDULER_SHUTDOWN_TIMEOUT", "")
	if cfg, err := Load(app("scheduler:\n  shutdown_timeout: 90\n")); err != nil || cfg.Scheduler.ShutdownTimeout != 90*time.Second {
		t.Errorf("shutdown_timeout: 90 = %v, %v", cfg.Scheduler.ShutdownTimeout, err)
	}
	if _, err := Load(app("scheduler:\n  shutdown_timeout: ${X:0}\n")); err == nil || !strings.Contains(err.Error(), "scheduler.shutdown_timeout") || !strings.Contains(err.Error(), "quote") {
		t.Errorf("an unquoted placeholder holding 0: %v", err)
	}
}
