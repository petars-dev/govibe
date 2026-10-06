package devmcp

import (
	"context"
	"fmt"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultGraphLimit = 100
	maxGraphLimit     = 1000

	// graphRefreshTimeout bounds the freshness check before a graph call.
	graphRefreshTimeout = 10 * time.Second
)

func registerGraphTools(srv *mcp.Server, qs graph.QuadStore, refresh func(context.Context)) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_query",
		Description: "Run a read-only Gizmo query against the embedded Cayley graph store used by /dev/graph " +
			"and return the matching results as JSON. The store holds a model of this project, rebuilt on boot " +
			"and refreshed before every call: Go packages, files, functions, types and imports; HTTP routes with " +
			"their handlers and middleware; templ components; config keys, env vars and where they are used; " +
			"MCP tools; the database schema (tables, columns, indexes, foreign keys, from the migrations and the live " +
			"database, with drift) and which functions read or write which table; docs and specs with their " +
			"status rows and what they mention. Node IDs and predicates are " +
			"IRIs, so address them as '<iri>' or g.IRI('iri'), never as a bare string: " +
			"g.V(g.IRI('route:GET:/api/hello')).Out(g.IRI('handled_by')).All(), " +
			"g.V('<pkg:controllers>').Out('<imports>').All(), " +
			"g.V(g.IRI('cfg:server.port')).In(g.IRI('uses_config')).All(), " +
			"g.V().Has(g.IRI('type'), g.IRI('kind:table')).In(g.IRI('reads_table')).All(), " +
			"g.V().Has(g.IRI('type'), g.IRI('kind:func')).Filter(like('func:controllers.%')).All(), " +
			"g.V(g.IRI('func:controllers.HelloAPI')).OutPredicates().All(). " +
			"Hand-added quads are plain strings, e.g. g.V('alice').Out('knows').All(). " +
			"Vocabulary and more queries: docs/public/GRAPH_SCHEMA.md.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in graphQueryIn) (*mcp.CallToolResult, graphQueryOut, error) {
		if refresh != nil {
			rctx, cancel := context.WithTimeout(ctx, graphRefreshTimeout)
			refresh(rctx)
			cancel()
		}
		limit := in.Limit
		if limit <= 0 {
			limit = defaultGraphLimit
		}
		if limit > maxGraphLimit {
			limit = maxGraphLimit
		}

		it, err := query.Execute(ctx, qs, gizmo.Name, in.Query, query.Options{Collation: query.JSON, Limit: limit})
		if err != nil {
			return nil, graphQueryOut{}, fmt.Errorf("gizmo: %w", err)
		}
		defer it.Close()

		var results []any
		for it.Next(ctx) {
			results = append(results, it.Result())
		}
		if err := it.Err(); err != nil {
			return nil, graphQueryOut{}, fmt.Errorf("gizmo: %w", err)
		}
		return nil, graphQueryOut{Results: results, Count: len(results)}, nil
	})
}

type graphQueryIn struct {
	Query string `json:"query" jsonschema:"Gizmo query to run, e.g. g.V().All()"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results to return (default 100, max 1000)"`
}

type graphQueryOut struct {
	Results []any `json:"results"`
	Count   int   `json:"count"`
}
