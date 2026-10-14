package devtools

import (
	"context"
	"reflect"
	"strings"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/devmcp"
	"github.com/petars-dev/govibe/internal/profile"
)

// FrameworkManifest is what the framework publishes about itself for the graph of a
// project built on it: its settings (the defaults file it ships and the Go fields of
// its typed Config) and the MCP tools its dev server offers. version is the
// framework's version in the build, "" when unknown.
func FrameworkManifest(ctx context.Context, version string) (*codegraph.Manifest, error) {
	tools, err := devmcp.Tools(ctx)
	if err != nil {
		return nil, err
	}
	m := &codegraph.Manifest{
		Module:       profile.FrameworkModule(),
		Version:      version,
		ConfigYAML:   config.Defaults(),
		ConfigFields: map[string]string{},
	}
	configFields(reflect.TypeOf(config.Config{}), "", "", m.ConfigFields)
	for _, t := range tools {
		m.Tools = append(m.Tools, codegraph.ManifestTool{Name: t.Name, Description: t.Description})
	}
	return m, nil
}

// configFields maps the Go fields of a struct to the keys its yaml tags name:
// "Server.Port" is "server.port". Groups are listed as well as leaves, so a chain that
// reads past a field (`cfg.Server.Port.String`) still finds its key.
func configFields(t reflect.Type, goPrefix, keyPrefix string, out map[string]string) {
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue // the resolved tree the dynamic helpers read
		}
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		goPath, keyPath := goPrefix+f.Name, keyPrefix+name
		out[goPath] = keyPath
		configFields(f.Type, goPath+".", keyPath+".", out)
	}
}
