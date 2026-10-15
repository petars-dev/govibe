package devui

import (
	"bytes"
	"context"
	"html/template"
	"net/http"
	"sort"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/codegraph"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
)

// DevInfo is what the /dev index page shows about the running application.
type DevInfo struct {
	AppName          string
	Module           string
	FrameworkVersion string
	Env              string
	// Commands are the application's own CLI commands.
	Commands []DevCommand
	// DB, Graph and MCP say which tools are mounted: a tool that is not is explained, not linked.
	DB, Graph, MCP bool
	// FrameworkDocs says whether the framework's reference docs are served.
	FrameworkDocs bool
	// Schedule reads the scheduler as it is now, for the scheduler card; nil leaves the
	// card out.
	Schedule func() schedview.Status
}

// DevCommand is one command of the application.
type DevCommand struct {
	Name, Short string
}

// DevIndex serves /dev, the front door to the dev tooling: what the app is, which tools are
// there, how big the app is (its own routes, commands, findings: the app's scope, not the
// framework's), and what to do first when it has no routes. The counts are read from the
// project graph after bringing stale layers up to date, as every dev page does.
func DevIndex(info DevInfo, ix *codegraph.Indexer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := devIndexPage{DevInfo: info, Host: r.Host}
		page.Commands = append([]DevCommand(nil), info.Commands...) // the handler serves many requests: sort a copy
		if page.AppName == "" {
			page.AppName = "this app"
		}
		sort.Slice(page.Commands, func(i, j int) bool { return page.Commands[i].Name < page.Commands[j].Name })
		if info.Schedule != nil {
			st := info.Schedule()
			page.Sched = &st
			for i := range st.Tasks {
				if t := &st.Tasks[i]; t.Next != nil && (page.SchedNext == nil || t.Next.Before(*page.SchedNext.Next)) {
					page.SchedNext = t
				}
			}
		}
		if ix != nil {
			ctx, cancel := context.WithTimeout(r.Context(), graphRefreshTimeout)
			defer cancel()
			if _, err := ix.Refresh(ctx); err != nil {
				logrus.WithError(err).Warn("graph index: refresh for the dev index failed; counting from the stored graph")
			}
			if m, err := ix.Model(ctx, codegraph.ScopeApp); err == nil && !m.Empty() {
				page.Counted = true
				page.Routes = len(m.Kind("route"))
				page.Packages = len(m.Kind("package"))
				page.Tables = len(m.Kind("table"))
				st := m.Status(nil)
				page.FindingsBuilt = st.Findings.Available
				page.Errors, page.Warnings, page.Infos = st.Findings.Error, st.Findings.Warning, st.Findings.Info
				page.Hidden = st.Hidden.Nodes
			}
		}
		var buf bytes.Buffer
		if err := devIndexTemplate.Execute(&buf, page); err != nil {
			logrus.WithError(err).Error("dev index: render")
			http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	}
}

type devIndexPage struct {
	DevInfo
	Host string

	Counted                 bool // the graph had something to count
	Routes, Packages        int
	Tables                  int
	FindingsBuilt           bool
	Errors, Warnings, Infos int
	Hidden                  int // framework nodes the app's scope leaves out

	Sched     *schedview.Status // the scheduler as of this request, nil when the page is not told
	SchedNext *schedview.Task   // the scheduled command due first, nil when none has a date
}

// NoRoutes is true when the graph was read and the app registers none: the case the page explains.
func (p devIndexPage) NoRoutes() bool { return p.Counted && p.Routes == 0 }

func (p devIndexPage) Findings() int { return p.Errors + p.Warnings + p.Infos }

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var devIndexTemplate = template.Must(template.New("devindex").Funcs(template.FuncMap{"plural": plural}).Parse(devIndexHTML))

const devIndexHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.AppName}} · GoVibe Dev</title>
<link rel="icon" href="/_govibe/static/govibe-logo.png">
<link rel="stylesheet" href="/_govibe/static/theme.css">
<link rel="stylesheet" href="/_govibe/static/docs.css">
<script src="/_govibe/static/theme.js"></script>
<style>
.dev{max-width:1000px;margin:0 auto;padding:20px 16px 48px}
.dev h1{margin:.2em 0 .1em;font-size:1.8rem}
.dev .meta{display:flex;flex-wrap:wrap;gap:6px 18px;color:var(--muted);font-size:.92rem;margin-bottom:18px}
.dev .meta code{background:var(--code-bg);border:1px solid var(--border);border-radius:4px;padding:0 5px}
.dev .counts{display:flex;flex-wrap:wrap;gap:10px;margin:0 0 22px;padding:0;list-style:none}
.dev .counts li{border:1px solid var(--border);border-radius:8px;padding:8px 14px;background:var(--bg-elev);min-width:110px}
.dev .counts b{display:block;font-size:1.4rem}
.dev .counts span{color:var(--muted);font-size:.82rem}
.dev .cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:12px}
.dev .card{border:1px solid var(--border);border-radius:10px;padding:14px 16px;background:var(--bg-elev)}
.dev .card h2{margin:0 0 4px;font-size:1.05rem}
.dev .card h2 a{text-decoration:none}
.dev .card p{margin:0;color:var(--muted);font-size:.9rem}
.dev .card.off{opacity:.7}
.dev .first{border:1px solid var(--accent);border-radius:10px;padding:12px 16px;margin:0 0 20px}
.dev .first pre{background:var(--code-bg);border:1px solid var(--border);border-radius:6px;padding:8px 10px;overflow:auto}
.dev .sev{font-weight:600}.dev .sev.error{color:#e5534b}.dev .sev.warning{color:#e0793b}.dev .sev.info{color:var(--muted)}
.dev ul.cmds{margin:6px 0 0;padding-left:18px;color:var(--muted);font-size:.9rem}
</style>
</head><body class="docs-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/_govibe/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev</span></div>
<nav class="top-tabs"><a class="tab active" href="/dev">Dev</a><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a>{{if .Graph}}<a class="tab" href="/dev/graph">Graph</a>{{end}}{{if .DB}}<a class="tab" href="/dev/db">DB</a>{{end}}<a class="tab" href="/dev/schedule">Schedule</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>
<main class="dev">
<h1>{{.AppName}}</h1>
<div class="meta">
{{if .Module}}<span>module <code>{{.Module}}</code></span>{{end}}
<span>GoVibe <code>{{if .FrameworkVersion}}{{.FrameworkVersion}}{{else}}unknown version{{end}}</code></span>
<span>environment <code>{{if .Env}}{{.Env}}{{else}}local{{end}}</code></span>
</div>
{{if .NoRoutes}}<div class="first" role="note"><strong>This app registers no routes yet.</strong>
<p>Add one in <code>routes/routes.go</code>, in the function the app declares as <code>Routes</code> in <code>main.go</code>:</p>
<pre>func Register(rt *govibe.Runtime, r chi.Router) error {
	r.Get("/", controllers.Home)
	return nil
}</pre>
<p>The router it receives already carries the standard middleware and the asset mounts; this page, the docs and the graph show the new route as soon as the app restarts.</p></div>{{end}}
{{if .Counted}}<ul class="counts" aria-label="What this app has">
<li><b>{{.Routes}}</b><span>{{plural .Routes "route" "routes"}}</span></li>
<li><b>{{len .Commands}}</b><span>{{plural (len .Commands) "command" "commands"}}</span></li>
<li><b>{{.Packages}}</b><span>{{plural .Packages "package" "packages"}}</span></li>
{{if .Tables}}<li><b>{{.Tables}}</b><span>{{plural .Tables "table" "tables"}}</span></li>{{end}}
<li><b>{{if .FindingsBuilt}}{{.Findings}}{{else}}?{{end}}</b><span>{{if .FindingsBuilt}}{{plural .Findings "finding" "findings"}}{{else}}findings not built{{end}}{{if .Errors}} · <span class="sev error">{{.Errors}} error</span>{{end}}{{if .Warnings}} · <span class="sev warning">{{.Warnings}} warning</span>{{end}}</span></li>
</ul>
<p class="meta">Counted in the app's own scope{{if .Hidden}}: {{.Hidden}} framework {{plural .Hidden "node is" "nodes are"}} left out (switch the scope on the graph page to see them){{end}}.</p>{{end}}
<div class="cards">
<div class="card"><h2><a href="/dev/docs/app">Docs</a></h2><p>Your docs and specs first{{if .FrameworkDocs}}; the framework's reference docs are a separate group, every title prefixed <code>GoVibe:</code>{{end}}.</p></div>
{{if .Graph}}<div class="card"><h2><a href="/dev/graph">Project graph</a></h2><p>Your routes, handlers, services, stores, tables and docs, and what is wrong with them. <a href="/dev/graph/reports">Reports</a>: routes, config, schema, layers.</p></div>
{{else}}<div class="card off"><h2>Project graph</h2><p>Not mounted: the graph store could not be opened (see the log).</p></div>{{end}}
{{if .DB}}<div class="card"><h2><a href="/dev/db">Database</a></h2><p>Browse tables and rows, run read-only queries and see explain plans.</p></div>
{{else}}<div class="card off"><h2>Database</h2><p>No database is configured. Set <code>MARIADB_DSN</code> to enable the explorer.</p></div>{{end}}
{{if .MCP}}<div class="card"><h2><a href="/dev/mcp">MCP server</a></h2><p>For local agents: <code>http://{{.Host}}/dev/mcp</code>. Start with the <code>graph_schema</code> tool.</p></div>{{end}}
{{with .Sched}}<div class="card{{if eq .State "none"}} off{{end}}"><h2><a href="/dev/schedule">Scheduler</a></h2><p>{{if eq .State "none"}}No command is scheduled. Give one a <code>Schedule</code> in <code>routes/console.go</code>.{{else}}{{.Scheduled}} scheduled of {{.Commands}} {{plural .Commands "command" "commands"}}{{if eq .State "running"}}, running{{else if eq .State "disabled"}}, disabled (<code>scheduler.enabled</code> is false){{else}}, not started in this process{{end}}.{{with $.SchedNext}} Next: <code>{{.Name}}</code> {{.NextIn}}.{{end}}{{end}}</p></div>{{end}}
{{if .Commands}}<div class="card"><h2>Commands</h2><p>Run with <code>./{{.AppName}} &lt;name&gt;</code>.</p><ul class="cmds">{{range .Commands}}<li><code>{{.Name}}</code>{{if .Short}}: {{.Short}}{{end}}</li>{{end}}</ul></div>{{end}}
</div>
</main></body></html>`
