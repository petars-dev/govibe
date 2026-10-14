package codegraph

import (
	"path/filepath"

	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// RequiresFramework reports whether the project at root depends on the framework (its
// go.mod requires it): whether it is an application, and so has a binary to ask.
//
// It asks about the real framework, whatever the project's profile names: a project built on
// another module (a test stub) has no binary that answers `inspect --json`.
func RequiresFramework(root string) bool {
	requires, _ := readGoModDeps(filepath.Join(root, "go.mod"))
	_, ok := requires[profile.FrameworkModule()]
	return ok
}

// OptionsFromInspect makes an indexer see a project as its own binary reports it, the
// way the running dev server does: its routes (with who registered each), the
// modules it was built from, the framework's version, and where its handlers' sources
// were compiled. It is how `govibe graph index` runs the route rules offline, with no
// router to read: it asks the app (`inspect --json`: no database, no dev mount, no
// scheduler) and hands the answer over.
//
// The result holds the fields the document fills; the caller sets the rest (Root,
// Schema, Calls, Git, Manifest).
func OptionsFromInspect(doc *routeinfo.Inspect) Options {
	facts := BuildFacts{
		AppName:          doc.App.Name,
		AppModule:        doc.App.Module,
		FrameworkModule:  doc.Framework.Module,
		FrameworkVersion: doc.Framework.Version,
		Tags:             doc.Build.Tags,
		Trimpath:         doc.Build.Trimpath,
		FrameworkRoutes:  map[string]bool{},
	}
	for _, m := range doc.Build.Modules {
		facts.Modules = append(facts.Modules, Module{Path: m.Path, Version: m.Version, ReplaceDir: m.ReplaceDir})
	}
	for _, r := range doc.Routes {
		if r.MountedBy == routeinfo.MountedByFramework {
			facts.FrameworkRoutes[routeinfo.Route{Method: r.Method, Path: r.Path}.Key()] = true
		}
	}
	return Options{
		Build:    facts,
		RootHint: doc.App.RootHint.Ref(),
		RouteSource: func() ([]routeinfo.Route, error) {
			return doc.RouteTable(), nil
		},
	}
}
