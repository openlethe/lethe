package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/openlethe/lethe/internal/canonical"
	"github.com/openlethe/lethe/internal/db"
)

// Protected-merge authorization envelope, version memory-git-merge/v2. The
// canonical bytes of this struct — exactly as declared, encoded with sorted
// keys and no HTML escaping — are what Charon signs. Field order and naming
// are signature-critical; append new fields at the end, never reorder.
type MergeAuthorizationEnvelope struct {
	Version           string `json:"version"`
	ProjectID         string `json:"project_id"`
	RefName           string `json:"ref_name"`
	ExpectedHead      string `json:"expected_head"`
	NewHead           string `json:"new_head"`
	ProposalID        string `json:"proposal_id"`
	ProposalDigest    string `json:"proposal_digest"`
	ReviewerPrincipal string `json:"reviewer_principal"`
	MergerPrincipal   string `json:"merger_principal"`
	Strategy          string `json:"strategy"`
	IssuedAt          string `json:"issued_at"`
	ExpiresAt         string `json:"expires_at"`
	Nonce             string `json:"nonce"`
	KeyID             string `json:"key_id"`
}

// MergeAuthorizationVersion is the only accepted envelope version.
const MergeAuthorizationVersion = "memory-git-merge/v2"

// mergeClockTolerance bounds accepted clock skew between Charon and Lethe.
const mergeClockTolerance = 60 * time.Second

// maxMergeAuthorizationTTL caps how long an authorization can live; issuers
// must use short expiries and this bound is enforced regardless.
const maxMergeAuthorizationTTL = 15 * time.Minute

var (
	errAuthorizationMalformed = errors.New("merge authorization is malformed")
	errAuthorizationSignature = errors.New("merge authorization signature invalid")
	errAuthorizationExpired   = errors.New("merge authorization expired")
	errAuthorizationFields    = errors.New("merge authorization fields do not match the request")
	errAuthorizationKeyID     = errors.New("merge authorization key id unknown")
	noncePattern              = regexp.MustCompile(`^[0-9a-f]{32}$`)
	sha256HexPattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// signedMergeAuthorization is the wire form: the envelope plus its HMAC.
type signedMergeAuthorization struct {
	Envelope  MergeAuthorizationEnvelope `json:"envelope"`
	Signature string                     `json:"signature"`
}

// verifyMergeAuthorizationV2 decodes and fully validates a signed envelope
// against the request it accompanies. Every check fails closed; signature or
// key material is never logged or included in errors.
func verifyMergeAuthorizationV2(
	keys map[string][]byte,
	project, refName, expectedHead, newHead, proposalID, reviewer string,
	authorization string,
	now time.Time,
) (*MergeAuthorizationEnvelope, error) {
	var signed signedMergeAuthorization
	if err := json.Unmarshal([]byte(authorization), &signed); err != nil {
		return nil, fmt.Errorf("%w: not a signed envelope", errAuthorizationMalformed)
	}
	env := &signed.Envelope
	if env.Version != MergeAuthorizationVersion {
		return nil, fmt.Errorf("%w: unsupported version", errAuthorizationMalformed)
	}

	key, ok := keys[env.KeyID]
	if !ok || len(key) < 32 {
		return nil, errAuthorizationKeyID
	}
	signature, err := hex.DecodeString(signed.Signature)
	if err != nil || len(signature) != sha256.Size {
		return nil, fmt.Errorf("%w: bad signature encoding", errAuthorizationMalformed)
	}
	canonicalBytes, err := canonical.JSON(env)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope not encodable", errAuthorizationMalformed)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonicalBytes)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errAuthorizationSignature
	}

	// Field binding: the signed envelope must describe exactly this request.
	if env.ProjectID != project || env.RefName != refName ||
		env.ExpectedHead != expectedHead || env.NewHead != newHead ||
		env.ProposalID != proposalID || env.ReviewerPrincipal != reviewer {
		return nil, errAuthorizationFields
	}
	if env.MergerPrincipal == "" {
		return nil, fmt.Errorf("%w: merger principal missing", errAuthorizationFields)
	}
	if !db.ValidMergeStrategy(env.Strategy) {
		return nil, fmt.Errorf("%w: strategy %q", errAuthorizationFields, env.Strategy)
	}
	if !noncePattern.MatchString(env.Nonce) {
		return nil, fmt.Errorf("%w: nonce form", errAuthorizationMalformed)
	}
	if !sha256HexPattern.MatchString(env.ProposalDigest) {
		return nil, fmt.Errorf("%w: proposal digest form", errAuthorizationMalformed)
	}

	issuedAt, err := time.Parse(time.RFC3339Nano, env.IssuedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: issued_at", errAuthorizationMalformed)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, env.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("%w: expires_at", errAuthorizationMalformed)
	}
	if issuedAt.After(now.Add(mergeClockTolerance)) {
		return nil, fmt.Errorf("%w: issued in the future beyond clock tolerance", errAuthorizationMalformed)
	}
	if !expiresAt.After(issuedAt) {
		return nil, fmt.Errorf("%w: expiry not after issuance", errAuthorizationMalformed)
	}
	if expiresAt.Sub(issuedAt) > maxMergeAuthorizationTTL {
		return nil, fmt.Errorf("%w: ttl exceeds %s", errAuthorizationMalformed, maxMergeAuthorizationTTL)
	}
	if !now.Before(expiresAt.Add(mergeClockTolerance)) {
		return nil, errAuthorizationExpired
	}
	return env, nil
}
