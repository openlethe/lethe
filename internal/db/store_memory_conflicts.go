package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/openlethe/lethe/internal/models"
)

// Conflict lifecycle errors.
var (
	ErrConflictNotFound = errors.New("conflict not found")
	ErrInvalidConflict  = errors.New("invalid conflict")
)

// Retirable conflict outcomes. A retired conflict no longer blocks merges or
// appears in accepted-context projections.
var retiredConflictStatuses = map[string]bool{
	"rejected":   true,
	"canceled":   true,
	"superseded": true,
}

// PersistConflicts binds a detected conflict set to its proposal as part of an
// explicit proposal operation. Identity is deterministic: repeating the same
// proposal (retry after a crash or response loss) converges on the same rows,
// and a new proposal that re-detects a semantically identical conflict
// reopens the single canonical row under its own binding instead of creating
// an equivalent duplicate.
func (s *Store) PersistConflicts(ctx context.Context, projectID, proposalID string, conflicts []*models.MemoryConflict) error {
	if projectID == "" || proposalID == "" {
		return fmt.Errorf("%w: project_id and proposal_id required", ErrInvalidConflict)
	}
	return withBusyRetry(ctx, func() error {
		tx, err := s.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, c := range conflicts {
			if err := validateConflictForPersist(projectID, c); err != nil {
				return err
			}
			c.ProjectID = projectID
			c.ConflictID = DeterministicConflictID(c)
			if c.Severity == "" {
				c.Severity = "blocking"
			}
			if c.CreatedAt.IsZero() {
				c.CreatedAt = time.Now().UTC()
			}
			if c.Details == nil {
				c.Details = map[string]any{}
			}
			detailsJSON, _ := json.Marshal(c.Details)
			_, err := tx.ExecContext(ctx, `
				INSERT INTO memory_conflicts (
					conflict_id, project_id, base_changeset_id, left_changeset_id, right_changeset_id,
					conflict_type, severity, summary, details_json, status, proposal_id, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)
				ON CONFLICT(conflict_id) DO UPDATE SET
					status = 'open',
					proposal_id = excluded.proposal_id,
					severity = excluded.severity,
					summary = excluded.summary,
					details_json = excluded.details_json,
					resolved_at = NULL,
					resolution_note = NULL
			`, c.ConflictID, c.ProjectID, nullString(c.BaseChangesetID), c.LeftChangesetID, c.RightChangesetID,
				c.ConflictType, c.Severity, c.Summary, string(detailsJSON), proposalID, c.CreatedAt)
			if err != nil {
				return fmt.Errorf("persist conflict %s: %w", c.ConflictID, err)
			}
		}
		return tx.Commit()
	})
}

func validateConflictForPersist(projectID string, c *models.MemoryConflict) error {
	if c == nil {
		return fmt.Errorf("%w: nil conflict", ErrInvalidConflict)
	}
	if c.ProjectID != "" && c.ProjectID != projectID {
		return fmt.Errorf("%w: conflict project %s does not match %s", ErrInvalidConflict, c.ProjectID, projectID)
	}
	if c.LeftChangesetID == "" || c.RightChangesetID == "" {
		return fmt.Errorf("%w: left and right changeset IDs required", ErrInvalidConflict)
	}
	if c.ConflictType == "" {
		return fmt.Errorf("%w: conflict_type required", ErrInvalidConflict)
	}
	if c.Summary == "" {
		return fmt.Errorf("%w: summary required", ErrInvalidConflict)
	}
	return nil
}

// ResolveConflict marks an open conflict resolved with an operator note.
// Because conflict identity is deterministic, resolving the canonical row
// retires every equivalent duplicate blocker at once.
func (s *Store) ResolveConflict(ctx context.Context, projectID, conflictID, note string) error {
	if projectID == "" || conflictID == "" {
		return fmt.Errorf("%w: project_id and conflict_id required", ErrInvalidConflict)
	}
	now := time.Now().UTC()
	res, err := s.ExecContext(ctx, `
		UPDATE memory_conflicts
		SET status = 'resolved', resolved_at = ?, resolution_note = ?
		WHERE conflict_id = ? AND project_id = ? AND status = 'open'
	`, now, nullString(note), conflictID, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w or not open: %s", ErrConflictNotFound, conflictID)
	}
	return nil
}

// RetireConflictsForProposal retires every open conflict bound to a proposal
// that reached a terminal state (rejected, canceled) or whose merge landed
// (superseded). Returns the number of retired rows.
func (s *Store) RetireConflictsForProposal(ctx context.Context, projectID, proposalID, status string) (int, error) {
	if projectID == "" || proposalID == "" {
		return 0, fmt.Errorf("%w: project_id and proposal_id required", ErrInvalidConflict)
	}
	if !retiredConflictStatuses[status] {
		return 0, fmt.Errorf("%w: status must be rejected, canceled, or superseded", ErrInvalidConflict)
	}
	now := time.Now().UTC()
	res, err := s.ExecContext(ctx, `
		UPDATE memory_conflicts
		SET status = ?, resolved_at = ?
		WHERE proposal_id = ? AND project_id = ? AND status = 'open'
	`, status, now, proposalID, projectID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListConflicts returns conflicts for a project, newest first. An empty status
// lists every state.
func (s *Store) ListConflicts(ctx context.Context, projectID, status string, limit int) ([]*models.MemoryConflict, error) {
	if projectID == "" {
		return nil, errors.New("project_id required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `
		SELECT conflict_id, project_id, COALESCE(base_changeset_id, ''), left_changeset_id, right_changeset_id,
			conflict_type, severity, summary, details_json, status, created_at,
			resolved_at, COALESCE(resolution_note, ''), COALESCE(proposal_id, '')
		FROM memory_conflicts WHERE project_id = ?`
	args := []any{projectID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, conflict_id ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]*models.MemoryConflict, 0)
	for rows.Next() {
		c, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConflict loads one conflict by ID.
func (s *Store) GetConflict(ctx context.Context, projectID, conflictID string) (*models.MemoryConflict, error) {
	rows, err := s.QueryContext(ctx, `
		SELECT conflict_id, project_id, COALESCE(base_changeset_id, ''), left_changeset_id, right_changeset_id,
			conflict_type, severity, summary, details_json, status, created_at,
			resolved_at, COALESCE(resolution_note, ''), COALESCE(proposal_id, '')
		FROM memory_conflicts WHERE project_id = ? AND conflict_id = ?
	`, projectID, conflictID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		return scanConflict(rows)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, ErrConflictNotFound
}

type conflictRowScanner interface {
	Scan(dest ...any) error
}

// scanConflict decodes one conflict row. Persisted conflict state fails
// closed on undecodable details, like every other integrity-relevant record.
func scanConflict(row conflictRowScanner) (*models.MemoryConflict, error) {
	var c models.MemoryConflict
	var detailsJSON string
	var createdAt string
	var resolvedAt *string
	if err := row.Scan(
		&c.ConflictID, &c.ProjectID, &c.BaseChangesetID, &c.LeftChangesetID, &c.RightChangesetID,
		&c.ConflictType, &c.Severity, &c.Summary, &detailsJSON, &c.Status, &createdAt,
		&resolvedAt, &c.ResolutionNote, &c.ProposalID,
	); err != nil {
		return nil, err
	}
	c.CreatedAt = parseTime(createdAt)
	if resolvedAt != nil {
		t := parseTime(*resolvedAt)
		c.ResolvedAt = &t
	}
	if err := json.Unmarshal([]byte(detailsJSON), &c.Details); err != nil {
		return nil, fmt.Errorf("conflict %s details do not decode: %w", c.ConflictID, err)
	}
	if c.Details == nil {
		c.Details = map[string]any{}
	}
	return &c, nil
}
