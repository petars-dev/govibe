package devui

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/codegraph"
)

// DevGraphReports serves the generated reference pages: the index at
// /dev/graph/reports and each report at /dev/graph/reports/{name} (routes, config,
// schema, layers). They are rendered from the graph on every request, after
// bringing any stale layer up to date (the same refresh as the graph page), so an
// edit to a doc, a migration or a source file shows up on the next load and the
// pages cannot drift from the code. dbUI says whether /dev/db is mounted, for
// the links into it. A failed refresh is logged and the stored graph is served.
func DevGraphReports(ix *codegraph.Indexer, dbUI bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.ToLower(chi.URLParam(r, "name"))
		if name != "" && codegraph.ReportTitle(name) == "" {
			http.Error(w, "unknown report; the reports are: "+strings.Join(codegraph.ReportNames, ", "), http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), graphRefreshTimeout)
		defer cancel()
		if _, err := ix.Refresh(ctx); err != nil {
			logrus.WithError(err).Warn("graph index: refresh before a report failed; serving the stored graph")
		}
		m, err := ix.Model(ctx)
		if err != nil {
			logrus.WithError(err).Error("graph report: read the graph")
			http.Error(w, "the graph could not be read: "+err.Error(), http.StatusInternalServerError)
			return
		}
		opts := codegraph.LinkOptions{Root: ix.RootDir(), DB: dbUI}

		page := reportPage{Name: name, Reports: codegraph.Reports(), AsOf: m.AsOf()}
		switch name {
		case "":
			page.Title = "Reports"
		case "routes":
			rep := m.RoutesReport(opts)
			page.Title, page.AsOf, page.Notes, page.Data = codegraph.ReportTitle(name), rep.AsOf, rep.Notes, rep
		case "config":
			rep := m.ConfigReport(opts)
			page.Title, page.AsOf, page.Notes, page.Data = codegraph.ReportTitle(name), rep.AsOf, rep.Notes, rep
		case "schema":
			rep := m.SchemaReport(opts)
			page.Title, page.AsOf, page.Notes, page.Data = codegraph.ReportTitle(name), rep.AsOf, rep.Notes, rep
		case "layers":
			rep := m.LayersReport(opts)
			page.Title, page.AsOf, page.Notes, page.Data = codegraph.ReportTitle(name), rep.AsOf, rep.Notes, rep
		}

		// Render to a buffer first: a template error must not leave half a page.
		var buf bytes.Buffer
		if err := reportTemplates.ExecuteTemplate(&buf, "page", page); err != nil {
			logrus.WithError(err).Error("graph report: render")
			http.Error(w, "the report could not be rendered", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	}
}

// reportPage is what every report template is given.
type reportPage struct {
	Name    string // "" for the index
	Title   string
	AsOf    string
	Notes   []string
	Reports []codegraph.ReportInfo
	Data    any // the report: *codegraph.RoutesReport and so on
}

var reportFuncs = template.FuncMap{
	// editorHref lets the vscode:// links through html/template's URL filter. Only
	// a URL codegraph.EditorURL built is accepted; anything else becomes "#".
	"editorHref": func(u string) template.URL {
		if strings.HasPrefix(u, "vscode://file/") {
			return template.URL(u)
		}
		return template.URL("#")
	},
	// sev maps a severity (or one of its synonyms) onto the three CSS classes.
	"sev": func(s string) string {
		switch strings.ToLower(s) {
		case "error", "high", "critical":
			return "error"
		case "warning", "warn", "medium":
			return "warning"
		}
		return "info"
	},
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	"yesno": func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	},
}

var reportTemplates = template.Must(template.New("reports").Funcs(reportFuncs).Parse(reportsHTML))

// reportsHTML holds the page chrome and one template per report. Everything
// dynamic goes through html/template's escaping; the only URL it trusts without
// checking is the vscode:// link built by codegraph.EditorURL (see editorHref).
const reportsHTML = `
{{define "page"}}<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · GoVibe Dev Graph reports</title>
<link rel="icon" href="/_govibe/static/govibe-logo.png">
<link rel="stylesheet" href="/_govibe/static/theme.css">
<link rel="stylesheet" href="/_govibe/static/docs.css">
<script src="/_govibe/static/theme.js"></script>
<style>` + reportsCSS + `</style>
</head><body class="docs-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/_govibe/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev Graph</span></div>
<nav class="top-tabs"><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a><a class="tab active" href="/dev/graph">Graph</a><a class="tab" href="/dev/db">DB</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>
<main class="rep">
<nav class="rep-nav" aria-label="Reports"><a href="/dev/graph">&larr; Graph explorer</a><a href="/dev/graph/reports"{{if eq .Name ""}} class="current"{{end}}>Reports</a>{{$name := .Name}}{{range .Reports}}<a href="{{.URL}}"{{if eq .Name $name}} class="current"{{end}}>{{.Title}}</a>{{end}}</nav>
<h1>{{.Title}}</h1>
{{if .Name}}<p class="asof">Generated from the project graph, as of <code>{{.AsOf}}</code>. The page is rebuilt on every load, so it cannot drift from the code.</p>{{end}}
{{if .Notes}}<div class="notes" role="note"><strong>Not the whole picture:</strong><ul>{{range .Notes}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
{{if eq .Name ""}}{{template "index" .}}
{{else if eq .Name "routes"}}{{template "routes" .Data}}
{{else if eq .Name "config"}}{{template "config" .Data}}
{{else if eq .Name "schema"}}{{template "schema" .Data}}
{{else if eq .Name "layers"}}{{template "layers" .Data}}{{end}}
</main></body></html>{{end}}

{{define "index"}}<p class="asof">Reference pages generated from the project graph ({{.AsOf}}). They replace hand-kept tables of routes, settings, schema and packages.</p>
<ul class="rep-index">{{range .Reports}}<li><a href="{{.URL}}">{{.Title}}</a><span>{{.What}}</span></li>{{end}}</ul>{{end}}

{{define "ref"}}<a href="{{.GraphURL}}">{{.Label}}</a>{{end}}
{{define "pageref"}}<a href="{{.PageURL}}">{{.Label}}</a>{{end}}
{{define "place"}}{{if .Path}}{{if .EditorURL}}<a class="loc" href="{{editorHref .EditorURL}}">{{.Path}}{{if .Line}}:{{.Line}}{{end}}</a>{{else}}<span class="loc">{{.Path}}{{if .Line}}:{{.Line}}{{end}}</span>{{end}}{{end}}{{end}}
{{define "docs"}}{{if .}}<ul class="plain">{{range .}}<li><a href="{{if .PageURL}}{{.PageURL}}{{else}}{{.GraphURL}}{{end}}">{{.Label}}</a></li>{{end}}</ul>{{else}}<span class="muted">none</span>{{end}}{{end}}
{{define "findings"}}{{if .Count}}<details class="fnd"><summary><span class="sev sev-{{sev .Worst}}">{{.Count}} {{.Worst}}</span></summary><ul class="plain">{{range .Items}}<li><span class="sev sev-{{sev .Severity}}">{{.Severity}}</span> <code>{{.Rule}}</code> {{.Message}}</li>{{end}}</ul></details>{{else}}<span class="muted">—</span>{{end}}{{end}}

{{define "routes"}}<p class="count">{{.AppRoutes}} application {{plural .AppRoutes "route" "routes"}}, {{.DevRoutes}} dev {{plural .DevRoutes "route" "routes"}} (under <code>/dev</code>).</p>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Method</th><th>Path</th><th>Handler</th><th>Middleware</th>{{if .Calls}}<th>Tables reached</th>{{end}}<th>Documented in</th><th>Findings</th></tr></thead>
<tbody>{{range .Routes}}<tr id="{{.Route.Anchor}}"{{if .Dev}} class="dev"{{end}}>
<td><span class="method">{{.Method}}</span></td>
<td class="mono"><a href="{{.Route.GraphURL}}">{{.Path}}</a>{{if .Dev}} <span class="badge">dev</span>{{end}}</td>
<td>{{if .Handler}}<a href="{{.Handler.GraphURL}}">{{.Handler.Label}}</a><br>{{template "place" .Handler}}{{else if .LibHandler}}<span class="mono">{{.LibHandler}}</span> <span class="badge">library</span>{{else}}<span class="muted">—</span>{{end}}</td>
<td>{{if .Middleware}}<ul class="plain">{{range .Middleware}}<li>{{template "ref" .}}</li>{{end}}</ul>{{else}}<span class="muted">none</span>{{end}}</td>
{{if $.Calls}}<td>{{if .Tables}}<ul class="plain">{{range .Tables}}<li><a href="{{.Table.PageURL}}">{{.Table.Label}}</a> <span class="muted">{{.Access}}{{if lt .Confidence 100}}, {{.Confidence}}%{{end}}</span></li>{{end}}</ul>{{else}}<span class="muted">none</span>{{end}}</td>{{end}}
<td>{{template "docs" .Docs}}</td>
<td>{{template "findings" .Findings}}</td>
</tr>{{else}}<tr><td colspan="{{if $.Calls}}7{{else}}6{{end}}" class="muted">No routes in the graph: the routes layer is built from the running router.</td></tr>{{end}}</tbody></table></div>{{end}}

{{define "config"}}<p class="count">{{len .Keys}} config {{plural (len .Keys) "key" "keys"}} ({{.Sensitive}} sensitive, {{.Undeclared}} read in code but not declared, {{.UnusedCount}} declared but never read). Secrets are never shown: a sensitive setting has no default in the graph.</p>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Key</th><th>Environment variable</th><th>Default</th><th>Declared</th><th>Go field</th><th>Used by</th><th>Documented in</th><th>Findings</th></tr></thead>
<tbody>{{range .Keys}}<tr id="{{.Key.Anchor}}"{{if not .Declared}} class="undeclared"{{end}}>
<td class="mono"><a href="{{.Key.GraphURL}}">{{.Name}}</a>{{if .Sensitive}} <span class="badge secret">sensitive</span>{{end}}</td>
<td class="mono">{{if .Env}}<a href="{{.Env.GraphURL}}">{{.Env.Label}}</a>{{else}}<span class="muted">none</span>{{end}}</td>
<td class="mono">{{if .Sensitive}}<span class="muted">secret – not shown</span>{{else if .HasDefault}}{{if .Default}}{{.Default}}{{else}}<span class="muted">(empty)</span>{{end}}{{else}}<span class="muted">—</span>{{end}}</td>
<td>{{if .Declared}}yes{{else}}<span class="badge warn">not declared</span>{{end}}</td>
<td class="mono">{{if .Field}}{{.Field}}{{else}}<span class="muted">—</span>{{end}}</td>
<td>{{if .UsedBy}}<ul class="plain">{{range .UsedBy}}<li>{{template "ref" .}}</li>{{end}}{{if gt .UsedByTotal 5}}<li class="muted">and {{.UsedByTotal}} in all</li>{{end}}</ul>{{else}}<span class="muted">nothing reads it</span>{{end}}</td>
<td>{{template "docs" .Docs}}</td>
<td>{{template "findings" .Findings}}</td>
</tr>{{else}}<tr><td colspan="8" class="muted">No config keys in the graph.</td></tr>{{end}}</tbody></table></div>
<h2>Environment variables read directly</h2>
<p class="count">Variables the code reads with <code>os.Getenv</code> that no config key is bound to. Prefer a key in <code>config/env.yml</code>.</p>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Variable</th><th>Declared</th><th>Read by</th><th>Documented in</th><th>Findings</th></tr></thead>
<tbody>{{range .EnvOnly}}<tr id="{{.Env.Anchor}}"{{if not .Declared}} class="undeclared"{{end}}>
<td class="mono"><a href="{{.Env.GraphURL}}">{{.Name}}</a>{{if .Sensitive}} <span class="badge secret">sensitive</span>{{end}}</td>
<td>{{if .Declared}}yes{{else}}<span class="badge warn">not declared</span>{{end}}</td>
<td>{{if .ReadBy}}<ul class="plain">{{range .ReadBy}}<li>{{template "ref" .}}</li>{{end}}{{if gt .ReadTotal 5}}<li class="muted">and {{.ReadTotal}} in all</li>{{end}}</ul>{{else}}<span class="muted">—</span>{{end}}</td>
<td>{{template "docs" .Docs}}</td>
<td>{{template "findings" .Findings}}</td>
</tr>{{else}}<tr><td colspan="5" class="muted">None: every variable the code reads is behind a config key.</td></tr>{{end}}</tbody></table></div>{{end}}

{{define "relation"}}{{if eq .Kind "many_to_many"}}<a href="{{.FromTable.PageURL}}">{{.FromTable.Label}}</a> &harr; <a href="{{.ToTable.PageURL}}">{{.ToTable.Label}}</a>{{if .Via}} via <a href="{{.Via.PageURL}}">{{.Via.Label}}</a>{{end}}{{else}}<a href="{{.FromTable.PageURL}}">{{.FromTable.Label}}</a>.<span class="mono">{{.FromColumn}}</span> &rarr; <a href="{{.ToTable.PageURL}}">{{.ToTable.Label}}</a>.<span class="mono">{{.ToColumn}}</span>{{end}}{{if .Inferred}} <span class="badge inferred" title="not declared by the schema: {{.Method}}">inferred{{if .Confidence}} {{.Confidence}}%{{end}}</span>{{else}}{{if or .OnDelete .OnUpdate}} <span class="muted">{{if .OnDelete}}on delete {{.OnDelete}}{{end}}{{if and .OnDelete .OnUpdate}}, {{end}}{{if .OnUpdate}}on update {{.OnUpdate}}{{end}}</span>{{end}}{{if .OneToOne}} <span class="badge">one-to-one</span>{{end}}{{end}}{{end}}

{{define "schema"}}{{if not .Tables}}<p class="count">No tables. The migrations in <code>storage/migrations/sql</code> create none yet, and no database schema was read. Add a migration, or set <code>MARIADB_DSN</code> to read a live database.</p>
{{else}}<p class="count">{{len .Tables}} {{plural (len .Tables) "table" "tables"}}{{if .Live}}, from the migrations merged with the live database{{else}}, from the migrations (no live database was read){{end}}.</p>
<ul class="toc">{{range .Tables}}<li><a href="#{{.Table.Anchor}}">{{.Name}}</a></li>{{end}}</ul>{{end}}
{{range .Tables}}<section class="tbl" id="{{.Table.Anchor}}">
<h2><a href="{{.Table.GraphURL}}">{{.Name}}</a>{{if $.Live}} <span class="badge">{{.Source}}</span>{{end}}{{if .Table.DBURL}} <a class="loc" href="{{.Table.DBURL}}">open in the database explorer</a>{{end}}</h2>
<p class="meta">{{if .Comment}}{{.Comment}} · {{end}}{{if .Engine}}engine {{.Engine}} · {{end}}{{if .IntroducedIn}}introduced in {{range $i, $m := .IntroducedIn}}{{if $i}}, {{end}}<a href="{{$m.PageURL}}">{{$m.Label}}</a>{{end}}{{else}}not created by a migration{{end}}</p>
{{if .Findings.Count}}{{template "findings" .Findings}}{{end}}
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Column</th><th>Type</th><th>Null</th><th>Key</th><th>Default</th><th>Extra</th><th>Comment</th></tr></thead>
<tbody>{{range .Columns}}<tr id="{{.Anchor}}"{{if .Drift}} class="drift"{{end}}>
<td class="mono">{{.Name}}</td><td class="mono">{{.DataType}}</td><td>{{if .Nullable}}yes{{else}}no{{end}}</td><td>{{.Key}}</td>
<td class="mono">{{if .HasDefault}}{{.Default}}{{end}}</td><td>{{.Extra}}</td>
<td>{{.Comment}}{{if .Drift}} <span class="badge warn">drift</span> {{.Drift}}{{end}}</td></tr>{{end}}</tbody></table></div>
{{if .Indexes}}<h3>Indexes</h3><div class="tablewrap"><table class="rep-table">
<thead><tr><th>Index</th><th>Columns</th><th>Kind</th></tr></thead>
<tbody>{{range .Indexes}}<tr><td class="mono">{{.Name}}</td><td class="mono">{{.Columns}}</td><td>{{if .Primary}}primary key{{else if .Unique}}unique{{else}}plain{{end}}{{if .Implicit}} <span class="muted">(added by the server for a foreign key)</span>{{end}}{{if .Drift}} <span class="badge warn">drift</span> {{.Drift}}{{end}}</td></tr>{{end}}</tbody></table></div>{{end}}
{{if .References}}<h3>References</h3><ul class="plain">{{range .References}}<li>{{template "relation" .}}</li>{{end}}</ul>{{end}}
{{if .ReferencedBy}}<h3>Referenced by</h3><ul class="plain">{{range .ReferencedBy}}<li>{{template "relation" .}}</li>{{end}}</ul>{{end}}
{{if or .ReadBy .WrittenBy .MappedTo}}<h3>Code</h3><ul class="plain">
{{range .ReadBy}}<li>read by {{template "ref" .}} {{template "place" .}}</li>{{end}}
{{range .WrittenBy}}<li>written by {{template "ref" .}} {{template "place" .}}</li>{{end}}
{{range .MappedTo}}<li>row type {{template "ref" .}} {{template "place" .}}</li>{{end}}</ul>{{end}}
</section>{{end}}
<h2>Migrations</h2>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Migration</th><th>Version</th><th>Applied</th><th>Tables introduced</th></tr></thead>
<tbody>{{range .Migrations}}<tr id="{{.Migration.Anchor}}"><td class="mono"><a href="{{.Migration.GraphURL}}">{{.Name}}</a> {{template "place" .Migration}}</td><td class="mono">{{.Version}}</td>
<td>{{if eq .Applied "no"}}<span class="badge warn">not applied</span>{{else if eq .Applied "yes"}}yes{{else}}<span class="muted">unknown (no live database)</span>{{end}}</td>
<td>{{if .Tables}}{{range $i, $t := .Tables}}{{if $i}}, {{end}}<a href="{{$t.PageURL}}">{{$t.Label}}</a>{{end}}{{else}}<span class="muted">none</span>{{end}}</td></tr>{{else}}<tr><td colspan="4" class="muted">No migrations.</td></tr>{{end}}</tbody></table></div>{{end}}

{{define "layers"}}<p class="count">The packages of each architecture layer, and what imports what. The rule is checked by the <code>layering</code> findings.</p>
<h2>The rule</h2>
<ul class="plain">{{range .Principles}}<li>{{.}}</li>{{end}}
<li><strong>{{range $i, $e := .Exempt}}{{if $i}}, {{end}}{{$e}}{{end}}</strong> packages assemble the application and may import anything (the allow-list).</li></ul>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>A package of layer</th><th>must not import (hover for why)</th></tr></thead>
<tbody>{{range .Rule}}<tr><td><strong>{{.From}}</strong></td><td>{{range $i, $f := .Forbidden}}{{if $i}}, {{end}}<span title="{{$f.Reason}}">{{$f.To}}</span>{{end}}</td></tr>{{end}}</tbody></table></div>
<h2>Imports between layers</h2>
<p class="count">Rows import columns. Red cells are imports the rule forbids; a number in one is a violation.</p>
<div class="tablewrap"><table class="rep-table matrix">
<thead><tr><th></th>{{range .Matrix.Layers}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>{{range .Matrix.Rows}}<tr><th>{{.From}}</th>{{range .Cells}}<td class="{{if .Same}}same{{else if .Violation}}forbidden{{end}}{{if and .Violation .Count}} broken{{end}}"{{if .Reason}} title="{{.Reason}}"{{end}}>{{if .Count}}{{.Count}}{{end}}</td>{{end}}</tr>{{end}}</tbody></table></div>
<h2>Violations</h2>
{{if not .FindingsBuilt}}<p class="muted">The findings layer is not built, so the violations are not listed; the red numbers in the matrix above are derived from the same rule.</p>
{{else if not .Violations}}<p class="count">None: no package imports one its layer must not.</p>
{{else}}<ul class="plain">{{range .Violations}}<li><span class="sev sev-{{sev .Finding.Severity}}">{{.Finding.Severity}}</span> {{if .Package}}<a href="{{.Package.GraphURL}}">{{.Package.Label}}</a>{{end}} {{.Finding.Message}}{{if .Path}} {{if .EditorURL}}<a class="loc" href="{{editorHref .EditorURL}}">{{.Path}}{{if .Line}}:{{.Line}}{{end}}</a>{{else}}<span class="loc">{{.Path}}{{if .Line}}:{{.Line}}{{end}}</span>{{end}}{{end}}</li>{{end}}</ul>{{end}}
{{range .Layers}}<section class="lyr"><h2>{{.Name}} <span class="muted">({{len .Packages}} {{plural (len .Packages) "package" "packages"}})</span></h2>
<div class="tablewrap"><table class="rep-table">
<thead><tr><th>Package</th><th>Funcs</th><th>Imports</th><th>Findings</th></tr></thead>
<tbody>{{range .Packages}}<tr id="{{.Package.Anchor}}"><td><a class="mono" href="{{.Package.GraphURL}}">{{.Package.Label}}</a>{{if .Doc}}<br><span class="muted">{{.Doc}}</span>{{end}}</td><td>{{.Funcs}}</td>
<td>{{if .Imports}}<ul class="plain">{{range .Imports}}<li>{{template "pageref" .}}</li>{{end}}</ul>{{else}}<span class="muted">none</span>{{end}}</td>
<td>{{template "findings" .Findings}}</td></tr>{{end}}</tbody></table></div></section>{{end}}{{end}}
`

const reportsCSS = `
.rep{max-width:1200px;margin:0 auto;padding:16px}
.rep h1{margin:.4em 0 .2em;font-size:1.7rem}
.rep h2{margin:1.6em 0 .4em;font-size:1.25rem;scroll-margin-top:68px}
.rep h3{margin:1.1em 0 .3em;font-size:1rem}
.rep-nav{display:flex;gap:6px 14px;flex-wrap:wrap;font-size:.9rem;padding-bottom:8px;border-bottom:1px solid var(--border)}
.rep-nav a.current{color:var(--text);font-weight:650;text-decoration:none}
.asof,.count,.meta{color:var(--muted);margin:.3em 0 .8em}
.asof code,.count code{font-size:.85em}
.notes{border:1px solid rgba(210,153,34,.5);background:rgba(210,153,34,.12);border-radius:8px;padding:8px 14px;margin:10px 0}
.notes ul{margin:.3em 0 0;padding-left:1.2em}
.tablewrap{overflow-x:auto;border:1px solid var(--border);border-radius:8px;margin:8px 0 14px}
.rep-table{width:100%;border-collapse:collapse;font-size:.9rem}
.rep-table th,.rep-table td{border-bottom:1px solid var(--border);padding:6px 10px;text-align:left;vertical-align:top}
.rep-table thead th{background:var(--bg-elev);position:sticky;top:0;white-space:nowrap}
.rep-table tr:last-child td{border-bottom:0}
.rep-table tr:target,.tbl:target h2{outline:2px solid var(--accent);outline-offset:-2px;background:rgba(59,130,246,.12)}
.rep-table tr.dev{opacity:.82}
.rep-table tr.undeclared td:first-child{border-left:3px solid #d29922}
.rep-table tr.drift td{background:rgba(210,153,34,.08)}
.mono,code{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.88em}
.muted{color:var(--muted)}
.plain{list-style:none;margin:0;padding:0}
.plain li{margin:2px 0}
.loc{font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:.82em;color:var(--muted)}
a.loc:hover{color:var(--link)}
h2 a.loc,h3 a.loc{font-family:inherit;font-weight:400;font-size:.8rem;color:var(--link);margin-left:6px}
.method{font-weight:650;font-family:ui-monospace,monospace;font-size:.85em}
.badge{display:inline-block;font-size:.72rem;padding:0 7px;border-radius:999px;border:1px solid var(--border);color:var(--muted);vertical-align:middle}
.badge.warn{color:#d29922;border-color:#d29922}
.badge.secret{color:#a371f7;border-color:#a371f7}
.badge.inferred{color:var(--link);border-color:var(--link)}
.sev{display:inline-block;font-size:.75rem;font-weight:650;padding:0 8px;border-radius:999px;border:1px solid}
.sev-error{color:#e5534b;border-color:#e5534b;background:rgba(229,83,75,.12)}
.sev-warning{color:#d29922;border-color:#d29922;background:rgba(210,153,34,.12)}
.sev-info{color:var(--link);border-color:var(--link);background:rgba(59,130,246,.1)}
.fnd summary{cursor:pointer;list-style:none}
.fnd ul{margin-top:6px;font-size:.85rem}
.rep-index{list-style:none;padding:0}
.rep-index li{margin:12px 0}
.rep-index a{font-weight:650;font-size:1.1rem}
.rep-index span{display:block;color:var(--muted)}
.toc{columns:3 200px;list-style:none;padding:0}
.matrix th,.matrix td{text-align:center}
.matrix tbody th{text-align:left;background:var(--bg-elev)}
.matrix td.same{background:rgba(127,127,127,.12)}
.matrix td.forbidden{background:rgba(229,83,75,.1)}
.matrix td.broken{background:rgba(229,83,75,.45);font-weight:700;color:var(--text)}
@media (max-width:700px){.rep{padding:10px}.rep-table{font-size:.82rem}}
`
