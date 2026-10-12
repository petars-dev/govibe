package controllers

import (
	"fmt"
	"net/http"

	"example.com/shop/business/actions"
	"example.com/shop/business/services/orders"
	"petars-dev/govibe/web"
)

// Home is a plain named handler.
func Home(w http.ResponseWriter, r *http.Request) {
	_, _ = fmt.Fprintf(w, "<h1>%s</h1>", actions.Hello())
}

// HelloAPI is a JSON handler using a framework helper.
func HelloAPI(w http.ResponseWriter, r *http.Request) {
	web.JSON(w, http.StatusOK, map[string]string{"message": actions.Hello()})
}

// ListOrders returns a closure (the "controllers.ListOrders(svc)" shape).
func ListOrders(svc *orders.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := svc.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		web.JSON(w, 200, rows)
	}
}

// Orders is a controller type; its method values are mounted.
type Orders struct{ Svc *orders.Service }

func (o *Orders) Show(w http.ResponseWriter, r *http.Request) { web.JSON(w, 200, "show") }

// Hello is an http.Handler type (ServeHTTP), like a framework-style handler.
type Plain struct{}

func (Plain) ServeHTTP(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("plain")) }

// AuditMW is app middleware.
func AuditMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
