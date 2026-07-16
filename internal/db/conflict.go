package db

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/openlethe/lethe/internal/models"
)

// ConflictDetector performs deterministic semantic conflict detection.
type ConflictDetector struct{ store *Store }

// NewConflictDetector creates a conflict detector.
func NewConflictDetector(s *Store) *ConflictDetector { return &ConflictDetector{store: s} }

// DetectBetween compares two divergent changesets against a common base and
// returns reviewable conflicts. Neither changeset is accepted into a ref; the
// caller is typically a merge-proposal creation path.
func (d *ConflictDetector) DetectBetween(ctx context.Context, projectID, baseID, leftID, rightID string) ([]*models.MemoryConflict, error) {
	if projectID == "" || leftID == "" || rightID == "" {
		return nil, errors.New("project_id, left_changeset_id, and right_changeset_id required")
	}

	base, err := d.store.GetChangeset(ctx, baseID)
	if err != nil {
		return nil, fmt.Errorf("load base: %w", err)
	}
	left, err := d.store.GetChangeset(ctx, leftID)
	if err != nil {
		return nil, fmt.Errorf("load left: %w", err)
	}
	right, err := d.store.GetChangeset(ctx, rightID)
	if err != nil {
		return nil, fmt.Errorf("load right: %w", err)
	}
	for _, cs := range []*models.MemoryChangeset{base, left, right} {
		if cs.ProjectID != projectID {
			return nil, fmt.Errorf("changeset %s project mismatch: %s vs %s", cs.ChangesetID, cs.ProjectID, projectID)
		}
	}

	leftHistory, err := d.store.memoryHistoryAt(ctx, projectID, left.ChangesetID)
	if err != nil {
		return nil, err
	}
	rightHistory, err := d.store.memoryHistoryAt(ctx, projectID, right.ChangesetID)
	if err != nil {
		return nil, err
	}
	leftIDs := changesetIDs(leftHistory)
	rightIDs := changesetIDs(rightHistory)
	leftHasBase := leftIDs[base.ChangesetID]
	rightHasBase := rightIDs[base.ChangesetID]

	commonHistory := make([]*models.MemoryChangeset, 0)
	leftUnique := make([]*models.MemoryChangeset, 0)
	for _, cs := range leftHistory {
		if rightIDs[cs.ChangesetID] {
			commonHistory = append(commonHistory, cs)
		} else {
			leftUnique = append(leftUnique, cs)
		}
	}
	rightUnique := make([]*models.MemoryChangeset, 0)
	for _, cs := range rightHistory {
		if !leftIDs[cs.ChangesetID] {
			rightUnique = append(rightUnique, cs)
		}
	}

	// Compare projected current semantics, not every historical operation.
	// Superseded values in the accepted history must not create conflicts.
	commonState := projectConflictState(commonHistory)
	leftState := cloneConflictState(commonState)
	applyConflictHistory(leftState, leftUnique)
	rightState := cloneConflictState(commonState)
	applyConflictHistory(rightState, rightUnique)
	commonCompare := conflictStateChanges(base, nil, commonState)
	leftCompare := conflictStateChanges(left, commonState, leftState)
	rightCompare := conflictStateChanges(right, commonState, rightState)

	var conflicts []*models.MemoryConflict

	// 1. Stale base / non-fast-forward. A base may be any ancestor, not only
	// the direct parent of a multi-commit branch.
	if !leftHasBase || !rightHasBase {
		conflicts = append(conflicts, &models.MemoryConflict{
			ProjectID:        projectID,
			BaseChangesetID:  baseID,
			LeftChangesetID:  leftID,
			RightChangesetID: rightID,
			ConflictType:     "stale_base",
			Severity:         "warning",
			Summary:          "One or both branches diverged from the expected base; rebase may be required",
			Details: map[string]any{
				"left_parents":  left.ParentIDs,
				"right_parents": right.ParentIDs,
				"expected_base": baseID,
			},
		})
	}

	// Pairwise semantic checks cover each branch against the accepted common
	// history as well as the two divergent branch deltas against each other.
	// Shared post-base commits live only in commonCompare, so they are never
	// compared with themselves.
	conflicts = append(conflicts, d.detectPairwise(commonCompare, leftCompare)...)
	conflicts = append(conflicts, d.detectPairwise(commonCompare, rightCompare)...)
	conflicts = append(conflicts, d.detectPairwise(leftCompare, rightCompare)...)

	// Unary policy checks run on their originating changeset, not an aggregate,
	// so actor/topic metadata remains attached to the operation that declared it.
	changed := append(append([]*models.MemoryChangeset{}, leftUnique...), rightUnique...)
	conflicts = append(conflicts, d.detectBoundaryViolations(changed, leftID, rightID)...)
	conflicts = append(conflicts, d.detectScopeFlow(changed, leftID, rightID)...)
	conflicts = append(conflicts, d.detectTrustDowngrade(changed, leftID, rightID)...)

	for _, conflict := range conflicts {
		conflict.ProjectID = projectID
		conflict.BaseChangesetID = baseID
		conflict.LeftChangesetID = leftID
		conflict.RightChangesetID = rightID
		conflict.Status = "open"
		conflict.ConflictID = DeterministicConflictID(conflict)
	}

	return conflicts, nil
}

func changesetIDs(history []*models.MemoryChangeset) map[string]bool {
	ids := make(map[string]bool, len(history))
	for _, cs := range history {
		ids[cs.ChangesetID] = true
	}
	return ids
}

type conflictState map[string]models.MemorySemanticOp

func projectConflictState(history []*models.MemoryChangeset) conflictState {
	state := make(conflictState)
	applyConflictHistory(state, history)
	return state
}

func cloneConflictState(source conflictState) conflictState {
	clone := make(conflictState, len(source))
	for id, op := range source {
		op.Payload = clonePayload(op.Payload)
		clone[id] = op
	}
	return clone
}

func applyConflictHistory(state conflictState, history []*models.MemoryChangeset) {
	for _, cs := range history {
		for _, op := range cs.Ops {
			switch op.OpType {
			case models.OpAddMemory:
				id := resultingMemoryID(cs.ChangesetID, op)
				op.ResultingEventID = id
				op.Payload = clonePayload(op.Payload)
				state[id] = op
			case models.OpCorrectMemory:
				targetID := op.TargetEventID
				if targetID == "" {
					targetID = resultingMemoryID(cs.ChangesetID, op)
				}
				current, ok := state[targetID]
				if !ok {
					continue
				}
				resultID := op.ResultingEventID
				if resultID == "" {
					resultID = targetID
				}
				payload := clonePayload(current.Payload)
				for key, value := range op.Payload {
					payload[key] = value
				}
				delete(state, targetID)
				state[resultID] = models.MemorySemanticOp{
					OpType: models.OpAddMemory, ResultingEventID: resultID, Payload: payload,
				}
			case models.OpSupersedeMemory:
				delete(state, op.TargetEventID)
				if content := stringPayload(op.Payload, "content"); content != "" {
					id := resultingMemoryID(cs.ChangesetID, op)
					state[id] = models.MemorySemanticOp{
						OpType: models.OpAddMemory, ResultingEventID: id, Payload: clonePayload(op.Payload),
					}
				}
			case models.OpMarkDuplicate:
				id := op.TargetEventID
				if id == "" {
					id = stringPayload(op.Payload, "duplicate_id")
				}
				delete(state, id)
			}
		}
	}
}

func conflictStateChanges(template *models.MemoryChangeset, base, current conflictState) *models.MemoryChangeset {
	result := *template
	result.Ops = nil
	ids := make([]string, 0, len(current))
	for id := range current {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		op := current[id]
		if previous, ok := base[id]; ok && reflect.DeepEqual(previous.Payload, op.Payload) {
			continue
		}
		result.Ops = append(result.Ops, op)
	}
	return &result
}

func (d *ConflictDetector) detectPairwise(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	if len(left.Ops) == 0 || len(right.Ops) == 0 {
		return nil
	}
	var conflicts []*models.MemoryConflict
	conflicts = append(conflicts, d.detectDuplicates(left, right)...)
	conflicts = append(conflicts, d.detectDecisionConflicts(left, right)...)
	conflicts = append(conflicts, d.detectFactConflicts(left, right)...)
	return conflicts
}

func (d *ConflictDetector) detectDuplicates(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	leftAdds := d.extractAddOps(left)
	rightAdds := d.extractAddOps(right)
	for _, la := range leftAdds {
		for _, ra := range rightAdds {
			if la == "" || ra == "" {
				continue
			}
			if strings.TrimSpace(la) == strings.TrimSpace(ra) {
				out = append(out, &models.MemoryConflict{
					ProjectID:        left.ProjectID,
					LeftChangesetID:  left.ChangesetID,
					RightChangesetID: right.ChangesetID,
					ConflictType:     "duplicate_content",
					Severity:         "info",
					Summary:          "Duplicate semantic content detected in both branches",
					Details: map[string]any{
						"duplicate_content": la,
						"left_changeset":    left.ChangesetID,
						"right_changeset":   right.ChangesetID,
					},
				})
			}
		}
	}
	return out
}

func (d *ConflictDetector) detectDecisionConflicts(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	leftDecisions := d.extractDecisions(left)
	rightDecisions := d.extractDecisions(right)
	for _, ld := range leftDecisions {
		for _, rd := range rightDecisions {
			if ld.scope == "" || rd.scope == "" {
				continue
			}
			if ld.scope != rd.scope {
				continue
			}
			conflictType := "contradictory_decision"
			if ld.protected || rd.protected {
				conflictType = "protected_decision"
			}
			if ld.eventID == "" && rd.eventID == "" {
				// Both synthetic ops without materialized event IDs.
				if ld.content != rd.content {
					out = append(out, &models.MemoryConflict{
						ProjectID:        left.ProjectID,
						LeftChangesetID:  left.ChangesetID,
						RightChangesetID: right.ChangesetID,
						ConflictType:     conflictType,
						Severity:         "blocking",
						Summary:          fmt.Sprintf("Incompatible decisions for scope %s", ld.scope),
						Details: map[string]any{
							"scope":         ld.scope,
							"left_content":  ld.content,
							"right_content": rd.content,
							"note":          "synthetic ops without materialized event IDs",
						},
					})
				}
				continue
			}
			if ld.eventID != rd.eventID && !d.sharesLineage(left, right, ld.eventID, rd.eventID) {
				out = append(out, &models.MemoryConflict{
					ProjectID:        left.ProjectID,
					LeftChangesetID:  left.ChangesetID,
					RightChangesetID: right.ChangesetID,
					ConflictType:     conflictType,
					Severity:         "blocking",
					Summary:          fmt.Sprintf("Incompatible decisions for scope %s", ld.scope),
					Details: map[string]any{
						"scope":         ld.scope,
						"left_event":    ld.eventID,
						"right_event":   rd.eventID,
						"left_content":  ld.content,
						"right_content": rd.content,
					},
				})
			}
		}
	}
	return out
}

func (d *ConflictDetector) detectBoundaryViolations(changesets []*models.MemoryChangeset, leftID, rightID string) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range changesets {
		for _, op := range cs.Ops {
			payloadProject, _ := op.Payload["project_id"].(string)
			payloadTopic, _ := op.Payload["topic_id"].(string)
			payloadActor, _ := op.Payload["actor_id"].(string)
			if payloadProject != "" && payloadProject != cs.ProjectID {
				out = append(out, &models.MemoryConflict{
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  leftID,
					RightChangesetID: rightID,
					ConflictType:     "boundary_violation",
					Severity:         "blocking",
					Summary:          fmt.Sprintf("Operation references project %s but changeset is in project %s", payloadProject, cs.ProjectID),
					Details: map[string]any{
						"changeset_id":    cs.ChangesetID,
						"payload_project": payloadProject,
						"cs_project":      cs.ProjectID,
					},
				})
			}
			if payloadTopic != "" && cs.TopicID != "" && payloadTopic != cs.TopicID {
				out = append(out, &models.MemoryConflict{
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  leftID,
					RightChangesetID: rightID,
					ConflictType:     "boundary_violation",
					Severity:         "warning",
					Summary:          fmt.Sprintf("Operation references topic %s but changeset topic is %s", payloadTopic, cs.TopicID),
					Details: map[string]any{
						"changeset_id":  cs.ChangesetID,
						"payload_topic": payloadTopic,
						"cs_topic":      cs.TopicID,
					},
				})
			}
			if payloadActor != "" && cs.ActorID != "" && payloadActor != cs.ActorID {
				out = append(out, &models.MemoryConflict{
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  leftID,
					RightChangesetID: rightID,
					ConflictType:     "boundary_violation",
					Severity:         "warning",
					Summary:          fmt.Sprintf("Operation references actor %s but changeset actor is %s", payloadActor, cs.ActorID),
					Details: map[string]any{
						"changeset_id":  cs.ChangesetID,
						"payload_actor": payloadActor,
						"cs_actor":      cs.ActorID,
					},
				})
			}
		}
	}
	return out
}

func (d *ConflictDetector) detectScopeFlow(changesets []*models.MemoryChangeset, leftID, rightID string) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range changesets {
		for _, op := range cs.Ops {
			fromVis, fromOK := op.Payload["from_visibility"].(string)
			toVis, toOK := op.Payload["to_visibility"].(string)
			if fromOK && toOK && fromVis != "" && toVis != "" {
				if d.isNarrower(fromVis, toVis) {
					out = append(out, &models.MemoryConflict{
						ProjectID:        cs.ProjectID,
						LeftChangesetID:  leftID,
						RightChangesetID: rightID,
						ConflictType:     "scope_flow",
						Severity:         "warning",
						Summary:          fmt.Sprintf("Potential private-to-public flow: %s → %s", fromVis, toVis),
						Details: map[string]any{
							"changeset_id":    cs.ChangesetID,
							"from_visibility": fromVis,
							"to_visibility":   toVis,
							"op_type":         string(op.OpType),
						},
					})
				}
			}
		}
	}
	return out
}

func (d *ConflictDetector) extractAddOps(cs *models.MemoryChangeset) []string {
	var out []string
	for _, op := range cs.Ops {
		if op.OpType == models.OpAddMemory || op.OpType == models.OpCorrectMemory || op.OpType == models.OpSupersedeMemory {
			if c, ok := op.Payload["content"].(string); ok && c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

type decisionEntry struct {
	scope     string
	content   string
	eventID   string
	protected bool
}

func (d *ConflictDetector) extractDecisions(cs *models.MemoryChangeset) []decisionEntry {
	var out []decisionEntry
	for _, op := range cs.Ops {
		if op.OpType == models.OpAddMemory || op.OpType == models.OpCorrectMemory || op.OpType == models.OpSupersedeMemory {
			content, _ := op.Payload["content"].(string)
			if content == "" {
				continue
			}
			if strings.Contains(strings.ToLower(content), "decision:") || op.Payload["kind"] == "decision" {
				scope, _ := op.Payload["scope"].(string)
				if scope == "" {
					scope, _ = op.Payload["project_id"].(string)
				}
				if scope == "" {
					scope = cs.ProjectID
				}
				out = append(out, decisionEntry{
					scope:     scope,
					content:   content,
					eventID:   op.ResultingEventID,
					protected: boolValue(op.Payload["protected"]) || stringValue(op.Payload["approval"]) == "user_approved",
				})
			}
		}
	}
	return out
}

type factEntry struct {
	key, scope, content, validFrom, validTo string
}

func (d *ConflictDetector) detectFactConflicts(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	leftFacts := extractFacts(left)
	rightFacts := extractFacts(right)
	var out []*models.MemoryConflict
	for _, lf := range leftFacts {
		for _, rf := range rightFacts {
			if lf.key == "" || lf.key != rf.key || lf.scope != rf.scope || lf.content == rf.content {
				continue
			}
			if !validityOverlaps(lf.validFrom, lf.validTo, rf.validFrom, rf.validTo) {
				continue
			}
			out = append(out, &models.MemoryConflict{
				ProjectID:       left.ProjectID,
				LeftChangesetID: left.ChangesetID, RightChangesetID: right.ChangesetID,
				ConflictType: "incompatible_fact", Severity: "blocking",
				Summary: fmt.Sprintf("Incompatible accepted facts for %s in scope %s", lf.key, lf.scope),
				Details: map[string]any{"fact_key": lf.key, "scope": lf.scope, "left_content": lf.content, "right_content": rf.content},
			})
		}
	}
	return out
}

func extractFacts(cs *models.MemoryChangeset) []factEntry {
	var out []factEntry
	for _, op := range cs.Ops {
		if op.OpType != models.OpAddMemory && op.OpType != models.OpCorrectMemory && op.OpType != models.OpSupersedeMemory {
			continue
		}
		if stringValue(op.Payload["kind"]) != "fact" {
			continue
		}
		key := stringValue(op.Payload["fact_key"])
		if key == "" {
			key = stringValue(op.Payload["subject"])
		}
		scope := stringValue(op.Payload["scope"])
		if scope == "" {
			scope = cs.ProjectID
		}
		out = append(out, factEntry{key: key, scope: scope, content: stringValue(op.Payload["content"]),
			validFrom: stringValue(op.Payload["valid_from"]), validTo: stringValue(op.Payload["valid_to"])})
	}
	return out
}

func (d *ConflictDetector) detectTrustDowngrade(changesets []*models.MemoryChangeset, leftID, rightID string) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range changesets {
		for _, op := range cs.Ops {
			if op.OpType != models.OpCorrectMemory && op.OpType != models.OpSupersedeMemory {
				continue
			}
			targetTrust := stringValue(op.Payload["target_trust"])
			sourceTrust := stringValue(op.Payload["source_trust"])
			if targetTrust == "user_approved" && (sourceTrust == "inference" || sourceTrust == "model_inference") {
				out = append(out, &models.MemoryConflict{
					ProjectID:       cs.ProjectID,
					LeftChangesetID: leftID, RightChangesetID: rightID,
					ConflictType: "trust_downgrade", Severity: "blocking",
					Summary: "Lower-trust inference would replace user-approved memory",
					Details: map[string]any{"changeset_id": cs.ChangesetID, "target_event_id": op.TargetEventID,
						"target_trust": targetTrust, "source_trust": sourceTrust},
				})
			}
		}
	}
	return out
}

func validityOverlaps(aFrom, aTo, bFrom, bTo string) bool {
	// RFC3339 timestamps sort lexicographically. Empty endpoints are unbounded.
	return (aTo == "" || bFrom == "" || aTo >= bFrom) && (bTo == "" || aFrom == "" || bTo >= aFrom)
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func boolValue(v any) bool {
	b, _ := v.(bool)
	return b
}

// sharesLineage checks if two events share a correction/supersede chain.
// For V1, a simple heuristic: if either event ID appears as target of a
// correct/supersede in either changeset, they share lineage.
func (d *ConflictDetector) sharesLineage(left, right *models.MemoryChangeset, leftEventID, rightEventID string) bool {
	if leftEventID == "" || rightEventID == "" {
		return false
	}
	if leftEventID == rightEventID {
		return true
	}
	for _, cs := range []*models.MemoryChangeset{left, right} {
		for _, op := range cs.Ops {
			if op.OpType == models.OpCorrectMemory || op.OpType == models.OpSupersedeMemory {
				if (op.TargetEventID == leftEventID && op.ResultingEventID == rightEventID) ||
					(op.TargetEventID == rightEventID && op.ResultingEventID == leftEventID) {
					return true
				}
			}
		}
	}
	return false
}

func (d *ConflictDetector) isNarrower(fromVis, toVis string) bool {
	order := map[string]int{"private": 0, "internal": 1, "shared": 2, "public": 3}
	return order[fromVis] < order[toVis]
}
