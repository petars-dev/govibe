package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The fixture module the indexer's own tests use: one error finding, on purpose
// (GetOrder calls the store directly), and one declared dependency-free module.
const fixture = "../../internal/codegraph/testdata/fixture"

func runIndex(t *testing.T, o graphIndexOptions) (string, error) {
	t.Helper()
	logrus.SetLevel(logrus.PanicLevel)
	if o.root == "" {
		o.root = fixture
	}
	if o.dir == "" {
		o.dir = t.TempDir()
	}
	if o.format == "" {
		o.format = "text"
	}
	var out, errOut bytes.Buffer
	err := runGraphIndex(context.Background(), o, &out, &errOut)
	return out.String(), err
}

func exitCode(err error) int {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestGraphIndexReportsAndDoesNotFailWithoutAThreshold(t *testing.T) {
	out, err := runIndex(t, graphIndexOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"graph index:", "layer", "code", "templates", "calls      off", "git        off", "findings: 1 error", "rules that did not run"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gate (") {
		t.Errorf("no threshold, no gate:\n%s", out)
	}
}

func TestGraphIndexGateFailsOnNewFindingsOnly(t *testing.T) {
	baseline := filepath.Join(t.TempDir(), "baseline.json")

	out, err := runIndex(t, graphIndexOptions{failOn: "error"})
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "1 new finding") {
		t.Fatalf("a new error finding fails with status 1: %v", err)
	}
	if !strings.Contains(out, "gate (fail on error): 1 new, 0 accepted") || !strings.Contains(out, "NEW error   layering") || !strings.Contains(out, "controllers/orders.go:") {
		t.Errorf("the finding is listed with where to look:\n%s", out)
	}

	// Accepting what is there makes the same run pass...
	if _, err := runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline, updateBaseline: true}); exitCode(err) != 2 || !strings.Contains(err.Error(), "--reason") {
		t.Fatalf("accepting a finding without saying why is refused: %v", err)
	}
	if _, err := os.Stat(baseline); err == nil {
		t.Error("a refused update wrote the baseline")
	}
	out, err = runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline, updateBaseline: true, reason: "the handler is replaced next sprint"})
	if err != nil || !strings.Contains(out, "baseline: wrote 1 finding") || strings.Contains(out, "gate (") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	out, err = runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline})
	if err != nil || !strings.Contains(out, "gate (fail on error): 0 new, 1 accepted by the baseline") {
		t.Fatalf("with the baseline: %v\n%s", err, out)
	}
	// ...but a stricter threshold finds what the baseline never accepted.
	if _, err := runIndex(t, graphIndexOptions{failOn: "warning", baseline: baseline}); exitCode(err) != 1 {
		t.Errorf("warnings are not in the baseline: %v", err)
	}
	// A baseline that lists something no longer found says so.
	writeBaseline(t, baseline, "finding:layering:pkg:controllers#pkg:storage/orderstore", "finding:gone:somewhere")
	if out, err = runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline}); err != nil || !strings.Contains(out, "1 baseline entry no longer found") {
		t.Errorf("%v\n%s", err, out)
	}
}

func writeBaseline(t *testing.T, path string, ids ...string) {
	t.Helper()
	var entries []map[string]string
	for _, id := range ids {
		entries = append(entries, map[string]string{"id": id, "reason": "accepted for the test"})
	}
	b, _ := json.Marshal(map[string]any{"findings": entries})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGraphIndexFormats(t *testing.T) {
	out, err := runIndex(t, graphIndexOptions{failOn: "error", format: "github"})
	if exitCode(err) != 1 {
		t.Fatal(err)
	}
	if !strings.Contains(out, "::error file=controllers/orders.go,line=") || !strings.Contains(out, ",title=layering::controllers imports storage/orderstore") {
		t.Errorf("a workflow annotation for the new finding:\n%s", out)
	}

	out, err = runIndex(t, graphIndexOptions{failOn: "error", format: "json"})
	if exitCode(err) != 1 {
		t.Fatal(err)
	}
	var rep struct {
		Report struct {
			Layers []struct{ Name, Status string }
			Quads  int
		}
		Gate struct {
			FailOn string `json:"fail_on"`
			New    []struct{ ID, Rule, Path string }
		}
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(rep.Report.Layers) != 11 || rep.Report.Quads < 1000 || rep.Gate.FailOn != "error" || len(rep.Gate.New) != 1 || rep.Gate.New[0].Rule != "layering" || rep.Gate.New[0].Path != "controllers/orders.go" {
		t.Errorf("%+v", rep)
	}
}

func TestGraphIndexOptInLayersAndCalls(t *testing.T) {
	out, err := runIndex(t, graphIndexOptions{calls: true})
	if err != nil || !strings.Contains(out, "calls      ok") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestGraphIndexUsageErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		o    graphIndexOptions
		want string
	}{
		"format":           {graphIndexOptions{format: "xml"}, `unknown --format "xml"`},
		"severity":         {graphIndexOptions{failOn: "fatal"}, "use error, warning, info or none"},
		"update without":   {graphIndexOptions{updateBaseline: true}, "--update-baseline needs --baseline"},
		"missing baseline": {graphIndexOptions{failOn: "error", baseline: filepath.Join(t.TempDir(), "nope.json")}, "does not exist"},
	} {
		if _, err := runIndex(t, tc.o); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

// A root that is not a Go module fails the layers, and that is status 2: the
// project was not checked, which is not the same as checked and clean.
func TestGraphIndexFailsWhenALayerFails(t *testing.T) {
	_, err := runIndex(t, graphIndexOptions{root: t.TempDir(), failOn: "error"})
	if exitCode(err) != 2 || !strings.Contains(err.Error(), "a layer failed") {
		t.Errorf("%v", err)
	}
}

func TestAnAcceptedFindingWithoutAReasonFailsTheGate(t *testing.T) {
	baseline := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"findings":[{"id":"finding:layering:pkg:controllers#pkg:storage/orderstore"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline})
	if exitCode(err) != 1 || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "baseline entries with no reason") || !strings.Contains(out, "0 new, 1 accepted") {
		t.Errorf("the report says which entries need a reason:\n%s", out)
	}
	// without gating nobody is judged
	if _, err := runIndex(t, graphIndexOptions{baseline: baseline}); err != nil {
		t.Errorf("no threshold, no verdict: %v", err)
	}
}

// helloApp is the reference app: a module of its own that depends on the framework.
const helloApp = "../../examples/hello"

func TestGraphIndexOfAnAppRunsTheRouteRulesFromItsOwnBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the example app")
	}
	out, err := runIndex(t, graphIndexOptions{root: helloApp, failOn: "info", calls: true})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"routes     ok", "routes: 5 from the application's own `inspect --json` (2 registered by the framework)",
		"findings: 0 error, 0 warning, 0 info", "gate (fail on info): 0 new, 0 accepted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	// the route rules ran: only the rules about a live database are left out
	if !strings.Contains(out, "did not run (no finding from them is not a clean bill): db.drift, db.unapplied\n") {
		t.Errorf("with the route table, docs.route_undocumented runs:\n%s", out)
	}
}

func TestGraphIndexUsesTheInspectDocumentItIsGiven(t *testing.T) {
	// A fake binary: the app mounts one route of its own and the framework two. The route
	// has a handler the hello sources can resolve, so the rule can look at it.
	doc := &routeinfo.Inspect{
		Schema:    routeinfo.InspectSchema,
		App:       routeinfo.InspectApp{Name: "hello", Module: "github.com/petars-dev/govibe/examples/hello"},
		Framework: routeinfo.InspectModule{Module: "github.com/petars-dev/govibe", Version: "v9.9.9"},
		Routes: []routeinfo.InspectRoute{
			{Method: "GET", Path: "/_govibe/static/htmx.min.js", MountedBy: routeinfo.MountedByFramework},
			{Method: "GET", Path: "/assets/*", MountedBy: routeinfo.MountedByFramework},
			{Method: "GET", Path: "/secret-page", MountedBy: routeinfo.MountedByApp},
		},
	}
	var asked string
	out, err := runIndex(t, graphIndexOptions{root: helloApp, failOn: "info", inspect: func(_ context.Context, root string) (*routeinfo.Inspect, error) {
		asked = root
		return doc, nil
	}})
	if asked != helloApp {
		t.Errorf("the app was asked at %q", asked)
	}
	if exitCode(err) != 1 || !strings.Contains(out, "docs.route_undocumented") || !strings.Contains(out, "GET /secret-page") {
		t.Fatalf("an app route no doc mentions is a finding, from the table the binary reported: %v\n%s", err, out)
	}
	if strings.Contains(out, "/_govibe/static") || strings.Contains(out, "GET /assets") {
		t.Errorf("the framework's routes are documented by the framework, not found wanting in the app:\n%s", out)
	}
	if !strings.Contains(out, "routes: 3 from the application's own `inspect --json` (2 registered by the framework)") {
		t.Errorf("the report says where the routes came from:\n%s", out)
	}
}

func TestGraphIndexSaysWhenTheAppCannotBeAsked(t *testing.T) {
	_, err := runIndex(t, graphIndexOptions{root: helloApp, inspect: func(context.Context, string) (*routeinfo.Inspect, error) {
		return nil, errors.New("go run: build failed: undefined: x")
	}})
	if err == nil || !strings.Contains(err.Error(), "build failed") || !strings.Contains(err.Error(), "--no-inspect") {
		t.Errorf("a build failure is an error (status 2 from the command) that says how to go on without routes: %v", err)
	}
	// and with --no-inspect the app is indexed without a route table, saying so
	out, err := runIndex(t, graphIndexOptions{root: helloApp, noInspect: true, failOn: "info", inspect: func(context.Context, string) (*routeinfo.Inspect, error) {
		t.Fatal("--no-inspect must not run the app")
		return nil, nil
	}})
	if err != nil || !strings.Contains(out, "routes: none: --no-inspect") || !strings.Contains(out, "docs.route_undocumented") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestAProjectThatIsNotAnAppHasNoBinaryToAsk(t *testing.T) {
	out, err := runIndex(t, graphIndexOptions{inspect: func(context.Context, string) (*routeinfo.Inspect, error) {
		t.Fatal("the fixture is not an app")
		return nil, nil
	}})
	if err != nil || !strings.Contains(out, "does not depend on the framework") {
		t.Errorf("%v\n%s", err, out)
	}
}

func TestTheAppIsAskedWithAScrubbedEnvironment(t *testing.T) {
	env := inspectEnv([]string{
		"PATH=/usr/bin", "HOME=/home/x", "GOCACHE=/c", "GOFLAGS=-mod=mod", "GOWORK=off", "LC_ALL=C", "TMPDIR=/tmp",
		"MARIADB_DSN=user:pass@tcp(db)/real", "AWS_SECRET_ACCESS_KEY=s", "GITHUB_TOKEN=t", "WORKOS_API_KEY=k", "CODE_REVIEW_DB_DSN=d",
	})
	got := strings.Join(env, " ")
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/x", "GOCACHE=/c", "GOFLAGS=-mod=mod", "GOWORK=off", "LC_ALL=C", "TMPDIR=/tmp"} {
		if !strings.Contains(got, want) {
			t.Errorf("the toolchain's %s was dropped: %s", want, got)
		}
	}
	for _, secret := range []string{"MARIADB_DSN", "AWS_SECRET", "GITHUB_TOKEN", "WORKOS", "CODE_REVIEW"} {
		if strings.Contains(got, secret) {
			t.Errorf("%s reaches the application's binary: %s", secret, got)
		}
	}
}

func TestAnInvalidProfileFailsBeforeAnythingIsWritten(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "govibe.yml"), []byte("migration: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := graphIndexCmd.Flags().Set("root", root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = graphIndexCmd.Flags().Set("root", ".") })
	graphIndexCmd.SetOut(&bytes.Buffer{})
	graphIndexCmd.SetErr(&bytes.Buffer{})
	err := graphIndexCmd.RunE(graphIndexCmd, nil)
	if exitCode(err) != 2 || !strings.Contains(err.Error(), "govibe.yml") || strings.Count(err.Error(), "graph index:") != 1 {
		t.Errorf("%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(cwd, "docs")); statErr == nil {
		t.Error("a store was created in the working directory before the profile was checked")
	}
}
