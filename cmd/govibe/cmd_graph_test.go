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
)

// The fixture module the indexer's own tests use: one error finding, on purpose
// (GetOrder calls the store directly), and one declared dependency-free module.
const fixture = "../../business/services/codegraph/testdata/fixture"

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
	out, err = runIndex(t, graphIndexOptions{failOn: "error", baseline: baseline, updateBaseline: true})
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
		entries = append(entries, map[string]string{"id": id})
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
	if len(rep.Report.Layers) != 10 || rep.Report.Quads < 1000 || rep.Gate.FailOn != "error" || len(rep.Gate.New) != 1 || rep.Gate.New[0].Rule != "layering" || rep.Gate.New[0].Path != "controllers/orders.go" {
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
