package db

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

// legacyHistoryAt is the original recursive traversal, preserved here as the
// reference oracle proving the bulk loader produces identical ordering.
func legacyHistoryAt(s *Store, ctx context.Context, projectID, headID string) ([]*models.MemoryChangeset, error) {
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	history := make([]*models.MemoryChangeset, 0)
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("cycle detected in memory changeset graph at %s", id)
		}
		visiting[id] = true
		cs, err := s.GetChangeset(ctx, id)
		if err != nil {
			return err
		}
		if cs.ProjectID != projectID {
			return fmt.Errorf("changeset %s project mismatch", id)
		}
		for _, parentID := range cs.ParentIDs {
			if err := visit(parentID); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		history = append(history, cs)
		return nil
	}
	if err := visit(headID); err != nil {
		return nil, err
	}
	return history, nil
}

// buildRandomDAG creates a random DAG of n changesets (each with 0-2 parents
// chosen from earlier changesets) and returns the tip ID.
func buildRandomDAG(t *testing.T, s *Store, project string, n int, seed int64) string {
	t.Helper()
	ctx := context.Background()
	setupAgentProject(t, s, "agent-dag", project)
	if _, _, err := s.EnsureLegacyRoot(ctx, project, "system"); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	ids := []string{}
	// Find the legacy root to use as the first parent.
	root, err := s.findLegacyRoot(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, root.ChangesetID)
	for i := 0; i < n; i++ {
		parentCount := 1 + rng.Intn(2) // 1-2 parents
		parents := make([]string, 0, parentCount)
		for p := 0; p < parentCount; p++ {
			parents = append(parents, ids[rng.Intn(len(ids))])
		}
		cs, err := s.CreateChangeset(ctx, CreateChangesetRequest{
			ProjectID: project, RefName: "refs/agents/dag/main",
			ParentIDs: parents, AuthorPrincipal: "dag", IdempotencyKey: fmt.Sprintf("dag-%d-%d", seed, i),
			Ops: []models.MemorySemanticOp{
				{OpType: models.OpAddMemory, Payload: map[string]any{"content": fmt.Sprintf("node %d", i)}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cs.ChangesetID)
	}
	return ids[len(ids)-1]
}

func TestBulkLoaderMatchesLegacyTraversalOrder(t *testing.T) {
	for _, cfg := range []struct {
		n    int
		seed int64
	}{
		{25, 1},
		{60, 42},
		{120, 7},
		// Multi-batch: batch boundaries previously wiped ops assigned by
		// earlier batches, corrupting every verified node beyond batch one.
		{1200, 5},
	} {
		t.Run(fmt.Sprintf("n=%d/seed=%d", cfg.n, cfg.seed), func(t *testing.T) {
			s, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()
			project := fmt.Sprintf("proj-dag-%d-%d", cfg.n, cfg.seed)
			tip := buildRandomDAG(t, s, project, cfg.n, cfg.seed)

			legacy, err := legacyHistoryAt(s, ctx, project, tip)
			if err != nil {
				t.Fatal(err)
			}
			graph, err := s.loadMemoryGraph(ctx, project, tip, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.order) != len(legacy) {
				t.Fatalf("sizes differ: legacy %d, bulk %d", len(legacy), len(graph.order))
			}
			for i := range legacy {
				if legacy[i].ChangesetID != graph.order[i].ChangesetID {
					t.Fatalf("position %d differs: legacy %s, bulk %s", i, legacy[i].ChangesetID, graph.order[i].ChangesetID)
				}
				if len(legacy[i].Ops) != len(graph.order[i].Ops) {
					t.Fatalf("ops count differs at %s", legacy[i].ChangesetID)
				}
				if ComputeChangesetDigest(legacy[i]) != ComputeChangesetDigest(graph.order[i]) {
					t.Fatalf("digest differs at %s", legacy[i].ChangesetID)
				}
			}
		})
	}
}

func TestHistoryLimitFailsClosed(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-limit"
	setupAgentProject(t, s, "agent-limit", project)
	root, _, err := s.EnsureLegacyRoot(ctx, project, "system")
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic linear chain: depth is exactly chain length + 1.
	head := root
	for i := 0; i < 20; i++ {
		head, err = s.CreateChangeset(ctx, CreateChangesetRequest{
			ProjectID: project, RefName: "refs/agents/limit/main", ParentIDs: []string{head.ChangesetID},
			AuthorPrincipal: "limit", IdempotencyKey: fmt.Sprintf("limit-%d", i),
			Ops: []models.MemorySemanticOp{
				{OpType: models.OpAddMemory, Payload: map[string]any{"content": fmt.Sprintf("node %d", i)}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.loadMemoryGraph(ctx, project, head.ChangesetID, 500); err != nil {
		t.Fatal(err)
	}
	_, err = s.loadMemoryGraph(ctx, project, head.ChangesetID, 5)
	if !errors.Is(err, ErrHistoryLimit) {
		t.Fatalf("err = %v, want ErrHistoryLimit", err)
	}
}

func TestTamperDetectedOnRepeatReads(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project := "proj-cache"
	setupAgentProject(t, s, "agent-cache", project)
	root, _, err := s.EnsureLegacyRoot(ctx, project, "system")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetChangeset(ctx, root.ChangesetID); err != nil {
		t.Fatal(err)
	}
	// Tamper the stored row: every subsequent read must fail verification —
	// no caching layer may mask it.
	if _, err := s.ExecContext(ctx, `UPDATE memory_changesets SET message = 'tampered' WHERE changeset_id = ?`, root.ChangesetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetChangeset(ctx, root.ChangesetID); err == nil {
		t.Fatal("tampered row served on repeat read")
	}
	// Reconstruction over the bulk loader also fails closed.
	if _, err := s.loadMemoryGraph(ctx, project, root.ChangesetID, 0); err == nil {
		t.Fatal("tampered row accepted by bulk loader")
	}
}
