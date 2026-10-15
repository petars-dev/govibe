package codegraph

import (
	"context"
	"strings"
	"testing"

	"github.com/cayleygraph/quad"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The facts the commands layer promises, asserted apart from the golden so that a wrongly
// regenerated golden is still caught.
func TestCommandsLayerFixtureFacts(t *testing.T) {
	out := buildLayer(t, fixtureWorld(t), commandsLayer{})
	if len(out.warnings) != 0 {
		t.Errorf("warnings: %v", out.warnings)
	}
	f := facts{out.quads}

	// A command is run by the function that holds its Run: a closure resolves to the factory
	// that contains it, a plain function to itself.
	for cmd, fn := range map[string]string{
		"command:orders:export":  "func:commands.ExportOrders",
		"command:reports:digest": "func:commands.Digest",
		"command:cache:warm":     "func:commands.WarmCache",
	} {
		if got := f.one(t, cmd, "run_by"); got != iri(fn) {
			t.Errorf("%s run_by %v, want %s", cmd, got, fn)
		}
		if got := f.one(t, cmd, "origin"); got != str("app") {
			t.Errorf("%s origin = %v: the framework declares no commands of its own", cmd, got)
		}
		if got := f.one(t, cmd, "type"); got != iri("kind:command") {
			t.Errorf("%s type = %v", cmd, got)
		}
	}
	if got := f.one(t, "command:orders:export", "description"); got != str("Export yesterday's orders") {
		t.Errorf("description = %v", got)
	}

	// The schedule a command declared, and nothing of one on a command that has none.
	for _, tc := range []struct {
		cmd, pred string
		want      quad.Value
	}{
		{"command:orders:export", "scheduled", quad.Bool(true)},
		{"command:orders:export", "schedule", str("0 2 * * *")},
		{"command:orders:export", "timezone", str("UTC")},
		{"command:orders:export", "overlap", str("skip")},
		{"command:orders:export", "timeout", str("10m0s")},
		{"command:reports:digest", "schedule", str("@every 6h")},
		{"command:reports:digest", "every", str("6h0m0s")},
		{"command:reports:digest", "overlap", str("allow")},
		{"command:cache:warm", "scheduled", quad.Bool(false)},
	} {
		if got := f.one(t, tc.cmd, tc.pred); got != tc.want {
			t.Errorf("%s %s = %v, want %v", tc.cmd, tc.pred, got, tc.want)
		}
	}
	for _, pred := range []string{"schedule", "timezone", "overlap", "timeout", "every"} {
		if got := f.out("command:cache:warm", pred); len(got) != 0 {
			t.Errorf("a command with no schedule carries %s = %v", pred, got)
		}
	}
	if got := f.out("command:orders:export", "every"); len(got) != 0 {
		t.Errorf("only an @every schedule has an interval: %v", got)
	}
	if got := f.out("command:reports:digest", "timeout"); len(got) != 0 {
		t.Errorf("no timeout was declared: %v", got)
	}
}

// A Run that is not application code is said so, not linked or invented; a command table
// that is absent is a skipped layer (nothing is cleared), one that is empty is a built layer.
func TestCommandsLayerOutsideTheHappyPath(t *testing.T) {
	lib := func() ([]routeinfo.InspectCmd, error) {
		return []routeinfo.InspectCmd{{Name: "tools:lib", Handler: routeinfo.InspectRef{Symbol: "github.com/spf13/cobra.(*Command).Execute", File: "/home/dev/go/pkg/mod/github.com/spf13/cobra@v1.8.0/command.go", Line: 1}}}, nil
	}
	opts := fixtureOptions()
	opts.CommandSource = lib
	out := buildLayer(t, newTestWorld(t, fixtureRoot, opts), commandsLayer{})
	f := facts{out.quads}
	if got := f.out("command:tools:lib", "run_by"); len(got) != 0 {
		t.Errorf("a library function must not be linked to code: %v", got)
	}
	if got := f.one(t, "command:tools:lib", "handler"); !strings.Contains(string(got.(quad.String)), "cobra") {
		t.Errorf("handler label = %v", got)
	}

	opts.CommandSource = nil
	skipped := buildLayer(t, newTestWorld(t, fixtureRoot, opts), commandsLayer{})
	if skipped.skipped == "" || skipped.clear || len(skipped.quads) != 0 || !strings.Contains(skipped.skipped, "no command table") {
		t.Errorf("no source must skip without clearing: %+v", skipped)
	}

	opts.CommandSource = func() ([]routeinfo.InspectCmd, error) { return nil, nil }
	empty := buildLayer(t, newTestWorld(t, fixtureRoot, opts), commandsLayer{})
	if empty.skipped != "" || len(empty.quads) != 0 {
		t.Errorf("an app with no commands is a built, empty layer: %+v", empty)
	}
}

// OptionsFromInspect hands the commands of the binary's own document to the layer.
func TestOptionsFromInspectFeedsTheCommandsLayer(t *testing.T) {
	doc := &routeinfo.Inspect{Schema: routeinfo.InspectSchema, Commands: fixtureCommands()}
	opts := OptionsFromInspect(doc)
	if opts.CommandSource == nil {
		t.Fatal("no command source")
	}
	cs, err := opts.CommandSource()
	if err != nil || len(cs) != 3 || cs[0].Schedule == nil || cs[2].Schedule != nil {
		t.Errorf("%v %v", cs, err)
	}
	empty := OptionsFromInspect(&routeinfo.Inspect{Schema: routeinfo.InspectSchema})
	if empty.CommandSource == nil {
		t.Error("a document with no commands still says so: the layer is built, with nothing in it")
	}
}

// A doc names a command by the name it is run with, or as it is typed at a prompt.
func TestDocsResolveCommandsByNameOrAsTyped(t *testing.T) {
	w := fixtureWorld(t)
	ci, _ := w.code()
	rt, _ := w.routeTable()
	ct, _ := w.commandTable()
	ti, _ := w.templates()
	cx, _ := w.configIndex()
	dm, _ := w.dbModel()
	sc, _ := w.docScan()
	rs := newResolver(w.root, mustProfile(t, w), sc, ci, rt, ct, ti, cx, dm)
	rs.appName = "fixture"

	for tok, want := range map[string]string{
		"orders:export":            "command:orders:export",
		"./fixture reports:digest": "command:reports:digest",
		"./anything cache:warm":    "command:cache:warm",     // a path to a binary, whatever it is called
		"fixture reports:digest":   "command:reports:digest", // the app's own name
		"command:orders:export":    "command:orders:export",
	} {
		got := rs.resolveToken(tok, ".")
		if len(got) != 1 || string(got[0]) != want {
			t.Errorf("%q resolves to %v, want %s", tok, got, want)
		}
	}
	for _, tok := range []string{
		"orders:nope",           // not a command of this app
		"other reports:digest",  // a word that is neither a path nor the app
		"orders:export --day 1", // flags are not part of a name: left unresolved, never guessed
		"cache",                 // a plain word is not taken for a command
	} {
		for _, id := range rs.resolveToken(tok, ".") {
			if strings.HasPrefix(string(id), "command:") {
				t.Errorf("%q must not resolve to a command, got %s", tok, id)
			}
		}
	}
	// A command is never judged stale: documents use names for things not built yet.
	if _, stale := rs.staleToken("orders:export", "."); stale {
		t.Error("a command name is never a stale reference")
	}
	if _, stale := rs.staleToken("orders:gone", "."); stale {
		t.Error("an unknown name with a colon is not a stale reference either")
	}
}

// docs.command_undocumented: one finding for the command nothing mentions, at the place
// that runs it, and it goes away when a doc names it.
func TestUndocumentedCommandFindingFollowsTheDocs(t *testing.T) {
	m := fixtureGraph(t)
	ids := byRule(m)["docs.command_undocumented"]
	if len(ids) != 1 || ids[0] != "finding:docs.command_undocumented:command:cache:warm" {
		t.Fatalf("findings: %v", ids)
	}
	f := m.Node(ids[0])
	if f.Str("severity") != "info" || f.first("about") != "command:cache:warm" || f.Str("path") != "commands/export.go" || f.Int("line") != 31 ||
		!strings.Contains(f.Str("message"), "`cache:warm`") || !strings.Contains(f.Str("message"), "naming it in backticks") {
		t.Errorf("finding: %+v", f)
	}
	// Documented ones, scheduled or by hand, are not reported.
	for _, id := range ids {
		if strings.Contains(id, "orders:export") || strings.Contains(id, "reports:digest") {
			t.Errorf("a documented command is reported: %s", id)
		}
	}

	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:finding')).Has(g.IRI('rule'), 'docs.command_undocumented').All()`); len(got) != 1 {
		t.Fatalf("before: %v", got)
	}
	appendTo(t, root, "docs/public/ARCHITECTURE.md", "\nThe `cache:warm` command fills the cache before a deploy.\n")
	if rep, err := ix.Refresh(ctx); err != nil || rep == nil {
		t.Fatalf("refresh: %v %v", rep, err)
	}
	if got := gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:finding')).Has(g.IRI('rule'), 'docs.command_undocumented').All()`); len(got) != 0 {
		t.Errorf("after documenting it: %v", got)
	}
}

// The rule says it did not look when there is no command table, instead of reporting a
// clean bill: a run that does not know the application's commands (an offline index with no
// binary to ask) cannot say that none is undocumented.
func TestUndocumentedCommandRuleIsNotCheckedWithoutACommandTable(t *testing.T) {
	r := ruleByName(t, "docs.command_undocumented")
	if r.severity != sevInfo || len(r.needs) != 2 || r.needs[0] != "commands" {
		t.Errorf("rule: %+v", r)
	}
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root, opts.CommandSource = fixtureRoot, nil
	rep, err := New(h.QuadStore, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		if l.Name == "commands" && l.Status != statusSkipped {
			t.Errorf("commands layer without a table: %+v", l)
		}
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	n := m.Node("rule:docs.command_undocumented")
	if n == nil || n.Bool("checked") || !strings.Contains(n.Str("note"), "commands layer is not built") {
		t.Errorf("without the commands layer the rule has not looked: %+v", n)
	}
	if len(byRule(m)["docs.command_undocumented"]) != 0 || len(m.byKind["command"]) != 0 {
		t.Error("an unchecked rule has no findings, and nothing invents a command")
	}
}

func ruleByName(t *testing.T, name string) rule {
	t.Helper()
	for _, r := range ruleCatalog() {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no rule %s", name)
	return rule{}
}

// A command follows the controllers' layering rule: it may call business, not storage.
func TestACommandThatImportsAStoreIsALayeringViolation(t *testing.T) {
	ctx := context.Background()
	ix, h, root := fixtureIndexer(t)
	if _, err := ix.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:finding')).Has(g.IRI('rule'), 'layering').All()`) {
		if strings.Contains(id, "pkg:commands") {
			t.Fatalf("the fixture's commands package is clean, but %s", id)
		}
	}
	writeFile(t, root, "commands/direct.go", `package commands

import "example.com/fixture/storage/orderstore"

// Direct reaches into storage, which a command must not.
func Direct(s *orderstore.Store) bool { return s != nil }
`)
	if rep, err := ix.Refresh(ctx); err != nil || rep == nil {
		t.Fatalf("refresh: %v %v", rep, err)
	}
	got := gizmoIDs(t, h, `g.V().Has(g.IRI('type'), g.IRI('kind:finding')).Has(g.IRI('rule'), 'layering').All()`)
	var found string
	for _, id := range got {
		if strings.Contains(id, "pkg:commands") {
			found = id
		}
	}
	if found == "" {
		t.Fatalf("a command importing a store must be a layering finding: %v", got)
	}
	m, err := Load(ctx, h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if f := m.Node(strings.Trim(found, "<>")); f == nil || f.Str("severity") != "error" || !strings.Contains(f.Str("message"), "commands") || !strings.Contains(f.Str("message"), "storage") || f.Str("path") != "commands/direct.go" {
		t.Errorf("finding: %+v", f)
	}
}

// Without call edges, a command in a package that imports a touched package is a possible,
// not proven, reach, the way a route is; with them it is exact and the chain is shown.
func TestImpactOfATableReachesCommands(t *testing.T) {
	m := fixtureGraph(t)
	res := impact(t, m, ImpactQuery{Node: "table:orders", Kinds: []string{"command"}})
	got := byID(res)
	for _, id := range []string{"command:orders:export", "command:reports:digest"} {
		a, ok := got[id]
		if !ok || a.Kind != "command" || a.Confidence > 60 || (a.Method != methodPkgImport && a.Method != methodPkgScope) {
			t.Errorf("%s without call edges is an estimate from imports: %+v (found %v)", id, a, ok)
		}
	}
	if len(res.Notes) == 0 {
		t.Error("the answer must say what it cannot see")
	}

	deep := fixtureDeepGraph(t)
	res = impact(t, deep, ImpactQuery{Node: "table:orders", Kinds: []string{"command"}})
	got = byID(res)
	exp, ok := got["command:orders:export"]
	if !ok {
		t.Fatalf("with call edges orders:export reaches orders: %v", affectedIDs(res))
	}
	if exp.Access != "read" || exp.Method != methodDispatch || exp.Confidence != 70 {
		t.Errorf("orders:export: %+v (Service.List calls the store through the Lister interface)", exp)
	}
	wantChain := "command:orders:export,func:commands.ExportOrders,func:business/services/orders.Service.List,func:storage/orderstore.Store.List,table:orders"
	if strings.Join(exp.Through, ",") != wantChain {
		t.Errorf("chain %v, want %s", exp.Through, wantChain)
	}
	if _, ok := got["command:cache:warm"]; ok {
		t.Error("with call edges a command that calls nothing reaches no table: cache:warm is not an answer")
	}
	// The command that runs a function is affected when the function changes.
	fn := byID(impact(t, deep, ImpactQuery{Node: "func:commands.ExportOrders"}))
	if a, ok := fn["command:orders:export"]; !ok || a.Via != "run_by" || a.Confidence != 100 {
		t.Errorf("changing the function that runs a command affects the command: %+v", a)
	}
	// Changing the service reaches the commands that go through it, and the routes.
	svc := byID(impact(t, deep, ImpactQuery{Node: "func:business/services/orders.Service.List"}))
	for _, id := range []string{"command:orders:export", "command:reports:digest", "route:GET:/api/orders"} {
		if _, ok := svc[id]; !ok {
			t.Errorf("changing Service.List must reach %s: %v", id, affectedIDs(&ImpactResult{Affected: mapValues(svc)}))
		}
	}
}

func mapValues(m map[string]Affected) []Affected {
	out := make([]Affected, 0, len(m))
	for _, a := range m {
		out = append(out, a)
	}
	return out
}

func TestContextOfACommandAndOfWhatTouchesIt(t *testing.T) {
	m := fixtureDeepGraph(t)
	c, err := m.Context(ContextQuery{Node: "command:orders:export"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Commands) != 1 {
		t.Fatalf("commands: %+v", c.Commands)
	}
	ci := c.Commands[0]
	if ci.Name != "orders:export" || !ci.Scheduled || ci.Schedule != "0 2 * * *" || ci.Timezone != "UTC" || ci.Overlap != "skip" || ci.Timeout != "10m0s" || ci.Handler != "func:commands.ExportOrders" {
		t.Errorf("command: %+v", ci)
	}
	if c.Handler == nil || c.Handler.ID != "func:commands.ExportOrders" || c.ArchLayer != "commands" || c.Package == nil || c.Package.ID != "pkg:commands" {
		t.Errorf("handler/package/layer: %+v %+v %q", c.Handler, c.Package, c.ArchLayer)
	}
	if c.Reach == nil || len(c.Reach.Tables) == 0 || c.Reach.Tables[0].Table.ID != "table:orders" {
		t.Errorf("a command is followed to the tables it reaches, as a handler is: %+v", c.Reach)
	}

	// A table lists the commands that reach it, scheduled first, with how sure and how.
	tc, err := m.Context(ContextQuery{Table: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range tc.Commands {
		names = append(names, c.Name)
		if c.Name == "orders:export" && (c.Access != "read" || c.Confidence != 70 || !c.Scheduled || c.Schedule == "") {
			t.Errorf("how orders:export reaches the table: %+v", c)
		}
	}
	if strings.Join(names, ",") != "orders:export,reports:digest" {
		t.Errorf("table commands = %v, want the two that reach it, by name", names)
	}

	// The function that runs a command says so.
	fc, err := m.Context(ContextQuery{Symbol: "commands.ExportOrders"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.Commands) != 1 || fc.Commands[0].Name != "orders:export" {
		t.Errorf("function context commands: %+v", fc.Commands)
	}
	// A command whose Run is not application code says why nothing is traced.
	lib := fixtureGraphWithCommand(t, routeinfo.InspectCmd{Name: "tools:lib", Handler: routeinfo.InspectRef{Symbol: "github.com/spf13/cobra.(*Command).Execute", File: "/x/pkg/mod/github.com/spf13/cobra@v1.8.0/command.go", Line: 1}})
	lc, err := lib.Context(ContextQuery{Node: "command:tools:lib"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lc.Notes) == 0 || !strings.Contains(strings.Join(lc.Notes, " "), "not application code") {
		t.Errorf("notes: %v", lc.Notes)
	}
}

// fixtureGraphWithCommand indexes the fixture with one more command in its table.
func fixtureGraphWithCommand(t *testing.T, extra routeinfo.InspectCmd) *Model {
	t.Helper()
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root = fixtureRoot
	base := opts.CommandSource
	opts.CommandSource = func() ([]routeinfo.InspectCmd, error) {
		cs, _ := base()
		return append(cs, extra), nil
	}
	if _, err := New(h.QuadStore, opts).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTableRefsListCommandsWithTheirSchedule(t *testing.T) {
	m := fixtureDeepGraph(t)
	refs, err := m.TableRefs("orders", LinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs.Commands) != 2 {
		t.Fatalf("commands: %+v", refs.Commands)
	}
	for _, it := range refs.Commands {
		if it.Kind != "command" || it.Schedule == "" || it.Access != "read" || it.Graph == "" {
			t.Errorf("%+v", it)
		}
	}
	if refs.Commands[0].Schedule != "0 2 * * *" && refs.Commands[1].Schedule != "0 2 * * *" {
		t.Errorf("a command is listed with the schedule it runs on: %+v", refs.Commands)
	}
	if refs.Commands == nil || (&TableRefs{}).Commands != nil {
		t.Error("an empty group is an empty list in JSON, not null")
	}
}

func TestPathFromACommandToATable(t *testing.T) {
	m := fixtureDeepGraph(t)
	res, err := m.Path(PathQuery{From: "command:orders:export", To: "table:orders"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || len(res.Steps) == 0 {
		t.Fatalf("%+v", res)
	}
	steps := res.Steps
	if len(steps) == 0 || steps[0].Predicate != "run_by" || steps[0].From != "command:orders:export" || steps[0].To != "func:commands.ExportOrders" {
		t.Errorf("the first step leaves a command through run_by: %+v", steps)
	}
}

func TestCommandsReport(t *testing.T) {
	rep := fixtureDeepGraph(t).CommandsReport(LinkOptions{})
	if rep.Total != 3 || rep.Scheduled != 2 || len(rep.Commands) != 3 || !rep.Calls {
		t.Fatalf("report: total=%d scheduled=%d calls=%v rows=%d", rep.Total, rep.Scheduled, rep.Calls, len(rep.Commands))
	}
	// scheduled first, then by name
	var order []string
	for _, r := range rep.Commands {
		order = append(order, r.Name)
	}
	if strings.Join(order, ",") != "orders:export,reports:digest,cache:warm" {
		t.Errorf("order = %v", order)
	}
	exp := rep.Commands[0]
	if !exp.Scheduled || exp.Schedule != "0 2 * * *" || exp.Timezone != "UTC" || exp.Overlap != "skip" || exp.Timeout != "10m0s" || exp.Description == "" ||
		exp.Handler == nil || exp.Handler.Label != "commands.ExportOrders" || len(exp.Tables) != 1 || exp.Tables[0].Table.Label != "orders" || exp.Docs == nil || len(exp.Docs) == 0 {
		t.Errorf("orders:export: %+v", exp)
	}
	if exp.Command.Anchor == "" || exp.Command.GraphURL == "" {
		t.Errorf("a row must be linkable: %+v", exp.Command)
	}
	warm := rep.Commands[2]
	if warm.Scheduled || warm.Schedule != "" || warm.Findings.Count != 1 || warm.Findings.Items[0].Rule != "docs.command_undocumented" || len(warm.Docs) != 0 {
		t.Errorf("cache:warm: %+v", warm)
	}
	if rep.Commands[1].Every != "6h0m0s" {
		t.Errorf("reports:digest: %+v", rep.Commands[1])
	}
	found := false
	for _, r := range Reports() {
		if r.Name == "commands" && r.URL == "/dev/graph/reports/commands" && ReportTitle("commands") == "Commands and schedules" {
			found = true
		}
	}
	if !found {
		t.Error("the commands report is listed")
	}
	if empty := modelWithout(t, "commands").CommandsReport(LinkOptions{}); empty.Total != 0 || empty.Commands == nil {
		t.Errorf("no commands layer: an empty list, not null: %+v", empty)
	}
}

func TestAppMapAndDocsCoverageShowCommands(t *testing.T) {
	m := fixtureDeepGraph(t).scoped(ScopeApp)
	v := viewOf(t, m, ViewQuery{})
	var exp, warm *ViewNode
	for i := range v.Nodes {
		switch v.Nodes[i].ID {
		case "command:orders:export":
			exp = &v.Nodes[i]
		case "command:cache:warm":
			warm = &v.Nodes[i]
		}
	}
	if exp == nil || warm == nil {
		t.Fatalf("commands are on the app map: %v", viewIDs(v))
	}
	if exp.Label != "orders:export · 0 2 * * *" || exp.Flag != "scheduled" || exp.Col != 0 {
		t.Errorf("a scheduled command shows its schedule and is flagged: %+v", exp)
	}
	if warm.Label != "cache:warm" || warm.Flag != "" {
		t.Errorf("a CLI-only command shows its name: %+v", warm)
	}
	if e := v.edge("command:orders:export", "run_by", "func:commands.ExportOrders"); e == nil {
		t.Error("a command is drawn with the function that runs it")
	}
	if e := v.edge("func:business/services/orders.Service.List", "reads_table", "table:orders"); e == nil && v.edge("func:storage/orderstore.Store.List", "reads_table", "table:orders") == nil {
		t.Error("and on to the tables it reaches (with call edges)")
	}
	if e := v.edge("doc:docs/public/ARCHITECTURE.md", "mentions", "command:orders:export"); e == nil {
		t.Error("with the docs that mention it")
	}
	// The docs-coverage view judges commands too: the undocumented one is flagged.
	cov := viewOf(t, m, ViewQuery{Preset: "docs_coverage"})
	for _, n := range cov.Nodes {
		if n.ID == "command:cache:warm" && n.Flag != "uncovered" {
			t.Errorf("an undocumented command is uncovered: %+v", n)
		}
		if n.ID == "command:orders:export" && n.Flag == "uncovered" {
			t.Errorf("a documented command is covered: %+v", n)
		}
	}
}

func viewIDs(v *ViewResult) []string {
	var out []string
	for _, n := range v.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestSchemaOverviewSearchAndScopeKnowCommands(t *testing.T) {
	m := fixtureGraph(t)
	sc := m.Schema(nil)
	var kind *KindInfo
	for i := range sc.Kinds {
		if sc.Kinds[i].Kind == "command" {
			kind = &sc.Kinds[i]
		}
	}
	if kind == nil || kind.Count != 3 || kind.Layer != "commands" || !strings.HasPrefix(kind.ID, "command:") {
		t.Errorf("kind: %+v", kind)
	}
	var sawRunBy bool
	for _, p := range sc.Predicates {
		if p.Predicate == "run_by" {
			sawRunBy = p.Structure && p.What != ""
		}
	}
	if !sawRunBy {
		t.Error("run_by is a documented structural predicate")
	}
	// the example query is made from this graph's own command
	var ex string
	for _, e := range sc.Examples {
		if e.Question == "What runs a command?" {
			ex = e.Query
		}
	}
	if ex != "g.V(g.IRI('command:cache:warm')).Out(g.IRI('run_by')).All()" && ex != "g.V(g.IRI('command:orders:export')).Out(g.IRI('run_by')).All()" && ex != "g.V(g.IRI('command:reports:digest')).Out(g.IRI('run_by')).All()" {
		t.Errorf("the example names a command of this graph: %q", ex)
	}

	ov := m.Overview(nil)
	for _, want := range []string{"## Commands", "`orders:export`", "`0 2 * * *`", "`@every 6h`", "CLI only", "commands.ExportOrders", "(commands/export.go:14)"} {
		if !strings.Contains(ov, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	if i, j := strings.Index(ov, "`orders:export`"), strings.Index(ov, "`cache:warm`"); i < 0 || j < 0 || i > j {
		t.Error("scheduled commands come before the CLI-only ones")
	}

	// search by the words an agent uses
	hits := m.Search("export", []string{"cmd"}, 5)
	if len(hits) == 0 || hits[0].ID != "command:orders:export" {
		t.Errorf("search: %+v", hits)
	}

	// scope: commands are the app's own
	app, fw := m.scoped(ScopeApp), m.scoped(ScopeFramework)
	if len(app.byKind["command"]) != 3 || len(fw.byKind["command"]) != 0 {
		t.Errorf("scope app sees %d commands, scope framework %d", len(app.byKind["command"]), len(fw.byKind["command"]))
	}
	if o := app.originOf(app.nodes["command:orders:export"]); o != OriginApp {
		t.Errorf("origin = %q", o)
	}
}

// The example stock query is a real one for a graph that has a command.
func TestCommandExampleUsesTheGraphsOwnCommand(t *testing.T) {
	h, ix := fixtureStore(t)
	_ = ix
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.examples() {
		if e.Question != "What runs a command?" {
			continue
		}
		res, err := gizmoRun(t, h, e.Query)
		if err != nil || len(res) == 0 {
			t.Errorf("%q returns nothing: %v %v", e.Query, res, err)
		}
		return
	}
	t.Error("no command example")
}

// A project that declares no commands has a built, empty layer; the overview then says so
// rather than leaving the section out as if the layer had not been built.
func TestACommandlessAppHasABuiltEmptyLayer(t *testing.T) {
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root = fixtureRoot
	opts.CommandSource = func() ([]routeinfo.InspectCmd, error) { return nil, nil }
	rep, err := New(h.QuadStore, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		if l.Name == "commands" && (l.Status != statusOK || l.Quads != 0) {
			t.Errorf("commands layer: %+v", l)
		}
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.byKind["command"]) != 0 {
		t.Error("no command nodes")
	}
	if got := byRule(m)["docs.command_undocumented"]; len(got) != 0 {
		t.Errorf("findings: %v", got)
	}
	if r := m.Node("rule:docs.command_undocumented"); r == nil || !r.Bool("checked") {
		t.Errorf("with a built layer the rule ran and found nothing: %+v", r)
	}
}

// With call edges an entry point is several hops from its table; the table's references
// must still list the routes and tools that reach it (a walk of one hop found none).
func TestTableRefsFindEntryPointsThroughCallChains(t *testing.T) {
	m := fixtureDeepGraph(t)
	refs, err := m.TableRefs("orders", LinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	for _, r := range refs.Routes {
		routes = append(routes, r.ID)
	}
	want := map[string]int{"route:GET:/api/orders": 70, "route:GET:/api/orders/{id}": 90}
	if len(routes) != len(want) {
		t.Fatalf("routes = %v, want %v", routes, want)
	}
	for _, r := range refs.Routes {
		if conf, ok := want[r.ID]; !ok || r.Confidence != conf {
			t.Errorf("%s: confidence %d, want %d", r.ID, r.Confidence, conf)
		}
	}
	// What reads the table itself is still one hop: callers further up are not "code that touches it".
	var code []string
	for _, c := range refs.Code {
		code = append(code, c.ID)
	}
	for _, id := range code {
		if id == "func:commands.ExportOrders" || id == "func:controllers.ListOrders" {
			t.Errorf("the code group lists what touches the table, not everything that calls it: %s", id)
		}
	}
}

// A bare command name resolves, as a table or a tool name does.
func TestACommandNameResolvesWithoutItsPrefix(t *testing.T) {
	m := fixtureGraph(t)
	got := m.Resolve("orders:export")
	if len(got) != 1 || got[0].ID != "command:orders:export" {
		t.Fatalf("Resolve(orders:export) = %v", got)
	}
	c, err := m.Context(ContextQuery{Node: "orders:export"})
	if err != nil || len(c.Commands) != 1 {
		t.Errorf("graph_context by bare name: %v %v", c, err)
	}
}

// A scheduled command comes before one run by hand, in graph_impact as in graph_context, and
// the answer says how it is scheduled: "which scheduled commands write this table" is
// answerable from it.
func TestImpactListsScheduledCommandsFirstWithTheirSchedule(t *testing.T) {
	m := fixtureGraphWithCommand(t, routeinfo.InspectCmd{Name: "aaa:cli", Short: "by hand",
		Handler: routeinfo.InspectRef{Symbol: "example.com/fixture/commands.WarmCache", File: "commands/export.go", Line: 31}})
	res := impact(t, m, ImpactQuery{Node: "table:orders", Kinds: []string{"command"}})
	var names []string
	for _, a := range res.Affected {
		names = append(names, a.Name)
		switch a.Name {
		case "orders:export":
			if a.Schedule != "0 2 * * *" {
				t.Errorf("a scheduled command carries its schedule: %+v", a)
			}
		case "aaa:cli":
			if a.Schedule != "" {
				t.Errorf("a command run by hand has none: %+v", a)
			}
		}
	}
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	if _, ok := idx["aaa:cli"]; !ok || idx["orders:export"] > idx["aaa:cli"] || idx["reports:digest"] > idx["aaa:cli"] {
		t.Errorf("scheduled commands come before aaa:cli (which sorts first by name): %v", names)
	}
	refs, err := m.TableRefs("orders", LinkOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var refNames []string
	for _, it := range refs.Commands {
		refNames = append(refNames, it.Name)
	}
	if strings.Join(refNames, ",") != strings.Join(names, ",") {
		t.Errorf("the table's references list them in the same order: %v vs %v", refNames, names)
	}
}

// A view cut at its node limit drops routes before it drops a scheduled command: what runs
// with nobody watching is what the map is for.
func TestScheduledCommandsSurviveANodeLimit(t *testing.T) {
	m := fixtureGraph(t).scoped(ScopeApp)
	v := viewOf(t, m, ViewQuery{Limit: 6})
	have := map[string]bool{}
	for _, n := range v.Nodes {
		have[n.ID] = true
	}
	if !have["command:orders:export"] || !have["command:reports:digest"] {
		t.Errorf("the scheduled commands are in a view cut at 6 nodes: %v", viewIDs(v))
	}
}

// A command whose Run is a closure declared inside a wiring function resolves to that
// function, which constructs dependencies and so calls whatever changes: reported as certain
// it would put every inline command in doubt at 100. It is possible, not proven.
func TestACommandInlineInAWiringFunctionIsOnlyPossible(t *testing.T) {
	h, _ := openStore(t)
	root := copyTree(t, fixtureRoot)
	opts := fixtureOptions()
	opts.Root, opts.Calls = root, true
	base := opts.CommandSource
	opts.CommandSource = func() ([]routeinfo.InspectCmd, error) {
		cs, _ := base()
		// Line 17 of routes/console.go is inside routes.Console: an inline closure would resolve there.
		return append(cs, routeinfo.InspectCmd{Name: "inline:run", Short: "declared inline", Handler: routeinfo.InspectRef{Symbol: "example.com/fixture/routes.Console.func1", File: "routes/console.go", Line: 17}}), nil
	}
	if _, err := New(h.QuadStore, opts).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := Load(context.Background(), h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	// routes.Console calls storage/orderstore.New, so changing it affects Console, whose
	// inline closure is the command's Run.
	a, ok := byID(impact(t, m, ImpactQuery{Node: "func:storage/orderstore.New", Kinds: []string{"command"}}))["command:inline:run"]
	if !ok {
		t.Fatal("the inline command is reached through Console")
	}
	if a.Confidence != 60 || a.Method != methodWiring {
		t.Errorf("an inline command is possible (60, %s), not certain: %+v", methodWiring, a)
	}
	if _, ok := byID(impact(t, m, ImpactQuery{Node: "func:storage/orderstore.New", Kinds: []string{"command"}}))["command:orders:export"]; ok {
		t.Error("a command whose factory is not the wiring function is not reached by what only the wiring function calls")
	}
}

// An offline run (no command table) leaves the commands an earlier run wrote, and says the
// layer was skipped; it does not clear them.
func TestAnOfflineRunKeepsTheCommandsOfTheEarlierRun(t *testing.T) {
	ctx := context.Background()
	h, _ := openStore(t)
	opts := fixtureOptions()
	opts.Root = fixtureRoot
	if _, err := New(h.QuadStore, opts).Run(ctx); err != nil {
		t.Fatal(err)
	}
	offline := opts
	offline.CommandSource = nil
	rep, err := New(h.QuadStore, offline).Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range rep.Layers {
		if l.Name == "commands" && l.Status != statusSkipped {
			t.Errorf("commands layer offline: %+v", l)
		}
	}
	m, err := Load(ctx, h.QuadStore)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(m.byKind["command"]); n != 3 {
		t.Errorf("the offline run cleared the commands: %d left", n)
	}
	if st := m.layerStatus("docs"); st != statusPartial {
		t.Errorf("docs says it could not resolve command mentions: %s", st)
	}
}
