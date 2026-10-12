// Package devmcp mounts a Model Context Protocol server, using the official
// Go SDK, over streamable HTTP so local agents can introspect and query the
// same embedded database and graph store that back /dev/db and /dev/graph.
// It is dev-only: routes.go only mounts it outside of production.
package devmcp

import (
	"net/http"

	"github.com/cayleygraph/cayley/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petars-dev/govibe/internal/dbexplorer"
)

// NewHandler builds the MCP server and returns an http.Handler for it, ready
// to be mounted at a single path (e.g. /dev/mcp) in the chi router. Either ex
// or qs may be nil, in which case the corresponding tools are not registered.
// ix, if not nil, is asked to bring stale graph layers up to date before every
// graph tool answers, and backs graph_reindex; without it the graph tools
// answer from the store as it is.
func NewHandler(ex *dbexplorer.Explorer, qs graph.QuadStore, ix GraphIndexer) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:        "govibe-dev-tools",
		Title:       "GoVibe Dev Tools",
		Version:     "0.2.0",
		Description: "Dev-only introspection over GoVibe's embedded MariaDB/MySQL database and a queryable graph model of the project itself.",
	}, &mcp.ServerOptions{
		Instructions: "Dev-only tools for the local GoVibe app. " +
			"START WITH graph_schema: it describes the project graph (what it holds, how fresh it is, which tool answers which question). " +
			"The graph models this project: Go code, HTTP routes and their handlers, templates, config, MCP tools, the database schema, docs and specs. " +
			"graph_context briefs you on a file, package, symbol, route or table in one call; graph_impact says what breaks if it changes " +
			"(and which docs to update); graph_path shows how a route reaches a table; graph_search, graph_neighbors, graph_findings and " +
			"graph_reindex round it out; graph_query runs a raw read-only Gizmo query. Results carry a confidence: 100 is a stored edge, " +
			"lower is an estimate from package imports (call edges are not indexed yet), possible and not proven. " +
			"The resources govibe://graph/overview and govibe://graph/schema and the explain-change prompt are generated from the same graph. " +
			"The graph is brought up to date before each call; edits to docs and specs show up without a restart. " +
			"db_* tools introspect and query the app's MariaDB/MySQL database (read-only except db_insert_test_row/db_reset_test_data, " +
			"which require mutations to be explicitly enabled). None of this is available in production.",
	})

	if ex != nil {
		registerDBTools(srv, ex)
	}
	if qs != nil {
		registerGraphTools(srv, qs, ix)
	}

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// Each tool call is independent and side-effect-free (or a single,
		// explicit mutation) — a stateless transport keeps this endpoint
		// trivial for scripts/agents to call without a session handshake.
		Stateless: true,
	})
}
