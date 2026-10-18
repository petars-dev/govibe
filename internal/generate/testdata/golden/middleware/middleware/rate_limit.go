package middleware

import "net/http"

// RateLimit wraps a handler. Do its work before or after next, or answer and
// return without calling it. Register it in routes.Register before any route is
// declared (r.Use(middleware.RateLimit)), or on one group (r.With(...)).
func RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
