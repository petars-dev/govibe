// Package routes wires every route in one place.
package routes

import (
	"database/sql"
	"net/http"

	"example.com/fixture/business/services/orders"
	"example.com/fixture/controllers"
	"example.com/fixture/devmcp"
	"example.com/fixture/middleware"
	"example.com/fixture/storage/orderstore"
)

// New builds the router. Wiring packages may import every other layer.
func New(db *sql.DB) http.Handler {
	store := orderstore.New(db)
	svc := orders.New(store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", controllers.HomePage)
	mux.HandleFunc("GET /api/hello", controllers.HelloAPI)
	mux.HandleFunc("GET /api/orders", controllers.ListOrders(svc))
	mux.HandleFunc("GET /api/orders/{id}", controllers.GetOrder(store))
	mux.Handle("/dev/mcp", devmcp.NewHandler(store))
	mux.Handle("/assets/", http.StripPrefix("/assets", http.FileServer(http.Dir("assets"))))
	return middleware.RequestLogger(mux)
}
