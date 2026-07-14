package db

import (
	"context"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

func TestConflictDetector(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-conf")

	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-conf", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	left, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-conf",
		RefName:         "refs/sessions/chatgpt/s1",
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p_chatgpt",
		Message:         "chatgpt decision",
		IdempotencyKey:  "conf-left",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory,
			Payload: map[string]any{
				"content": "Decision: use OAuth for MCP",
				"kind":    "decision",
				"scope":   "auth",
			},
		}},
	})
	if err != nil {
		t.Fatalf("left: %v", err)
	}

	right, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-conf",
		RefName:         "refs/sessions/archimedes/s1",
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p_arch",
		Message:         "arch decision",
		IdempotencyKey:  "conf-right",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory,
			Payload: map[string]any{
				"content": "Decision: use session tokens for MCP",
				"kind":    "decision",
				"scope":   "auth",
			},
		}},
	})
	if err != nil {
		t.Fatalf("right: %v", err)
	}

	det := NewConflictDetector(s)
	conflicts, err := det.DetectBetween(context.Background(), "proj-conf", root.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	var hasDecisionConflict, hasDuplicate bool
	for _, c := range conflicts {
		switch c.ConflictType {
		case "contradictory_decision":
			hasDecisionConflict = true
			if c.Severity != "blocking" {
				t.Fatalf("expected blocking severity for contradictory_decision, got %s", c.Severity)
			}
		case "duplicate_content":
			hasDuplicate = true
		}
	}
	if !hasDecisionConflict {
		t.Fatalf("expected contradictory_decision conflict, got %v", conflicts)
	}

	// Duplicate test
	leftDup, _ := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-conf",
		RefName:         "refs/sessions/chatgpt/s2",
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p_chatgpt",
		Message:         "dup",
		IdempotencyKey:  "conf-dup-left",
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "Same exact content"},
		}},
	})
	rightDup, _ := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-conf",
		RefName:         "refs/sessions/archimedes/s2",
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p_arch",
		Message:         "dup",
		IdempotencyKey:  "conf-dup-right",
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "Same exact content"},
		}},
	})
	conflicts2, err := det.DetectBetween(context.Background(), "proj-conf", root.ChangesetID, leftDup.ChangesetID, rightDup.ChangesetID)
	if err != nil {
		t.Fatalf("detect dup: %v", err)
	}
	for _, c := range conflicts2 {
		if c.ConflictType == "duplicate_content" {
			hasDuplicate = true
		}
	}
	if !hasDuplicate {
		t.Fatalf("expected duplicate_content, got %v", conflicts2)
	}
}
