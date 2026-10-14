package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The document `inspect --json` prints is defined in routeinfo, which the tools that
// read it share; these names keep the kernel's own vocabulary.
type (
	Inspect       = routeinfo.Inspect
	InspectApp    = routeinfo.InspectApp
	InspectModule = routeinfo.InspectModule
	InspectBuild  = routeinfo.InspectBuild
	BuildModule   = routeinfo.BuildModule
	InspectRef    = routeinfo.InspectRef
	InspectRoute  = routeinfo.InspectRoute
	InspectCmd    = routeinfo.InspectCmd
)

// InspectSchema versions the document.
const InspectSchema = routeinfo.InspectSchema

// runInspect is `inspect --json`: steps 1, 2, 4 and 5 of the boot, with no
// database, no dev mount and quiet logging, so stdout is the document alone.
func runInspect(ctx context.Context, app App, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the inspect document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*asJSON {
		return errors.New("inspect: only --json is supported")
	}
	b, err := Build(ctx, app, Options{Quiet: true})
	if err != nil {
		return err
	}
	defer b.Close()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(b.Inspect())
}

// FrameworkModule is the module that holds the framework, derived rather than
// written down so a rename cannot leave it behind.
func FrameworkModule() string {
	return strings.TrimSuffix(reflect.TypeOf(config.Config{}).PkgPath(), "/config")
}

// Inspect builds the document for a booted app.
func (b *Built) Inspect() Inspect {
	doc := Inspect{Schema: InspectSchema, Go: runtime.Version()}
	doc.App.Name = b.App.Name
	doc.App.Contributes = routeinfo.InspectContributes{
		Config: b.App.Config != nil, Migrations: b.App.Migrations != nil, Assets: b.App.Assets != nil,
		Routes: b.App.Routes != nil, Commands: b.App.Commands != nil, Shutdown: b.App.Shutdown != nil,
	}
	doc.Framework.Module = FrameworkModule()
	doc.Framework.Version = "(devel)"
	doc.Build.Modules = []BuildModule{}

	if bi, ok := debug.ReadBuildInfo(); ok {
		doc.App.Module = bi.Main.Path
		for _, d := range bi.Deps {
			m := BuildModule{Path: d.Path, Version: d.Version}
			// A local replace is recorded as `=> ../fw (devel)`: its Version is
			// "(devel)", not empty, and its path is as written in go.mod.
			if d.Replace != nil && (strings.HasPrefix(d.Replace.Path, ".") || strings.HasPrefix(d.Replace.Path, "/")) {
				m.ReplaceDir = d.Replace.Path
			}
			doc.Build.Modules = append(doc.Build.Modules, m)
			if d.Path == doc.Framework.Module {
				doc.Framework.Version = d.Version
			}
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "-trimpath":
				doc.Build.Trimpath = s.Value == "true"
			case "-tags":
				doc.Build.Tags = s.Value
			}
		}
	}
	if b.App.Routes != nil {
		doc.App.RootHint = ref(rootHint(b.App))
	}

	doc.Routes = []InspectRoute{}
	for _, r := range routeinfo.Walk(b.Router) {
		ir := InspectRoute{Method: r.Method, Path: r.Path, Handler: ref(r.Handler), MountedBy: routeinfo.MountedByApp}
		if b.FrameworkRoutes[r.Key()] {
			ir.MountedBy = routeinfo.MountedByFramework
		}
		for _, m := range r.MW {
			ir.Middleware = append(ir.Middleware, ref(m))
		}
		doc.Routes = append(doc.Routes, ir)
	}
	doc.Commands = []InspectCmd{}
	for _, c := range b.Commands {
		doc.Commands = append(doc.Commands, InspectCmd{
			Name: c.Use, Short: c.Short, Handler: ref(routeinfo.FuncRef(reflect.ValueOf(c.Run))),
		})
	}
	return doc
}

// rootHint is the app's Routes function as the runtime reports it: where the app's
// sources were compiled, for the tools that must map a route's handler onto them.
// The runtime names a function of package main "main.f", with no import path, and an
// app's Routes is often written in main (a closure in newApp, say); the binary's own
// build information knows that package's path, so the symbol is completed with it.
func rootHint(app App) routeinfo.Ref {
	if app.Routes == nil {
		return routeinfo.Ref{}
	}
	ref := routeinfo.FuncRef(reflect.ValueOf(app.Routes))
	if bi, ok := debug.ReadBuildInfo(); ok {
		ref.Symbol = qualifyMain(ref.Symbol, bi.Path)
	}
	return ref
}

// qualifyMain turns "main.f" into "<import path of package main>.f"; any other
// symbol is returned as it is.
func qualifyMain(symbol, mainPath string) string {
	if rest, ok := strings.CutPrefix(symbol, "main."); ok && mainPath != "" {
		return mainPath + "." + rest
	}
	return symbol
}

func ref(r routeinfo.Ref) InspectRef { return InspectRef{Symbol: r.Symbol, File: r.File, Line: r.Line} }
