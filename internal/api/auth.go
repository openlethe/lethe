package api

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// parseBearer parses an Authorization header and returns the token.
// It requires exactly two fields: "Bearer" (case-insensitive) and the token.
// It does not accept raw tokens, missing schemes, or extra fields.
func parseBearer(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 {
		return "", false
	}
	if !strings.EqualFold(fields[0], "Bearer") {
		return "", false
	}
	if fields[1] == "" {
		return "", false
	}
	return fields[1], true
}

// TrustMode controls which clients are trusted when no API key is configured.
type TrustMode string

const (
	// TrustPrivate allows loopback, private-network, and link-local peers.
	// This is the default for local development and Docker Desktop.
	TrustPrivate TrustMode = "private"
	// TrustLoopback allows only loopback peers.
	TrustLoopback TrustMode = "loopback"
)

// WithTrustMode sets the trust mode for unauthenticated requests.
func WithTrustMode(mode TrustMode) Option {
	return func(s *Server) {
		s.trustMode = mode
	}
}

type Option func(*Server)

// WithAuthToken enables bearer-token authentication when token is non-empty.
// Empty token keeps trusted-local-network mode: loopback/private clients are
// allowed and public clients are rejected.
func WithAuthToken(token string) Option {
	return func(s *Server) {
		s.authToken = strings.TrimSpace(token)
	}
}

// WithCharonMergeKey configures the separate Charon-held HMAC key used to
// authorize protected-ref merge operations. The bearer API token alone is not
// sufficient to move a protected ref.
func WithCharonMergeKey(key string) Option {
	return func(s *Server) {
		s.charonMergeKey = []byte(strings.TrimSpace(key))
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
				if isTrustedPeer(r.RemoteAddr, s.trustMode) {
					next.ServeHTTP(w, r)
					return
				}
				writeAuthError(w, http.StatusForbidden, "lethe API key not configured; public-network access denied")
				return
			}

			got, ok := parseBearer(r.Header.Get("Authorization"))
			if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) != 1 {
				writeAuthError(w, http.StatusUnauthorized, "missing or invalid bearer token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isTrustedPeer returns whether a remote address is trusted under the given mode.
// It parses the host from "host:port" and checks the IP against the mode rules.
// It does not use hostnames or forwarded headers as trust identities.
func isTrustedPeer(remoteAddr string, mode TrustMode) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if host == "" {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if mode == TrustLoopback {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
