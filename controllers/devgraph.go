package controllers

import (
	"net/http"
)

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
.viz-toolbar input{flex:1;min-width:160px;font-family:ui-monospace,monospace;padding:.5rem;border-radius:6px;border:1px solid var(--border-color,#3333);background:transparent;color:inherit}
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

function buildElements(quads){
  var seen = {};
  var els = [];
  quads.forEach(function(q){
    if(!seen[q.subject]){ seen[q.subject]=true; els.push({data:{id:q.subject, label:q.subject}}); }
    if(!seen[q.object]){ seen[q.object]=true; els.push({data:{id:q.object, label:q.object}}); }
  });
  quads.forEach(function(q, i){
    els.push({data:{id:'e'+i, source:q.subject, target:q.object, label:q.predicate}});
  });
  return els;
}

async function refreshGraph(){
  var stats = document.getElementById('graph-stats');
  try{
    var res = await fetch('/dev/graph/api/v2/read?format=json');
    var text = await res.text();
    var quads = [];
    try{ var parsed = JSON.parse(text); if(Array.isArray(parsed)) quads = parsed; }catch(_e){}
    lastQuads = quads;
    var els = buildElements(quads);
    var nodeCount = els.filter(function(e){ return !e.data.source; }).length;
    stats.textContent = nodeCount + ' node' + (nodeCount===1?'':'s') + ', ' + quads.length + ' quad' + (quads.length===1?'':'s');
    if(cy){ cy.destroy(); }
    cy = cytoscape({
      container: document.getElementById('cy'),
      elements: els,
      style: [
        { selector: 'node', style: { 'label':'data(label)', 'background-color':'#5b8def', 'color':'#e6e6e6', 'text-outline-color':'#000', 'text-outline-width':1, 'font-size':10, 'text-valign':'center', 'text-halign':'center', 'width':'label', 'height':'label', 'padding':'8px', 'shape':'round-rectangle' } },
        { selector: 'edge', style: { 'label':'data(label)', 'font-size':9, 'curve-style':'bezier', 'target-arrow-shape':'triangle', 'width':1.5, 'line-color':'#8a8a8a', 'target-arrow-color':'#8a8a8a', 'text-rotation':'autorotate', 'color':'#999' } },
        { selector: '.dim', style: { 'opacity':0.15 } },
        { selector: '.highlight', style: { 'background-color':'#e0793b', 'line-color':'#e0793b', 'target-arrow-color':'#e0793b' } }
      ],
      layout: { name:'cose', animate:false, padding:20 }
    });
    cy.on('tap', 'node', function(evt){
      var id = evt.target.data('id');
      var rel = lastQuads.filter(function(q){ return q.subject===id || q.object===id; });
      document.getElementById('node-detail').textContent = rel.map(function(q){
        return q.subject+' → '+q.predicate+' → '+q.object;
      }).join('\n') || '(no relations)';
    });
    applySearch();
  }catch(e){ stats.textContent = String(e); }
}

function applySearch(){
  if(!cy) return;
  var term = (document.getElementById('graph-search').value||'').toLowerCase();
  cy.nodes().removeClass('highlight dim');
  if(!term) return;
  var matched = cy.nodes().filter(function(n){ return String(n.data('id')).toLowerCase().indexOf(term)!==-1; });
  matched.addClass('highlight');
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
