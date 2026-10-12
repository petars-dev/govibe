package devui

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/codegraph"
)

// graphRefreshTimeout bounds how long a page load waits for stale layers to be
// rebuilt; a layer takes tens of milliseconds, so this only matters if
// something is badly wrong.
const graphRefreshTimeout = 10 * time.Second

// DevGraphPage serves the graph console after rebuilding any layer whose inputs
// changed since it was last indexed. That is what makes an edit to a doc or a
// spec, which the dev server's file watcher ignores, show up on the next page
// load without a restart. A failed refresh is logged and the page is served
// from what the store holds.
func DevGraphPage(ix *codegraph.Indexer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), graphRefreshTimeout)
		defer cancel()
		if _, err := ix.Refresh(ctx); err != nil {
			logrus.WithError(err).Warn("graph index: refresh on page load failed; serving the stored graph")
		}
		DevGraphConsole(w, r)
	}
}

// reindexResponse is the JSON body of POST /dev/graph/reindex: the run report,
// plus the reason when the run could not complete at all.
type reindexResponse struct {
	*codegraph.Report
	Error string `json:"error,omitempty"`
}

// DevGraphReindex rebuilds every layer of the project graph and returns the run
// report (per-layer status, quad counts, added and removed vs the previous
// run). It answers 500, with the same body, when a layer failed or the run
// could not complete, so scripts and agents can rely on the status code.
func DevGraphReindex(ix *codegraph.Indexer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, err := ix.Run(r.Context())
		resp := reindexResponse{Report: rep}
		status := http.StatusOK
		if err != nil {
			resp.Error = err.Error()
			status = http.StatusInternalServerError
		} else if rep.Failed() {
			status = http.StatusInternalServerError
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// DevGraphConsole serves the graph explorer. The page is a shell: its script
// (assets/static/graph.js) asks /dev/graph/data/* for bounded views, a node's
// inspector, search and findings, and keeps the quad editor and Gizmo console
// of the Cayley API (/dev/graph/api/v2/*) in a drawer at the bottom.
func DevGraphConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(devGraphPage))
}

const devGraphPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GoVibe Dev Graph</title>
<link rel="icon" href="/_govibe/static/govibe-logo.png">
<link rel="stylesheet" href="/_govibe/static/theme.css">
<link rel="stylesheet" href="/_govibe/static/docs.css">
<link rel="stylesheet" href="/_govibe/static/graph.css">
<script src="/_govibe/static/theme.js"></script>
<script src="/_govibe/static/cytoscape.min.js"></script>
</head><body class="docs-body gv-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/_govibe/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev Graph</span></div>
<nav class="top-tabs"><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a><a class="tab active" href="/dev/graph">Graph</a><a class="tab" href="/dev/db">DB</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>

<section class="gv-banner" id="gv-banner" aria-live="polite"><span class="gv-muted">Loading the graph&hellip;</span></section>

<section class="gv-toolbar">
  <div class="gv-presets" id="gv-presets" role="tablist" aria-label="Views"></div>
  <div class="gv-search">
    <input id="gv-search" type="search" placeholder="Search nodes: a file, symbol, route, table&hellip;   ( / )" autocomplete="off" aria-label="Search the graph">
    <ul id="gv-search-results" class="gv-results" hidden></ul>
  </div>
</section>

<section class="gv-kinds" id="gv-kinds" aria-label="Kinds of node in this view"></section>

<main class="gv-main">
  <div class="gv-stage">
    <div class="gv-viewbar"><span id="gv-title" class="gv-title"></span><span id="gv-stats" class="gv-muted"></span>
      <span class="gv-spacer"></span>
      <button class="gv-btn" id="gv-back" title="Back to the preset view" hidden>&larr; Back to view</button>
      <button class="gv-btn" id="gv-fit" title="Fit the view to the canvas">Fit</button>
    </div>
    <div class="gv-desc gv-muted" id="gv-desc"></div>
    <div class="gv-notes" id="gv-notes"></div>
    <div id="cy" role="img" aria-label="Graph view"></div>
    <div class="gv-hint gv-muted">Click a node to inspect it. Double-click to expand its neighbours. Scroll to zoom, drag to pan.</div>
  </div>
  <aside class="gv-side" aria-label="Details">
    <div class="gv-tabs" role="tablist">
      <button class="gv-tab active" data-tab="inspector" role="tab" aria-selected="true">Inspector</button>
      <button class="gv-tab" data-tab="findings" role="tab" aria-selected="false">Findings <span class="gv-count" id="gv-findings-count"></span></button>
    </div>
    <div id="gv-inspector" class="gv-pane"><p class="gv-muted">Pick a node to see what the graph knows about it.</p></div>
    <div id="gv-findings" class="gv-pane" hidden></div>
  </aside>
</main>

<details class="gv-raw" id="gv-raw">
  <summary>Quad editor and Gizmo console</summary>
  <div class="gv-raw-grid">
    <div class="gv-panel">
      <h2>Add a quad</h2>
      <p class="gv-muted">Hand-added quads are plain strings with no layer label. A reindex never touches them; the &ldquo;Hand-added quads&rdquo; view draws them.</p>
      <div class="gv-row">
        <input id="q-sub" placeholder="subject" value="alice" aria-label="subject">
        <input id="q-pred" placeholder="predicate" value="knows" aria-label="predicate">
        <input id="q-obj" placeholder="object" value="bob" aria-label="object">
        <input id="q-label" placeholder="label (optional)" aria-label="label">
      </div>
      <button class="gv-btn" onclick="addQuad()">Add quad</button>
      <pre id="add-result" class="gv-result"></pre>
      <h2>Browse quads</h2>
      <button class="gv-btn" onclick="browseQuads()">Browse all quads</button>
      <pre id="browse-result" class="gv-result"></pre>
    </div>
    <div class="gv-panel">
      <h2>Gizmo query console</h2>
      <textarea id="query-input" aria-label="Gizmo query">g.V().All()</textarea>
      <button class="gv-btn" onclick="runQuery()">Run query</button>
      <pre id="query-result" class="gv-result"></pre>
    </div>
  </div>
</details>
<script src="/_govibe/static/graph.js"></script>
</body></html>`
