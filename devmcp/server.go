// Package devmcp mounts a Model Context Protocol server, using the official
// Go SDK, over streamable HTTP so local agents can introspect and query the
// same embedded database and graph store that back /dev/db and /dev/graph.
// It is dev-only: routes.go only mounts it outside of production.
package devmcp

import (
	"net/http"

	"github.com/cayleygraph/cayley/graph"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"petars-dev/govibe/storage/dbexplorer"
)

// NewHandler builds the MCP server and returns an http.Handler for it, ready
// to be mounted at a single path (e.g. /dev/mcp) in the chi router. Either ex
// or qs may be nil, in which case the corresponding tools are not registered.
func NewHandler(ex *dbexplorer.Explorer, qs graph.QuadStore) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:        "govibe-dev-tools",
		Title:       "GoVibe Dev Tools",
		Version:     "0.1.0",
		Description: "Dev-only introspection over GoVibe's embedded MariaDB/MySQL database and Cayley graph store.",
	}, &mcp.ServerOptions{
		Instructions: "Dev-only tools for the local GoVibe app. db_* tools introspect and query the app's " +
			"MariaDB/MySQL database (read-only except db_insert_test_row/db_reset_test_data, which require " +
			"mutations to be explicitly enabled). graph_query runs a read-only Gizmo query against the embedded " +
			"Cayley graph store. None of this is available in production.",
	})

	if ex != nil {
		registerDBTools(srv, ex)
	}
	if qs != nil {
		registerGraphTools(srv, qs)
	}

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// Each tool call is independent and side-effect-free (or a single,
		// explicit mutation) — a stateless transport keeps this endpoint
		// trivial for scripts/agents to call without a session handshake.
		Stateless: true,
	})
}
