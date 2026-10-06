package codegraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The findings gate: `govibe graph index --fail-on error` turns the findings the
// graph holds into an exit status, so CI can refuse a change that adds a layering
// violation. Only a *new* finding fails it: a project is rarely clean on the day
// the gate is switched on, and a gate that fails on day one gets switched off. What
// is already known is written to a baseline file (a list of finding IDs, kept in
// the repository), reviewed like any other change, and shrinks as the debt is paid.

// GateOptions say what the gate fails on.
type GateOptions struct {
	// FailOn is the least severe finding that fails the gate: "error", "warning" or
	// "info" (every finding). Empty or "none" never fails.
	FailOn string
	// Baseline holds the IDs of findings that are accepted.
	Baseline map[string]bool
}

// GateResult is the verdict.
type GateResult struct {
	FailOn string `json:"fail_on"`
	// New are the findings at or above FailOn that the baseline does not accept:
	// what fails the gate.
	New []FindingRef `json:"new"`
	// Accepted are the findings at or above FailOn that the baseline accepts.
	Accepted []FindingRef `json:"accepted"`
	// Fixed are baseline entries no finding has any more: debt that has been paid,
	// which the baseline can drop.
	Fixed []string `json:"fixed,omitempty"`
	// BySeverity counts every finding, whatever the threshold.
	BySeverity map[string]int `json:"by_severity"`
	// NotRun are the rules that did not run on this graph, with why. A rule that
	// did not run has not looked: no finding from it is not a clean bill.
	NotRun []RuleInfo `json:"not_run,omitempty"`
}

// Failed reports whether the gate fails: a finding at or above the threshold that
// nobody accepted.
func (r *GateResult) Failed() bool { return len(r.New) > 0 }

// severityName is a finding's severity normalised to error, warning or info.
func severityName(s string) string {
	return [...]string{sevError, sevWarning, sevInfo, ""}[severityRank(s)]
}

// failRank is how severe a finding must be to fail: 0 error, 1 warning, 2 info,
// and -1 for a threshold that never fails.
func failRank(failOn string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(failOn)) {
	case "", "none", "never":
		return -1, nil
	case sevError:
		return 0, nil
	case sevWarning:
		return 1, nil
	case sevInfo:
		return 2, nil
	}
	return 0, fmt.Errorf("unknown severity %q: use error, warning, info or none", failOn)
}

// AllFindings lists every finding of the graph, worst first. Unlike graph_findings
// it is not cut at a limit: a gate must see all of them.
func (m *Model) AllFindings() []FindingRef {
	out := make([]FindingRef, 0, len(m.byKind["finding"]))
	for _, f := range m.byKind["finding"] {
		out = append(out, m.findingRef(f))
	}
	sortFindings(out)
	return out
}

// Gate judges the graph's findings against a threshold and a baseline. It fails
// when the findings layer is not built: a graph with no findings layer has not been
// checked at all, and must not read as clean.
func (m *Model) Gate(o GateOptions) (*GateResult, error) {
	rank, err := failRank(o.FailOn)
	if err != nil {
		return nil, err
	}
	if !m.layerBuilt("findings") {
		return nil, errors.New("the findings layer is not built, so nothing was checked")
	}
	res := &GateResult{FailOn: strings.ToLower(strings.TrimSpace(o.FailOn)), BySeverity: map[string]int{}}
	if res.FailOn == "" {
		res.FailOn = "none"
	}
	present := map[string]bool{}
	for _, f := range m.AllFindings() {
		present[f.ID] = true
		res.BySeverity[severityName(f.Severity)]++
		if rank < 0 || severityRank(f.Severity) > rank {
			continue
		}
		if o.Baseline[f.ID] {
			res.Accepted = append(res.Accepted, f)
		} else {
			res.New = append(res.New, f)
		}
	}
	for id := range o.Baseline {
		if !present[id] {
			res.Fixed = append(res.Fixed, id)
		}
	}
	sort.Strings(res.Fixed)
	for _, r := range m.ruleInfos(nil) {
		if !r.Checked {
			res.NotRun = append(res.NotRun, r)
		}
	}
	return res, nil
}

// baselineFile is the file format of a baseline. Only the IDs matter to the
// gate; the rule and the message are there so that a review of the file shows what
// is being accepted.
type baselineFile struct {
	Comment  string          `json:"comment"`
	Findings []baselineEntry `json:"findings"`
}

type baselineEntry struct {
	ID       string `json:"id"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

const baselineComment = "Findings accepted as known debt by `govibe graph index --baseline`. A finding listed here does not fail the gate; one that is not listed does. " +
	"Regenerate with `govibe graph index --update-baseline` after fixing or deliberately accepting findings, and review the diff."

// ReadBaseline reads the IDs of a baseline file. A file that does not exist is an
// error, not an empty baseline: a typo in the path must not accept nothing silently
// and fail every finding, or (worse) the other way round.
func ReadBaseline(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("baseline %s does not exist (create it with --update-baseline)", path)
		}
		return nil, err
	}
	var f baselineFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("baseline %s: %w", path, err)
	}
	ids := make(map[string]bool, len(f.Findings))
	for _, e := range f.Findings {
		if e.ID == "" {
			return nil, fmt.Errorf("baseline %s: an entry has no id", path)
		}
		ids[e.ID] = true
	}
	return ids, nil
}

// WriteBaseline writes the findings as a baseline, sorted by ID so that the file
// diffs cleanly, and returns how many it holds.
func WriteBaseline(path string, findings []FindingRef) (int, error) {
	f := baselineFile{Comment: baselineComment, Findings: []baselineEntry{}}
	for _, x := range findings {
		f.Findings = append(f.Findings, baselineEntry{ID: x.ID, Rule: x.Rule, Severity: severityName(x.Severity), Message: x.Message})
	}
	sort.Slice(f.Findings, func(i, j int) bool { return f.Findings[i].ID < f.Findings[j].ID })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	return len(f.Findings), os.WriteFile(path, append(data, '\n'), 0o644)
}

// AtOrAbove returns the findings at or above a severity, as the baseline of that
// threshold should hold them.
func AtOrAbove(findings []FindingRef, failOn string) ([]FindingRef, error) {
	rank, err := failRank(failOn)
	if err != nil {
		return nil, err
	}
	var out []FindingRef
	for _, f := range findings {
		if rank >= 0 && severityRank(f.Severity) <= rank {
			out = append(out, f)
		}
	}
	return out, nil
}
