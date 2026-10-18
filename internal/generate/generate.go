// Package generate is `govibe make`: the generators that add a page, a resource, an
// action, a service, a store, a migration, a middleware, a command, a typed config or a
// spec to an application. Each writes files that obey the layering rules and registers
// what it made at a marker comment in the wiring (`// govibe:routes` in routes/routes.go,
// `// govibe:commands` in routes/console.go) and in the docs (under the "Routes",
// "Commands" and "Tables" headings of the app's architecture and schema docs); when a
// marker or a heading is missing it prints the snippet instead of guessing.
//
// A generator reads the project (its go.mod, its profile, its files) and returns a
// Result: the files to create or edit, the snippets that could not be inserted and what
// to do next. It writes nothing itself; the tool applies the result, so a generator is
// testable against a golden tree.
package generate

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/mod/modfile"

	"github.com/petars-dev/govibe/internal/profile"
	"github.com/petars-dev/govibe/internal/scaffold"
)

//go:embed templates/*.tmpl
var templates embed.FS

// The markers the generators insert at.
const (
	RoutesMarker   = "// govibe:routes"
	CommandsMarker = "// govibe:commands"
)

// Project is the application a generator works on.
type Project struct {
	Root      string // the directory holding go.mod
	Module    string // the app's module path
	Name      string // the app's name (App.Name), the binary's name
	Framework string // the framework's module path
	Profile   profile.Profile

	fsys fs.FS
}

// Load reads the project at root: its go.mod (the module path), its main.go (the app's
// name) and its govibe.yml (the profile).
func Load(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p := &Project{Root: abs, fsys: os.DirFS(abs), Framework: profile.FrameworkModule()}
	gomod, err := fs.ReadFile(p.fsys, "go.mod")
	if err != nil {
		return nil, fmt.Errorf("%s is not the root of a Go module (no go.mod): run the generator in the project root, or pass --root", abs)
	}
	mf, err := modfile.Parse("go.mod", gomod, nil)
	if err != nil {
		return nil, fmt.Errorf("go.mod: %w", err)
	}
	if mf.Module == nil || mf.Module.Mod.Path == "" {
		return nil, errors.New("go.mod has no module line")
	}
	p.Module = mf.Module.Mod.Path
	p.Profile, err = profile.Load(abs)
	if err != nil {
		return nil, err
	}
	if p.Profile.Framework != "" {
		p.Framework = p.Profile.Framework
	}
	p.Name = scaffold.AppName(abs, p.Module)
	return p, nil
}

// File is one file a generator creates or edits.
type File struct {
	Path    string // slash-separated, relative to the root
	Content []byte
	// Edit is true when the file exists and is changed (a marker file, a doc); false
	// for a new file, which the tool refuses to overwrite.
	Edit bool
}

// Snippet is text the generator could not insert: where it goes and what it is.
type Snippet struct {
	Where string
	Text  string
}

// Result is what a generator made.
type Result struct {
	Kind     string
	Files    []File
	Snippets []Snippet
	Notes    []string
	// Templ is true when a .templ file was written: `go tool templ generate` must run.
	Templ bool
}

// Paths lists the result's files, sorted.
func (r *Result) Paths() []string {
	var out []string
	for _, f := range r.Files {
		out = append(out, f.Path)
	}
	sort.Strings(out)
	return out
}

func (r *Result) add(p string, content []byte) {
	r.Files = append(r.Files, File{Path: p, Content: content})
}

func (r *Result) edit(p string, content []byte) {
	r.Files = append(r.Files, File{Path: p, Content: content, Edit: true})
}

func (r *Result) snippet(where, text string) {
	r.Snippets = append(r.Snippets, Snippet{Where: where, Text: text})
}

func (r *Result) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// --- reading the project ---

func (p *Project) read(rel string) ([]byte, error) { return fs.ReadFile(p.fsys, rel) }

func (p *Project) exists(rel string) bool {
	_, err := fs.Stat(p.fsys, rel)
	return err == nil
}

// goFilesIn reports whether dir holds a Go file: whether the package exists already
// (its first file carries the package comment).
func (p *Project) goFilesIn(dir string) bool {
	entries, err := fs.ReadDir(p.fsys, dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// layerDir is the directory of an architecture layer: the first entry of the profile's
// layout for it that is not the root, else def.
func (p *Project) layerDir(layer, def string) string {
	for _, d := range p.Profile.Layout[layer] {
		if d != "." && d != "" {
			return strings.TrimSuffix(d, "/")
		}
	}
	return def
}

// Where things go, by layer.
func (p *Project) controllersDir() string { return p.layerDir("controllers", "controllers") }
func (p *Project) businessDir() string    { return p.layerDir("business", "business") }
func (p *Project) storageDir() string     { return p.layerDir("storage", "storage") }
func (p *Project) middlewareDir() string  { return p.layerDir("middleware", "middleware") }
func (p *Project) commandsDir() string    { return p.layerDir("commands", "commands") }
func (p *Project) templatesDir() string   { return p.layerDir("views", "assets") + "/templates" }
func (p *Project) migrationsDir() string  { return p.Profile.Migrations }
func (p *Project) specsDir() string       { return p.Profile.Docs.Specs }

// docsDir is the first public docs directory, "" when the profile lists none.
func (p *Project) docsDir() string {
	if len(p.Profile.Docs.Dirs) == 0 {
		return ""
	}
	return strings.TrimSuffix(p.Profile.Docs.Dirs[0], "/")
}

// MarkerFile finds the Go file of the wiring layer that holds marker: the wiring
// directories (and the root) are searched one level deep. "" when none has it.
func (p *Project) MarkerFile(marker string) (string, []byte) {
	dirs := append([]string(nil), p.Profile.Layout["wiring"]...)
	if len(dirs) == 0 {
		dirs = []string{".", "routes"}
	}
	for _, d := range dirs {
		d = strings.TrimSuffix(d, "/")
		if d == "" {
			d = "."
		}
		entries, err := fs.ReadDir(p.fsys, d)
		if err != nil {
			continue
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			rel := path.Join(d, n)
			b, err := p.read(rel)
			if err != nil {
				continue
			}
			if hasMarker(b, marker) {
				return rel, b
			}
		}
	}
	return "", nil
}

// --- rendering ---

// render executes one embedded template with data. The delimiters are the scaffold's,
// so templ "{ }" and Go-template "{{ }}" in generated files need no escaping.
func render(name string, data any) ([]byte, error) {
	text, err := templates.ReadFile("templates/" + name + ".tmpl")
	if err != nil {
		return nil, err
	}
	t, err := template.New(name).Delims(scaffold.LeftDelim, scaffold.RightDelim).Option("missingkey=error").Parse(string(text))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// renderGo renders a Go file and formats it.
func renderGo(name string, data any) ([]byte, error) {
	b, err := render(name, data)
	if err != nil {
		return nil, err
	}
	out, err := format.Source(b)
	if err != nil {
		return nil, fmt.Errorf("%s: the rendered Go does not parse: %w\n%s", name, err, b)
	}
	return out, nil
}

// refuseExisting is the error for a file the generator would create but which exists.
func (p *Project) refuseExisting(rel string) error {
	if p.exists(rel) {
		return fmt.Errorf("%s exists: remove it, or choose another name (--force overwrites it)", rel)
	}
	return nil
}
