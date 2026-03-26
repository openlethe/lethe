package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mentholmike/lethe/internal/models"
)

// Store wraps a DB and provides all Lethe read/write operations.
type Store struct {
	*DB
}

// NewStore opens a database and returns a Store.
func NewStore(dbPath string) (*Store, error) {
	db, err := Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &Store{db}, nil
}

// --- Agents ---

// UpsertAgent creates or updates an agent.
func (s *Store) UpsertAgent(ctx context.Context, a *models.Agent) error {
	q := `INSERT INTO agents (agent_id, name, created_at, last_seen_at)
	      VALUES (?, ?, ?, ?)
	      ON CONFLICT(agent_id) DO UPDATE SET name=excluded.name, last_seen_at=excluded.last_seen_at`
	now := time.Now().UTC()
	_, err := s.ExecContext(ctx, q, a.AgentID, a.Name, now, now)
	return err
}

// TouchAgent updates last_seen_at for an agent.
func (s *Store) TouchAgent(ctx context.Context, agentID string) error {
	q := `UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`
	_, err := s.ExecContext(ctx, q, time.Now().UTC(), agentID)
	return err
}

// --- Projects ---

// UpsertProject creates or updates a project.
func (s *Store) UpsertProject(ctx context.Context, p *models.Project) error {
	q := `INSERT INTO projects (project_id, name, created_at, updated_at)
	      VALUES (?, ?, ?, ?)
	      ON CONFLICT(project_id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`
	now := time.Now().UTC()
	_, err := s.ExecContext(ctx, q, p.ProjectID, p.Name, now, now)
	return err
}

// --- Sessions ---

// CreateSession inserts a new session.
func (s *Store) CreateSession(ctx context.Context, sess *models.Session) error {
	q := `INSERT INTO sessions (session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?)`
	now := time.Now().UTC()
	nullTime := sql.NullTime{Time: now, Valid: true}
	_, err := s.ExecContext(ctx, q, sess.SessionID, nullString(sess.SessionKey), sess.AgentID, sess.ProjectID,
		sess.State, now, nullTime)
	return err
}

// UpdateSessionState updates a session's state and optionally its summary/ended_at.
func (s *Store) UpdateSessionState(ctx context.Context, sessionID string, state models.SessionState, summary string, endedAt *time.Time) error {
	q := `UPDATE sessions SET state=?, summary=? WHERE session_id=?`
	_, err := s.ExecContext(ctx, q, state, summary, sessionID)
	if err != nil {
		return err
	}
	if endedAt != nil {
		q2 := `UPDATE sessions SET ended_at=? WHERE session_id=?`
		_, err = s.ExecContext(ctx, q2, *endedAt, sessionID)
	}
	return err
}

// TouchSessionHeartbeat updates last_heartbeat_at and optionally token_budget.
func (s *Store) TouchSessionHeartbeat(ctx context.Context, sessionID string, tokenBudget int) error {
	q := `UPDATE sessions SET last_heartbeat_at=? WHERE session_id=?`
	args := []interface{}{time.Now().UTC(), sessionID}
	if tokenBudget > 0 {
		q = `UPDATE sessions SET last_heartbeat_at=?, token_budget=? WHERE session_id=?`
		args = []interface{}{time.Now().UTC(), tokenBudget, sessionID}
	}
	_, err := s.ExecContext(ctx, q, args...)
	return err
}

// UpdateTokenBudget persists the latest token_budget for a session.
func (s *Store) UpdateTokenBudget(ctx context.Context, sessionID string, tokenBudget int) error {
	q := `UPDATE sessions SET token_budget=? WHERE session_id=?`
	_, err := s.ExecContext(ctx, q, tokenBudget, sessionID)
	return err
}

// InterruptAllActive transitions all active sessions to interrupted.
// Used during graceful shutdown so sessions are resumable on next startup.
func (s *Store) InterruptAllActive(ctx context.Context) error {
	q := `UPDATE sessions SET state=? WHERE state=?`
	_, err := s.ExecContext(ctx, q, models.SessionInterrupted, models.SessionActive)
	return err
}

// GetSession returns a session by ID.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*models.Session, error) {
	q := `SELECT session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary, token_budget
	      FROM sessions WHERE session_id=?`
	var sess models.Session
	var sessionKey sql.NullString
	var lastHb, ended sql.NullTime
	var summary sql.NullString
	var tokenBudget sql.NullInt64
	err := s.QueryRowContext(ctx, q, sessionID).Scan(
		&sess.SessionID, &sessionKey, &sess.AgentID, &sess.ProjectID, &sess.State,
		&sess.StartedAt, &lastHb, &ended, &summary, &tokenBudget,
	)
	if tokenBudget.Valid {
		sess.TokenBudget = int(tokenBudget.Int64)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.LastHeartbeatAt = lastHb
	sess.EndedAt = ended
	if sessionKey.Valid {
		sess.SessionKey = sessionKey.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}
	return &sess, nil
}

// GetInterruptedSession returns the most recent interrupted session for an agent+project.
func (s *Store) GetInterruptedSession(ctx context.Context, agentID, projectID string) (*models.Session, error) {
	q := `SELECT session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary, token_budget
	      FROM sessions
	      WHERE agent_id=? AND project_id=? AND state='interrupted'
	      ORDER BY last_heartbeat_at DESC LIMIT 1`
	var sess models.Session
	var sessionKey sql.NullString
	var lastHb, ended sql.NullTime
	var summary sql.NullString
	var tokenBudget sql.NullInt64
	err := s.QueryRowContext(ctx, q, agentID, projectID).Scan(
		&sess.SessionID, &sessionKey, &sess.AgentID, &sess.ProjectID, &sess.State,
		&sess.StartedAt, &lastHb, &ended, &summary, &tokenBudget,
	)
	if tokenBudget.Valid {
		sess.TokenBudget = int(tokenBudget.Int64)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.LastHeartbeatAt = lastHb
	sess.EndedAt = ended
	if sessionKey.Valid {
		sess.SessionKey = sessionKey.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}
	return &sess, nil
}

// GetSessionByKey returns a session by its session_key.
func (s *Store) GetSessionByKey(ctx context.Context, sessionKey string) (*models.Session, error) {
	q := `SELECT session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary, token_budget
	      FROM sessions WHERE session_key=?`
	var sess models.Session
	var sessionKeyVal sql.NullString
	var lastHb, ended sql.NullTime
	var summary sql.NullString
	var tokenBudget sql.NullInt64
	err := s.QueryRowContext(ctx, q, sessionKey).Scan(
		&sess.SessionID, &sessionKeyVal, &sess.AgentID, &sess.ProjectID, &sess.State,
		&sess.StartedAt, &lastHb, &ended, &summary, &tokenBudget,
	)
	if tokenBudget.Valid {
		sess.TokenBudget = int(tokenBudget.Int64)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.LastHeartbeatAt = lastHb
	sess.EndedAt = ended
	if sessionKeyVal.Valid {
		sess.SessionKey = sessionKeyVal.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}
	return &sess, nil
}

// GetInterruptedSessionByKey returns the most recent interrupted session for a sessionKey.
func (s *Store) GetInterruptedSessionByKey(ctx context.Context, sessionKey string) (*models.Session, error) {
	q := `SELECT session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary, token_budget
	      FROM sessions
	      WHERE session_key=? AND state='interrupted'
	      ORDER BY last_heartbeat_at DESC LIMIT 1`
	var sess models.Session
	var sessionKeyVal sql.NullString
	var lastHb, ended sql.NullTime
	var summary sql.NullString
	var tokenBudget sql.NullInt64
	err := s.QueryRowContext(ctx, q, sessionKey).Scan(
		&sess.SessionID, &sessionKeyVal, &sess.AgentID, &sess.ProjectID, &sess.State,
		&sess.StartedAt, &lastHb, &ended, &summary, &tokenBudget,
	)
	if tokenBudget.Valid {
		sess.TokenBudget = int(tokenBudget.Int64)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.LastHeartbeatAt = lastHb
	sess.EndedAt = ended
	if sessionKeyVal.Valid {
		sess.SessionKey = sessionKeyVal.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}
	return &sess, nil
}

// --- Checkpoints ---

// CreateCheckpoint inserts a checkpoint with the next seq number.
func (s *Store) CreateCheckpoint(ctx context.Context, c *models.Checkpoint) error {
	var maxSeq sql.NullInt64
	err := s.QueryRowContext(ctx,
		`SELECT MAX(seq) FROM checkpoints WHERE session_id=?`, c.SessionID,
	).Scan(&maxSeq)
	if err != nil {
		return fmt.Errorf("max seq: %w", err)
	}
	c.Seq = 1
	if maxSeq.Valid {
		c.Seq = int(maxSeq.Int64) + 1
	}

	q := `INSERT INTO checkpoints (checkpoint_id, session_id, seq, snapshot, created_at)
	      VALUES (?, ?, ?, ?, ?)`
	now := time.Now().UTC()
	_, err = s.ExecContext(ctx, q, c.CheckpointID, c.SessionID, c.Seq, models.MarshalSnapshot(c.Snapshot), now)
	if err != nil {
		return err
	}
	c.CreatedAt = now
	return nil
}

// GetLatestCheckpoint returns the most recent checkpoint for a session.
func (s *Store) GetLatestCheckpoint(ctx context.Context, sessionID string) (*models.Checkpoint, error) {
	q := `SELECT checkpoint_id, session_id, seq, snapshot, created_at
	      FROM checkpoints WHERE session_id=? ORDER BY seq DESC LIMIT 1`
	var c models.Checkpoint
	var snap string
	err := s.QueryRowContext(ctx, q, sessionID).Scan(&c.CheckpointID, &c.SessionID, &c.Seq, &snap, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Snapshot, _ = models.UnmarshalSnapshot(snap)
	return &c, nil
}

// GetCheckpoints returns all checkpoints for a session, ordered by seq DESC.
func (s *Store) GetCheckpoints(ctx context.Context, sessionID string) ([]*models.Checkpoint, error) {
	q := `SELECT checkpoint_id, session_id, seq, snapshot, created_at
	      FROM checkpoints WHERE session_id=? ORDER BY seq DESC`
	rows, err := s.QueryContext(ctx, q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cps []*models.Checkpoint
	for rows.Next() {
		var c models.Checkpoint
		var snap string
		if err := rows.Scan(&c.CheckpointID, &c.SessionID, &c.Seq, &snap, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Snapshot, _ = models.UnmarshalSnapshot(snap)
		cps = append(cps, &c)
	}
	return cps, rows.Err()
}

// --- Events ---

// CreateEvent inserts an event. event_id must be provided.
func (s *Store) CreateEvent(ctx context.Context, e *models.Event) error {
	q := `INSERT INTO events
	      (event_id, session_id, parent_event_id, event_type, content,
	       confidence, tags, embedding_id, task_title, task_status, status_changed_at,
	       human_reviewed_at, reviewer_id, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	now := time.Now().UTC()
	_, err := s.ExecContext(ctx, q,
		e.EventID, e.SessionID, nullString(e.ParentEventID), e.EventType, e.Content,
		e.Confidence, nullString(e.Tags), nullString(e.EmbeddingID),
		nullString(e.TaskTitle), e.TaskStatus,
		e.StatusChangedAt, e.HumanReviewedAt, nullString(e.ReviewerID),
		now,
	)
	if err != nil {
		return err
	}
	e.CreatedAt = now
	return nil
}

// GetSessionEvents returns paginated events for a session.
func (s *Store) GetSessionEvents(ctx context.Context, sessionID string, limit, offset int) ([]*models.Event, error) {
	q := `SELECT event_id, session_id, parent_event_id, event_type, content,
	             confidence, tags, embedding_id, task_title, task_status,
	             status_changed_at, human_reviewed_at, reviewer_id, created_at
	      FROM events WHERE session_id=? ORDER BY created_at ASC LIMIT ? OFFSET ?`
	rows, err := s.QueryContext(ctx, q, sessionID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// GetRecentSessionEvents returns the N most recent events for a session, newest-first.
// Use this for compact summaries and assemble() context injection — it avoids returning
// stale events from early in the session when the caller only wants recent context.
func (s *Store) GetRecentSessionEvents(ctx context.Context, sessionID string, limit int) ([]*models.Event, error) {
	q := `SELECT event_id, session_id, parent_event_id, event_type, content,
	             confidence, tags, embedding_id, task_title, task_status,
	             status_changed_at, human_reviewed_at, reviewer_id, created_at
	      FROM events WHERE session_id=? ORDER BY created_at DESC LIMIT ?`
	rows, err := s.QueryContext(ctx, q, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// SearchEvents searches all events by content (case-insensitive LIKE) and optionally by tag.
// Results ordered by created_at DESC (newest first).
func (s *Store) SearchEvents(ctx context.Context, query string, tag string, limit int) ([]*models.Event, error) {
	q := `SELECT event_id, session_id, parent_event_id, event_type, content,
	             confidence, tags, embedding_id, task_title, task_status,
	             status_changed_at, human_reviewed_at, reviewer_id, created_at
	      FROM events
	      WHERE content LIKE ?`
	args := []interface{}{"%" + query + "%"}

	if tag != "" {
		q += ` AND tags LIKE ?`
		args = append(args, "%"+tag+"%")
	}

	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// GetTaskChain walks the parent chain from an event_id back to the root.
// Returns events newest-first (the queried event first, oldest task last).
// Uses iterative parent lookup for deterministic ordering.
func (s *Store) GetTaskChain(ctx context.Context, eventID string) ([]*models.Event, error) {
	// Build the chain by walking parent links iteratively.
	var chain []*models.Event
	currentID := eventID
	for {
		q := `SELECT event_id, session_id, parent_event_id, event_type, content,
	             confidence, tags, embedding_id, task_title, task_status,
	             status_changed_at, human_reviewed_at, reviewer_id, created_at
	      FROM events WHERE event_id = ?`
		rows, err := s.QueryContext(ctx, q, currentID)
		if err != nil {
			return nil, err
		}
		events, err := scanEvents(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(events) == 0 {
			break
		}
		chain = append(chain, events[0])
		currentID = events[0].ParentEventID
		if currentID == "" {
			break
		}
	}
	return chain, nil
}

// GetFlaggedEvents returns all un-reviewed flag events.
func (s *Store) GetFlaggedEvents(ctx context.Context, limit, offset int) ([]*models.Event, error) {
	q := `SELECT event_id, session_id, parent_event_id, event_type, content,
	             confidence, tags, embedding_id, task_title, task_status,
	             status_changed_at, human_reviewed_at, reviewer_id, created_at
	      FROM events WHERE event_type='flag' AND human_reviewed_at IS NULL
	      ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := s.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

// MarkFlagReviewed sets human_reviewed_at and reviewer_id on a flag event.
func (s *Store) MarkFlagReviewed(ctx context.Context, eventID, reviewerID string) error {
	q := `UPDATE events SET human_reviewed_at=?, reviewer_id=? WHERE event_id=? AND event_type='flag'`
	_, err := s.ExecContext(ctx, q, time.Now().UTC(), reviewerID, eventID)
	return err
}

// --- Session Links ---

// CreateSessionLink records a relationship between sessions.
func (s *Store) CreateSessionLink(ctx context.Context, link *models.SessionLink) error {
	q := `INSERT OR IGNORE INTO session_links (session_id, prior_session_id, link_type)
	      VALUES (?, ?, ?)`
	_, err := s.ExecContext(ctx, q, link.SessionID, link.PriorSessionID, link.LinkType)
	return err
}

// GetStats returns aggregate stats: session count, total events, total checkpoints, flag count.
func (s *Store) GetStats(ctx context.Context) (map[string]int, error) {
	stats := map[string]int{}
	var count int

	q := `SELECT COUNT(*) FROM sessions`
	if err := s.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return nil, err
	}
	stats["sessions"] = count

	q = `SELECT COUNT(*) FROM events`
	if err := s.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return nil, err
	}
	stats["events"] = count

	q = `SELECT COUNT(*) FROM checkpoints`
	if err := s.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return nil, err
	}
	stats["checkpoints"] = count

	q = `SELECT COUNT(*) FROM events WHERE event_type='flag' AND human_reviewed_at IS NULL`
	if err := s.QueryRowContext(ctx, q).Scan(&count); err != nil {
		return nil, err
	}
	stats["flags"] = count

	return stats, nil
}

// GetAllSessions returns all sessions ordered by last heartbeat descending.
func (s *Store) GetAllSessions(ctx context.Context, limit int) ([]*models.Session, error) {
	q := `SELECT session_id, session_key, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary, token_budget
	      FROM sessions ORDER BY last_heartbeat_at DESC LIMIT ?`
	rows, err := s.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []*models.Session
	for rows.Next() {
		var sess models.Session
		var sessionKey sql.NullString
		var lastHb, ended sql.NullTime
		var summary sql.NullString
		var tokenBudget sql.NullInt64
		if err := rows.Scan(
			&sess.SessionID, &sessionKey, &sess.AgentID, &sess.ProjectID, &sess.State,
			&sess.StartedAt, &lastHb, &ended, &summary, &tokenBudget,
		); err != nil {
			return nil, err
		}
		sess.LastHeartbeatAt = lastHb
		sess.EndedAt = ended
		if sessionKey.Valid {
			sess.SessionKey = sessionKey.String
		}
		if summary.Valid {
			sess.Summary = summary.String
		}
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

// GetSessionEventsCount returns the total number of events for a session.
func (s *Store) GetSessionEventsCount(ctx context.Context, sessionID string) (int, error) {
	var count int
	q := `SELECT COUNT(*) FROM events WHERE session_id=?`
	err := s.QueryRowContext(ctx, q, sessionID).Scan(&count)
	return count, err
}

// CountSessions returns the total number of sessions.
func (s *Store) CountSessions(ctx context.Context) (int, error) {
	var n int
	q := `SELECT COUNT(*) FROM sessions`
	err := s.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// CountEvents returns the total number of events.
func (s *Store) CountEvents(ctx context.Context) (int, error) {
	var n int
	q := `SELECT COUNT(*) FROM events`
	err := s.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// CountCheckpoints returns the total number of checkpoints.
func (s *Store) CountCheckpoints(ctx context.Context) (int, error) {
	var n int
	q := `SELECT COUNT(*) FROM checkpoints`
	err := s.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// CountFlags returns the number of unreviewed flag events.
func (s *Store) CountFlags(ctx context.Context) (int, error) {
	var n int
	q := `SELECT COUNT(*) FROM events WHERE event_type='flag' AND human_reviewed_at IS NULL`
	err := s.QueryRowContext(ctx, q).Scan(&n)
	return n, err
}

// CompactSession writes a summary string to the session.
func (s *Store) CompactSession(ctx context.Context, sessionID string, summary string) error {
	q := `UPDATE sessions SET summary=? WHERE session_id=?`
	_, err := s.ExecContext(ctx, q, nullString(summary), sessionID)
	return err
}

// --- Helpers ---

func nullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func scanEvents(rows *sql.Rows) ([]*models.Event, error) {
	var events []*models.Event
	for rows.Next() {
		var e models.Event
		var parent, tags, embID, taskTitle, reviewerID sql.NullString
		var conf sql.NullFloat64
		var statusChanged, reviewedAt sql.NullTime
		err := rows.Scan(
			&e.EventID, &e.SessionID, &parent, &e.EventType, &e.Content,
			&conf, &tags, &embID, &taskTitle, &e.TaskStatus,
			&statusChanged, &reviewedAt, &reviewerID, &e.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if parent.Valid {
			e.ParentEventID = parent.String
		}
		if conf.Valid {
			e.Confidence = &conf.Float64
		}
		if tags.Valid {
			e.Tags = tags.String
		}
		if embID.Valid {
			e.EmbeddingID = embID.String
		}
		if taskTitle.Valid {
			e.TaskTitle = taskTitle.String
		}
		if reviewerID.Valid {
			e.ReviewerID = reviewerID.String
		}
		e.StatusChangedAt = statusChanged
		e.HumanReviewedAt = reviewedAt
		events = append(events, &e)
	}
	return events, rows.Err()
}
