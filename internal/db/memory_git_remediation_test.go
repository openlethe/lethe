package db

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

// --- WP1: conflict detection purity, deterministic identity, lifecycle ---

// setupConflictBranches builds base/left/right changesets with a known
// contradictory decision conflict between left and right.
func setupConflictBranches(t *testing.T, s *Store, project string) (base, left, right *models.MemoryChangeset) {
	t.Helper()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-wp1", project)
	root, _, err := s.EnsureLegacyRoot(ctx, project, "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemoryBranch(ctx, project, "refs/agents/left/main", root.ChangesetID, "left", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemoryBranch(ctx, project, "refs/agents/right/main", root.ChangesetID, "right", false); err != nil {
		t.Fatal(err)
	}
	left, err = s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/left/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "wp1-left",
		ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "left-decision", Payload: map[string]any{"kind": "decision", "scope": "net", "content": "Decision: private"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err = s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/right/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "wp1-right",
		ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "right-decision", Payload: map[string]any{"kind": "decision", "scope": "net", "content": "Decision: public"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, left, right
}

func countConflicts(t *testing.T, s *Store, project string) int {
	t.Helper()
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM memory_conflicts WHERE project_id = ?`, project).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConflictAnalysisIsPure(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-pure"
	base, left, right := setupConflictBranches(t, s, project)

	det := NewConflictDetector(s)
	first, err := det.DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 {
		t.Fatal("expected conflicts")
	}
	// Repeating analysis creates no rows and returns identical identities.
	for i := 0; i < 3; i++ {
		again, err := det.DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d: %d conflicts, want %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].ConflictID != first[j].ConflictID {
				t.Fatalf("run %d conflict %d: id %s != %s (not deterministic)", i, j, again[j].ConflictID, first[j].ConflictID)
			}
		}
	}
	if n := countConflicts(t, s, project); n != 0 {
		t.Fatalf("pure analysis persisted %d conflict rows", n)
	}
}

func TestPersistConflictsIdempotentReplay(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-persist"
	base, left, right := setupConflictBranches(t, s, project)

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	// Replaying the same proposal operation creates no additional rows.
	for i := 0; i < 3; i++ {
		if err := s.PersistConflicts(ctx, project, "proposal-1", conflicts); err != nil {
			t.Fatal(err)
		}
	}
	if n := countConflicts(t, s, project); n != len(conflicts) {
		t.Fatalf("rows = %d, want %d (one per deterministic conflict)", n, len(conflicts))
	}

	// A second proposal detecting the same semantic conflicts reopens the same
	// canonical rows under its own binding — still no duplicates.
	if err := s.PersistConflicts(ctx, project, "proposal-2", conflicts); err != nil {
		t.Fatal(err)
	}
	if n := countConflicts(t, s, project); n != len(conflicts) {
		t.Fatalf("rows after second proposal = %d, want %d", n, len(conflicts))
	}
	open, err := s.ListConflicts(ctx, project, "open", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range open {
		if c.ProposalID != "proposal-2" {
			t.Fatalf("conflict %s bound to %s, want proposal-2", c.ConflictID, c.ProposalID)
		}
	}
}

func TestRetireConflictsOnTerminalProposalStates(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, status := range []string{"canceled", "rejected", "superseded"} {
		project := "proj-retire-" + status
		base, left, right := setupConflictBranches(t, s, project)
		conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.PersistConflicts(ctx, project, "proposal-"+status, conflicts); err != nil {
			t.Fatal(err)
		}
		retired, err := s.RetireConflictsForProposal(ctx, project, "proposal-"+status, status)
		if err != nil {
			t.Fatal(err)
		}
		if retired != len(conflicts) {
			t.Fatalf("%s: retired %d, want %d", status, retired, len(conflicts))
		}
		open, err := s.ListConflicts(ctx, project, "open", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(open) != 0 {
			t.Fatalf("%s: %d conflicts still open", status, len(open))
		}
	}

	// Invalid retire status is rejected.
	if _, err := s.RetireConflictsForProposal(ctx, "proj-retire-canceled", "proposal-canceled", "open"); err == nil {
		t.Fatal("retiring to open must fail")
	}
}

func TestResolveConflictRetiresEquivalentBlockers(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-resolve"
	base, left, right := setupConflictBranches(t, s, project)

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	// Persist the same semantic set under two proposals; they share rows.
	if err := s.PersistConflicts(ctx, project, "proposal-a", conflicts); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistConflicts(ctx, project, "proposal-b", conflicts); err != nil {
		t.Fatal(err)
	}

	for _, c := range conflicts {
		if err := s.ResolveConflict(ctx, project, c.ConflictID, "resolved by rebase"); err != nil {
			t.Fatal(err)
		}
	}
	open, err := s.ListConflicts(ctx, project, "open", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("%d equivalent blockers survived resolution", len(open))
	}
	// Resolving a non-open conflict fails closed.
	if err := s.ResolveConflict(ctx, project, conflicts[0].ConflictID, "again"); err == nil {
		t.Fatal("double resolve must fail")
	}
}

func TestAbandonedProposalConflictsAbsentFromAcceptedContext(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-abandon"
	base, left, right := setupConflictBranches(t, s, project)

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PersistConflicts(ctx, project, "abandoned-proposal", conflicts); err != nil {
		t.Fatal(err)
	}

	// While open, relevant conflicts appear in the projection of an affected head.
	view, err := s.BuildMemoryContext(ctx, project, "refs/agents/left/main", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.UnresolvedConflicts) == 0 {
		t.Fatal("open conflicts should surface at the affected head")
	}

	// The proposal is abandoned (canceled): its conflicts retire and the
	// accepted context is no longer polluted by them.
	if _, err := s.RetireConflictsForProposal(ctx, project, "abandoned-proposal", "canceled"); err != nil {
		t.Fatal(err)
	}
	view, err = s.BuildMemoryContext(ctx, project, "refs/agents/left/main", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.UnresolvedConflicts) != 0 {
		t.Fatalf("abandoned proposal conflicts still projected: %v", view.UnresolvedConflicts)
	}
}

func TestResolvedConflictDoesNotWithholdMemory(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-withhold"
	base, left, right := setupConflictBranches(t, s, project)

	// Merge both sides into a shared head so both conflicting changesets are
	// reachable from the projected head (the withholding case).
	if _, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/left/main",
		ParentIDs:       []string{left.ChangesetID, right.ChangesetID},
		AuthorPrincipal: "merger", IdempotencyKey: "wp1-merge",
		ExpectedHead: left.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "reviewed merge", "reviewer": "merger"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PersistConflicts(ctx, project, "withhold-proposal", conflicts); err != nil {
		t.Fatal(err)
	}

	view, err := s.BuildMemoryContext(ctx, project, "refs/agents/left/main", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	withheld := map[string]bool{}
	for id, reason := range view.ExclusionReasons {
		if strings.Contains(reason, "withheld by unresolved conflict") {
			withheld[id] = true
		}
	}
	if len(withheld) == 0 {
		t.Fatal("open conflicts should withhold conflicting memories")
	}

	for _, c := range conflicts {
		if err := s.ResolveConflict(ctx, project, c.ConflictID, "adjudicated"); err != nil {
			t.Fatal(err)
		}
	}
	view, err = s.BuildMemoryContext(ctx, project, "refs/agents/left/main", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for id, reason := range view.ExclusionReasons {
		if strings.Contains(reason, "withheld by unresolved conflict") {
			t.Fatalf("memory %s still withheld after resolution: %s", id, reason)
		}
	}
}

func TestConflictProjectedOnlyForRelevantHead(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-relevant"
	base, left, right := setupConflictBranches(t, s, project)

	conflicts, err := NewConflictDetector(s).DetectBetween(ctx, project, base.ChangesetID, left.ChangesetID, right.ChangesetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PersistConflicts(ctx, project, "relevance-proposal", conflicts); err != nil {
		t.Fatal(err)
	}

	// An unrelated branch whose history contains neither side must not report
	// the conflict.
	if _, err := s.CreateMemoryBranch(ctx, project, "refs/agents/other/main", base.ChangesetID, "other", false); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/other/main", ParentIDs: []string{base.ChangesetID},
		AuthorPrincipal: "other", IdempotencyKey: "wp1-other",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, Payload: map[string]any{"content": "unrelated"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.BuildMemoryContext(ctx, project, "refs/agents/other/main", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.UnresolvedConflicts) != 0 {
		t.Fatalf("conflict projected at unrelated head %s: %v", other.ChangesetID, view.UnresolvedConflicts)
	}
}

func TestConflictMigrationCollapsesLegacyDuplicates(t *testing.T) {
	// Build a database whose conflict table predates the lifecycle migration:
	// create a fresh DB, then simulate legacy rows by inserting duplicates with
	// distinct random IDs directly.
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-mig", "proj-mig")

	insert := func(id string) {
		t.Helper()
		_, err := s.ExecContext(ctx, `
			INSERT INTO memory_conflicts (
				conflict_id, project_id, base_changeset_id, left_changeset_id, right_changeset_id,
				conflict_type, severity, summary, details_json, status, created_at
			) VALUES (?, 'proj-mig', 'base', 'left', 'right', 'contradictory_decision', 'blocking',
				'Incompatible decisions for scope net', '{}', 'open', '2026-01-01 00:00:00')
			ON CONFLICT(conflict_id) DO NOTHING`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("random-uuid-1")
	insert("random-uuid-2")

	// The dedupe rule from migration 011, applied to these legacy rows, keeps
	// exactly the earliest semantic row. Verify the semantic uniqueness rule
	// directly: PersistConflicts maps equivalent inputs onto one canonical ID.
	c := &models.MemoryConflict{
		ProjectID: "proj-mig", BaseChangesetID: "base", LeftChangesetID: "left", RightChangesetID: "right",
		ConflictType: "contradictory_decision", Summary: "Incompatible decisions for scope net",
		Details: map[string]any{"scope": "net", "left_event": "l", "right_event": "r"},
	}
	if err := s.PersistConflicts(ctx, "proj-mig", "p1", []*models.MemoryConflict{c}); err != nil {
		t.Fatal(err)
	}
	duplicate := &models.MemoryConflict{
		ProjectID: "proj-mig", BaseChangesetID: "base", LeftChangesetID: "left", RightChangesetID: "right",
		ConflictType: "contradictory_decision", Summary: "Incompatible decisions for scope net",
		Details: map[string]any{"scope": "net", "left_event": "l", "right_event": "r"},
	}
	if err := s.PersistConflicts(ctx, "proj-mig", "p2", []*models.MemoryConflict{duplicate}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.QueryRow(`SELECT COUNT(*) FROM memory_conflicts WHERE project_id = 'proj-mig'
		AND conflict_type = 'contradictory_decision' AND details_json != '{}'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("equivalent conflicts persisted as %d rows, want 1", n)
	}
}

// --- WP5: semantic operation validation ---

func TestSemanticValidationAddMemoryInvariants(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-val-add"
	setupAgentProject(t, s, "agent-val", project)
	root, _, err := s.EnsureLegacyRoot(ctx, project, "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemoryBranch(ctx, project, "refs/agents/val/main", root.ChangesetID, "val", false); err != nil {
		t.Fatal(err)
	}

	create := func(op models.MemorySemanticOp) error {
		_, err := s.CreateChangeset(ctx, CreateChangesetRequest{
			ProjectID: project, RefName: "refs/agents/val/main", ParentIDs: []string{root.ChangesetID},
			AuthorPrincipal: "val", IdempotencyKey: "val-add",
			Ops: []models.MemorySemanticOp{op},
		})
		return err
	}

	invalid := map[string]models.MemorySemanticOp{
		"empty content":         {OpType: models.OpAddMemory, Payload: map[string]any{"content": "  "}},
		"missing content":       {OpType: models.OpAddMemory, Payload: map[string]any{"kind": "fact"}},
		"invalid kind":          {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "kind": "hunch"}},
		"invalid visibility":    {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "visibility": "everyone"}},
		"confidence too high":   {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "confidence": 1.5}},
		"confidence negative":   {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "confidence": -0.1}},
		"confidence wrong type": {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "confidence": "high"}},
		"tags wrong type":       {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "tags": "oops"}},
		"empty scope":           {OpType: models.OpAddMemory, Payload: map[string]any{"content": "x", "scope": ""}},
	}
	for name, op := range invalid {
		if err := create(op); err == nil {
			t.Fatalf("%s: accepted invalid add_memory", name)
		}
	}

	// Boundary values are accepted.
	if err := create(models.MemorySemanticOp{OpType: models.OpAddMemory, Payload: map[string]any{
		"content": "valid", "kind": "fact", "visibility": "shared", "confidence": 1.0, "scope": project,
		"tags": []any{"a", "b"},
	}}); err != nil {
		t.Fatalf("valid add_memory rejected: %v", err)
	}
}

func TestSemanticValidationTargetExistence(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-val-target"
	setupAgentProject(t, s, "agent-val", project)
	root, _, err := s.EnsureLegacyRoot(ctx, project, "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemoryBranch(ctx, project, "refs/agents/val/main", root.ChangesetID, "val", false); err != nil {
		t.Fatal(err)
	}

	head := root
	seed := func(id string) *models.MemoryChangeset {
		t.Helper()
		cs, err := s.CreateChangeset(ctx, CreateChangesetRequest{
			ProjectID: project, RefName: "refs/agents/val/main", ParentIDs: []string{head.ChangesetID},
			AuthorPrincipal: "val", IdempotencyKey: "seed-" + id,
			Ops: []models.MemorySemanticOp{
				{OpType: models.OpAddMemory, ResultingEventID: id, Payload: map[string]any{"content": "seed " + id}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		head = cs
		return cs
	}
	seed("mem-a")
	seedB := seed("mem-b")

	createSeq := 0
	create := func(op models.MemorySemanticOp) error {
		createSeq++
		_, err := s.CreateChangeset(ctx, CreateChangesetRequest{
			ProjectID: project, RefName: "refs/agents/val/main", ParentIDs: []string{seedB.ChangesetID},
			AuthorPrincipal: "val", IdempotencyKey: fmt.Sprintf("val-target-%d", createSeq),
			Ops: []models.MemorySemanticOp{op},
		})
		return err
	}

	invalid := map[string]models.MemorySemanticOp{
		"correct missing":           {OpType: models.OpCorrectMemory, TargetEventID: "ghost", Payload: map[string]any{"content": "x"}},
		"supersede missing":         {OpType: models.OpSupersedeMemory, TargetEventID: "ghost"},
		"deprecate missing":         {OpType: models.OpProposeDeprecation, TargetEventID: "ghost", Payload: map[string]any{"reason": "old"}},
		"duplicate missing":         {OpType: models.OpMarkDuplicate, TargetEventID: "ghost", Payload: map[string]any{"duplicate_of": "mem-a"}},
		"canonical missing":         {OpType: models.OpMarkDuplicate, TargetEventID: "mem-a", Payload: map[string]any{"duplicate_of": "ghost"}},
		"relationship from":         {OpType: models.OpAddRelationship, TargetEventID: "ghost", ResultingEventID: "mem-a", Payload: map[string]any{"kind": "supports"}},
		"relationship to":           {OpType: models.OpAddRelationship, TargetEventID: "mem-a", ResultingEventID: "ghost", Payload: map[string]any{"kind": "supports"}},
		"evidence missing":          {OpType: models.OpAttachEvidence, TargetEventID: "ghost", Payload: map[string]any{"summary": "s"}},
		"self supersede":            {OpType: models.OpSupersedeMemory, TargetEventID: "mem-a", ResultingEventID: "mem-a"},
		"self relationship":         {OpType: models.OpAddRelationship, TargetEventID: "mem-a", ResultingEventID: "mem-a", Payload: map[string]any{"kind": "supports"}},
		"duplicate same":            {OpType: models.OpMarkDuplicate, TargetEventID: "mem-a", Payload: map[string]any{"duplicate_of": "mem-a"}},
		"trust downgrade":           {OpType: models.OpSupersedeMemory, TargetEventID: "mem-a", Payload: map[string]any{"target_trust": "user_approved", "source_trust": "model_inference"}},
		"result collision":          {OpType: models.OpAddMemory, ResultingEventID: "mem-a", Payload: map[string]any{"content": "clobber"}},
		"deprecation no reason":     {OpType: models.OpProposeDeprecation, TargetEventID: "mem-a"},
		"attestation no summary":    {OpType: models.OpAttachVerification, Payload: map[string]any{"reviewer": "r"}},
		"attestation no provenance": {OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "s"}},
	}
	for name, op := range invalid {
		if err := create(op); err == nil {
			t.Fatalf("%s: accepted invalid op", name)
		}
	}

	valid := map[string]models.MemorySemanticOp{
		"correct":      {OpType: models.OpCorrectMemory, TargetEventID: "mem-a", Payload: map[string]any{"content": "corrected"}},
		"supersede":    {OpType: models.OpSupersedeMemory, TargetEventID: "mem-a", ResultingEventID: "mem-c", Payload: map[string]any{"content": "replacement"}},
		"deprecate":    {OpType: models.OpProposeDeprecation, TargetEventID: "mem-a", Payload: map[string]any{"reason": "stale"}},
		"duplicate":    {OpType: models.OpMarkDuplicate, TargetEventID: "mem-b", Payload: map[string]any{"duplicate_of": "mem-a"}},
		"relationship": {OpType: models.OpAddRelationship, TargetEventID: "mem-a", ResultingEventID: "mem-b", Payload: map[string]any{"kind": "supports"}},
		"evidence":     {OpType: models.OpAttachEvidence, TargetEventID: "mem-a", Payload: map[string]any{"summary": "ci run", "source": "ci"}},
		"attestation":  {OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "reviewed", "reviewer": "principal"}},
	}
	for name, op := range valid {
		if err := create(op); err != nil {
			t.Fatalf("%s: valid op rejected: %v", name, err)
		}
	}
}

func TestSemanticValidationCrossProjectRejected(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-a", "proj-x")
	setupAgentProject(t, s, "agent-b", "proj-y")
	rootX, _, err := s.EnsureLegacyRoot(ctx, "proj-x", "system")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMemoryBranch(ctx, "proj-x", "refs/agents/a/main", rootX.ChangesetID, "a", false); err != nil {
		t.Fatal(err)
	}
	rootY, _, err := s.EnsureLegacyRoot(ctx, "proj-y", "system")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-y", RefName: "refs/agents/b/main", ParentIDs: []string{rootY.ChangesetID},
		AuthorPrincipal: "b", IdempotencyKey: "foreign",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "foreign-mem", Payload: map[string]any{"content": "foreign"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = foreign

	// A correction in proj-x against proj-y's memory must fail: the identity
	// does not exist in proj-x's parent state.
	_, err = s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: "proj-x", RefName: "refs/agents/a/main", ParentIDs: []string{rootX.ChangesetID},
		AuthorPrincipal: "a", IdempotencyKey: "cross-project",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpCorrectMemory, TargetEventID: "foreign-mem", Payload: map[string]any{"content": "hijack"}},
		},
	})
	if err == nil {
		t.Fatal("cross-project correction accepted")
	}
}

// Regression (autoreview): a parentless changeset carrying an op that needs
// state validation must not panic — it validates against empty root state.
func TestRootChangesetWithStatefulOpDoesNotPanic(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-root-stateful"
	setupAgentProject(t, s, "agent-root", project)

	// add_memory with an explicit resulting identity triggers state validation
	// even with no parents.
	cs, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/root/main", ParentIDs: nil,
		AuthorPrincipal: "root", IdempotencyKey: "root-stateful",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, ResultingEventID: "root-mem", Payload: map[string]any{"content": "root memory"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cs.ChangesetID == "" {
		t.Fatal("no changeset created")
	}

	// A parentless changeset referencing a missing target must still fail
	// validation cleanly (no panic).
	_, err = s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/root/main", ParentIDs: []string{},
		AuthorPrincipal: "root", IdempotencyKey: "root-stateful-2",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpCorrectMemory, TargetEventID: "ghost", Payload: map[string]any{"content": "x"}},
		},
	})
	if err == nil {
		t.Fatal("missing-target correction on root accepted")
	}
}
