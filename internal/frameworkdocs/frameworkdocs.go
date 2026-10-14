// Package frameworkdocs is the framework's own reference docs, as an application sees
// them: a curated, embedded allowlist (the guide, the configuration, the architecture, the
// graph vocabulary, the database explorer, the rules, the upgrade notes), every title
// prefixed "GoVibe:", under a slug namespace of their own so they cannot collide with the
// application's docs. The framework's development specs (SPEC_*.md) are not in the set and
// are never embedded: a test fails if one is.
//
// The copies in docs/ are generated from the repository's docs/public (go generate); a test
// fails when one is stale. A contributor can serve the set from a checkout instead
// (FromDisk), and list that checkout's specs (ContributorSpecs).
package frameworkdocs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/petars-dev/govibe/internal/rules"
)

//go:generate go run ./gen

//go:embed docs/*.md
var embedded embed.FS

// Prefix labels every title, so the framework's docs are never taken for the app's.
const Prefix = "GoVibe: "

// Doc is one doc of the set.
type Doc struct {
	Slug  string // its name under /dev/docs/govibe/ and govibe://docs/
	Title string // with the prefix
}

// Set is a set of framework docs.
type Set interface {
	// List is every doc, in the order the panel lists them.
	List() []Doc
	// Read returns the markdown of the doc with the slug.
	Read(slug string) (string, error)
}

// entry is one doc of the allowlist.
type entry struct {
	slug, title string
	file        string // in docs/public of the repository, and in docs/ here; "" for a generated doc
}

// The allowlist. A name is here or it is not shipped: nothing is picked up by listing a directory.
var allowed = []entry{
	{"guide", "Guide", "GUIDE.md"},
	{"configuration", "Configuration", "CONFIGURATION.md"},
	{"commands-and-scheduler", "Commands and Scheduler", "COMMANDS_AND_SCHEDULER.md"},
	{"architecture", "Architecture", "ARCHITECTURE.md"},
	{"graph-schema", "Graph Schema", "GRAPH_SCHEMA.md"},
	{"dev-db-explorer", "Dev DB Explorer", "DEV_DB_EXPLORER.md"},
	{"skill", "Rules", ""}, // rendered from the rules tables
	{"upgrading", "Upgrading", "UPGRADING.md"},
}

// Markers of a block that is for the framework's contributors only: it stays in docs/public (the
// repository's own docs) and is removed from the copy an application serves, so an app never reads
// about the framework's test fixtures or its internals.
const (
	frameworkOnlyStart = "<!-- framework-only:start -->"
	frameworkOnlyEnd   = "<!-- framework-only:end -->"
)

// Shipped is a doc of docs/public as an application sees it: without its framework-only blocks.
// An unbalanced marker is an error, so a block cannot swallow the rest of a doc by accident.
func Shipped(doc []byte) ([]byte, error) {
	text := string(doc)
	for {
		i := strings.Index(text, frameworkOnlyStart)
		if i < 0 {
			if strings.Contains(text, frameworkOnlyEnd) {
				return nil, fmt.Errorf("%s without a %s before it", frameworkOnlyEnd, frameworkOnlyStart)
			}
			return []byte(text), nil
		}
		j := strings.Index(text[i:], frameworkOnlyEnd)
		if j < 0 {
			return nil, fmt.Errorf("%s without a %s after it", frameworkOnlyStart, frameworkOnlyEnd)
		}
		end := i + j + len(frameworkOnlyEnd)
		text = strings.TrimRight(text[:i], "\n") + "\n" + strings.TrimLeft(text[end:], "\n")
	}
}

// Files are the files of the allowlist, as they are named in docs/public and in docs/: what the generator copies.
func Files() []string {
	var out []string
	for _, e := range allowed {
		if e.file != "" {
			out = append(out, e.file)
		}
	}
	return out
}

// Slugs are the slugs of the allowlist, in order.
func Slugs() []string {
	var out []string
	for _, e := range allowed {
		out = append(out, e.slug)
	}
	return out
}

type set struct {
	read func(file string) ([]byte, error)
}

// Embedded is the set shipped in the binary.
func Embedded() Set {
	return set{read: func(file string) ([]byte, error) { return embedded.ReadFile("docs/" + file) }}
}

// FromDisk is the set read from a checkout of the framework (its docs/public) each time, so
// an edit shows at once. Docs the checkout does not have are left out.
func FromDisk(checkout string) Set {
	return set{read: func(file string) ([]byte, error) {
		return os.ReadFile(filepath.Join(checkout, "docs", "public", file))
	}}
}

func (s set) List() []Doc {
	var out []Doc
	for _, e := range allowed {
		if _, err := s.Read(e.slug); err != nil {
			continue // not written yet, or not in this checkout
		}
		out = append(out, Doc{Slug: e.slug, Title: Prefix + e.title})
	}
	return out
}

func (s set) Read(slug string) (string, error) {
	for _, e := range allowed {
		if e.slug != slug {
			continue
		}
		if e.file == "" {
			return rules.Markdown(), nil
		}
		b, err := s.read(e.file)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("the framework doc %q is not in this set", slug)
			}
			return "", err
		}
		return string(b), nil
	}
	return "", fmt.Errorf("no framework doc %q; the docs are: %s", slug, strings.Join(Slugs(), ", "))
}

// ContributorSpecs lists the development specs of a checkout of the framework (its
// docs/specs/SPEC_*.md), for the contributors' group of the dev docs. They are read from the
// checkout and never embedded.
func ContributorSpecs(checkout string) Set {
	return specs{dir: filepath.Join(checkout, "docs", "specs")}
}

type specs struct{ dir string }

func (s specs) files() []string {
	matches, _ := filepath.Glob(filepath.Join(s.dir, "SPEC_*.md"))
	sort.Strings(matches)
	return matches
}

// specSlug is a spec's name under the dev docs: SPEC_GRAPH_INDEXER.md is "graph-indexer".
func specSlug(file string) string {
	name := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(file), ".md"), "SPEC_")
	return strings.Trim(strings.ToLower(strings.NewReplacer("_", "-", " ", "-").Replace(name)), "-")
}

// specTitle is "Graph Indexer" for SPEC_GRAPH_INDEXER.md.
func specTitle(file string) string {
	name := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(file), ".md"), "SPEC_")
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(name))
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
	}
	return strings.Join(words, " ")
}

func (s specs) List() []Doc {
	var out []Doc
	for _, m := range s.files() {
		out = append(out, Doc{Slug: specSlug(m), Title: specTitle(m)})
	}
	return out
}

func (s specs) Read(slug string) (string, error) {
	for _, m := range s.files() {
		if specSlug(m) == slug {
			b, err := os.ReadFile(m)
			return string(b), err
		}
	}
	return "", fmt.Errorf("no spec %q in %s", slug, s.dir)
}
