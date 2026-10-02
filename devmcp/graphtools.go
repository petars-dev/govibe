package devmcp

import (
	"context"
	"fmt"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultGraphLimit = 100
	maxGraphLimit     = 1000
)

func registerGraphTools(srv *mcp.Server, qs graph.QuadStore) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_query",
		Description: "Run a read-only Gizmo query against the embedded Cayley graph store used by /dev/graph " +
			"(e.g. \"g.V().All()\" or \"g.V('alice').Out('knows').All()\") and return the matching results as JSON.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in graphQueryIn) (*mcp.CallToolResult, graphQueryOut, error) {
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
