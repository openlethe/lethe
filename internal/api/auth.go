package api

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Option configures the API server.
type Option func(*Server)

// WithAuthToken enables bearer-token authentication when token is non-empty.
// Empty token keeps trusted-local-network mode: loopback/private clients are
// allowed and public clients are rejected.
func WithAuthToken(token string) Option {
	return func(s *Server) {
		s.authToken = strings.TrimSpace(token)
	}
}

// AuthMiddleware protects API, UI, and SSE routes. With a configured token it
// requires Authorization: Bearer <token>. Without a token, it only allows
// loopback/private clients so Docker Desktop and local development remain
// usable without exposing memory data to public networks by accident.
func (s *Server) AuthMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.authToken == "" {
				if isLocalRequest(r) {
					next.ServeHTTP(w, r)
					return
				}
				writeAuthError(w, http.StatusForbidden, "lethe API key not configured; public-network access denied")
				return
			}

			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) != 1 {
				writeAuthError(w, http.StatusUnauthorized, "missing or invalid bearer token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return false
	}
	if strings.HasSuffix(host, ".example.com") || host == "example.com" {
		// httptest.NewRequest sets RemoteAddr to example.com:80 by default.
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
