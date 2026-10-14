package codegraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// findingsLayer derives findings from the other layers. It is built last, from
// what they wrote to the store (the same read the agent tools make), so a rule
// sees exactly the graph a user or an agent sees, and any edit to any input
// makes it stale: it depends on every other layer and has no inputs of its own.
// See findings.go for the rules and the contract.
type findingsLayer struct{}

func (findingsLayer) name() string { return "findings" }

func (findingsLayer) deps() []string {
	return []string{"code", "routes", "templates", "config", "mcp", "db", "docs", "externals"}
}

// fingerprint is a constant: nothing but the other layers feeds the rules. A
// change to the rules themselves ships with a new IndexerVersion, and a restart
// builds every layer.
func (findingsLayer) fingerprint(*world) (string, error) { return "derived", nil }

func (findingsLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	qs := w.x.writer.qs
	if qs == nil {
		return nil, errors.New("no graph store to read the other layers from")
	}
	prof, err := w.profile()
	if err != nil {
		return nil, err
	}
	if err := checkRuleSettings(prof); err != nil {
		return nil, err
	}
	m, err := loadModel(ctx, qs, "findings")
	if err != nil {
		return nil, err
	}
	env := &ruleEnv{
		m:     m,
		prof:  prof,
		templ: w.templText(),
		built: func(layer string) bool {
			// What this run (or an earlier one) recorded: the meta layer is only
			// written at the end of a run, so the store itself is a step behind.
			st := w.x.state[layer].Status
			return st == statusOK || st == statusPartial
		},
	}
	if ci, err := w.code(); err == nil {
		env.ci = ci
	}
	res := runRules(env)
	return &layerOutput{quads: res.quads, warnings: res.warnings}, nil
}

// templText is the text of every .templ file, which calls Go functions the code
// index cannot see (their generated Go is skipped). Unreadable files are left out.
func (w *world) templText() string {
	ti, err := w.templates()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, f := range ti.files {
		data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(f.rel)))
		if err != nil || len(data) > maxTemplFileBytes {
			continue
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
