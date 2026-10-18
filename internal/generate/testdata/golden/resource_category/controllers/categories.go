package controllers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/acme/shop/business/services/categories"
)

// ListCategories answers GET /api/categories with every category as JSON. It is a thin
// adapter: the service owns the logic and the storage call.
func ListCategories(svc *categories.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	}
}

// GetCategory answers GET /api/categories/{id} with one category as JSON, 404 when there is none.
func GetCategory(svc *categories.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "the id is not a number", http.StatusBadRequest)
			return
		}
		item, err := svc.Get(r.Context(), id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(item)
	}
}
