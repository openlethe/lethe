package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Forwarded headers must never influence trust decisions: only the direct
// peer address counts.
func TestForwardedHeadersDoNotBypassTrust(t *testing.T) {
	for _, mode := range []TrustMode{TrustLoopback, TrustPrivate} {
		s := &Server{trustMode: mode}
		handler := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.RemoteAddr = "203.0.113.5:4444" // public peer
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		req.Header.Set("X-Real-IP", "127.0.0.1")
		req.Header.Set("Forwarded", "for=127.0.0.1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("mode %s: public peer with loopback forwarded headers got %d", mode, rec.Code)
		}
	}
}

func TestPrivatePeerNotAuthenticatedInLoopbackMode(t *testing.T) {
	s := &Server{trustMode: TrustLoopback}
	handler := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "192.168.1.20:5555" // private network peer
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("private-network peer implicitly trusted in loopback mode: %d", rec.Code)
	}
}

func TestLoopbackPeerAllowedWithoutKey(t *testing.T) {
	for _, mode := range []TrustMode{TrustLoopback, TrustPrivate} {
		s := &Server{trustMode: mode}
		handler := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.RemoteAddr = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("mode %s: loopback peer got %d", mode, rec.Code)
		}
	}
}

func TestBearerRequiredRegardlessOfLocality(t *testing.T) {
	s := &Server{trustMode: TrustPrivate, authToken: "secret-token"}
	handler := s.AuthMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Private-network peer with no token is rejected: network locality is not
	// identity once a key is configured.
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "192.168.1.20:5555"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token-less private peer got %d with key configured", rec.Code)
	}
	// Loopback peer with the right token passes.
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid bearer got %d", rec.Code)
	}
}
