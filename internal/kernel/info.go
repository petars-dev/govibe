package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"text/tabwriter"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// runRoutes is `routes`: every route of the finished router, in registration order,
// and who registered it. Like inspect it boots with no database, no dev tools and
// quiet logging, so it works in production and prints nothing but the table.
func runRoutes(ctx context.Context, app App, args []string, out io.Writer) error {
	if len(args) > 0 {
		return errors.New("routes takes no arguments")
	}
	b, err := Build(ctx, app, Options{Quiet: true})
	if err != nil {
		return err
	}
	defer b.Close()
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "METHOD\tPATH\tHANDLER\tREGISTERED BY")
	for _, r := range routeinfo.Walk(b.Router) {
		by := "app"
		if b.FrameworkRoutes[r.Key()] {
			by = "framework"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Method, r.Path, r.Handler.Symbol, by)
	}
	return tw.Flush()
}

// runVersion is `version`: the app, the framework it was built against and the Go
// toolchain, read from the binary's own build information. It boots nothing.
func runVersion(app App, args []string, out io.Writer) error {
	if len(args) > 0 {
		return errors.New("version takes no arguments")
	}
	name := strings.TrimSpace(app.Name)
	if name == "" {
		name = "app"
	}
	module, version := FrameworkModule(), "(devel)"
	if bi, ok := debug.ReadBuildInfo(); ok {
		version = frameworkVersion(bi.Deps, module)
	}
	fmt.Fprintf(out, "%s\ngovibe %s %s\n%s\n", name, module, version, runtime.Version())
	return nil
}

// frameworkVersion is the framework's version among a binary's dependencies, with
// the directory a local `replace` points at: a checkout under development has the
// placeholder version v0.0.0, which alone would read as a release. A binary built
// inside the framework's own workspace lists no such dependency: "(devel)".
func frameworkVersion(deps []*debug.Module, module string) string {
	for _, d := range deps {
		if d.Path != module {
			continue
		}
		if d.Replace != nil && d.Replace.Path != "" {
			return d.Version + " (replaced by " + d.Replace.Path + ")"
		}
		return d.Version
	}
	return "(devel)"
}
