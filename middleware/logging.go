package middleware

import (
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// RequestLogger is a minimal request logging middleware.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logrus.WithFields(logrus.Fields{
			"method": r.Method,
			"path":   r.URL.Path,
			"t":      time.Since(start).String(),
		}).Info("request")
	})
}
