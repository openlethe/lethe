package models

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Agent represents a registered agent.
type Agent struct {
	AgentID    string       `json:"agent_id"`
	Name      string       `json:"name"`
	CreatedAt  time.Time   `json:"created_at"`
	LastSeenAt sql.NullTime `json:"-"`
}

// Project represents a project namespace.
type Project struct {
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionState is the state of a session.
type SessionState string

const (
	SessionActive      SessionState = "active"
	SessionInterrupted SessionState = "interrupted"
	SessionCompleted  SessionState = "completed"
)

// Session represents a Lethe session.
type Session struct {
	SessionID        string       `json:"session_id"`
	SessionKey       string       `json:"session_key,omitempty"`
	AgentID          string       `json:"agent_id"`
	ProjectID        string       `json:"project_id"`
	State            SessionState `json:"state"`
	StartedAt        time.Time    `json:"started_at"`
	LastHeartbeatAt  sql.NullTime `json:"-"`
	EndedAt          sql.NullTime `json:"-"`
	Summary          string       `json:"summary,omitempty"`
}

// Checkpoint represents a session checkpoint snapshot.
type Checkpoint struct {
	CheckpointID string    `json:"checkpoint_id"`
	SessionID   string    `json:"session_id"`
	Seq         int       `json:"seq"`
	Snapshot    Snapshot  `json:"snapshot"`
	CreatedAt   time.Time `json:"created_at"`
}

// Snapshot is the JSON blob stored in a checkpoint.
type Snapshot struct {
	OpenThreads    []string `json:"open_threads"`
	RecentEventIDs []string `json:"recent_event_ids"`
	CurrentTask   string   `json:"current_task"`
	LastTool      string   `json:"last_tool"`
}

// MarshalSnapshot encodes a Snapshot to JSON bytes.
func MarshalSnapshot(s Snapshot) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// UnmarshalSnapshot decodes JSON bytes to a Snapshot.
func UnmarshalSnapshot(data string) (Snapshot, error) {
	var s Snapshot
	err := json.Unmarshal([]byte(data), &s)
	return s, err
}

// EventType distinguishes record, log, flag, and task events.
type EventType string

const (
	EventRecord EventType = "record"
	EventLog    EventType = "log"
	EventFlag   EventType = "flag"
	EventTask   EventType = "task"
)

// TaskStatus is the status of a task event.
type TaskStatus string

const (
	TaskTodo       TaskStatus = "todo"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskBlocked    TaskStatus = "blocked"
)

// Event represents a Lethe event.
type Event struct {
	EventID         string       `json:"event_id"`
	SessionID       string      `json:"session_id"`
	ParentEventID   string      `json:"parent_event_id,omitempty"`
	EventType       EventType   `json:"event_type"`
	Content         string      `json:"content"`
	Confidence      *float64    `json:"confidence,omitempty"`
	Tags            string      `json:"tags,omitempty"`
	EmbeddingID    string      `json:"embedding_id,omitempty"`
	TaskTitle       string      `json:"task_title,omitempty"`
	TaskStatus      *TaskStatus `json:"task_status,omitempty"`
	StatusChangedAt sql.NullTime `json:"-"`
	HumanReviewedAt sql.NullTime `json:"-"`
	ReviewerID     string      `json:"reviewer_id,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
}

// SessionLink records a relationship between two sessions.
type SessionLink struct {
	SessionID       string `json:"session_id"`
	PriorSessionID  string `json:"prior_session_id"`
	LinkType        string `json:"link_type"`
}
