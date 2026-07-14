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

	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

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

	mergeMessage := mergeAuthorizationMessage("project-test", "refs/shared/main", root.ChangesetID, left.ChangesetID, "proposal-1", "reviewer")
	mac := hmac.New(sha256.New, []byte("0123456789abcdef0123456789abcdef"))
	_, _ = mac.Write([]byte(mergeMessage))
	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID,
		"merge_proposal_id": "proposal-1", "reviewer_principal": "reviewer", "merge_authorization": "00"})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/merge", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("forged merge authorization status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]string{"ref_name": "refs/shared/main", "expected_head": root.ChangesetID, "new_head": left.ChangesetID,
		"merge_proposal_id": "proposal-1", "reviewer_principal": "reviewer", "merge_authorization": hex.EncodeToString(mac.Sum(nil))})
	req = httptest.NewRequest(http.MethodPost, "/memory/project-test/refs/merge", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge advance status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/memory/project-test/refs/resolve?name=refs%2Fshared%2Fmain", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec = httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve slash ref status=%d body=%s", rec.Code, rec.Body.String())
	}
}
