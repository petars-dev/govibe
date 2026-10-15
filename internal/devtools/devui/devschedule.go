package devui

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/devtools/schedview"
	"github.com/petars-dev/govibe/internal/scheduler"
)

// DevSchedule serves /dev/schedule: the scheduled commands with their next run, and
// the last runs of each, read from the scheduler as it is now. status builds the
// document (schedview.Build) for the moment of the request.
func DevSchedule(status func() schedview.Status) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := schedulePage{Status: status()}
		// A message left by the "Run now" redirect: only for a task that exists, so the
		// query string cannot put its own words on the page.
		for _, t := range page.Tasks {
			if t.Running > 0 {
				page.Refresh = true
			}
			switch {
			case r.URL.Query().Get("ran") == t.Name:
				page.Notice = "Started " + t.Name + ". It runs in the background: this page refreshes while it does, and the run appears in its history."
			case r.URL.Query().Get("refused") == t.Name && t.Running > 0:
				// Only while it is true: the address cannot make the page claim a task is running.
				page.Notice = t.Name + " is still running (overlap: skip), so it was not started again."
			}
		}
		var buf bytes.Buffer
		if err := scheduleTemplate.Execute(&buf, page); err != nil {
			logrus.WithError(err).Error("dev schedule: render")
			http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	}
}

// DevScheduleData serves /dev/schedule/data: the same document as the page and the
// schedule_status MCP tool, as JSON.
func DevScheduleData(status func() schedview.Status) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(status())
	}
}

// DevScheduleRun handles POST /dev/schedule/run/{name}: "Run now". It starts the task
// through the scheduler's own code path (overlap policy, timeout, lock, recovery, history),
// which runs the same function `./app <name>` runs. A browser form is answered with a
// redirect back to the page; a client that asks for JSON gets the status code: 202 started,
// 409 refused (still running, or the scheduler is stopped), 404 no such task.
//
// The endpoint runs the app's code, so a page on the web must not be able to make a
// developer's machine do that. A request from another site is refused (the browser's
// Sec-Fetch-Site or Origin header says so), and so is one addressed to a host that is not
// the machine itself: the dev server listens on every interface, and a page that rebinds
// its own name to 127.0.0.1 is "same-origin" with it. A request with no such header at all
// (curl, a script) is let through when the host is local: it is the developer's own.
func DevScheduleRun(src schedview.Source) http.HandlerFunc {
	protect := http.NewCrossOriginProtection()
	return func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "Run now is only available when the panel is opened through localhost, 127.0.0.1 or [::1], not through "+r.Host+": run the command by hand instead", http.StatusForbidden)
			return
		}
		if err := protect.Check(r); err != nil {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		// The page escapes the name as one path segment (runPath). chi gives the handler the
		// raw segment when the request carries a RawPath (a name with a slash in it, say) and
		// the decoded one otherwise, so it is unescaped only in the first case: a name that
		// itself contains "%3A" must not be read twice.
		name := chi.URLParam(r, "name")
		if r.URL.RawPath != "" {
			if unescaped, err := url.PathUnescape(name); err == nil {
				name = unescaped
			}
		}
		var err error
		asJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
		reply := func(code int, msg string, redirectQuery string) {
			switch {
			case asJSON:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				out := map[string]string{"task": name}
				if code < 300 {
					out["status"] = msg
				} else {
					out["error"] = msg
				}
				_ = json.NewEncoder(w).Encode(out)
			case redirectQuery != "":
				http.Redirect(w, r, "/dev/schedule?"+redirectQuery+"#t-"+url.PathEscape(name), http.StatusSeeOther)
			default:
				http.Error(w, msg, code)
			}
		}
		if src == nil {
			reply(http.StatusNotFound, "no command declares a Schedule", "")
			return
		}
		_, err = src.RunNow(name)
		switch {
		case err == nil:
			reply(http.StatusAccepted, "started", "ran="+url.QueryEscape(name))
		case errors.Is(err, scheduler.ErrUnknownTask):
			reply(http.StatusNotFound, "no scheduled command named "+name, "")
		case errors.Is(err, scheduler.ErrRunning):
			reply(http.StatusConflict, err.Error(), "refused="+url.QueryEscape(name))
		case errors.Is(err, scheduler.ErrStopped):
			reply(http.StatusConflict, err.Error(), "")
		default:
			logrus.WithError(err).Error("dev schedule: run now")
			reply(http.StatusInternalServerError, err.Error(), "")
		}
	}
}

type schedulePage struct {
	schedview.Status
	Notice  string
	Refresh bool // a task is running: reload the page to show how it ends
}

// loopbackHost reports whether a request's Host names this machine: localhost (and
// *.localhost), 127.0.0.0/8 or ::1, with or without a port.
func loopbackHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// runPath is the address "Run now" posts to for a task: its name escaped as one path
// segment, so a name with a space, a slash, a question mark or an accent still reaches it.
func runPath(name string) template.URL {
	return template.URL("/dev/schedule/run/" + url.PathEscape(name))
}

var scheduleTemplate = template.Must(template.New("devschedule").Funcs(template.FuncMap{"runPath": runPath}).Parse(devScheduleHTML))

const devScheduleHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{if .Refresh}}<meta http-equiv="refresh" content="3">{{end}}
<title>Schedule · GoVibe Dev</title>
<link rel="icon" href="/_govibe/static/govibe-logo.png">
<link rel="stylesheet" href="/_govibe/static/theme.css">
<link rel="stylesheet" href="/_govibe/static/docs.css">
<script src="/_govibe/static/theme.js"></script>
<style>
.sch{max-width:1200px;margin:0 auto;padding:16px 16px 48px}
.sch h1{margin:.4em 0 .2em;font-size:1.7rem}
.sch h2{margin:1.6em 0 .4em;font-size:1.15rem}
.sch .meta{display:flex;flex-wrap:wrap;gap:6px 18px;color:var(--muted);font-size:.92rem;margin:.2em 0 14px}
.sch .meta code,.sch td code{background:var(--code-bg);border:1px solid var(--border);border-radius:4px;padding:0 5px}
.sch .state{display:inline-block;font-weight:650;border-radius:999px;padding:2px 12px;border:1px solid var(--border)}
.sch .state.running{background:rgba(46,160,67,.15);border-color:rgba(46,160,67,.5);color:#2ea043}
.sch .state.stopped,.sch .state.disabled{background:rgba(210,153,34,.14);border-color:rgba(210,153,34,.5);color:#d29922}
.sch .state.none{color:var(--muted)}
.sch .note{border:1px solid rgba(210,153,34,.5);background:rgba(210,153,34,.12);border-radius:8px;padding:8px 14px;margin:10px 0}
.sch .notice{border:1px solid var(--accent);border-radius:8px;padding:8px 14px;margin:10px 0}
.sch .first{border:1px solid var(--accent);border-radius:10px;padding:12px 16px;margin:16px 0}
.sch .first pre{background:var(--code-bg);border:1px solid var(--border);border-radius:6px;padding:8px 10px;overflow:auto}
.sch .tablewrap{overflow-x:auto;border:1px solid var(--border);border-radius:8px;margin:8px 0 14px}
.sch table{width:100%;border-collapse:collapse;font-size:.9rem}
.sch th,.sch td{border-bottom:1px solid var(--border);padding:6px 10px;text-align:left;vertical-align:top}
.sch th{color:var(--muted);font-weight:600;white-space:nowrap}
.sch tr:last-child td{border-bottom:0}
.sch .mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.88em}
.sch .muted{color:var(--muted)}
.sch .badge{display:inline-block;font-size:.78rem;font-weight:650;border-radius:5px;padding:0 7px;border:1px solid var(--border);color:var(--muted)}
.sch .badge.ok{color:#2ea043;border-color:rgba(46,160,67,.5);background:rgba(46,160,67,.12)}
.sch .badge.error,.sch .badge.panic{color:#e5534b;border-color:rgba(229,83,75,.5);background:rgba(229,83,75,.12)}
.sch .badge.timeout,.sch .badge.overrun{color:#e0793b;border-color:rgba(224,121,59,.5);background:rgba(224,121,59,.12)}
.sch .badge.skipped,.sch .badge.missed{color:var(--muted)}
.sch form{margin:0}
.sch button.run{cursor:pointer;font:inherit;font-size:.85rem;padding:3px 12px;border-radius:6px;border:1px solid var(--accent);background:transparent;color:var(--link)}
.sch button.run:hover{background:var(--accent);color:#fff}
.sch .runs-title{display:flex;align-items:baseline;gap:10px;flex-wrap:wrap}
</style>
</head><body class="docs-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/_govibe/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev</span></div>
<nav class="top-tabs"><a class="tab" href="/dev">Dev</a><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a><a class="tab" href="/dev/graph">Graph</a><a class="tab" href="/dev/db">DB</a><a class="tab active" href="/dev/schedule">Schedule</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>
<main class="sch">
<h1>Schedule</h1>
<p><span class="state {{.State}}">{{if eq .State "running"}}Scheduler running{{else if eq .State "stopped"}}Scheduler not started here{{else if eq .State "disabled"}}Scheduler disabled{{else}}Nothing scheduled{{end}}</span></p>
{{if .Reason}}<div class="note" role="note">{{.Reason}}.</div>{{end}}
{{if .Notice}}<div class="notice" role="status">{{.Notice}}</div>{{end}}
<div class="meta">
<span>timezone <code>{{.Timezone}}</code></span>
<span>lock <code>{{.Lock}}</code>{{if eq .Lock "none"}} (every instance of the app runs every due task){{else}} (one instance runs a task at a time){{end}}</span>
<span>{{.Scheduled}} scheduled of {{.Commands}} {{if eq .Commands 1}}command{{else}}commands{{end}}</span>
</div>
{{if not .Tasks}}<div class="first" role="note"><strong>No command declares a Schedule.</strong>
<p>Put a command on a schedule with one more field, in the function the app declares as <code>Commands</code> (<code>routes/console.go</code>):</p>
<pre>{{.HowToDeclare}}</pre>
<p>The same <code>Run</code> serves <code>./app items:sync</code> and the scheduled run. <code>./app schedule list</code> shows what is scheduled, with no server and no database.</p></div>
{{else}}
<div class="tablewrap"><table>
<thead><tr><th>Command</th><th>Schedule</th><th>Next run</th><th>Overlap</th><th>Timeout</th><th>Running</th><th></th></tr></thead>
<tbody>{{range .Tasks}}<tr id="t-{{.Name}}">
<td><code>{{.Name}}</code>{{if .Description}}<br><span class="muted">{{.Description}}</span>{{end}}</td>
<td><span class="mono">{{.Schedule}}</span><br><span class="muted">{{.Timezone}}</span></td>
<td>{{if .Next}}<span class="mono">{{.Next.Format "2006-01-02 15:04:05"}}</span><br><span class="muted">{{.NextIn}}</span>{{else}}<span class="muted">{{.NextNote}}</span>{{end}}</td>
<td>{{.Overlap}}</td>
<td>{{if .Timeout}}{{.Timeout}}{{else}}<span class="muted">none</span>{{end}}</td>
<td>{{.Running}}</td>
<td><form method="post" action="{{runPath .Name}}"><button class="run" type="submit" title="Run {{.Name}} now, the way the scheduler does">Run now</button></form></td>
</tr>{{end}}</tbody></table></div>
{{range .Tasks}}<h2 class="runs-title">Recent runs of <code>{{.Name}}</code> <span class="muted">(last 20, newest first, times in {{.Timezone}})</span></h2>
{{if .Runs}}<div class="tablewrap"><table>
<thead><tr><th>Status</th><th>Trigger</th><th>Due</th><th>Started</th><th>Took</th><th>Detail</th></tr></thead>
<tbody>{{range .Runs}}<tr>
<td><span class="badge {{.Status}}">{{.Status}}</span>{{if gt .Count 1}} <span class="muted">&times;{{.Count}}</span>{{end}}</td>
<td>{{.Trigger}}</td>
<td class="mono">{{.Due.Format "2006-01-02 15:04:05"}}</td>
<td class="mono">{{if .Started.IsZero}}<span class="muted">never</span>{{else}}{{.Started.Format "2006-01-02 15:04:05"}}{{end}}</td>
<td class="mono">{{if .Duration}}{{.Duration}}{{else}}<span class="muted">-</span>{{end}}</td>
<td>{{.Detail}}</td>
</tr>{{end}}</tbody></table></div>
{{else}}<p class="muted">No run yet. History is kept in memory for the life of this process (the structured log lines are the record in production).</p>{{end}}{{end}}
{{end}}
</main></body></html>`
