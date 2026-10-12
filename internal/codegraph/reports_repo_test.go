package codegraph_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/petars-dev/govibe/internal/codegraph"
)

// The reports on this repository's own graph: the layers page shows no violation,
// the routes of the real router are in the route table, and no secret reaches the
// config reference.
func TestReportsOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)
	o := codegraph.LinkOptions{Root: "/repo"}

	layers := m.LayersReport(o)
	if len(layers.Violations) != 0 || !layers.FindingsBuilt {
		t.Errorf("the framework has no layering violation: %+v", layers.Violations)
	}
	for _, r := range layers.Matrix.Rows {
		for _, c := range r.Cells {
			if c.Violation {
				t.Errorf("%s -> %s is a violation: %+v", r.From, c.To, c)
			}
		}
	}

	routes := m.RoutesReport(o)
	var htmx bool
	for _, r := range routes.Routes {
		htmx = htmx || (r.Route.ID == "route:GET:/_govibe/static/htmx.min.js" && r.Handler != nil && r.Handler.ID == "func:web.ServeHTMX" &&
			r.Handler.EditorURL != "" && strings.HasPrefix(r.Handler.EditorURL, "vscode://file/repo/web/web.go:"))
	}
	if !htmx || routes.AppRoutes == 0 || routes.DevRoutes == 0 {
		t.Errorf("routes report: %d app, %d dev, htmx %v", routes.AppRoutes, routes.DevRoutes, htmx)
	}

	cfg := m.ConfigReport(o)
	b, _ := json.Marshal(cfg)
	for _, row := range cfg.Keys {
		if row.Sensitive && (row.HasDefault || row.Default != "") {
			t.Errorf("%s: a sensitive key with a default: %+v", row.Name, row)
		}
	}
	if cfg.Sensitive < 1 || len(cfg.Keys) < 9 || strings.Contains(string(b), "@tcp(") {
		t.Errorf("config report: %d keys, %d sensitive", len(cfg.Keys), cfg.Sensitive)
	}

	// The framework owns no migrations: an empty schema.
	schema := m.SchemaReport(o)
	if len(schema.Tables) != 0 || len(schema.Migrations) != 0 {
		t.Errorf("schema report: %d tables, %d migrations", len(schema.Tables), len(schema.Migrations))
	}
}
