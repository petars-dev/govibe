package controllers

import (
	"net/http"

	"example.com/fixture/business/actions"
)

// HelloAPI returns a JSON greeting. It reaches no table.
func HelloAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"message": actions.Hello()})
}
