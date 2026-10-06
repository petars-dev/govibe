package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"petars-dev/govibe/business/services/codegraph"
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

// DevGraphConsole renders a minimal Gizmo query console and quad editor for
// the embedded Cayley graph store. The page talks to the store purely
// through the mounted Cayley HTTP API at /dev/graph/api/v2/*.
func DevGraphConsole(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(devGraphPage))
}

const devGraphPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GoVibe Dev Graph</title>
<link rel="icon" href="/assets/static/govibe-logo.png">
<link rel="stylesheet" href="/assets/static/theme.css">
<link rel="stylesheet" href="/assets/static/docs.css">
<script src="/assets/static/theme.js"></script>
<script src="/assets/static/vendor/cytoscape.min.js"></script>
<style>
.graph-layout{display:grid;grid-template-columns:1fr 1fr;gap:1.5rem;padding:1.5rem;max-width:1200px;margin:0 auto}
@media (max-width:900px){.graph-layout{grid-template-columns:1fr}}
.graph-panel{border:1px solid var(--border-color,#3333);border-radius:8px;padding:1rem}
.graph-panel h2{margin-top:0;font-size:1rem}
.graph-panel textarea,.graph-panel input{width:100%;box-sizing:border-box;font-family:ui-monospace,monospace;padding:.5rem;margin:.25rem 0;border-radius:6px;border:1px solid var(--border-color,#3333);background:transparent;color:inherit}
.graph-panel textarea{min-height:120px;resize:vertical}
.graph-row{display:flex;gap:.5rem;flex-wrap:wrap}
.graph-row input{flex:1;min-width:120px}
pre.graph-result{white-space:pre-wrap;word-break:break-word;max-height:360px;overflow:auto;background:var(--code-bg,#0002);padding:.75rem;border-radius:6px}
.viz-panel{margin:0 1.5rem 1.5rem;border:1px solid var(--border-color,#3333);border-radius:8px;padding:1rem;max-width:1200px;margin-left:auto;margin-right:auto}
.viz-panel h2{margin-top:0;font-size:1rem}
.viz-toolbar{display:flex;gap:.5rem;align-items:center;margin-bottom:.5rem;flex-wrap:wrap}
.viz-toolbar #graph-search{flex:1;min-width:160px;font-family:ui-monospace,monospace;padding:.5rem;border-radius:6px;border:1px solid var(--border-color,#3333);background:transparent;color:inherit}
.viz-toolbar select{padding:.5rem;border-radius:6px;border:1px solid var(--border-color,#3333);background:var(--bg-elev,transparent);color:inherit;font:inherit}
.viz-check{display:flex;gap:.35rem;align-items:center;font-size:.85rem;white-space:nowrap;cursor:pointer}
.viz-size-controls{display:flex;gap:.25rem}
.viz-size-controls .btn{padding:.4rem .65rem;line-height:1}
#graph-stats{opacity:.7;font-size:.85rem;white-space:nowrap}
#cy{width:100%;height:420px;min-height:200px;max-height:80vh;resize:vertical;overflow:hidden;border:1px solid var(--border-color,#3333);border-radius:6px;background:var(--code-bg,#0001)}
#node-detail{margin-top:.5rem;min-height:2.5rem}
</style>
</head><body class="docs-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/assets/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev Graph</span></div>
<nav class="top-tabs"><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a><a class="tab active" href="/dev/graph">Graph</a><a class="tab" href="/dev/db">DB</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>
<p style="padding:0 1.5rem;opacity:.7;font-size:.9rem">Embedded Cayley graph store (bolt file under <code>docs/graph</code>). Dev-only &mdash; disabled in production.</p>
<div class="viz-panel">
  <h2>Visualizer</h2>
  <div class="viz-toolbar">
    <input id="graph-search" placeholder="Search nodes..." oninput="applySearch()">
    <select id="graph-layer" onchange="pickLayer(this.value)" title="Show one layer of the graph at a time"></select>
    <label class="viz-check" title="Draw literal values (names, paths, line numbers) as nodes. Slow on big graphs."><input type="checkbox" id="graph-attrs" onchange="renderGraph()"> Attributes as nodes</label>
    <div class="viz-size-controls">
      <button class="btn" onclick="resizeCanvas(-80)" title="Shrink canvas">&minus;</button>
      <button class="btn" onclick="resizeCanvas(80)" title="Grow canvas">+</button>
      <button class="btn" onclick="fitGraph()" title="Fit graph to view">&#10530; Fit</button>
    </div>
    <button class="btn" onclick="refreshGraph()">Refresh</button>
    <span id="graph-stats"></span>
  </div>
  <div id="cy"></div>
  <pre id="node-detail" class="graph-result">Click a node to see its triples.</pre>
</div>
<div class="graph-layout">
  <div class="graph-panel">
    <h2>Add a quad</h2>
    <div class="graph-row">
      <input id="q-sub" placeholder="subject" value="alice">
      <input id="q-pred" placeholder="predicate" value="knows">
      <input id="q-obj" placeholder="object" value="bob">
      <input id="q-label" placeholder="label (optional)">
    </div>
    <button class="btn" onclick="addQuad()">Add quad</button>
    <pre id="add-result" class="graph-result"></pre>
    <h2>Browse quads</h2>
    <button class="btn" onclick="browseQuads()">Browse all quads</button>
    <pre id="browse-result" class="graph-result"></pre>
  </div>
  <div class="graph-panel">
    <h2>Gizmo query console</h2>
    <textarea id="query-input">g.V().All()</textarea>
    <button class="btn" onclick="runQuery()">Run query</button>
    <pre id="query-result" class="graph-result"></pre>
  </div>
</div>
<script>
function nq(v){ return '"' + String(v).replace(/\\/g,'\\\\').replace(/"/g,'\\"') + '"'; }
async function addQuad(){
  var sub=document.getElementById('q-sub').value, pred=document.getElementById('q-pred').value,
      obj=document.getElementById('q-obj').value, label=document.getElementById('q-label').value;
  var out=document.getElementById('add-result');
  if(!sub||!pred||!obj){ out.textContent='subject, predicate and object are required'; return; }
  var line = nq(sub)+' '+nq(pred)+' '+nq(obj)+(label?' '+nq(label):'')+' .\n';
  try{
    var res = await fetch('/dev/graph/api/v2/write', {method:'POST', headers:{'Content-Type':'application/n-quads'}, body: line});
    out.textContent = await res.text();
    if(res.ok){ refreshGraph(); }
  }catch(e){ out.textContent = String(e); }
}
async function browseQuads(){
  var out=document.getElementById('browse-result');
  try{
    var res = await fetch('/dev/graph/api/v2/read?format=json');
    out.textContent = await res.text();
  }catch(e){ out.textContent = String(e); }
}
async function runQuery(){
  var q=document.getElementById('query-input').value;
  var out=document.getElementById('query-result');
  try{
    var res = await fetch('/dev/graph/api/v2/query?lang=gizmo', {method:'POST', body: q});
    var text = await res.text();
    try{ out.textContent = JSON.stringify(JSON.parse(text), null, 2); }
    catch(_e){ out.textContent = text; }
  }catch(e){ out.textContent = String(e); }
}

var cy = null;
var lastQuads = [];
var searchText = Object.create(null);
var layerChoice = null; // null until first load; '*' means every layer

// Above this many quads, the first load shows a single layer, not all of them.
var AUTO_LAYER_LIMIT = 1500;
// Above this many edges, edges are drawn as cheap arrowless lines.
var STRAIGHT_EDGE_LIMIT = 1000;
// Labels are drawn only for hovered, tapped and searched nodes, and only when
// there are at most this many: on a dense graph the labels of more than a
// few dozen nodes overlap into a wall of text.
var FOCUS_LIMIT = 40;

// Quad values arrive as Cayley prints them: <iri>, _:bnode, "6"^^<type>,
// "x"@lang, or a bare string for a plain literal.
function isRef(v){ return /^<.*>$/.test(v) || v.indexOf('_:') === 0; }
// An attribute quad hangs a literal off an entity (file --name--> "hello.go").
// That is data about a node, not structure, so it stays out of the canvas and
// shows in the detail panel. A quad whose subject is not an entity
// (alice --knows--> bob) is structure, whatever its object looks like.
function isAttr(q){ return isRef(q.subject) && !isRef(q.object); }
function litValue(v){
  var m = /^"([\s\S]*)"(?:\^\^<.*>|@.+)$/.exec(v);
  return m ? m[1] : v;
}
function layerOf(q){ return q.label || ''; }
function layerName(l){ return l ? l.replace(/^<(?:layer:)?/, '').replace(/>$/, '') : '(no layer)'; }

// <func:business/services/codegraph.Indexer.Run> -> codegraph.Indexer.Run. The
// full id is still the node's id, and the first line of the detail panel.
function shortLabel(id){
  var s = id.replace(/^<|>$/g, ''), i = s.lastIndexOf('/');
  return i >= 0 && i < s.length - 1 ? s.slice(i + 1) : s;
}

function buildElements(quads, withAttrs){
  var seen = Object.create(null), search = Object.create(null);
  var nodes = [], edges = [], hidden = 0;
  function node(id){ if(!seen[id]){ seen[id] = true; nodes.push({data:{id:id, label:shortLabel(id)}}); } }
  quads.forEach(function(q, i){
    node(q.subject);
    if(!withAttrs && isAttr(q)){
      hidden++;
      search[q.subject] = (search[q.subject] || '') + '\n' + litValue(q.object).toLowerCase();
      return;
    }
    node(q.object);
    edges.push({data:{id:'e'+i, source:q.subject, target:q.object, label:q.predicate}});
  });
  return {nodes:nodes, edges:edges, hidden:hidden, search:search};
}

// Concentric rings, hubs in the middle. Left to rank by degree, nearly every
// node ties at 1-3 and lands on one giant ring, so deal nodes into rings of
// 1, 6, 12, 18... instead: a compact disc, most-connected first. Within a ring
// nodes go by id, which keeps neighbours from the same package side by side.
// The fractional part orders a ring's nodes; levelWidth 1 keeps rings apart.
function ringValues(nodes){
  var byDegree = nodes.toArray().sort(function(a, b){ return b.degree() - a.degree(); });
  var rings = [], cap = 1, i = 0;
  while(i < byDegree.length){ rings.push(byDegree.slice(i, i + cap)); i += cap; cap = 6 * rings.length; }
  var val = Object.create(null);
  rings.forEach(function(ring, k){
    ring.sort(function(a, b){ return a.id() < b.id() ? -1 : 1; });
    ring.forEach(function(n, j){ val[n.id()] = (rings.length - k) + 0.9 * (1 - j / ring.length); });
  });
  return val;
}

function populateLayers(quads){
  var sel = document.getElementById('graph-layer');
  var counts = Object.create(null), names = [];
  quads.forEach(function(q){
    var l = layerOf(q);
    if(!(l in counts)){ counts[l] = 0; names.push(l); }
    counts[l]++;
  });
  names.sort();
  if(layerChoice === null){
    layerChoice = (names.length > 1 && quads.length > AUTO_LAYER_LIMIT) ? names[0] : '*';
  }
  if(layerChoice !== '*' && !(layerChoice in counts)){ layerChoice = '*'; }
  sel.innerHTML = '';
  sel.add(new Option('All layers (' + quads.length + ')', '*'));
  names.forEach(function(l){ sel.add(new Option(layerName(l) + ' (' + counts[l] + ')', l)); });
  sel.value = layerChoice;
}

function pickLayer(v){ layerChoice = v; renderGraph(); }

function describeNode(id){
  var attrs = [], rels = [];
  lastQuads.forEach(function(q){
    if(q.subject === id && isAttr(q)){ attrs.push(q.predicate + ': ' + litValue(q.object)); }
    else if(q.subject === id || q.object === id){ rels.push(q.subject + ' → ' + q.predicate + ' → ' + q.object); }
  });
  return attrs.concat(attrs.length && rels.length ? [''] : [], rels).join('\n') || '(no relations)';
}

function renderGraph(){
  var stats = document.getElementById('graph-stats');
  try{
    var withAttrs = document.getElementById('graph-attrs').checked;
    var quads = layerChoice === '*' ? lastQuads : lastQuads.filter(function(q){ return layerOf(q) === layerChoice; });
    var g = buildElements(quads, withAttrs);
    searchText = g.search;
    var many = g.edges.length > STRAIGHT_EDGE_LIMIT;
    stats.textContent = g.nodes.length + ' node' + (g.nodes.length===1?'':'s') + ', ' + g.edges.length + ' edge' + (g.edges.length===1?'':'s') +
      (g.hidden ? ' (+' + g.hidden + ' attribute' + (g.hidden===1?'':'s') + ' in detail)' : '') +
      ' · ' + quads.length + ' of ' + lastQuads.length + ' quads';
    if(cy){ cy.destroy(); cy = null; }
    var fg = getComputedStyle(document.getElementById('cy')).color;
    var ring = null;
    // Labels, arrows and curves are what make Cytoscape slow, so the base style
    // has none of them: nodes are fixed-size dots, and labels (node and edge)
    // only appear around the node you hover or tap, and on search matches.
    cy = cytoscape({
      container: document.getElementById('cy'),
      elements: g.nodes.concat(g.edges),
      textureOnViewport: true,
      hideEdgesOnViewport: many,
      style: [
        { selector: 'node', style: { 'background-color':'#5b8def', 'color':fg, 'font-size':10, 'text-valign':'bottom', 'text-margin-y':3, 'width':12, 'height':12 } },
        { selector: 'node.named, node.focus, node.hover', style: { 'label':'data(label)' } },
        { selector: 'edge', style: { 'curve-style': many ? 'haystack' : 'straight', 'width':1, 'line-color':'#8a8a8a', 'target-arrow-color':'#8a8a8a', 'target-arrow-shape': many ? 'none' : 'triangle', 'arrow-scale':0.8 } },
        { selector: 'edge.focus', style: { 'label':'data(label)', 'font-size':9, 'color':fg, 'line-color':'#e0793b', 'target-arrow-color':'#e0793b', 'width':2 } },
        { selector: 'node.focus, node.hover', style: { 'z-index':10 } },
        { selector: 'node.focus', style: { 'background-color':'#e0793b' } },
        { selector: '.dim', style: { 'opacity':0.15 } },
        { selector: '.highlight', style: { 'background-color':'#e0793b', 'line-color':'#e0793b', 'target-arrow-color':'#e0793b' } }
      ],
      // concentric is a single pass (cose took ~9s on 500 nodes).
      layout: { name:'concentric', animate:false, padding:20, minNodeSpacing:14, concentric:function(n){ if(!ring) ring = ringValues(n.cy().nodes()); return ring[n.id()]; }, levelWidth:function(){ return 1; } }
    });
    cy.on('tap', 'node', function(evt){
      var n = evt.target;
      cy.elements().removeClass('focus');
      // A hub with hundreds of neighbours would label all of them at once; the
      // detail panel lists them instead.
      var nb = n.closedNeighborhood();
      (nb.nodes().length <= FOCUS_LIMIT ? nb : n).addClass('focus');
      document.getElementById('node-detail').textContent = describeNode(n.data('id'));
    });
    cy.on('tap', function(evt){ if(evt.target === cy){ cy.elements().removeClass('focus'); } });
    cy.on('mouseover', 'node', function(evt){ evt.target.addClass('hover'); });
    cy.on('mouseout', 'node', function(evt){ evt.target.removeClass('hover'); });
    applySearch();
  }catch(e){ stats.textContent = String(e); }
}

async function refreshGraph(){
  var stats = document.getElementById('graph-stats');
  try{
    var res = await fetch('/dev/graph/api/v2/read?format=json');
    var text = await res.text();
    var quads = [];
    try{ var parsed = JSON.parse(text); if(Array.isArray(parsed)) quads = parsed; }catch(_e){}
    lastQuads = quads;
    populateLayers(quads);
    renderGraph();
  }catch(e){ stats.textContent = String(e); }
}

function applySearch(){
  if(!cy) return;
  var term = (document.getElementById('graph-search').value||'').toLowerCase();
  cy.nodes().removeClass('highlight dim named');
  if(!term) return;
  // Match a node's id, or any attribute value kept off the canvas.
  var matched = cy.nodes().filter(function(n){
    var id = n.data('id');
    return String(id).toLowerCase().indexOf(term) !== -1 || (searchText[id] || '').indexOf(term) !== -1;
  });
  matched.addClass('highlight');
  if(matched.length <= FOCUS_LIMIT){ matched.addClass('named'); }
  cy.nodes().not(matched).addClass('dim');
}

if(window.ResizeObserver){
  new ResizeObserver(function(){ if(cy){ cy.resize(); } }).observe(document.getElementById('cy'));
}

function resizeCanvas(delta){
  var el = document.getElementById('cy');
  var min = 200, max = Math.round(window.innerHeight*0.8);
  var h = Math.max(min, Math.min(max, el.getBoundingClientRect().height + delta));
  el.style.height = h + 'px';
}

function fitGraph(){
  if(cy){ cy.fit(undefined, 30); }
}

refreshGraph();
</script>
</body></html>`
