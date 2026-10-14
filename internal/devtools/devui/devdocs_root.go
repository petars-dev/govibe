package devui

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/petars-dev/govibe/internal/frameworkdocs"
)

// Docs says where the docs pages find what they show: the application's own docs and specs
// (by the project's profile) come first; the framework's reference docs are a separate,
// muted group under /dev/docs/govibe, every title prefixed "GoVibe:".
type Docs struct {
	// Root is the project root the directories below are under; "" is the working directory.
	Root string
	// Dirs are the directories of public docs (the first is the one /dev/docs/app opens on),
	// Specs the directory of specs and SpecPrefix the prefix that makes a file in it a spec.
	// The zero values are a generated app's layout: docs/public, docs/specs, SPEC_.
	Dirs       []string
	Specs      string
	SpecPrefix string
	// Framework is the framework's curated reference docs; nil shows none.
	Framework frameworkdocs.Set
	// FromProfile says the directories above come from the project's profile, so an empty one
	// means none (a project with no specs directory has no specs), not "the default".
	FromProfile bool
	// FrameworkSpecs is the framework's development specs, listed only for a contributor who
	// pointed the server at a checkout (dev.framework_src); nil, always, in an application.
	FrameworkSpecs frameworkdocs.Set
}

type docsKey struct{}

// withDefaults fills the layout of a generated app where d says nothing.
func (d Docs) withDefaults() Docs {
	if d.FromProfile {
		return d
	}
	if len(d.Dirs) == 0 {
		d.Dirs = []string{"docs/public"}
	}
	if d.Specs == "" {
		d.Specs = "docs/specs"
	}
	if d.SpecPrefix == "" {
		d.SpecPrefix = "SPEC_"
	}
	return d
}

// DocsFor is middleware that tells the docs pages where to read from. Without it they
// read docs/public and docs/specs relative to the working directory, as they always have.
func DocsFor(d Docs) func(http.Handler) http.Handler {
	d = d.withDefaults()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), docsKey{}, d)))
		})
	}
}

// DocsRoot is DocsFor with the default layout under root: the docs pages read docs/public
// and docs/specs from there and not from the working directory the process happened to
// start in.
func DocsRoot(root string) func(http.Handler) http.Handler { return DocsFor(Docs{Root: root}) }

// docsOf is the docs configuration of a request.
func docsOf(r *http.Request) Docs {
	if d, ok := r.Context().Value(docsKey{}).(Docs); ok {
		return d
	}
	return Docs{}.withDefaults()
}

// docsDir is the directory rel (such as docs/public) under the project root.
func (d Docs) docsDir(rel string) string {
	if d.Root != "" {
		return filepath.Join(d.Root, filepath.FromSlash(rel))
	}
	return rel
}
