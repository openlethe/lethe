package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openlethe/lethe/internal/db"
)

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  Mode
	}{
		{"", ModeLegacy},
		{"legacy", ModeLegacy},
		{"GIT", ModeGit},
		{" hybrid ", ModeHybrid},
	} {
		got, err := ParseMode(tc.input)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMode(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if _, err := ParseMode("unknown"); err == nil {
		t.Fatal("ParseMode(unknown) unexpectedly succeeded")
	}
}

func TestModeRouteIsolation(t *testing.T) {
	store, err := db.NewStore(t.TempDir() + "/mode.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	for _, tc := range []struct {
		name         string
		mode         Mode
		legacyStatus int
		gitStatus    int
	}{
		{"legacy", ModeLegacy, http.StatusOK, http.StatusNotFound},
		{"git", ModeGit, http.StatusNotFound, http.StatusOK},
		{"hybrid", ModeHybrid, http.StatusOK, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := NewServer(store, nil, WithMode(tc.mode), WithAuthToken("test-token"))
			defer srv.StopBroadcaster()

			assertRouteStatus(t, srv, "/sessions", tc.legacyStatus)
			assertRouteStatus(t, srv, "/memory/project-mode/refs", tc.gitStatus)

			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Header.Set("Authorization", "Bearer test-token")
			rec := httptest.NewRecorder()
			srv.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("health status = %d", rec.Code)
			}
			if got := rec.Body.String(); got == "" || !containsJSONMode(got, tc.mode) {
				t.Fatalf("health body %q does not report mode %q", got, tc.mode)
			}
		})
	}
}

func assertRouteStatus(t *testing.T, srv *Server, path string, want int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s status = %d, want %d; body=%s", path, rec.Code, want, rec.Body.String())
	}
}

func containsJSONMode(body string, mode Mode) bool {
	return body == "{\"mode\":\""+string(mode)+"\",\"status\":\"ok\"}\n" ||
		body == "{\"status\":\"ok\",\"mode\":\""+string(mode)+"\"}\n"
}
