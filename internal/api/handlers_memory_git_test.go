package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/canonical"
	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

// signTestEnvelope mirrors Charon's merge-authorization signing: HMAC-SHA256
// over the canonical envelope bytes.
func signTestEnvelope(t *testing.T, key string, env MergeAuthorizationEnvelope) string {
	t.Helper()
	canonicalBytes, err := canonical.JSON(env)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write(canonicalBytes)
	signed, err := json.Marshal(map[string]any{
		"envelope":  env,
		"signature": hex.EncodeToString(mac.Sum(nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(signed)
}

func validTestEnvelope(project, refName, expected, newHead, nonce string) MergeAuthorizationEnvelope {
	return MergeAuthorizationEnvelope{
		Version:           MergeAuthorizationVersion,
		ProjectID:         project,
		RefName:           refName,
		ExpectedHead:      expected,
		NewHead:           newHead,
		ProposalID:        "proposal-1",
		ProposalDigest:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ReviewerPrincipal: "reviewer",
		MergerPrincipal:   "merger",
		Strategy:          "fast_forward",
		IssuedAt:          time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano),
		ExpiresAt:         time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano),
		Nonce:             nonce,
		KeyID:             "",
	}
}

func TestMemoryGitConflictAndSlashRefRoutes(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}
	left, err := srv.store.CreateChangeset(ctx, db.CreateChangesetRequest{
		ProjectID: "project-test", RefName: "refs/agents/left/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "left", IdempotencyKey: "left-api", Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, Payload: map[string]any{"content": "same"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := srv.store.CreateChangeset(ctx, db.CreateChangesetRequest{
		ProjectID: "project-test", RefName: "refs/agents/right/main", ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "right", IdempotencyKey: "right-api", Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, Payload: map[string]any{"content": "same"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"base_changeset_id": root.ChangesetID, "left_changeset_id": left.ChangesetID, "right_changeset_id": right.ChangesetID})
	req := httptest.NewRequest(http.MethodPost, "/memory/project-test/conflicts/detect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detect status=%d body=%s", rec.Code, rec.Body.String())
	}
	var detected struct {
		Conflicts []models.MemoryConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detected); err != nil || len(detected.Conflicts) == 0 {
		t.Fatalf("detect response=%s err=%v", rec.Body.String(), err)
	}

	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/advance", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("protected direct advance status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID,
		"merge_proposal_id": "proposal-1", "reviewer_principal": "reviewer", "merge_authorization": "00"})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/merge", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
		t.Fatalf("forged merge authorization status=%d body=%s", rec.Code, rec.Body.String())
	}

	envelope := validTestEnvelope("project-test", "refs/shared/main", root.ChangesetID, left.ChangesetID, "0123456789abcdef0123456789abcdef")
	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID,
		"merge_proposal_id": "proposal-1", "reviewer_principal": "reviewer",
		"merge_authorization": signTestEnvelope(t, "0123456789abcdef0123456789abcdef", envelope)})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/merge", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge advance status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Immediate replay of the same single-use authorization is rejected.
	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID,
		"merge_proposal_id": "proposal-1", "reviewer_principal": "reviewer",
		"merge_authorization": signTestEnvelope(t, "0123456789abcdef0123456789abcdef", envelope)})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/merge", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replayed authorization status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/memory/project-test/refs/resolve?name=refs%2Fshared%2Fmain", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve slash ref status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateManifestCanonicalizesSessionKeyForAssembly(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"direction": "input", "project_id": "project-test",
		"ref_name": models.RefSharedMain, "head_changeset_id": root.ChangesetID,
		"selected_memory_ids": []string{}, "session_id": "stable-session-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/manifests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("manifest status=%d body=%s", rec.Code, rec.Body.String())
	}
	var manifest models.MemoryManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SessionID != "sess-1" {
		t.Fatalf("manifest session_id=%q want canonical sess-1", manifest.SessionID)
	}

	assemblyBody, _ := json.Marshal(map[string]any{
		"assembly_id": "asm-stable-key", "source": "openclaw-plugin",
		"assembler_version": "openclaw-memory-git-v1", "message_count": 1,
		"memory_manifest_id":       manifest.ManifestID,
		"memory_head_changeset_id": root.ChangesetID,
		"packed_bytes":             0, "items": []any{},
	})
	req = httptest.NewRequest(http.MethodPost, "/sessions/stable-session-key/assemblies", bytes.NewReader(assemblyBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("assembly status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGitModeCreateManifestPreservesMCPAttribution(t *testing.T) {
	srv := newTestServer(t)
	srv.mode = ModeGit
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"direction": "input", "project_id": "project-test",
		"ref_name": models.RefSharedMain, "head_changeset_id": root.ChangesetID,
		"selected_memory_ids": []string{}, "session_id": "mcp-session-not-in-legacy-table",
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/manifests", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("manifest status=%d body=%s", rec.Code, rec.Body.String())
	}
	var manifest models.MemoryManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SessionID != "mcp-session-not-in-legacy-table" {
		t.Fatalf("manifest session_id=%q", manifest.SessionID)
	}
}

func TestMemoryContextRouteCreatesReproducibleInputManifest(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}
	branch := "refs/agents/chatgpt/main"
	if _, err := srv.store.CreateMemoryBranch(ctx, "project-test", branch, root.ChangesetID, "chatgpt", false); err != nil {
		t.Fatal(err)
	}
	cs, err := srv.store.CreateChangeset(ctx, db.CreateChangesetRequest{
		ProjectID: "project-test", RefName: branch, ParentIDs: []string{root.ChangesetID},
		AuthorPrincipal: "chatgpt", IdempotencyKey: "context-api", ExpectedHead: root.ChangesetID, AdvanceRef: true,
		Ops: []models.MemorySemanticOp{{
			OpType: models.OpAddMemory, ResultingEventID: "mem-api",
			Payload: map[string]any{"content": "Use the accepted API contract", "kind": "decision"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.CASMergeProtectedRef(ctx, "project-test", models.RefSharedMain, root.ChangesetID, cs.ChangesetID); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"ref_name": models.RefSharedMain, "query": "API contract", "limit": 10,
		"session_id": "sess-1", "actor_id": "chatgpt", "create_manifest": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/project-test/context", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", rec.Code, rec.Body.String())
	}
	var view models.MemoryContext
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.ManifestID == "" || view.HeadChangesetID != cs.ChangesetID {
		t.Fatalf("view missing pin: %#v", view)
	}
	manifest, err := srv.store.GetManifest(ctx, view.ManifestID)
	if err != nil || manifest == nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest.SessionID != "sess-1" || len(manifest.SelectedMemoryIDs) != 1 ||
		manifest.SelectedMemoryIDs[0] != "mem-api" {
		t.Fatalf("manifest mismatch: %#v", manifest)
	}

	assemblyBody, _ := json.Marshal(map[string]any{
		"assembly_id": "asm-memory-context", "source": "test",
		"assembler_version": "openclaw-memory-git-v1", "message_count": 1,
		"memory_manifest_id": view.ManifestID, "memory_head_changeset_id": view.HeadChangesetID,
		"accepted_estimated_tokens": 12, "packed_bytes": 48, "items": []any{},
	})
	req = httptest.NewRequest(http.MethodPost, "/sessions/sess-1/assemblies", bytes.NewReader(assemblyBody))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("assembly status=%d body=%s", rec.Code, rec.Body.String())
	}
	assembly, err := srv.store.GetContextAssembly(ctx, "asm-memory-context")
	if err != nil {
		t.Fatal(err)
	}
	if assembly.MemoryManifestID != view.ManifestID || assembly.MemoryHeadChangesetID != view.HeadChangesetID ||
		assembly.AcceptedEstimatedTokens == nil || *assembly.AcceptedEstimatedTokens != 12 {
		t.Fatalf("assembly memory pin mismatch: %#v", assembly)
	}
}

func TestGitModeMemoryContextPreservesMCPAttribution(t *testing.T) {
	srv := newTestServer(t)
	srv.mode = ModeGit
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"ref_name": models.RefSharedMain, "limit": 10,
		"session_id": "mcp-session-not-in-legacy-table", "actor_id": "charon",
		"create_manifest": true,
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/project-test/context", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("context status=%d body=%s", rec.Code, rec.Body.String())
	}
	var view models.MemoryContext
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.HeadChangesetID != root.ChangesetID || view.ManifestID == "" {
		t.Fatalf("unexpected context: %#v", view)
	}
	manifest, err := srv.store.GetManifest(ctx, view.ManifestID)
	if err != nil || manifest == nil {
		t.Fatalf("get manifest: %v", err)
	}
	if manifest.SessionID != "mcp-session-not-in-legacy-table" {
		t.Fatalf("manifest session_id=%q", manifest.SessionID)
	}
}

func TestCreateChangesetAcceptsSnakeCaseWireFormat(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()
	root, _, err := srv.store.EnsureLegacyRoot(ctx, "project-test", "system")
	if err != nil {
		t.Fatal(err)
	}
	branch := "refs/sessions/principal-test/wire-format"
	if _, err := srv.store.CreateMemoryBranch(ctx, "project-test", branch, root.ChangesetID, "principal-test", false); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{
		"project_id": "project-test", "ref_name": branch,
		"parent_ids": []string{root.ChangesetID}, "author_principal": "principal-test",
		"message": "snake case request", "idempotency_key": "snake-case-request",
		"expected_head": root.ChangesetID, "advance_ref": true,
		"ops": []map[string]any{{
			"op_type": "add_memory", "resulting_event_id": "mem-snake-case",
			"payload": map[string]any{"content": "accepted wire format"},
		}},
	})
	req := httptest.NewRequest(http.MethodPost, "/memory/changesets", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("changeset status=%d body=%s", rec.Code, rec.Body.String())
	}
	updated, err := srv.store.GetMemoryRef(ctx, "project-test", branch)
	if err != nil || updated == nil || updated.HeadChangesetID == root.ChangesetID {
		t.Fatalf("branch was not advanced: ref=%#v err=%v", updated, err)
	}
}
