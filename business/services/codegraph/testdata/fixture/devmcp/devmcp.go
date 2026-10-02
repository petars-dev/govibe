// Package devmcp stands in for the dev-only tooling package.
package devmcp

import (
	"net/http"

	"example.com/fixture/storage/orderstore"
)

// NewHandler returns the dev tooling handler. Dev packages may import storage.
func NewHandler(store *orderstore.Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("dev tools"))
	})
}
