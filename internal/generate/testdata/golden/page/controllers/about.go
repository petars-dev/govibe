package controllers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/acme/shop/assets/templates"
)

// About renders the about page (GET /about).
func About(w http.ResponseWriter, r *http.Request) {
	templ.Handler(templates.About()).ServeHTTP(w, r)
}
