package controllers

import (
	"github.com/petars-dev/govibe/examples/hello/assets/templates"
	"net/http"

	"github.com/a-h/templ"
)

// Home renders the default landing page.
func Home(w http.ResponseWriter, r *http.Request) {
	h := templates.Home()
	templ.Handler(h).ServeHTTP(w, r)
}
