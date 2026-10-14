package codegraph

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/rules"
)

// The rules. Each reads the model and lists what is wrong; the engine in
// findings.go turns that into nodes. A message says what is wrong and what to do
// about it, because it is shown on its own in a list and to an agent.

// --- layering ---

func checkLayering(e *ruleEnv) []finding {
	var out []finding
	for _, p := range e.m.Kind("package") {
		from := p.Str("arch_layer")
		for _, imp := range e.m.out(p, "imports") {
			why := layerVerdict(from, imp.Str("arch_layer"))
			if why == "" {
				continue
			}
			f := finding{
				subject: quad.IRI(p.ID), detail: imp.ID, path: p.Str("path"),
				message: fmt.Sprintf("%s imports %s, against the layering rule: %s. (%s layer into %s layer", p.Str("path"), imp.Str("path"), why, from, imp.Str("arch_layer")),
			}
			if file, line := e.firstRef(p.Str("path"), imp.Str("path")); file != "" {
				f.path, f.line = file, line
				f.message += fmt.Sprintf("; first used at %s:%d", file, line)
			}
			f.message += ".)"
			out = append(out, f)
		}
	}
	return out
}

// --- framework imports ---

// dependsOnFramework is the applicability of the rules about what a project takes from
// the framework: they have nothing to look at in the framework's own repository, where
// nothing is external to it.
func dependsOnFramework(e *ruleEnv) string {
	if e.ci != nil && e.ci.mods.app == e.ci.mods.framework {
		return "this project is the framework itself, so nothing it imports is the framework's API"
	}
	return ""
}

// checkFrameworkImport judges every framework package a package, or a .templ file (whose
// generated Go the code layer does not read), imports against the layer it is in.
func checkFrameworkImport(e *ruleEnv) []finding {
	fw := e.ci.mods.framework
	var out []finding
	judge := func(from *Node, layer, where string) {
		for _, ext := range e.m.out(from, "imports_external") {
			rel, ok := rules.RelativePath(fw, ext.Str("import_path"))
			if !ok {
				continue
			}
			why := rules.FrameworkImportVerdict(layer, rel, ext.Bool("internal"))
			if why == "" {
				continue
			}
			f := finding{subject: quad.IRI(from.ID), detail: ext.ID, path: where}
			place := ""
			if file, line := e.firstExtImport(from.Str("path"), ext.Str("import_path")); file != "" {
				f.path, f.line = file, line
				place = fmt.Sprintf("; imported at %s:%d", file, line)
			}
			f.message = fmt.Sprintf("%s imports %s, against the framework-import rule: %s. (%s layer%s.)", where, ext.Str("import_path"), why, layer, place)
			out = append(out, f)
		}
	}
	for _, p := range e.m.Kind("package") {
		judge(p, p.Str("arch_layer"), p.Str("path"))
	}
	for _, f := range e.m.Kind("file") {
		if strings.HasSuffix(f.Str("path"), ".templ") {
			judge(f, e.prof.Classify(pathDir(f.Str("path"))), f.Str("path"))
		}
	}
	return out
}

// firstExtImport is where a package in dir first imports the external package path.
func (e *ruleEnv) firstExtImport(dir, importPath string) (string, int) {
	file, line := "", 0
	for _, r := range e.ci.extImports {
		if r.dir != dir || r.name != importPath {
			continue
		}
		if file == "" || r.file < file || (r.file == file && r.line < line) {
			file, line = r.file, r.line
		}
	}
	return file, line
}

// --- docs ---

// unresolvedRefs reads a node's `unresolved_ref` literals, "<line>:<text>".
func unresolvedRefs(n *Node) []unresolvedRef {
	var out []unresolvedRef
	for _, v := range n.Attrs["unresolved_ref"] {
		s, ok := v.(string)
		if !ok {
			continue
		}
		lineText, tok, found := strings.Cut(s, ":")
		if !found {
			lineText, tok = "0", s
		}
		line, _ := strconv.Atoi(lineText)
		out = append(out, unresolvedRef{line: line, token: tok})
	}
	return out
}

func checkStaleRefs(e *ruleEnv) []finding {
	var out []finding
	for _, kind := range []string{"doc", "spec"} {
		for _, d := range e.m.Kind(kind) {
			for _, u := range unresolvedRefs(d) {
				out = append(out, finding{
					subject: quad.IRI(d.ID), detail: u.String(), path: d.Str("path"), line: u.line,
					message: fmt.Sprintf("%s mentions `%s`, which does not exist: it was removed or renamed, or has a typo. Update the reference or drop it.", d.Str("path"), u.token),
				})
			}
		}
	}
	return out
}

func checkSpecMissing(e *ruleEnv) []finding {
	var out []finding
	for _, it := range e.m.Kind("spec_item") {
		if it.Str("status") != "implemented" {
			continue
		}
		for _, u := range unresolvedRefs(it) {
			out = append(out, finding{
				subject: quad.IRI(it.ID), detail: u.String(), path: it.Str("path"), line: u.line,
				message: fmt.Sprintf("%s marks %q implemented, but `%s`, which the row names, does not exist. Fix the row, or the code that should be there.", it.Str("path"), it.Str("name"), u.token),
			})
		}
	}
	return out
}

func checkRouteUndocumented(e *ruleEnv) []finding {
	var out []finding
	for _, r := range e.m.Kind("route") {
		// The framework's routes (the dev panel, its static files) are documented by the
		// framework, not by the project that happens to run it.
		if r.Bool("dev_only") || r.Str("origin") == OriginFramework || len(r.In["mentions"])+len(r.In["covers"]) > 0 {
			continue
		}
		f := finding{subject: quad.IRI(r.ID), message: fmt.Sprintf("No doc or spec mentions %s %s. Say what it is for in docs/public/ARCHITECTURE.md or the spec that owns it.", r.Str("method"), r.Str("path"))}
		if h := e.m.nodes[r.first("handled_by")]; h != nil {
			f.path, f.line = e.where(h)
		}
		out = append(out, f)
	}
	return out
}

// --- config ---

// declared is false only when the indexer said so: a node with no `declared`
// attribute is not judged.
func declaredFalse(n *Node) bool {
	for _, v := range n.Attrs["declared"] {
		if b, ok := v.(bool); ok && !b {
			return true
		}
	}
	return false
}

// readersOf names who reads a setting: the functions if there are any (a
// package that reads it is then just where they live), else the packages.
func readersOf(nodes []*Node) []*Node {
	var funcs []*Node
	for _, n := range nodes {
		if n.Kind == "func" {
			funcs = append(funcs, n)
		}
	}
	if len(funcs) > 0 {
		return funcs
	}
	return nodes
}

// settingsFile names the project's own settings file, the last the profile lists: where an
// undeclared setting should be declared.
func (e *ruleEnv) settingsFile() string {
	if e.prof != nil && len(e.prof.Config) > 0 {
		return e.prof.Config[len(e.prof.Config)-1]
	}
	return "env.yml"
}

func checkConfigUndeclared(e *ruleEnv) []finding {
	var out []finding
	for _, n := range e.m.Kind("config_key") {
		if !declaredFalse(n) {
			continue
		}
		readers := readersOf(e.m.in(n, "uses_config"))
		f := finding{subject: quad.IRI(n.ID), message: fmt.Sprintf("Config key %s is read by %s but %s does not declare it. Declare it there as ${ENV:default} (SKILL.md), so it can be found and overridden.", n.Str("name"), e.names(readers, 3), e.settingsFile())}
		if len(readers) > 0 {
			f.path, f.line = e.where(readers[0])
		}
		out = append(out, f)
	}
	for _, n := range e.m.Kind("env_var") {
		if !declaredFalse(n) {
			continue
		}
		readers := readersOf(e.m.in(n, "reads_env"))
		f := finding{subject: quad.IRI(n.ID), message: fmt.Sprintf("Environment variable %s is read by %s but %s does not declare it. Declare it there (SKILL.md prefers config over ad-hoc env lookups).", n.Str("name"), e.names(readers, 3), e.settingsFile())}
		if len(readers) > 0 {
			f.path, f.line = e.where(readers[0])
		}
		out = append(out, f)
	}
	return out
}

func checkConfigUnused(e *ruleEnv) []finding {
	var out []finding
	for _, n := range e.m.Kind("config_key") {
		if declaredFalse(n) || len(n.In["uses_config"]) > 0 || n.Str("origin") == OriginFramework || n.Bool("framework_key") {
			continue // a framework setting (or the application's override of one) is the framework's to read
		}
		msg := fmt.Sprintf("Config key %s is declared in %s but no code reads it", n.Str("name"), e.settingsFile())
		if env := e.m.out(n, "reads_env"); len(env) > 0 {
			msg += " (environment variable " + env[0].Str("name") + ")"
		}
		msg += ". Remove it, or read it; a read through a variable name is not seen."
		out = append(out, finding{subject: quad.IRI(n.ID), path: n.Str("path"), line: n.Int("line"), message: msg})
	}
	return out
}

// --- code ---

func checkUnreferenced(e *ruleEnv) []finding {
	if e.refTo == nil {
		e.refTo = map[string]bool{}
		for _, r := range e.ci.refs {
			e.refTo[r.toDir+"\x00"+r.name] = true
		}
	}
	var out []finding
	for _, fn := range e.m.Kind("func") {
		name := fn.Str("name")
		if !fn.Bool("exported") || strings.Contains(name, ".") || name == "" {
			continue // unexported, or a method (an interface may call it)
		}
		pkg := e.m.pkgOf(fn)
		if pkg == nil || pkg.Str("name") == "main" {
			continue
		}
		if file := e.m.nodes[fn.first("defined_in")]; file != nil && file.Bool("generated") {
			continue
		}
		if len(fn.In["handled_by"]) > 0 || len(fn.Out["registers"]) > 0 {
			continue
		}
		dir := pkg.Str("path")
		if e.refTo[dir+"\x00"+name] || e.ci.usedWithin(dir, name, quad.IRI(fn.ID)) || e.ci.referencedByTests(dir, name) || e.templUses(name) {
			continue
		}
		path, line := e.where(fn)
		out = append(out, finding{
			subject: quad.IRI(fn.ID), path: path, line: line,
			message: fmt.Sprintf("%s is exported, but nothing else in the code or its tests references it. Use it, unexport it, or delete it.", e.label(fn)),
		})
	}
	return out
}

// templUses reports whether the text of a .templ file names the function: a
// component can call a Go helper, and its generated Go is not indexed.
func (e *ruleEnv) templUses(name string) bool {
	if e.templ == "" {
		return false
	}
	if e.templWords == nil {
		e.templWords = map[string]bool{}
		for _, w := range identWordRe.FindAllString(e.templ, -1) {
			e.templWords[w] = true
		}
	}
	return e.templWords[name]
}

var identWordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// --- db ---

// tableOf is the table a column or index belongs to.
func (e *ruleEnv) tableOf(n *Node) *Node {
	for _, t := range e.m.in(n, "has_column") {
		if t.Kind == "table" {
			return t
		}
	}
	for _, t := range e.m.in(n, "has_index") {
		if t.Kind == "table" {
			return t
		}
	}
	return nil
}

// dbLabel names a table, column or index the way SQL does.
func dbLabel(n *Node) string {
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(n.ID, "table:"), "col:"), "idx:")
}

func checkNoPrimaryKey(e *ruleEnv) []finding {
	var out []finding
	for _, t := range e.m.Kind("table") {
		primary := false
		for _, ix := range e.m.out(t, "has_index") {
			primary = primary || ix.Bool("primary")
		}
		if primary {
			continue
		}
		out = append(out, finding{
			subject: quad.IRI(t.ID),
			message: fmt.Sprintf("Table %s has no primary key. Without one, replication, ORMs and row-level tools cannot address a row; add one in a new migration.", t.Str("name")),
		})
	}
	return out
}

func checkFKUnindexed(e *ruleEnv) []finding {
	var out []finding
	for _, c := range e.m.Kind("column") {
		fk := false
		for _, t := range e.m.out(c, "fk_to") {
			fk = fk || t.Kind == "column"
		}
		t := e.tableOf(c)
		if !fk || t == nil {
			continue
		}
		indexed := false
		for _, ix := range e.m.out(t, "has_index") {
			first, _, _ := strings.Cut(ix.Str("columns"), ",")
			indexed = indexed || strings.TrimSpace(first) == c.Str("name")
		}
		if indexed {
			continue
		}
		out = append(out, finding{
			subject: quad.IRI(c.ID),
			message: fmt.Sprintf("%s is a foreign key, but no index starts with it: joins and cascading deletes on it scan the table. Add an index in a new migration.", dbLabel(c)),
		})
	}
	return out
}

func checkMissingFK(e *ruleEnv) []finding {
	var out []finding
	for _, c := range e.m.Kind("column") {
		for _, target := range e.m.out(c, "implicit_ref") {
			if target.Kind != "column" {
				continue
			}
			a := e.m.annotationOf(c.ID, "implicit_ref", target.ID)
			out = append(out, finding{
				subject: quad.IRI(c.ID), detail: target.ID,
				message: fmt.Sprintf("%s looks like a reference to %s (inferred from its name, confidence %d) but no foreign key enforces it. Add one in a new migration, or rename the column if it is not a reference.", dbLabel(c), dbLabel(target), a.conf),
			})
		}
	}
	return out
}

// liveKnown reports whether the graph was built against a live database: only
// then does a table's `source` mean more than "from the migrations".
func (e *ruleEnv) liveKnown() bool {
	for _, mg := range e.m.Kind("migration") {
		if len(mg.Attrs["applied"]) > 0 {
			return true
		}
	}
	for _, t := range e.m.Kind("table") {
		if s := t.Str("source"); s == "live" || s == "both" {
			return true
		}
	}
	return false
}

func checkDrift(e *ruleEnv) []finding {
	var out []finding
	for _, kind := range []string{"table", "column", "index"} {
		for _, n := range e.m.Kind(kind) {
			if n.Bool("implicit") {
				continue // the server's own index for a foreign key
			}
			t := n
			if kind != "table" {
				if t = e.tableOf(n); t == nil {
					continue
				}
			}
			label := kind + " " + dbLabel(n)
			src := n.Str("source")
			// A table that is only on one side already says so for everything in it.
			whole := kind != "table" && t.Str("source") != "both"
			switch {
			case whole:
			case src == "live":
				out = append(out, finding{subject: quad.IRI(n.ID), detail: "live", message: fmt.Sprintf("The %s exists only in the live database; no migration creates it. Write the migration, or drop it from the database.", label)})
			case src == "migration" && !e.unappliedIntro(n):
				out = append(out, finding{subject: quad.IRI(n.ID), detail: "migration", message: fmt.Sprintf("The %s is defined by the migrations but missing from the live database. Someone changed the database by hand, or the migration was edited after it ran.", label)})
			}
			if d := n.Str("drift"); d != "" {
				out = append(out, finding{subject: quad.IRI(n.ID), detail: "drift", message: fmt.Sprintf("The %s differs between the migrations and the live database (%s).", label, d)})
			}
		}
	}
	return out
}

// unappliedIntro reports whether the migration that introduced n has not been
// applied, in which case the missing element is that migration's finding.
func (e *ruleEnv) unappliedIntro(n *Node) bool {
	for _, mg := range e.m.out(n, "introduced_in") {
		for _, v := range mg.Attrs["applied"] {
			if b, ok := v.(bool); ok && !b {
				return true
			}
		}
	}
	return false
}

func checkUnapplied(e *ruleEnv) []finding {
	var out []finding
	for _, mg := range e.m.Kind("migration") {
		for _, v := range mg.Attrs["applied"] {
			if b, ok := v.(bool); ok && !b {
				out = append(out, finding{
					subject: quad.IRI(mg.ID), path: mg.Str("path"),
					message: fmt.Sprintf("Migration %s is not applied to the live database. Run `govibe migrate up`.", mg.Str("name")),
				})
			}
		}
	}
	return out
}
