package session

import (
	"context"
	"testing"

	"github.com/openlethe/lethe/internal/db"
	"github.com/openlethe/lethe/internal/models"
)

func newTestManager(t *testing.T) (*Manager, func()) {
	tmp := t.TempDir() + "/test.db"
	s, err := db.NewStore(tmp)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return NewManager(s), func() { s.Close() }
}

func TestStartSession(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, err := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if sess.State != models.SessionActive {
		t.Errorf("state=%v, want active", sess.State)
	}
	if sess.SessionID == "" {
		t.Error("session_id should not be empty")
	}
}

func TestResumeSession(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	// Start then interrupt a session.
	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")
	m.InterruptSession(context.Background(), sess, nil)

	// Resume it.
	resumed, err := m.ResumeSession(context.Background(), "agent-1", "proj-1")
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if resumed.State != models.SessionActive {
		t.Errorf("state=%v, want active", resumed.State)
	}
	if resumed.SessionID != sess.SessionID {
		t.Errorf("session_id changed: %s → %s", sess.SessionID, resumed.SessionID)
	}
}

func TestResumeSessionNoInterrupted(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	// Start a fresh session — no interrupted sessions.
	m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	_, err := m.ResumeSession(context.Background(), "agent-1", "proj-1")
	if err != ErrNoInterrupted {
		t.Errorf("err=%v, want ErrNoInterrupted", err)
	}
}

func TestInterruptSession(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	err := m.InterruptSession(context.Background(), sess, nil)
	if err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}

	// Verify via store.
	store := m.store
	sess2, _ := store.GetSession(context.Background(), sess.SessionID)
	if sess2.State != models.SessionInterrupted {
		t.Errorf("state=%v, want interrupted", sess2.State)
	}
}

func TestCompleteSession(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	err := m.CompleteSession(context.Background(), sess, "Finished build")
	if err != nil {
		t.Fatalf("CompleteSession: %v", err)
	}

	sess2, _ := m.store.GetSession(context.Background(), sess.SessionID)
	if sess2.State != models.SessionCompleted {
		t.Errorf("state=%v, want completed", sess2.State)
	}
	if sess2.Summary != "Finished build" {
		t.Errorf("summary=%q", sess2.Summary)
	}
}

func TestInterruptWritesCheckpoint(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	snap := &models.Snapshot{
		OpenThreads:    []string{"task-1"},
		CurrentTask:   "deploying",
		LastTool:      "docker",
	}
	err := m.InterruptSession(context.Background(), sess, snap)
	if err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}

	cp, err := m.store.GetLatestCheckpoint(context.Background(), sess.SessionID)
	if err != nil {
		t.Fatalf("GetLatestCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint should have been written")
	}
	if cp.Snapshot.CurrentTask != "deploying" {
		t.Errorf("current_task=%q", cp.Snapshot.CurrentTask)
	}
}

func TestInvalidTransition(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")
	m.CompleteSession(context.Background(), sess, "")

	// Can't interrupt a completed session.
	err := m.InterruptSession(context.Background(), sess, nil)
	if err == nil {
		t.Error("expected error on invalid transition")
	}
}

func TestHeartbeat(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	if err := m.Heartbeat(context.Background(), sess.SessionID, 0); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
}

func TestSameStateNoOp(t *testing.T) {
	m, cleanup := newTestManager(t)
	defer cleanup()

	sess, _ := m.StartSession(context.Background(), "agent-1", "proj-1", "Archimedes", "WAGMIOS")

	// Transitioning to same state should not error.
	err := m.InterruptSession(context.Background(), sess, nil)
	if err != nil {
		t.Fatalf("InterruptSession: %v", err)
	}

	// Resume back to active.
	sess.State = models.SessionActive
	err = m.store.UpdateSessionState(context.Background(), sess.SessionID, models.SessionActive, "", nil)
	if err != nil {
		t.Fatalf("UpdateSessionState: %v", err)
	}

	err = m.InterruptSession(context.Background(), sess, nil) // already interrupted → interrupted
	if err != nil {
		t.Fatalf("InterruptSession same state: %v", err)
	}
}
