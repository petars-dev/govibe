package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The keystone: the framework tests its own output. An app is scaffolded from this
// checkout with the real `govibe new`, a scheduled command is added from the overlay in
// testdata/overlay, and the app is built both ways, tested, gated, booted with and
// without the dev tools and asked the questions of acceptance scenarios A1, A2, A4, A6
// and A7 (SPEC_FRAMEWORK_EXTRACTION). It downloads modules and takes minutes, so it runs
// only with GOVIBE_KEYSTONE set: `make keystone`, and the ci workflow.
func TestKeystone(t *testing.T) {
	if os.Getenv("GOVIBE_KEYSTONE") == "" {
		t.Skip("set GOVIBE_KEYSTONE=1 to scaffold an app from this checkout and exercise it (network, minutes)")
	}
	checkout, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	k := keystone{t: t, checkout: checkout, work: work, app: filepath.Join(work, "shop"), tool: filepath.Join(work, "govibe")}

	t.Log("building the tool from the checkout")
	k.run(checkout, "go", "build", "-o", k.tool, "./cmd/govibe")

	t.Log("govibe new")
	out := k.run(work, k.tool, "new", "github.com/keystone/shop", "--dir", k.app, "--replace", checkout)
	for _, want := range []string{"==> go mod tidy", "==> go tool templ generate", "==> git commit", "==> go tool govibe check", "findings: 0 error, 0 warning, 0 info", "Done."} {
		if !strings.Contains(out, want) {
			t.Errorf("govibe new: output lacks %q", want)
		}
	}
	k.treeIsClean()

	t.Log("overlay: a scheduled command, and the app's own dev tool and page (App.DevTools)")
	o := Options{Module: "github.com/keystone/shop", FrameworkModule: "github.com/petars-dev/govibe", Replace: checkout}
	if err := o.Complete(); err != nil {
		t.Fatal(err)
	}
	overlay := Tree{}
	if err := RenderFS(os.DirFS(filepath.Join("testdata", "overlay")), DataFor(o), overlay); err != nil {
		t.Fatal(err)
	}
	for p, b := range overlay {
		fp := filepath.Join(k.app, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Log("build both ways, vet, test")
	k.run(k.app, "go", "build", "./...")
	k.run(k.app, "go", "build", "-tags", "govibe_dev", "./...")
	k.run(k.app, "go", "vet", "./...")
	out = k.run(k.app, "go", "test", "./...")
	if !strings.Contains(out, "ok  \tgithub.com/keystone/shop\t") {
		t.Errorf("go test: %s", out)
	}

	t.Log("govibe check: no finding at any severity, with the empty baseline (A1, A6)")
	out = k.run(k.app, "go", "tool", "govibe", "check", "--fail-on", "info")
	for _, want := range []string{"managed files: 4 as generated", "findings: 0 error, 0 warning, 0 info", "commands: 1 (1 scheduled)"} {
		if !strings.Contains(out, want) {
			t.Errorf("check: output lacks %q:\n%s", want, out)
		}
	}

	t.Log("the dev build: the demo, the panel, the graph, the MCP server, the schedule (A1, A6)")
	dev := filepath.Join(work, "shop-dev")
	k.run(k.app, "go", "build", "-tags", "govibe_dev", "-o", dev, ".")
	out = k.run(k.app, dev, "schedule", "list")
	if !strings.Contains(out, "greeting:log") || !strings.Contains(out, "*/5 * * * *") {
		t.Errorf("schedule list:\n%s", out)
	}
	k.serve(dev, func(base string) {
		page := k.get(base+"/", 200)
		for _, want := range []string{"Try the htmx demo", `hx-get="/hello"`, `href="/dev/`} {
			if !strings.Contains(page, want) {
				t.Errorf("GET / lacks %q", want)
			}
		}
		if body := k.get(base+"/api/hello", 200); strings.TrimSpace(body) != `{"message":"Hello GoVibe!"}` {
			t.Errorf("GET /api/hello = %q", body)
		}
		if body := k.get(base+"/hello", 200); body != "Hello GoVibe!" {
			t.Errorf("GET /hello = %q", body)
		}
		for _, p := range []string{"/dev", "/dev/graph", "/dev/docs/app", "/dev/docs/govibe/guide", "/dev/schedule", "/dev/mcp"} {
			k.get(base+p, 0)
		}
		k.get(base+"/dev", 200)
		k.get(base+"/dev/docs/govibe-specs", 404) // the framework's specs are never in an app
		var status struct {
			Freshness struct {
				Layers []struct{ Name string } `json:"layers"`
			} `json:"freshness"`
		}
		k.waitJSON(base+"/dev/graph/data/status", &status, func() bool { return len(status.Freshness.Layers) > 0 })
		layers := map[string]bool{}
		for _, l := range status.Freshness.Layers {
			layers[l.Name] = true
		}
		if !layers["commands"] || !layers["routes"] {
			t.Errorf("graph layers: %+v", status.Freshness.Layers)
		}
		var schema struct {
			Scope     string                   `json:"scope"`
			App       struct{ Name string }    `json:"app"`
			Framework struct{ Version string } `json:"framework"`
		}
		k.mcp(base, "graph_schema", `{}`, &schema)
		if schema.Scope != "app" || schema.App.Name != "shop" || schema.Framework.Version == "" {
			t.Errorf("graph_schema = %+v", schema)
		}
		var sched struct {
			State string                  `json:"state"`
			Tasks []struct{ Name string } `json:"tasks"`
		}
		k.mcp(base, "schedule_status", `{}`, &sched)
		if len(sched.Tasks) != 1 || sched.Tasks[0].Name != "greeting:log" {
			t.Errorf("schedule_status = %+v", sched)
		}
		// The app's own dev tool and page (T7.3): the page under /dev/app, linked from /dev;
		// the tool on /dev/mcp, answering with the app's code; and the tool in the app's graph,
		// registered by the app's function.
		if page := k.get(base+"/dev/app/greeting", 200); !strings.Contains(page, "Hello GoVibe!") {
			t.Errorf("GET /dev/app/greeting:\n%s", page)
		}
		if index := k.get(base+"/dev", 200); !strings.Contains(index, `href="/dev/app/greeting"`) || !strings.Contains(index, "greeting_preview") {
			t.Error("/dev does not list the app's dev page and tool")
		}
		var greeting struct{ Message string }
		k.mcp(base, "greeting_preview", `{}`, &greeting)
		if greeting.Message != "Hello GoVibe!" {
			t.Errorf("greeting_preview = %+v", greeting)
		}
		var toolCtx struct {
			Node struct {
				ID     string `json:"id"`
				Kind   string `json:"kind"`
				Origin string `json:"origin"`
			} `json:"node"`
		}
		k.mcp(base, "graph_context", `{"node":"tool:greeting_preview"}`, &toolCtx)
		if toolCtx.Node.ID != "tool:greeting_preview" || toolCtx.Node.Kind != "mcp_tool" || toolCtx.Node.Origin != "app" {
			t.Errorf("graph_context on the app's tool = %+v", toolCtx)
		}
	})

	t.Log("the production build: no dev tooling, no dev link (A7)")
	prod := filepath.Join(work, "shop-prod")
	k.run(k.app, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", prod, ".")
	k.serve(prod, func(base string) {
		page := k.get(base+"/", 200)
		if strings.Contains(page, `href="/dev`) {
			t.Error("the production page links to /dev")
		}
		for _, p := range []string{"/dev", "/dev/graph", "/dev/docs", "/dev/mcp", "/dev/schedule", "/dev/app/greeting", "/_govibe/static/graph.js", "/assets/static/graph.js"} {
			k.get(base+p, 404)
		}
		k.get(base+"/api/hello", 200)
		k.get(base+"/assets/static/home.css", 200)
		k.get(base+"/_govibe/static/htmx.min.js", 200)
	})
	deps := k.run(k.app, "go", "list", "-deps", "-tags", "", ".")
	if m := devOnly.FindString(deps); m != "" {
		t.Errorf("the default build links %s", m)
	}

	t.Log("the rules hold: framework.import and layering fail the check (A4)")
	bad := filepath.Join(k.app, "business", "actions", "bad.go")
	os.WriteFile(bad, []byte("package actions\n\nimport \"github.com/petars-dev/govibe/web\"\n\nvar _ = web.HTMXPath\n"), 0o644)
	out, code := k.try(k.app, "go", "tool", "govibe", "check")
	if code != 1 || !strings.Contains(out, "framework.import") {
		t.Errorf("a business package importing govibe/web: exit %d\n%s", code, out)
	}
	os.Remove(bad)
	store := filepath.Join(k.app, "storage", "badstore")
	os.MkdirAll(store, 0o755)
	os.WriteFile(filepath.Join(store, "store.go"), []byte("package badstore\n\n// List lists nothing.\nfunc List() []string { return nil }\n"), 0o644)
	badCtl := filepath.Join(k.app, "controllers", "bad.go")
	os.WriteFile(badCtl, []byte("package controllers\n\nimport \"github.com/keystone/shop/storage/badstore\"\n\nvar _ = badstore.List\n"), 0o644)
	out, code = k.try(k.app, "go", "tool", "govibe", "check")
	if code != 1 || !strings.Contains(out, "layering") {
		t.Errorf("a controller importing a store: exit %d\n%s", code, out)
	}
	os.Remove(badCtl)
	os.RemoveAll(store)

	t.Log("generators: a resource, a scheduled command and a page leave the gate green (T6.1)")
	readme, _ := os.ReadFile(filepath.Join(k.app, "README.md"))
	k.run(k.app, "go", "tool", "govibe", "make", "resource", "order")
	k.run(k.app, "go", "tool", "govibe", "make", "command", "items:prune", "--schedule", "0 3 * * *", "--short", "Delete archived items")
	out = k.run(k.app, "go", "tool", "govibe", "make", "page", "about")
	if !strings.Contains(out, "==> go tool templ generate") {
		t.Errorf("make page did not run templ:\n%s", out)
	}
	k.run(k.app, "go", "build", "./...")
	k.run(k.app, "go", "build", "-tags", "govibe_dev", "./...")
	k.run(k.app, "go", "vet", "./...")
	out = k.run(k.app, "go", "test", "./...")
	for _, pkg := range []string{"github.com/keystone/shop\t", "github.com/keystone/shop/business/services/orders", "github.com/keystone/shop/storage/orderstore"} {
		if !strings.Contains(out, "ok  \t"+pkg) {
			t.Errorf("go test after the generators: no ok for %s:\n%s", pkg, out)
		}
	}
	out = k.run(k.app, "go", "tool", "govibe", "check", "--fail-on", "info")
	for _, want := range []string{"findings: 0 error, 0 warning, 0 info", "commands: 2 (2 scheduled)"} {
		if !strings.Contains(out, want) {
			t.Errorf("check after the generators: output lacks %q:\n%s", want, out)
		}
	}
	k.run(k.app, "go", "build", "-tags", "govibe_dev", "-o", dev, ".")
	out = k.run(k.app, dev, "schedule", "list")
	if !strings.Contains(out, "items:prune") || !strings.Contains(out, "0 3 * * *") {
		t.Errorf("schedule list after make command:\n%s", out)
	}
	k.serve(dev, func(base string) {
		if page := k.get(base+"/about", 200); !strings.Contains(page, "<h1>About</h1>") {
			t.Errorf("GET /about:\n%s", page)
		}
		// No DSN: the generated store says so instead of panicking.
		if body := k.get(base+"/api/orders", 500); !strings.Contains(body, "no database is configured") {
			t.Errorf("GET /api/orders = %q", body)
		}
		k.get(base+"/api/orders/x", 400)
		k.get(base+"/api/hello", 200)
	})

	t.Log("upgrade: managed files rewritten, an edited one refused then forced, owned files untouched (A5, T6.2)")
	out = k.run(k.app, "go", "tool", "govibe", "upgrade")
	if !strings.Contains(out, "up to date") {
		t.Errorf("upgrade with nothing to do:\n%s", out)
	}
	mk, _ := os.ReadFile(filepath.Join(k.app, "Makefile"))
	os.WriteFile(filepath.Join(k.app, "Makefile"), append(mk, "\nlint:\n\ttrue\n"...), 0o644)
	out, code = k.try(k.app, "go", "tool", "govibe", "upgrade")
	if code != 1 || !strings.Contains(out, "edited    Makefile") || !strings.Contains(out, "-lint:") {
		t.Errorf("upgrade over an edited Makefile: exit %d\n%s", code, out)
	}
	out = k.run(k.app, "go", "tool", "govibe", "upgrade", "--force")
	if !strings.Contains(out, "forced    Makefile") {
		t.Errorf("upgrade --force:\n%s", out)
	}
	if mk, _ = os.ReadFile(filepath.Join(k.app, "Makefile")); strings.Contains(string(mk), "lint:") {
		t.Error("upgrade --force kept the edit")
	}
	if now, _ := os.ReadFile(filepath.Join(k.app, "README.md")); !bytes.Equal(now, readme) {
		t.Error("README.md, an owned file, was changed")
	}
	out = k.run(k.app, "go", "tool", "govibe", "check")
	if !strings.Contains(out, "managed files: 4 as generated") {
		t.Errorf("check after the upgrade:\n%s", out)
	}

	t.Log("doctor: the build checks on the real app")
	out = k.run(k.app, "go", "tool", "govibe", "doctor")
	for _, want := range []string{"note  framework  go.mod replaces", "ok    managed    4 files as generated", "ok    inspect    the app declares", "ok    embeds", "ok    build      the default build links no dev tooling", "0 failures"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor: output lacks %q:\n%s", want, out)
		}
	}
}

// devOnly names what only the dev tools may link (prod_test.go of the demo says the same).
var devOnly = regexp.MustCompile(`cayleygraph|modelcontextprotocol|golang\.org/x/tools|yuin/goldmark|alecthomas/chroma|petars-dev/govibe/internal/(devtools|codegraph|dbexplorer|devmcp|graphdb)`)

type keystone struct {
	t                         *testing.T
	checkout, work, app, tool string
}

// env is a clean environment: the toolchain's variables and the path, no secret and no
// DSN of the shell that runs the test, and no workspace (the app is a module of its own).
func (k *keystone) env() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "GOWORK":
			continue
		case strings.HasPrefix(name, "GO"), strings.HasPrefix(name, "LC_"), strings.HasPrefix(name, "CGO_"):
		case name == "PATH", name == "HOME", name == "USER", name == "LANG", name == "TMPDIR", name == "TEMP", name == "TMP",
			name == "SystemRoot", name == "SYSTEMROOT", name == "USERPROFILE", name == "LOCALAPPDATA", name == "APPDATA", name == "XDG_CACHE_HOME",
			name == "SSL_CERT_FILE", name == "SSL_CERT_DIR", name == "HTTPS_PROXY", name == "HTTP_PROXY", name == "NO_PROXY", name == "RUNNER_TEMP":
		default:
			continue
		}
		out = append(out, kv)
	}
	return append(out, "GOWORK=off")
}

// run runs a command in dir and fails the test if it fails.
func (k *keystone) run(dir string, args ...string) string {
	k.t.Helper()
	out, code := k.try(dir, args...)
	if code != 0 {
		k.t.Fatalf("%s (in %s) exited %d:\n%s", strings.Join(args, " "), dir, code, out)
	}
	return out
}

// try runs a command in dir and returns its output and exit status.
func (k *keystone) try(dir string, args ...string) (string, int) {
	k.t.Helper()
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	c.Env = k.env()
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		k.t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, buf.String())
	}
	return buf.String(), code
}

// treeIsClean checks the generated tree: the app repository is clean (A2).
func (k *keystone) treeIsClean() {
	k.t.Helper()
	var files []string
	filepath.WalkDir(k.app, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(k.app, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	for _, f := range files {
		base := filepath.Base(f)
		switch {
		case strings.HasPrefix(base, "SPEC_"):
			k.t.Errorf("the tree has a spec: %s", f)
		case base == "graph.js", base == "cytoscape.min.js", base == "htmx.min.js", strings.HasPrefix(f, "internal/"), strings.HasPrefix(f, "config/"):
			k.t.Errorf("the tree has a file of the framework: %s", f)
		}
	}
	env, _ := os.ReadFile(filepath.Join(k.app, "env.yml"))
	if strings.Contains(strings.ToLower(string(env)), "mail") || strings.Contains(strings.ToLower(string(env)), "workos") {
		k.t.Error("env.yml has mail or WorkOS keys")
	}
	main, _ := os.ReadFile(filepath.Join(k.app, "main.go"))
	if n := bytes.Count(main, []byte("\n")); n >= 30 {
		k.t.Errorf("main.go has %d lines", n)
	}
	if out := k.run(k.app, "git", "status", "--porcelain"); strings.TrimSpace(out) != "" {
		k.t.Errorf("the first commit does not cover the tree (templ generate changed it?):\n%s", out)
	}
}

// serve boots bin on a free port under APP_ENV=dev, calls f with the base URL and stops it.
func (k *keystone) serve(bin string, f func(base string)) {
	k.t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		k.t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	c := exec.Command(bin)
	c.Dir = k.app
	c.Env = append(k.env(), "APP_ENV=dev", "LOG_LEVEL=warn", fmt.Sprintf("PORT=%d", port))
	var log bytes.Buffer
	c.Stdout, c.Stderr = &log, &log
	if err := c.Start(); err != nil {
		k.t.Fatal(err)
	}
	defer func() {
		_ = c.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			_ = c.Process.Kill()
			<-done
		}
		if k.t.Failed() {
			k.t.Logf("%s log:\n%s", filepath.Base(bin), log.String())
		}
	}()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		resp, err := http.Get(base + "/api/hello")
		if err == nil {
			resp.Body.Close()
			break
		}
		select {
		case <-ctx.Done():
			k.t.Fatalf("%s did not come up on %s:\n%s", bin, base, log.String())
		case <-time.After(200 * time.Millisecond):
		}
	}
	f(base)
}

// get fetches url and checks the status (0: any).
func (k *keystone) get(url string, want int) string {
	k.t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		k.t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if want != 0 && resp.StatusCode != want {
		k.t.Errorf("GET %s = %d, want %d", url, resp.StatusCode, want)
	}
	return string(b)
}

// waitJSON fetches url into v until ok says so (the graph is indexed at boot, in the background).
func (k *keystone) waitJSON(url string, v any, ok func() bool) {
	k.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		body := k.get(url, 200)
		if err := json.Unmarshal([]byte(body), v); err == nil && ok() {
			return
		}
		if time.Now().After(deadline) {
			k.t.Fatalf("GET %s never answered as expected; last: %s", url, body)
		}
		time.Sleep(time.Second)
	}
}

// mcp calls a tool over /dev/mcp and decodes its structured content into v. A new
// server indexes the graph at boot; a tool that answers with an error is retried.
func (k *keystone) mcp(base, tool, args string, v any) {
	k.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, args)
		req, _ := http.NewRequest("POST", base+"/dev/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			k.t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		text := string(raw)
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "data:") {
				text = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				break
			}
		}
		var env struct {
			Result struct {
				IsError           bool            `json:"isError"`
				StructuredContent json.RawMessage `json:"structuredContent"`
			} `json:"result"`
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(text), &env); err == nil && env.Error == nil && !env.Result.IsError && len(env.Result.StructuredContent) > 0 {
			if err := json.Unmarshal(env.Result.StructuredContent, v); err != nil {
				k.t.Fatalf("%s: %v: %s", tool, err, env.Result.StructuredContent)
			}
			return
		}
		if time.Now().After(deadline) {
			k.t.Fatalf("%s over /dev/mcp never answered; last: %s", tool, raw)
		}
		time.Sleep(time.Second)
	}
}
