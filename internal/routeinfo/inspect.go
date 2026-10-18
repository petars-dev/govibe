package routeinfo

import (
	"encoding/json"
	"fmt"
)

// InspectSchema versions the document `inspect --json` prints. The tools that read it
// refuse a schema they do not know.
const InspectSchema = "govibe.inspect/1"

// Inspect is the offline facts of an app: its routes and commands as the running
// binary reports them, plus what a reader needs to place a handler's source file
// (the module table, the build flags, a hint to the compile-time root). It holds
// nothing else resolved from the environment: no config values (but the scheduler's
// own, below), no database schema. The
// app binary writes it; the tool reads it (internal/codegraph turns it into what the
// indexer takes).
type Inspect struct {
	Schema    string         `json:"schema"`
	App       InspectApp     `json:"app"`
	Framework InspectModule  `json:"framework"`
	Go        string         `json:"go"`
	Build     InspectBuild   `json:"build"`
	Routes    []InspectRoute `json:"routes"`
	Commands  []InspectCmd   `json:"commands"`
	// Scheduler is the scheduler's own settings: whether it runs, in which zone a
	// schedule is read, and how instances are kept from running a task twice. The one
	// part of the app's configuration the document carries, because a command's next
	// run depends on it.
	Scheduler InspectScheduler `json:"scheduler"`
}

// InspectScheduler is the scheduler.* configuration as the binary read it.
type InspectScheduler struct {
	Enabled  bool   `json:"enabled"`
	Timezone string `json:"timezone"`
	Lock     string `json:"lock"`
}

// InspectContributes says which fields of its App declaration an application filled in:
// what it contributes to the framework, as the declaration is the one place an agent
// looks to find out.
type InspectContributes struct {
	Config     bool `json:"config"`
	Migrations bool `json:"migrations"`
	Assets     bool `json:"assets"`
	Routes     bool `json:"routes"`
	Commands   bool `json:"commands"`
	Shutdown   bool `json:"shutdown"`
	// DevTools is true when the binary that answers has an App.DevTools: a govibe_dev
	// build with the app's dev file compiled in. A default build reports false.
	DevTools bool `json:"dev_tools"`
}

type InspectApp struct {
	Name        string             `json:"name"`
	Module      string             `json:"module"`
	Contributes InspectContributes `json:"contributes"`
	// RootHint is where the app's Routes function was compiled: with its import
	// path it recovers the compile-time root of the module, whatever -trimpath,
	// GOMODCACHE or `replace` made of the file spelling.
	RootHint InspectRef `json:"root_hint"`
}

type InspectModule struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

type InspectBuild struct {
	Trimpath bool   `json:"trimpath"`
	Tags     string `json:"tags,omitempty"`
	// Modules is the build's dependency table (debug.ReadBuildInfo): with it a
	// source path maps to a module exactly.
	Modules []BuildModule `json:"modules"`
}

// BuildModule is one dependency of the binary.
type BuildModule struct {
	Path       string `json:"path"`
	Version    string `json:"version"`
	ReplaceDir string `json:"replace_dir,omitempty"` // where a local `replace` points, as written in go.mod
}

// InspectRef is a function as the Go runtime describes it.
type InspectRef struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Line   int    `json:"line"`
}

// Ref converts to the runtime's own description.
func (r InspectRef) Ref() Ref { return Ref{Symbol: r.Symbol, File: r.File, Line: r.Line} }

// Mounted-by values of a route.
const (
	MountedByFramework = "framework"
	MountedByApp       = "app"
)

type InspectRoute struct {
	Method  string     `json:"method"`
	Path    string     `json:"path"`
	Handler InspectRef `json:"handler"`
	// MountedBy says who registered the route: "framework" (before the app's Routes
	// ran) or "app". It is not the handler's origin: an app can mount a framework
	// handler and the framework mounts stdlib ones.
	MountedBy  string       `json:"mounted_by"`
	Middleware []InspectRef `json:"middleware,omitempty"`
}

type InspectCmd struct {
	Name    string     `json:"name"`
	Short   string     `json:"short"`
	Handler InspectRef `json:"handler"`
	// Schedule is set when the command declares one.
	Schedule *InspectSchedule `json:"schedule,omitempty"`
}

// InspectSchedule is a command's Schedule, validated, with the zone it is read in.
type InspectSchedule struct {
	Spec     string `json:"spec"`
	Timezone string `json:"timezone"`
	Overlap  string `json:"overlap"` // "skip" or "allow"
	Timeout  string `json:"timeout,omitempty"`
	// Every is the interval of an "@every" spec.
	Every string `json:"every,omitempty"`
}

// ParseInspect reads the document `inspect --json` printed, refusing one whose schema
// this version does not know.
func ParseInspect(data []byte) (*Inspect, error) {
	var doc Inspect
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("inspect document: %w", err)
	}
	if doc.Schema != InspectSchema {
		return nil, fmt.Errorf("inspect document has schema %q, this tool reads %q: build the tool and the app from the same framework version", doc.Schema, InspectSchema)
	}
	return &doc, nil
}

// RouteTable is the document's routes as the router walk reports them.
func (d *Inspect) RouteTable() []Route {
	out := make([]Route, 0, len(d.Routes))
	for _, r := range d.Routes {
		rt := Route{Method: r.Method, Path: r.Path, Handler: r.Handler.Ref()}
		for _, m := range r.Middleware {
			rt.MW = append(rt.MW, m.Ref())
		}
		out = append(out, rt)
	}
	return Sort(out)
}
