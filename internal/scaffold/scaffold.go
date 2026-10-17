// Package scaffold renders a new GoVibe application: the tree `govibe new` writes.
//
// The templates are embedded, so the scaffold's version is the framework's, it works
// offline and it is testable. They render with the delimiters "[[" and "]]", so the
// templ "{ }" and the Go-template "{{ }}" in generated files need no escaping. Two sets
// make up an app:
//
//   - templates/demo is the Hello GoVibe demo, copied from examples/hello by `go
//     generate ./internal/scaffold` with the module path templated (go:embed cannot
//     reach a nested module); a test fails when the copy is stale, so the demo a user
//     gets is the demo the framework runs and gates;
//   - templates/app is everything else: the settings, the docs, the Makefile, the
//     workflow and the rest, written here.
//
// go.mod, SKILL.md and govibe.yml are rendered by code (they depend on versions, on the
// rules tables and on the hashes of the other files). A file named *.tmpl is a template
// and loses the suffix; any other file (an image) is copied as it is.
package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"io/fs"
	"path"
	"sort"
	"strings"
	"text/template"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

//go:generate go run ./gen

//go:embed all:templates
var templates embed.FS

// The versions a generated app pins. The `go` line is the framework's own (a test checks
// it against go.mod), and so is templ's; air is pinned at the last release that keeps that
// `go` line (a newer air or templ raises it, and `go get -tool x@latest` would do so
// silently). The framework itself is required at the tool's version.
const (
	GoVersion    = "1.25.0"
	TemplVersion = "v0.3.1020"
	AirVersion   = "v1.67.0"
	ChiVersion   = "v5.1.0"

	TemplModule = "github.com/a-h/templ"
	AirModule   = "github.com/air-verse/air"
	ChiModule   = "github.com/go-chi/chi/v5"
)

// The database variants of `govibe new --db`.
const (
	DBNone    = "none"
	DBMariaDB = "mariadb"
)

// Delimiters of the templates.
const (
	LeftDelim  = "[["
	RightDelim = "]]"
)

// Options say what to render.
type Options struct {
	// Module is the app's module path, "github.com/acme/shop".
	Module string
	// Name is the app's name (App.Name, the binary, the directory): the last element of
	// the module path unless set.
	Name string
	// DB is DBNone (the default) or DBMariaDB.
	DB string
	// FrameworkModule is the framework's module path.
	FrameworkModule string
	// FrameworkVersion is the version of the framework the app requires: the tool's own.
	// With Replace it is "v0.0.0", the placeholder a replaced module carries.
	FrameworkVersion string
	// Replace, when set, is the absolute path of a checkout of the framework the app's
	// go.mod points at instead of a published version (for contributors).
	Replace string
}

// Data is what the templates see.
type Data struct {
	Options
	GoVersion    string
	TemplVersion string
	AirVersion   string
	// MariaDB is true for the DBMariaDB variant.
	MariaDB bool
	// DemoDeletable is the list of demo files to delete to start an app of one's own,
	// each in backticks, for the seeded SKILL.md.
	DemoDeletable string
}

// Complete fills the defaults and checks the options. It is called by Render.
func (o *Options) Complete() error {
	// CheckImportPath, not CheckPath: a local module may have no dot in its first element
	// ("shop"), as `go mod init` allows.
	if err := module.CheckImportPath(o.Module); err != nil {
		return fmt.Errorf("module path %q: %w", o.Module, err)
	}
	if o.Name == "" {
		o.Name = NameOf(o.Module)
	}
	if err := checkName(o.Name); err != nil {
		return err
	}
	switch o.DB {
	case "":
		o.DB = DBNone
	case DBNone, DBMariaDB:
	default:
		return fmt.Errorf("--db %q: use %s or %s", o.DB, DBNone, DBMariaDB)
	}
	if o.FrameworkModule == "" {
		return errors.New("the framework module is not set")
	}
	if o.Replace != "" {
		if !path.IsAbs(strings.ReplaceAll(o.Replace, "\\", "/")) && !(len(o.Replace) > 1 && o.Replace[1] == ':') {
			return fmt.Errorf("--replace %q: the path must be absolute, so the app builds from any directory", o.Replace)
		}
		o.FrameworkVersion = "v0.0.0"
	}
	if o.FrameworkVersion == "" || !semver.IsValid(o.FrameworkVersion) {
		return fmt.Errorf("the framework version %q is not a version the app can require", o.FrameworkVersion)
	}
	return nil
}

// NameOf is the app name a module path implies: its last element, without a major
// version suffix ("github.com/acme/shop/v2" is "shop").
func NameOf(modulePath string) string {
	prefix, _, ok := module.SplitPathVersion(modulePath)
	if !ok {
		prefix = modulePath
	}
	return path.Base(prefix)
}

// checkName accepts a name that works as a directory, a binary and a Go string.
func checkName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\ "'`+"\t\n") {
		return fmt.Errorf("app name %q: use a plain directory name", name)
	}
	for _, r := range name {
		if !(r == '-' || r == '_' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Errorf("app name %q: use letters, digits, '-', '_' and '.'", name)
		}
	}
	return nil
}

// DataFor is what the templates see for o (which must be Complete).
func DataFor(o Options) Data {
	var files []string
	for _, f := range DemoDeletable() {
		files = append(files, "`"+f+"`")
	}
	return Data{Options: o, GoVersion: GoVersion, TemplVersion: TemplVersion, AirVersion: AirVersion, MariaDB: o.DB == DBMariaDB,
		DemoDeletable: strings.Join(files, ", ")}
}

// Tree is a rendered app: slash-separated paths, relative to the app root, to contents.
type Tree map[string][]byte

// Paths lists the tree's paths, sorted.
func (t Tree) Paths() []string {
	out := make([]string, 0, len(t))
	for p := range t {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Render renders a whole new application for o.
func Render(o Options) (Tree, error) {
	if err := o.Complete(); err != nil {
		return nil, err
	}
	d := DataFor(o)
	tree := Tree{}
	for _, set := range []string{"templates/demo", "templates/app"} {
		sub, err := fs.Sub(templates, set)
		if err != nil {
			return nil, err
		}
		if err := RenderFS(sub, d, tree); err != nil {
			return nil, fmt.Errorf("%s: %w", set, err)
		}
	}
	tree["go.mod"] = []byte(GoMod(o))
	skill, err := Skill(d)
	if err != nil {
		return nil, err
	}
	tree["SKILL.md"] = skill
	manifest, err := Manifest(o, tree)
	if err != nil {
		return nil, err
	}
	tree["govibe.yml"] = manifest
	return tree, nil
}

// RenderFS renders every file of fsys into tree: a *.tmpl file is executed with d and
// loses the suffix, any other file is copied. A Go file is formatted (the module path
// changes the order of imports). A path the tree already has is an error: the sets do
// not overlap.
func RenderFS(fsys fs.FS, d Data, tree Tree) error {
	return fs.WalkDir(fsys, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out := p
		if strings.HasSuffix(p, ".tmpl") {
			out = strings.TrimSuffix(p, ".tmpl")
			if b, err = Execute(p, b, d); err != nil {
				return err
			}
		}
		if strings.HasSuffix(out, ".go") && !strings.HasSuffix(out, "_templ.go") {
			if b, err = format.Source(b); err != nil {
				return fmt.Errorf("%s: the rendered Go does not parse: %w", out, err)
			}
		}
		if _, dup := tree[out]; dup {
			return fmt.Errorf("%s is rendered twice", out)
		}
		tree[out] = b
		return nil
	})
}

// Execute renders one template.
func Execute(name string, text []byte, d Data) ([]byte, error) {
	t, err := template.New(name).Delims(LeftDelim, RightDelim).Option("missingkey=error").Parse(string(text))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Demo lists the files of the Hello GoVibe demo as a generated app has them: what to
// delete to start an app of one's own. (The .gitkeep files stay: the embed patterns in
// assets/embed.go need something to match.)
func Demo() []string {
	var out []string
	_ = fs.WalkDir(templates, "templates/demo", func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, "templates/demo/"), ".tmpl")
		switch {
		case strings.HasSuffix(rel, ".gitkeep"), strings.HasSuffix(rel, "_templ.go"):
			return nil
		}
		out = append(out, rel)
		return nil
	})
	sort.Strings(out)
	return out
}
