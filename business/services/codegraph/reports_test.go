package codegraph

import (
	"encoding/json"
	"strings"
	"testing"
)

// A report is built from the graph alone and must never carry what the graph
// keeps secret. These are the fixture's secrets: sensitive defaults in env.yml
// and a sensitive column default in the migration.
var fixtureSecrets = []string{"user:pass@tcp(db)/fixture", "s3cret", "hunter2", "none"}

func reportOptions(t *testing.T) LinkOptions {
	t.Helper()
	return LinkOptions{Root: mustResolve(t, fixtureRoot), DB: true}
}

func routeRowOf(t *testing.T, rep *RoutesReport, id string) RouteRow {
	t.Helper()
	for _, r := range rep.Routes {
		if r.Route.ID == id {
			return r
		}
	}
	t.Fatalf("no route %s in %d rows", id, len(rep.Routes))
	return RouteRow{}
}

func TestRoutesReportOnTheFixture(t *testing.T) {
	m := fixtureGraph(t)
	o := reportOptions(t)
	rep := m.RoutesReport(o)

	if rep.AppRoutes != 5 || rep.DevRoutes != 1 || len(rep.Routes) != 6 || len(rep.Notes) != 0 {
		t.Errorf("counts: %d app, %d dev, %d rows, notes %v", rep.AppRoutes, rep.DevRoutes, len(rep.Routes), rep.Notes)
	}
	// application routes come first, dev routes after
	seenDev := false
	for _, r := range rep.Routes {
		if seenDev && !r.Dev {
			t.Errorf("app route %s listed after a dev route", r.Route.ID)
		}
		seenDev = seenDev || r.Dev
	}

	hello := routeRowOf(t, rep, "route:GET:/api/hello")
	if hello.Handler == nil || hello.Handler.ID != "func:controllers.HelloAPI" || hello.Handler.Label != "controllers.HelloAPI" ||
		hello.Handler.Path != "controllers/hello.go" || hello.Handler.Line != 10 {
		t.Errorf("handler: %+v", hello.Handler)
	}
	if want := "vscode://file" + o.Root + "/controllers/hello.go:10"; hello.Handler.EditorURL != want {
		t.Errorf("editor link %q, want %q", hello.Handler.EditorURL, want)
	}
	if len(hello.Middleware) != 2 || hello.Method != "GET" || hello.Path != "/api/hello" || hello.Dev {
		t.Errorf("row: %+v", hello)
	}
	if hello.Route.Anchor != Anchor("route:GET:/api/hello") || !strings.HasPrefix(hello.Route.GraphURL, "/dev/graph#node=route") {
		t.Errorf("anchor and graph link: %+v", hello.Route)
	}
	var docs []string
	for _, d := range hello.Docs {
		docs = append(docs, d.ID)
	}
	mustHave(t, "docs that mention GET /api/hello", docs, "doc:docs/public/ARCHITECTURE.md")
	for _, d := range hello.Docs {
		if d.ID == "doc:docs/public/ARCHITECTURE.md" && d.PageURL != "/dev/docs/app/architecture" {
			t.Errorf("a doc links to its page: %+v", d)
		}
	}

	// a library handler has no function, and says what serves it instead
	assets := routeRowOf(t, rep, "route:ANY:/assets/")
	if assets.Handler != nil || assets.LibHandler != "net/http.StripPrefix" {
		t.Errorf("library handler: %+v", assets)
	}
	// an undocumented route carries its finding
	orders := routeRowOf(t, rep, "route:GET:/api/orders/{id}")
	if orders.Findings.Count != 1 || orders.Findings.Worst != "info" || len(orders.Findings.Items) != 1 || len(orders.Docs) != 0 {
		t.Errorf("findings: %+v docs %v", orders.Findings, orders.Docs)
	}
	if dev := routeRowOf(t, rep, "route:ANY:/dev/mcp"); !dev.Dev {
		t.Errorf("dev route: %+v", dev)
	}
}

func TestConfigReportOnTheFixtureNeverShowsASecret(t *testing.T) {
	m := fixtureGraph(t)
	rep := m.ConfigReport(reportOptions(t))
	byName := map[string]ConfigRow{}
	for _, k := range rep.Keys {
		byName[k.Name] = k
	}

	port := byName["server.port"]
	if port.Default != "8080" || !port.HasDefault || port.Sensitive || !port.Declared || port.Env == nil || port.Env.Label != "PORT" || port.UsedByTotal == 0 {
		t.Errorf("server.port: %+v", port)
	}
	dsn := byName["database.dsn"]
	if !dsn.Sensitive || dsn.HasDefault || dsn.Default != "" || dsn.Env == nil {
		t.Errorf("database.dsn must be flagged and carry no default: %+v", dsn)
	}
	for _, name := range []string{"auth.api_key", "queue.broker.password"} {
		if k := byName[name]; !k.Sensitive || k.HasDefault || k.Default != "" {
			t.Errorf("%s: %+v", name, k)
		}
	}
	if byName["region"].Env != nil || byName["region"].Default != "eu-west-1" {
		t.Errorf("a static value has no environment variable: %+v", byName["region"])
	}

	beta := byName["feature.beta"]
	if beta.Declared || beta.UsedByTotal == 0 || beta.Findings.Count != 1 || beta.Findings.Worst != "warning" {
		t.Errorf("a key read but not declared: %+v", beta)
	}
	if k := byName["auth.api_key"]; k.UsedByTotal != 0 || k.Findings.Count != 1 || k.Field == "" {
		t.Errorf("a key nothing reads, bound to a field: %+v", k)
	}
	if rep.Undeclared != 2 || rep.Sensitive != 3 {
		t.Errorf("counts: %d undeclared, %d sensitive", rep.Undeclared, rep.Sensitive)
	}

	// environment variables read in code with no key behind them
	var env []string
	for _, e := range rep.EnvOnly {
		env = append(env, e.Name)
		if e.Name == "FIXTURE_DEBUG" && (e.Declared || len(e.ReadBy) != 1 || e.ReadBy[0].ID != "func:..main" || e.Findings.Count != 1) {
			t.Errorf("FIXTURE_DEBUG: %+v", e)
		}
	}
	mustHave(t, "env vars read directly", env, "FIXTURE_DEBUG")
	mustNotHave(t, "env vars read directly", env, "PORT", "FIXTURE_DSN")

	b, _ := json.Marshal(rep)
	for _, s := range fixtureSecrets[:3] {
		if strings.Contains(string(b), s) {
			t.Errorf("the config report leaks %q", s)
		}
	}
}

func TestSchemaReportOnTheFixture(t *testing.T) {
	m := fixtureGraph(t)
	rep := m.SchemaReport(reportOptions(t))
	tables := map[string]TableReport{}
	var names []string
	for _, tr := range rep.Tables {
		tables[tr.Name] = tr
		names = append(names, tr.Name)
	}
	if strings.Join(names, ",") != "order_items,orders,products,users" || rep.Live || len(rep.Notes) != 0 {
		t.Errorf("tables %v live=%v notes=%v", names, rep.Live, rep.Notes)
	}

	users := tables["users"]
	if users.Source != "migration" || users.Engine != "InnoDB" || users.Comment != "Accounts" || len(users.IntroducedIn) != 1 ||
		users.IntroducedIn[0].Label != "0001_init" || users.IntroducedIn[0].PageURL != "#"+Anchor("mig:0001_init") {
		t.Errorf("users: %+v", users)
	}
	var cols []string
	for _, c := range users.Columns {
		cols = append(cols, c.Name)
		if c.Name == "password_hash" && (c.HasDefault || c.Default != "") {
			t.Errorf("a sensitive column carries a default: %+v", c)
		}
		if c.Name == "created_at" && (!c.HasDefault || c.Default != "current_timestamp()") {
			t.Errorf("a plain default is shown: %+v", c)
		}
		if c.Name == "email" && c.Comment != "login name" {
			t.Errorf("column comment: %+v", c)
		}
	}
	if strings.Join(cols, ",") != "id,email,password_hash,created_at" {
		t.Errorf("columns in table order: %v", cols)
	}
	if len(users.Indexes) < 2 || !users.Indexes[0].Primary {
		t.Errorf("the primary key comes first: %+v", users.Indexes)
	}

	// the implied reference from orders.user_id is inferred, with its confidence
	orders := tables["orders"]
	var implicit *RelationRow
	for i, r := range orders.References {
		if r.Kind == "implicit_ref" {
			implicit = &orders.References[i]
		}
	}
	if implicit == nil || !implicit.Inferred || implicit.Confidence != 95 || implicit.Method != "name_match" ||
		implicit.FromTable.ID != "table:orders" || implicit.FromColumn != "user_id" || implicit.ToTable.ID != "table:users" || implicit.ToColumn != "id" {
		t.Fatalf("orders.user_id -> users.id: %+v", orders.References)
	}
	if implicit.ToTable.PageURL != "#"+Anchor("table:users") {
		t.Errorf("the target links to its row: %+v", implicit.ToTable)
	}
	if orders.Findings.Count == 0 {
		t.Errorf("the reference without a foreign key is a finding about the column: %+v", orders.Findings)
	}
	// ...and users knows who points at it
	var back bool
	for _, r := range users.ReferencedBy {
		back = back || (r.Kind == "implicit_ref" && r.FromTable.ID == "table:orders" && r.FromColumn == "user_id" && r.Inferred)
	}
	if !back {
		t.Errorf("users referenced by: %+v", users.ReferencedBy)
	}

	// declared foreign keys with their rules, and the junction between orders and products
	items := tables["order_items"]
	var fk *RelationRow
	for i, r := range items.References {
		if r.Kind == "foreign_key" && r.FromColumn == "order_id" {
			fk = &items.References[i]
		}
	}
	if fk == nil || fk.Inferred || fk.ToTable.ID != "table:orders" || fk.OnDelete != "CASCADE" || fk.OnUpdate != "RESTRICT" || fk.FKName != "fk_items_order" {
		t.Errorf("order_items.order_id: %+v", items.References)
	}
	var junction *RelationRow
	for i, r := range orders.References {
		if r.Kind == "many_to_many" {
			junction = &orders.References[i]
		}
	}
	if junction == nil || junction.ToTable.ID != "table:products" || junction.Via == nil || junction.Via.ID != "table:order_items" || junction.Confidence != 90 {
		t.Errorf("orders many-to-many: %+v", orders.References)
	}

	// code that touches the table, and the type that maps to it
	var readers []string
	for _, f := range orders.ReadBy {
		readers = append(readers, f.Label)
	}
	mustHave(t, "readers of orders", readers, "orderstore.Store.List")
	if len(orders.WrittenBy) == 0 || len(orders.MappedTo) == 0 || orders.MappedTo[0].Label != "orderstore.Order" {
		t.Errorf("writers %v mapped %v", orders.WrittenBy, orders.MappedTo)
	}

	if len(rep.Migrations) != 1 || rep.Migrations[0].Name != "0001_init" || rep.Migrations[0].Applied != "unknown" || len(rep.Migrations[0].Tables) != 4 {
		t.Errorf("migrations: %+v", rep.Migrations)
	}
	b, _ := json.Marshal(rep)
	if strings.Contains(string(b), "'none'") || strings.Contains(string(b), `"none"`) {
		t.Errorf("the schema report leaks a sensitive default: %s", b)
	}
}

func TestLayersReportOnTheFixture(t *testing.T) {
	m := fixtureGraph(t)
	rep := m.LayersReport(reportOptions(t))

	var layers []string
	rows := map[string]PackageRow{}
	for _, l := range rep.Layers {
		layers = append(layers, l.Name)
		for _, p := range l.Packages {
			rows[p.Package.Path] = p
		}
	}
	// the closed set's order, only layers that have packages
	if got := strings.Join(layers, ","); got != "controllers,business,storage,middleware,wiring,dev,support" {
		t.Errorf("layers: %s", got)
	}
	if p := rows["controllers"]; p.Funcs == 0 || p.Package.Anchor != Anchor("pkg:controllers") || len(p.Imports) == 0 || p.Findings.Count != 1 || p.Findings.Worst != "error" {
		t.Errorf("controllers: %+v", p)
	}
	var imports []string
	for _, i := range rows["controllers"].Imports {
		imports = append(imports, i.Path)
		if i.PageURL != "#"+i.Anchor {
			t.Errorf("an import links to its row on the page: %+v", i)
		}
	}
	mustHave(t, "imports of controllers", imports, "storage/orderstore", "business/actions")

	// the matrix marks the forbidden pairs, whether or not they occur
	cell := func(from, to string) MatrixCell {
		for _, r := range rep.Matrix.Rows {
			if r.From == from {
				for _, c := range r.Cells {
					if c.To == to {
						return c
					}
				}
			}
		}
		t.Fatalf("no matrix cell %s -> %s", from, to)
		return MatrixCell{}
	}
	if c := cell("controllers", "storage"); c.Count != 1 || !c.Violation || !strings.Contains(c.Reason, "only business actions and services may call storage") {
		t.Errorf("controllers -> storage: %+v", c)
	}
	if c := cell("business", "storage"); c.Count == 0 || c.Violation {
		t.Errorf("business -> storage is allowed: %+v", c)
	}
	if c := cell("dev", "storage"); c.Violation {
		t.Errorf("dev may import anything: %+v", c)
	}
	if c := cell("controllers", "controllers"); !c.Same {
		t.Errorf("the diagonal: %+v", c)
	}

	// the rule is stated from the same function the findings use
	if strings.Join(rep.Exempt, ",") != "wiring,dev" || len(rep.Rule) == 0 || len(rep.Principles) != 3 {
		t.Errorf("rule %+v exempt %v principles %v", rep.Rule, rep.Exempt, rep.Principles)
	}
	// the sentences of the page agree with the function that decides
	forbidden := map[string][]string{}
	for _, r := range rep.Rule {
		for _, f := range r.Forbidden {
			forbidden[r.From] = append(forbidden[r.From], f.To)
		}
	}
	for _, from := range []string{"controllers", "middleware", "views", "support"} {
		if strings.Join(forbidden[from], ",") != "storage" {
			t.Errorf("%s may call storage only through business: %v", from, forbidden[from])
		}
	}
	if got := strings.Join(forbidden["storage"], ","); got != "controllers,business,middleware,wiring,dev,views" {
		t.Errorf("storage depends on nothing above it: %s", got)
	}
	if got := strings.Join(forbidden["business"], ","); got != "controllers,middleware,wiring,dev,views" {
		t.Errorf("business does not depend on the HTTP layer: %s", got)
	}
	for _, r := range rep.Rule {
		if r.From == "wiring" || r.From == "dev" {
			t.Errorf("an exempt layer has a rule: %+v", r)
		}
	}

	if !rep.FindingsBuilt || len(rep.Violations) != 1 {
		t.Fatalf("violations: %+v", rep.Violations)
	}
	v := rep.Violations[0]
	if v.Finding.Rule != "layering" || v.Package == nil || v.Package.ID != "pkg:controllers" || v.Path != "controllers/orders.go" || v.Line == 0 ||
		!strings.HasSuffix(v.EditorURL, "/controllers/orders.go:"+itoa(v.Line)) {
		t.Errorf("violation: %+v", v)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A graph that lacks the layers a report reads says so, instead of an empty page.
func TestReportsSayWhichLayersAreMissing(t *testing.T) {
	m := modelWithout(t, "routes")
	rep := m.RoutesReport(LinkOptions{})
	if len(rep.Routes) != 0 || !strings.Contains(strings.Join(rep.Notes, " "), "routes layer is not built") {
		t.Errorf("%+v", rep)
	}
	// no editor links without a root
	m = fixtureGraph(t)
	for _, r := range m.RoutesReport(LinkOptions{}).Routes {
		if r.Handler != nil && r.Handler.EditorURL != "" {
			t.Errorf("an editor link without a root: %+v", r.Handler)
		}
	}
	lr := modelWithout(t, "findings").LayersReport(LinkOptions{})
	if lr.FindingsBuilt || len(lr.Violations) != 0 || !strings.Contains(strings.Join(lr.Notes, " "), "findings layer is not built") {
		t.Errorf("layers without findings: %+v", lr.Notes)
	}
	if c := lr.Matrix.Rows[0]; len(c.Cells) == 0 {
		t.Error("the matrix does not need the findings layer")
	}
	// no tables at all: an empty report, not a failure
	empty := modelOf(nil)
	if rep := empty.SchemaReport(LinkOptions{}); len(rep.Tables) != 0 || rep.Tables == nil || rep.Migrations == nil {
		t.Errorf("empty schema: %+v", rep)
	}
}

func TestReportList(t *testing.T) {
	rs := Reports()
	if len(rs) != 4 || rs[0].Name != "routes" || rs[3].URL != "/dev/graph/reports/layers" {
		t.Errorf("%+v", rs)
	}
	if ReportTitle("Schema") != "Database schema" || ReportTitle("nope") != "" {
		t.Error("report titles")
	}
}
