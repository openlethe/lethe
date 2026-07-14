package db

import (
	"context"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

// TestMemoryGitV1Acceptance exercises the full 16-step lifecycle from
// Memory Git V1 against a real Lethe database.
func TestMemoryGitV1Acceptance(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	projectID := "acceptance-test"

	// Create project first (required for foreign keys)
	if err := s.UpsertProject(ctx, &models.Project{ProjectID: projectID, Name: "Acceptance Test"}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// --- Step 1: Initialize refs with a legacy root changeset ---
	mainRef := "refs/shared/main"
	s1Ref := "refs/sessions/claude/s1"
	s2Ref := "refs/sessions/chatgpt/s2"

	rootCs, rootRef, err := s.EnsureLegacyRoot(ctx, projectID, "root")
	if err != nil {
		t.Fatalf("ensure legacy root: %v", err)
	}
	_ = rootRef

	// Create model session branches from root
	if _, err := s.CreateMemoryBranch(ctx, projectID, s1Ref, rootCs.ChangesetID, "root", false); err != nil {
		t.Fatalf("create s1 ref: %v", err)
	}
	if _, err := s.CreateMemoryBranch(ctx, projectID, s2Ref, rootCs.ChangesetID, "root", false); err != nil {
		t.Fatalf("create s2 ref: %v", err)
	}

	// --- Step 2-3: Commit initial changesets onto main ---
	csA, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID:       projectID,
		RefName:         mainRef,
		ParentIDs:       []string{rootCs.ChangesetID},
		Message:         "Initial project guidelines",
		AuthorPrincipal: "root",
		ActorID:         "root",
		Ops: []models.MemorySemanticOp{{
			OpType:           models.OpAddMemory,
			ResultingEventID: "evt-001",
			Payload:          map[string]interface{}{"content": "Be helpful"},
		}},
		IdempotencyKey:     "init-001",
		AdvanceRef:         true,
		CreateRefIfMissing: true,
		ExpectedHead:       rootCs.ChangesetID,
	})
	if err != nil {
		t.Fatalf("create csA: %v", err)
	}

	csB, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID:       projectID,
		RefName:         mainRef,
		ParentIDs:       []string{csA.ChangesetID},
		Message:         "Add privacy guideline",
		AuthorPrincipal: "root",
		ActorID:         "root",
		Ops: []models.MemorySemanticOp{{
			OpType:           models.OpAddMemory,
			ResultingEventID: "evt-002",
			Payload:          map[string]interface{}{"content": "Never share secrets"},
		}},
		IdempotencyKey:     "init-002",
		AdvanceRef:         true,
		CreateRefIfMissing: true,
		ExpectedHead:       csA.ChangesetID,
	})
	if err != nil {
		t.Fatalf("create csB: %v", err)
	}

	// --- Step 4-5: Model 1 branches from B ---
	if _, err := s.CASUpdateRef(ctx, projectID, s1Ref, rootCs.ChangesetID, csB.ChangesetID); err != nil {
		t.Fatalf("advance s1 to B: %v", err)
	}

	csM1, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID:       projectID,
		RefName:         s1Ref,
		ParentIDs:       []string{csB.ChangesetID},
		Message:         "Add deployment decision",
		AuthorPrincipal: "m1",
		ActorID:         "claude",
		Ops: []models.MemorySemanticOp{{
			OpType:           models.OpAddMemory,
			ResultingEventID: "evt-003",
			Payload:          map[string]interface{}{"content": "Use Cloudflare Tunnels"},
		}},
		IdempotencyKey:     "m1-001",
		AdvanceRef:         true,
		CreateRefIfMissing: true,
		ExpectedHead:       csB.ChangesetID,
	})
	if err != nil {
		t.Fatalf("create csM1: %v", err)
	}

	// --- Step 6-7: Model 2 branches from B (conflicting) ---
	if _, err := s.CASUpdateRef(ctx, projectID, s2Ref, rootCs.ChangesetID, csB.ChangesetID); err != nil {
		t.Fatalf("advance s2 to B: %v", err)
	}

	csM2, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID:       projectID,
		RefName:         s2Ref,
		ParentIDs:       []string{csB.ChangesetID},
		Message:         "Add deployment decision (conflict)",
		AuthorPrincipal: "m2",
		ActorID:         "chatgpt",
		Ops: []models.MemorySemanticOp{{
			OpType:           models.OpAddMemory,
			ResultingEventID: "evt-004",
			Payload:          map[string]interface{}{"content": "Use Tailscale Funnel"},
		}},
		IdempotencyKey:     "m2-001",
		AdvanceRef:         true,
		CreateRefIfMissing: true,
		ExpectedHead:       csB.ChangesetID,
	})
	if err != nil {
		t.Fatalf("create csM2: %v", err)
	}

	// --- Step 8-9: Conflict detection ---
	conflict := &models.MemoryConflict{
		ProjectID:        projectID,
		LeftChangesetID:  csM2.ChangesetID,
		RightChangesetID: csM1.ChangesetID,
		Severity:         "blocking",
		Summary:          "M1 and M2 both added deployment decisions",
		Status:           "open",
	}
	if err := s.CreateConflict(ctx, conflict); err != nil {
		t.Fatalf("create conflict: %v", err)
	}

	// --- Step 12-13: Human approves and fast-forwards main to M1 ---
	updatedRef, err := s.CASUpdateRef(ctx, projectID, mainRef, csB.ChangesetID, csM1.ChangesetID)
	if err != nil {
		t.Fatalf("fast-forward main to M1: %v", err)
	}
	if updatedRef.HeadChangesetID != csM1.ChangesetID {
		t.Fatalf("CASUpdateRef returned wrong head: %s", updatedRef.HeadChangesetID)
	}
	mainRefHead, _ := s.GetMemoryRef(ctx, projectID, mainRef)
	if mainRefHead != nil {
		t.Logf("main ref head after CAS: %s", mainRefHead.HeadChangesetID)
	}

	// --- Step 14-15: Model 2 rebases and resolves ---
	csM2Rebased, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID:       projectID,
		RefName:         s2Ref,
		ParentIDs:       []string{csM1.ChangesetID}, // rebase onto M1
		Message:         "Rebase: accept M1's deployment, add monitoring",
		AuthorPrincipal: "m2",
		ActorID:         "chatgpt",
		Ops: []models.MemorySemanticOp{{
			OpType:           models.OpAddMemory,
			ResultingEventID: "evt-005",
			Payload:          map[string]interface{}{"content": "Monitoring: Grafana + Prometheus"},
		}},
		IdempotencyKey:     "m2-002",
		AdvanceRef:         true,
		CreateRefIfMissing: true,
		ExpectedHead:       csM2.ChangesetID, // s2 head is currently M2-original
	})
	if err != nil {
		t.Fatalf("create rebased M2: %v", err)
	}

	// --- Step 16: Verify linear history (newest first) ---
	log, err := s.ListChangesets(ctx, projectID, mainRef, 10)
	if err != nil {
		t.Fatalf("list main log: %v", err)
	}
	for i, cs := range log {
		t.Logf("main log[%d]: %s - %s", i, cs.ChangesetID, cs.Message)
	}
	if len(log) != 4 {
		t.Fatalf("expected 4 changesets on main, got %d", len(log))
	}
	if log[0].ChangesetID != csM1.ChangesetID {
		t.Fatalf("expected first (newest) changeset to be M1 (%s), got %s", csM1.ChangesetID, log[0].ChangesetID)
	}
	if log[3].ChangesetID != rootCs.ChangesetID {
		t.Fatalf("expected last (oldest) changeset to be root (%s), got %s", rootCs.ChangesetID, log[3].ChangesetID)
	}

	// Verify S2 has rebased history (newest first: M2-rebased, M1, B, A, root)
	s2Log, err := s.ListChangesets(ctx, projectID, s2Ref, 10)
	if err != nil {
		t.Fatalf("list s2 log: %v", err)
	}
	if len(s2Log) != 5 {
		t.Fatalf("expected 5 changesets on s2, got %d", len(s2Log))
	}
	if s2Log[0].ChangesetID != csM2Rebased.ChangesetID {
		t.Fatalf("expected first s2 changeset to be rebased M2, got %s", s2Log[0].ChangesetID)
	}
	if s2Log[4].ChangesetID != rootCs.ChangesetID {
		t.Fatalf("expected last s2 changeset to be root, got %s", s2Log[4].ChangesetID)
	}

	// Verify diff between M2-original and M2-rebased shows the rebase
	diff, err := s.DiffChangesets(ctx, projectID, csM1.ChangesetID, csM2Rebased.ChangesetID)
	if err != nil {
		t.Fatalf("diff rebased M2: %v", err)
	}
	if diff.TargetChangesetID != csM2Rebased.ChangesetID {
		t.Fatalf("diff target mismatch")
	}
	if diff.BaseChangesetID != csM1.ChangesetID {
		t.Fatalf("diff base mismatch: expected %s, got %s", csM1.ChangesetID, diff.BaseChangesetID)
	}

	// Verify all refs
	refs, err := s.ListMemoryRefs(ctx, projectID)
	if err != nil {
		t.Fatalf("list refs: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %d", len(refs))
	}

	t.Logf("Memory Git V1 acceptance test passed: %d changesets on main, %d on s2, %d refs, 1 conflict recorded",
		len(log), len(s2Log), len(refs))
}
