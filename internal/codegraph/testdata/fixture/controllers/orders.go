package controllers

import (
	"encoding/json"
	"net/http"

	"example.com/fixture/business/services/orders"
	"example.com/fixture/storage/orderstore"
)

// ListOrders returns every order. It follows the layering rule: the handler
// only calls the orders service, which owns the storage call.
func ListOrders(svc *orders.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, list)
	}
}

// GetOrder returns one order. It calls the store directly, which breaks the
// rule that only actions and services touch storage, on purpose, so the
// indexer has a layering violation to report.
func GetOrder(store *orderstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, err := store.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, o)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
