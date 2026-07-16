package db

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

func TestBuildMemoryContextProjectsAcceptedHeadAndFreezesLegacyBaseline(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-context", "project-context")

	legacy := &models.Event{
		EventID: "evt-legacy", ProjectID: "project-context",
		EventType: models.EventRecord, Content: "Legacy accepted decision",
	}
	if err := s.CreateEvent(ctx, legacy); err != nil {
		t.Fatalf("create legacy event: %v", err)
	}

	root, _, err := s.EnsureLegacyRoot(ctx, "project-context", "system")
	if err != nil {
		t.Fatalf("ensure root: %v", err)
	}

	// This direct event write happened after the frozen baseline and must not
	// silently become accepted Memory Git state.
	postRoot := &models.Event{
		EventID: "evt-post-root", ProjectID: "project-context",
		EventType: models.EventRecord, Content: "Unversioned post-root event",
	}
	if err := s.CreateEvent(ctx, postRoot); err != nil {
		t.Fatalf("create post-root event: %v", err)
	}

	branch := "refs/agents/archimedes/main"
	if _, err := s.CreateMemoryBranch(ctx, "project-context", branch, root.ChangesetID, "archimedes", false); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	added, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-context", RefName: branch,
		ParentIDs: []string{root.ChangesetID}, AuthorPrincipal: "archimedes",
		ActorID: "archimedes", Message: "add accepted endpoint",
		IdempotencyKey: "context-add", ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "mem-health",
			Payload: map[string]any{
				"content": "Health endpoint is /health", "kind": "decision", "scope": "api",
			},
		}},
	})
	if err != nil {
		t.Fatalf("create add changeset: %v", err)
	}
	if _, err := s.CASMergeProtectedRef(ctx, "project-context", models.RefSharedMain, root.ChangesetID, added.ChangesetID); err != nil {
		t.Fatalf("merge add: %v", err)
	}

	view, err := s.BuildMemoryContext(ctx, "project-context", models.RefSharedMain, "", "", 20)
	if err != nil {
		t.Fatalf("build view: %v", err)
	}
	if view.HeadChangesetID != added.ChangesetID {
		t.Fatalf("head=%s want %s", view.HeadChangesetID, added.ChangesetID)
	}
	assertMemoryContent(t, view.Memories, "evt-legacy", "Legacy accepted decision")
	assertMemoryContent(t, view.Memories, "mem-health", "Health endpoint is /health")
	assertMemoryMissing(t, view.Memories, "evt-post-root")

	corrected, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-context", RefName: branch,
		ParentIDs: []string{added.ChangesetID}, AuthorPrincipal: "archimedes",
		ActorID: "archimedes", Message: "correct accepted endpoint",
		IdempotencyKey: "context-correct", ExpectedHead: added.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpCorrectMemory, TargetEventID: "mem-health",
			Payload: map[string]any{"content": "Health endpoint is /healthz"},
		}},
	})
	if err != nil {
		t.Fatalf("create correction: %v", err)
	}
	if _, err := s.CASMergeProtectedRef(ctx, "project-context", models.RefSharedMain, added.ChangesetID, corrected.ChangesetID); err != nil {
		t.Fatalf("merge correction: %v", err)
	}

	current, err := s.BuildMemoryContext(ctx, "project-context", models.RefSharedMain, "", "healthz", 1)
	if err != nil {
		t.Fatalf("build corrected view: %v", err)
	}
	assertMemoryContent(t, current.Memories, "mem-health", "Health endpoint is /healthz")
	if len(current.Memories) != 1 {
		t.Fatalf("query-limited memories=%d want 1", len(current.Memories))
	}

	historical, err := s.BuildMemoryContext(ctx, "project-context", models.RefSharedMain, added.ChangesetID, "", 20)
	if err != nil {
		t.Fatalf("build historical view: %v", err)
	}
	assertMemoryContent(t, historical.Memories, "mem-health", "Health endpoint is /health")

	unmerged, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-context", RefName: branch,
		ParentIDs: []string{corrected.ChangesetID}, AuthorPrincipal: "archimedes",
		IdempotencyKey: "context-unmerged", ExpectedHead: corrected.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": "Unmerged branch content"},
		}},
	})
	if err != nil {
		t.Fatalf("create unmerged changeset: %v", err)
	}
	if _, err := s.BuildMemoryContext(ctx, "project-context", models.RefSharedMain, unmerged.ChangesetID, "", 20); err == nil ||
		!strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("unmerged head should be rejected, got %v", err)
	}
}

func TestBuildMemoryContextAppliesSupersedeAndDuplicate(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-ops", "project-ops")
	root, _, err := s.EnsureLegacyRoot(ctx, "project-ops", "system")
	if err != nil {
		t.Fatal(err)
	}
	branch := "refs/agents/ops/main"
	if _, err := s.CreateMemoryBranch(ctx, "project-ops", branch, root.ChangesetID, "ops", false); err != nil {
		t.Fatal(err)
	}
	cs, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-ops", RefName: branch, ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "ops", IdempotencyKey: "ops-all", ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "old", Payload: map[string]any{"content": "old"}},
			{OpType: models.OpSupersedeMemory, TargetEventID: "old", ResultingEventID: "new", Payload: map[string]any{"content": "new"}},
			{OpType: models.OpAddMemory, ResultingEventID: "duplicate", Payload: map[string]any{"content": "new"}},
			{OpType: models.OpMarkDuplicate, TargetEventID: "duplicate", Payload: map[string]any{"duplicate_of": "new"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRef(ctx, "project-ops", models.RefSharedMain, root.ChangesetID, cs.ChangesetID); err != nil {
		t.Fatal(err)
	}
	view, err := s.BuildMemoryContext(ctx, "project-ops", models.RefSharedMain, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	assertMemoryMissing(t, view.Memories, "old")
	assertMemoryMissing(t, view.Memories, "duplicate")
	assertMemoryContent(t, view.Memories, "new", "new")

	// A correction against a missing target can no longer enter immutable
	// history at all; semantic validation rejects it at write time.
	_, err = s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-ops", RefName: branch, ParentIDs: []string{cs.ChangesetID},
		AuthorPrincipal: "ops", IdempotencyKey: "ops-missing-target",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpCorrectMemory, TargetEventID: "missing-target", Payload: map[string]any{"content": "must not be promoted"}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not exist at the parent state") {
		t.Fatalf("missing-target correction must be rejected, got %v", err)
	}
}

func TestBuildMemoryContextRanksAcceptedMetadataUpdatesAsRecent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-rank", "project-rank")
	root, _, err := s.EnsureLegacyRoot(ctx, "project-rank", "system")
	if err != nil {
		t.Fatal(err)
	}
	branch := "refs/agents/rank/main"
	if _, err := s.CreateMemoryBranch(ctx, "project-rank", branch, root.ChangesetID, "rank", false); err != nil {
		t.Fatal(err)
	}
	cs, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-rank", RefName: branch, ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "rank", IdempotencyKey: "rank-metadata", ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "older", Payload: map[string]any{"content": "older memory"}},
			{OpType: models.OpAddMemory, ResultingEventID: "newer", Payload: map[string]any{"content": "newer memory"}},
			{OpType: models.OpAttachEvidence, TargetEventID: "older", Payload: map[string]any{"summary": "fresh evidence"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRef(ctx, "project-rank", models.RefSharedMain, root.ChangesetID, cs.ChangesetID); err != nil {
		t.Fatal(err)
	}
	view, err := s.BuildMemoryContext(ctx, "project-rank", models.RefSharedMain, "", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Memories) != 1 || view.Memories[0].MemoryID != "older" {
		t.Fatalf("freshly evidenced memory was not ranked first: %#v", view.Memories)
	}
}

func TestLoadLegacyEventsBatchesFrozenIDsAndPreservesOrder(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-batched-baseline", "project-batched-baseline")

	const count = 1005
	createdIDs := make([]string, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("evt-batch-%04d", i)
		createdIDs = append(createdIDs, id)
		if err := s.CreateEvent(ctx, &models.Event{
			EventID: id, ProjectID: "project-batched-baseline", EventType: models.EventRecord,
			Content: fmt.Sprintf("event %d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]string, 0, count)
	for i := len(createdIDs) - 1; i >= 0; i-- {
		ids = append(ids, createdIDs[i])
	}

	events, err := s.loadLegacyEvents(ctx, "project-batched-baseline", ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != count {
		t.Fatalf("loaded %d events, want %d", len(events), count)
	}
	if events[0].EventID != "evt-batch-0000" || events[count-1].EventID != "evt-batch-1004" {
		t.Fatalf("batched events are not globally ordered: first=%s last=%s", events[0].EventID, events[count-1].EventID)
	}
}

func TestBuildMemoryContextWithholdsBothSidesOfUnresolvedMergedConflict(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-conflicted-context", "project-conflicted-context")
	root, _, err := s.EnsureLegacyRoot(ctx, "project-conflicted-context", "system")
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-conflicted-context", RefName: models.RefSharedMain, ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "reviewer", IdempotencyKey: "conflicted-context-base",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "deployment-target",
			Payload: map[string]any{"content": "Deploy to blue", "kind": "decision", "scope": "deploy"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-conflicted-context", RefName: "refs/agents/left/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "conflicted-context-left",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpCorrectMemory, TargetEventID: "deployment-target",
			Payload: map[string]any{"content": "Deploy to green"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-conflicted-context", RefName: "refs/agents/right/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "conflicted-context-right",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpCorrectMemory, TargetEventID: "deployment-target",
			Payload: map[string]any{"content": "Deploy to canary"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	merge, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "project-conflicted-context", RefName: models.RefSharedMain,
		ParentIDs: []string{left.ChangesetID, right.ChangesetID}, AuthorPrincipal: "reviewer",
		IdempotencyKey: "conflicted-context-merge",
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "merge-note",
			Payload: map[string]any{"content": "Merged pending conflict resolution", "kind": "observation"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRef(ctx, "project-conflicted-context", models.RefSharedMain, root.ChangesetID, merge.ChangesetID); err != nil {
		t.Fatal(err)
	}
	conflict := &models.MemoryConflict{
		ConflictID: "conflict-deployment-target", ProjectID: "project-conflicted-context",
		BaseChangesetID: base.ChangesetID, LeftChangesetID: left.ChangesetID, RightChangesetID: right.ChangesetID,
		ConflictType: "contradictory_decision", Severity: "blocking", Status: "open",
	}
	if err := s.CreateConflict(ctx, conflict); err != nil {
		t.Fatal(err)
	}

	view, err := s.BuildMemoryContext(ctx, "project-conflicted-context", models.RefSharedMain, "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	assertMemoryMissing(t, view.Memories, "deployment-target")
	if len(view.UnresolvedConflicts) != 1 || view.UnresolvedConflicts[0] != conflict.ConflictID {
		t.Fatalf("unresolved conflicts=%v", view.UnresolvedConflicts)
	}
	if !strings.Contains(view.ExclusionReasons["deployment-target"], conflict.ConflictID) {
		t.Fatalf("missing conflict exclusion reason: %#v", view.ExclusionReasons)
	}
}

func assertMemoryContent(t *testing.T, memories []models.AcceptedMemory, id, content string) {
	t.Helper()
	for _, memory := range memories {
		if memory.MemoryID == id {
			if memory.Content != content {
				t.Fatalf("memory %s content=%q want %q", id, memory.Content, content)
			}
			return
		}
	}
	t.Fatalf("memory %s missing from %#v", id, memories)
}

func assertMemoryMissing(t *testing.T, memories []models.AcceptedMemory, id string) {
	t.Helper()
	for _, memory := range memories {
		if memory.MemoryID == id {
			t.Fatalf("memory %s unexpectedly present", id)
		}
	}
}
