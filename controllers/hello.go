package controllers

import (
    "net/http"
    "trace-loop/os/business/actions"
)

// Hello returns a plain text greeting, useful for htmx swaps.
func Hello(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    _, _ = w.Write([]byte(actions.Hello()))
}
