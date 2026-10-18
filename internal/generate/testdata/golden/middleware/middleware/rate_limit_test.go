package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimitCallsTheNextHandler(t *testing.T) {
	called := false
	h := RateLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("called = %v, status = %d", called, rec.Code)
	}
}
