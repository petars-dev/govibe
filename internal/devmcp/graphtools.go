package devmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cayleygraph/cayley/graph"
	"github.com/cayleygraph/cayley/query"
	"github.com/cayleygraph/cayley/query/gizmo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/internal/codegraph"
)

const (
	defaultGraphLimit = 100
	maxGraphLimit     = 1000

	// graphRefreshTimeout bounds the freshness check before a graph call.
	graphRefreshTimeout = 10 * time.Second
)

// GraphIndexer is what the graph tools need from the indexer: to bring stale
// layers up to date before answering, to say which still are, and to rebuild
// layers on request. *codegraph.Indexer is the implementation.
type GraphIndexer interface {
	Refresh(ctx context.Context) (*codegraph.Report, error)
	RunLayers(ctx context.Context, layers []string) (*codegraph.Report, error)
	Stale(ctx context.Context) ([]string, error)
	LayerNames() []string
}

// graphTools is what every graph tool shares: the store to read and, when there
// is one, the indexer that keeps it fresh.
type graphTools struct {
	qs graph.QuadStore
	ix GraphIndexer // may be nil
}

// refresh brings stale layers up to date. A failure is logged, not returned: the
// graph as stored still answers.
func (g *graphTools) refresh(ctx context.Context) {
	if g.ix == nil {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, graphRefreshTimeout)
	defer cancel()
	if _, err := g.ix.Refresh(rctx); err != nil {
		logrus.WithError(err).Warn("graph index: refresh before a graph tool failed; answering from the stored graph")
	}
}

// model refreshes the graph and reads it. The read is a few thousand quads, tens
// of milliseconds, so nothing is cached: every answer is from the graph as it is.
func (g *graphTools) model(ctx context.Context) (*codegraph.Model, error) {
	g.refresh(ctx)
	m, err := codegraph.Load(ctx, g.qs)
	if err != nil {
		return nil, err
	}
	if m.Empty() {
		return nil, fmt.Errorf("the graph has not been indexed yet: indexing runs in the background after boot, so retry in a moment, or call graph_reindex")
	}
	return m, nil
}

func (g *graphTools) stale(ctx context.Context) []string {
	if g.ix == nil {
		return nil
	}
	stale, err := g.ix.Stale(ctx)
	if err != nil {
		return nil
	}
	return stale
}

func boolPtr(b bool) *bool { return &b }

// readOnly marks a tool that changes nothing.
var readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}

const graphQueryPrompt = "Prefer graph_context, graph_impact, graph_path, graph_search and graph_neighbors for the common questions; " +
	"call graph_schema first for the vocabulary, counts, example queries and how fresh the graph is. "

func registerGraphTools(srv *mcp.Server, qs graph.QuadStore, ix GraphIndexer) {
	g := &graphTools{qs: qs, ix: ix}

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_query",
		Description: graphQueryPrompt +
			"Runs a read-only Gizmo query against the embedded Cayley graph store used by /dev/graph " +
			"and returns the matching results as JSON. The store holds a model of this project, rebuilt on boot " +
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
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in graphQueryIn) (*mcp.CallToolResult, graphQueryOut, error) {
		g.refresh(ctx)
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

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_schema",
		Description: "START HERE. Describes the project graph as it is right now: every kind of node with its count, ID format and a real example, " +
			"every predicate with its count and what it links, how to read confidence and method, which tool answers which question, " +
			"example graph_query queries, which layers are built, and how fresh the graph is (git sha, dirty, indexed_at, stale layers). " +
			"Opt-in layers that are off (calls, git) are named with how to switch them on, so an empty answer is not read as a clean one.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, codegraph.SchemaInfo, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.SchemaInfo{}, err
		}
		return nil, *m.Schema(g.stale(ctx)), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_context",
		Description: "One-call briefing on a file, package, function, type, route, table, template, config key, spec or any node: where it lives (package, arch layer), " +
			"what it defines, the routes it serves, the tables it touches, the config it reads, the docs and specs that mention it, findings about it, " +
			"and its neighbours. For a route or function: its handler, middleware, what it calls and is called by, and the tables it reaches (through the chain of calls when the calls layer is on; " +
			"otherwise estimated from package imports and labelled possible, not proven); for a file, doc or function, its git history (last commit, churn, what changes with it) when the git layer is on. " +
			"For a table: columns, keys, relations (declared and implied, with confidence), readers, writers and migrations. " +
			"Give exactly one of path, symbol, route, table or node.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in contextIn) (*mcp.CallToolResult, codegraph.Context, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.Context{}, err
		}
		c, err := m.Context(codegraph.ContextQuery{Path: in.Path, Symbol: in.Symbol, Route: in.Route, Table: in.Table, Node: in.Node})
		if err != nil {
			return nil, codegraph.Context{}, err
		}
		return nil, *c, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_impact",
		Description: "What is affected if this changes? Returns the nodes that depend on it, strongest evidence first, each with its confidence and method: " +
			"100 is a stored edge or a plain call, 90 an edge read from SQL strings or db tags, 80 and below a call through an interface (interface_dispatch), and 60 and below a package-level " +
			"estimate (used for code only when the calls layer is off: the importers of the package that holds the changed code, and the routes and MCP tools registered in them, with the import chain in `through`). " +
			"With the calls layer on, routes and tools carry the chain of calls that reaches them in `through`, and the default depth is 6. " +
			"Also lists the docs, specs and spec rows that mention the changed code (what to update). For a table the routes carry an access " +
			"(read, write, read+write, the union over every call path). A file or package means everything it defines; a migration, the tables it introduced. " +
			"`co_changed` lists what usually changes with the changed files (git layer). " +
			"Call graph_context first if you only need to know what something is.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in impactIn) (*mcp.CallToolResult, codegraph.ImpactResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.ImpactResult{}, err
		}
		res, err := m.Impact(codegraph.ImpactQuery{Node: in.Node, Depth: in.Depth, Limit: in.Limit, Kinds: in.Kinds})
		if err != nil {
			return nil, codegraph.ImpactResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_path",
		Description: "How does one node reach another? A chain of dependency edges (route -> handler -> service -> store -> table; package -> package; function -> config key), " +
			"each hop with its confidence, the surest chain first; the path is as sure as its weakest hop. With the calls layer on, a route and a table are linked by calls or not at all. " +
			"Without it, if no exact chain exists the search goes through package imports and shared packages, flagged `inferred` with confidence 50. " +
			"Tries both orientations (a depends on b, then b on a). When nothing links them it says so and returns what each side reaches by exact edges, " +
			"so the missing hop is visible. It never invents a hop.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pathIn) (*mcp.CallToolResult, codegraph.PathResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.PathResult{}, err
		}
		res, err := m.Path(codegraph.PathQuery{From: in.From, To: in.To, MaxDepth: in.MaxDepth})
		if err != nil {
			return nil, codegraph.PathResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_search",
		Description: "Find nodes by words: every word must match the node's ID, name, path, title, signature or doc text; best match first. " +
			"Use it when you know part of a name or a phrase; use graph_context when you know what you are looking at.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, codegraph.SearchResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.SearchResult{}, err
		}
		res, err := m.SearchNodes(codegraph.SearchQuery{Text: in.Text, Kinds: in.Kinds, Limit: in.Limit})
		if err != nil {
			return nil, codegraph.SearchResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_neighbors",
		Description: "The nodes directly connected to a node, and the edges between them, out to a depth of 3, bounded. " +
			"Filter by direction (out, in, both) and by predicate. `degree` counts the node's edges by predicate even when the list is cut off.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in neighborsIn) (*mcp.CallToolResult, codegraph.NeighborsResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.NeighborsResult{}, err
		}
		res, err := m.Neighbors(codegraph.NeighborsQuery{Node: in.Node, Direction: in.Direction, Predicates: in.Predicates, Depth: in.Depth, Limit: in.Limit})
		if err != nil {
			return nil, codegraph.NeighborsResult{}, err
		}
		return nil, *res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "graph_findings",
		Description: "What is stale, broken or against the rules: layering violations, docs that mention code that is gone, config keys declared and never read, " +
			"tables without a primary key, a route no doc mentions. Each finding has a rule, a severity (error, warning, info), a message that says what to do, " +
			"and the node it is about; filter by `severity` or `rule` (a trailing * matches a prefix, e.g. `docs.*`). `rules` lists every rule and whether it ran on this graph: " +
			"a rule that did not run (a layer it reads is not built, or there is no live database to compare with) has not looked, so no findings from it is not a clean bill; " +
			"`available` is false when the whole findings layer is missing.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findingsIn) (*mcp.CallToolResult, codegraph.FindingsResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, codegraph.FindingsResult{}, err
		}
		return nil, *m.Findings(codegraph.FindingsQuery{Severity: in.Severity, Rule: in.Rule, Limit: in.Limit}), nil
	})

	if ix != nil {
		mcp.AddTool(srv, &mcp.Tool{
			Name: "graph_reindex",
			Description: "Rebuild the graph from the sources now. With no layers, every layer that is on; otherwise the named layers and the layers built on them. " +
				"Naming an opt-in layer (calls: the call graph, about a second; git: history) switches it on for as long as the server runs. " +
				"Every graph tool already brings stale layers up to date before answering, so this is for when something changed that fingerprints cannot see, " +
				"or to retry a layer that failed. It writes only the indexer's own quads (hand-added quads survive) and is safe to repeat. " +
				"Returns each layer's status, whether it ran, and its quad counts.",
			Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in reindexIn) (*mcp.CallToolResult, reindexOut, error) {
			rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			rep, err := ix.RunLayers(rctx, in.Layers)
			if err != nil && rep == nil {
				return nil, reindexOut{}, err
			}
			out := reindexOut{KnownLayers: ix.LayerNames()}
			if rep != nil {
				out.Report = *rep
				out.Failed = rep.Failed()
			}
			if err != nil {
				out.Error = err.Error()
			}
			return nil, out, nil
		})
	}

	registerGraphResources(srv, g)
	registerGraphPrompts(srv, g)
}

// --- resources and prompts ---

const (
	overviewURI = "govibe://graph/overview"
	schemaURI   = "govibe://graph/schema"
)

func registerGraphResources(srv *mcp.Server, g *graphTools) {
	srv.AddResource(&mcp.Resource{
		URI:         overviewURI,
		Name:        "graph-overview",
		Title:       "Project architecture overview",
		Description: "The architecture of this project rendered from the graph: layers and their packages, imports between layers, routes with handlers, MCP tools, database tables, config, specs, and what is not covered. Always as fresh as the graph.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, "text/markdown", m.Overview(g.stale(ctx))), nil
	})

	srv.AddResource(&mcp.Resource{
		URI:         schemaURI,
		Name:        "graph-schema",
		Title:       "Graph vocabulary",
		Description: "What graph_schema returns, as a document: node kinds, predicates, conventions, example queries and freshness.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, err
		}
		b, err := json.MarshalIndent(m.Schema(g.stale(ctx)), "", "  ")
		if err != nil {
			return nil, err
		}
		return textResource(req.Params.URI, "application/json", string(b)), nil
	})
}

func textResource(uri, mime, text string) *mcp.ReadResourceResult {
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: text}}}
}

func registerGraphPrompts(srv *mcp.Server, g *graphTools) {
	srv.AddPrompt(&mcp.Prompt{
		Name:  "explain-change",
		Title: "Explain a change",
		Description: "Briefs you on the blast radius of a change: for each changed file, what it defines, what it affects (routes, tools, tables, packages) and how sure the graph is, " +
			"the docs and specs that mention it, and the docs the project rule says to update (the spec, ARCHITECTURE.md, MEMORY_SCHEMA.md). Give changed paths, a unified diff, or both.",
		Arguments: []*mcp.PromptArgument{
			{Name: "paths", Title: "Changed paths", Description: "Repository-relative paths, separated by commas or newlines."},
			{Name: "diff", Title: "Unified diff", Description: "The output of git diff; the files it names are analysed."},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		m, err := g.model(ctx)
		if err != nil {
			return nil, err
		}
		args := req.Params.Arguments
		text, err := m.ExplainChange(args["paths"], args["diff"])
		if err != nil {
			return nil, err
		}
		return &mcp.GetPromptResult{
			Description: "Impact of the change and the docs to update",
			Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
		}, nil
	})
}

// --- tool inputs and outputs ---

type graphQueryIn struct {
	Query string `json:"query" jsonschema:"Gizmo query to run, e.g. g.V().All()"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results to return (default 100, max 1000)"`
}

type graphQueryOut struct {
	Results []any `json:"results"`
	Count   int   `json:"count"`
}

type contextIn struct {
	Path   string `json:"path,omitempty" jsonschema:"a repo-relative file or directory, doc or spec (a :line suffix or an absolute path inside the repo also works), e.g. controllers/api.go"`
	Symbol string `json:"symbol,omitempty" jsonschema:"a Go function, type or templ component: controllers.HelloAPI, orderstore.Store.List, Type.Method or a bare name"`
	Route  string `json:"route,omitempty" jsonschema:"a route as 'GET /api/hello', or a bare path, or a concrete URL path that a route pattern serves"`
	Table  string `json:"table,omitempty" jsonschema:"a table name, e.g. orders"`
	Node   string `json:"node,omitempty" jsonschema:"any node ID (cfg:server.port, tool:graph_query, spec:graph-indexer) or any of the forms above"`
}

type impactIn struct {
	Node  string   `json:"node" jsonschema:"what changes: a node ID or any reference graph_context takes (a file, package, symbol, route, table, config key, migration)"`
	Depth int      `json:"depth,omitempty" jsonschema:"hops of exact dependencies to follow (default 3, max 6; with the calls layer on, default 6, max 10)"`
	Limit int      `json:"limit,omitempty" jsonschema:"max affected nodes returned, weakest evidence dropped first (default 100, max 500)"`
	Kinds []string `json:"kinds,omitempty" jsonschema:"only return these kinds, e.g. route, mcp_tool, func, package, table (by_kind still counts everything)"`
}

type pathIn struct {
	From     string `json:"from" jsonschema:"where to start: a node ID or any reference graph_context takes"`
	To       string `json:"to" jsonschema:"where to end"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"longest chain to search, in hops (default 12, max 24)"`
}

type searchIn struct {
	Text  string   `json:"text" jsonschema:"words to look for; every word must match"`
	Kinds []string `json:"kinds,omitempty" jsonschema:"only these kinds, e.g. func, route, table, doc (aliases like function and endpoint work)"`
	Limit int      `json:"limit,omitempty" jsonschema:"max hits (default 20, max 100)"`
}

type neighborsIn struct {
	Node       string   `json:"node" jsonschema:"the node: an ID or any reference graph_context takes"`
	Direction  string   `json:"direction,omitempty" jsonschema:"out (edges leaving the node), in (edges arriving) or both (default)"`
	Predicates []string `json:"predicates,omitempty" jsonschema:"only follow these predicates, e.g. imports, handled_by, reads_table"`
	Depth      int      `json:"depth,omitempty" jsonschema:"how far to walk (default 1, max 3)"`
	Limit      int      `json:"limit,omitempty" jsonschema:"max neighbours returned (default 50, max 300)"`
}

type findingsIn struct {
	Severity string `json:"severity,omitempty" jsonschema:"only this severity"`
	Rule     string `json:"rule,omitempty" jsonschema:"only this rule; a trailing * matches a prefix"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max findings (default 50, max 200)"`
}

type reindexIn struct {
	Layers []string `json:"layers,omitempty" jsonschema:"layers to rebuild: code, routes, templates, config, mcp, db, docs, findings, and the opt-in calls and git (naming one switches it on); omit for every layer that is on"`
}

type reindexOut struct {
	codegraph.Report
	Failed      bool     `json:"failed"`
	Error       string   `json:"error,omitempty"`
	KnownLayers []string `json:"known_layers"`
}
