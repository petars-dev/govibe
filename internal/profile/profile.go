// Package profile says where a project keeps what the tools read: its settings
// file, its migrations, its docs, its graph store, and which directory belongs to
// which architecture layer. The indexer, the dev docs pages and the doctor take
// these from a Profile instead of from constants, so a project that lays itself out
// differently only has to say so in govibe.yml.
//
// The default profile is the layout of a generated app (SKILL.md). govibe.yml sits
// in the project root; every key is optional and overrides the default for that key
// alone. Unknown keys are an error: a typo must not silently leave the default in
// force.
package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is the name of the project manifest in the project root.
const File = "govibe.yml"

// Layers is the closed set of architecture layers a package can belong to.
var Layers = []string{"controllers", "business", "storage", "middleware", "commands", "wiring", "dev", "views", "support"}

// Rule settings in govibe.yml: a rule is switched off, or given a severity.
const (
	RuleOff     = "off"
	RuleError   = "error"
	RuleWarning = "warning"
	RuleInfo    = "info"
)

// Profile is where a project keeps things. The zero value is not meant to be
// used: start from Default or Load.
type Profile struct {
	// Framework is the module path of the framework the project is built on; empty is
	// this repository's own module path (FrameworkModule). The tools use it to tell what the
	// project takes from the framework from what it takes from a library.
	Framework string `yaml:"framework"`
	// Config lists the settings files, lowest layer first; a key declared again by a
	// later file replaces the earlier declaration. A file that is absent is skipped.
	Config []string `yaml:"config"`
	// Migrations is the directory of goose migrations, "" for a project with none.
	Migrations string `yaml:"migrations"`
	Docs       Docs   `yaml:"docs"`
	Graph      Graph  `yaml:"graph"`
	// Layout maps an architecture layer to the directories whose packages belong to it
	// (a directory and everything below it; "." is the root package alone). The most
	// specific directory wins; a package no entry claims is `support`. An entry for a
	// layer replaces the default entry of that layer.
	Layout map[string][]string `yaml:"layout"`
	// Rules switches a rule off or changes its severity: off, error, warning or info.
	Rules map[string]string `yaml:"rules"`

	// Source is the file the profile was read from, relative to the root, or "" when
	// it is the default (there is no govibe.yml). Not part of the file.
	Source string `yaml:"-"`
}

// Docs says where the project's documentation lives.
type Docs struct {
	Dirs       []string `yaml:"dirs"`        // directories of public docs: every .md directly in them is a doc
	Specs      string   `yaml:"specs"`       // the directory of specs ("" for none)
	SpecPrefix string   `yaml:"spec_prefix"` // a file in Specs with this prefix is a spec
	Root       []string `yaml:"root"`        // docs at the project root
}

// Graph says where the graph store lives and which paths a clone may lack.
type Graph struct {
	Dir string `yaml:"dir"` // the store's directory
	// Derived are globs of files that are generated, ignored by git and absent from a
	// clone: a document that names one is not stale for that reason.
	Derived []string `yaml:"derived"`
}

// Default is the profile of a generated app.
func Default() Profile {
	return Profile{
		Config:     []string{"env.yml"},
		Migrations: "storage/migrations/sql",
		Docs: Docs{
			Dirs:       []string{"docs/public"},
			Specs:      "docs/specs",
			SpecPrefix: "SPEC_",
			Root:       []string{"README.md", "SKILL.md"},
		},
		Graph: Graph{Dir: "docs/graph", Derived: []string{"docs/graph/*.bolt"}},
		Layout: map[string][]string{
			"wiring":      {".", "routes", "cmd"},
			"controllers": {"controllers"},
			"business":    {"business"},
			"storage":     {"storage"},
			"middleware":  {"middleware"},
			"commands":    {"commands"},
			"views":       {"assets"},
		},
		Rules: map[string]string{},
	}
}

// Load reads <root>/govibe.yml over the default profile. A project with no
// govibe.yml gets the default; one with a malformed or inconsistent file gets an
// error that names the file and the key.
func Load(root string) (Profile, error) {
	p := Default()
	b, err := os.ReadFile(filepath.Join(root, File))
	if errors.Is(err, fs.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return Profile{}, fmt.Errorf("%s: %w", File, err)
	}
	if err := p.Merge(b); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", File, err)
	}
	p.Source = File
	return p, nil
}

// Merge applies a govibe.yml document over p and checks the result.
func (p *Profile) Merge(doc []byte) error {
	// A key with no value (`layout:` and nothing under it, or only commented-out entries) is
	// absent, not an instruction to empty the default: yaml.v3 would decode it as null and
	// replace the default with nothing. Such keys are dropped before decoding.
	var root yaml.Node
	if err := yaml.Unmarshal(doc, &root); err != nil {
		return err
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		pruneNulls(root.Content[0])
		pruned, err := yaml.Marshal(root.Content[0])
		if err != nil {
			return err
		}
		doc = pruned
	}
	dec := yaml.NewDecoder(bytes.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(p); err != nil && !errors.Is(err, io.EOF) { // an empty or comment-only file changes nothing
		return err
	}
	return p.Validate()
}

// pruneNulls removes the mapping entries whose value is null, at every depth.
func pruneNulls(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	kept := n.Content[:0]
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		pruneNulls(v)
		kept = append(kept, k, v)
	}
	n.Content = kept
}

// Validate checks that every path stays inside the project and every name is known.
func (p *Profile) Validate() error {
	paths := []struct {
		key      string
		list     []string
		optional bool // "" means none
	}{
		{"config", p.Config, false},
		{"migrations", []string{p.Migrations}, true},
		{"docs.dirs", p.Docs.Dirs, false},
		{"docs.specs", []string{p.Docs.Specs}, true},
		{"docs.root", p.Docs.Root, false},
		{"graph.dir", []string{p.Graph.Dir}, false},
	}
	for _, e := range paths {
		for _, v := range e.list {
			if v == "" && e.optional {
				continue
			}
			if err := checkRel(v); err != nil {
				return fmt.Errorf("%s: %q %w", e.key, v, err)
			}
		}
	}
	for _, g := range p.Graph.Derived {
		if _, err := path.Match(g, "x"); err != nil {
			return fmt.Errorf("graph.derived: %q is not a glob: %w", g, err)
		}
		if err := checkRel(strings.NewReplacer("*", "x", "?", "x", "[", "x", "]", "x").Replace(g)); err != nil {
			return fmt.Errorf("graph.derived: %q %w", g, err)
		}
	}
	if strings.ContainsAny(p.Framework, " \t\\") || strings.HasPrefix(p.Framework, "/") || strings.HasSuffix(p.Framework, "/") {
		return fmt.Errorf("framework: %q is not a module path", p.Framework)
	}
	claimedBy := map[string]string{}
	layers := make([]string, 0, len(p.Layout))
	for layer := range p.Layout {
		layers = append(layers, layer)
	}
	sort.Strings(layers)
	for _, layer := range layers {
		if !validLayer(layer) {
			return fmt.Errorf("layout: %q is not an architecture layer (%s)", layer, strings.Join(Layers, ", "))
		}
		for _, d := range p.Layout[layer] {
			if err := checkRel(d); err != nil {
				return fmt.Errorf("layout.%s: %q %w", layer, d, err)
			}
			d = strings.TrimSuffix(d, "/")
			if other, dup := claimedBy[d]; dup && other != layer {
				return fmt.Errorf("layout: %q is claimed by both %s and %s", d, other, layer)
			}
			claimedBy[d] = layer
		}
	}
	for rule, v := range p.Rules {
		switch v {
		case RuleOff, RuleError, RuleWarning, RuleInfo:
		default:
			return fmt.Errorf("rules.%s: %q is not off, error, warning or info", rule, v)
		}
	}
	return nil
}

func validLayer(l string) bool {
	for _, x := range Layers {
		if x == l {
			return true
		}
	}
	return false
}

// checkRel accepts a clean, relative, slash-separated path that stays in the project.
func checkRel(p string) error {
	switch {
	case p == "":
		return errors.New("is empty")
	case strings.Contains(p, `\`):
		return errors.New("uses a backslash: write paths with '/'")
	case path.IsAbs(p) || filepath.IsAbs(p):
		return errors.New("is absolute: paths are relative to the project root")
	}
	if c := path.Clean(p); c != p && c != strings.TrimSuffix(p, "/") {
		return fmt.Errorf("is not a clean path (%s)", c)
	}
	if p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return errors.New("leaves the project")
	}
	return nil
}

// Classify maps a package directory (relative to the root, "." for the root) to its
// architecture layer: the layer whose Layout entry is the longest match.
func (p *Profile) Classify(dir string) string {
	best, bestLen := "support", -1
	layers := make([]string, 0, len(p.Layout))
	for l := range p.Layout {
		layers = append(layers, l)
	}
	sort.Strings(layers) // equal matches resolve the same way every time
	for _, layer := range layers {
		for _, d := range p.Layout[layer] {
			n := matchDir(d, dir)
			if n > bestLen {
				best, bestLen = layer, n
			}
		}
	}
	return best
}

// matchDir returns how specifically entry claims dir: the length of the entry, or -1
// when it does not. "." claims the root package alone.
func matchDir(entry, dir string) int {
	entry = strings.TrimSuffix(entry, "/")
	switch {
	case entry == ".":
		if dir == "." {
			return 0
		}
		return -1
	case dir == entry || strings.HasPrefix(dir, entry+"/"):
		return len(entry) + 1
	}
	return -1
}

// IsDerived reports whether a project-relative path is generated, ignored and absent
// from a clone, so that a document naming it is not stale for being absent.
func (p *Profile) IsDerived(rel string) bool {
	for _, g := range p.Graph.Derived {
		if ok, _ := path.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// FrameworkModule is the module path of the framework the project is built on.
func (p *Profile) FrameworkModule() string {
	if p.Framework != "" {
		return p.Framework
	}
	return FrameworkModule()
}

// ScanDirs are the directories the docs layer reads, public docs first.
func (p *Profile) ScanDirs() []string {
	dirs := append([]string(nil), p.Docs.Dirs...)
	if p.Docs.Specs != "" {
		dirs = append(dirs, p.Docs.Specs)
	}
	return dirs
}

// RuleSetting returns the setting of a rule: its value and whether the file sets
// one at all.
func (p *Profile) RuleSetting(rule string) (string, bool) {
	v, ok := p.Rules[rule]
	return v, ok
}

// FrameworkModule is the module path of the framework, derived from this package's
// own import path so that a rename cannot leave it behind.
func FrameworkModule() string {
	return strings.TrimSuffix(reflect.TypeOf(Profile{}).PkgPath(), "/internal/profile")
}
