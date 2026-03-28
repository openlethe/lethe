package db

import (
	"context"
	"testing"
	"time"

	"github.com/openlethe/lethe/internal/models"
)

func newTestStore(t *testing.T) (*Store, func()) {
	tmp := t.TempDir() + "/test.db"
	s, err := NewStore(tmp)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, func() { s.Close() }
}

// setupAgentProject creates a linked agent+project for session tests.
func setupAgentProject(t *testing.T, s *Store, agentID, projectID string) {
	if err := s.UpsertAgent(context.Background(), &models.Agent{AgentID: agentID, Name: "TestAgent"}); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if err := s.UpsertProject(context.Background(), &models.Project{ProjectID: projectID, Name: "TestProj"}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func TestUpsertAgent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	a := &models.Agent{AgentID: "agent-1", Name: "Archimedes"}
	if err := s.UpsertAgent(context.Background(), a); err != nil {
		t.Fatalf("UpsertAgent: %v", err)
	}
	if err := s.TouchAgent(context.Background(), "agent-1"); err != nil {
		t.Fatalf("TouchAgent: %v", err)
	}
}

func TestUpsertProject(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()

	p := &models.Project{ProjectID: "proj-1", Name: "WAGMIOS"}
	if err := s.UpsertProject(context.Background(), p); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func TestCreateAndGetSession(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	if err := s.CreateSession(context.Background(), s1); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess, err := s.GetSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found")
	}
	if sess.State != models.SessionActive {
		t.Errorf("state=%v, want active", sess.State)
	}
}

func TestUpdateSessionState(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	if err := s.CreateSession(context.Background(), s1); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.UpdateSessionState(context.Background(), "sess-1", models.SessionCompleted, "test summary", nil); err != nil {
		t.Fatalf("UpdateSessionState: %v", err)
	}

	sess, err := s.GetSession(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session nil after update")
	}
	if sess.Summary != "test summary" {
		t.Errorf("summary=%q, want %q", sess.Summary, "test summary")
	}
	if sess.State != models.SessionCompleted {
		t.Errorf("state=%v, want completed", sess.State)
	}
}

func TestTouchSessionHeartbeat(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	if err := s.TouchSessionHeartbeat(context.Background(), "sess-1"); err != nil {
		t.Fatalf("TouchSessionHeartbeat: %v", err)
	}
}

func TestGetInterruptedSession(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	sess, err := s.GetInterruptedSession(context.Background(), "agent-1", "proj-1")
	if err != nil {
		t.Fatalf("GetInterruptedSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil, got %v", sess)
	}

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionInterrupted}
	s.CreateSession(context.Background(), s1)

	sess, err = s.GetInterruptedSession(context.Background(), "agent-1", "proj-1")
	if err != nil {
		t.Fatalf("GetInterruptedSession: %v", err)
	}
	if sess == nil || sess.SessionID != "sess-1" {
		t.Errorf("expected sess-1, got %v", sess)
	}
}

func TestCheckpointCreateAndGet(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	c := &models.Checkpoint{
		CheckpointID: "cp-1",
		SessionID:    "sess-1",
		Snapshot: models.Snapshot{
			OpenThreads:    []string{"task-1"},
			RecentEventIDs: []string{"evt-1"},
			CurrentTask:   "deploying WAGMIOS",
			LastTool:      "docker compose up",
		},
	}
	if err := s.CreateCheckpoint(context.Background(), c); err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}

	cp, err := s.GetLatestCheckpoint(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("GetLatestCheckpoint: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint not found")
	}
	if cp.Seq != 1 {
		t.Errorf("seq=%d, want 1", cp.Seq)
	}
	if len(cp.Snapshot.OpenThreads) != 1 || cp.Snapshot.OpenThreads[0] != "task-1" {
		t.Errorf("snapshot=%v", cp.Snapshot)
	}

	c2 := &models.Checkpoint{CheckpointID: "cp-2", SessionID: "sess-1", Snapshot: models.Snapshot{}}
	s.CreateCheckpoint(context.Background(), c2)
	cp, _ = s.GetLatestCheckpoint(context.Background(), "sess-1")
	if cp.Seq != 2 {
		t.Errorf("seq=%d, want 2", cp.Seq)
	}
}

func TestCreateAndGetEvent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	e := &models.Event{
		EventID:   "evt-1",
		SessionID: "sess-1",
		EventType: models.EventRecord,
		Content:   "Decided to use Docker Compose v2",
	}
	if err := s.CreateEvent(context.Background(), e); err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}

	events, err := s.GetSessionEvents(context.Background(), "sess-1", 10, 0)
	if err != nil {
		t.Fatalf("GetSessionEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len=%d, want 1", len(events))
	}
	if events[0].Content != "Decided to use Docker Compose v2" {
		t.Errorf("content=%q", events[0].Content)
	}
}

func TestTaskEvent(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	ts := models.TaskInProgress
	e := &models.Event{
		EventID:    "evt-task-1",
		SessionID:  "sess-1",
		EventType:  models.EventTask,
		Content:    "Working on Docker setup",
		TaskTitle:  "implement Docker compose setup",
		TaskStatus: &ts,
	}
	if err := s.CreateEvent(context.Background(), e); err != nil {
		t.Fatalf("CreateEvent task: %v", err)
	}

	events, _ := s.GetSessionEvents(context.Background(), "sess-1", 10, 0)
	if events[0].TaskTitle != "implement Docker compose setup" {
		t.Errorf("task_title=%q", events[0].TaskTitle)
	}
	if *events[0].TaskStatus != models.TaskInProgress {
		t.Errorf("task_status=%v", *events[0].TaskStatus)
	}
}

func TestFlagEventWithConfidence(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	conf := 0.3
	e := &models.Event{
		EventID:    "evt-flag-1",
		SessionID:  "sess-1",
		EventType:  models.EventFlag,
		Content:    "Not sure if volume mounts are correct",
		Confidence: &conf,
	}
	if err := s.CreateEvent(context.Background(), e); err != nil {
		t.Fatalf("CreateEvent flag: %v", err)
	}

	flags, err := s.GetFlaggedEvents(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("GetFlaggedEvents: %v", err)
	}
	if len(flags) != 1 {
		t.Fatalf("len=%d, want 1", len(flags))
	}
	if *flags[0].Confidence != 0.3 {
		t.Errorf("confidence=%v", *flags[0].Confidence)
	}
}

func TestMarkFlagReviewed(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	conf := 0.3
	e := &models.Event{
		EventID:    "evt-flag-1",
		SessionID:  "sess-1",
		EventType:  models.EventFlag,
		Content:    "Uncertain about X",
		Confidence: &conf,
	}
	s.CreateEvent(context.Background(), e)

	if err := s.MarkFlagReviewed(context.Background(), "evt-flag-1", "human-mike"); err != nil {
		t.Fatalf("MarkFlagReviewed: %v", err)
	}

	flags, _ := s.GetFlaggedEvents(context.Background(), 10, 0)
	if len(flags) != 0 {
		t.Errorf("flags after review: %d, want 0", len(flags))
	}
}

func TestTaskChain(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	ts1 := models.TaskTodo
	e1 := &models.Event{EventID: "evt-1", SessionID: "sess-1", EventType: models.EventTask, Content: "task created", TaskTitle: "setup", TaskStatus: &ts1}
	s.CreateEvent(context.Background(), e1)

	ts2 := models.TaskInProgress
	e2 := &models.Event{EventID: "evt-2", SessionID: "sess-1", ParentEventID: "evt-1", EventType: models.EventTask, Content: "started", TaskTitle: "setup", TaskStatus: &ts2}
	s.CreateEvent(context.Background(), e2)

	ts3 := models.TaskDone
	e3 := &models.Event{EventID: "evt-3", SessionID: "sess-1", ParentEventID: "evt-2", EventType: models.EventTask, Content: "done", TaskTitle: "setup", TaskStatus: &ts3}
	s.CreateEvent(context.Background(), e3)

	chain, err := s.GetTaskChain(context.Background(), "evt-3")
	if err != nil {
		t.Fatalf("GetTaskChain: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain len=%d, want 3", len(chain))
	}
	if chain[0].EventID != "evt-3" {
		t.Errorf("chain[0]=%s", chain[0].EventID)
	}
	if *chain[0].TaskStatus != models.TaskDone {
		t.Errorf("first should be done, got %v", *chain[0].TaskStatus)
	}
}

func TestSessionLink(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionCompleted}
	s2 := &models.Session{SessionID: "sess-2", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)
	s.CreateSession(context.Background(), s2)

	link := &models.SessionLink{SessionID: "sess-2", PriorSessionID: "sess-1", LinkType: "resume"}
	if err := s.CreateSessionLink(context.Background(), link); err != nil {
		t.Fatalf("CreateSessionLink: %v", err)
	}
}

func TestCreateEventSetsCreatedAt(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	setupAgentProject(t, s, "agent-1", "proj-1")
	setupAgentProject(t, s, "agent-1", "proj-1")

	s1 := &models.Session{SessionID: "sess-1", AgentID: "agent-1", ProjectID: "proj-1", State: models.SessionActive}
	s.CreateSession(context.Background(), s1)

	before := time.Now().UTC().Add(-time.Second)
	e := &models.Event{EventID: "evt-1", SessionID: "sess-1", EventType: models.EventLog, Content: "test"}
	s.CreateEvent(context.Background(), e)
	after := time.Now().UTC().Add(time.Second)

	events, _ := s.GetSessionEvents(context.Background(), "sess-1", 1, 0)
	if events[0].CreatedAt.Before(before) || events[0].CreatedAt.After(after) {
		t.Errorf("created_at=%v, want between %v and %v", events[0].CreatedAt, before, after)
	}
}
