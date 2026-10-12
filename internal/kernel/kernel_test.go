package kernel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/web"
)

// isolate keeps a test off the developer's real resources: no database from the
// shell's MARIADB_DSN, no graph store in the repository (the dev tools index and
// lock docs/graph under the application root), production behavior unless the
// test says otherwise.
func isolate(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("MARIADB_DSN", "")
	t.Setenv("DATABASE_TYPE", "")
	t.Setenv("GOVIBE_ROOT", root)
	t.Setenv("APP_ENV", "prod")
	t.Setenv("PORT", "")
	return root
}

// envFS is what an app's `//go:embed env.yml` looks like from the outside.
func envFS(yml string) fstest.MapFS { return fstest.MapFS{"env.yml": {Data: []byte(yml)}} }

func get(t *testing.T, h http.Handler, path string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Body.String(), rec.Header()
}

func hello(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }

func shopApp() App {
	return App{
		Name:   "shop",
		Config: envFS("shop:\n  greeting: ${SHOP_GREETING:hi}\n"),
		Assets: fstest.MapFS{"static/a.txt": {Data: []byte("embedded a")}},
		Routes: func(rt *Runtime, r chi.Router) error {
			r.Get("/hello", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(rt.Config.String("shop.greeting")))
			})
			r.Route("/api", func(api chi.Router) { api.Get("/ping", hello) })
			return nil
		},
		Commands: func(rt *Runtime) []Command {
			return []Command{{Use: "orders:export", Short: "Export orders", Run: func(ctx context.Context, rt *Runtime, args []string) error { return nil }}}
		},
	}
}

func TestBuildBootsAnApp(t *testing.T) {
	isolate(t)
	t.Setenv("SHOP_GREETING", "hello from env")
	b, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if code, body, _ := get(t, b.Router, "/hello"); code != 200 || body != "hello from env" {
		t.Fatalf("/hello = %d %q", code, body)
	}
	if code, _, _ := get(t, b.Router, "/api/ping"); code != 200 {
		t.Fatalf("/api/ping = %d", code)
	}
	// The framework defaults sit under the app's file, and the runtime carries them.
	if b.Rt.Config.Server.Port != "8080" || b.Rt.Config.String("shop.greeting") != "hello from env" {
		t.Fatalf("config = %+v", b.Rt.Config.Server)
	}
	if b.Rt.DB != nil {
		t.Fatal("no database was asked for")
	}
	if b.Rt.Log == nil || len(b.Commands) != 1 || b.Commands[0].Use != "orders:export" {
		t.Fatalf("log %v, commands %v", b.Rt.Log, b.Commands)
	}
	// prod: the embedded assets, not an assets directory.
	if b.Assets != "embedded" {
		t.Fatalf("assets = %q", b.Assets)
	}
	if code, body, _ := get(t, b.Router, "/assets/static/a.txt"); code != 200 || body != "embedded a" {
		t.Fatalf("embedded asset = %d %q", code, body)
	}
	// The framework serves htmx itself.
	if code, body, hdr := get(t, b.Router, web.HTMXPath); code != 200 || !strings.Contains(body, "htmx") || !strings.HasPrefix(hdr.Get("Content-Type"), "text/javascript") {
		t.Fatalf("htmx = %d %q", code, hdr.Get("Content-Type"))
	}
	// Dev was not asked for, and prod would refuse anyway.
	if b.DevMounted {
		t.Fatal("dev tools mounted")
	}
	if code, _, _ := get(t, b.Router, "/dev/docs/app"); code != 404 {
		t.Fatalf("/dev/docs/app = %d", code)
	}
}

// Every App field is optional.
func TestZeroApp(t *testing.T) {
	isolate(t)
	b, err := Build(context.Background(), App{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if code, _, _ := get(t, b.Router, web.HTMXPath); code != 200 {
		t.Fatalf("htmx = %d", code)
	}
	if code, _, _ := get(t, b.Router, "/assets/anything"); code != 404 || b.Assets != "none" {
		t.Fatalf("/assets = %d, assets = %q", code, b.Assets)
	}
}

func TestBadConfigIsABootError(t *testing.T) {
	isolate(t)
	_, err := Build(context.Background(), App{Config: fstest.MapFS{}}, Options{})
	if err == nil || !strings.Contains(err.Error(), "env.yml") {
		t.Fatalf("an app whose Config holds no env.yml must fail naming it, got %v", err)
	}
	_, err = Build(context.Background(), App{Config: envFS("broken: [unclosed\n")}, Options{})
	if err == nil || !strings.Contains(err.Error(), "env.yml") {
		t.Fatalf("a malformed env.yml must fail naming it, got %v", err)
	}
}

func TestRoutesErrorFailsTheBoot(t *testing.T) {
	isolate(t)
	boom := errors.New("boom")
	_, err := Build(context.Background(), App{Routes: func(*Runtime, chi.Router) error { return boom }}, Options{})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "routes") {
		t.Fatalf("err = %v", err)
	}
}

// In local and dev the app's assets directory is served from disk with no cache,
// resolved against the application root and not the working directory; elsewhere
// the embedded files are served.
func TestAssetsFromDiskResolveAgainstTheRoot(t *testing.T) {
	root := isolate(t)
	t.Setenv("APP_ENV", "local")
	writeFile(t, root, "assets/static/a.txt", "from disk")
	writeFile(t, root, "local/assets/static/b.txt", "from the local override")

	b, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	// local/assets wins over assets, as it always has; the process's own working
	// directory (this package) has neither.
	if want := "disk " + filepath.Join(root, "local/assets"); b.Assets != want {
		t.Fatalf("assets = %q, want %q", b.Assets, want)
	}
	code, body, hdr := get(t, b.Router, "/assets/static/b.txt")
	if code != 200 || body != "from the local override" || !strings.Contains(hdr.Get("Cache-Control"), "no-store") {
		t.Fatalf("disk asset = %d %q cache %q", code, body, hdr.Get("Cache-Control"))
	}

	// With only assets/: that directory.
	if err := os.RemoveAll(filepath.Join(root, "local")); err != nil {
		t.Fatal(err)
	}
	b2, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if code, body, _ := get(t, b2.Router, "/assets/static/a.txt"); code != 200 || body != "from disk" || b2.Assets != "disk "+filepath.Join(root, "assets") {
		t.Fatalf("assets dir = %d %q (%s)", code, body, b2.Assets)
	}

	// No directory in local: the embedded files, not a 404.
	if err := os.RemoveAll(filepath.Join(root, "assets")); err != nil {
		t.Fatal(err)
	}
	b3, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b3.Close()
	if code, body, _ := get(t, b3.Router, "/assets/static/a.txt"); code != 200 || body != "embedded a" || b3.Assets != "embedded" {
		t.Fatalf("fallback = %d %q (%s)", code, body, b3.Assets)
	}

	// Production never reads the disk, even when the directory exists.
	t.Setenv("APP_ENV", "prod")
	writeFile(t, root, "assets/static/a.txt", "from disk")
	b4, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b4.Close()
	if code, body, _ := get(t, b4.Router, "/assets/static/a.txt"); body != "embedded a" || code != 200 || b4.Assets != "embedded" {
		t.Fatalf("prod = %d %q (%s)", code, body, b4.Assets)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindRoot(t *testing.T) {
	t.Setenv("GOVIBE_ROOT", "")
	base := t.TempDir()
	proj := filepath.Join(base, "proj")
	writeFile(t, proj, "go.mod", "module example.com/proj\n")
	deep := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }

	t.Chdir(deep)
	if got := real(FindRoot("")); got != real(proj) {
		t.Errorf("from a subdirectory: %q, want the directory holding go.mod %q", got, proj)
	}
	// govibe.yml marks a root too, and the nearest marker wins.
	writeFile(t, proj, "a/govibe.yml", "")
	if got := real(FindRoot("")); got != real(filepath.Join(proj, "a")) {
		t.Errorf("nearest marker: %q", got)
	}
	// A configured root wins over everything, made absolute.
	if got := FindRoot(base); got != base {
		t.Errorf("configured: %q, want %q", got, base)
	}
	// No marker anywhere above: the working directory.
	bare := t.TempDir()
	t.Chdir(bare)
	if got := real(FindRoot("")); got != real(bare) {
		t.Errorf("no marker: %q, want %q", got, bare)
	}
}

// What the framework registered before the app's Routes ran is the framework's.
func TestFrameworkRoutesAreSnapshotted(t *testing.T) {
	isolate(t)
	b, err := Build(context.Background(), shopApp(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, key := range []string{"GET " + web.HTMXPath, "ANY /assets/*"} {
		if !b.FrameworkRoutes[key] {
			t.Errorf("%s is not marked as the framework's: %v", key, b.FrameworkRoutes)
		}
	}
	for key := range b.FrameworkRoutes {
		if strings.Contains(key, "/hello") || strings.Contains(key, "/api") {
			t.Errorf("an app route is marked as the framework's: %s", key)
		}
	}
}

// /dev and /_govibe belong to the framework: an app that registers there fails to boot.
func TestReservedPrefixes(t *testing.T) {
	isolate(t)
	for _, path := range []string{"/dev", "/dev/graph", "/dev/anything/deep", "/_govibe/ping", "/_govibe"} {
		app := App{Routes: func(rt *Runtime, r chi.Router) error { r.Get(path, hello); return nil }}
		_, err := Build(context.Background(), app, Options{})
		if err == nil || !strings.Contains(err.Error(), "reserves") || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: want a boot error naming the path, got %v", path, err)
		}
	}
	// A sub-router counts too.
	app := App{Routes: func(rt *Runtime, r chi.Router) error {
		r.Route("/dev", func(d chi.Router) { d.Get("/x", hello) })
		return nil
	}}
	if _, err := Build(context.Background(), app, Options{}); err == nil || !strings.Contains(err.Error(), "GET /dev/x") {
		t.Errorf("sub-router under /dev: %v", err)
	}
	// Similar names are fine.
	for _, path := range []string{"/devices", "/developers/x", "/_govibes", "/api/dev"} {
		app := App{Routes: func(rt *Runtime, r chi.Router) error { r.Get(path, hello); return nil }}
		if b, err := Build(context.Background(), app, Options{}); err != nil {
			t.Errorf("%s must be allowed: %v", path, err)
		} else {
			b.Close()
		}
	}
}

func TestCommandDeclarationsAreChecked(t *testing.T) {
	isolate(t)
	run := func(context.Context, *Runtime, []string) error { return nil }
	for name, cmds := range map[string][]Command{
		"unnamed":    {{Short: "x", Run: run}},
		"no Run":     {{Use: "a:b"}},
		"duplicate":  {{Use: "a:b", Run: run}, {Use: "a:b", Run: run}},
		"built-in":   {{Use: "serve", Run: run}},
		"built-in 2": {{Use: "migrate", Run: run}},
	} {
		app := App{Commands: func(*Runtime) []Command { return cmds }}
		if _, err := Build(context.Background(), app, Options{}); err == nil {
			t.Errorf("%s: want a boot error", name)
		}
	}
}

// probeApp has one page that says whether it sees the dev marker.
func probeApp() App {
	return App{Routes: func(rt *Runtime, r chi.Router) error {
		r.Get("/probe", func(w http.ResponseWriter, r *http.Request) {
			if web.IsDev(r.Context()) {
				_, _ = w.Write([]byte("dev"))
			} else {
				_, _ = w.Write([]byte("plain"))
			}
		})
		return nil
	}}
}

func TestMarkDevOnlyWhenDevToolsAreMounted(t *testing.T) {
	isolate(t)
	probe := probeApp()

	// prod refuses to mount, even when asked: no marker, no routes.
	b, err := Build(context.Background(), probe, Options{Dev: true})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if code, body, _ := get(t, b.Router, "/probe"); body != "plain" || code != 200 || b.DevMounted {
		t.Fatalf("prod: %d %q mounted=%v", code, body, b.DevMounted)
	}
	if code, _, _ := get(t, b.Router, "/dev/docs/app"); code != 404 {
		t.Fatalf("prod /dev/docs/app = %d", code)
	}

	// dev, not asked for: still plain (read-only commands mount nothing).
	t.Setenv("APP_ENV", "dev")
	b2, err := Build(context.Background(), probe, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	if _, body, _ := get(t, b2.Router, "/probe"); body != "plain" || b2.DevMounted {
		t.Fatalf("dev without Dev option: %q mounted=%v", body, b2.DevMounted)
	}
}

// inspect --json: the offline facts, from the binary itself, with no database.
func TestInspectJSON(t *testing.T) {
	isolate(t)
	// A DSN that cannot work: inspect must not even try to connect.
	t.Setenv("MARIADB_DSN", "nobody:nothing@tcp(127.0.0.1:1)/none")
	var logs bytes.Buffer
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(os.Stderr) })

	app := shopApp()
	inner := app.Routes
	app.Routes = func(rt *Runtime, r chi.Router) error {
		rt.Log.Info("registering routes") // registration chatter must not reach stdout
		return inner(rt, r)
	}

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), app, []string{"inspect", "--json"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Errorf("inspect logged: %q", logs.String())
	}
	dec := json.NewDecoder(&out)
	dec.DisallowUnknownFields()
	var doc Inspect
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not one inspect document: %v\n%s", err, out.String())
	}
	if dec.More() {
		t.Fatal("stdout holds more than the document")
	}
	if doc.Schema != InspectSchema || doc.App.Name != "shop" || !strings.HasPrefix(doc.Go, "go") {
		t.Fatalf("header: %+v", doc)
	}
	if doc.Framework.Module != "github.com/petars-dev/govibe" {
		t.Fatalf("framework module = %q", doc.Framework.Module)
	}
	// The app's Routes function is where the compile-time root is recovered from
	// (here, the wrapper this test installed).
	if !strings.Contains(doc.App.RootHint.Symbol, "TestInspectJSON") || !strings.HasSuffix(doc.App.RootHint.File, "kernel_test.go") || doc.App.RootHint.File == "" || doc.App.RootHint.Line == 0 {
		t.Fatalf("root hint = %+v", doc.App.RootHint)
	}

	byKey := map[string]InspectRoute{}
	for _, r := range doc.Routes {
		byKey[r.Method+" "+r.Path] = r
		if r.Handler.Symbol == "" || r.Handler.File == "" || r.Handler.Line == 0 {
			t.Errorf("route %s %s has no handler: %+v", r.Method, r.Path, r.Handler)
		}
	}
	for key, by := range map[string]string{
		"GET /hello":          "app",
		"GET /api/ping":       "app",
		"GET " + web.HTMXPath: "framework",
		"ANY /assets/*":       "framework",
	} {
		r, ok := byKey[key]
		if !ok || r.MountedBy != by {
			t.Errorf("%s: %+v (found %v), want mounted_by %s", key, r, ok, by)
		}
	}
	if len(byKey["GET /hello"].Middleware) < 4 {
		t.Errorf("the standard middleware stack is missing: %+v", byKey["GET /hello"].Middleware)
	}
	if len(doc.Commands) != 1 || doc.Commands[0].Name != "orders:export" || doc.Commands[0].Handler.Symbol == "" {
		t.Fatalf("commands = %+v", doc.Commands)
	}
	// A test binary carries no module table (a real build does: see the scratch run
	// in the spec), but the document always has the list, never null.
	if doc.Build.Modules == nil || doc.Routes == nil || doc.Commands == nil {
		t.Fatalf("a list is null: %+v", doc)
	}
	if !json.Valid(bytes.TrimSpace([]byte(mustJSON(t, doc)))) {
		t.Fatal("not valid JSON")
	}

	if err := Run(context.Background(), app, []string{"inspect"}, &out, &errOut); err == nil {
		t.Fatal("inspect without --json must say so")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunAppCommandWithFlagsAndArgs(t *testing.T) {
	isolate(t)
	var gotDay string
	var gotArgs []string
	var gotRT *Runtime
	app := App{
		Config: envFS("shop:\n  currency: EUR\n"),
		Commands: func(rt *Runtime) []Command {
			return []Command{{
				Use:   "orders:export",
				Short: "Export orders",
				Flags: func(fs *flag.FlagSet) { fs.StringVar(&gotDay, "day", "yesterday", "the day") },
				Run: func(ctx context.Context, rt *Runtime, args []string) error {
					gotRT, gotArgs = rt, args
					return nil
				},
			}, {
				Use: "fail", Short: "Always fails",
				Run: func(context.Context, *Runtime, []string) error { return errors.New("nope") },
			}}
		},
	}
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), app, []string{"orders:export", "--day", "2026-01-02", "a", "b"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if gotDay != "2026-01-02" || strings.Join(gotArgs, ",") != "a,b" {
		t.Fatalf("day %q args %v", gotDay, gotArgs)
	}
	if gotRT == nil || gotRT.Config.String("shop.currency") != "EUR" || gotRT.DB != nil {
		t.Fatalf("runtime = %+v", gotRT)
	}
	if err := Run(context.Background(), app, []string{"fail"}, &out, &errOut); err == nil || err.Error() != "nope" {
		t.Fatalf("a command's error is Run's: %v", err)
	}
	if err := Run(context.Background(), app, []string{"orders:export", "--nope"}, &out, &errOut); err == nil {
		t.Fatal("an unknown flag is an error")
	}
	if err := Run(context.Background(), app, []string{"missing"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command: %v", err)
	}

	out.Reset()
	if err := Run(context.Background(), app, []string{"--help"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"serve", "migrate", "inspect", "orders:export", "Export orders", "fail"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
}

func TestMigrateNeedsADatabaseAndMigrations(t *testing.T) {
	isolate(t)
	var out, errOut bytes.Buffer
	withMigrations := App{Migrations: fstest.MapFS{"sql/0001_init.sql": {Data: []byte("-- +goose Up\n-- +goose Down\n")}}}
	if err := Run(context.Background(), withMigrations, []string{"migrate", "up"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "no DSN") {
		t.Fatalf("no DSN: %v", err)
	}
	if err := Run(context.Background(), App{}, []string{"migrate", "up"}, &out, &errOut); err == nil || !strings.Contains(err.Error(), "Migrations") {
		t.Fatalf("no Migrations: %v", err)
	}
	for _, args := range [][]string{{"migrate"}, {"migrate", "sideways"}, {"migrate", "up", "extra"}} {
		if err := Run(context.Background(), withMigrations, args, &out, &errOut); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
}

// serve runs until its context ends, then stops gracefully: the app's Shutdown runs
// after the server has stopped and the pool is released.
func TestServeStopsGracefully(t *testing.T) {
	isolate(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:")
	ln.Close()
	t.Setenv("PORT", port)

	var shutdown atomic.Bool
	app := shopApp()
	app.Shutdown = func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("Shutdown got a context that is already done")
		}
		shutdown.Store(true)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, app, nil, io.Discard, io.Discard) }() // no arguments: serve is the default

	var body string
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://127.0.0.1:" + port + "/hello")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server never came up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if body != "hi" {
		t.Fatalf("/hello = %q", body)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
	if !shutdown.Load() {
		t.Fatal("App.Shutdown was not called")
	}
}

func TestServeFailsWhenThePortIsTaken(t *testing.T) {
	isolate(t)
	// The wildcard address, as serve binds it: a listener on loopback alone would not conflict everywhere.
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("PORT", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = Run(ctx, App{}, []string{"serve"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "server") {
		t.Fatalf("a taken port must fail the boot, got %v", err)
	}
}

func TestServeTakesNoArguments(t *testing.T) {
	isolate(t)
	if err := Run(context.Background(), App{}, []string{"serve", "--no-schedule"}, io.Discard, io.Discard); err == nil {
		t.Fatal("serve takes no arguments yet")
	}
}

// A connection that will not finish must not hold the port, the pool or the exit code:
// after the graceful window the server closes it and the stop still succeeds.
func TestServeClosesStuckConnectionsAfterTheGracefulWindow(t *testing.T) {
	isolate(t)
	old := shutdownTimeout
	shutdownTimeout = 300 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = old })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:")
	ln.Close()
	t.Setenv("PORT", port)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	started := make(chan struct{}, 1)
	app := App{Routes: func(rt *Runtime, r chi.Router) error {
		r.Get("/stuck", func(w http.ResponseWriter, r *http.Request) {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		r.Get("/up", hello)
		return nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, app, nil, io.Discard, io.Discard) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get("http://127.0.0.1:" + port + "/up"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the server never came up")
		}
	}
	go func() {
		if resp, err := http.Get("http://127.0.0.1:" + port + "/stuck"); err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the stuck request never arrived")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a stuck connection must not fail the stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop with a connection still open")
	}
}

// An app's Routes written in package main is named "main.f" by the runtime: the hint
// the dev tools use to find the app's sources needs the package's import path.
func TestQualifyMain(t *testing.T) {
	const path = "github.com/acme/shop"
	for in, want := range map[string]string{
		"main.main.newApp.func1":               path + ".main.newApp.func1",
		"main.routes":                          path + ".routes",
		"github.com/acme/shop/routes.Register": "github.com/acme/shop/routes.Register",
		"mainframe.Handler":                    "mainframe.Handler",
		"":                                     "",
	} {
		if got := qualifyMain(in, path); got != want {
			t.Errorf("qualifyMain(%q) = %q, want %q", in, got, want)
		}
	}
	if got := qualifyMain("main.f", ""); got != "main.f" {
		t.Errorf("with no build information the symbol stays as it is, got %q", got)
	}
}

// inspect reports the hint with the same completion, so the tool reading it sees the
// same thing the dev tools do.
func TestRootHintOfARoutesFunction(t *testing.T) {
	app := App{Routes: func(*Runtime, chi.Router) error { return nil }}
	got := rootHint(app)
	if got.File == "" || !strings.Contains(got.Symbol, "kernel.TestRootHintOfARoutesFunction") {
		t.Errorf("hint = %+v", got)
	}
	if rootHint(App{}) != (routeinfo.Ref{}) {
		t.Error("an app with no Routes has no hint")
	}
}

func TestFrameworkVersionShowsALocalReplace(t *testing.T) {
	const module = "github.com/petars-dev/govibe"
	deps := []*debug.Module{{Path: "github.com/go-chi/chi/v5", Version: "v5.1.0"}}
	if got := frameworkVersion(deps, module); got != "(devel)" {
		t.Errorf("no such dependency: %q", got)
	}
	deps = append(deps, &debug.Module{Path: module, Version: "v0.3.1"})
	if got := frameworkVersion(deps, module); got != "v0.3.1" {
		t.Errorf("a release: %q", got)
	}
	deps[1] = &debug.Module{Path: module, Version: "v0.0.0", Replace: &debug.Module{Path: "../.."}}
	if got := frameworkVersion(deps, module); got != "v0.0.0 (replaced by ../..)" {
		t.Errorf("a local checkout: %q", got)
	}
}
