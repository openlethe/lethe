package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mentholmike/lethe/internal/db"
	"github.com/mentholmike/lethe/internal/models"
)

var (
	ErrInvalidTransition = errors.New("invalid session state transition")
	ErrNoInterrupted     = errors.New("no interrupted session found")
)

// Manager handles session lifecycle transitions.
type Manager struct {
	store *db.Store
}

// NewManager creates a session manager backed by the given store.
func NewManager(store *db.Store) *Manager {
	return &Manager{store: store}
}

// Store returns the underlying store for use by API handlers.
func (m *Manager) Store() *db.Store {
	return m.store
}

// StartSession creates a new active session, upserting agent and project first.
func (m *Manager) StartSession(ctx context.Context, agentID, projectID, agentName, projectName string) (*models.Session, error) {
	if err := m.store.UpsertAgent(ctx, &models.Agent{AgentID: agentID, Name: agentName}); err != nil {
		return nil, fmt.Errorf("upsert agent: %w", err)
	}
	if err := m.store.UpsertProject(ctx, &models.Project{ProjectID: projectID, Name: projectName}); err != nil {
		return nil, fmt.Errorf("upsert project: %w", err)
	}

	sess := &models.Session{
		SessionID: uuid.New().String(),
		AgentID:   agentID,
		ProjectID: projectID,
		State:     models.SessionActive,
		StartedAt: time.Now().UTC(),
	}
	if err := m.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// ResumeSession finds the most recent interrupted session for the agent/project
// and transitions it back to active. Returns ErrNoInterrupted if none found.
func (m *Manager) ResumeSession(ctx context.Context, agentID, projectID string) (*models.Session, error) {
	sess, err := m.store.GetInterruptedSession(ctx, agentID, projectID)
	if err != nil {
		return nil, fmt.Errorf("get interrupted session: %w", err)
	}
	if sess == nil {
		return nil, ErrNoInterrupted
	}

	if !IsValidTransition(sess.State, models.SessionActive) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sess.State, models.SessionActive)
	}

	sess.State = models.SessionActive
	if err := m.store.UpdateSessionState(ctx, sess.SessionID, models.SessionActive, "", nil); err != nil {
		return nil, fmt.Errorf("resume transition: %w", err)
	}

	return sess, nil
}

// InterruptSession transitions an active session to interrupted.
// If ShouldCheckpoint returns true, writes a checkpoint first.
func (m *Manager) InterruptSession(ctx context.Context, sess *models.Session, snapshot *models.Snapshot) error {
	if !IsValidTransition(sess.State, models.SessionInterrupted) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sess.State, models.SessionInterrupted)
	}

	if snapshot != nil && ShouldCheckpoint(sess.State, models.SessionInterrupted) {
		cp := &models.Checkpoint{
			CheckpointID: uuid.New().String(),
			SessionID:    sess.SessionID,
			Snapshot:    *snapshot,
		}
		if err := m.store.CreateCheckpoint(ctx, cp); err != nil {
			return fmt.Errorf("create checkpoint: %w", err)
		}
	}

	sess.State = models.SessionInterrupted
	if err := m.store.UpdateSessionState(ctx, sess.SessionID, models.SessionInterrupted, "", nil); err != nil {
		return fmt.Errorf("interrupt transition: %w", err)
	}
	return nil
}

// CompleteSession transitions a session to completed.
func (m *Manager) CompleteSession(ctx context.Context, sess *models.Session, summary string) error {
	if !IsValidTransition(sess.State, models.SessionCompleted) {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sess.State, models.SessionCompleted)
	}

	now := time.Now().UTC()
	sess.State = models.SessionCompleted
	sess.Summary = summary
	return m.store.UpdateSessionState(ctx, sess.SessionID, models.SessionCompleted, summary, &now)
}

// Heartbeat updates the session's last_heartbeat_at timestamp.
func (m *Manager) Heartbeat(ctx context.Context, sessionID string, tokenBudget int) error {
	return m.store.TouchSessionHeartbeat(ctx, sessionID, tokenBudget)
}

// InterruptAllActive transitions every active session to interrupted.
// Used during graceful shutdown so sessions are resumable on next startup.
func (m *Manager) InterruptAllActive(ctx context.Context) error {
	return m.store.InterruptAllActive(ctx)
}

// GetSession returns a session by ID.
func (m *Manager) GetSession(ctx context.Context, sessionID string) (*models.Session, error) {
	return m.store.GetSession(ctx, sessionID)
}

// StartSessionWithKey creates a new session tied to a known sessionKey.
// If a session with that sessionKey already exists, returns it directly.
func (m *Manager) StartSessionWithKey(ctx context.Context, sessionKey, agentID, projectID, agentName, projectName string) (*models.Session, error) {
	// Upsert agent and project.
	if err := m.store.UpsertAgent(ctx, &models.Agent{AgentID: agentID, Name: agentName}); err != nil {
		return nil, fmt.Errorf("upsert agent: %w", err)
	}
	if err := m.store.UpsertProject(ctx, &models.Project{ProjectID: projectID, Name: projectName}); err != nil {
		return nil, fmt.Errorf("upsert project: %w", err)
	}

	// Check if a session with this sessionKey already exists.
	existing, err := m.store.GetSessionByKey(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("check existing session: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Create new session.
	sess := &models.Session{
		SessionID:  uuid.New().String(),
		SessionKey: sessionKey,
		AgentID:   agentID,
		ProjectID: projectID,
		State:     models.SessionActive,
		StartedAt: time.Now().UTC(),
	}
	if err := m.store.CreateSession(ctx, sess); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// ResumeSessionByKey finds the most recent interrupted session for a sessionKey
// and transitions it back to active. Returns ErrNoInterrupted if none found.
func (m *Manager) ResumeSessionByKey(ctx context.Context, sessionKey string) (*models.Session, error) {
	sess, err := m.store.GetInterruptedSessionByKey(ctx, sessionKey)
	if err != nil {
		return nil, fmt.Errorf("get interrupted session: %w", err)
	}
	if sess == nil {
		return nil, ErrNoInterrupted
	}

	if !IsValidTransition(sess.State, models.SessionActive) {
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sess.State, models.SessionActive)
	}

	sess.State = models.SessionActive
	if err := m.store.UpdateSessionState(ctx, sess.SessionID, models.SessionActive, "", nil); err != nil {
		return nil, fmt.Errorf("resume transition: %w", err)
	}

	return sess, nil
}

// ResumeSessionByID transitions a specific session (by ID) to active.
func (m *Manager) ResumeSessionByID(ctx context.Context, sessionID string) error {
	return m.store.UpdateSessionState(ctx, sessionID, models.SessionActive, "", nil)
}

// UpdateTokenBudget persists the latest token count for a session.
func (m *Manager) UpdateTokenBudget(ctx context.Context, sessionID string, tokenBudget int) error {
	return m.store.UpdateTokenBudget(ctx, sessionID, tokenBudget)
}
