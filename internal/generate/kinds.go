package generate

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/petars-dev/govibe/internal/rules"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// Kinds lists what `govibe make` makes, in the order the help shows them.
var Kinds = []string{"page", "resource", "action", "service", "store", "migration", "middleware", "command", "config", "spec"}

// Options are the flags of a generator. Each kind reads the ones that concern it.
type Options struct {
	// Force overwrites a file that exists instead of refusing.
	Force bool
	// Path is the route of a page (default "/<name>").
	Path string
	// Schedule is a command's cron spec; "" for a CLI-only command.
	Schedule string
	// Short is a command's one-line description.
	Short string
	// Table is the table a migration creates; "" for an empty migration.
	Table string
	// Section is the env.yml section a typed config binds (default: the app's name).
	Section string
}

// Make runs the generator of kind for name. Nothing is written: the result says
// what to write.
func Make(p *Project, kind, name string, o Options) (*Result, error) {
	switch kind {
	case "page":
		return Page(p, name, o)
	case "resource":
		return Resource(p, name, o)
	case "action":
		return Action(p, name, o)
	case "service":
		return Service(p, name, o)
	case "store":
		return Store(p, name, o)
	case "migration":
		return Migration(p, name, o)
	case "middleware":
		return Middleware(p, name, o)
	case "command":
		return Command(p, name, o)
	case "config":
		return Config(p, o)
	case "spec":
		return Spec(p, name, o)
	}
	return nil, fmt.Errorf("govibe make: no generator %q (one of %s)", kind, strings.Join(Kinds, ", "))
}

// common is what every template sees.
type common struct {
	Module, Framework, AppName string
}

func (p *Project) common() common {
	return common{Module: p.Module, Framework: p.Framework, AppName: p.Name}
}

// new prepares the result for one file the generator creates.
func (p *Project) create(r *Result, rel string, content []byte, o Options) error {
	if !o.Force {
		if err := p.refuseExisting(rel); err != nil {
			return err
		}
	}
	r.add(rel, content)
	return nil
}

// --- page ---

// Page makes a templ page, the controller that renders it and its route.
func Page(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	route := o.Path
	if route == "" {
		route = "/" + n.Kebab
	}
	if !strings.HasPrefix(route, "/") || strings.ContainsAny(route, " \t") {
		return nil, fmt.Errorf("--path %q: a route starts with '/' and has no spaces", route)
	}
	if strings.HasPrefix(route, "/dev") || strings.HasPrefix(route, "/_govibe") {
		return nil, fmt.Errorf("--path %q: /dev and /_govibe are the framework's; the app cannot register there", route)
	}
	r := &Result{Kind: "page", Templ: true}
	tdir := p.templatesDir()
	cdir := p.controllersDir()
	data := struct {
		common
		Name                            Name
		Method, Path                    string
		Pkg, TemplatesDir, TemplatesPkg string
		TemplFile                       string
	}{common: p.common(), Name: n, Method: "GET", Path: route, Pkg: path.Base(cdir), TemplatesDir: tdir, TemplatesPkg: path.Base(tdir),
		TemplFile: path.Join(tdir, n.Snake+".templ")}
	tmpl, err := render("page_templ", data)
	if err != nil {
		return nil, err
	}
	if err := p.create(r, data.TemplFile, tmpl, o); err != nil {
		return nil, err
	}
	ctl, err := renderGo("page_controller", data)
	if err != nil {
		return nil, err
	}
	if err := p.create(r, path.Join(cdir, n.Snake+".go"), ctl, o); err != nil {
		return nil, err
	}
	snippet := fmt.Sprintf("r.Get(%q, %s.%s)", route, data.Pkg, n.Pascal)
	p.wireRoute(r, snippet, []string{p.Module + "/" + cdir})
	p.document(r, "Routes", []string{fmt.Sprintf("- `GET %s`: the %s page.", route, n.Spaced())})
	return r, nil
}

// wireRoute inserts a routes snippet at the routes marker, or keeps it as a snippet.
func (p *Project) wireRoute(r *Result, snippet string, imports []string) {
	file, src := p.MarkerFile(RoutesMarker)
	if file != "" {
		out, ok, err := insertAtMarker(src, RoutesMarker, snippet, imports)
		if err == nil && ok {
			r.edit(file, out)
			return
		}
		if err != nil {
			r.note("%s: %v", file, err)
		}
	}
	r.snippet(fmt.Sprintf("in routes.Register (no %s marker was found in the wiring)", RoutesMarker), snippet)
}

// wireCommand inserts a command declaration at the commands marker, or keeps it.
func (p *Project) wireCommand(r *Result, snippet string, imports []string) {
	file, src := p.MarkerFile(CommandsMarker)
	if file != "" {
		out, ok, err := insertAtMarker(src, CommandsMarker, snippet, imports)
		if err == nil && ok {
			r.edit(file, out)
			return
		}
		if err != nil {
			r.note("%s: %v", file, err)
		}
	}
	r.snippet(fmt.Sprintf("in the []govibe.Command that routes.Console returns (no %s marker was found in the wiring)", CommandsMarker), snippet)
}

// document appends lines under a heading of the app's architecture doc
// (ARCHITECTURE.md in the first docs directory), or keeps them as a snippet.
func (p *Project) document(r *Result, heading string, lines []string) {
	p.documentIn(r, "ARCHITECTURE.md", heading, lines, false)
}

func (p *Project) documentIn(r *Result, doc, heading string, lines []string, createSection bool) {
	dir := p.docsDir()
	if dir != "" {
		rel := path.Join(dir, doc)
		if md, err := p.read(rel); err == nil {
			// The same doc may be edited twice by one generator (a resource documents two routes
			// and a table): build on the pending edit.
			for _, f := range r.Files {
				if f.Path == rel && f.Edit {
					md = f.Content
				}
			}
			out, ok := insertUnderHeading(md, heading, lines)
			if !ok && createSection {
				out, ok = appendSection(md, heading, lines), true
			}
			if ok {
				r.replaceEdit(rel, out)
				return
			}
		}
	}
	r.snippet(fmt.Sprintf("under \"## %s\" in the architecture doc (every route and command is named in a doc; the graph reports the rest)", heading), strings.Join(lines, "\n"))
}

// replaceEdit records an edit, replacing a pending edit of the same file.
func (r *Result) replaceEdit(rel string, content []byte) {
	for i, f := range r.Files {
		if f.Path == rel && f.Edit {
			r.Files[i].Content = content
			return
		}
	}
	r.edit(rel, content)
}

// --- action, service, store, middleware ---

// Action makes a business action and its test.
func Action(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	dir := p.businessDir() + "/actions"
	data := struct {
		common
		Name Name
		Pkg  string
	}{p.common(), n, path.Base(dir)}
	r := &Result{Kind: "action"}
	for _, t := range []struct{ tmpl, file string }{{"action", n.Snake + ".go"}, {"action_test", n.Snake + "_test.go"}} {
		b, err := renderGo(t.tmpl, data)
		if err != nil {
			return nil, err
		}
		if err := p.create(r, path.Join(dir, t.file), b, o); err != nil {
			return nil, err
		}
	}
	r.note("call %s.%s from a controller or a command; business code imports nothing from the framework", data.Pkg, n.Pascal)
	return r, nil
}

// Service makes a service package and its test.
func Service(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	dir := p.businessDir() + "/services/" + n.Pkg
	data := struct {
		common
		Name Name
		Pkg  string
	}{p.common(), n, n.Pkg}
	r := &Result{Kind: "service"}
	for _, t := range []struct{ tmpl, file string }{{"service", n.Pkg + ".go"}, {"service_test", n.Pkg + "_test.go"}} {
		b, err := renderGo(t.tmpl, data)
		if err != nil {
			return nil, err
		}
		if err := p.create(r, path.Join(dir, t.file), b, o); err != nil {
			return nil, err
		}
	}
	r.note("construct it in routes.Register or routes.Console (svc := %s.New()) and hand it to the controllers and commands that need it", n.Pkg)
	return r, nil
}

// storeData is what the store templates see.
type storeData struct {
	common
	Name     Name // as given
	Singular Name
	Plural   Name
	Pkg      string // orderstore
	Dir      string // storage/orderstore
	Table    string // orders
	Type     string // Order
}

func (p *Project) storeData(n Name) storeData {
	s, pl := singularName(n), pluralName(singularName(n))
	return storeData{common: p.common(), Name: n, Singular: s, Plural: pl, Pkg: s.Pkg + "store", Dir: p.storageDir() + "/" + s.Pkg + "store", Table: pl.Snake, Type: s.Pascal}
}

// Store makes a store package over a table, and its test.
func Store(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	r := &Result{Kind: "store"}
	d := p.storeData(n)
	if err := p.storeFiles(r, d, o); err != nil {
		return nil, err
	}
	r.note("construct it in routes.Register or routes.Console (%s := %s.New(rt.DB)); only business actions and services call it", d.Singular.Camel+"Store", d.Pkg)
	if !p.tableExists(d.Table) {
		r.note("no migration creates the %s table: govibe make migration create_%s --table %s", d.Table, d.Table, d.Table)
	}
	return r, nil
}

func (p *Project) storeFiles(r *Result, d storeData, o Options) error {
	for _, t := range []struct{ tmpl, file string }{{"store", d.Pkg + ".go"}, {"store_test", d.Pkg + "_test.go"}} {
		b, err := renderGo(t.tmpl, d)
		if err != nil {
			return err
		}
		if err := p.create(r, path.Join(d.Dir, t.file), b, o); err != nil {
			return err
		}
	}
	return nil
}

// tableExists reports whether a migration already creates the table.
func (p *Project) tableExists(table string) bool {
	re := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(IF\s+NOT\s+EXISTS\s+)?` + "`?" + regexp.QuoteMeta(table) + "`?" + `\b`)
	for _, f := range p.migrationFiles() {
		if b, err := p.read(path.Join(p.migrationsDir(), f)); err == nil && re.Match(b) {
			return true
		}
	}
	return false
}

// Middleware makes an app middleware and its test.
func Middleware(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	dir := p.middlewareDir()
	data := struct {
		common
		Name Name
		Pkg  string
	}{p.common(), n, path.Base(dir)}
	r := &Result{Kind: "middleware"}
	for _, t := range []struct{ tmpl, file string }{{"middleware", n.Snake + ".go"}, {"middleware_test", n.Snake + "_test.go"}} {
		b, err := renderGo(t.tmpl, data)
		if err != nil {
			return nil, err
		}
		if err := p.create(r, path.Join(dir, t.file), b, o); err != nil {
			return nil, err
		}
	}
	// chi wants every Use before the first route, so the generator does not insert at the
	// routes marker, which comes after them.
	r.snippet("at the top of routes.Register, before any route (chi refuses a Use after a route), or on one group with r.With",
		fmt.Sprintf("r.Use(%s.%s)", data.Pkg, n.Pascal))
	return r, nil
}

// --- migration ---

var migrationRe = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// migrationFiles lists the migration files, sorted.
func (p *Project) migrationFiles() []string {
	dir := p.migrationsDir()
	if dir == "" {
		return nil
	}
	entries, err := fsReadDir(p, dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if migrationRe.MatchString(e) {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// nextMigration is the next version number, zero-padded like the existing ones (four
// digits at least).
func (p *Project) nextMigration() string {
	max, width := 0, 4
	for _, f := range p.migrationFiles() {
		m := migrationRe.FindStringSubmatch(f)
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
		if len(m[1]) > width {
			width = len(m[1])
		}
	}
	return fmt.Sprintf("%0*d", width, max+1)
}

// Migration makes a goose migration; with Table, one that creates the table.
func Migration(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	if p.migrationsDir() == "" {
		return nil, fmt.Errorf("this project declares no migrations directory (govibe.yml: migrations)")
	}
	if o.Table != "" && !tableRe.MatchString(o.Table) {
		return nil, fmt.Errorf("--table %q: a table name is lower-case letters, digits and '_'", o.Table)
	}
	r := &Result{Kind: "migration"}
	if err := p.migrationFile(r, n, o); err != nil {
		return nil, err
	}
	return r, nil
}

var tableRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (p *Project) migrationFile(r *Result, n Name, o Options) error {
	data := struct {
		common
		Name  Name
		Table string
	}{p.common(), n, o.Table}
	b, err := render("migration", data)
	if err != nil {
		return err
	}
	file := path.Join(p.migrationsDir(), p.nextMigration()+"_"+n.Snake+".sql")
	if err := p.create(r, file, b, o); err != nil {
		return err
	}
	if o.Table != "" {
		p.documentIn(r, "MEMORY_SCHEMA.md", "Tables", []string{
			fmt.Sprintf("- `%s`: created by `%s`; columns `id` (the primary key) and `created_at`. Add every column, index and constraint here as migrations change it.", o.Table, file),
		}, true)
	} else {
		r.note("the framework applies it on the next start with a DSN set (./%s migrate up); record the change in %s", p.Name, path.Join(p.docsDir(), "MEMORY_SCHEMA.md"))
	}
	return nil
}

// --- command ---

var useRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*(:[a-z][a-z0-9_-]*)?$`)

// builtinCommands are the framework's own command names (kernel.builtinCommands).
var builtinCommands = []string{"serve", "migrate", "routes", "schedule", "version", "inspect", "help"}

// Command makes a CLI command: its body in the commands package, its declaration at
// the commands marker and, with a schedule, a test on the fake clock.
func Command(p *Project, use string, o Options) (*Result, error) {
	use = strings.TrimSpace(use)
	if !useRe.MatchString(use) {
		return nil, fmt.Errorf("command name %q: use noun:verb, in lower case (items:sync)", use)
	}
	for _, b := range builtinCommands {
		if use == b {
			return nil, fmt.Errorf("command name %q is the framework's own; choose another", use)
		}
	}
	// noun:verb reads as VerbNoun in Go: items:sync is SyncItems.
	noun, verb, hasVerb := strings.Cut(use, ":")
	var fn Name
	var err error
	if hasVerb {
		v, err1 := ParseName(verb)
		nn, err2 := ParseName(noun)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("command name %q: %v", use, firstErr(err1, err2))
		}
		fn, err = ParseName(v.Pascal + nn.Pascal)
	} else {
		fn, err = ParseName(noun)
	}
	if err != nil {
		return nil, err
	}
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	var next time.Time
	if o.Schedule != "" {
		spec, err := scheduler.Parse(o.Schedule, time.UTC, start)
		if err != nil {
			return nil, err
		}
		next = spec.Next(start).UTC()
	}
	short := o.Short
	if short == "" {
		short = fn.Title()
	}
	if strings.ContainsAny(short, "\"\n") {
		return nil, fmt.Errorf("--short: no quotes or newlines")
	}
	dir := p.commandsDir()
	data := struct {
		common
		Use, Short, Func, Pkg, Schedule, NextExpr string
		PackageDoc                                bool
	}{common: p.common(), Use: use, Short: short, Func: fn.Pascal, Pkg: path.Base(dir), Schedule: o.Schedule, PackageDoc: !p.goFilesIn(dir)}
	r := &Result{Kind: "command"}
	body, err := renderGo("command", data)
	if err != nil {
		return nil, err
	}
	if err := p.create(r, path.Join(dir, fn.Snake+".go"), body, o); err != nil {
		return nil, err
	}
	decl := fmt.Sprintf("{Use: %q, Short: %q, ", use, short)
	if o.Schedule != "" {
		decl += fmt.Sprintf("Schedule: govibe.Cron(%q), ", o.Schedule)
	}
	decl += fmt.Sprintf("Run: %s.%s},", data.Pkg, fn.Pascal)
	p.wireCommand(r, decl, []string{p.Module + "/" + dir, p.Framework})
	line := fmt.Sprintf("- `%s`: %s.", use, strings.TrimSuffix(short, "."))
	if o.Schedule != "" {
		line = fmt.Sprintf("- `%s`: %s. Scheduled `%s` while the app serves; by hand, `./%s %s`.", use, strings.TrimSuffix(short, "."), o.Schedule, p.Name, use)
		data.NextExpr = fmt.Sprintf("time.Date(%d, time.%s, %d, %d, %d, 0, 0, time.UTC)", next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute())
		if p.exists("main.go") && strings.Contains(string(mustRead(p, "main.go")), "func newApp()") {
			test, err := renderGo("command_schedule_test", data)
			if err != nil {
				return nil, err
			}
			if err := p.create(r, "schedule_"+fn.Snake+"_test.go", test, o); err != nil {
				return nil, err
			}
		} else {
			r.note("no newApp() in main.go, so no scheduled-run test was written; see the framework's Commands and Scheduler doc for one")
		}
	}
	p.document(r, "Commands", []string{line})
	r.note("run it: go build . && ./%s %s", p.Name, use)
	if o.Schedule != "" {
		r.note("see when it is due: ./%s schedule list", p.Name)
	}
	return r, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func mustRead(p *Project, rel string) []byte {
	b, _ := p.read(rel)
	return b
}

// --- spec ---

// specBodies is the seed text of each section of a spec.
var specBodies = map[string][]string{
	"Description":      {"What [[name]] is, for whom, and why it is needed."},
	"Goals":            {"- The outcomes this spec commits to."},
	"Phases and Tasks": {"1. Phase 1: the first step, and what \"done\" means for it."},
	"Backlog":          {"- What is left out for now, and why."},
	"Status Table":     {"", "| Item | Implemented | Skipped | Why |", "| --- | --- | --- | --- |", "| Phase 1 | No | | Planned |"},
}

// Spec makes a spec with the sections the rules require, in docs/specs.
func Spec(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(strings.TrimPrefix(strings.TrimSuffix(name, ".md"), p.Profile.Docs.SpecPrefix))
	if err != nil {
		return nil, err
	}
	if p.specsDir() == "" {
		return nil, fmt.Errorf("this project declares no specs directory (govibe.yml: docs.specs)")
	}
	title := p.Profile.Docs.SpecPrefix + n.Upper
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", title)
	for _, s := range rules.SpecSections {
		fmt.Fprintf(&b, "\n## %s\n", s.Name)
		for _, line := range specBodies[s.Name] {
			b.WriteString(strings.ReplaceAll(line, "[[name]]", n.Spaced()) + "\n")
		}
	}
	r := &Result{Kind: "spec"}
	if err := p.create(r, path.Join(p.specsDir(), title+".md"), []byte(b.String()), o); err != nil {
		return nil, err
	}
	r.note("update the status table after each development loop; a row marked implemented must name things that exist (the graph checks it)")
	return r, nil
}

// --- resource ---

// Resource makes the vertical slice for a noun: the migration that creates its table, a
// store, a service over the store, a JSON controller, the two routes and their docs.
func Resource(p *Project, name string, o Options) (*Result, error) {
	n, err := ParseName(name)
	if err != nil {
		return nil, err
	}
	if p.migrationsDir() == "" {
		return nil, fmt.Errorf("this project declares no migrations directory (govibe.yml: migrations), so a resource has nowhere to create its table")
	}
	d := p.storeData(n)
	r := &Result{Kind: "resource"}
	// The table.
	if !p.tableExists(d.Table) {
		mo := o
		mo.Table = d.Table
		mn, _ := ParseName("create_" + d.Table)
		if err := p.migrationFile(r, mn, mo); err != nil {
			return nil, err
		}
	} else {
		r.note("a migration already creates the %s table: none was added", d.Table)
	}
	// The store.
	if err := p.storeFiles(r, d, o); err != nil {
		return nil, err
	}
	// The service.
	sdir := p.businessDir() + "/services/" + d.Plural.Pkg
	sdata := struct {
		storeData
		StoreDir, StorePkg, ServiceDir, ServicePkg string
	}{storeData: d, StoreDir: d.Dir, StorePkg: d.Pkg, ServiceDir: sdir, ServicePkg: d.Plural.Pkg}
	sdata.Pkg = d.Plural.Pkg
	for _, t := range []struct{ tmpl, file string }{{"service_resource", d.Plural.Pkg + ".go"}, {"service_resource_test", d.Plural.Pkg + "_test.go"}} {
		b, err := renderGo(t.tmpl, sdata)
		if err != nil {
			return nil, err
		}
		if err := p.create(r, path.Join(sdir, t.file), b, o); err != nil {
			return nil, err
		}
	}
	// The controller.
	cdir := p.controllersDir()
	route := "/api/" + d.Plural.Kebab
	cdata := struct {
		storeData
		StoreDir, StorePkg, ServiceDir, ServicePkg, Path string
	}{storeData: d, StoreDir: d.Dir, StorePkg: d.Pkg, ServiceDir: sdir, ServicePkg: d.Plural.Pkg, Path: route}
	cdata.Pkg = path.Base(cdir)
	ctl, err := renderGo("resource_controller", cdata)
	if err != nil {
		return nil, err
	}
	if err := p.create(r, path.Join(cdir, d.Plural.Snake+".go"), ctl, o); err != nil {
		return nil, err
	}
	// The wiring: construction in the routes function, two routes.
	store, svc := d.Singular.Camel+"Store", d.Singular.Camel+"Svc"
	snippet := strings.Join([]string{
		fmt.Sprintf("%s := %s.New(rt.DB) // a nil rt.DB is fine until a query runs", store, d.Pkg),
		fmt.Sprintf("%s := %s.New(%s)", svc, d.Plural.Pkg, store),
		fmt.Sprintf("r.Get(%q, %s.List%s(%s))", route, cdata.Pkg, d.Plural.Pascal, svc),
		fmt.Sprintf("r.Get(%q, %s.Get%s(%s))", route+"/{id}", cdata.Pkg, d.Singular.Pascal, svc),
	}, "\n")
	p.wireRoute(r, snippet, []string{p.Module + "/" + cdir, p.Module + "/" + sdir, p.Module + "/" + d.Dir})
	p.document(r, "Routes", []string{
		fmt.Sprintf("- `GET %s`: every %s as JSON, newest first.", route, d.Singular.Spaced()),
		fmt.Sprintf("- `GET %s/{id}`: one %s as JSON; 404 when there is none.", route, d.Singular.Spaced()),
	})
	r.note("the table is created on the next start with a DSN set; without one the routes answer 500 with %s.ErrNoDatabase", d.Pkg)
	return r, nil
}
