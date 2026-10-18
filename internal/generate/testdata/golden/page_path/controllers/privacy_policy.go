package controllers

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/acme/shop/assets/templates"
)

// PrivacyPolicy renders the privacy policy page (GET /legal/privacy).
func PrivacyPolicy(w http.ResponseWriter, r *http.Request) {
	templ.Handler(templates.PrivacyPolicy()).ServeHTTP(w, r)
}
