package scaffold

import (
	"fmt"
	"strings"
)

// GoMod is the app's go.mod: the framework at the tool's version (or a replaced
// checkout), the libraries the demo imports, and the tools the Makefile runs with `go
// tool` (templ, air, the framework's own tool), every one pinned. `go mod tidy` fills in
// the rest. The `go` line is written explicitly: `go mod init` would write the local
// toolchain's, and a tool fetched at @latest would raise it.
func GoMod(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo %s\n\n", o.Module, GoVersion)
	fmt.Fprintf(&b, "require (\n\t%s %s\n\t%s %s\n\t%s %s\n\t%s %s\n)\n\n", TemplModule, TemplVersion, AirModule, AirVersion, ChiModule, ChiVersion, o.FrameworkModule, o.FrameworkVersion)
	fmt.Fprintf(&b, "tool (\n\t%s/cmd/templ\n\t%s\n\t%s/cmd/govibe\n)\n", TemplModule, AirModule, o.FrameworkModule)
	if o.Replace != "" {
		fmt.Fprintf(&b, "\nreplace %s => %s\n", o.FrameworkModule, o.Replace)
	}
	return b.String()
}
