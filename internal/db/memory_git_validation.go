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
	if len(parentIDs) > 0 && parentIDs[0] != "" {
		var err error
		active, err = s.activeMemoryStateAt(ctx, projectID, parentIDs[0])
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
		applyOpStateEffect(active, parentIDs[0], &ops[i])
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
	if _, err := json.Marshal(op.Payload); err != nil {
		return fail("payload is not JSON-encodable: %v", err)
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
			for _, tag := range list {
				if text, ok := tag.(string); !ok || text == "" {
					return fail("payload.tags must be an array of non-empty strings")
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
		targetTrust := payloadString("target_trust")
		sourceTrust := payloadString("source_trust")
		if targetTrust == "user_approved" && (sourceTrust == "inference" || sourceTrust == "model_inference") {
			return fail("invalid trust transition: lower-trust inference cannot replace user-approved memory")
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
		if duplicate == "" || canonical == "" {
			return fail("duplicate and canonical targets are both required")
		}
		if duplicate == canonical {
			return fail("duplicate and canonical targets must be distinct")
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
		if op.ResultingEventID != "" {
			if err := requireNew("supersede result", op.ResultingEventID); err != nil {
				return err
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
