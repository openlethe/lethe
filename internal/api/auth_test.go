package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareRequiresBearerWhenTokenConfigured(t *testing.T) {
	s := &Server{authToken: "secret"}
	h := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "invalid", header: "Bearer wrong", want: http.StatusUnauthorized},
		{name: "valid", header: "Bearer secret", want: http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.RemoteAddr = "203.0.113.10:12345"
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAuthMiddlewareNoTokenAllowsTrustedLocalNetwork(t *testing.T) {
	s := &Server{}
	h := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name       string
		remoteAddr string
		want       int
	}{
		{name: "loopback ipv4", remoteAddr: "127.0.0.1:12345", want: http.StatusNoContent},
		{name: "loopback ipv6", remoteAddr: "[::1]:12345", want: http.StatusNoContent},
		{name: "docker desktop private gateway", remoteAddr: "192.168.65.1:12345", want: http.StatusNoContent},
		{name: "remote public", remoteAddr: "203.0.113.10:12345", want: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
			req.RemoteAddr = tt.remoteAddr
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestRouterUsesAuthMiddleware(t *testing.T) {
	server := NewServer(nil, nil, WithAuthToken("secret"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected router to reject missing token, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("Authorization", "Bearer secret")
	server.Router().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected valid bearer token to reach health handler, got %d", rr.Code)
	}
}
