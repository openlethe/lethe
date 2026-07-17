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
