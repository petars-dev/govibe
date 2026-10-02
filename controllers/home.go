package controllers

import (
    "net/http"
    "github.com/a-h/templ"
    "trace-loop/os/assets/templates"
)

// Home renders the default landing page.
func Home(w http.ResponseWriter, r *http.Request) {
    h := templates.Home()
    templ.Handler(h).ServeHTTP(w, r)
}
