package db

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openlethe/lethe/internal/models"
)

// Deterministic conflict identity. A conflict ID is derived from the semantic
// content of the conflict — never from a random UUID — so repeating analysis
// or replaying a proposal converges on the same row instead of creating
// duplicate open conflicts.
//
// The identity covers: project, base, left and right changesets, conflict
// type, the affected semantic memory or relationship identity, and the
// normalized details relevant to equivalence.
const conflictIDDomain = "lethe/memory-conflict/v1"

// DeterministicConflictID computes the canonical identity of a conflict. The
// detector calls it for every detected conflict; persistence upserts on it.
func DeterministicConflictID(c *models.MemoryConflict) string {
	payload := map[string]any{
		"project_id":         c.ProjectID,
		"base_changeset_id":  c.BaseChangesetID,
		"left_changeset_id":  c.LeftChangesetID,
		"right_changeset_id": c.RightChangesetID,
		"conflict_type":      c.ConflictType,
		"identity":           conflictSemanticIdentity(c),
	}
	canonical, err := canonicalJSON(payload)
	if err != nil {
		// map[string]any of strings cannot fail to encode.
		panic(err)
	}
	h := sha256.New()
	h.Write([]byte(conflictIDDomain))
	h.Write([]byte{'\n'})
	h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil))
}

// conflictSemanticIdentity extracts the type-specific identity of the affected
// memory, relationship, decision, or fact from the conflict details.
func conflictSemanticIdentity(c *models.MemoryConflict) string {
	d := c.Details
	str := func(key string) string {
		v, _ := d[key].(string)
		return v
	}
	sortedPair := func(a, b string) string {
		pair := []string{a, b}
		sort.Strings(pair)
		return pair[0] + "|" + pair[1]
	}
	switch c.ConflictType {
	case "duplicate_content":
		return strings.TrimSpace(str("duplicate_content"))
	case "contradictory_decision", "protected_decision":
		scope := str("scope")
		if left, right := str("left_event"), str("right_event"); left != "" || right != "" {
			return scope + "|" + sortedPair(left, right)
		}
		return scope + "|" + sortedPair(str("left_content"), str("right_content"))
	case "incompatible_fact":
		return str("fact_key") + "|" + str("scope") + "|" + sortedPair(str("left_content"), str("right_content"))
	case "boundary_violation":
		for _, key := range []string{"payload_project", "payload_topic", "payload_actor"} {
			if v := str(key); v != "" {
				return str("changeset_id") + "|" + key + "|" + v
			}
		}
		return str("changeset_id")
	case "scope_flow":
		return str("changeset_id") + "|" + str("from_visibility") + "|" + str("to_visibility") + "|" + str("op_type")
	case "trust_downgrade":
		return str("changeset_id") + "|" + str("target_event_id")
	case "stale_base":
		return ""
	default:
		// Unknown future types fall back to their summary so distinct semantic
		// findings still get distinct identities.
		return c.Summary
	}
}

// canonicalJSON marshals v with sorted map keys and no HTML escaping, matching
// the Charon canonical package byte for byte. Cross-service digests and
// signatures depend on both sides producing identical bytes.
func canonicalJSON(v any) ([]byte, error) {
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
