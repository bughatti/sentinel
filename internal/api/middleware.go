// Package api provides the HTTP API server for Sentinel NVR with
// REST endpoints.
package api

import (
	"bufio"
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type contextKey string

const requestIDKey contextKey = "requestID"

// requestIDMiddleware generates a UUID request ID and attaches it to the
// request context and response header.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// loggingMiddleware logs every request with method, path, status, and duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// corsMiddleware adds permissive CORS headers. In production callers should
// pass a whitelist of allowed origins via APIConfig.CORSOrigins.
// corsMiddleware lets the listed browser origins read API responses from
// other sites. An empty list allows no cross-origin reads; the embedded
// dashboard is same-origin and never needs CORS. Only an explicit "*" opens
// the API to every website, which matters because a page on the internet,
// loaded in a browser on the same network, could otherwise read camera data.
func corsMiddleware(origins []string) func(http.Handler) http.Handler {
	allowAll := false
	for _, o := range origins {
		if o == "*" {
			allowAll = true
		}
	}

	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAll || allowed[strings.ToLower(strings.TrimRight(origin, "/"))] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,DELETE,OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,X-Request-ID")
					w.Header().Set("Access-Control-Max-Age", "3600")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authMiddleware enforces Bearer token authentication when apiKey is non-empty.
// The path /api/ws (WebSocket) is excluded so browser clients can connect.
func authMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			// A Bearer token in Authorization, or ?api_key= for requests a
			// browser cannot add headers to: <img>, <video>, HLS segments and
			// the WebSocket upgrade. The WebSocket is not exempt: it carries
			// the live event stream, including recognised face names.
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if token == "" {
				token = r.URL.Query().Get("api_key")
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.status = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return rw.ResponseWriter.(http.Hijacker).Hijack()
}
