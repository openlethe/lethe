package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
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

	var conflicts []*models.MemoryConflict

	// 1. Stale base / non-fast-forward
	if !hasParent(left, baseID) || !hasParent(right, baseID) {
		conflicts = append(conflicts, &models.MemoryConflict{
			ConflictID:       uuid.Must(uuid.NewV7()).String(),
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

	// 2. Duplicate semantic content
	dupConflicts := d.detectDuplicates(left, right)
	conflicts = append(conflicts, dupConflicts...)

	// 3. Incompatible decisions in same scope
	decisionConflicts := d.detectDecisionConflicts(left, right)
	conflicts = append(conflicts, decisionConflicts...)

	// 4. Incompatible accepted facts with overlapping validity.
	factConflicts := d.detectFactConflicts(left, right)
	conflicts = append(conflicts, factConflicts...)

	// 5. Boundary violations (project, topic, actor)
	boundaryConflicts := d.detectBoundaryViolations(left, right)
	conflicts = append(conflicts, boundaryConflicts...)

	// 6. Private-to-broader scope flow
	scopeConflicts := d.detectScopeFlow(left, right)
	conflicts = append(conflicts, scopeConflicts...)

	// 7. User-approved memory replaced by lower-trust inference.
	conflicts = append(conflicts, d.detectTrustDowngrade(left, right)...)

	return conflicts, nil
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
					ConflictID:       uuid.Must(uuid.NewV7()).String(),
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
						ConflictID:       uuid.Must(uuid.NewV7()).String(),
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
					ConflictID:       uuid.Must(uuid.NewV7()).String(),
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

func (d *ConflictDetector) detectBoundaryViolations(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range []*models.MemoryChangeset{left, right} {
		for _, op := range cs.Ops {
			payloadProject, _ := op.Payload["project_id"].(string)
			payloadTopic, _ := op.Payload["topic_id"].(string)
			payloadActor, _ := op.Payload["actor_id"].(string)
			if payloadProject != "" && payloadProject != cs.ProjectID {
				out = append(out, &models.MemoryConflict{
					ConflictID:       uuid.Must(uuid.NewV7()).String(),
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  left.ChangesetID,
					RightChangesetID: right.ChangesetID,
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
					ConflictID:       uuid.Must(uuid.NewV7()).String(),
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  left.ChangesetID,
					RightChangesetID: right.ChangesetID,
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
					ConflictID:       uuid.Must(uuid.NewV7()).String(),
					ProjectID:        cs.ProjectID,
					LeftChangesetID:  left.ChangesetID,
					RightChangesetID: right.ChangesetID,
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

func (d *ConflictDetector) detectScopeFlow(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range []*models.MemoryChangeset{left, right} {
		for _, op := range cs.Ops {
			fromVis, fromOK := op.Payload["from_visibility"].(string)
			toVis, toOK := op.Payload["to_visibility"].(string)
			if fromOK && toOK && fromVis != "" && toVis != "" {
				if d.isNarrower(fromVis, toVis) {
					out = append(out, &models.MemoryConflict{
						ConflictID:       uuid.Must(uuid.NewV7()).String(),
						ProjectID:        cs.ProjectID,
						LeftChangesetID:  left.ChangesetID,
						RightChangesetID: right.ChangesetID,
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
				ConflictID: uuid.Must(uuid.NewV7()).String(), ProjectID: left.ProjectID,
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

func (d *ConflictDetector) detectTrustDowngrade(left, right *models.MemoryChangeset) []*models.MemoryConflict {
	var out []*models.MemoryConflict
	for _, cs := range []*models.MemoryChangeset{left, right} {
		for _, op := range cs.Ops {
			if op.OpType != models.OpCorrectMemory && op.OpType != models.OpSupersedeMemory {
				continue
			}
			targetTrust := stringValue(op.Payload["target_trust"])
			sourceTrust := stringValue(op.Payload["source_trust"])
			if targetTrust == "user_approved" && (sourceTrust == "inference" || sourceTrust == "model_inference") {
				out = append(out, &models.MemoryConflict{
					ConflictID: uuid.Must(uuid.NewV7()).String(), ProjectID: cs.ProjectID,
					LeftChangesetID: left.ChangesetID, RightChangesetID: right.ChangesetID,
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

func hasParent(cs *models.MemoryChangeset, parentID string) bool {
	for _, parent := range cs.ParentIDs {
		if parent == parentID {
			return true
		}
	}
	return false
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
