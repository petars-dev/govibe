package devui

import (
	"context"
	"net/http"
	"path/filepath"
)

type docsRootKey struct{}

// DocsRoot is middleware that tells the docs pages where the project root is, so
// they read docs/public and docs/specs from there and not from the working
// directory the process happened to start in. Without it they read relative to the
// working directory, as they always have.
func DocsRoot(root string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), docsRootKey{}, root)))
		})
	}
}

// docsDir is the directory rel (such as docs/public) under the project root.
func docsDir(r *http.Request, rel string) string {
	if root, _ := r.Context().Value(docsRootKey{}).(string); root != "" {
		return filepath.Join(root, filepath.FromSlash(rel))
	}
	return rel
}
