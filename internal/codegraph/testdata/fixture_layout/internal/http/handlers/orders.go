// Package handlers is the HTTP layer of the shop.
package handlers

import (
	"net/http"

	"example.com/shop/internal/core/orders"
	"example.com/shop/internal/store/orderstore"
)

// List goes through the service, as the layering rule asks.
func List(svc *orders.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = svc.All()
	}
}

// Get calls the store directly: a layering violation, on purpose.
func Get(store *orderstore.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = store.One(r.PathValue("id"))
	}
}

// Currency reads a setting through a config value: shop.limit is not declared anywhere.
func Currency(cfg interface{ Get(string) (string, bool) }) (string, string) {
	c, _ := cfg.Get("shop.currency")
	l, _ := cfg.Get("shop.limit")
	return c, l
}
