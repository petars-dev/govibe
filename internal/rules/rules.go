// Package rules holds the rules of SKILL.md as data: which architecture layer may
// import which, which framework packages each layer may import, and the sections a
// spec has. The graph's findings enforce them, `govibe doctor` checks the rest, and the
// SKILL.md a generated app carries is rendered from the same tables, so the text an
// agent reads and the check CI runs cannot drift apart.
package rules

import (
	"fmt"
	"sort"
	"strings"
)

// Layering: which architecture layer may import which (SKILL.md, "Key Constraints"):
//
//   - only business actions and services may call the storage layer;
//   - storage is the lowest layer: it depends on nothing above it;
//   - business logic does not depend on the HTTP layer or on commands (a command is the
//     CLI twin of a controller: a thin adapter that calls business, not the other way
//     round).
//
// `wiring` (main, routes) and `dev` packages assemble the application and may import
// anything. Everything else is checked, including a controller that is only used by
// dev pages: a controller that touches storage is the violation the rule exists to find.

// Exempt are the layers that may import anything.
var Exempt = []string{"wiring", "dev"}

// Principles state the layering rule in words, for the pages and the SKILL.md text.
var Principles = []string{
	"Only business actions and services may call the storage layer.",
	"Storage is the lowest layer: it depends on nothing above it, only on support code.",
	"Business logic does not depend on the HTTP layer or on commands (controllers, commands, middleware, views) or on wiring and dev code.",
}

// LayerVerdict explains why a package of architecture layer `from` must not import one
// of layer `to`; it returns "" when the import is allowed.
func LayerVerdict(from, to string) string {
	if from == to || from == "" || to == "" || isExempt(from) {
		return ""
	}
	switch from {
	case "storage":
		switch to {
		case "support":
			return ""
		}
		return "storage is the lowest layer: it must not depend on " + to + " packages"
	case "business":
		switch to {
		case "controllers", "commands", "middleware", "views", "wiring", "dev":
			return "business logic must not depend on " + to + " packages (controllers call business, not the other way round)"
		}
	}
	if to == "storage" && from != "business" {
		// controllers, commands, middleware, views, support: nothing but business may call storage.
		return "only business actions and services may call storage; " + from + " packages should go through them"
	}
	return ""
}

func isExempt(layer string) bool {
	for _, l := range Exempt {
		if l == layer {
			return true
		}
	}
	return false
}

// The framework's public packages, by import path relative to the framework module.
const (
	PkgGovibe     = ""           // the root package: App, Runtime, Command, Main
	PkgWeb        = "web"        // render, JSON, error and htmx helpers, middleware
	PkgConfig     = "config"     // the layered configuration
	PkgGovibetest = "govibetest" // boots an App in a test
)

// frameworkImports says which of those each layer may import. The point is a small
// surface: business logic and storage know nothing of the framework (they stay plain
// Go, testable without it), views and controllers need only the web helpers, a command
// needs the types that describe it, and only the wiring sees the lifecycle API.
var frameworkImports = map[string][]string{
	"business":    nil,
	"storage":     nil,
	"controllers": {PkgWeb},
	"middleware":  {PkgWeb},
	"views":       {PkgWeb},
	"commands":    {PkgGovibe}, // the RunFunc and Runtime types only
	"support":     {PkgConfig},
	"wiring":      {PkgGovibe, PkgConfig, PkgWeb},
}

// frameworkImportWhy is the sentence that says why a layer takes what it takes.
var frameworkImportWhy = map[string]string{
	"business":    "business logic imports nothing from the framework, so it stays plain Go that tests without it",
	"storage":     "storage imports nothing from the framework: a store takes a *sqlx.DB from its constructor",
	"controllers": "controllers are thin adapters and import only the web helpers",
	"middleware":  "middleware imports only the web helpers",
	"views":       "views import only the web helpers (web.HTMXScript, web.IfDev)",
	"commands":    "commands are thin adapters and import only govibe, for the RunFunc and Runtime types",
	"support":     "support packages import only govibe/config",
	"wiring":      "wiring (main, routes) imports govibe, govibe/config and govibe/web",
}

// FrameworkImportVerdict explains why a package of the given layer must not import
// the framework package rel (its import path under the framework module; "" is the root
// package); it returns "" when the import is allowed. internal reports whether the
// path is inside the framework's internal/: never part of the API.
func FrameworkImportVerdict(layer, rel string, internal bool) string {
	if layer == "dev" {
		return "" // the framework's own dev tooling
	}
	name := pkgName(rel)
	switch {
	case internal:
		return fmt.Sprintf("%s is inside the framework's internal/, which is not API: it can change in any release", name)
	case rel == PkgGovibetest:
		return "govibetest is for tests: no package of the application imports it"
	}
	for _, ok := range frameworkImports[layer] {
		if ok == rel {
			return ""
		}
	}
	why, known := frameworkImportWhy[layer]
	if !known {
		return ""
	}
	return fmt.Sprintf("%s is not one of the framework packages a %s package may import: %s", name, layer, why)
}

// FrameworkImports lists the framework packages a layer may import, for the rendered
// rules and the reports.
func FrameworkImports(layer string) []string {
	var out []string
	for _, rel := range frameworkImports[layer] {
		out = append(out, pkgName(rel))
	}
	sort.Strings(out)
	return out
}

func pkgName(rel string) string {
	if rel == PkgGovibe {
		return "govibe"
	}
	return "govibe/" + rel
}

// RelativePath is a framework import path under the framework module: "web" for
// <module>/web, "" for the root package. ok is false when the path is not the framework's.
func RelativePath(frameworkModule, importPath string) (rel string, ok bool) {
	if importPath == frameworkModule {
		return PkgGovibe, true
	}
	if rel, ok = strings.CutPrefix(importPath, frameworkModule+"/"); ok {
		return rel, true
	}
	return "", false
}

// SpecSections are the sections a spec has, in order (SKILL.md, "Spec-Driven
// Development Guidelines"). Each has the headings that count for it.
var SpecSections = []SpecSection{
	{"Description", []string{"description"}},
	{"Goals", []string{"goals"}},
	{"Phases and Tasks", []string{"phases and tasks", "phases & tasks", "phases", "tasks"}},
	{"Backlog", []string{"backlog"}},
	{"Status Table", []string{"status table", "status"}},
}

// SpecSection is one required section of a spec.
type SpecSection struct {
	Name     string
	Headings []string // lower-case, any of which names the section
}

// MissingSpecSections lists the sections a spec lacks, given the headings it has.
func MissingSpecSections(headings []string) []string {
	have := map[string]bool{}
	for _, h := range headings {
		have[strings.ToLower(strings.TrimSpace(h))] = true
	}
	var missing []string
	for _, s := range SpecSections {
		found := false
		for _, h := range s.Headings {
			found = found || have[h]
		}
		if !found {
			missing = append(missing, s.Name)
		}
	}
	return missing
}

// Markdown renders the rules as a document: the layering rule, what each layer may
// import from the framework, and the sections of a spec. It is the text an agent reads
// when a project has no SKILL.md of its own, and the framework's "rules" doc.
func Markdown() string {
	var b strings.Builder
	b.WriteString("# Rules of this project\n\nThese are the rules the graph's findings enforce (`govibe check`).\n\n")
	b.WriteString("## Layering\n\n")
	for _, p := range Principles {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	fmt.Fprintf(&b, "- Packages in the layers %s may import anything: they assemble the application.\n", strings.Join(Exempt, " and "))
	b.WriteString("\n## What each layer may import from the framework\n\n")
	for _, l := range []string{"business", "storage", "controllers", "middleware", "views", "commands", "support", "wiring"} {
		imports := FrameworkImports(l)
		if len(imports) == 0 {
			fmt.Fprintf(&b, "- %s: nothing\n", l)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", l, strings.Join(imports, ", "))
	}
	b.WriteString("\nNo application package imports `govibe/internal/...` or `govibetest` (tests only).\n")
	b.WriteString("\n## Specs\n\nEvery spec in the specs directory has these sections:\n\n")
	for i, s := range SpecSections {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s.Name)
	}
	return b.String()
}
