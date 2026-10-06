package codegraph

import (
	"sort"
	"strings"
	"time"
)

// What the graph page shows about one node when you click it: its attributes, its
// edges grouped by predicate, the findings about it, and where it can be opened.

// Attr is one attribute of a node. A value is a string, a number, a boolean, a
// time (RFC 3339) or, for an attribute that occurs more than once, a list.
type Attr struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// EdgeGroup is the edges of one predicate at a node, in one direction.
type EdgeGroup struct {
	Predicate string    `json:"predicate"`
	Total     int       `json:"total"`
	Nodes     []NodeRef `json:"nodes"`
	Truncated bool      `json:"truncated,omitempty"`
}

// Inspector is the whole picture of one node.
type Inspector struct {
	Node     NodeRef      `json:"node"`
	Label    string       `json:"label"`
	Summary  string       `json:"summary,omitempty"`
	Attrs    []Attr       `json:"attrs"`
	Out      []EdgeGroup  `json:"out"`
	In       []EdgeGroup  `json:"in"`
	Findings []FindingRef `json:"findings"`
	Links    []Link       `json:"links"`
	AsOf     string       `json:"as_of"`
}

// maxGroupNodes bounds how many nodes of one predicate the inspector lists.
const maxGroupNodes = 25

// attrFirst are the attributes shown before the rest, in this order.
var attrFirst = []string{"name", "path", "line", "title", "status", "doc", "description", "message", "signature", "severity", "rule"}

// Inspect describes the node a reference names.
func (m *Model) Inspect(ref string, o LinkOptions) (*Inspector, error) {
	n, err := m.resolveOne(ref)
	if err != nil {
		return nil, err
	}
	in := &Inspector{
		Node: m.Ref(n), Label: viewLabel(m, n), Summary: m.summary(n),
		Attrs: []Attr{}, Out: []EdgeGroup{}, In: []EdgeGroup{}, Findings: []FindingRef{},
		Links: m.Links(n, o), AsOf: m.AsOf(),
	}

	names := make([]string, 0, len(n.Attrs))
	for name := range n.Attrs {
		names = append(names, name)
	}
	rank := map[string]int{}
	for i, a := range attrFirst {
		rank[a] = i + 1
	}
	sort.Slice(names, func(i, j int) bool {
		ri, rj := rank[names[i]], rank[names[j]]
		if ri != rj {
			if ri == 0 || rj == 0 {
				return rj == 0
			}
			return ri < rj
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		vals := n.Attrs[name]
		a := Attr{Name: name}
		conv := func(v any) any {
			if t, ok := v.(time.Time); ok {
				return t.Format(time.RFC3339)
			}
			return v
		}
		if len(vals) == 1 {
			a.Value = conv(vals[0])
		} else {
			list := make([]any, len(vals))
			for i, v := range vals {
				list[i] = conv(v)
			}
			a.Value = list
		}
		in.Attrs = append(in.Attrs, a)
	}

	showBook := bookkeeping(n.Kind)
	group := func(edges map[string][]string) []EdgeGroup {
		var out []EdgeGroup
		preds := make([]string, 0, len(edges))
		for p := range edges {
			preds = append(preds, p)
		}
		sort.Strings(preds)
		for _, p := range preds {
			var nodes []*Node
			for _, id := range edges[p] {
				if t := m.nodes[id]; t != nil && (showBook || !bookkeeping(t.Kind)) {
					nodes = append(nodes, t)
				}
			}
			if len(nodes) == 0 {
				continue
			}
			sortNodes(nodes)
			g := EdgeGroup{Predicate: p, Total: len(nodes)}
			if len(nodes) > maxGroupNodes {
				nodes, g.Truncated = nodes[:maxGroupNodes], true
			}
			g.Nodes = m.refs(nodes)
			out = append(out, g)
		}
		return out
	}
	in.Out, in.In = group(n.Out), group(n.In)
	if in.Out == nil {
		in.Out = []EdgeGroup{}
	}
	if in.In == nil {
		in.In = []EdgeGroup{}
	}
	// A finding is about its node; a finding node's own edges say what it is about.
	for _, f := range m.findingNodesAbout(n.ID) {
		in.Findings = append(in.Findings, m.findingRef(f))
	}
	sortFindings(in.Findings)
	return in, nil
}

// --- status: the freshness banner ---

// FindingCounts is how many findings there are, and whether to trust "none".
type FindingCounts struct {
	Available bool     `json:"available"`
	Total     int      `json:"total"`
	Error     int      `json:"error"`
	Warning   int      `json:"warning"`
	Info      int      `json:"info"`
	NotRun    []string `json:"not_run,omitempty"` // rules that did not run
}

// Status is what the banner of the graph page shows.
type Status struct {
	Freshness Freshness     `json:"freshness"`
	Findings  FindingCounts `json:"findings"`
	Notes     []string      `json:"notes,omitempty"`
}

// Status summarises how fresh the graph is and what its findings add up to. stale
// is what the indexer reports as stale (the caller owns the indexer).
func (m *Model) Status(stale []string) *Status {
	s := &Status{Freshness: m.Freshness()}
	s.Freshness.StaleLayers = stale
	if !s.Freshness.Indexed {
		s.Notes = append(s.Notes, "The graph has not been indexed yet: reindex, or retry in a few seconds after a boot.")
		return s
	}
	if m.layerBuilt("findings") {
		s.Findings.Available = true
		for _, f := range m.byKind["finding"] {
			s.Findings.Total++
			switch severityRank(f.Str("severity")) {
			case 0:
				s.Findings.Error++
			case 1:
				s.Findings.Warning++
			default:
				s.Findings.Info++
			}
		}
		for _, r := range m.byKind["rule"] {
			if !r.Bool("checked") {
				s.Findings.NotRun = append(s.Findings.NotRun, r.Str("name"))
			}
		}
		sort.Strings(s.Findings.NotRun)
	}
	for _, l := range s.Freshness.Layers {
		switch l.Status {
		case statusError:
			s.Notes = append(s.Notes, "The "+l.Name+" layer failed: "+l.Error)
		case statusSkipped:
			s.Notes = append(s.Notes, "The "+l.Name+" layer was skipped: "+l.Note)
		}
	}
	if len(stale) > 0 {
		s.Notes = append(s.Notes, "Stale layers: "+strings.Join(stale, ", ")+".")
	}
	return s
}
