package codegraph

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/profile"
)

// Findings are the problems the graph can see across its layers: a package that
// breaks the layering rule, a doc that names a file that is gone, a config key
// nothing reads, a table without a primary key. They are derived data, written
// by the `findings` layer from what the other layers wrote, so they are as fresh
// as the graph and need no checker of their own to keep up with the code.
//
// A rule is a function of a read-only Model (plus, for the two rules that need
// facts the graph does not store, the code index). It lists every problem it
// finds; the engine turns them into `finding` nodes with an `about` edge to the
// node at fault, and every rule into a `rule` node that says whether it ran: a
// rule whose layers are not built has not looked, and "no findings" from it
// would read as "clean".

// Severities, worst first. The agent tools and the UI also accept the synonyms
// of severityRank.
const (
	sevError   = "error"   // breaks a rule the project states (layering, a spec that claims what is not there)
	sevWarning = "warning" // wrong or stale, and worth fixing before it misleads someone
	sevInfo    = "info"    // a smell: worth a look, not a bug
)

// maxFindingsPerRule bounds one rule's output, so a rule that misfires on a big
// project cannot flood the graph. The cut is reported on the layer.
const maxFindingsPerRule = 200

// finding is one problem a rule found.
type finding struct {
	subject quad.IRI // the node at fault
	detail  string   // tells apart several findings of one rule on one subject
	message string
	path    string // where to look, when the rule knows
	line    int
	// severity overrides the rule's own, when one rule reports at two levels.
	severity string
}

// rule is one check.
type rule struct {
	name        string
	title       string // what a finding of this rule is called, in a list
	severity    string
	description string
	needs       []string // layers whose data the rule reads; all must be built
	// applies says why the rule has nothing to look at in this project ("" when it
	// has). A rule that does not apply has not looked either, so it is recorded as
	// not checked, but that is not a failure of the graph and does not make the layer partial.
	applies func(e *ruleEnv) string
	check   func(e *ruleEnv) []finding
}

// ruleEnv is what a rule looks at.
type ruleEnv struct {
	m *Model
	// prof is the project's profile: rules read where things live from it, and it
	// can switch a rule off or change its severity.
	prof *profile.Profile
	// ci is the code index, for the facts the graph does not store (where an
	// import is used, who calls an exported function). Nil when it is not
	// available: the rules that need it are then not checked.
	ci    *codeIndex
	templ string // the text of the .templ files, which call Go functions the code index never sees
	// built reports whether a layer wrote its quads in this graph.
	built func(layer string) bool

	refTo      map[string]bool // "dir\x00name": exported names other packages use, by package
	templWords map[string]bool // every identifier in the .templ text
}

// RuleInfo describes one rule and whether it ran, as stored in the graph.
type RuleInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Checked     bool   `json:"checked"`
	Off         bool   `json:"off,omitempty"`  // switched off in govibe.yml: a choice, not a rule that could not look
	Note        string `json:"note,omitempty"` // why it did not run
	Findings    int    `json:"findings"`
}

// ruleCatalog is every rule, by name.
func ruleCatalog() []rule {
	rs := []rule{
		{
			name: "layering", title: "Layering violation", severity: sevError,
			description: "A package imports one its architecture layer must not (SKILL.md: only actions and services may call storage). wiring and dev packages are exempt.",
			needs:       []string{"code"}, check: checkLayering,
		},
		{
			name: "framework.import", title: "Import the layer may not take from the framework", severity: sevError,
			description: "A package or template imports a framework package its architecture layer may not (SKILL.md: business logic and storage import nothing from the framework; controllers, middleware and views only govibe/web; commands only govibe; wiring govibe, govibe/config and govibe/web; nothing the framework's internal/). It does not apply to the framework's own repository.",
			needs:       []string{"code", "templates", "externals"}, applies: dependsOnFramework, check: checkFrameworkImport,
		},
		{
			name: "docs.stale_ref", title: "Stale reference in a doc", severity: sevWarning,
			description: "A doc names a file, a symbol or a page that does not exist.",
			needs:       []string{"docs"}, check: checkStaleRefs,
		},
		{
			name: "docs.spec_missing", title: "Spec claims what is missing", severity: sevWarning,
			description: "A spec row marked implemented names a file or symbol that does not exist.",
			needs:       []string{"docs"}, check: checkSpecMissing,
		},
		{
			name: "docs.route_undocumented", title: "Route no doc mentions", severity: sevInfo,
			description: "An application route (not under /dev) that no doc or spec mentions.",
			needs:       []string{"routes", "docs"}, check: checkRouteUndocumented,
		},
		{
			name: "config.undeclared", title: "Config read but not declared", severity: sevWarning,
			description: "Code reads a config key or environment variable that the project's settings file (env.yml) does not declare.",
			needs:       []string{"config"}, check: checkConfigUndeclared,
		},
		{
			name: "config.unused", title: "Config declared but never read", severity: sevInfo,
			description: "The project's settings file (env.yml) declares a key that no code reads. Reads through a variable name are not seen.",
			needs:       []string{"config"}, check: checkConfigUnused,
		},
		{
			name: "code.unreferenced", title: "Exported function nothing references", severity: sevInfo,
			description: "An exported package-level function that nothing else in the code or its tests references. Methods are not checked.",
			needs:       []string{"code"}, check: checkUnreferenced,
		},
		{
			name: "db.no_primary_key", title: "Table without a primary key", severity: sevWarning,
			description: "A table with no primary key.",
			needs:       []string{"db"}, check: checkNoPrimaryKey,
		},
		{
			name: "db.fk_unindexed", title: "Foreign key without an index", severity: sevWarning,
			description: "A foreign key column that no index starts with.",
			needs:       []string{"db"}, check: checkFKUnindexed,
		},
		{
			name: "db.missing_fk", title: "Reference without a foreign key", severity: sevInfo,
			description: "A column that looks like a reference to another table (`<name>_id`) but has no foreign key.",
			needs:       []string{"db"}, check: checkMissingFK,
		},
		{
			name: "db.drift", title: "Migrations and live database disagree", severity: sevWarning,
			description: "With a live database configured: a table, column or index that only one side has, or that the two describe differently.",
			needs:       []string{"db"}, applies: needsLiveDB, check: checkDrift,
		},
		{
			name: "db.unapplied", title: "Migration not applied", severity: sevWarning,
			description: "With a live database configured: a migration its goose table does not list.",
			needs:       []string{"db"}, applies: needsLiveDB, check: checkUnapplied,
		},
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].name < rs[j].name })
	return rs
}

// findingQuads is the output of the engine: the quads, and what was not checked.
type findingQuads struct {
	quads    []quad.Quad
	warnings []string
	found    int
}

// checkRuleSettings fails on a govibe.yml that names a rule that does not exist: a
// typo would otherwise leave the rule running and the author sure it was off.
func checkRuleSettings(prof *profile.Profile) error {
	known := map[string]bool{}
	var names []string
	for _, r := range ruleCatalog() {
		known[r.name] = true
		names = append(names, r.name)
	}
	var unknown []string
	for name := range prof.Rules {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%s: rules.%s is not a rule; rules: %s", profile.File, strings.Join(unknown, ", rules."), strings.Join(names, ", "))
}

// runRules runs every rule whose layers are built and renders the result.
func runRules(e *ruleEnv) findingQuads {
	set := newQuadSet("findings")
	var out findingQuads
	seen := map[quad.IRI]bool{}
	for _, r := range ruleCatalog() {
		setting, _ := e.prof.RuleSetting(r.name)
		if setting != "" && setting != profile.RuleOff {
			r.severity = setting // govibe.yml changes the severity of every finding of the rule
		}
		rid := ruleID(r.name)
		set.add(rid, PredType, KindRule)
		set.str(rid, PredName, r.name)
		set.str(rid, PredTitle, r.title)
		set.str(rid, PredSeverity, r.severity)
		set.str(rid, PredDescription, r.description)

		if setting == profile.RuleOff {
			// A choice, not a gap: it is not counted among the rules that did not look.
			set.flag(rid, PredChecked, false)
			set.flag(rid, PredOff, true)
			set.str(rid, PredNote, "switched off in "+profile.File)
			continue
		}
		if why := unmet(e, r); why != "" {
			set.flag(rid, PredChecked, false)
			set.str(rid, PredNote, why)
			out.warnings = append(out.warnings, fmt.Sprintf("rule %s not checked: %s", r.name, why))
			continue
		}
		if r.applies != nil {
			if why := r.applies(e); why != "" {
				set.flag(rid, PredChecked, false)
				set.str(rid, PredNote, "not applicable: "+why)
				continue
			}
		}
		set.flag(rid, PredChecked, true)

		fs := r.check(e)
		sort.SliceStable(fs, func(i, j int) bool {
			if fs[i].subject != fs[j].subject {
				return fs[i].subject < fs[j].subject
			}
			return fs[i].detail < fs[j].detail
		})
		if len(fs) > maxFindingsPerRule {
			out.warnings = append(out.warnings, fmt.Sprintf("rule %s found %d, kept the first %d", r.name, len(fs), maxFindingsPerRule))
			fs = fs[:maxFindingsPerRule]
		}
		for _, f := range fs {
			id := findingID(r.name, f.subject, f.detail)
			if seen[id] {
				continue
			}
			seen[id] = true
			sev := f.severity
			if sev == "" {
				sev = r.severity
			}
			set.add(id, PredType, KindFinding)
			set.str(id, PredName, r.title)
			set.str(id, PredRule, r.name)
			set.str(id, PredSeverity, sev)
			set.str(id, PredMessage, oneLine(f.message, 600))
			set.add(id, PredAbout, f.subject)
			if f.path != "" {
				set.str(id, PredPath, f.path)
				if f.line > 0 {
					set.num(id, PredLine, f.line)
				}
			}
			out.found++
		}
	}
	out.quads = set.quads
	return out
}

// unmet says why a rule cannot run, "" when it can.
func unmet(e *ruleEnv, r rule) string {
	for _, l := range r.needs {
		if !e.built(l) {
			return fmt.Sprintf("the %s layer is not built", l)
		}
	}
	if (r.name == "code.unreferenced" || r.name == "framework.import") && e.ci == nil {
		return "there is no code index"
	}
	return ""
}

// needsLiveDB is the applicability of the rules that compare the migrations with
// a live database.
func needsLiveDB(e *ruleEnv) string {
	if !e.liveKnown() {
		return "no live database was read (set MARIADB_DSN), so there is nothing to compare the migrations with"
	}
	return ""
}

// --- reading the model for the rules ---

// where is the file and line of the declaration a node stands for, if it has one.
func (e *ruleEnv) where(n *Node) (string, int) {
	ref := e.m.Ref(n)
	return ref.Path, ref.Line
}

// firstRef is the first place (file, then line) a package in fromDir refers to
// one in toDir with `pkg.Name`: where the import is used.
func (e *ruleEnv) firstRef(fromDir, toDir string) (string, int) {
	if e.ci == nil {
		return "", 0
	}
	file, line := "", 0
	for _, list := range [][]refSite{e.ci.refs, e.ci.typeRefs} {
		for _, r := range list {
			if r.dir != fromDir || r.toDir != toDir {
				continue
			}
			if file == "" || r.file < file || (r.file == file && r.line < line) {
				file, line = r.file, r.line
			}
		}
	}
	return file, line
}

// label is how a message names a node: a function or type with its package
// (`controllers.GetOrder`), anything else by its name, falling back to its ID.
func (e *ruleEnv) label(n *Node) string {
	name := n.Str("name")
	if name == "" {
		return n.ID
	}
	if n.Kind == "func" || n.Kind == "type" {
		if p := e.m.pkgOf(n); p != nil && p.Str("name") != "" {
			return p.Str("name") + "." + name
		}
	}
	return name
}

// names lists up to n labels, "a, b and 2 more".
func (e *ruleEnv) names(ns []*Node, n int) string {
	var parts []string
	for i, x := range ns {
		if i == n {
			parts = append(parts, fmt.Sprintf("%d more", len(ns)-n))
			break
		}
		parts = append(parts, e.label(x))
	}
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
	return strings.Join(parts, "")
}
