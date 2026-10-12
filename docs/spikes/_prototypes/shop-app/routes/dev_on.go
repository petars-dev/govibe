//go:build govibe_dev

package routes

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/shop/business/actions"
	"petars-dev/govibe"
	"petars-dev/govibe/web"
)

// devRoutes exists only in a dev build: an app file the calls layer sees only with -tags govibe_dev.
func devRoutes(rt *govibe.Runtime, r chi.Router) {
	r.Get("/dev/app", func(w http.ResponseWriter, req *http.Request) {
		web.JSON(w, 200, actions.Hello())
	})
}
