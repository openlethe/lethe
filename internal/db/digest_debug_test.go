package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openlethe/lethe/internal/models"
)

func TestDigestDebug(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := s.ExecContext(ctx, `INSERT INTO projects (project_id, name, created_at, updated_at) VALUES ('dbg', 'dbg', ?, ?)`, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	cs := &models.MemoryChangeset{
		ChangesetID:     uuid.Must(uuid.NewV7()).String(),
		SchemaVersion:   models.MemoryGitSchemaVersion,
		ProjectID:       "dbg",
		RefName:         models.RefSharedMain,
		ParentIDs:       []string{},
		AuthorPrincipal: "bench",
		Message:         "bench node 0",
		CreatedAt:       time.Now().UTC(),
		IdempotencyKey:  "bench-0",
		Ops: []models.MemorySemanticOp{
			{Ordinal: 0, OpType: models.OpAddMemory, Payload: map[string]any{"content": "bench memory 0"}},
		},
		Evidence:     []map[string]any{},
		Verification: []map[string]any{},
	}
	before := ComputeChangesetDigest(cs)
	if _, err := s.insertChangeset(ctx, cs); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetChangeset(ctx, cs.ChangesetID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Logf("before=%s loaded-digest=%s stored=%s", before, ComputeChangesetDigest(loaded), loaded.IntegrityDigest)
}
