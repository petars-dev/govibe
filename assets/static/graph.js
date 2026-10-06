/* The /dev/graph explorer. It draws bounded views the server chose (/dev/graph/data/view),
   never the whole graph, so it stays quick however many quads the store holds. Everything
   that comes from the server is put on the page as text (textContent), never as HTML. */
(function () {
  'use strict';

  // ---------- small helpers ----------
  function $(id) { return document.getElementById(id); }
  function el(tag, props, kids) {
    var e = document.createElement(tag);
    Object.keys(props || {}).forEach(function (k) {
      var v = props[k];
      if (k === 'text') e.textContent = v;
      else if (k === 'class') e.className = v;
      else if (k.indexOf('on') === 0) e.addEventListener(k.slice(2), v);
      else if (v !== undefined && v !== null && v !== false) e.setAttribute(k, v === true ? '' : v);
    });
    (kids || []).forEach(function (c) { if (c) e.appendChild(typeof c === 'string' ? document.createTextNode(c) : c); });
    return e;
  }
  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); return node; }
  function num(n) { return Number(n || 0).toLocaleString('en-US'); }
  function safeURL(u) { return /^(\/(?!\/)|vscode:\/\/)/.test(u || '') ? u : '#'; }

  function api(path, params) {
    var q = new URLSearchParams();
    Object.keys(params || {}).forEach(function (k) { if (params[k] !== '' && params[k] != null) q.set(k, params[k]); });
    var url = '/dev/graph/data/' + path + (q.toString() ? '?' + q.toString() : '');
    return fetch(url, { headers: { Accept: 'application/json' } }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error(j.error || (r.status + ' ' + r.statusText));
        return j;
      });
    });
  }

  function ago(iso) {
    var s = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000));
    if (s < 5) return 'just now';
    if (s < 90) return s + ' s ago';
    if (s < 5400) return Math.round(s / 60) + ' min ago';
    if (s < 129600) return Math.round(s / 3600) + ' h ago';
    return Math.round(s / 86400) + ' days ago';
  }

  // ---------- what a kind looks like ----------
  var KINDS = {
    package: ['#4f8cff', 'round-rectangle'], file: ['#8b95a5', 'rectangle'], func: ['#2fb7a3', 'ellipse'], type: ['#a173ff', 'hexagon'],
    route: ['#f0883e', 'diamond'], middleware: ['#d4a72c', 'triangle'], template: ['#e26fb0', 'tag'], mcp_tool: ['#c86bfa', 'star'],
    config_key: ['#c9a227', 'barrel'], env_var: ['#b08a3e', 'round-tag'], doc: ['#3fb950', 'rectangle'], spec: ['#2ea043', 'round-rectangle'],
    spec_item: ['#56d364', 'ellipse'], table: ['#3b82f6', 'cut-rectangle'], column: ['#7aa7ff', 'ellipse'], index: ['#6c8ebf', 'vee'],
    migration: ['#a8794a', 'pentagon'], annotation: ['#8b95a5', 'ellipse'], finding: ['#e5534b', 'octagon'], rule: ['#8b95a5', 'octagon'],
    build: ['#8b95a5', 'ellipse'], layer: ['#8b95a5', 'ellipse'], term: ['#8b95a5', 'ellipse']
  };
  var ARCH = { wiring: '#8b95a5', dev: '#a173ff', controllers: '#f0883e', middleware: '#d4a72c', views: '#e26fb0', business: '#3fb950', storage: '#3b82f6', support: '#6c8ebf' };
  var SEV = { error: '#e5534b', warning: '#d29922', info: '#58a6ff' };
  var SEV_ORDER = ['error', 'warning', 'info'];
  var SIZE = { package: 26, route: 22, table: 26, doc: 22, spec: 22, finding: 18, mcp_tool: 22 };

  function kindStyle(n) {
    var k = KINDS[n.kind] || ['#8b95a5', 'ellipse'];
    var color = k[0];
    if (n.kind === 'package' && ARCH[n.group]) color = ARCH[n.group];
    if (n.kind === 'finding' && SEV[n.group]) color = SEV[n.group];
    return { color: color, shape: k[1] };
  }

  var POLY = {
    diamond: '7,0 14,7 7,14 0,7', triangle: '7,1 14,13 0,13', hexagon: '3.5,1 10.5,1 14,7 10.5,13 3.5,13 0,7',
    pentagon: '7,0 14,5.5 11.5,14 2.5,14 0,5.5', octagon: '4.2,0 9.8,0 14,4.2 14,9.8 9.8,14 4.2,14 0,9.8 0,4.2',
    'cut-rectangle': '3,0 11,0 14,3 14,11 11,14 3,14 0,11 0,3', tag: '0,2 10,2 14,7 10,12 0,12', 'round-tag': '0,2 10,2 14,7 10,12 0,12',
    vee: '0,0 14,0 7,14', star: '7,0 8.8,5 14,5.3 9.9,8.6 11.3,13.7 7,10.8 2.7,13.7 4.1,8.6 0,5.3 5.2,5'
  };
  var SVGNS = 'http://www.w3.org/2000/svg';
  function swatch(kind) {
    var st = kindStyle({ kind: kind }), svg = document.createElementNS(SVGNS, 'svg'), s;
    svg.setAttribute('viewBox', '0 0 14 14'); svg.setAttribute('aria-hidden', 'true');
    if (POLY[st.shape]) { s = document.createElementNS(SVGNS, 'polygon'); s.setAttribute('points', POLY[st.shape]); }
    else if (st.shape === 'ellipse') { s = document.createElementNS(SVGNS, 'circle'); s.setAttribute('cx', 7); s.setAttribute('cy', 7); s.setAttribute('r', 6.5); }
    else {
      s = document.createElementNS(SVGNS, 'rect'); s.setAttribute('x', 0.5); s.setAttribute('y', 1.5); s.setAttribute('width', 13); s.setAttribute('height', 11);
      s.setAttribute('rx', st.shape === 'rectangle' ? 0 : st.shape === 'barrel' ? 5 : 3);
    }
    s.setAttribute('fill', st.color);
    svg.appendChild(s);
    return svg;
  }

  // ---------- state ----------
  var S = {
    preset: 'architecture', focus: '', view: null, hidden: {}, cy: null, selected: '', seq: 0,
    status: null, lastIndexedAt: '', presets: [], tab: 'inspector', fsev: '', frule: '', findings: null, expanded: {}
  };

  // ---------- the freshness banner ----------
  function renderBanner(st) {
    S.status = st;
    var b = clear($('gv-banner')), f = st.freshness || {};
    if (!f.indexed) {
      b.appendChild(el('span', { class: 'gv-muted', text: 'The graph has not been indexed yet.' }));
      b.appendChild(el('button', { class: 'gv-btn', id: 'gv-reindex', text: 'Index now', onclick: reindex }));
      return;
    }
    var stale = (f.stale_layers || []).length, bad = (f.layers || []).some(function (l) { return l.status === 'error'; });
    var left = el('span', {}, [el('span', { class: 'gv-dot' + (bad ? ' bad' : stale ? ' stale' : ''), title: bad ? 'a layer failed' : stale ? 'stale layers: ' + f.stale_layers.join(', ') : 'up to date' })]);
    left.appendChild(document.createTextNode('indexed '));
    left.appendChild(el('span', { id: 'gv-ago', 'data-t': f.indexed_at, text: ago(f.indexed_at) }));
    b.appendChild(left);
    if (f.git_sha) b.appendChild(el('span', {}, ['@ ', el('code', { text: f.git_sha + (f.dirty ? '+dirty' : '') })]));
    b.appendChild(el('span', { text: num(f.quads) + ' quads' }));
    b.appendChild(el('span', { class: 'gv-muted', title: 'quads written by the last run, relative to the one before', text: '+' + num(f.added) + '/−' + num(f.removed) + ' vs previous' }));
    if (f.duration_ms) b.appendChild(el('span', { class: 'gv-muted', text: f.duration_ms + ' ms' }));

    var fc = st.findings || {};
    var fs = el('a', { href: '#findings', title: 'Open the findings panel', onclick: function (e) { e.preventDefault(); showTab('findings'); } });
    if (!fc.available) fs.appendChild(document.createTextNode('findings: not built'));
    else if (!fc.total) fs.appendChild(document.createTextNode('no findings'));
    else {
      SEV_ORDER.forEach(function (s) {
        if (fc[s]) fs.appendChild(el('span', { class: 'gv-sev ' + s, style: 'margin-right:8px', text: fc[s] + ' ' + s }));
      });
    }
    b.appendChild(fs);

    var layers = el('span', { class: 'gv-layers', title: 'layers of the graph and their last run' });
    (f.layers || []).forEach(function (l) {
      var isStale = (f.stale_layers || []).indexOf(l.name) >= 0;
      var tip = l.name + ': ' + l.status + (l.quads ? ', ' + num(l.quads) + ' quads' : '') + (l.note ? ' (' + l.note + ')' : '') + (l.error ? ' (' + l.error + ')' : '') + (isStale ? ' (stale)' : '');
      layers.appendChild(el('span', { class: 'gv-pill ' + (isStale ? 'stale' : l.status), title: tip, text: l.name }));
    });
    b.appendChild(layers);
    b.appendChild(el('span', { class: 'gv-spacer' }));
    var reports = el('span', { class: 'gv-reports' }, [el('span', { class: 'gv-muted', text: 'Reports:' })]);
    ['routes', 'config', 'schema', 'layers'].forEach(function (r) { reports.appendChild(el('a', { href: '/dev/graph/reports/' + r, text: r })); });
    b.appendChild(reports);
    b.appendChild(el('button', { class: 'gv-btn', id: 'gv-reindex', text: 'Reindex', title: 'Rebuild every layer now', onclick: reindex }));
    (st.notes || []).forEach(function (n) { b.appendChild(el('div', { class: 'gv-muted', style: 'flex-basis:100%', text: n })); });
    if (S.lastIndexedAt && S.lastIndexedAt !== f.indexed_at && S.view) {
      b.appendChild(el('span', { class: 'gv-muted' }, ['The graph changed. ', el('button', { class: 'gv-node-link', text: 'Redraw the view', onclick: function () { loadView(); } })]));
    }
  }

  function loadStatus() {
    return api('status').then(function (st) { renderBanner(st); if (st.findings) setCount(st.findings); return st; })
      .catch(function (e) { clear($('gv-banner')).appendChild(el('span', { class: 'gv-sev error', text: 'Could not read the graph: ' + e.message })); });
  }

  function setCount(fc) {
    var c = $('gv-findings-count');
    c.textContent = fc.available ? '(' + fc.total + ')' : '';
    var worst = fc.error ? 'error' : fc.warning ? 'warning' : fc.info ? 'info' : '';
    c.className = 'gv-count' + (worst ? ' gv-sev ' + worst : '');
    c.style.setProperty('--x', '0');
  }

  setInterval(function () { var a = $('gv-ago'); if (a) a.textContent = ago(a.getAttribute('data-t')); }, 5000);
  setInterval(function () { if (!document.hidden) loadStatus(); }, 20000);
  document.addEventListener('visibilitychange', function () { if (!document.hidden) loadStatus(); });

  function reindex() {
    var btn = $('gv-reindex');
    if (btn) { btn.disabled = true; btn.textContent = 'Reindexing…'; }
    return fetch('/dev/graph/reindex', { method: 'POST' }).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) { if (!r.ok) throw new Error(failedLayers(j) || j.error || r.statusText); });
    }).catch(function (e) { alert('Reindex finished with a problem: ' + e.message); })
      .then(function () { S.findings = null; return Promise.all([loadStatus(), loadView(), loadFindings()]); });
  }
  function failedLayers(j) {
    return (j.layers || []).filter(function (l) { return l.status === 'error'; }).map(function (l) { return l.name + ': ' + l.error; }).join('; ');
  }

  // ---------- presets, kind chips ----------
  function renderPresets() {
    var box = clear($('gv-presets'));
    (S.presets || []).forEach(function (p) {
      var on = !S.focus && S.preset === p.id;
      box.appendChild(el('button', { class: 'gv-preset' + (on ? ' active' : ''), role: 'tab', 'aria-selected': on ? 'true' : 'false', title: p.description, text: p.title, onclick: function () { pickPreset(p.id); } }));
    });
  }
  function pickPreset(id) {
    S.preset = id; S.focus = ''; S.hidden = {}; S.expanded = {};
    history.replaceState(null, '', '#preset=' + id);
    loadView();
  }

  function renderChips() {
    var box = clear($('gv-kinds')), v = S.view;
    if (!v) return;
    var counts = {};
    if (S.cy) S.cy.nodes().forEach(function (n) { if (n.data('kind') !== '__head') counts[n.data('kind')] = (counts[n.data('kind')] || 0) + 1; });
    else (v.kinds || []).forEach(function (k) { counts[k.kind] = k.count; });
    Object.keys(counts).sort(function (a, b) { return counts[b] - counts[a]; }).forEach(function (k) {
      var chip = el('button', { class: 'gv-chip', 'aria-pressed': S.hidden[k] ? 'false' : 'true', title: 'Show or hide ' + k + ' nodes', onclick: function () { S.hidden[k] = !S.hidden[k]; applyVisibility(); renderChips(); } },
        [swatch(k), k.replace(/_/g, ' '), el('span', { class: 'n', text: String(counts[k]) })]);
      box.appendChild(chip);
    });
  }
  function applyVisibility() {
    if (!S.cy) return;
    S.cy.batch(function () {
      S.cy.nodes().forEach(function (n) { if (n.hasClass('colhead')) return; n.style('display', S.hidden[n.data('kind')] && n.id() !== S.focus ? 'none' : 'element'); });
    });
  }

  // ---------- loading and drawing a view ----------
  function loadView() {
    var seq = ++S.seq, params = S.focus ? { focus: S.focus, depth: 1 } : { preset: S.preset };
    $('gv-title').textContent = 'Loading…';
    return api('view', params).then(function (v) {
      if (seq !== S.seq) return;
      S.view = v; S.presets = v.presets || S.presets; S.lastIndexedAt = S.status && S.status.freshness ? S.status.freshness.indexed_at : '';
      renderPresets();
      draw(v);
    }).catch(function (e) {
      if (seq !== S.seq) return;
      $('gv-title').textContent = 'Could not load the view';
      clear($('gv-notes')).appendChild(el('div', { text: e.message }));
      destroyCy();
    });
  }

  function destroyCy() { if (S.cy) { S.cy.destroy(); S.cy = null; } }

  function cssVar(name, fallback) { return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback; }

  function nodeData(n) {
    var st = kindStyle(n), bw = 0, bc = 'transparent', bs = 'solid';
    if (n.worst) { bw = 3; bc = SEV[n.worst]; }
    if (n.flag === 'uncovered') { bw = 3; bc = '#e0793b'; bs = 'dashed'; }
    else if (n.flag === 'undeclared') { bw = 3; bc = '#e5534b'; bs = 'dashed'; }
    else if (n.flag === 'sensitive') { bw = Math.max(bw, 2); bc = bc === 'transparent' ? '#a173ff' : bc; bs = 'dotted'; }
    return {
      id: n.id, label: n.label, kind: n.kind, color: st.color, shape: st.shape, size: SIZE[n.kind] || 16,
      bw: bw, bc: bc, bs: bs, flag: n.flag || '', worst: n.worst || '', findings: n.findings || 0, col: n.col, dist: n.dist || 0, degree: n.degree || 0, more: n.more || 0, group: n.group || ''
    };
  }
  function edgeData(e, i) {
    return { id: 'e' + i + ':' + e.source + '|' + e.predicate + '|' + e.target, source: e.source, target: e.target, pred: e.predicate, violation: !!e.violation, inferred: !!e.inferred, conf: e.confidence || 0 };
  }

  // How a view is laid out decides where labels go: beside the node when there are few columns
  // to read across, under it when there are many, and under it in a free layout.
  function layoutMode(view) {
    if (!(view.columns && view.columns.length)) return 'free';
    return view.columns.length <= 4 ? 'right' : 'below';
  }
  function currentStyle() {
    var v = S.view;
    return styleSheet(v.edges.length > 700, v.nodes.length <= 150, layoutMode(v));
  }

  function styleSheet(many, labelled, mode) {
    var right = mode === 'right';
    var fg = cssVar('--text', '#e6edf3'), muted = cssVar('--muted', '#9fb1c3'), edge = cssVar('--muted', '#8a8a8a'), bg = cssVar('--bg', '#0b0f14');
    return [
      { selector: 'node', style: {
        'shape': 'data(shape)', 'background-color': 'data(color)', 'width': 'data(size)', 'height': 'data(size)',
        'border-width': 'data(bw)', 'border-color': 'data(bc)', 'border-style': 'data(bs)',
        'color': fg, 'font-size': 11, 'text-outline-color': bg, 'text-outline-width': 2, 'min-zoomed-font-size': 7,
        'label': labelled ? 'data(label)' : '', 'text-wrap': 'ellipsis', 'text-max-width': right ? 260 : mode === 'below' ? 170 : 160,
        'text-valign': right ? 'center' : 'bottom', 'text-halign': right ? 'right' : 'center', 'text-margin-x': right ? 8 : 0, 'text-margin-y': right ? 0 : 3
      } },
      { selector: 'node[flag = "dev"]', style: { 'opacity': 0.75 } },
      { selector: 'node.colhead', style: { 'width': 1, 'height': 1, 'background-opacity': 0, 'border-width': 0, 'label': 'data(label)', 'font-size': 13, 'font-weight': 700, 'color': muted, 'text-transform': 'uppercase', 'text-valign': 'center', 'text-halign': 'center', 'text-margin-x': 0, 'text-margin-y': 0, 'min-zoomed-font-size': 0, 'events': 'no', 'z-index': 0, 'text-max-width': 300 } },
      { selector: 'node.named, node.hl, node.hover, node:selected', style: { 'label': 'data(label)', 'z-index': 10, 'text-max-width': 320 } },
      { selector: 'node:selected', style: { 'border-width': 4, 'border-color': '#e0793b', 'border-style': 'solid' } },
      { selector: 'edge', style: {
        'curve-style': many ? 'haystack' : mode !== 'free' ? 'bezier' : 'straight', 'width': 1.2, 'line-color': edge, 'opacity': 0.55,
        'target-arrow-color': edge, 'target-arrow-shape': many ? 'none' : 'triangle', 'arrow-scale': 0.8
      } },
      { selector: 'edge[?inferred]', style: { 'line-style': 'dashed', 'line-dash-pattern': [6, 4] } },
      { selector: 'edge[?violation]', style: { 'line-color': '#e5534b', 'target-arrow-color': '#e5534b', 'width': 3, 'opacity': 1, 'z-index': 9 } },
      { selector: 'edge.hl', style: { 'line-color': '#e0793b', 'target-arrow-color': '#e0793b', 'width': 2.4, 'opacity': 1, 'label': 'data(pred)', 'font-size': 9, 'color': muted, 'text-outline-color': bg, 'text-outline-width': 2, 'z-index': 11 } },
      { selector: '.dim', style: { 'opacity': 0.12 } }
    ];
  }

  // Columns left to right: nodes keep the server's order (it puts a route next to its handler),
  // then each column is reordered by where its neighbours in earlier columns sit, which untangles most crossings.
  function layeredPositions(view) {
    var nodes = view.nodes, ncols = Math.max.apply(null, nodes.map(function (n) { return n.col; }).concat([0])) + 1;
    var cols = [], idx = {}, i;
    for (i = 0; i < ncols; i++) cols.push([]);
    nodes.forEach(function (n, k) { idx[n.id] = k; cols[n.col].push(n.id); });
    var adj = {};
    view.edges.forEach(function (e) { (adj[e.source] = adj[e.source] || []).push(e.target); (adj[e.target] = adj[e.target] || []).push(e.source); });
    var col = {}; nodes.forEach(function (n) { col[n.id] = n.col; });
    var y = {};
    cols[0].forEach(function (id, k) { y[id] = k; });
    for (var c = 1; c < ncols; c++) {
      cols[c].forEach(function (id) {
        var ys = (adj[id] || []).filter(function (o) { return col[o] < c && y[o] !== undefined; }).map(function (o) { return y[o]; });
        y[id] = ys.length ? ys.reduce(function (a, b) { return a + b; }, 0) / ys.length : null;
      });
      var order = {}; cols[c].forEach(function (id, k) { order[id] = k; });
      cols[c].sort(function (a, b) {
        if (y[a] === null && y[b] === null) return order[a] - order[b];
        if (y[a] === null) return 1; if (y[b] === null) return -1;
        return y[a] - y[b] || order[a] - order[b];
      });
      cols[c].forEach(function (id, k) { y[id] = k; });
    }
    var tallest = Math.max.apply(null, cols.map(function (c) { return c.length; }).concat([1]));
    var right = layoutMode(view) === 'right';
    var rowH = right ? (tallest > 60 ? 20 : tallest > 25 ? 28 : 38) : (tallest > 40 ? 34 : 50), colW = right ? 340 : 200, pos = {};
    // A column much taller than the layout is wide wraps into sub-columns, so the picture fills the
    // canvas instead of shrinking to a thin strip.
    var wrap = Math.max(1, Math.min(4, Math.ceil((tallest * rowH) / (0.7 * colW * ncols))));
    var per = cols.map(function (list) { return list.length > 12 ? Math.ceil(list.length / wrap) : list.length || 1; });
    var tallRows = Math.max.apply(null, per.concat([1])), xBase = 0;
    cols.forEach(function (list, c) {
      var subs = Math.max(1, Math.ceil(list.length / per[c]));
      var offset = (tallRows - Math.min(list.length, per[c])) * rowH / 2;
      list.forEach(function (id, k) {
        pos[id] = { x: xBase + Math.floor(k / per[c]) * colW, y: offset + (k % per[c]) * rowH };
      });
      xBase += subs * colW + (subs > 1 ? colW * 0.35 : 0);
    });
    return pos;
  }

  // Column titles: label-only nodes above each column, so they pan and zoom with the graph.
  function columnHeads(view) {
    if (!(view.columns && view.columns.length)) return [];
    var pos = layeredPositions(view), top = Infinity, left = {};
    view.nodes.forEach(function (n) { top = Math.min(top, pos[n.id].y); left[n.col] = pos[n.id].x; });
    return view.columns.map(function (title, c) {
      return { group: 'nodes', classes: 'colhead', selectable: false, grabbable: false, data: { id: '__col' + c, kind: '__head', label: title }, position: { x: left[c] === undefined ? c * 200 : left[c], y: top - 54 } };
    });
  }

  function layoutOptions(view) {
    var n = view.nodes.length;
    if (view.columns && view.columns.length) {
      var pos = layeredPositions(view);
      return { name: 'preset', positions: function (node) { return node.hasClass('colhead') ? node.position() : pos[node.id()]; }, fit: true, padding: 30, animate: false };
    }
    if (view.preset === 'focus') {
      return { name: 'concentric', concentric: function (node) { return 10 - node.data('dist'); }, levelWidth: function () { return 1; }, minNodeSpacing: 26, padding: 30, animate: false, avoidOverlap: true, spacingFactor: 1.15 };
    }
    if (view.preset === 'db_er') return { name: 'breadthfirst', directed: true, spacingFactor: 1.6, padding: 40, avoidOverlap: true, animate: false };
    if (n <= 80) return { name: 'cose', animate: false, padding: 30, nodeRepulsion: 9000, idealEdgeLength: 90, numIter: 600, randomize: true };
    return { name: 'concentric', concentric: function (node) { return node.data('degree'); }, levelWidth: function () { return 2; }, minNodeSpacing: 18, padding: 30, animate: false };
  }

  function draw(v) {
    var t0 = performance.now();
    destroyCy();
    var notes = clear($('gv-notes'));
    (v.notes || []).forEach(function (n) { notes.appendChild(el('div', { text: n })); });
    $('gv-title').textContent = v.title || '';
    $('gv-desc').textContent = v.description || '';
    $('gv-back').hidden = !S.focus;
    var cv = $('cy');
    if (!v.nodes.length) {
      renderChips();
      $('gv-stats').textContent = '';
      return;
    }
        var els = v.nodes.map(function (n) { return { group: 'nodes', data: nodeData(n) }; })
      .concat(v.edges.map(function (e, i) { return { group: 'edges', data: edgeData(e, i) }; }))
      .concat(columnHeads(v));
    S.cy = cytoscape({
      container: cv, elements: els, style: currentStyle(), layout: layoutOptions(v),
      textureOnViewport: true, hideEdgesOnViewport: v.edges.length > 700, minZoom: 0.05, maxZoom: 4, wheelSensitivity: 0.25, boxSelectionEnabled: false
    });
    S.cy.on('tap', 'node', function (evt) { selectNode(evt.target.id()); });
    S.cy.on('dbltap', 'node', function (evt) { expand(evt.target.id()); });
    S.cy.on('tap', function (evt) { if (evt.target === S.cy) { S.cy.elements().removeClass('dim hl'); } });
    S.cy.on('mouseover', 'node', function (evt) { evt.target.addClass('hover'); });
    S.cy.on('mouseout', 'node', function (evt) { evt.target.removeClass('hover'); });
    // A handful of nodes should not be blown up to fill the canvas.
    if (S.cy.zoom() > 1.15) { S.cy.zoom(1.15); S.cy.center(); }
    applyVisibility();
    renderChips();
    var ms = Math.round(performance.now() - t0);
    $('gv-stats').textContent = v.nodes.length + (v.total > v.nodes.length ? ' of ' + v.total : '') + ' nodes, ' + v.edges.length + ' edges · drawn in ' + ms + ' ms' + (v.quads ? ' · graph has ' + num(v.quads) + ' quads' : '');
    if (S.selected && S.cy.getElementById(S.selected).length) S.cy.getElementById(S.selected).select();
    window.__gvLastDrawMs = ms;
  }

  function selectNode(id) {
    S.selected = id;
    if (S.cy) {
      var n = S.cy.getElementById(id);
      S.cy.elements().removeClass('dim hl');
      if (n.length) {
        S.cy.elements().unselect();
        n.select();
        var hood = n.closedNeighborhood();
        S.cy.elements().not(hood).not('.colhead').addClass('dim');
        hood.addClass('hl');
      }
    }
    return inspect(id);
  }

  // Go to a node: select it where it is drawn, or else draw its neighbourhood.
  function goto(id) {
    if (S.cy && S.cy.getElementById(id).length && !S.cy.getElementById(id).style('display').match(/none/)) {
      var n = S.cy.getElementById(id);
      S.cy.animate({ center: { eles: n }, zoom: Math.max(S.cy.zoom(), 0.9) }, { duration: 200 });
      selectNode(id);
      return;
    }
    focusOn(id);
  }
  function focusOn(id) {
    S.focus = id; S.selected = id; S.hidden = {}; S.expanded = {};
    history.replaceState(null, '', '#node=' + encodeURIComponent(id));
    showTab('inspector');
    inspect(id);
    loadView().then(function () {
      if (S.cy && S.cy.getElementById(id).length) S.cy.getElementById(id).select();
    });
  }
  $('gv-back').addEventListener('click', function () { S.focus = ''; S.selected = ''; history.replaceState(null, '', '#preset=' + S.preset); loadView(); });
  $('gv-fit').addEventListener('click', function () { if (S.cy) S.cy.fit(undefined, 30); });

  // Expanding a node adds its neighbours in a ring around it; nothing else moves.
  function expand(id) {
    if (!S.cy || S.expanded[id]) return;
    S.expanded[id] = true;
    api('view', { focus: id, depth: 1, limit: 150 }).then(function (v) {
      var anchor = S.cy.getElementById(id);
      if (!anchor.length) return;
      var have = {}; S.cy.nodes().forEach(function (n) { have[n.id()] = true; });
      var fresh = v.nodes.filter(function (n) { return !have[n.id]; });
      var p = anchor.position(), r = 90 + Math.min(fresh.length, 40) * 3;
      S.cy.batch(function () {
        fresh.forEach(function (n, i) {
          var a = (2 * Math.PI * i) / Math.max(fresh.length, 1);
          S.cy.add({ group: 'nodes', data: nodeData(n), position: { x: p.x + r * Math.cos(a), y: p.y + r * Math.sin(a) } });
        });
        v.edges.forEach(function (e, i) {
          var d = edgeData(e, 'x' + Date.now() + i);
          if (!S.cy.edges().some(function (x) { return x.data('source') === d.source && x.data('target') === d.target && x.data('pred') === d.pred; })) S.cy.add({ group: 'edges', data: d });
        });
        var more = (v.nodes.find(function (n) { return n.id === id; }) || {}).more || 0;
        anchor.data('more', more);
      });
      applyVisibility();
      renderChips();
      var added = fresh.length;
      var msg = added ? 'Added ' + added + ' neighbour' + (added === 1 ? '' : 's') + ' of ' + anchor.data('label') + '.' : anchor.data('label') + ' has no more neighbours to add.';
      $('gv-stats').textContent = msg + ' ' + S.cy.nodes().not('.colhead').length + ' nodes now.';
      selectNode(id);
    }).catch(function (e) { delete S.expanded[id]; $('gv-stats').textContent = 'Could not expand: ' + e.message; });
  }

  // ---------- the inspector ----------
  function sevBadge(s) { return el('span', { class: 'gv-sev ' + s, text: s }); }
  // A package or file is told apart by its path; anything else by its name, with where it is.
  function nodeLink(ref) {
    var byPath = ref.kind === 'package' || ref.kind === 'file' || ref.kind === 'doc' || ref.kind === 'spec';
    var text = byPath ? (ref.path || ref.name || ref.id) : (ref.name || ref.id);
    var kids = [el('span', { class: 'gv-k', text: ref.kind }), text];
    if (!byPath && ref.path) kids.push(el('span', { class: 'gv-muted', text: '  ' + ref.path + (ref.line ? ':' + ref.line : '') }));
    return el('button', { class: 'gv-node-link', title: ref.id, onclick: function () { goto(ref.id); } }, kids);
  }
  function attrValue(v) {
    if (Array.isArray(v)) { var ul = el('ul'); v.forEach(function (x) { ul.appendChild(el('li', { text: String(x) })); }); return ul; }
    return document.createTextNode(String(v));
  }

  function inspect(id) {
    var box = clear($('gv-inspector'));
    showTab('inspector');
    // A hand-added term is not in the indexed graph: there is nothing to look up.
    var term = S.cy && S.view && S.view.preset === 'manual' ? S.cy.getElementById(id) : null;
    if (term && term.length) {
      box.appendChild(el('div', { class: 'gv-node-title' }, [el('span', { class: 'gv-kindbadge', text: 'hand-added' }), term.data('label')]));
      box.appendChild(el('p', { class: 'gv-muted', text: 'Quads added by hand are plain strings with no layer label. A reindex never touches them; query them with the Gizmo console below.' }));
      return Promise.resolve();
    }
    box.appendChild(el('p', { class: 'gv-muted', text: 'Loading…' }));
    return api('node', { id: id }).then(function (d) {
      if (S.selected !== id && S.selected) return;
      clear(box);
      box.appendChild(el('div', { class: 'gv-node-title' }, [el('span', { class: 'gv-kindbadge', text: d.node.kind }), d.label]));
      box.appendChild(el('div', { class: 'gv-id', text: d.node.id }));
      if (d.node.path) box.appendChild(el('div', { class: 'gv-id', text: d.node.path + (d.node.line ? ':' + d.node.line : '') }));
      if (d.summary) box.appendChild(el('div', { class: 'gv-summary', text: d.summary }));
      var links = el('div', { class: 'gv-links' });
      (d.links || []).forEach(function (l) {
        if (l.kind === 'graph') return; // already here
        links.appendChild(el('a', { href: safeURL(l.url), text: l.label, target: l.kind === 'editor' ? '_self' : null }));
      });
      if (links.childNodes.length) box.appendChild(links);
      if ((d.findings || []).length) {
        box.appendChild(el('h3', { text: 'Findings about it' }));
        d.findings.forEach(function (f) { box.appendChild(findingCard(f, false)); });
      }
      if ((d.attrs || []).length) {
        box.appendChild(el('h3', { text: 'Attributes' }));
        var t = el('table', { class: 'gv-attrs' });
        d.attrs.forEach(function (a) { t.appendChild(el('tr', {}, [el('th', { text: a.name }), el('td', {}, [attrValue(a.value)])])); });
        box.appendChild(t);
      }
      [['out', 'Points to'], ['in', 'Pointed to by']].forEach(function (pair) {
        var groups = d[pair[0]] || [];
        if (!groups.length) return;
        box.appendChild(el('h3', { text: pair[1] }));
        groups.forEach(function (g) {
          var ul = el('ul');
          g.nodes.forEach(function (n) { ul.appendChild(el('li', {}, [nodeLink(n)])); });
          if (g.truncated) ul.appendChild(el('li', { class: 'gv-muted', text: '… and ' + (g.total - g.nodes.length) + ' more (expand the node in the graph)' }));
          box.appendChild(el('div', { class: 'gv-group' }, [el('div', { class: 'gv-pred', text: g.predicate + ' (' + g.total + ')' }), ul]));
        });
      });
    }).catch(function (e) { clear(box).appendChild(el('p', { class: 'gv-sev error', text: e.message })); });
  }

  function findingCard(f, withActions) {
    var c = el('div', { class: 'gv-finding ' + f.severity }, [
      el('div', { class: 'gv-rule' }, [sevBadge(f.severity), ' ' + f.rule]),
      el('p', { text: f.message })
    ]);
    if (withActions) {
      var acts = el('div', { class: 'gv-links' }, [
        el('button', { class: 'gv-btn', text: 'Show ' + (f.about.split(':')[0] || 'node'), title: f.about, onclick: function () { goto(f.about); } }),
        el('button', { class: 'gv-btn', text: 'Details', onclick: function () { S.selected = f.id; inspect(f.id); } })
      ]);
      c.appendChild(acts);
    }
    return c;
  }

  // ---------- findings panel ----------
  function showTab(name) {
    S.tab = name;
    Array.prototype.forEach.call(document.querySelectorAll('.gv-tab'), function (t) {
      var on = t.getAttribute('data-tab') === name;
      t.classList.toggle('active', on); t.setAttribute('aria-selected', on ? 'true' : 'false');
    });
    $('gv-inspector').hidden = name !== 'inspector';
    $('gv-findings').hidden = name !== 'findings';
    if (name === 'findings') loadFindings();
  }
  Array.prototype.forEach.call(document.querySelectorAll('.gv-tab'), function (t) {
    t.addEventListener('click', function () { showTab(t.getAttribute('data-tab')); });
  });

  function loadFindings() {
    return api('findings', { severity: S.fsev, rule: S.frule, limit: 200 }).then(function (r) { S.findings = r; renderFindings(r); })
      .catch(function (e) { clear($('gv-findings')).appendChild(el('p', { class: 'gv-sev error', text: e.message })); });
  }
  function renderFindings(r) {
    var box = clear($('gv-findings'));
    if (!r.available) {
      box.appendChild(el('p', { class: 'gv-sev warning', text: r.note || 'The findings layer is not built.' }));
      return;
    }
    var sev = el('div', { class: 'gv-filters' });
    [''].concat(SEV_ORDER).forEach(function (s) {
      var n = s ? (r.by_severity || {})[s] || 0 : r.total_all;
      sev.appendChild(el('button', { class: 'gv-chip gv-filter', 'aria-pressed': S.fsev === s ? 'true' : 'false', onclick: function () { S.fsev = S.fsev === s ? '' : s; loadFindings(); } },
        [s ? sevBadge(s) : 'all', s ? el('span', { class: 'n', text: String(n) }) : null]));
    });
    var sel = el('select', { 'aria-label': 'Filter by rule', onchange: function () { S.frule = this.value; loadFindings(); } }, [el('option', { value: '', text: 'every rule' })]);
    (r.rules || []).forEach(function (ru) {
      var o = el('option', { value: ru.name, text: ru.name + ' (' + ru.findings + ')' + (ru.checked ? '' : ' — not run') });
      if (ru.name === S.frule) o.selected = true;
      sel.appendChild(o);
    });
    sev.appendChild(sel);
    box.appendChild(sev);
    if (r.note) box.appendChild(el('p', { class: 'gv-muted', text: r.note }));
    if (!r.findings.length) box.appendChild(el('p', { class: 'gv-muted', text: r.total_all ? 'No findings match the filter.' : 'No findings: every rule that could run found nothing.' }));
    r.findings.forEach(function (f) { box.appendChild(findingCard(f, true)); });
    if (r.truncated) box.appendChild(el('p', { class: 'gv-muted', text: 'Showing the first ' + r.findings.length + ' of ' + r.total + '.' }));
    var rules = el('details', { class: 'gv-rules' }, [el('summary', { text: 'Rules (' + (r.rules || []).length + ')' })]);
    var ul = el('ul'); (r.rules || []).forEach(function (ru) {
      ul.appendChild(el('li', { class: ru.checked ? '' : 'off' }, [el('strong', { text: ru.name }), ' — ' + ru.description + (ru.checked ? '' : ' [' + ru.note + ']')]));
    });
    rules.appendChild(ul); box.appendChild(rules);
  }

  // ---------- search ----------
  var searchTimer = 0, hits = [], hitSel = -1;
  var sBox = $('gv-search'), sList = $('gv-search-results');
  function closeSearch() { sList.hidden = true; hits = []; hitSel = -1; }
  function paintHits() {
    clear(sList);
    hits.forEach(function (h, i) {
      sList.appendChild(el('li', { class: i === hitSel ? 'sel' : '', onmousedown: function (e) { e.preventDefault(); pick(h); } },
        [el('span', { class: 'gv-kind', text: h.kind }), el('span', { class: 'gv-name', text: h.name || h.id }), el('span', { class: 'gv-sub', text: h.path || '' })]));
    });
    sList.hidden = !hits.length;
  }
  function pick(h) { closeSearch(); sBox.value = ''; sBox.blur(); goto(h.id); }
  sBox.addEventListener('input', function () {
    clearTimeout(searchTimer);
    var q = sBox.value.trim();
    if (q.length < 2) { closeSearch(); return; }
    searchTimer = setTimeout(function () {
      api('search', { q: q, limit: 12 }).then(function (r) {
        if (sBox.value.trim() !== q) return;
        hits = r.hits || []; hitSel = hits.length ? 0 : -1; paintHits();
        if (!hits.length) { clear(sList).appendChild(el('li', { class: 'gv-muted', text: 'Nothing matches “' + q + '”' })); sList.hidden = false; }
      }).catch(function (e) { clear(sList).appendChild(el('li', { class: 'gv-sev error', text: e.message })); sList.hidden = false; });
    }, 180);
  });
  sBox.addEventListener('keydown', function (e) {
    if (e.key === 'ArrowDown' && hits.length) { hitSel = (hitSel + 1) % hits.length; paintHits(); e.preventDefault(); }
    else if (e.key === 'ArrowUp' && hits.length) { hitSel = (hitSel - 1 + hits.length) % hits.length; paintHits(); e.preventDefault(); }
    else if (e.key === 'Enter' && hits[hitSel]) { pick(hits[hitSel]); e.preventDefault(); }
    else if (e.key === 'Escape') { closeSearch(); sBox.blur(); }
  });
  sBox.addEventListener('blur', function () { setTimeout(closeSearch, 120); });
  document.addEventListener('keydown', function (e) {
    if (e.key === '/' && !/^(INPUT|TEXTAREA|SELECT)$/.test((document.activeElement || {}).tagName || '')) { e.preventDefault(); sBox.focus(); }
  });

  // ---------- theme ----------
  new MutationObserver(function () { if (S.cy && S.view) { S.cy.style(currentStyle()); } })
    .observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
  if (window.ResizeObserver) new ResizeObserver(function () { if (S.cy) S.cy.resize(); }).observe($('cy'));

  // ---------- the quad editor and Gizmo console (Cayley's own API) ----------
  function nq(v) { return '"' + String(v).replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"'; }
  window.addQuad = function () {
    var sub = $('q-sub').value, pred = $('q-pred').value, obj = $('q-obj').value, label = $('q-label').value, out = $('add-result');
    if (!sub || !pred || !obj) { out.textContent = 'subject, predicate and object are required'; return; }
    var line = nq(sub) + ' ' + nq(pred) + ' ' + nq(obj) + (label ? ' ' + nq(label) : '') + ' .\n';
    fetch('/dev/graph/api/v2/write', { method: 'POST', headers: { 'Content-Type': 'application/n-quads' }, body: line })
      .then(function (r) { return r.text().then(function (t) { out.textContent = t; if (r.ok) { if (S.preset === 'manual' && !S.focus) loadView(); else pickPreset('manual'); loadStatus(); } }); })
      .catch(function (e) { out.textContent = String(e); });
  };
  window.browseQuads = function () {
    var out = $('browse-result');
    fetch('/dev/graph/api/v2/read?format=json').then(function (r) { return r.text(); }).then(function (t) { out.textContent = t.length > 200000 ? t.slice(0, 200000) + '\n… (cut: use the Gizmo console for anything specific)' : t; })
      .catch(function (e) { out.textContent = String(e); });
  };
  window.runQuery = function () {
    var q = $('query-input').value, out = $('query-result');
    fetch('/dev/graph/api/v2/query?lang=gizmo', { method: 'POST', body: q }).then(function (r) { return r.text(); })
      .then(function (t) { try { out.textContent = JSON.stringify(JSON.parse(t), null, 2); } catch (_e) { out.textContent = t; } })
      .catch(function (e) { out.textContent = String(e); });
  };

  // ---------- start ----------
  function start() {
    var h = decodeURIComponent((location.hash || '').replace(/^#/, ''));
    var node = /^node=(.+)$/.exec(h), preset = /^preset=(.+)$/.exec(h);
    if (node) { S.focus = node[1]; S.selected = node[1]; }
    else if (preset) S.preset = preset[1];
    loadStatus().then(function () {
      loadView().then(function () { if (S.focus) inspect(S.focus); });
      api('findings', { limit: 1 }).then(function (r) { setCount({ available: r.available, total: r.total }); }).catch(function () {});
    });
    window.addEventListener('hashchange', function () {
      var m = /^#node=(.+)$/.exec(decodeURIComponent(location.hash));
      if (m && m[1] !== S.focus) focusOn(m[1]);
    });
  }
  window.__gv = { S: S, layeredPositions: layeredPositions, ago: ago, safeURL: safeURL };
  start();
})();
