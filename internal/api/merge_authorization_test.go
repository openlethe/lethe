package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/canonical"
)

const testMergeKey = "0123456789abcdef0123456789abcdef"

func envelopeForTest() MergeAuthorizationEnvelope {
	now := time.Now().UTC()
	return MergeAuthorizationEnvelope{
		Version:           MergeAuthorizationVersion,
		ProjectID:         "project",
		RefName:           "refs/shared/main",
		ExpectedHead:      "expected",
		NewHead:           "new",
		ProposalID:        "proposal",
		ProposalDigest:    strings.Repeat("a", 64),
		ReviewerPrincipal: "reviewer",
		MergerPrincipal:   "merger",
		Strategy:          "fast_forward",
		IssuedAt:          now.Add(-time.Second).Format(time.RFC3339Nano),
		ExpiresAt:         now.Add(2 * time.Minute).Format(time.RFC3339Nano),
		Nonce:             "0123456789abcdef0123456789abcdef",
		KeyID:             "",
	}
}

func signEnvelope(t *testing.T, key string, env MergeAuthorizationEnvelope) string {
	t.Helper()
	canonicalBytes, err := canonical.JSON(env)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(canonicalBytes)
	signed, err := json.Marshal(map[string]any{
		"envelope":  env,
		"signature": hex.EncodeToString(mac.Sum(nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func testKeys() map[string][]byte {
	return map[string][]byte{"": []byte(testMergeKey)}
}

func verifyForTest(t *testing.T, keys map[string][]byte, authorization string) error {
	t.Helper()
	_, err := verifyMergeAuthorizationV2(keys, "project", "refs/shared/main", "expected", "new", "proposal", "reviewer", authorization, time.Now().UTC())
	return err
}

func TestVerifyEnvelopeAcceptsValid(t *testing.T) {
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, envelopeForTest())); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
}

func TestVerifyEnvelopeRejectsBadSignatureAndTampering(t *testing.T) {
	env := envelopeForTest()
	if err := verifyForTest(t, testKeys(), signEnvelope(t, "wrong-key-wrong-key-wrong-key-12", env)); err == nil {
		t.Fatal("wrong key accepted")
	}

	// Tamper with the proposal digest after signing: verification must fail.
	signed := signEnvelope(t, testMergeKey, env)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(signed), &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["envelope"].(map[string]any)["proposal_digest"] = strings.Repeat("b", 64)
	tampered, _ := json.Marshal(decoded)
	if err := verifyForTest(t, testKeys(), string(tampered)); err == nil {
		t.Fatal("tampered proposal digest accepted")
	}
}

func TestVerifyEnvelopeExpiry(t *testing.T) {
	env := envelopeForTest()
	env.ExpiresAt = time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339Nano)
	env.IssuedAt = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("expired authorization accepted")
	}
}

func TestVerifyEnvelopeFutureIssued(t *testing.T) {
	env := envelopeForTest()
	env.IssuedAt = time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	env.ExpiresAt = time.Now().UTC().Add(12 * time.Minute).Format(time.RFC3339Nano)
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("future-issued authorization beyond tolerance accepted")
	}
}

func TestVerifyEnvelopeTTLCap(t *testing.T) {
	env := envelopeForTest()
	env.ExpiresAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("over-long TTL accepted")
	}
}

func TestVerifyEnvelopeFieldBinding(t *testing.T) {
	env := envelopeForTest()
	signed := signEnvelope(t, testMergeKey, env)
	// The request describes different merge fields than the envelope.
	_, err := verifyMergeAuthorizationV2(testKeys(), "project", "refs/shared/main", "expected", "DIFFERENT", "proposal", "reviewer", signed, time.Now().UTC())
	if err == nil {
		t.Fatal("envelope accepted for different merge fields")
	}
	_, err = verifyMergeAuthorizationV2(testKeys(), "project", "refs/shared/main", "expected", "new", "DIFFERENT", "reviewer", signed, time.Now().UTC())
	if err == nil {
		t.Fatal("envelope accepted for different proposal")
	}
	_, err = verifyMergeAuthorizationV2(testKeys(), "project", "refs/shared/main", "expected", "new", "proposal", "DIFFERENT", signed, time.Now().UTC())
	if err == nil {
		t.Fatal("envelope accepted for different reviewer")
	}
}

func TestVerifyEnvelopeStrategyAndForms(t *testing.T) {
	env := envelopeForTest()
	env.Strategy = "delete_everything"
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("invalid strategy accepted")
	}
	env = envelopeForTest()
	env.Nonce = "not-hex"
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("malformed nonce accepted")
	}
	env = envelopeForTest()
	env.ProposalDigest = "short"
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("malformed proposal digest accepted")
	}
	env = envelopeForTest()
	env.Version = "memory-git-merge/v1"
	if err := verifyForTest(t, testKeys(), signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("legacy v1 envelope accepted")
	}
}

func TestVerifyEnvelopeKeyRotation(t *testing.T) {
	env := envelopeForTest()
	env.KeyID = "old-key"
	keys := map[string][]byte{
		"current-key": []byte("current-current-current-current-1"),
		"old-key":     []byte(testMergeKey),
	}
	// During the overlap window the old key still verifies its envelopes.
	if err := verifyForTest(t, keys, signEnvelope(t, testMergeKey, env)); err != nil {
		t.Fatalf("rotated key envelope rejected during overlap: %v", err)
	}
	// An unknown key id fails closed.
	env.KeyID = "unknown-key"
	if err := verifyForTest(t, keys, signEnvelope(t, testMergeKey, env)); err == nil {
		t.Fatal("unknown key id accepted")
	}
}
