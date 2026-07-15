package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAPIKeyMiddleware gates every route behind the configured bearer token and is
// a no-op when no key is set.
func TestAPIKeyMiddleware(t *testing.T) {
	s := New(nil, nil)
	s.apiKey = "s3cret"
	h := s.Handler()

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong bearer", "Bearer wrong", http.StatusUnauthorized},
		{"bare wrong", "wrong", http.StatusUnauthorized},
		{"matching bearer", "Bearer s3cret", http.StatusOK},
		{"matching bare", "s3cret", http.StatusOK},
		{"lowercase bearer scheme", "bearer s3cret", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

// TestAPIKeyMiddlewareEmptyIsOpen confirms the server stays open when no key is
// configured (backward compatibility).
func TestAPIKeyMiddlewareEmptyIsOpen(t *testing.T) {
	s := New(nil, nil) // s.apiKey == ""
	h := s.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (open when no key set)", rr.Code)
	}
}