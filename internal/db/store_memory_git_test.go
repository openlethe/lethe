package db

import (
	"context"
	"errors"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

func TestMemoryGitLegacyRootAndBranchCAS(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-mg")

	root, ref, err := s.EnsureLegacyRoot(context.Background(), "proj-mg", "system")
	if err != nil {
		t.Fatalf("EnsureLegacyRoot: %v", err)
	}
	if root == nil || ref == nil {
		t.Fatal("expected root changeset and shared ref")
	}
	if ref.RefName != models.RefSharedMain {
		t.Fatalf("ref=%s", ref.RefName)
	}
	if !ref.Protected {
		t.Fatal("shared/main should be protected")
	}

	// Idempotent ensure
	root2, ref2, err := s.EnsureLegacyRoot(context.Background(), "proj-mg", "system")
	if err != nil {
		t.Fatalf("EnsureLegacyRoot 2: %v", err)
	}
	if root2.ChangesetID != root.ChangesetID || ref2.HeadChangesetID != ref.HeadChangesetID {
		t.Fatal("legacy root should be stable")
	}

	chatgptBranch := "refs/sessions/chatgpt/sess-1"
	branch, err := s.CreateMemoryBranch(context.Background(), "proj-mg", chatgptBranch, root.ChangesetID, "principal_chatgpt", false)
	if err != nil {
		t.Fatalf("CreateMemoryBranch: %v", err)
	}
	if branch.HeadChangesetID != root.ChangesetID {
		t.Fatal("branch should start at shared head")
	}

	archBranch := "refs/sessions/archimedes/sess-local"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-mg", archBranch, root.ChangesetID, "principal_arch", false); err != nil {
		t.Fatalf("CreateMemoryBranch arch: %v", err)
	}

	// ChatGPT commit on its session branch
	csChat, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-mg",
		RefName:         chatgptBranch,
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "principal_chatgpt",
		ActorID:         "chatgpt",
		Message:         "chatgpt: note A",
		IdempotencyKey:  "chat-1",
		ExpectedHead:    root.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory,
			Payload: map[string]any{
				"content":    "Decision: use OAuth for MCP",
				"event_type": "record",
				"kind":       "decision",
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateChangeset chatgpt: %v", err)
	}

	// Local Archimedes commit on its branch from same base
	csArch, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-mg",
		RefName:         archBranch,
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "principal_arch",
		ActorID:         "archimedes",
		Message:         "arch: note B",
		IdempotencyKey:  "arch-1",
		ExpectedHead:    root.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory,
			Payload: map[string]any{
				"content":    "Decision: keep Lethe private",
				"event_type": "record",
				"kind":       "decision",
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateChangeset arch: %v", err)
	}

	// Deterministic semantic diff for arch branch
	diff, err := s.DiffChangesets(context.Background(), "proj-mg", root.ChangesetID, csArch.ChangesetID)
	if err != nil {
		t.Fatalf("DiffChangesets: %v", err)
	}
	if len(diff.MemoriesAdded) != 1 {
		t.Fatalf("expected 1 memory added, got %d", len(diff.MemoriesAdded))
	}
	if len(diff.DecisionsChanged) == 0 {
		t.Fatal("expected decision classification")
	}

	// Fast-forward merge arch into shared via CAS
	if _, err := s.CASUpdateRef(context.Background(), "proj-mg", models.RefSharedMain, root.ChangesetID, csArch.ChangesetID); err != nil {
		t.Fatalf("CASUpdateRef shared: %v", err)
	}
	shared, err := s.GetMemoryRef(context.Background(), "proj-mg", models.RefSharedMain)
	if err != nil || shared.HeadChangesetID != csArch.ChangesetID {
		t.Fatalf("shared head not advanced: %v %#v", err, shared)
	}

	// Stale ChatGPT base against shared should CAS-fail if trying to advance shared from old root
	_, err = s.CASUpdateRef(context.Background(), "proj-mg", models.RefSharedMain, root.ChangesetID, csChat.ChangesetID)
	if !errors.Is(err, ErrRefCASConflict) {
		t.Fatalf("expected CAS conflict for stale base, got %v", err)
	}

	// Multi-parent reviewed merge without losing history
	mergeCS, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-mg",
		RefName:         models.RefSharedMain,
		ParentIDs:       []string{csArch.ChangesetID, csChat.ChangesetID},
		AuthorPrincipal: "principal_arch",
		ActorID:         "archimedes",
		Message:         "merge: reviewed multi-parent",
		IdempotencyKey:  "merge-1",
		ExpectedHead:    csArch.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAttachVerification,
			Payload: map[string]any{
				"summary": "human reviewed merge of chatgpt + arch branches",
			},
		}},
	})
	if err != nil {
		t.Fatalf("multi-parent merge: %v", err)
	}
	if len(mergeCS.ParentIDs) != 2 {
		t.Fatalf("expected 2 parents, got %v", mergeCS.ParentIDs)
	}

	// Revert via correcting changeset
	revertCS, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-mg",
		RefName:         models.RefSharedMain,
		ParentIDs:       []string{mergeCS.ChangesetID},
		AuthorPrincipal: "principal_arch",
		Message:         "revert decision A framing",
		IdempotencyKey:  "revert-1",
		ExpectedHead:    mergeCS.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType:        models.OpCorrectMemory,
			TargetEventID: "evt-placeholder-chat",
			Payload: map[string]any{
				"content": "Decision: use OAuth for MCP (corrected wording)",
				"summary": "correct without erase",
			},
		}},
	})
	if err != nil {
		t.Fatalf("revert changeset: %v", err)
	}

	// Manifest pin to pre-revert head remains reproducible
	m := &models.MemoryManifest{
		Direction:         "input",
		ProjectID:         "proj-mg",
		RefName:           models.RefSharedMain,
		HeadChangesetID:   mergeCS.ChangesetID,
		SelectedMemoryIDs: []string{"evt-placeholder-chat"},
		InclusionReasons:  map[string]string{"evt-placeholder-chat": "in accepted view"},
		SessionID:         "sess-local",
		ActorID:           "archimedes",
	}
	if err := s.CreateManifest(context.Background(), m); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	got, err := s.GetManifest(context.Background(), m.ManifestID)
	if err != nil || got == nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if got.HeadChangesetID != mergeCS.ChangesetID {
		t.Fatal("manifest must pin old head even after later revert")
	}

	// Diff after revert shows correction as temporal update
	diff2, err := s.DiffChangesets(context.Background(), "proj-mg", mergeCS.ChangesetID, revertCS.ChangesetID)
	if err != nil {
		t.Fatalf("diff revert: %v", err)
	}
	if len(diff2.Corrections) != 1 || diff2.Corrections[0].Kind != "temporal_update" {
		t.Fatalf("expected temporal correction, got %#v", diff2.Corrections)
	}

	// Idempotent create
	again, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-mg",
		RefName:         chatgptBranch,
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "principal_chatgpt",
		Message:         "chatgpt: note A",
		IdempotencyKey:  "chat-1",
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "Decision: use OAuth for MCP"},
		}},
	})
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if again.ChangesetID != csChat.ChangesetID {
		t.Fatal("idempotency should return same changeset")
	}

	// Cross-project isolation: other project does not see refs
	setupAgentProject(t, s, "agent-1", "other")
	refs, err := s.ListMemoryRefs(context.Background(), "other")
	if err != nil {
		t.Fatalf("ListMemoryRefs other: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected no cross-project refs, got %d", len(refs))
	}
}

func TestMemoryGitCASConcurrentStyle(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-cas")

	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-cas", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	branch := "refs/agents/chatgpt/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-cas", branch, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	first, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-cas",
		RefName:         branch,
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p1",
		Message:         "first",
		IdempotencyKey:  "k1",
		ExpectedHead:    root.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "a"},
		}},
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Second writer still expects old head → clean CAS failure
	_, err = s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID:       "proj-cas",
		RefName:         branch,
		ParentIDs:       []string{root.ChangesetID},
		AuthorPrincipal: "p2",
		Message:         "stale",
		IdempotencyKey:  "k2",
		ExpectedHead:    root.ChangesetID,
		AdvanceRef:      true,
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "b"},
		}},
	})
	if !errors.Is(err, ErrRefCASConflict) {
		t.Fatalf("expected CAS conflict, got %v", err)
	}

	// Winner remains first
	ref, err := s.GetMemoryRef(context.Background(), "proj-cas", branch)
	if err != nil {
		t.Fatalf("get ref: %v", err)
	}
	if ref.HeadChangesetID != first.ChangesetID {
		t.Fatalf("head=%s want %s", ref.HeadChangesetID, first.ChangesetID)
	}
}
