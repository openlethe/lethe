package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
	"github.com/openlethe/lethe/internal/session"
)

func newTestServer(t *testing.T) *Server {
	tmp := t.TempDir() + "/test.db"
	store, err := db.NewStore(tmp)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	ctx := context.Background()
	if err := store.UpsertAgent(ctx, &models.Agent{AgentID: "agent-test", Name: "Test Agent"}); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if err := store.UpsertProject(ctx, &models.Project{ProjectID: "project-test", Name: "Test Project"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if err := store.CreateSession(ctx, &models.Session{
		SessionID: "sess-1",
		AgentID:   "agent-test",
		ProjectID: "project-test",
		State:     models.SessionActive,
		StartedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return NewServer(store, session.NewManager(store))
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() == 0 {
		t.Error("body should not be empty")
	}
}

func TestCreateEventValidation(t *testing.T) {
	srv := newTestServer(t)

	// Missing event_type → 400.
	req := httptest.NewRequest("POST", "/sessions/sess-1/events", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestGetCheckpointsEmpty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/sessions/sess-1/checkpoints", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want %d", rec.Code, http.StatusOK)
	}
}

func TestGetFlagsEmpty(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/flags", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want %d", rec.Code, http.StatusOK)
	}
}

func TestGetTaskChainNotFound(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequest("GET", "/events/nonexistent/chain", nil)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAllRoutesReturnExpected(t *testing.T) {
	srv := newTestServer(t)

	type route struct {
		method string
		path   string
		status int
	}

	routes := []route{
		{"POST", "/sessions", http.StatusBadRequest}, // empty body
		{"GET", "/sessions/sess-1", http.StatusOK},
		{"GET", "/sessions/sess-1/events", http.StatusOK},
		{"POST", "/sessions/sess-1/heartbeat", http.StatusOK},
		{"POST", "/sessions/sess-1/interrupt", http.StatusOK},
		{"POST", "/sessions/sess-1/complete", http.StatusBadRequest}, // empty body
		{"POST", "/sessions/sess-1/events", http.StatusBadRequest},
		{"POST", "/sessions/sess-1/checkpoints", http.StatusBadRequest}, // empty body
		{"GET", "/sessions/sess-1/checkpoints", http.StatusOK},
		{"GET", "/flags", http.StatusOK},                      // implemented
		{"PUT", "/flags/evt-1/review", http.StatusBadRequest}, // missing reviewer_id
		{"GET", "/events/evt-1/chain", http.StatusNotFound},   // no event
	}

	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		rec := httptest.NewRecorder()
		srv.router.ServeHTTP(rec, req)
		if rec.Code != r.status {
			t.Errorf("%s %s: status=%d, want %d", r.method, r.path, rec.Code, r.status)
		}
	}
}
