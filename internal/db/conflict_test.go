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

func TestConflictDetectorMinimumV1Classes(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-min")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-min", "system")
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID: "proj-min", RefName: "refs/agents/left/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "left-min", Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, Payload: map[string]any{"kind": "fact", "fact_key": "cluster_version", "scope": "prod", "content": "1.31", "valid_from": "2026-07-01T00:00:00Z"}},
			{OpType: models.OpAddMemory, Payload: map[string]any{"kind": "decision", "scope": "network", "content": "Decision: private ingress", "protected": true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID: "proj-min", RefName: "refs/agents/right/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "right-min", Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, Payload: map[string]any{"kind": "fact", "fact_key": "cluster_version", "scope": "prod", "content": "1.32", "valid_from": "2026-07-10T00:00:00Z"}},
			{OpType: models.OpAddMemory, Payload: map[string]any{"kind": "decision", "scope": "network", "content": "Decision: public ingress"}},
			{OpType: models.OpCorrectMemory, TargetEventID: "evt-user", Payload: map[string]any{"content": "inferred replacement", "target_trust": "user_approved", "source_trust": "model_inference"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err := NewConflictDetector(s).DetectBetween(context.Background(), "proj-min", root.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, conflict := range conflicts {
		types[conflict.ConflictType] = true
	}
	for _, want := range []string{"incompatible_fact", "protected_decision", "trust_downgrade"} {
		if !types[want] {
			t.Errorf("missing %s conflict: %#v", want, types)
		}
	}
}
