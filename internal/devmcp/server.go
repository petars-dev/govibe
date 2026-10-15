// Package devmcp mounts a Model Context Protocol server, using the official
// Go SDK, over streamable HTTP so local agents can introspect and query the
// same embedded database and graph store that back /dev/db and /dev/graph.
// It is dev-only: routes.go only mounts it outside of production.
package devmcp

import (
	"context"
	"net/http"
	"sort"

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
func NewHandler(ex *dbexplorer.Explorer, qs graph.QuadStore, ix GraphIndexer, options ...Options) http.Handler {
	var opt Options
	if len(options) > 0 {
		opt = options[0]
	}
	srv := newServer(opt.AppName)
	if ex != nil {
		registerDBTools(srv, ex)
	}
	if qs != nil {
		registerGraphTools(srv, qs, ix)
	}
	registerScheduleTools(srv, opt.Schedule)
	registerAppResources(srv, opt)

	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		// Each tool call is independent and side-effect-free (or a single,
		// explicit mutation) — a stateless transport keeps this endpoint
		// trivial for scripts/agents to call without a session handshake.
		Stateless: true,
	})
}

// newServer is the MCP server with no tools on it.
func newServer(appName string) *mcp.Server {
	title := "GoVibe Dev Tools"
	if appName != "" {
		title = appName + " dev tools (GoVibe)"
	}
	return mcp.NewServer(&mcp.Implementation{
		Name:        "govibe-dev-tools",
		Title:       title,
		Version:     "0.2.0",
		Description: "Dev-only introspection over GoVibe's embedded MariaDB/MySQL database and a queryable graph model of the project itself.",
	}, &mcp.ServerOptions{
		Instructions: "Dev-only tools for the local GoVibe app. " +
			"START WITH graph_schema: it describes the project graph (what it holds, how fresh it is, which tool answers which question). " +
			"The graph models this project: Go code, HTTP routes and their handlers, templates, config, MCP tools, the database schema, docs and specs. " +
			"graph_context briefs you on a file, package, symbol, route, command or table in one call; graph_impact says what breaks if it changes " +
			"(and which docs to update); graph_path shows how a route reaches a table; graph_search, graph_neighbors, graph_findings and " +
			"graph_reindex round it out; graph_query runs a raw read-only Gizmo query. " +
			"Every graph tool answers about the app's own code by default and takes `scope`: framework (what the framework it runs on contributes) or all (both, each node labelled with its origin); " +
			"graph_query is the one tool that is not scoped. Results carry a confidence: 100 is a stored edge, " +
			"lower is an estimate from package imports (call edges are not indexed yet), possible and not proven. " +
			"The resources govibe://graph/overview and govibe://graph/schema and the explain-change prompt are generated from the same graph; " +
			"govibe://skill is the rules of this project, govibe://app/manifest what its App declaration contributes, and govibe://docs/<slug> the framework's reference docs. " +
			"The graph is brought up to date before each call; edits to docs and specs show up without a restart. " +
			"schedule_status says what the app has scheduled (its commands with a Schedule), when each runs next and how its recent runs went. " +
			"db_* tools introspect and query the app's MariaDB/MySQL database (read-only except db_insert_test_row/db_reset_test_data, " +
			"which require mutations to be explicitly enabled). None of this is available in production.",
	})
}

// ToolInfo names one tool of the dev MCP server.
type ToolInfo struct {
	Name        string
	Description string
}

// Tools lists every tool the server offers when both its backing stores exist (the
// db_*, graph_* and schedule_* tools), as a client sees them. It is how the framework
// publishes them in its manifest, so a project's graph can name the tools its dev
// server offers without indexing the framework.
func Tools(ctx context.Context) ([]ToolInfo, error) {
	srv := newServer("")
	// Registration only builds closures: nothing is read from the stores until a tool is called.
	registerDBTools(srv, nil)
	registerGraphTools(srv, nil, nil)
	registerScheduleTools(srv, nil)

	clientT, serverT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		return nil, err
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "govibe-manifest"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		return nil, err
	}
	defer cs.Close()

	var out []ToolInfo
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, ToolInfo{Name: t.Name, Description: t.Description})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
