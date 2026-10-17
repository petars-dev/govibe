package scaffold

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

// DemoSource is the module path of the reference app the demo is synced from.
func DemoSource(frameworkModule string) string { return frameworkModule + "/examples/hello" }

// DemoName is the reference app's name, as its main.go declares it.
const DemoName = "hello"

// syncSkip says which files of the reference app are not part of the demo: the module
// files, what the scaffold writes itself (the settings, the docs, the readme, the rules),
// and what a checkout accumulates.
func syncSkip(rel string) bool {
	switch rel {
	case "go.mod", "go.sum", "README.md", "SKILL.md", "env.yml", "govibe.yml", ".gitignore":
		return true
	}
	base := path.Base(rel)
	switch {
	case strings.HasPrefix(rel, "docs/public/"), strings.HasPrefix(rel, "tmp/"), strings.HasPrefix(rel, ".git/"):
		return true
	case base == ".DS_Store", strings.HasSuffix(base, ".bolt"), strings.HasSuffix(base, ".lock"):
		return true
	}
	return false
}

// SyncDemo reads the reference app (its root as fsys) and returns the demo template set
// as templates/demo should hold it: a text file with the module path and the name
// templated, as <path>.tmpl; a binary file as it is. A text file that holds a delimiter
// is an error: it could not be templated without escaping.
func SyncDemo(fsys fs.FS, frameworkModule string) (map[string][]byte, error) {
	src := DemoSource(frameworkModule)
	out := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if p != "." && syncSkip(p+"/") {
				return fs.SkipDir
			}
			return nil
		}
		if syncSkip(p) {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
			out[p] = b // an image: copied as it is
			return nil
		}
		if bytes.Contains(b, []byte(LeftDelim)) || bytes.Contains(b, []byte(RightDelim)) {
			return fmt.Errorf("%s contains the template delimiter %s or %s", p, LeftDelim, RightDelim)
		}
		text := strings.ReplaceAll(string(b), src, LeftDelim+".Module"+RightDelim)
		if p == "main.go" {
			name := fmt.Sprintf("Name:%s%q,", strings.Repeat(" ", 7), DemoName)
			if !strings.Contains(text, name) {
				return fmt.Errorf("main.go does not declare %s, which the sync templates", name)
			}
			text = strings.Replace(text, name, "Name:       \""+LeftDelim+".Name"+RightDelim+"\",", 1)
		}
		out[p+".tmpl"] = []byte(text)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, must := range []string{"main.go.tmpl", "main_test.go.tmpl", "routes/routes.go.tmpl", "routes/console.go.tmpl", "assets/embed.go.tmpl", "assets/templates/home.templ.tmpl", "docs/graph/baseline.json.tmpl"} {
		if _, ok := out[must]; !ok {
			return nil, fmt.Errorf("the reference app has no %s", strings.TrimSuffix(must, ".tmpl"))
		}
	}
	return out, nil
}

// EmbeddedDemo is the demo template set as embedded, for the test that compares it with
// the reference app.
func EmbeddedDemo() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(templates, "templates/demo", func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		b, err := fs.ReadFile(templates, p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "templates/demo/")] = b
		return nil
	})
	return out, err
}

// DemoDeletable lists the demo files to delete to start an app of one's own: the
// controllers, the action, the page and its static files. The rest of the demo
// (main.go, the tests, the routes files, the embeds, the migration, the baseline) stays
// and is edited instead.
func DemoDeletable() []string {
	var out []string
	for _, f := range Demo() {
		switch {
		case strings.HasPrefix(f, "controllers/"), strings.HasPrefix(f, "business/"), strings.HasPrefix(f, "assets/templates/"), strings.HasPrefix(f, "assets/static/"):
			out = append(out, f)
		}
	}
	return out
}
