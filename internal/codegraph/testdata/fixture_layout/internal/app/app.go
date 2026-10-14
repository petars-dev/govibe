// Package app assembles the service: wiring may import every other layer.
package app

import (
	"net/http"

	"example.com/shop/internal/core/orders"
	"example.com/shop/internal/http/handlers"
	"example.com/shop/internal/store/orderstore"
)

// Run wires and serves.
func Run() {
	svc := orders.New(orderstore.New())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders", handlers.List(svc))
	mux.HandleFunc("GET /orders/{id}", handlers.Get(orderstore.New()))
	_ = http.ListenAndServe(":8080", mux)
}
