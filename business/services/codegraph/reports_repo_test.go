package codegraph_test

import (
	"encoding/json"
	"strings"
	"testing"

	"petars-dev/govibe/business/services/codegraph"
)

// The reports on this repository's own graph: the one known layering violation
// is on the layers page, the routes of the real router are in the route table,
// and no secret reaches the config reference.
func TestReportsOnThisRepo(t *testing.T) {
	m := loadRepoModel(t)
	o := codegraph.LinkOptions{Root: "/repo"}

	layers := m.LayersReport(o)
	var found bool
	for _, v := range layers.Violations {
		found = found || (v.Package != nil && v.Package.ID == "pkg:controllers" && strings.Contains(v.Finding.Message, "storage/dbexplorer") && v.Path == "controllers/devdb.go")
	}
	if !found || !layers.FindingsBuilt {
		t.Errorf("controllers -> storage/dbexplorer should be a violation on the layers report: %+v", layers.Violations)
	}
	for _, r := range layers.Matrix.Rows {
		for _, c := range r.Cells {
			if r.From == "controllers" && c.To == "storage" && (c.Count == 0 || !c.Violation) {
				t.Errorf("controllers -> storage: %+v", c)
			}
			if r.From == "business" && c.To == "storage" && c.Violation {
				t.Errorf("business -> storage is allowed: %+v", c)
			}
		}
	}

	routes := m.RoutesReport(o)
	var hello bool
	for _, r := range routes.Routes {
		hello = hello || (r.Route.ID == "route:GET:/api/hello" && r.Handler != nil && r.Handler.ID == "func:controllers.HelloAPI" &&
			r.Handler.EditorURL != "" && strings.HasPrefix(r.Handler.EditorURL, "vscode://file/repo/controllers/api.go:"))
	}
	if !hello || routes.AppRoutes == 0 || routes.DevRoutes == 0 {
		t.Errorf("routes report: %d app, %d dev, hello %v", routes.AppRoutes, routes.DevRoutes, hello)
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

	// This repository's migration creates no tables: an empty schema, with the migration listed.
	schema := m.SchemaReport(o)
	if len(schema.Tables) != 0 || len(schema.Migrations) != 1 {
		t.Errorf("schema report: %d tables, %d migrations", len(schema.Tables), len(schema.Migrations))
	}
}
