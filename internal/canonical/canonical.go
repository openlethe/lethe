// Package canonical provides deterministic serialization shared by conflict
// identities, changeset digests, and merge-authorization envelope
// verification. It matches Charon's canonical package byte for byte:
// cross-service signatures depend on both sides producing identical bytes.
package canonical

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// JSON marshals v in the platform canonical form: struct fields in
// declaration order, map keys sorted, no insignificant whitespace, and no
// HTML escaping so digests never depend on encoder escaping policy.
func JSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("canonical encode: %w", err)
	}
	out := buf.Bytes()
	if n := len(out); n > 0 && out[n-1] == '\n' {
		out = out[:n-1]
	}
	return out, nil
}

// Digest returns SHA-256 hex over domain-separated canonical JSON.
func Digest(domain string, v any) (string, error) {
	if domain == "" {
		return "", errors.New("canonical digest domain required")
	}
	payload, err := JSON(v)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{'\n'})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil)), nil
}
