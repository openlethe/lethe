package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/models"
)

// setupProtectedMain builds a project with a protected refs/shared/main and
// returns its head (the legacy root).
func setupProtectedMain(t *testing.T, s *Store, project string) *models.MemoryChangeset {
	t.Helper()
	setupAgentProject(t, s, "agent-merge-auth", project)
	root, _, err := s.EnsureLegacyRoot(context.Background(), project, "system")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func addDescendant(t *testing.T, s *Store, project, refName, parentID, key string) *models.MemoryChangeset {
	t.Helper()
	cs, err := s.CreateChangeset(context.Background(), CreateChangesetRequest{
		ProjectID: project, RefName: refName, ParentIDs: []string{parentID},
		AuthorPrincipal: "author", IdempotencyKey: key,
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, Payload: map[string]any{"content": "descendant " + key}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func advancement(project, refName, expected, newHead, nonce, strategy string) MergeAdvancement {
	return MergeAdvancement{
		ProjectID: project, RefName: refName, ExpectedHead: expected, NewHead: newHead,
		ProposalID: "proposal-1", ProposalDigest: strings.Repeat("a", 64),
		ReviewerPrincipal: "reviewer", MergerPrincipal: "merger",
		Strategy: strategy, Nonce: nonce, ExpiresAt: time.Now().UTC().Add(2 * time.Minute),
	}
}

func TestAuthorizedMergeAdvanceFastForward(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-ff"
	root := setupProtectedMain(t, s, project)
	middle := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-ff-1")
	tip := addDescendant(t, s, project, "refs/agents/a/main", middle.ChangesetID, "ma-ff-2")

	ref, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, tip.ChangesetID, "nonce-ff-1", StrategyFastForward))
	if err != nil {
		t.Fatal(err)
	}
	if ref.HeadChangesetID != tip.ChangesetID {
		t.Fatalf("head = %s, want %s", ref.HeadChangesetID, tip.ChangesetID)
	}

	// Durable advancement record exists.
	advances, err := s.ListProtectedRefAdvances(ctx, project, models.RefSharedMain, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(advances) != 1 || advances[0].NewHead != tip.ChangesetID || advances[0].AuthorizationNonce != "nonce-ff-1" {
		t.Fatalf("advances = %#v", advances)
	}
}

func TestAuthorizedMergeShapes(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-shapes"
	root := setupProtectedMain(t, s, project)
	left := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-l")
	right := addDescendant(t, s, project, "refs/agents/b/main", root.ChangesetID, "ma-r")

	// Two-parent merge commit.
	mergeCommit, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/a/main", ParentIDs: []string{left.ChangesetID, right.ChangesetID},
		AuthorPrincipal: "merger", IdempotencyKey: "ma-merge",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "reviewed", "reviewer": "r"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, left.ChangesetID, mergeCommit.ChangesetID, "nonce-mc", StrategyMergeCommit)); err == nil {
		t.Fatal("merge commit with wrong expected head accepted")
	}
	// Advance to left first so the merge commit's first parent matches.
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, left.ChangesetID, "nonce-ff", StrategyFastForward)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, left.ChangesetID, mergeCommit.ChangesetID, "nonce-mc2", StrategyMergeCommit)); err != nil {
		t.Fatalf("valid merge commit rejected: %v", err)
	}

	// Cherry-pick: single parent on the current head.
	cherry, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/a/main", ParentIDs: []string{mergeCommit.ChangesetID},
		AuthorPrincipal: "merger", IdempotencyKey: "ma-cherry",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAddMemory, Payload: map[string]any{"content": "cherry-picked"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, mergeCommit.ChangesetID, cherry.ChangesetID, "nonce-cp", StrategyCherryPick)); err != nil {
		t.Fatalf("valid cherry-pick rejected: %v", err)
	}

	// Wrong strategy for the shape fails.
	twoParent, err := s.CreateChangeset(ctx, CreateChangesetRequest{
		ProjectID: project, RefName: "refs/agents/a/main", ParentIDs: []string{cherry.ChangesetID, right.ChangesetID},
		AuthorPrincipal: "merger", IdempotencyKey: "ma-mc3",
		Ops: []models.MemorySemanticOp{
			{OpType: models.OpAttachVerification, Payload: map[string]any{"summary": "reviewed", "reviewer": "r"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, cherry.ChangesetID, twoParent.ChangesetID, "nonce-wrong", StrategyCherryPick))
	if !errors.Is(err, ErrMergeShape) {
		t.Fatalf("two-parent head as cherry-pick: err = %v, want ErrMergeShape", err)
	}
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, cherry.ChangesetID, twoParent.ChangesetID, "nonce-right", StrategyMergeCommit)); err != nil {
		t.Fatalf("valid second merge commit rejected: %v", err)
	}
}

func TestAuthorizationSingleUseImmediateAndAfterCycle(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-replay"
	root := setupProtectedMain(t, s, project)
	tip := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-replay-1")

	advance := advancement(project, models.RefSharedMain, root.ChangesetID, tip.ChangesetID, "nonce-replay", StrategyFastForward)
	if _, err := s.CASMergeProtectedRefAuthorized(ctx, advance); err != nil {
		t.Fatal(err)
	}
	// Immediate replay.
	_, err := s.CASMergeProtectedRefAuthorized(ctx, advance)
	if !errors.Is(err, ErrAuthorizationReplay) {
		t.Fatalf("immediate replay: err = %v, want ErrAuthorizationReplay", err)
	}

	// Cycle the ref back to the original expected head (operator rollback via
	// direct storage repair), then replay the captured authorization: the
	// consumed nonce still rejects it.
	if _, err := s.ExecContext(ctx, `UPDATE memory_refs SET head_changeset_id = ? WHERE project_id = ? AND ref_name = ?`,
		root.ChangesetID, project, models.RefSharedMain); err != nil {
		t.Fatal(err)
	}
	_, err = s.CASMergeProtectedRefAuthorized(ctx, advance)
	if !errors.Is(err, ErrAuthorizationReplay) {
		t.Fatalf("replay after ref cycle-back: err = %v, want ErrAuthorizationReplay", err)
	}
}

func TestAuthorizationNonceConcurrency(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-race"
	root := setupProtectedMain(t, s, project)
	tip := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-race-1")

	const racers = 12
	var wg sync.WaitGroup
	var successes, replays, others int
	var mu sync.Mutex
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, tip.ChangesetID, "nonce-race", StrategyFastForward))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrAuthorizationReplay) || errors.Is(err, ErrRefCASConflict):
				replays++
			default:
				others++
			}
		}()
	}
	wg.Wait()
	if successes != 1 || others != 0 {
		t.Fatalf("successes=%d replays=%d others=%d, want exactly 1 success", successes, replays, others)
	}
}

func TestAuthorizationRejectsInvalidShapes(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-invalid"
	root := setupProtectedMain(t, s, project)

	// Unrelated same-project head (not a descendant of expected).
	unrelated := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-unrelated")
	other := addDescendant(t, s, project, "refs/agents/b/main", root.ChangesetID, "ma-other")
	_, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, unrelated.ChangesetID, other.ChangesetID, "nonce-unrelated", StrategyFastForward))
	if !errors.Is(err, ErrMergeShape) {
		t.Fatalf("unrelated new head: err = %v, want ErrMergeShape", err)
	}

	// Cross-project new head.
	setupAgentProject(t, s, "agent-x", "proj-x")
	rootX, _, err := s.EnsureLegacyRoot(ctx, "proj-x", "system")
	if err != nil {
		t.Fatal(err)
	}
	foreign := addDescendant(t, s, "proj-x", "refs/agents/x/main", rootX.ChangesetID, "ma-foreign")
	_, err = s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, foreign.ChangesetID, "nonce-foreign", StrategyFastForward))
	if !errors.Is(err, ErrMergeShape) {
		t.Fatalf("cross-project new head: err = %v, want ErrMergeShape", err)
	}

	// Invalid strategy.
	_, err = s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, unrelated.ChangesetID, "nonce-strategy", "delete_everything"))
	if !errors.Is(err, ErrInvalidStrategy) {
		t.Fatalf("invalid strategy: err = %v, want ErrInvalidStrategy", err)
	}
}

// TestAuthorizedAdvanceConcurrencyOnlyOneCAS: competing protected merges with
// distinct nonces; exactly one CAS can win.
func TestAuthorizedAdvanceConcurrencyOnlyOneCAS(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-ma-casrace"
	root := setupProtectedMain(t, s, project)
	tipA := addDescendant(t, s, project, "refs/agents/a/main", root.ChangesetID, "ma-casa")
	tipB := addDescendant(t, s, project, "refs/agents/b/main", root.ChangesetID, "ma-casb")

	const racers = 8
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for i := 0; i < racers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			tip := tipA
			if i%2 == 1 {
				tip = tipB
			}
			_, err := s.CASMergeProtectedRefAuthorized(ctx, advancement(project, models.RefSharedMain, root.ChangesetID, tip.ChangesetID, fmt.Sprintf("nonce-cas-%d", i), StrategyFastForward))
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, ErrRefCASConflict) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes = %d, want exactly 1", successes)
	}
}
