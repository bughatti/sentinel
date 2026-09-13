package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

// The real contract: a Bearer token in Authorization, or ?api_key=. The /ws
// path is deliberately exempt because browsers cannot set headers on a
// WebSocket upgrade — that hole is intentional, so pin it.
func TestAuthMiddleware(t *testing.T) {
	cases := []struct {
		name, key, authHeader, query, path string
		want                               int
	}{
		{"no key configured lets anything through", "", "", "", "/api/events", http.StatusOK},
		{"correct bearer token accepted", "sekrit", "Bearer sekrit", "", "/api/events", http.StatusOK},
		{"bare token without the Bearer prefix accepted", "sekrit", "sekrit", "", "/api/events", http.StatusOK},
		{"wrong token rejected", "sekrit", "Bearer nope", "", "/api/events", http.StatusUnauthorized},
		{"missing credential rejected", "sekrit", "", "", "/api/events", http.StatusUnauthorized},
		{"query parameter accepted", "sekrit", "", "sekrit", "/api/events", http.StatusOK},
		{"wrong query parameter rejected", "sekrit", "", "nope", "/api/events", http.StatusUnauthorized},
		{"websocket upgrade is exempt by design", "sekrit", "", "", "/ws", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := authMiddleware(c.key)(http.HandlerFunc(ok))
			url := c.path
			if c.query != "" {
				url += "?api_key=" + c.query
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Errorf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	h := requestIDMiddleware(http.HandlerFunc(ok))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestCORSMiddleware(t *testing.T) {
	h := corsMiddleware([]string{"https://example.test"})(http.HandlerFunc(ok))

	req := httptest.NewRequest(http.MethodOptions, "/api/events", nil)
	req.Header.Set("Origin", "https://example.test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		t.Errorf("preflight from an allowed origin should not fail, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("Origin", "https://evil.test")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "https://evil.test" {
		t.Error("an unlisted origin must not be echoed back as allowed")
	}
}
