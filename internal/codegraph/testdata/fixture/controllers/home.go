package controllers

import (
	"net/http"

	"example.com/fixture/assets/templates"
)

// HomePage renders the landing page template.
func HomePage(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte(templates.Home()))
}
