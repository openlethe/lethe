package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openlethe/lethe/internal/models"
)

// benchProject bulk-builds a linear history of n changesets plus a ref
// pointing at the tip. Insertion bypasses request validation but writes
// proper integrity digests, so read paths verify normally.
func benchProject(b *testing.B, s *Store, project string, n int, mergeEvery int) (tipID string) {
	b.Helper()
	ctx := context.Background()
	if _, err := s.ExecContext(ctx, `INSERT INTO projects (project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		project, project, time.Now().UTC(), time.Now().UTC()); err != nil {
		b.Fatal(err)
	}
	root := &models.MemoryChangeset{
		ChangesetID:     uuid.Must(uuid.NewV7()).String(),
		SchemaVersion:   models.MemoryGitSchemaVersion,
		ProjectID:       project,
		RefName:         models.RefSharedMain,
		ParentIDs:       []string{},
		AuthorPrincipal: "bench",
		Message:         "bench root",
		CreatedAt:       time.Now().UTC(),
		IdempotencyKey:  "bench-root",
		Ops:             nil,
		Evidence:        []map[string]any{},
		Verification:    []map[string]any{},
	}
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := s.insertChangesetTx(ctx, tx, root); err != nil {
		b.Fatal(err)
	}
	tip := root.ChangesetID
	var leftTip, rightTip string
	for i := 0; i < n; i++ {
		parents := []string{tip}
		if mergeEvery > 0 && i > 0 && i%mergeEvery == 0 && rightTip != "" {
			parents = []string{leftTip, rightTip}
		}
		cs := &models.MemoryChangeset{
			ChangesetID:     uuid.Must(uuid.NewV7()).String(),
			SchemaVersion:   models.MemoryGitSchemaVersion,
			ProjectID:       project,
			RefName:         models.RefSharedMain,
			ParentIDs:       parents,
			AuthorPrincipal: "bench",
			Message:         fmt.Sprintf("bench node %d", i),
			CreatedAt:       time.Now().UTC(),
			IdempotencyKey:  fmt.Sprintf("bench-%d", i),
			Ops: []models.MemorySemanticOp{
				{Ordinal: 0, OpType: models.OpAddMemory, Payload: map[string]any{
					"content": fmt.Sprintf("bench memory %d: latency budgets and cache policy notes", i),
				}},
			},
			Evidence:     []map[string]any{},
			Verification: []map[string]any{},
		}
		if _, err := s.insertChangesetTx(ctx, tx, cs); err != nil {
			b.Fatal(err)
		}
		if mergeEvery > 0 && i%2 == 0 {
			leftTip = cs.ChangesetID
		} else {
			rightTip = cs.ChangesetID
		}
		tip = cs.ChangesetID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_refs (project_id, ref_name, head_changeset_id, protected, created_at, updated_at, created_by_principal)
		VALUES (?, ?, ?, 1, ?, ?, 'bench')
	`, project, models.RefSharedMain, tip, time.Now().UTC(), time.Now().UTC()); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return tip
}

func benchReconstruct(b *testing.B, n int, mergeEvery int) {
	b.Helper()
	s, cleanup := newTestStore(&testing.T{})
	defer cleanup()
	project := fmt.Sprintf("bench-%d-%d", n, mergeEvery)
	tip := benchProject(b, s, project, n, mergeEvery)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.BuildMemoryContext(ctx, project, models.RefSharedMain, tip, "", 100); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconstructLinear1K(b *testing.B)    { benchReconstruct(b, 1000, 0) }
func BenchmarkReconstructLinear10K(b *testing.B)   { benchReconstruct(b, 10000, 0) }
func BenchmarkReconstructMergeDAG10K(b *testing.B) { benchReconstruct(b, 10000, 7) }

func BenchmarkReconstructLinear100K(b *testing.B) {
	if testing.Short() {
		b.Skip("100k changeset benchmark skipped in short mode")
	}
	b.Setenv("LETHE_MEMORY_GIT_MAX_HISTORY", "200000")
	benchReconstruct(b, 100000, 0)
}

func BenchmarkMemoryHistoryAt10K(b *testing.B) {
	s, cleanup := newTestStore(&testing.T{})
	defer cleanup()
	project := "bench-history-10k"
	tip := benchProject(b, s, project, 10000, 0)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.memoryHistoryAt(ctx, project, tip); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconstructConcurrentReaders(b *testing.B) {
	s, cleanup := newTestStore(&testing.T{})
	defer cleanup()
	project := "bench-concurrent"
	tip := benchProject(b, s, project, 2000, 0)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := s.BuildMemoryContext(ctx, project, models.RefSharedMain, tip, "", 100); err != nil {
				b.Fatal(err)
			}
		}
	})
}
