package devmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/petars-dev/govibe/internal/devext"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
	"github.com/petars-dev/govibe/internal/frameworkdocs"
	"github.com/petars-dev/govibe/internal/routeinfo"
	"github.com/petars-dev/govibe/internal/rules"
)

// Options say what the server knows about the application it fronts.
type Options struct {
	// AppName titles the server (App.Name); empty is a plain "GoVibe Dev Tools".
	AppName string
	// Root is the application's root directory, where its SKILL.md is read from.
	Root string
	// Inspect is what the application's binary reports about itself (the document
	// `inspect --json` prints): the source of govibe://app/manifest.
	Inspect *routeinfo.Inspect
	// Docs serves the framework's own reference docs as resources
	// (govibe://docs/<slug>). Nil: none.
	Docs frameworkdocs.Set
	// Schedule reads the scheduler as it is now, for schedule_status. Nil: the process
	// has none, and the tool says so.
	Schedule func() schedview.Status
	// AppTools are the tools the application adds through App.DevTools, already
	// validated (names, no duplicates, none of the framework's families).
	AppTools []devext.Tool
}

const (
	skillURI       = "govibe://skill"
	appManifestURI = "govibe://app/manifest"
	docURIPrefix   = "govibe://docs/"

	maxSkillBytes = 256 << 10
)

// registerAppResources adds what an agent reads to learn the project it is working in:
// the rules, what the App declaration contributes, and the framework's docs.
func registerAppResources(srv *mcp.Server, opt Options) {
	srv.AddResource(&mcp.Resource{
		URI:         skillURI,
		Name:        "skill",
		Title:       "The rules of this project",
		Description: "SKILL.md: the layering, import, config and doc rules this project follows, which the graph's findings enforce. When the project has no SKILL.md, the same rules rendered from the framework's tables.",
		MIMEType:    "text/markdown",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return textResource(req.Params.URI, "text/markdown", skillText(opt.Root)), nil
	})

	if opt.Inspect != nil {
		doc := opt.Inspect
		srv.AddResource(&mcp.Resource{
			URI:         appManifestURI,
			Name:        "app-manifest",
			Title:       "What the App declaration contributes",
			Description: "The application as its own binary reports it: its name and module, the framework version it is built on, which fields of its App declaration it fills in, the routes it registers (not the framework's) and its commands.",
			MIMEType:    "application/json",
		}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			b, err := json.MarshalIndent(appManifestOf(doc), "", "  ")
			if err != nil {
				return nil, err
			}
			return textResource(req.Params.URI, "application/json", string(b)), nil
		})
	}

	if opt.Docs != nil {
		for _, d := range opt.Docs.List() {
			d := d
			srv.AddResource(&mcp.Resource{
				URI:         docURIPrefix + d.Slug,
				Name:        "doc-" + d.Slug,
				Title:       d.Title,
				Description: "A reference doc of the framework this application is built on (not a graph node).",
				MIMEType:    "text/markdown",
			}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				text, err := opt.Docs.Read(d.Slug)
				if err != nil {
					return nil, err
				}
				return textResource(req.Params.URI, "text/markdown", text), nil
			})
		}
	}
}

// skillText is the project's SKILL.md, or the rules rendered from the framework's
// tables when it has none (or it is unreadable).
func skillText(root string) string {
	if root != "" {
		if b, err := os.ReadFile(filepath.Join(root, "SKILL.md")); err == nil && len(b) <= maxSkillBytes {
			return string(b)
		}
	}
	return rules.Markdown()
}

// appManifest is govibe://app/manifest.
type appManifest struct {
	App struct {
		Name   string `json:"name"`
		Module string `json:"module"`
	} `json:"app"`
	Framework   routeinfo.InspectModule      `json:"framework"`
	Contributes routeinfo.InspectContributes `json:"contributes"`
	Routes      []appManifestRoute           `json:"routes"`
	Commands    []appManifestCommand         `json:"commands"`
	// Scheduler is the scheduler's settings as the binary read them: whether it runs, the
	// zone the schedules are read in, and how instances are kept from running a task twice.
	Scheduler routeinfo.InspectScheduler `json:"scheduler"`
	Build     struct {
		Tags     string `json:"tags,omitempty"`
		Trimpath bool   `json:"trimpath"`
	} `json:"build"`
}

type appManifestRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
}

type appManifestCommand struct {
	Name    string `json:"name"`
	Short   string `json:"short"`
	Handler string `json:"handler"`
	// Schedule is set for a command that is on a schedule: the spec, the zone it is read in,
	// the overlap policy and the timeout.
	Schedule *routeinfo.InspectSchedule `json:"schedule,omitempty"`
}

func appManifestOf(doc *routeinfo.Inspect) appManifest {
	var m appManifest
	m.App.Name, m.App.Module = doc.App.Name, doc.App.Module
	m.Framework, m.Contributes = doc.Framework, doc.App.Contributes
	m.Build.Tags, m.Build.Trimpath = doc.Build.Tags, doc.Build.Trimpath
	m.Routes, m.Commands = []appManifestRoute{}, []appManifestCommand{}
	for _, r := range doc.Routes {
		if r.MountedBy == routeinfo.MountedByFramework {
			continue // the framework's own, not what the app contributes
		}
		m.Routes = append(m.Routes, appManifestRoute{Method: r.Method, Path: r.Path, Handler: r.Handler.Symbol})
	}
	sort.Slice(m.Routes, func(i, j int) bool {
		if m.Routes[i].Path != m.Routes[j].Path {
			return m.Routes[i].Path < m.Routes[j].Path
		}
		return m.Routes[i].Method < m.Routes[j].Method
	})
	for _, c := range doc.Commands {
		m.Commands = append(m.Commands, appManifestCommand{Name: c.Name, Short: c.Short, Handler: c.Handler.Symbol, Schedule: c.Schedule})
	}
	m.Scheduler = doc.Scheduler
	return m
}
