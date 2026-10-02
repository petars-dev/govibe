package controllers

import (
	"encoding/json"
	"net/http"
	"petars-dev/govibe/business/actions"
)

type helloResponse struct {
	Message string `json:"message"`
}

// HelloAPI returns a JSON greeting.
func HelloAPI(w http.ResponseWriter, r *http.Request) {
	resp := helloResponse{Message: actions.Hello()}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
