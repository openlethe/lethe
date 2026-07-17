package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecoveryReadOnlyMode(t *testing.T) {
	srv := newTestServer(t)
	srv.recoveryReadOnly = true
	ctx := context.Background()
	if _, _, err := srv.store.EnsureLegacyRoot(ctx, "project-rec", "system"); err != nil {
		t.Fatal(err)
	}

	// Mutations are rejected with 503.
	mutations := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/memory/project-rec/changesets", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/branches", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/refs/advance", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/refs/merge", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/legacy-root", nil},
		{http.MethodPost, "/memory/manifests", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/conflicts/persist", map[string]any{}},
		{http.MethodPost, "/memory/project-rec/conflicts/retire", map[string]any{}},
	}
	for _, tc := range mutations {
		var reader *bytes.Reader
		if tc.body != nil {
			data, _ := json.Marshal(tc.body)
			reader = bytes.NewReader(data)
		} else {
			reader = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(tc.method, tc.path, reader)
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}

	// Reads and pure conflict analysis stay available.
	req := httptest.NewRequest(http.MethodGet, "/memory/project-rec/refs", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET refs blocked: %d", rec.Code)
	}
}

// Regression (autoreview): recovery mode must also block implicit writes on
// read paths — context reconstruction must not auto-create the legacy root.
func TestRecoveryReadOnlyBlocksImplicitRootCreation(t *testing.T) {
	srv := newTestServer(t)
	srv.recoveryReadOnly = true
	srv.store.SetRecoveryReadOnly(true)

	_, err := srv.store.BuildMemoryContext(context.Background(), "project-never-created", "refs/shared/main", "", "", 10)
	if err == nil {
		t.Fatal("context on unknown project succeeded in recovery mode")
	}
	// And it must not have created anything.
	var n int
	if err := srv.store.QueryRow(`SELECT COUNT(*) FROM memory_refs WHERE project_id = 'project-never-created'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("recovery read path created %d refs", n)
	}
	var ncs int
	if err := srv.store.QueryRow(`SELECT COUNT(*) FROM memory_changesets WHERE project_id = 'project-never-created'`).Scan(&ncs); err != nil {
		t.Fatal(err)
	}
	if ncs != 0 {
		t.Fatalf("recovery read path created %d changesets", ncs)
	}

	// With recovery off, the same call lazily initializes as before.
	srv.store.SetRecoveryReadOnly(false)
	if _, err := srv.store.BuildMemoryContext(context.Background(), "project-never-created", "refs/shared/main", "", "", 10); err != nil {
		t.Fatalf("normal mode context failed: %v", err)
	}
}

// Regression (autoreview round 6): recovery mode must also block legacy
// baseline capture during context reconstruction — a read path must not
// insert into memory_legacy_baselines.
func TestRecoveryReadOnlyBlocksBaselineCapture(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	// Build a project with a legacy root but deliberately no captured baseline
	// (simulating a restored/upgraded database).
	if _, _, err := srv.store.EnsureLegacyRoot(ctx, "project-nobaseline", "system"); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.ExecContext(ctx, `DELETE FROM memory_legacy_baselines WHERE project_id = 'project-nobaseline'`); err != nil {
		t.Fatal(err)
	}

	srv.store.SetRecoveryReadOnly(true)
	_, err := srv.store.BuildMemoryContext(ctx, "project-nobaseline", "refs/shared/main", "", "", 10)
	if err == nil {
		t.Fatal("context with missing baseline succeeded in recovery mode")
	}
	var n int
	if err := srv.store.QueryRow(`SELECT COUNT(*) FROM memory_legacy_baselines WHERE project_id = 'project-nobaseline'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("recovery read captured %d baselines", n)
	}

	// Outside recovery the same call captures the baseline and succeeds.
	srv.store.SetRecoveryReadOnly(false)
	if _, err := srv.store.BuildMemoryContext(ctx, "project-nobaseline", "refs/shared/main", "", "", 10); err != nil {
		t.Fatalf("normal mode context failed: %v", err)
	}
}
