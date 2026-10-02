package middleware

import (
    "net/http"
    "time"
)

// DevNoCache sets headers to avoid client/proxy caching of static assets during development.
func DevNoCache(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
        w.Header().Set("Pragma", "no-cache")
        w.Header().Set("Expires", time.Unix(0, 0).UTC().Format(http.TimeFormat))
        next.ServeHTTP(w, r)
    })
}
