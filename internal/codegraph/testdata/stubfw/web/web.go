// Package web holds the helpers controllers and views share with the framework.
package web

import (
	"encoding/json"
	"net/http"
)

// JSON writes v as JSON.
func JSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// HTMXScript is the tag that loads the vendored htmx.
func HTMXScript() string { return `<script src="/_stub/htmx.min.js"></script>` }
