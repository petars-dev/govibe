#!/bin/bash
# usage: collect.sh <outdir>   (server must be up on :18094)
cd "$(dirname "$0")"
OUT=$1
for sc in all app framework; do
  d=$OUT/$sc; mkdir -p $d
  curl -s "localhost:18094/s9/scope?v=$sc" >/dev/null
  B=localhost:18094/dev/graph
  curl -s $B/data/status > $d/status.json
  for p in architecture request_flow db_er docs_coverage config_usage findings; do curl -s "$B/data/view?preset=$p" > $d/view_$p.json; done
  curl -s "$B/data/findings" > $d/findings.json
  for r in routes config schema layers; do curl -s "$B/reports/$r" > $d/report_$r.html; done
  curl -s "$B/data/node?id=route:GET:/api/hello" > $d/node_route_hello.json
  curl -s "$B/data/node?id=pkg:controllers" > $d/node_pkg_controllers.json
  curl -s "$B/data/node?id=cfg:server.port" > $d/node_cfg_server_port.json
  curl -s "$B/data/search?q=hello" > $d/search_hello.json
  ./probe graph_schema '{}' > $d/mcp_graph_schema.txt 2>&1
  ./probe graph_context '{"route":"GET /api/hello"}' > $d/mcp_graph_context_route.txt 2>&1
  ./probe graph_context '{"node":"pkg:controllers"}' > $d/mcp_graph_context_pkg.txt 2>&1
  ./probe graph_findings '{}' > $d/mcp_graph_findings.txt 2>&1
  ./probe graph_impact '{"node":"func:business/actions.Hello"}' > $d/mcp_graph_impact_hello.txt 2>&1
  ./probe graph_impact '{"node":"cfg:server.port"}' > $d/mcp_graph_impact_cfg.txt 2>&1
  ./probe graph_search '{"text":"hello"}' > $d/mcp_graph_search.txt 2>&1
  ./probe graph_neighbors '{"node":"route:GET:/api/hello"}' > $d/mcp_graph_neighbors.txt 2>&1
  ./probe graph_path '{"from":"route:GET:/api/hello","to":"func:business/actions.Hello"}' > $d/mcp_graph_path.txt 2>&1
  ./probe graph_query '{"query":"g.V().Has(g.IRI(\"type\"), g.IRI(\"kind:route\")).All()","limit":200}' > $d/mcp_graph_query_routes.txt 2>&1
  curl -s "localhost:18094/dev/graph/api/v2/read?limit=3" > $d/cayley_http_read.txt 2>&1
done
