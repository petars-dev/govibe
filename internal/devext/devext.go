// Package devext is the dev panel as an application sees it: what an App's DevTools
// function is handed, so the app can add MCP tools and pages of its own to the dev
// tooling of a govibe_dev build. It is a collector, nothing more: the framework's dev
// tools read what was registered and mount it (the pages under /dev/app, the tools on
// the MCP server at /dev/mcp). It depends on nothing of the dev stack, so the public
// API can name its types without linking any of it into a default build.
package devext

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// PagePrefix is where an application's own dev pages are served.
const PagePrefix = "/dev/app"

// Tool is an MCP tool an application adds to its dev server.
type Tool struct {
	// Name is the tool's name as agents call it: lower-case letters, digits and
	// underscores, starting with a letter ("orders_summary"). The framework's own
	// names (graph_*, db_*, schedule_*) are refused.
	Name string
	// Description tells an agent what the tool answers and when to use it.
	Description string
	// Input is a zero value of the arguments struct: its exported fields, with their
	// json and jsonschema tags, are the tool's parameters, as for the framework's own
	// tools. Nil, or struct{}, is a tool with no arguments.
	Input any
	// Mutates says the tool changes something; the default (false) marks it read-only
	// for the agent.
	Mutates bool
	// Run answers one call: args is the JSON object of arguments as the agent sent it.
	// The result is returned as the call's structured content, and as text (JSON, or
	// the string itself when the result is one). An error is returned to the agent as
	// a failed call; it never fails the server.
	Run func(ctx context.Context, args json.RawMessage) (any, error)
}

// Page is a page an application adds to the dev panel, linked from /dev.
type Page struct {
	// Path is the page's path under /dev/app ("/orders" is served at /dev/app/orders).
	Path string
	// Title is the link text on /dev.
	Title string
}

// Route is one handler an application registers under /dev/app: a page, or anything
// else (a JSON endpoint, an htmx fragment) that is not linked from /dev.
type Route struct {
	Method  string // "" is every method
	Pattern string // a chi pattern under /dev/app ("/orders", "/orders/{id}")
	Handler http.Handler
}

// Dev collects what an application adds to the dev panel. The framework creates one,
// hands it to App.DevTools, and mounts what it holds. It is not safe for concurrent
// use; registration happens once, at boot.
type Dev struct {
	tools    []Tool
	pages    []Page
	routes   []Route
	problems []string
	names    map[string]bool
}

// New is an empty Dev.
func New() *Dev { return &Dev{names: map[string]bool{}} }

var toolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// reservedToolPrefixes are the framework's own tool families.
var reservedToolPrefixes = []string{"graph_", "db_", "schedule_"}

// Tool adds an MCP tool. A tool with an invalid or duplicate name, a reserved prefix,
// or no Run is recorded as a problem (Problems) and left out, never a panic: the dev
// panel must come up whatever the app registers.
func (d *Dev) Tool(t Tool) {
	switch {
	case t.Name == "":
		d.problem("a tool with no name (description %q) was not registered", t.Description)
		return
	case !toolNameRe.MatchString(t.Name):
		d.problem("tool %q: a name is lower-case letters, digits and underscores, starting with a letter", t.Name)
		return
	case t.Run == nil:
		d.problem("tool %q has no Run", t.Name)
		return
	case d.names[t.Name]:
		d.problem("tool %q is registered twice", t.Name)
		return
	}
	for _, p := range reservedToolPrefixes {
		if strings.HasPrefix(t.Name, p) {
			d.problem("tool %q: names starting with %s are the framework's", t.Name, p)
			return
		}
	}
	d.names[t.Name] = true
	d.tools = append(d.tools, t)
}

// Page adds a page at /dev/app/<path>, served by h for GET and linked from /dev as title.
func (d *Dev) Page(path, title string, h http.Handler) {
	path = cleanPath(path)
	if path == "" {
		d.problem("page %q: a path starts with / and names something", title)
		return
	}
	if h == nil {
		d.problem("page %s has no handler", path)
		return
	}
	if title == "" {
		title = strings.TrimPrefix(path, "/")
	}
	d.pages = append(d.pages, Page{Path: path, Title: title})
	d.routes = append(d.routes, Route{Method: http.MethodGet, Pattern: path, Handler: h})
}

// Handle adds a handler at /dev/app/<pattern> for one method ("" for all), without a
// link on /dev: a JSON endpoint behind a page, an htmx fragment, a form's POST.
func (d *Dev) Handle(method, pattern string, h http.Handler) {
	pattern = cleanPath(pattern)
	if pattern == "" {
		d.problem("handler %s %q: a pattern starts with / and names something", method, pattern)
		return
	}
	if h == nil {
		d.problem("handler %s %s has no handler", method, pattern)
		return
	}
	d.routes = append(d.routes, Route{Method: strings.ToUpper(method), Pattern: pattern, Handler: h})
}

// Tools lists the registered tools, in registration order.
func (d *Dev) Tools() []Tool { return append([]Tool(nil), d.tools...) }

// Pages lists the pages linked from /dev, by path.
func (d *Dev) Pages() []Page {
	out := append([]Page(nil), d.pages...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Routes lists every handler to mount under /dev/app, in registration order.
func (d *Dev) Routes() []Route { return append([]Route(nil), d.routes...) }

// Problems lists what was refused and why: the framework logs them and /dev shows them.
func (d *Dev) Problems() []string { return append([]string(nil), d.problems...) }

// Empty reports whether nothing was registered (and nothing refused).
func (d *Dev) Empty() bool {
	return len(d.tools) == 0 && len(d.routes) == 0 && len(d.problems) == 0
}

func (d *Dev) problem(format string, args ...any) {
	d.problems = append(d.problems, fmt.Sprintf(format, args...))
}

// cleanPath makes a path start with one slash and end without one; "" when nothing is left.
func cleanPath(p string) string {
	p = "/" + strings.Trim(strings.TrimSpace(p), "/")
	if p == "/" {
		return ""
	}
	return p
}

// ToolFunc builds a Tool whose arguments and result are typed: the framework's
// DevToolFunc is this, with the names an application reads.
func ToolFunc[In, Out any](name, description string, run func(ctx context.Context, in In) (Out, error)) Tool {
	var zero In
	return Tool{
		Name:        name,
		Description: description,
		Input:       zero,
		Run: func(ctx context.Context, args json.RawMessage) (any, error) {
			var in In
			if len(args) > 0 && string(args) != "null" {
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, fmt.Errorf("arguments: %w", err)
				}
			}
			return run(ctx, in)
		},
	}
}
