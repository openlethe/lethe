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
			{OpType: models.OpAddMemory, ResultingEventID: "evt-user", Payload: map[string]any{"content": "user-approved baseline"}},
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

func TestConflictDetectorUsesCompleteBranchDelta(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-delta", "proj-delta")
	root, _, err := s.EnsureLegacyRoot(ctx, "proj-delta", "system")
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-delta", RefName: "refs/shared/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "delta-base",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "accepted-auth", Payload: map[string]any{
			"content": "Decision: require OAuth", "kind": "decision", "scope": "auth", "protected": true,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-delta", RefName: "refs/agents/chatgpt/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "chatgpt", IdempotencyKey: "delta-first",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "proposed-auth", Payload: map[string]any{
			"content": "Decision: disable OAuth", "kind": "decision", "scope": "auth",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tip, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-delta", RefName: "refs/agents/chatgpt/main", ParentIDs: []string{first.ChangesetID},
		AuthorPrincipal: "chatgpt", IdempotencyKey: "delta-tip",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAttachEvidence, TargetEventID: "proposed-auth", Payload: map[string]any{
			"summary": "implementation note",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, "proj-delta", base.ChangesetID, base.ChangesetID, tip.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	seenDecision := false
	for _, conflict := range conflicts {
		if conflict.ConflictType == "stale_base" {
			t.Errorf("multi-commit descendant was incorrectly marked stale")
		}
		if conflict.ConflictType == "protected_decision" {
			seenDecision = true
		}
	}
	if !seenDecision {
		t.Fatalf("earlier branch operation was not checked: %#v", conflicts)
	}
}

func TestConflictDetectorChecksAcceptedBaseWhenBothBranchesChanged(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-base-check", "proj-base-check")
	root, _, err := s.EnsureLegacyRoot(ctx, "proj-base-check", "system")
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-base-check", RefName: "refs/shared/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "base-check-base",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "accepted-auth", Payload: map[string]any{
			"content": "Decision: require OAuth", "kind": "decision", "scope": "auth", "protected": true,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-base-check", RefName: "refs/agents/left/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "base-check-left",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "left-auth", Payload: map[string]any{
			"content": "Decision: disable OAuth", "kind": "decision", "scope": "auth",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-base-check", RefName: "refs/agents/right/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "base-check-right",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "right-observation", Payload: map[string]any{
			"content": "Observation: OAuth rollout scheduled", "kind": "observation", "scope": "delivery",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, "proj-base-check", base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range conflicts {
		if conflict.ConflictType == "protected_decision" {
			return
		}
	}
	t.Fatalf("branch contradiction with accepted base was missed: %#v", conflicts)
}

func TestConflictDetectorExcludesSharedPostBaseHistory(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-common", "proj-common")
	root, _, err := s.EnsureLegacyRoot(ctx, "proj-common", "system")
	if err != nil {
		t.Fatal(err)
	}
	common, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-common", RefName: "refs/shared/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "common-shared",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, ResultingEventID: "shared-memory", Payload: map[string]any{
			"content": "Shared accepted content", "kind": "record",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-common", RefName: "refs/agents/left/main", ParentIDs: []string{common.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "common-left",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, Payload: map[string]any{"content": "left only"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-common", RefName: "refs/agents/right/main", ParentIDs: []string{common.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "common-right",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, Payload: map[string]any{"content": "right only"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, "proj-common", root.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range conflicts {
		if conflict.ConflictType == "duplicate_content" {
			t.Fatalf("shared post-base operation was compared with itself: %#v", conflicts)
		}
	}
}

func TestConflictDetectorPreservesOriginatingMetadata(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-metadata", "proj-metadata")
	root, _, err := s.EnsureLegacyRoot(ctx, "proj-metadata", "system")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-metadata", RefName: "refs/agents/left/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "left", ActorID: "actor-a", TopicID: "topic-a", IdempotencyKey: "metadata-first",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, Payload: map[string]any{
			"content": "first", "actor_id": "actor-a", "topic_id": "topic-a",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-metadata", RefName: "refs/agents/left/main", ParentIDs: []string{first.ChangesetID},
		AuthorPrincipal: "left", ActorID: "actor-b", TopicID: "topic-b", IdempotencyKey: "metadata-tip",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, Payload: map[string]any{
			"content": "second", "actor_id": "actor-b", "topic_id": "topic-b",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-metadata", RefName: "refs/agents/right/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "metadata-right",
		Ops: []models.MemorySemanticOp{{OpType: models.OpAddMemory, Payload: map[string]any{"content": "right"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, "proj-metadata", root.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range conflicts {
		if conflict.ConflictType == "boundary_violation" {
			t.Fatalf("operation was checked against tip metadata instead of its originating changeset: %#v", conflicts)
		}
	}
}

func TestConflictDetectorProjectsAcceptedStateInsteadOfObsoleteHistory(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-projected-base", "proj-projected-base")
	root, _, err := s.EnsureLegacyRoot(ctx, "proj-projected-base", "system")
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-projected-base", RefName: models.RefSharedMain, ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "projected-base-old",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "cluster-version",
			Payload: map[string]any{"kind": "fact", "fact_key": "cluster_version", "scope": "prod", "content": "1.30"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-projected-base", RefName: models.RefSharedMain, ParentIDs: []string{old.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "projected-base-current",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpCorrectMemory, TargetEventID: "cluster-version",
			Payload: map[string]any{"kind": "fact", "fact_key": "cluster_version", "scope": "prod", "content": "1.31"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-projected-base", RefName: "refs/agents/left/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "projected-base-left",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "left-confirmation",
			Payload: map[string]any{"kind": "fact", "fact_key": "cluster_version", "scope": "prod", "content": "1.31"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-projected-base", RefName: "refs/agents/right/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "projected-base-right",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "rollout-note",
			Payload: map[string]any{"kind": "observation", "scope": "delivery", "content": "rollout complete"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, "proj-projected-base", base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range conflicts {
		if conflict.ConflictType == "incompatible_fact" {
			t.Fatalf("obsolete pre-base fact produced a conflict: %#v", conflicts)
		}
	}
}
