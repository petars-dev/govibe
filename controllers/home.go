package controllers

import (
	"net/http"
	"petars-dev/govibe/assets/templates"

	"github.com/a-h/templ"
)

// Home renders the default landing page.
func Home(w http.ResponseWriter, r *http.Request) {
	h := templates.Home()
	templ.Handler(h).ServeHTTP(w, r)
}
