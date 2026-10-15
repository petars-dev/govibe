package devui

// devDBPage is the embedded, Faucet-like database explorer UI served at
// /dev/db. It is a single static page (no build step) that talks to the JSON
// API mounted at /dev/db/api/*; see devdb.go for those handlers and
// storage/dbexplorer for the introspection/query logic behind them.
const devDBPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>GoVibe Dev DB</title>
<link rel="icon" href="/_govibe/static/govibe-logo.png">
<link rel="stylesheet" href="/_govibe/static/theme.css">
<link rel="stylesheet" href="/_govibe/static/docs.css">
<script src="/_govibe/static/theme.js"></script>
<style>
.db-layout{display:grid;grid-template-columns:280px 1fr;gap:0;min-height:calc(100vh - 52px)}
.db-sidebar{background:var(--bg-elev);border-right:1px solid var(--border);padding:16px;position:sticky;top:52px;align-self:start;height:calc(100vh - 52px);overflow:auto}
.db-sidebar ul{list-style:none;margin:0;padding:0}
.db-table-link{display:flex;justify-content:space-between;gap:8px;padding:6px 8px;border-radius:6px;color:var(--text);cursor:pointer}
.db-table-link:hover{background:rgba(255,255,255,0.05)}
.db-table-link.active{background:rgba(59,130,246,0.15);color:var(--link);border:1px solid rgba(59,130,246,0.35)}
.db-table-link .rows-est{opacity:.6;font-size:.8em;font-family:ui-monospace,monospace}
.db-main{padding:20px 24px;min-width:0}
.db-badges{display:flex;gap:8px;align-items:center;margin-bottom:14px;flex-wrap:wrap}
.badge{padding:2px 8px;border-radius:999px;font-size:.78rem;border:1px solid var(--border);opacity:.85}
.badge.on{color:#3fb950;border-color:#3fb950}
.badge.off{color:var(--muted)}
.db-current{font-weight:650;font-size:1.1rem;margin-bottom:6px}
.db-tabs{display:flex;gap:6px;flex-wrap:wrap;margin-bottom:14px;border-bottom:1px solid var(--border);padding-bottom:10px}
.db-tab-btn{padding:6px 12px;border-radius:8px;border:1px solid var(--border);background:transparent;color:var(--text);cursor:pointer;font:inherit}
.db-tab-btn:hover{border-color:var(--link)}
.db-tab-btn.active{background:rgba(59,130,246,0.15);border-color:rgba(59,130,246,0.35);color:var(--link)}
.db-panel{display:none}
.db-panel.active{display:block}
.db-toolbar{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-bottom:10px}
.db-toolbar input{font-family:ui-monospace,monospace;padding:6px 8px;border-radius:6px;border:1px solid var(--border);background:var(--bg);color:var(--text)}
.db-toolbar input.where{flex:2;min-width:180px}
.db-toolbar input.order{flex:1;min-width:120px}
.db-toolbar input.num{width:80px}
textarea.sql{width:100%;box-sizing:border-box;min-height:120px;font-family:ui-monospace,monospace;padding:10px;border-radius:8px;border:1px solid var(--border);background:var(--bg);color:var(--text);resize:vertical}
.db-result-meta{opacity:.65;font-size:.85rem;margin:8px 0}
.db-table-wrap{overflow:auto;border:1px solid var(--border);border-radius:8px;max-height:60vh}
table.db-grid{width:100%;border-collapse:collapse;font-size:.9rem}
table.db-grid th,table.db-grid td{border:1px solid var(--border);padding:6px 10px;text-align:left;white-space:pre}
table.db-grid thead th{background:var(--bg-elev);position:sticky;top:0}
table.db-grid td.null{color:var(--muted);font-style:italic}
.db-empty{opacity:.65;padding:24px 0}
.db-error{color:#e5534b;background:rgba(229,83,75,0.08);border:1px solid rgba(229,83,75,0.35);border-radius:8px;padding:10px 12px;margin:10px 0;white-space:pre-wrap}
.db-form-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:10px;margin-bottom:12px}
.db-form-field label{display:block;font-size:.8rem;opacity:.75;margin-bottom:4px}
.db-form-field input{width:100%;box-sizing:border-box;padding:6px 8px;border-radius:6px;border:1px solid var(--border);background:var(--bg);color:var(--text)}
.btn-danger{color:#e5534b;border-color:#e5534b}
.btn-danger:hover{background:rgba(229,83,75,0.1)}
.pagination{display:flex;gap:8px;align-items:center;margin-top:10px}
.db-refs{border:1px solid var(--border);border-radius:8px;padding:10px 12px;background:var(--bg-elev)}
.db-refs h4{margin:10px 0 4px;font-size:.78rem;text-transform:uppercase;letter-spacing:.05em;opacity:.7}
.db-refs h4:first-child{margin-top:0}
.db-refs ul{list-style:none;margin:0;padding:0}
.db-refs li{padding:2px 0;font-size:.9rem}
.db-refs .how{opacity:.65;font-size:.8rem;margin-left:6px}
.db-refs .maybe{color:#d29922}
.db-refs code{font-size:.85em}
</style>
</head><body class="docs-body">
<header class="site-header">
<div class="brand"><a class="brand-link" href="/"><img class="brand-logo" src="/_govibe/static/govibe-logo.png" alt="GoVibe"></a><span class="brand-name">GoVibe Dev DB</span></div>
<nav class="top-tabs"><a class="tab" href="/dev">Dev</a><a class="tab" href="/dev/docs/app">Docs</a><a class="tab" href="/dev/docs/specs">Specs</a><a class="tab" href="/dev/graph">Graph</a><a class="tab active" href="/dev/db">DB</a><a class="tab" href="/dev/schedule">Schedule</a></nav>
<div class="header-actions"><a class="btn btn-ghost" href="/">&larr; App</a><button class="theme-toggle" onclick="toggleTheme()" aria-label="Toggle theme"><svg class="icon-sun" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M6.76 4.84l-1.8-1.79-1.41 1.41 1.79 1.8 1.42-1.42zm10.48 0l1.79-1.79 1.41 1.41-1.79 1.8-1.41-1.42zM12 4V1h-1v3h1zm0 19v-3h-1v3h1zm7-11h3v-1h-3v1zm-19 0h3v-1H0v1zm3.55 8.45l1.41 1.41 1.79-1.79-1.41-1.41-1.79 1.79zM18.24 18.24l1.79 1.79 1.41-1.41-1.79-1.79-1.41 1.41zM12 7a5 5 0 100 10 5 5 0 000-10z"/></svg><svg class="icon-moon" width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><path d="M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z"/></svg></button></div>
</header>
<div class="db-layout">
  <aside class="db-sidebar">
    <div class="sidebar-title">Tables</div>
    <input id="tableSearch" class="search-input" type="search" placeholder="Search tables..." aria-label="Search tables">
    <ul id="tableList"></ul>
  </aside>
  <main class="db-main">
    <div class="db-badges">
      <span class="badge" id="conn-badge">connecting&hellip;</span>
      <span class="badge" id="mut-badge">mutations: unknown</span>
      <span style="opacity:.65;font-size:.85rem">Embedded MariaDB/MySQL explorer &mdash; dev-only, disabled in production.</span>
    </div>
    <div id="noTableNotice" class="db-empty">Pick a table on the left, or use the Query/Explain tabs for ad-hoc SQL.</div>
    <div id="tableView" style="display:none">
      <div class="db-current" id="currentTableName"></div>
      <div class="db-tabs">
        <button class="db-tab-btn active" data-tab="schema" onclick="showTab('schema')">Schema</button>
        <button class="db-tab-btn" data-tab="rows" onclick="showTab('rows')">Rows</button>
        <button class="db-tab-btn" data-tab="sample" onclick="showTab('sample')">Sample</button>
        <button class="db-tab-btn" data-tab="mutate" id="mutateTabBtn" style="display:none" onclick="showTab('mutate')">Mutate</button>
      </div>

      <div class="db-panel active" id="panel-schema"></div>

      <div class="db-panel" id="panel-rows">
        <div class="db-toolbar">
          <input class="where" id="rowsWhere" placeholder="WHERE fragment, e.g. id > 10">
          <input class="order" id="rowsOrder" placeholder="ORDER BY, e.g. id DESC">
          <input class="num" id="rowsLimit" type="number" value="50" min="1" max="1000">
          <button class="btn btn-primary" onclick="loadRows(0)">Load rows</button>
        </div>
        <div id="rowsResult"></div>
        <div class="pagination" id="rowsPagination"></div>
      </div>

      <div class="db-panel" id="panel-sample">
        <div class="db-toolbar">
          <input class="num" id="sampleLimit" type="number" value="20" min="1" max="200">
          <button class="btn btn-primary" onclick="loadSample()">Sample rows</button>
        </div>
        <div id="sampleResult"></div>
      </div>

      <div class="db-panel" id="panel-mutate">
        <h3>Insert test row</h3>
        <div id="insertForm" class="db-form-grid"></div>
        <button class="btn btn-primary" onclick="insertRow()">Insert row</button>
        <div id="insertResult"></div>
        <h3 style="margin-top:24px">Reset test data</h3>
        <p style="opacity:.7">Deletes every row in this table. No undo.</p>
        <button class="btn btn-danger" id="resetBtn" onclick="resetTable()">Delete all rows</button>
        <div id="resetResult"></div>
      </div>
    </div>

    <div class="db-tabs" style="margin-top:28px">
      <button class="db-tab-btn active" data-gtab="query" onclick="showGlobalTab('query')">Query</button>
      <button class="db-tab-btn" data-gtab="explain" onclick="showGlobalTab('explain')">Explain</button>
    </div>
    <div class="db-panel active" id="gpanel-query">
      <textarea class="sql" id="querySql" placeholder="SELECT * FROM some_table WHERE ...">SHOW TABLES</textarea>
      <div style="margin:10px 0"><button class="btn btn-primary" onclick="runQuery()">Run query</button></div>
      <div id="queryResult"></div>
    </div>
    <div class="db-panel" id="gpanel-explain">
      <textarea class="sql" id="explainSql" placeholder="SELECT * FROM some_table WHERE ..."></textarea>
      <div style="margin:10px 0"><button class="btn btn-primary" onclick="runExplain()">Explain</button></div>
      <div id="explainResult"></div>
    </div>
  </main>
</div>
<script>
var state = { tables: [], currentTable: null, allowMutations: false, rowsOffset: 0, rowsLimit: 50, schemaByTable: {}, resetArmed: false };

function el(tag, attrs, children){
  var e = document.createElement(tag);
  attrs = attrs || {};
  Object.keys(attrs).forEach(function(k){
    if(k === 'text'){ e.textContent = attrs[k]; }
    else if(k.indexOf('on') === 0){ e.addEventListener(k.slice(2), attrs[k]); }
    else { e.setAttribute(k, attrs[k]); }
  });
  (children||[]).forEach(function(c){ e.appendChild(c); });
  return e;
}

function fmtCell(v){
  if(v === null || v === undefined) return el('td', {class:'null', text:'NULL'});
  var s = typeof v === 'object' ? JSON.stringify(v) : String(v);
  return el('td', {text: s});
}

function renderResult(container, result){
  container.innerHTML = '';
  if(!result || !result.columns){ container.appendChild(el('div', {class:'db-empty', text:'No result.'})); return; }
  var meta = el('div', {class:'db-result-meta', text: result.row_count + ' row(s)' + (result.truncated ? ' (truncated)' : '') + ' in ' + result.duration_ms + 'ms'});
  container.appendChild(meta);
  var wrap = el('div', {class:'db-table-wrap'});
  var table = el('table', {class:'db-grid'});
  var thead = el('thead');
  var htr = el('tr');
  result.columns.forEach(function(c){ htr.appendChild(el('th', {text:c})); });
  thead.appendChild(htr);
  table.appendChild(thead);
  var tbody = el('tbody');
  (result.rows||[]).forEach(function(row){
    var tr = el('tr');
    row.forEach(function(v){ tr.appendChild(fmtCell(v)); });
    tbody.appendChild(tr);
  });
  table.appendChild(tbody);
  wrap.appendChild(table);
  container.appendChild(wrap);
}

function renderError(container, err){
  container.innerHTML = '';
  container.appendChild(el('div', {class:'db-error', text: String(err)}));
}

async function api(path, opts){
  var res = await fetch(path, opts);
  var body = await res.json().catch(function(){ return {}; });
  if(!res.ok){ throw new Error(body.error || (res.status + ' ' + res.statusText)); }
  return body;
}

async function loadMeta(){
  try{
    var meta = await api('/dev/db/api/meta');
    state.allowMutations = !!meta.allow_mutations;
    var mb = document.getElementById('mut-badge');
    mb.textContent = 'mutations: ' + (state.allowMutations ? 'enabled' : 'disabled');
    mb.className = 'badge ' + (state.allowMutations ? 'on' : 'off');
    document.getElementById('mutateTabBtn').style.display = state.allowMutations ? '' : 'none';
    document.getElementById('conn-badge').textContent = 'connected';
    document.getElementById('conn-badge').className = 'badge on';
  }catch(e){
    document.getElementById('conn-badge').textContent = 'disconnected';
    document.getElementById('conn-badge').className = 'badge off';
  }
}

async function loadTables(){
  var list = document.getElementById('tableList');
  try{
    var data = await api('/dev/db/api/tables');
    state.tables = data.tables || [];
    list.innerHTML = '';
    state.tables.forEach(function(t){
      var li = el('li');
      var a = el('div', {class:'db-table-link', 'data-title': t.name.toLowerCase(), onclick: function(){ selectTable(t.name); }});
      a.appendChild(el('span', {text: t.name}));
      a.appendChild(el('span', {class:'rows-est', text: '~' + t.rows_estimate}));
      li.appendChild(a);
      list.appendChild(li);
    });
  }catch(e){
    list.innerHTML = '';
    list.appendChild(el('li', {class:'db-error', text: String(e)}));
  }
}

document.getElementById('tableSearch').addEventListener('input', function(e){
  var term = (e.target.value||'').toLowerCase();
  Array.prototype.forEach.call(document.querySelectorAll('.db-table-link'), function(a){
    a.parentElement.style.display = a.getAttribute('data-title').indexOf(term) !== -1 ? '' : 'none';
  });
});

function showTab(name){
  Array.prototype.forEach.call(document.querySelectorAll('.db-tabs .db-tab-btn[data-tab]'), function(b){
    b.classList.toggle('active', b.getAttribute('data-tab') === name);
  });
  ['schema','rows','sample','mutate'].forEach(function(n){
    document.getElementById('panel-' + n).classList.toggle('active', n === name);
  });
}

function showGlobalTab(name){
  Array.prototype.forEach.call(document.querySelectorAll('.db-tabs .db-tab-btn[data-gtab]'), function(b){
    b.classList.toggle('active', b.getAttribute('data-gtab') === name);
  });
  ['query','explain'].forEach(function(n){
    document.getElementById('gpanel-' + n).classList.toggle('active', n === name);
  });
}

async function selectTable(name){
  state.currentTable = name;
  state.resetArmed = false;
  document.getElementById('resetBtn').textContent = 'Delete all rows';
  Array.prototype.forEach.call(document.querySelectorAll('.db-table-link'), function(a){
    a.classList.toggle('active', a.firstChild.textContent === name);
  });
  document.getElementById('noTableNotice').style.display = 'none';
  document.getElementById('tableView').style.display = '';
  document.getElementById('currentTableName').textContent = name;
  document.getElementById('insertResult').innerHTML = '';
  document.getElementById('resetResult').innerHTML = '';
  showTab('schema');
  await loadSchema(name);
  loadRows(0);
}

async function loadSchema(name){
  var container = document.getElementById('panel-schema');
  try{
    var schema = await api('/dev/db/api/tables/' + encodeURIComponent(name));
    state.schemaByTable[name] = schema;
    container.innerHTML = '';

    container.appendChild(el('h3', {text: 'Columns'}));
    var colsDiv = el('div');
    container.appendChild(colsDiv);
    renderResult(colsDiv, {
      columns: ['name','type','nullable','key','default','extra'],
      rows: schema.columns.map(function(c){ return [c.name, c.type, c.nullable, c.key, c.default, c.extra]; }),
      row_count: schema.columns.length, truncated: false, duration_ms: 0
    });

    container.appendChild(el('h3', {text: 'Indexes'}));
    var idxDiv = el('div');
    container.appendChild(idxDiv);
    if((schema.indexes||[]).length){
      renderResult(idxDiv, {
        columns: ['name','unique','columns'],
        rows: schema.indexes.map(function(i){ return [i.name, i.unique, i.columns.join(', ')]; }),
        row_count: schema.indexes.length, truncated:false, duration_ms:0
      });
    } else { idxDiv.appendChild(el('div', {class:'db-empty', text:'No indexes.'})); }

    container.appendChild(el('h3', {text: 'Foreign keys'}));
    var fkDiv = el('div');
    container.appendChild(fkDiv);
    if((schema.foreign_keys||[]).length){
      renderResult(fkDiv, {
        columns: ['constraint','column','ref_table','ref_column'],
        rows: schema.foreign_keys.map(function(f){ return [f.constraint_name, f.column, f.ref_table, f.ref_column]; }),
        row_count: schema.foreign_keys.length, truncated:false, duration_ms:0
      });
    } else { fkDiv.appendChild(el('div', {class:'db-empty', text:'No foreign keys.'})); }

    loadRefs(name, container);
    buildInsertForm(schema);
  }catch(e){ renderError(container, e); }
}

// What refers to the table, from the project graph (the indexer's model of the
// code, routes, commands and docs). Links go to the graph explorer and the editor; a route
// or command reached only through a package import is marked as possible, not proven.
async function loadRefs(name, container){
  container.appendChild(el('h3', {text: 'Referenced by'}));
  var box = el('div', {class: 'db-refs'});
  container.appendChild(box);
  try{
    var r = await api('/dev/graph/data/table?name=' + encodeURIComponent(name));
    if(!r.known){
      box.appendChild(el('div', {class: 'db-empty', text: (r.notes && r.notes[0]) || 'The project graph does not know this table yet.'}));
      return;
    }
    function link(item, label){
      var a = el('a', {href: item.graph, text: label || item.name || item.id});
      return a;
    }
    function section(title, items, fmt){
      if(!items || !items.length) return;
      box.appendChild(el('h4', {text: title + ' (' + items.length + ')'}));
      var ul = el('ul');
      items.forEach(function(it){ ul.appendChild(fmt(it)); });
      box.appendChild(ul);
    }
    function where(it){
      if(!it.path) return null;
      var loc = it.path + (it.line ? ':' + it.line : '');
      return it.editor ? el('a', {class: 'how', href: it.editor, text: loc}) : el('span', {class: 'how', text: loc});
    }
    section('Code', r.code, function(it){
      var li = el('li', {}, [link(it)]);
      if(it.access) li.appendChild(el('span', {class: 'how', text: it.access}));
      if(!it.certain) li.appendChild(el('span', {class: 'how maybe', text: 'possible (same package)'}));
      var w = where(it); if(w) li.appendChild(w);
      return li;
    });
    section('Routes', r.routes, function(it){
      var li = el('li', {}, [link(it)]);
      if(it.access) li.appendChild(el('span', {class: 'how', text: it.access}));
      li.appendChild(el('span', {class: 'how' + (it.certain ? '' : ' maybe'), text: it.certain ? 'certain' : 'possible, not proven (' + it.confidence + '%, ' + it.method.replace(/_/g, ' ') + ')'}));
      return li;
    });
    // A scheduled command that writes a table runs with nobody watching: its schedule is shown.
    section('Commands', r.commands, function(it){
      var li = el('li', {}, [link(it)]);
      if(it.schedule) li.appendChild(el('span', {class: 'how', text: 'scheduled ' + it.schedule}));
      if(it.access) li.appendChild(el('span', {class: 'how', text: it.access}));
      li.appendChild(el('span', {class: 'how' + (it.certain ? '' : ' maybe'), text: it.certain ? 'certain' : 'possible, not proven (' + it.confidence + '%, ' + it.method.replace(/_/g, ' ') + ')'}));
      return li;
    });
    section('MCP tools', r.tools, function(it){ return el('li', {}, [link(it), el('span', {class: 'how', text: it.certain ? '' : 'possible'})]); });
    section('Docs and specs', r.docs, function(it){
      var li = el('li', {}, [link(it, it.path || it.name)]);
      if(it.spec) li.appendChild(el('span', {class: 'how', text: 'row of ' + it.spec}));
      return li;
    });
    (r.findings || []).forEach(function(f){
      box.appendChild(el('div', {class: 'db-error', text: f.severity + ' ' + f.rule + ': ' + f.message}));
    });
    if(!box.firstChild) box.appendChild(el('div', {class: 'db-empty', text: 'Nothing in the indexed code, routes, commands or docs refers to this table.'}));
    var links = (r.links || []).filter(function(l){ return l.kind === 'report' || l.kind === 'graph'; });
    if(links.length){
      var p = el('div', {class: 'how', style: 'margin-top:8px'});
      links.forEach(function(l, i){ if(i) p.appendChild(document.createTextNode(' \u00b7 ')); p.appendChild(el('a', {href: l.url, text: l.label})); });
      box.appendChild(p);
    }
    (r.notes || []).forEach(function(n){ box.appendChild(el('div', {class: 'how', text: n})); });
  }catch(e){
    box.appendChild(el('div', {class: 'db-empty', text: 'The project graph is not available (' + e.message + ').'}));
  }
}

function buildInsertForm(schema){
  var form = document.getElementById('insertForm');
  form.innerHTML = '';
  schema.columns.forEach(function(c){
    if(String(c.extra||'').toLowerCase().indexOf('auto_increment') !== -1) return;
    var field = el('div', {class:'db-form-field'});
    field.appendChild(el('label', {text: c.name + ' (' + c.type + ')' + (c.nullable ? '' : ' *')}));
    var input = el('input', {'data-col': c.name, placeholder: c.default || ''});
    field.appendChild(input);
    form.appendChild(field);
  });
}

async function loadRows(offset){
  state.rowsOffset = offset || 0;
  state.rowsLimit = parseInt(document.getElementById('rowsLimit').value, 10) || 50;
  var container = document.getElementById('rowsResult');
  var params = new URLSearchParams({
    where: document.getElementById('rowsWhere').value,
    order: document.getElementById('rowsOrder').value,
    limit: String(state.rowsLimit),
    offset: String(state.rowsOffset)
  });
  try{
    var result = await api('/dev/db/api/tables/' + encodeURIComponent(state.currentTable) + '/rows?' + params.toString());
    renderResult(container, result);
    var pager = document.getElementById('rowsPagination');
    pager.innerHTML = '';
    if(state.rowsOffset > 0){
      pager.appendChild(el('button', {class:'btn btn-ghost', onclick: function(){ loadRows(Math.max(0, state.rowsOffset - state.rowsLimit)); }, text: '← Prev'}));
    }
    if(result.truncated || result.row_count === state.rowsLimit){
      pager.appendChild(el('button', {class:'btn btn-ghost', onclick: function(){ loadRows(state.rowsOffset + state.rowsLimit); }, text: 'Next →'}));
    }
  }catch(e){ renderError(container, e); }
}

async function loadSample(){
  var container = document.getElementById('sampleResult');
  var limit = document.getElementById('sampleLimit').value || 20;
  try{
    var result = await api('/dev/db/api/tables/' + encodeURIComponent(state.currentTable) + '/sample?limit=' + encodeURIComponent(limit));
    renderResult(container, result);
  }catch(e){ renderError(container, e); }
}

async function insertRow(){
  var container = document.getElementById('insertResult');
  var values = {};
  Array.prototype.forEach.call(document.querySelectorAll('#insertForm input'), function(input){
    if(input.value !== '') values[input.getAttribute('data-col')] = input.value;
  });
  try{
    var result = await api('/dev/db/api/tables/' + encodeURIComponent(state.currentTable) + '/rows', {
      method: 'POST', headers: {'Content-Type':'application/json'}, body: JSON.stringify({values: values})
    });
    container.innerHTML = '';
    container.appendChild(el('div', {class:'db-result-meta', text: 'Inserted (last_insert_id=' + result.last_insert_id + ', rows_affected=' + result.rows_affected + ')'}));
    if(result.row){
      var rowDiv = el('div');
      container.appendChild(rowDiv);
      renderResult(rowDiv, {
        columns: Object.keys(result.row), rows: [Object.values(result.row)],
        row_count: 1, truncated:false, duration_ms:0
      });
    }
    loadRows(0);
  }catch(e){ renderError(container, e); }
}

async function resetTable(){
  var btn = document.getElementById('resetBtn');
  if(!state.resetArmed){
    state.resetArmed = true;
    btn.textContent = 'Click again to confirm — deletes ALL rows';
    setTimeout(function(){ state.resetArmed = false; btn.textContent = 'Delete all rows'; }, 5000);
    return;
  }
  state.resetArmed = false;
  btn.textContent = 'Delete all rows';
  var container = document.getElementById('resetResult');
  try{
    var result = await api('/dev/db/api/tables/' + encodeURIComponent(state.currentTable) + '/rows', {method: 'DELETE'});
    container.innerHTML = '';
    container.appendChild(el('div', {class:'db-result-meta', text: 'Deleted ' + result.rows_affected + ' row(s) from ' + result.table}));
    loadRows(0);
  }catch(e){ renderError(container, e); }
}

async function runQuery(){
  var container = document.getElementById('queryResult');
  try{
    var result = await api('/dev/db/api/query', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({sql: document.getElementById('querySql').value})});
    renderResult(container, result);
  }catch(e){ renderError(container, e); }
}

async function runExplain(){
  var container = document.getElementById('explainResult');
  try{
    var result = await api('/dev/db/api/explain', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({sql: document.getElementById('explainSql').value})});
    renderResult(container, result);
  }catch(e){ renderError(container, e); }
}

// /dev/db#table=items opens that table (the graph explorer and its reports link here).
function openFromHash(){
  var m = /(?:^|[#&])table=([^&]+)/.exec(location.hash || '');
  if(m){ try{ selectTable(decodeURIComponent(m[1].replace(/\+/g, ' '))); }catch(e){} }
}
window.addEventListener('hashchange', openFromHash);

loadMeta();
loadTables().then(openFromHash);
</script>
</body></html>`
