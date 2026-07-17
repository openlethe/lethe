package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openlethe/lethe/internal/models"
)

// Versioned semantic validation for Memory Git operations (memory_git/v1).
// Every operation is validated before it can enter immutable,
// integrity-digested history, so projection never has to guess how to handle
// malformed operations. Stateless invariants are checked for every op; when
// any op references existing memory, the active memory set at the first
// parent is projected once and target-existence rules are enforced against
// it. Charon performs the same structural checks client-side; Lethe is the
// authoritative enforcer.

// validMemoryKinds enumerates the accepted add_memory kinds.
var validMemoryKinds = map[string]bool{
	"observation": true,
	"fact":        true,
	"decision":    true,
	"task":        true,
	"flag":        true,
	"record":      true,
}

// validVisibilities enumerates the accepted visibility levels.
var validVisibilities = map[string]bool{
	"private":  true,
	"internal": true,
	"shared":   true,
	"public":   true,
}

// attestationProvenanceKeys are the provenance fields that make a target-less
// attach_evidence/attach_verification op a valid merge/review attestation
// marker under memory_git/v1.
var attestationProvenanceKeys = []string{
	"reviewer", "proposal_id", "source_changeset_id",
	"rejected_from", "cherrypicked_from", "left_branch", "right_branch",
}

// Payload size caps. The API layer already bounds request bodies at 1 MiB
// (api.MaxJSONBodySize); these per-op bounds stop a single op from crowding
// out a changeset and keep free-text fields proportional to how projection,
// diff, and conflict detection actually use them. Every observed legitimate
// value in tests, docs, and the Charon merge flow is orders of magnitude
// below these caps.
const (
	// maxOpPayloadBytes bounds the canonical JSON encoding of one op's
	// payload (Go marshals map keys in sorted order, so this is exact).
	maxOpPayloadBytes = 64 << 10
	// maxContentBytes bounds free-text memory content.
	maxContentBytes = 16 << 10
	// maxSummaryBytes bounds human-readable summaries, reasons, and notes.
	maxSummaryBytes = 4 << 10
	// maxFieldBytes bounds every other payload string (identifiers, kinds,
	// provenance references, visibility markers); UUIDs and ref names are
	// far shorter.
	maxFieldBytes = 1024
	// maxTagBytes bounds a single tag entry.
	maxTagBytes = 128
	// maxTags bounds the tags array of one op.
	maxTags = 64
	// maxIDBytes bounds the op-level identifier channels
	// (target_event_id / resulting_event_id).
	maxIDBytes = 1024
)

// commonPayloadKeys have defined semantics for every op type: the diff
// renderer summarizes any op by its summary; the conflict detector reads
// boundary (project/topic/actor) and scope-flow metadata from every op; and
// reviewed-merge/cherry-pick construction annotates copied ops with the
// attestation provenance markers (e.g. a cherry-picked add_memory records
// cherrypicked_from), so those keys are legitimate on every op.
var commonPayloadKeys = append([]string{
	"summary", "project_id", "topic_id", "actor_id", "from_visibility", "to_visibility",
}, attestationProvenanceKeys...)

// memoryContentKeys are the fields the projector overlays onto an accepted
// memory record, plus the fact/decision metadata the conflict detector
// compares across branches.
var memoryContentKeys = []string{
	"content", "kind", "event_type", "scope", "visibility", "tags", "confidence",
	"fact_key", "subject", "valid_from", "valid_to", "protected", "approval",
}

// attestationPayloadKeys are the evidence-record fields an attach op may
// carry in addition to the merge/review provenance markers (status records
// the review outcome, e.g. a rejection attestation's "rejected").
var attestationPayloadKeys = []string{"kind", "source", "note", "status"}

// allowedPayloadKeys is the exact payload key contract per op type, derived
// from the projector (store_memory_context.go), the conflict detector
// (conflict.go), and the documented attestation flow. Anything outside the
// set is rejected so immutable history never records fields no consumer
// understands.
var allowedPayloadKeys = map[models.SemanticOpType]map[string]bool{
	models.OpAddMemory:     keySet(commonPayloadKeys, memoryContentKeys, []string{"memory_id", "event_id"}),
	models.OpCorrectMemory: keySet(commonPayloadKeys, memoryContentKeys, []string{"target_trust", "source_trust"}),
	models.OpSupersedeMemory: keySet(commonPayloadKeys, memoryContentKeys,
		[]string{"target_trust", "source_trust", "memory_id", "event_id"}),
	models.OpMarkDuplicate:      keySet(commonPayloadKeys, []string{"duplicate_id", "duplicate_of"}),
	models.OpAddRelationship:    keySet(commonPayloadKeys, []string{"kind", "from_memory_id", "to_memory_id"}),
	models.OpProposeDeprecation: keySet(commonPayloadKeys, []string{"reason"}),
	models.OpAttachEvidence:     keySet(commonPayloadKeys, attestationPayloadKeys, attestationProvenanceKeys),
	models.OpAttachVerification: keySet(commonPayloadKeys, attestationPayloadKeys, attestationProvenanceKeys),
}

func keySet(groups ...[]string) map[string]bool {
	set := map[string]bool{}
	for _, group := range groups {
		for _, key := range group {
			set[key] = true
		}
	}
	return set
}

// payloadStringLimit returns the size cap for a free-text payload field.
func payloadStringLimit(key string) int {
	switch key {
	case "content":
		return maxContentBytes
	case "summary", "reason", "note":
		return maxSummaryBytes
	default:
		return maxFieldBytes
	}
}

// idsDisagree reports whether two identifier channels for the same role are
// both set but name different identities. Equal values via two channels are
// fine: the projector's fallback order simply normalizes them.
func idsDisagree(a, b string) bool {
	return a != "" && b != "" && a != b
}

// validateSemanticOps validates every op in a new changeset against the v1
// semantic contract. Stateless invariants are checked per op. When any op
// references existing memory, the active memory set at the first parent is
// projected once and ops are then validated sequentially, applying each op's
// state effect before the next — exactly how projection applies them — so an
// op may target memory introduced earlier in the same changeset.
func (s *Store) validateSemanticOps(ctx context.Context, projectID string, parentIDs []string, ops []models.MemorySemanticOp) error {
	needsState := false
	for i := range ops {
		if err := validateOpStateless(projectID, i, &ops[i]); err != nil {
			return err
		}
		if opNeedsParentState(&ops[i]) {
			needsState = true
		}
	}
	if !needsState {
		return nil
	}
	var active map[string]bool
	parentID := ""
	if len(parentIDs) > 0 && parentIDs[0] != "" {
		var err error
		parentID = parentIDs[0]
		active, err = s.activeMemoryStateAt(ctx, projectID, parentID)
		if err != nil {
			return fmt.Errorf("project parent state for operation validation: %w", err)
		}
	} else {
		// A root changeset has no parent state.
		active = map[string]bool{}
	}
	for i := range ops {
		if err := validateOpAgainstState(i, &ops[i], active); err != nil {
			return err
		}
		applyOpStateEffect(active, parentID, &ops[i])
	}
	return nil
}

// applyOpStateEffect mirrors the projection's per-op effect on the active ID
// set so sequential validation observes the same state projection would.
// changesetID is unknown before insert, so synthesized identities use the
// first parent as a stable stand-in; explicit resulting IDs are unaffected.
func applyOpStateEffect(active map[string]bool, parentID string, op *models.MemorySemanticOp) {
	payloadString := func(key string) string {
		text, _ := op.Payload[key].(string)
		return text
	}
	switch op.OpType {
	case models.OpAddMemory:
		id := op.ResultingEventID
		if id == "" {
			id = payloadString("memory_id")
		}
		if id == "" {
			id = payloadString("event_id")
		}
		if id == "" {
			id = fmt.Sprintf("mem:%s:%d", parentID, op.Ordinal)
		}
		active[id] = true
	case models.OpCorrectMemory:
		target := op.TargetEventID
		result := op.ResultingEventID
		if result != "" && result != target {
			delete(active, target)
			active[result] = true
		}
	case models.OpSupersedeMemory:
		delete(active, op.TargetEventID)
		if payloadString("content") != "" {
			id := op.ResultingEventID
			if id == "" {
				id = payloadString("memory_id")
			}
			if id == "" {
				id = payloadString("event_id")
			}
			if id != "" {
				active[id] = true
			}
		}
	case models.OpMarkDuplicate:
		duplicate := op.TargetEventID
		if duplicate == "" {
			duplicate = payloadString("duplicate_id")
		}
		delete(active, duplicate)
	}
}

// opNeedsParentState reports whether the op references memory identities that
// must be checked against the parent state.
func opNeedsParentState(op *models.MemorySemanticOp) bool {
	switch op.OpType {
	case models.OpCorrectMemory, models.OpSupersedeMemory, models.OpMarkDuplicate,
		models.OpAddRelationship, models.OpProposeDeprecation:
		return true
	case models.OpAttachEvidence, models.OpAttachVerification:
		return op.TargetEventID != ""
	case models.OpAddMemory:
		return op.ResultingEventID != "" || stringPayload(op.Payload, "memory_id") != "" || stringPayload(op.Payload, "event_id") != ""
	default:
		return false
	}
}

// activeMemoryStateAt projects the active semantic memory ID set at headID,
// including the frozen legacy baseline events.
func (s *Store) activeMemoryStateAt(ctx context.Context, projectID, headID string) (map[string]bool, error) {
	history, err := s.memoryHistoryAt(ctx, projectID, headID)
	if err != nil {
		return nil, err
	}
	state := projectConflictState(history)
	active := make(map[string]bool, len(state))
	for id := range state {
		active[id] = true
	}
	for _, cs := range history {
		if cs.IdempotencyKey != "legacy-root" {
			continue
		}
		if err := s.EnsureLegacyBaseline(ctx, cs); err != nil {
			return nil, err
		}
		ids, err := s.loadLegacyBaselineIDs(ctx, projectID, cs.ChangesetID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			active[id] = true
		}
	}
	return active, nil
}

func validateOpStateless(projectID string, index int, op *models.MemorySemanticOp) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: ops[%d] (%s): %s", ErrInvalidSemanticOp, index, op.OpType, fmt.Sprintf(format, args...))
	}
	encoded, err := json.Marshal(op.Payload)
	if err != nil {
		return fail("payload is not JSON-encodable: %v", err)
	}
	if len(encoded) > maxOpPayloadBytes {
		return fail("payload is %d bytes, exceeding the %d-byte per-op cap", len(encoded), maxOpPayloadBytes)
	}
	if len(op.TargetEventID) > maxIDBytes {
		return fail("target_event_id exceeds the %d-byte identifier cap", maxIDBytes)
	}
	if len(op.ResultingEventID) > maxIDBytes {
		return fail("resulting_event_id exceeds the %d-byte identifier cap", maxIDBytes)
	}
	if allowed, ok := allowedPayloadKeys[op.OpType]; ok {
		for key, value := range op.Payload {
			if !allowed[key] {
				return fail("payload field %q is not supported for %s", key, op.OpType)
			}
			if text, isString := value.(string); isString {
				if limit := payloadStringLimit(key); len(text) > limit {
					return fail("payload.%s exceeds the %d-byte cap", key, limit)
				}
			}
		}
	}
	payloadString := func(key string) string {
		text, _ := op.Payload[key].(string)
		return text
	}

	switch op.OpType {
	case models.OpAddMemory:
		if strings.TrimSpace(payloadString("content")) == "" {
			return fail("payload.content is required and must be a non-empty string")
		}
		// The resulting identity may arrive over three channels; they must
		// never disagree about which memory this op creates.
		if idsDisagree(op.ResultingEventID, payloadString("memory_id")) ||
			idsDisagree(op.ResultingEventID, payloadString("event_id")) ||
			idsDisagree(payloadString("memory_id"), payloadString("event_id")) {
			return fail("ambiguous resulting identity: resulting_event_id, payload.memory_id, and payload.event_id must agree when more than one is set")
		}
		if kind, present := op.Payload["kind"]; present {
			text, ok := kind.(string)
			if !ok || !validMemoryKinds[text] {
				return fail("payload.kind must be one of observation, fact, decision, task, flag, record")
			}
		}
		if scope, present := op.Payload["scope"]; present {
			text, ok := scope.(string)
			if !ok || text == "" {
				return fail("payload.scope must be a non-empty string when present")
			}
		}
		if visibility, present := op.Payload["visibility"]; present {
			text, ok := visibility.(string)
			if !ok || !validVisibilities[text] {
				return fail("payload.visibility must be one of private, internal, shared, public")
			}
		}
		if confidence, present := op.Payload["confidence"]; present {
			value, ok := confidence.(float64)
			if !ok || value < 0 || value > 1 {
				return fail("payload.confidence must be a number between 0 and 1")
			}
		}
		if tags, present := op.Payload["tags"]; present {
			list, ok := tags.([]any)
			if !ok {
				return fail("payload.tags must be an array of strings")
			}
			if len(list) > maxTags {
				return fail("payload.tags has %d entries, exceeding the %d-entry cap", len(list), maxTags)
			}
			for _, tag := range list {
				text, ok := tag.(string)
				if !ok || text == "" {
					return fail("payload.tags must be an array of non-empty strings")
				}
				if len(text) > maxTagBytes {
					return fail("payload.tags entry exceeds the %d-byte cap", maxTagBytes)
				}
			}
		}

	case models.OpCorrectMemory:
		if op.TargetEventID == "" {
			return fail("target_event_id is required")
		}
		if len(op.Payload) == 0 {
			return fail("payload must contain at least one correction field")
		}
		if op.ResultingEventID != "" && op.ResultingEventID == op.TargetEventID {
			return fail("resulting_event_id must differ from target_event_id")
		}

	case models.OpSupersedeMemory:
		if op.TargetEventID == "" {
			return fail("target_event_id is required")
		}
		if op.ResultingEventID != "" && op.ResultingEventID == op.TargetEventID {
			return fail("no self-supersede: resulting_event_id must differ from target_event_id")
		}
		if payloadString("memory_id") == op.TargetEventID && payloadString("memory_id") != "" {
			return fail("no self-supersede: payload memory_id must differ from target_event_id")
		}
		// The replacement identity may arrive over three channels; they must
		// never disagree about which memory replaces the target.
		if idsDisagree(op.ResultingEventID, payloadString("memory_id")) ||
			idsDisagree(op.ResultingEventID, payloadString("event_id")) ||
			idsDisagree(payloadString("memory_id"), payloadString("event_id")) {
			return fail("ambiguous replacement identity: resulting_event_id, payload.memory_id, and payload.event_id must agree when more than one is set")
		}
		targetTrust := payloadString("target_trust")
		sourceTrust := payloadString("source_trust")
		if targetTrust == "user_approved" && (sourceTrust == "inference" || sourceTrust == "model_inference") {
			return fail("invalid trust transition: lower-trust inference cannot replace user-approved memory")
		}

	case models.OpMarkDuplicate:
		// The duplicate and canonical identities each have two channels; a
		// disagreement would make the recorded lineage ambiguous.
		if idsDisagree(op.TargetEventID, payloadString("duplicate_id")) {
			return fail("ambiguous duplicate identity: target_event_id and payload.duplicate_id must agree when both are set")
		}
		if idsDisagree(op.ResultingEventID, payloadString("duplicate_of")) {
			return fail("ambiguous canonical identity: resulting_event_id and payload.duplicate_of must agree when both are set")
		}
		duplicate := op.TargetEventID
		if duplicate == "" {
			duplicate = payloadString("duplicate_id")
		}
		canonical := op.ResultingEventID
		if canonical == "" {
			canonical = payloadString("duplicate_of")
		}
		if duplicate == "" || canonical == "" {
			return fail("duplicate and canonical targets are both required")
		}
		if duplicate == canonical {
			return fail("duplicate and canonical targets must be distinct")
		}

	case models.OpAddRelationship:
		if idsDisagree(op.TargetEventID, payloadString("from_memory_id")) {
			return fail("ambiguous from identity: target_event_id and payload.from_memory_id must agree when both are set")
		}
		if idsDisagree(op.ResultingEventID, payloadString("to_memory_id")) {
			return fail("ambiguous to identity: resulting_event_id and payload.to_memory_id must agree when both are set")
		}
		from := op.TargetEventID
		if from == "" {
			from = payloadString("from_memory_id")
		}
		to := op.ResultingEventID
		if to == "" {
			to = payloadString("to_memory_id")
		}
		if from == "" || to == "" {
			return fail("from and to memory IDs are both required")
		}
		// Self-relations are explicitly not allowed under memory_git/v1.
		if from == to {
			return fail("self-relations are not allowed")
		}
		if payloadString("kind") == "" {
			return fail("payload.kind is required and must be a non-empty string")
		}

	case models.OpProposeDeprecation:
		if op.TargetEventID == "" {
			return fail("target_event_id is required")
		}
		if payloadString("reason") == "" {
			return fail("payload.reason is required and must be a non-empty string")
		}

	case models.OpAttachEvidence, models.OpAttachVerification:
		if len(op.Payload) == 0 {
			return fail("payload must be a non-empty object")
		}
		if op.TargetEventID == "" {
			// Target-less form is a merge/review attestation marker: it needs a
			// human-readable summary and at least one provenance field.
			if payloadString("summary") == "" {
				return fail("target-less attestation requires payload.summary")
			}
			provenance := false
			for _, key := range attestationProvenanceKeys {
				if payloadString(key) != "" {
					provenance = true
					break
				}
			}
			if !provenance {
				return fail("target-less attestation requires a provenance field (one of: %s)", strings.Join(attestationProvenanceKeys, ", "))
			}
		}

	default:
		return fail("unsupported operation type")
	}
	return nil
}

// validateOpAgainstState enforces target-existence and identity-collision
// rules against the active memory set at the parent state. All identities are
// project-local by construction, which forbids cross-project targets.
func validateOpAgainstState(index int, op *models.MemorySemanticOp, active map[string]bool) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: ops[%d] (%s): %s", ErrInvalidSemanticOp, index, op.OpType, fmt.Sprintf(format, args...))
	}
	payloadString := func(key string) string {
		text, _ := op.Payload[key].(string)
		return text
	}
	requireActive := func(role, id string) error {
		if !active[id] {
			return fail("%s %q does not exist at the parent state", role, id)
		}
		return nil
	}
	requireNew := func(role, id string) error {
		if active[id] {
			return fail("%s %q already exists at the parent state", role, id)
		}
		return nil
	}

	switch op.OpType {
	case models.OpAddMemory:
		id := op.ResultingEventID
		if id == "" {
			id = payloadString("memory_id")
		}
		if id == "" {
			id = payloadString("event_id")
		}
		if id != "" {
			if err := requireNew("resulting memory", id); err != nil {
				return err
			}
		}
	case models.OpCorrectMemory:
		if err := requireActive("correction target", op.TargetEventID); err != nil {
			return err
		}
		if op.ResultingEventID != "" {
			if err := requireNew("correction result", op.ResultingEventID); err != nil {
				return err
			}
		}
	case models.OpSupersedeMemory:
		if err := requireActive("supersede target", op.TargetEventID); err != nil {
			return err
		}
		// The replacement identity must be new under every fallback the
		// projection uses (resulting_event_id → memory_id → event_id), so a
		// supersede can never overwrite an unrelated active memory.
		if payloadString("content") != "" {
			id := op.ResultingEventID
			if id == "" {
				id = payloadString("memory_id")
			}
			if id == "" {
				id = payloadString("event_id")
			}
			if id != "" {
				if err := requireNew("supersede result", id); err != nil {
					return err
				}
			}
		}
	case models.OpMarkDuplicate:
		duplicate := op.TargetEventID
		if duplicate == "" {
			duplicate = payloadString("duplicate_id")
		}
		canonical := op.ResultingEventID
		if canonical == "" {
			canonical = payloadString("duplicate_of")
		}
		if err := requireActive("duplicate target", duplicate); err != nil {
			return err
		}
		if err := requireActive("canonical target", canonical); err != nil {
			return err
		}
	case models.OpAddRelationship:
		from := op.TargetEventID
		if from == "" {
			from = payloadString("from_memory_id")
		}
		to := op.ResultingEventID
		if to == "" {
			to = payloadString("to_memory_id")
		}
		if err := requireActive("from memory", from); err != nil {
			return err
		}
		if err := requireActive("to memory", to); err != nil {
			return err
		}
	case models.OpProposeDeprecation:
		if err := requireActive("deprecation target", op.TargetEventID); err != nil {
			return err
		}
	case models.OpAttachEvidence, models.OpAttachVerification:
		if op.TargetEventID != "" {
			if err := requireActive("attachment target", op.TargetEventID); err != nil {
				return err
			}
		}
	}
	return nil
}
