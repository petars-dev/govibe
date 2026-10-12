package codegraph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture has one error finding, on purpose: GetOrder calls the store directly.
const fixtureViolation = "finding:layering:pkg:controllers#pkg:storage/orderstore"

func TestAllFindingsAreNotCutAndCarryWhereToLook(t *testing.T) {
	m := fixtureGraph(t)
	all := m.AllFindings()
	if len(all) != len(m.byKind["finding"]) || len(all) < 5 {
		t.Fatalf("%d findings", len(all))
	}
	if all[0].ID != fixtureViolation || all[0].Severity != "error" {
		t.Errorf("worst first: %+v", all[0])
	}
	if all[0].Path != "controllers/orders.go" || all[0].Line == 0 {
		t.Errorf("a layering finding says where the import is used: %+v", all[0])
	}
}

func TestGateFailsOnANewFindingAndAcceptsABaselinedOne(t *testing.T) {
	m := fixtureGraph(t)

	res, err := m.Gate(GateOptions{FailOn: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() || len(res.New) != 1 || res.New[0].ID != fixtureViolation || len(res.Accepted) != 0 {
		t.Fatalf("%+v", res)
	}
	if res.BySeverity["error"] != 1 || res.BySeverity["warning"] == 0 {
		t.Errorf("counts cover every finding: %v", res.BySeverity)
	}

	res, err = m.Gate(GateOptions{FailOn: "error", Baseline: map[string]bool{fixtureViolation: true, "finding:gone:node": true}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() || len(res.New) != 0 || len(res.Accepted) != 1 {
		t.Errorf("a baselined finding does not fail: %+v", res)
	}
	if len(res.Fixed) != 1 || res.Fixed[0] != "finding:gone:node" {
		t.Errorf("a baseline entry no finding matches is debt paid: %v", res.Fixed)
	}
}

func TestGateThresholds(t *testing.T) {
	m := fixtureGraph(t)
	counts := func(failOn string) (int, bool) {
		res, err := m.Gate(GateOptions{FailOn: failOn})
		if err != nil {
			t.Fatalf("%q: %v", failOn, err)
		}
		return len(res.New), res.Failed()
	}
	if n, failed := counts("none"); n != 0 || failed {
		t.Errorf("none never fails: %d", n)
	}
	if n, failed := counts(""); n != 0 || failed {
		t.Errorf("no threshold never fails: %d", n)
	}
	errs, _ := counts("error")
	warns, _ := counts("warning")
	infos, _ := counts("INFO")
	if !(errs < warns && warns < infos) || infos != len(m.AllFindings()) {
		t.Errorf("each threshold takes in the milder severities: error %d warning %d info %d of %d", errs, warns, infos, len(m.AllFindings()))
	}
	if _, err := m.Gate(GateOptions{FailOn: "fatal"}); err == nil || !strings.Contains(err.Error(), "use error, warning, info or none") {
		t.Errorf("an unknown severity is an error: %v", err)
	}
}

// With no findings layer nothing has been checked: that is an error, not a pass.
func TestGateRefusesAGraphThatWasNotChecked(t *testing.T) {
	if _, err := modelWithout(t, "findings").Gate(GateOptions{FailOn: "error"}); err == nil || !strings.Contains(err.Error(), "nothing was checked") {
		t.Errorf("err = %v", err)
	}
}

func TestGateSaysWhichRulesDidNotRun(t *testing.T) {
	res, err := fixtureGraph(t).Gate(GateOptions{FailOn: "error"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range res.NotRun {
		names = append(names, r.Name)
		if r.Note == "" {
			t.Errorf("%s did not run and does not say why", r.Name)
		}
	}
	// the fixture has no live database and (in this graph) no router
	mustHave(t, "rules that did not run", names, "db.drift", "db.unapplied")
}

func TestBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "baseline.json")
	m := fixtureGraph(t)
	errs, err := AtOrAbove(m.AllFindings(), "error")
	if err != nil || len(errs) != 1 {
		t.Fatalf("%v %v", errs, err)
	}
	if n, err := WriteBaseline(path, append(errs, FindingRef{ID: "finding:a:b", Rule: "a", Severity: "high", Message: "m"})); err != nil || n != 2 {
		t.Fatalf("write: %d %v", n, err)
	}
	ids, err := ReadBaseline(path)
	if err != nil || len(ids) != 2 || !ids[fixtureViolation] || !ids["finding:a:b"] {
		t.Fatalf("read: %v %v", ids, err)
	}
	text, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(text), "}\n") || strings.Index(string(text), "finding:a:b") > strings.Index(string(text), fixtureViolation) {
		t.Errorf("entries are sorted by ID, and the file ends in a newline:\n%s", text)
	}
	if !strings.Contains(string(text), `"severity": "error"`) || !strings.Contains(string(text), "--update-baseline") {
		t.Errorf("the file explains itself and names the severity:\n%s", text)
	}

	if _, err := ReadBaseline(filepath.Join(t.TempDir(), "missing.json")); err == nil || !strings.Contains(err.Error(), "--update-baseline") {
		t.Errorf("a missing baseline is an error that says how to make one: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	for name, content := range map[string]string{"not json": "nope", "no id": `{"findings":[{"rule":"x"}]}`} {
		if err := os.WriteFile(bad, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadBaseline(bad); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := AtOrAbove(nil, "x"); err == nil {
		t.Error("AtOrAbove checks the severity")
	}
}
