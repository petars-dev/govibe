package codegraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cayleygraph/quad"
)

// The vocabulary of the graph, defined once. docs/public/GRAPH_SCHEMA.md is
// the authority on what each name means; keep the two in step.

// Kinds: the object of a node's `type` quad.
const (
	KindPackage = quad.IRI("kind:package")
	KindFile    = quad.IRI("kind:file")
	KindFunc    = quad.IRI("kind:func")
	KindType    = quad.IRI("kind:type")
	KindBuild   = quad.IRI("kind:build")
	KindLayer   = quad.IRI("kind:layer")

	// Phase 2 layers.
	KindRoute      = quad.IRI("kind:route")
	KindMiddleware = quad.IRI("kind:middleware")
	KindTemplate   = quad.IRI("kind:template")
	KindMCPTool    = quad.IRI("kind:mcp_tool")
	KindConfigKey  = quad.IRI("kind:config_key")
	KindEnvVar     = quad.IRI("kind:env_var")
	KindDoc        = quad.IRI("kind:doc")
	KindSpec       = quad.IRI("kind:spec")
	KindSpecItem   = quad.IRI("kind:spec_item")

	// Phase 3: the db layer.
	KindTable      = quad.IRI("kind:table")
	KindColumn     = quad.IRI("kind:column")
	KindIndex      = quad.IRI("kind:index")
	KindMigration  = quad.IRI("kind:migration")
	KindAnnotation = quad.IRI("kind:annotation")

	// Phase 5: the findings layer.
	KindFinding = quad.IRI("kind:finding")
	KindRule    = quad.IRI("kind:rule")

	// Framework extraction: a package or symbol outside the indexed project, which the
	// project imports or calls (the framework's public API, a library).
	KindExternal = quad.IRI("kind:external")

	// Commands and the scheduler: a CLI command of the application, which may be on a schedule.
	KindCommand = quad.IRI("kind:command")
)

// Structural predicates: an IRI object, drawn as an edge by the explorer.
const (
	PredType      = quad.IRI("type")
	PredDefinedIn = quad.IRI("defined_in")
	PredInPackage = quad.IRI("in_package")
	PredImports   = quad.IRI("imports")
	PredHasLayer  = quad.IRI("has_layer")

	// Phase 2 layers.
	PredHandledBy     = quad.IRI("handled_by")     // route -> func
	PredUsesMW        = quad.IRI("uses_mw")        // route -> middleware
	PredImplementedBy = quad.IRI("implemented_by") // middleware -> func
	PredRenders       = quad.IRI("renders")        // func, template -> template
	PredUsesConfig    = quad.IRI("uses_config")    // func, package -> config key
	PredReadsEnv      = quad.IRI("reads_env")      // config key, func -> env var
	PredRegisters     = quad.IRI("registers")      // func -> MCP tool
	PredHasItem       = quad.IRI("has_item")       // spec -> spec item
	PredMentions      = quad.IRI("mentions")       // doc, spec, spec item -> any node
	PredCovers        = quad.IRI("covers")         // spec item -> any node

	// Phase 3: the db layer.
	PredHasColumn    = quad.IRI("has_column")    // table, index -> column
	PredHasIndex     = quad.IRI("has_index")     // table -> index
	PredFKTo         = quad.IRI("fk_to")         // column -> column, table -> table
	PredImplicitRef  = quad.IRI("implicit_ref")  // column -> column, table -> table (inferred)
	PredManyToMany   = quad.IRI("many_to_many")  // table -> table (inferred, through a junction)
	PredIntroducedIn = quad.IRI("introduced_in") // table, column, index -> migration
	PredReadsTable   = quad.IRI("reads_table")   // func -> table
	PredWritesTable  = quad.IRI("writes_table")  // func -> table
	PredMapsTo       = quad.IRI("maps_to")       // type -> table
	PredSubject      = quad.IRI("subject")       // annotation -> the inferred edge's subject
	PredObject       = quad.IRI("object")        // annotation -> the inferred edge's object
	PredVia          = quad.IRI("via")           // annotation -> the junction table

	// Phase 5: the findings layer.
	PredAbout = quad.IRI("about") // finding -> the node it is about

	// Phase 6: the opt-in calls and git layers.
	PredCalls         = quad.IRI("calls")           // func -> func it calls (an annotation says how sure when it is not a plain call)
	PredCoChangedWith = quad.IRI("co_changed_with") // file, doc, spec -> one that changed in the same commits (an annotation says how often)

	// Framework extraction: edges from the project to what lies outside it. They are
	// separate from `imports`, `calls` and `handled_by`, whose targets are always
	// project code, so nothing that walks those has to know about externals.
	PredImportsExt   = quad.IRI("imports_external")    // package -> external package it imports
	PredCallsExt     = quad.IRI("calls_external")      // func -> external function or package it calls (the calls layer)
	PredHandledByExt = quad.IRI("handled_by_external") // route -> external function that handles it

	// Commands and the scheduler.
	PredRunBy = quad.IRI("run_by") // command -> the function that runs it
)

// Attribute predicates: a literal object, which a viewer can hide.
const (
	PredName            = quad.IRI("name")
	PredDoc             = quad.IRI("doc")
	PredSignature       = quad.IRI("signature")
	PredLine            = quad.IRI("line")
	PredExported        = quad.IRI("exported")
	PredPath            = quad.IRI("path")
	PredImportPath      = quad.IRI("import_path")
	PredArchLayer       = quad.IRI("arch_layer")
	PredExternalImports = quad.IRI("external_imports")
	PredLines           = quad.IRI("lines")
	PredGenerated       = quad.IRI("generated")

	// Framework extraction: where a node comes from, and what an external node is.
	PredFrameworkKey = quad.IRI("framework_key")  // config key: the framework declares it too (an application overrides a default of the framework's)
	PredOrigin       = quad.IRI("origin")         // route, middleware, config_key, env_var, mcp_tool, external: "app" or "framework"
	PredMountedBy    = quad.IRI("mounted_by")     // route: who registered it, "app" or "framework"
	PredModule       = quad.IRI("module")         // external: the module the package belongs to
	PredModuleVer    = quad.IRI("module_version") // external: that module's version in this build ("(devel)" for a local replace)
	PredInternal     = quad.IRI("internal")       // external: inside the framework's internal/, collapsed to one leaf per package

	// Commands and the scheduler: what a command declares about its schedule. A command
	// with no schedule carries `scheduled` false and none of the others.
	PredScheduled = quad.IRI("scheduled") // command: it is on a schedule
	PredSchedule  = quad.IRI("schedule")  // command: the schedule as declared ("0 2 * * *", "@every 6h", "CRON_TZ=Europe/Sofia 0 9 * * *")
	PredTimezone  = quad.IRI("timezone")  // command: the zone the schedule is read in
	PredOverlap   = quad.IRI("overlap")   // command: skip or allow, when a run is due while the last one is going
	PredTimeout   = quad.IRI("timeout")   // command: a run's deadline, as a Go duration (absent: none)
	PredEvery     = quad.IRI("every")     // command: the interval of an "@every" schedule, as a Go duration

	// Phase 2 layers.
	PredMethod      = quad.IRI("method")
	PredDevOnly     = quad.IRI("dev_only")
	PredHandler     = quad.IRI("handler")
	PredDescription = quad.IRI("description")
	PredInputSchema = quad.IRI("input_schema")
	PredDefault     = quad.IRI("default")
	PredSensitive   = quad.IRI("sensitive")
	PredDeclared    = quad.IRI("declared")
	PredField       = quad.IRI("field")
	PredTitle       = quad.IRI("title")
	PredWhySkipped  = quad.IRI("why_skipped")
	PredNote        = quad.IRI("note")

	// Phase 5: findings and what they need from the docs layer.
	PredRule          = quad.IRI("rule")           // finding: the rule that found it
	PredSeverity      = quad.IRI("severity")       // finding, rule: error, warning or info
	PredMessage       = quad.IRI("message")        // finding: what is wrong and what to do
	PredChecked       = quad.IRI("checked")        // rule: it ran on this graph (false: a layer it reads is not built)
	PredOff           = quad.IRI("off")            // rule: switched off in govibe.yml, so it is not a rule that could not look
	PredUnresolvedRef = quad.IRI("unresolved_ref") // doc, spec item: "<line>:<text>" of a reference to something that is gone

	// Phase 6: the git layer's attributes, on file, doc and spec nodes, and the
	// annotation of a co-change edge.
	PredLastCommit   = quad.IRI("last_commit")    // the newest commit that touched it: the first 12 characters of its hash
	PredLastCommitAt = quad.IRI("last_commit_at") // when that commit was made
	PredLastSubject  = quad.IRI("last_subject")   // that commit's subject line
	PredCommits      = quad.IRI("commits")        // commits that touched it within the history window
	PredChurn        = quad.IRI("churn")          // lines added plus lines deleted by those commits
	PredAuthors      = quad.IRI("authors")        // distinct authors of those commits (a count: no names or addresses are kept)
	PredTogether     = quad.IRI("together")       // on a co-change annotation: commits that touched both ends

	// Phase 3: the db layer.
	PredDataType    = quad.IRI("data_type")
	PredNullable    = quad.IRI("nullable")
	PredUnique      = quad.IRI("unique")
	PredPrimary     = quad.IRI("primary")
	PredEngine      = quad.IRI("engine")
	PredComment     = quad.IRI("comment")
	PredKey         = quad.IRI("key")
	PredExtra       = quad.IRI("extra")
	PredOrdinal     = quad.IRI("ordinal")
	PredColumns     = quad.IRI("columns")
	PredImplicit    = quad.IRI("implicit")
	PredSource      = quad.IRI("source")
	PredDrift       = quad.IRI("drift")
	PredCardinality = quad.IRI("cardinality")
	PredFKName      = quad.IRI("fk_name")
	PredOnDelete    = quad.IRI("on_delete")
	PredOnUpdate    = quad.IRI("on_update")
	PredVersion     = quad.IRI("version")
	PredApplied     = quad.IRI("applied")
	PredPredicate   = quad.IRI("predicate")
	PredConfidence  = quad.IRI("confidence")

	// build:latest and layer:<name> run nodes (owned by the meta layer).
	PredAppName          = quad.IRI("app_name")          // build: the application's name (App.Name)
	PredAppModule        = quad.IRI("app_module")        // build: its module path
	PredFrameworkModule  = quad.IRI("framework_module")  // build: the framework it is built on
	PredFrameworkVersion = quad.IRI("framework_version") // build: that framework's version in this build
	PredGitSHA           = quad.IRI("git_sha")
	PredDirty            = quad.IRI("dirty")
	PredIndexedAt        = quad.IRI("indexed_at")
	PredDurationMS       = quad.IRI("duration_ms")
	PredIndexerVersion   = quad.IRI("indexer_version")
	PredQuads            = quad.IRI("quads")
	PredFingerprint      = quad.IRI("fingerprint")
	PredStatus           = quad.IRI("status")
	PredAdded            = quad.IRI("added")
	PredRemoved          = quad.IRI("removed")
	PredError            = quad.IRI("error")
)

// structuralPredicates are the predicates whose object must be a node with a
// `type` quad (the "no dangling references" invariant, see the tests).
var structuralPredicates = []quad.IRI{
	PredDefinedIn, PredInPackage, PredImports, PredHasLayer,
	PredHandledBy, PredUsesMW, PredImplementedBy, PredRenders, PredUsesConfig, PredReadsEnv,
	PredRegisters, PredHasItem, PredMentions, PredCovers,
	PredHasColumn, PredHasIndex, PredFKTo, PredImplicitRef, PredManyToMany, PredIntroducedIn,
	PredReadsTable, PredWritesTable, PredMapsTo, PredSubject, PredObject, PredVia,
	PredAbout, PredCalls, PredCoChangedWith,
	PredImportsExt, PredCallsExt, PredHandledByExt,
	PredRunBy,
}

// archLayers is the closed set of `arch_layer` values.
var archLayers = []string{"controllers", "business", "storage", "middleware", "commands", "wiring", "dev", "views", "support"}

// LayerLabel is the label IRI that owns every quad of the named layer. The
// `layer:` namespace is reserved for the indexer.
func LayerLabel(layer string) quad.IRI { return quad.IRI("layer:" + layer) }

const labelPrefix = "layer:"

// idKey percent-encodes the characters an ID may not contain, so one source
// name always maps to one ID. N-Quads, the default wire format of the HTTP API,
// cannot carry ASCII control characters, space, `"` or `\` inside an IRI
// (GRAPH_SCHEMA.md, F5); `%` is encoded too so the mapping stays one-to-one.
func idKey(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || c == ' ' || c == '"' || c == '\\' || c == '%' {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// validID reports whether s is safe as an IRI on every wire format.
func validID(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || c == ' ' || c == '"' || c == '\\' {
			return false
		}
	}
	return s != ""
}

// ID builders. A directory is repo-relative with '/' separators and "." for
// the module root, so the root package is `pkg:.` and its main is `func:..main`.

func pkgID(dir string) quad.IRI   { return quad.IRI("pkg:" + idKey(dir)) }
func fileID(path string) quad.IRI { return quad.IRI("file:" + idKey(path)) }
func typeID(dir, name string) quad.IRI {
	return quad.IRI("typ:" + idKey(dir) + "." + idKey(name))
}

// funcID names a function (recv == "") or a method (recv is the receiver type
// without `*` or type parameters).
func funcID(dir, recv, name string) quad.IRI {
	if recv == "" {
		return quad.IRI("func:" + idKey(dir) + "." + idKey(name))
	}
	return quad.IRI("func:" + idKey(dir) + "." + idKey(recv) + "." + idKey(name))
}

func routeID(method, path string) quad.IRI {
	return quad.IRI("route:" + idKey(method) + ":" + idKey(path))
}
func mwID(key string) quad.IRI { return quad.IRI("mw:" + idKey(key)) }

// commandID names a CLI command by the name it is run with (`command:items:sync`).
func commandID(name string) quad.IRI { return quad.IRI("command:" + idKey(name)) }
func tmplID(dir, name string) quad.IRI {
	return quad.IRI("tmpl:" + idKey(dir) + "." + idKey(name))
}
func toolID(name string) quad.IRI  { return quad.IRI("tool:" + idKey(name)) }
func cfgID(key string) quad.IRI    { return quad.IRI("cfg:" + idKey(key)) }
func envID(name string) quad.IRI   { return quad.IRI("env:" + idKey(name)) }
func docID(path string) quad.IRI   { return quad.IRI("doc:" + idKey(path)) }
func specID(slug string) quad.IRI  { return quad.IRI("spec:" + idKey(slug)) }
func tableID(name string) quad.IRI { return quad.IRI("table:" + idKey(name)) }
func colID(table, col string) quad.IRI {
	return quad.IRI("col:" + idKey(table) + "." + idKey(col))
}
func idxID(table, index string) quad.IRI {
	return quad.IRI("idx:" + idKey(table) + "." + idKey(index))
}
func migID(stem string) quad.IRI { return quad.IRI("mig:" + idKey(stem)) }

// extID names an external package (`ext:github.com/petars-dev/govibe/web`) or one of
// its functions (`ext:github.com/petars-dev/govibe/web.ServeHTMX`).
func extID(importPath, name string) quad.IRI {
	if name == "" {
		return quad.IRI("ext:" + idKey(importPath))
	}
	return quad.IRI("ext:" + idKey(importPath) + "." + idKey(name))
}

// annID names the annotation of one inferred edge: a short hash of the edge, so
// the same edge always has the same annotation.
func annID(subject, predicate, object quad.IRI) quad.IRI {
	h := sha256.Sum256([]byte(string(subject) + "\x00" + string(predicate) + "\x00" + string(object)))
	return quad.IRI("ann:" + hex.EncodeToString(h[:])[:12])
}

// findingID names one finding: the rule, the node it is about and, when one rule
// finds several problems on the same node (a package that imports two forbidden
// ones), a detail that tells them apart. `#` is the same separator the duplicate
// suffix of a code ID uses.
func findingID(rule string, subject quad.IRI, detail string) quad.IRI {
	id := "finding:" + idKey(rule) + ":" + string(subject)
	if detail != "" {
		id += "#" + idKey(detail)
	}
	return quad.IRI(id)
}

func ruleID(name string) quad.IRI { return quad.IRI("rule:" + idKey(name)) }

func itemID(specSlug, itemSlug string) quad.IRI {
	return quad.IRI("item:" + idKey(specSlug) + "/" + idKey(itemSlug))
}

// lit returns a string literal. JSON writes, JSON reads and `?sub=` filters
// reinterpret strings that look like IRIs, blank nodes or typed values
// (GRAPH_SCHEMA.md, F4), so those are wrapped in backticks to stay plain strings.
func lit(s string) quad.String {
	if quad.StringToValue(s) != quad.Value(quad.String(s)) {
		s = "`" + s + "`"
	}
	return quad.String(s)
}

// quadSet collects the quads of one layer under that layer's label.
type quadSet struct {
	label quad.IRI
	quads []quad.Quad
}

func newQuadSet(layer string) *quadSet { return &quadSet{label: LayerLabel(layer)} }

func (s *quadSet) add(subj, pred quad.IRI, obj quad.Value) {
	s.quads = append(s.quads, quad.Quad{Subject: subj, Predicate: pred, Object: obj, Label: s.label})
}

func (s *quadSet) str(subj, pred quad.IRI, v string) { s.add(subj, pred, lit(v)) }
func (s *quadSet) num(subj, pred quad.IRI, v int)    { s.add(subj, pred, quad.Int(v)) }
func (s *quadSet) flag(subj, pred quad.IRI, v bool)  { s.add(subj, pred, quad.Bool(v)) }
