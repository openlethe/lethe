package db

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/openlethe/lethe/internal/models"
)

// --- High: strict changeset idempotency ---

func idempotentRequest(project, ref, parent, principal, key, content string) CreateChangesetRequest {
	return CreateChangesetRequest{
		ProjectID:       project,
		RefName:         ref,
		ParentIDs:       []string{parent},
		AuthorPrincipal: principal,
		ActorID:         "tester",
		Message:         "commit " + content,
		IdempotencyKey:  key,
		Ops: []models.MemorySemanticOp{{
			OpType:  models.OpAddMemory,
			Payload: map[string]any{"content": content},
		}},
	}
}

func TestChangesetIdempotentIdenticalReplay(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-idem")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-idem", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-idem", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	first, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-idem", ref, root.ChangesetID, "p1", "k1", "alpha"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-idem", ref, root.ChangesetID, "p1", "k1", "alpha"))
	if err != nil {
		t.Fatalf("identical replay must succeed: %v", err)
	}
	if second.ChangesetID != first.ChangesetID {
		t.Fatalf("identical replay returned %s, want %s", second.ChangesetID, first.ChangesetID)
	}
}

func TestChangesetIdempotencyMismatchedReplay(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-idem-mix")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-idem-mix", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-idem-mix", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	first, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-idem-mix", ref, root.ChangesetID, "p1", "same-key", "alpha"))
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Same key, different content: must be rejected, not silently dropped.
	_, err = s.CreateChangeset(context.Background(), idempotentRequest("proj-idem-mix", ref, root.ChangesetID, "p1", "same-key", "beta"))
	if !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("mismatched replay error = %v, want ErrIdempotencyMismatch", err)
	}

	// The original write is preserved and the conflicting write never landed.
	got, err := s.GetChangeset(context.Background(), first.ChangesetID)
	if err != nil {
		t.Fatalf("GetChangeset: %v", err)
	}
	if got.Ops[0].Payload["content"] != "alpha" {
		t.Fatalf("stored content = %v, want alpha", got.Ops[0].Payload["content"])
	}
	dupe, err := s.FindChangesetByIdempotency(context.Background(), "proj-idem-mix", "p1", "same-key")
	if err != nil || dupe == nil {
		t.Fatalf("FindChangesetByIdempotency: %v", err)
	}
	if dupe.ChangesetID != first.ChangesetID {
		t.Fatalf("idempotency record moved to %s", dupe.ChangesetID)
	}
}

// TestChangesetIdempotencyBindsRefMutationIntent ports the request-digest
// binding: the replay digest covers the ref-mutation control fields
// (ExpectedHead, AdvanceRef, CreateRefIfMissing, Protected), so replaying an
// idempotency key with flipped control fields fails closed instead of
// returning a false-success replay.
func TestChangesetIdempotencyBindsRefMutationIntent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-idem-controls")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-idem-controls", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-idem-controls", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	req := idempotentRequest("proj-idem-controls", ref, root.ChangesetID, "p1", "control-key", "alpha")
	first, err := s.CreateChangeset(context.Background(), req)
	if err != nil {
		t.Fatalf("detached create: %v", err)
	}

	// Byte-identical replay of the stored request still succeeds.
	again, err := s.CreateChangeset(context.Background(), req)
	if err != nil {
		t.Fatalf("identical replay must succeed: %v", err)
	}
	if again.ChangesetID != first.ChangesetID {
		t.Fatalf("identical replay returned %s, want %s", again.ChangesetID, first.ChangesetID)
	}

	// Same key and same content but flipped ref-mutation intent: rejected, and
	// the ref must not advance on the back of a false-success replay.
	flipped := idempotentRequest("proj-idem-controls", ref, root.ChangesetID, "p1", "control-key", "alpha")
	flipped.AdvanceRef = true
	flipped.ExpectedHead = root.ChangesetID
	if _, err := s.CreateChangeset(context.Background(), flipped); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("control-field replay error = %v, want ErrIdempotencyMismatch", err)
	}
	gotRef, err := s.GetMemoryRef(context.Background(), "proj-idem-controls", ref)
	if err != nil {
		t.Fatalf("GetMemoryRef: %v", err)
	}
	if gotRef.HeadChangesetID != root.ChangesetID {
		t.Fatalf("ref advanced to %s after rejected replay; want %s", gotRef.HeadChangesetID, root.ChangesetID)
	}
	stored, err := s.GetChangeset(context.Background(), first.ChangesetID)
	if err != nil {
		t.Fatalf("GetChangeset: %v", err)
	}
	if stored.RequestDigest == "" {
		t.Fatal("new changeset did not persist request digest")
	}

	// A create that advances from the start replays identically as well.
	advancing := idempotentRequest("proj-idem-controls", ref, root.ChangesetID, "p1", "advancing-key", "beta")
	advancing.AdvanceRef = true
	advancing.ExpectedHead = root.ChangesetID
	advanced, err := s.CreateChangeset(context.Background(), advancing)
	if err != nil {
		t.Fatalf("advancing create: %v", err)
	}
	replayed, err := s.CreateChangeset(context.Background(), advancing)
	if err != nil {
		t.Fatalf("identical advancing replay must succeed even though the ref moved: %v", err)
	}
	if replayed.ChangesetID != advanced.ChangesetID {
		t.Fatalf("advancing replay returned %s, want %s", replayed.ChangesetID, advanced.ChangesetID)
	}
}

// Rows written before migration 013 carry no request digest. Their historic
// ref-mutation intent is unrecoverable, so replaying their idempotency keys
// fails closed rather than guessing.
func TestChangesetIdempotencyLegacyRowFailsClosed(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-idem-legacy")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-idem-legacy", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-idem-legacy", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	req := idempotentRequest("proj-idem-legacy", ref, root.ChangesetID, "p1", "legacy-key", "alpha")
	first, err := s.CreateChangeset(context.Background(), req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate a pre-013 row: no persisted request digest.
	if _, err := s.Exec("UPDATE memory_changesets SET request_digest = '' WHERE changeset_id = ?", first.ChangesetID); err != nil {
		t.Fatalf("clear request digest: %v", err)
	}
	if _, err := s.CreateChangeset(context.Background(), req); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("legacy-row replay error = %v, want ErrIdempotencyMismatch", err)
	}
}

func TestChangesetIdempotencyConcurrentMismatch(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-idem-race")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-idem-race", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-idem-race", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	const perSide = 20
	var wg sync.WaitGroup
	type outcome struct {
		content string
		id      string
		err     error
	}
	results := make(chan outcome, 2*perSide)
	for i := 0; i < perSide; i++ {
		for _, content := range []string{"alpha", "beta"} {
			wg.Add(1)
			go func(content string) {
				defer wg.Done()
				cs, err := s.CreateChangeset(context.Background(),
					idempotentRequest("proj-idem-race", ref, root.ChangesetID, "p1", "race-key", content))
				if err != nil {
					results <- outcome{err: err}
					return
				}
				results <- outcome{content: content, id: cs.ChangesetID}
			}(content)
		}
	}
	wg.Wait()
	close(results)

	var winnerID, winnerContent string
	mismatches := 0
	for r := range results {
		if r.err != nil {
			if !errors.Is(r.err, ErrIdempotencyMismatch) {
				t.Fatalf("unexpected error: %v", r.err)
			}
			mismatches++
			continue
		}
		if winnerID == "" {
			winnerID, winnerContent = r.id, r.content
			continue
		}
		if r.id != winnerID {
			t.Fatalf("two changesets won the key: %s and %s", winnerID, r.id)
		}
		if r.content != winnerContent {
			t.Fatalf("winning content mismatch: %s vs %s", winnerContent, r.content)
		}
	}
	if winnerID == "" {
		t.Fatal("no request succeeded")
	}
	if mismatches == 0 {
		t.Fatal("expected the losing side to be rejected with ErrIdempotencyMismatch")
	}

	// Exactly one changeset exists for the key.
	stored, err := s.FindChangesetByIdempotency(context.Background(), "proj-idem-race", "p1", "race-key")
	if err != nil || stored == nil {
		t.Fatalf("stored lookup: %v", err)
	}
	if stored.ChangesetID != winnerID {
		t.Fatalf("stored %s, want winner %s", stored.ChangesetID, winnerID)
	}
}

// --- High: SQLite contention ---

func TestMemoryGitConcurrentBranchWrites(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-conc")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-conc", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	const total = 200
	const workers = 50
	sem := make(chan struct{}, workers)
	errs := make(chan error, total)
	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ref := fmt.Sprintf("refs/agents/agent-%d/main", i)
			if _, err := s.CreateMemoryBranch(context.Background(), "proj-conc", ref, root.ChangesetID, "p1", false); err != nil {
				errs <- fmt.Errorf("branch %d: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	refs, err := s.ListMemoryRefs(context.Background(), "proj-conc")
	if err != nil {
		t.Fatalf("ListMemoryRefs: %v", err)
	}
	// 200 branches + shared/main from the legacy root.
	if len(refs) != total+1 {
		t.Fatalf("refs = %d, want %d", len(refs), total+1)
	}
}

func TestMemoryGitConcurrentChangesetCommits(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-conc-cs")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-conc-cs", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}

	const writers = 50
	refs := make([]string, writers)
	for i := 0; i < writers; i++ {
		refs[i] = fmt.Sprintf("refs/agents/writer-%d/main", i)
		if _, err := s.CreateMemoryBranch(context.Background(), "proj-conc-cs", refs[i], root.ChangesetID, "p1", false); err != nil {
			t.Fatalf("branch %d: %v", i, err)
		}
	}

	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := idempotentRequest("proj-conc-cs", refs[i], root.ChangesetID, "p1", fmt.Sprintf("k-%d", i), fmt.Sprintf("note-%d", i))
			req.ExpectedHead = root.ChangesetID
			req.AdvanceRef = true
			cs, err := s.CreateChangeset(context.Background(), req)
			if err != nil {
				errs <- fmt.Errorf("writer %d: %w", i, err)
				return
			}
			ref, err := s.GetMemoryRef(context.Background(), "proj-conc-cs", refs[i])
			if err != nil {
				errs <- fmt.Errorf("writer %d ref: %w", i, err)
				return
			}
			if ref.HeadChangesetID != cs.ChangesetID {
				errs <- fmt.Errorf("writer %d: head %s, want %s", i, ref.HeadChangesetID, cs.ChangesetID)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// --- High: database permissions ---

func TestOpenSecurePermissions(t *testing.T) {
	tmp := t.TempDir()
	dbPath := tmp + "/secure/nested/test.db"
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	assertPerm := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}

	assertPerm(tmp+"/secure", 0700)
	assertPerm(dbPath, 0600)
	// WAL/SHM exist after migration writes and must be owner-only too.
	assertPerm(dbPath+"-wal", 0600)
	assertPerm(dbPath+"-shm", 0600)
}

// --- Medium: integrity digest ---

func TestComputeChangesetDigestPreservesParentOrder(t *testing.T) {
	base := &models.MemoryChangeset{
		SchemaVersion:   models.MemoryGitSchemaVersion,
		ProjectID:       "p",
		RefName:         "refs/shared/main",
		AuthorPrincipal: "a",
		Message:         "merge",
		IdempotencyKey:  "m-1",
		Ops:             []models.MemorySemanticOp{},
		Evidence:        []map[string]any{},
		Verification:    []map[string]any{},
	}
	ab := *base
	ab.ParentIDs = []string{"a", "b"}
	ba := *base
	ba.ParentIDs = []string{"b", "a"}
	if ComputeChangesetDigest(&ab) == ComputeChangesetDigest(&ba) {
		t.Fatal("digest collided for different parent order")
	}

	single := *base
	single.ParentIDs = []string{"a"}
	if ComputeChangesetDigest(&ab) == ComputeChangesetDigest(&single) {
		t.Fatal("digest collided for different parent sets")
	}
}

func TestGetChangesetDetectsTampering(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-tamper")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-tamper", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-tamper", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}
	cs, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-tamper", ref, root.ChangesetID, "p1", "k1", "alpha"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Sanity: untampered record loads.
	if _, err := s.GetChangeset(context.Background(), cs.ChangesetID); err != nil {
		t.Fatalf("GetChangeset clean: %v", err)
	}

	// Tamper with the stored message; the digest must catch it on read.
	if _, err := s.Exec("UPDATE memory_changesets SET message = 'forged' WHERE changeset_id = ?", cs.ChangesetID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := s.GetChangeset(context.Background(), cs.ChangesetID); !errors.Is(err, ErrIntegrityDigestMismatch) {
		t.Fatalf("tampered read error = %v, want ErrIntegrityDigestMismatch", err)
	}
}

func TestVerifyChangesetChain(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-chain")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-chain", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-chain", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}
	if _, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-chain", ref, root.ChangesetID, "p1", "k1", "alpha")); err != nil {
		t.Fatalf("create: %v", err)
	}

	verified, failures := s.VerifyChangesetChain(context.Background(), "proj-chain")
	if len(failures) != 0 {
		t.Fatalf("clean chain failures: %v", failures)
	}
	if verified != 2 { // legacy root + one commit
		t.Fatalf("verified = %d, want 2", verified)
	}

	if _, err := s.Exec("UPDATE memory_changesets SET parent_ids_json = '[\"tampered\"]' WHERE idempotency_key = 'k1'"); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	_, failures = s.VerifyChangesetChain(context.Background(), "proj-chain")
	if len(failures) == 0 {
		t.Fatal("corrupted chain was not reported")
	}
}

func TestMigrateChangesetDigestsV2UpgradesV1Rows(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-digest-mig")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-digest-mig", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-digest-mig", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}

	// Two-parent merge: parent order matters for v2.
	req := idempotentRequest("proj-digest-mig", ref, root.ChangesetID, "p1", "merge-k", "merged")
	parentA, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-digest-mig", ref, root.ChangesetID, "p1", "pa", "parent-a"))
	if err != nil {
		t.Fatalf("parentA: %v", err)
	}
	parentB, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-digest-mig", ref, root.ChangesetID, "p1", "pb", "parent-b"))
	if err != nil {
		t.Fatalf("parentB: %v", err)
	}
	req.ParentIDs = []string{parentB.ChangesetID, parentA.ChangesetID} // unsorted on purpose
	merge, err := s.CreateChangeset(context.Background(), req)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	v2 := merge.IntegrityDigest

	// Simulate a pre-upgrade row: v1 hashed sorted parents.
	v1Copy := *merge
	v1Copy.ParentIDs = append([]string(nil), merge.ParentIDs...)
	sort.Strings(v1Copy.ParentIDs)
	v1 := ComputeChangesetDigest(&v1Copy)
	if v1 == v2 {
		t.Fatal("test premise broken: v1 and v2 digests match for unsorted parents")
	}
	if _, err := s.Exec("UPDATE memory_changesets SET integrity_digest = ? WHERE changeset_id = ?", v1, merge.ChangesetID); err != nil {
		t.Fatalf("downgrade digest: %v", err)
	}

	// Reads now reject the legacy digest...
	if _, err := s.GetChangeset(context.Background(), merge.ChangesetID); !errors.Is(err, ErrIntegrityDigestMismatch) {
		t.Fatalf("pre-migration read error = %v, want ErrIntegrityDigestMismatch", err)
	}

	// ...until the migration upgrades it.
	if _, err := s.Exec("DELETE FROM schema_versions WHERE name = '011_changeset_digests_v2'"); err != nil {
		t.Fatalf("clear marker: %v", err)
	}
	if err := migrateChangesetDigestsV2(s.DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, err := s.GetChangeset(context.Background(), merge.ChangesetID)
	if err != nil {
		t.Fatalf("post-migration read: %v", err)
	}
	if got.IntegrityDigest != v2 {
		t.Fatalf("digest = %s, want v2 %s", got.IntegrityDigest, v2)
	}

	// Migration is idempotent.
	if err := migrateChangesetDigestsV2(s.DB); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
}

func TestMigrateChangesetDigestsV2RefusesTamperedRows(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-digest-tamper")
	root, _, err := s.EnsureLegacyRoot(context.Background(), "proj-digest-tamper", "system")
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	ref := "refs/agents/a/main"
	if _, err := s.CreateMemoryBranch(context.Background(), "proj-digest-tamper", ref, root.ChangesetID, "p1", false); err != nil {
		t.Fatalf("branch: %v", err)
	}
	cs, err := s.CreateChangeset(context.Background(), idempotentRequest("proj-digest-tamper", ref, root.ChangesetID, "p1", "k1", "alpha"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Forge a digest that matches neither v1 nor v2 (pre-upgrade tampering).
	const forged = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	if _, err := s.Exec("UPDATE memory_changesets SET integrity_digest = ? WHERE changeset_id = ?", forged, cs.ChangesetID); err != nil {
		t.Fatalf("forge digest: %v", err)
	}
	if _, err := s.Exec("DELETE FROM schema_versions WHERE name = '011_changeset_digests_v2'"); err != nil {
		t.Fatalf("clear marker: %v", err)
	}

	err = migrateChangesetDigestsV2(s.DB)
	if !errors.Is(err, ErrIntegrityDigestMismatch) {
		t.Fatalf("migration error = %v, want ErrIntegrityDigestMismatch", err)
	}

	// The forged row is not blessed and the migration did not record itself.
	var stored string
	if err := s.QueryRow("SELECT integrity_digest FROM memory_changesets WHERE changeset_id = ?", cs.ChangesetID).Scan(&stored); err != nil {
		t.Fatalf("read stored digest: %v", err)
	}
	if stored != forged {
		t.Fatal("tampered row was rehashed and blessed")
	}
	var marker int
	if err := s.QueryRow("SELECT COUNT(*) FROM schema_versions WHERE name = '011_changeset_digests_v2'").Scan(&marker); err != nil {
		t.Fatalf("read marker: %v", err)
	}
	if marker != 0 {
		t.Fatal("failed migration recorded its marker")
	}
}
