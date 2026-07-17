package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/openlethe/lethe/internal/metrics"
	"github.com/openlethe/lethe/internal/models"
)

// Protected-merge authorization errors.
var (
	// ErrAuthorizationReplay means the nonce was already consumed. A captured
	// authorization can never be used twice, even if the protected ref later
	// returns to the same expected head.
	ErrAuthorizationReplay = errors.New("merge authorization nonce already consumed")
	// ErrMergeShape means the new head does not satisfy the authorized merge
	// strategy (fast-forward descendant, two-parent merge, or cherry-pick).
	ErrMergeShape = errors.New("new head does not satisfy the authorized merge strategy")
	// ErrInvalidStrategy means the requested merge strategy is unknown.
	ErrInvalidStrategy = errors.New("invalid merge strategy")
)

// Merge strategies Lethe independently enforces for protected-ref movement.
const (
	StrategyFastForward = "fast_forward"
	StrategyMergeCommit = "merge_commit"
	StrategyCherryPick  = "cherry_pick"
)

// ValidMergeStrategy reports whether strategy is enforceable.
func ValidMergeStrategy(strategy string) bool {
	switch strategy {
	case StrategyFastForward, StrategyMergeCommit, StrategyCherryPick:
		return true
	default:
		return false
	}
}

// MergeAdvancement is the validated, durable content of one authorized
// protected-ref movement.
type MergeAdvancement struct {
	ProjectID         string
	RefName           string
	ExpectedHead      string
	NewHead           string
	ProposalID        string
	ProposalDigest    string
	ReviewerPrincipal string
	MergerPrincipal   string
	Strategy          string
	Nonce             string
	KeyID             string
	ExpiresAt         time.Time
}

// CASMergeProtectedRefAuthorized executes one signed merge attempt:
//  1. the authorization nonce is consumed DURABLY in its own transaction —
//     every signed-valid attempt burns its nonce, including attempts that
//     later fail shape checks or lose the CAS. A nonce can therefore never be
//     replayed even if the ref cycles back to the signed expected head;
//  2. the merge shape is enforced and the protected ref CAS + durable
//     advancement record commit atomically in a second transaction.
//
// Exactly one caller can consume a nonce; the CAS transaction is idempotent
// under busy retries so contention never turns into a false replay error.
func (s *Store) CASMergeProtectedRefAuthorized(ctx context.Context, m MergeAdvancement) (*models.MemoryRef, error) {
	if m.ProjectID == "" || m.RefName == "" || m.ExpectedHead == "" || m.NewHead == "" {
		return nil, errors.New("project, ref, expected head, and new head are required")
	}
	if !ValidMergeStrategy(m.Strategy) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidStrategy, m.Strategy)
	}
	if m.ProposalID == "" || m.ProposalDigest == "" || m.ReviewerPrincipal == "" || m.MergerPrincipal == "" {
		return nil, errors.New("proposal binding, reviewer, and merger principals are required")
	}
	if m.Nonce == "" {
		return nil, errors.New("authorization nonce required")
	}

	if err := withBusyRetry(ctx, func() error { return s.consumeMergeNonceOnce(ctx, m) }); err != nil {
		return nil, err
	}

	var ref *models.MemoryRef
	err := withBusyRetry(ctx, func() error {
		var err error
		ref, err = s.casProtectedRefWithAdvanceRecord(ctx, m)
		return err
	})
	return ref, err
}

// consumeMergeNonceOnce burns the authorization nonce in its own committed
// transaction, independent of the merge outcome.
func (s *Store) consumeMergeNonceOnce(ctx context.Context, m MergeAdvancement) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_merge_authorizations (
			nonce, key_id, project_id, ref_name, expected_head, new_head,
			merge_proposal_id, proposal_digest, reviewer_principal, merger_principal,
			strategy, expires_at, consumed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Nonce, m.KeyID, m.ProjectID, m.RefName, m.ExpectedHead, m.NewHead,
		m.ProposalID, m.ProposalDigest, m.ReviewerPrincipal, m.MergerPrincipal,
		m.Strategy, m.ExpiresAt, now); err != nil {
		if isUniqueViolation(err) {
			metrics.Inc(metrics.MergeReplayRejects)
			return fmt.Errorf("%w: %s", ErrAuthorizationReplay, m.Nonce)
		}
		return err
	}
	return tx.Commit()
}

// casProtectedRefWithAdvanceRecord enforces the merge shape, CAS-advances the
// protected ref, and writes the durable advancement record atomically.
func (s *Store) casProtectedRefWithAdvanceRecord(ctx context.Context, m MergeAdvancement) (*models.MemoryRef, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// The ref must exist and be protected.
	var currentHead string
	var protected int
	err = tx.QueryRowContext(ctx, `
		SELECT head_changeset_id, protected FROM memory_refs WHERE project_id = ? AND ref_name = ?
	`, m.ProjectID, m.RefName).Scan(&currentHead, &protected)
	if err == sql.ErrNoRows {
		return nil, ErrRefNotFound
	}
	if err != nil {
		return nil, err
	}
	if protected != 1 {
		return nil, errors.New("ref is not protected")
	}

	// Lethe independently enforces the authorized merge shape and the new
	// head's project.
	if err := enforceMergeShape(ctx, tx, m); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	res, err := tx.ExecContext(ctx, `
		UPDATE memory_refs
		SET head_changeset_id = ?, updated_at = ?
		WHERE project_id = ? AND ref_name = ? AND head_changeset_id = ?
	`, m.NewHead, now, m.ProjectID, m.RefName, m.ExpectedHead)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, fmt.Errorf("%w: expected %s, current %s", ErrRefCASConflict, m.ExpectedHead, currentHead)
	}

	// Durable advancement record for reconciliation.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memory_protected_ref_advances (
			advance_id, project_id, ref_name, expected_head, new_head,
			merge_proposal_id, proposal_digest, reviewer_principal, merger_principal,
			strategy, authorization_nonce, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, uuid.Must(uuid.NewV7()).String(), m.ProjectID, m.RefName, m.ExpectedHead, m.NewHead,
		m.ProposalID, m.ProposalDigest, m.ReviewerPrincipal, m.MergerPrincipal,
		m.Strategy, m.Nonce, now); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	metrics.Inc(metrics.MergeAuthorized)
	return s.GetMemoryRef(ctx, m.ProjectID, m.RefName)
}

// mergeAncestryWalkLimit bounds the fast-forward ancestry proof so a hostile
// or corrupt graph cannot cause unbounded traversal inside the transaction.
const mergeAncestryWalkLimit = 10000

// enforceMergeShape proves the new head satisfies the authorized strategy.
func enforceMergeShape(ctx context.Context, tx *sql.Tx, m MergeAdvancement) error {
	newHead, err := loadChangesetSummaryTx(ctx, tx, m.NewHead)
	if err != nil {
		return fmt.Errorf("load new head: %w", err)
	}
	if newHead.projectID != m.ProjectID {
		return fmt.Errorf("%w: new head belongs to project %s, not %s", ErrMergeShape, newHead.projectID, m.ProjectID)
	}

	switch m.Strategy {
	case StrategyCherryPick:
		// Approved cherry-pick result: a single-parent changeset on top of the
		// expected head.
		if len(newHead.parentIDs) == 1 && newHead.parentIDs[0] == m.ExpectedHead {
			return nil
		}
		return fmt.Errorf("%w: cherry-pick head must have exactly the expected head as its single parent", ErrMergeShape)
	case StrategyMergeCommit:
		// Approved two-parent merge changeset whose first parent is the
		// expected head.
		if len(newHead.parentIDs) == 2 && newHead.parentIDs[0] == m.ExpectedHead {
			return nil
		}
		return fmt.Errorf("%w: merge commit must have two parents with the expected head first", ErrMergeShape)
	case StrategyFastForward:
		// Approved descendant: the expected head must be an ancestor of the
		// new head (or the new head itself).
		if m.NewHead == m.ExpectedHead {
			return nil
		}
		seen := map[string]bool{}
		queue := append([]string(nil), newHead.parentIDs...)
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if id == m.ExpectedHead {
				return nil
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if len(seen) > mergeAncestryWalkLimit {
				return fmt.Errorf("%w: fast-forward ancestry proof exceeds safety limit", ErrMergeShape)
			}
			cs, err := loadChangesetSummaryTx(ctx, tx, id)
			if err != nil {
				return fmt.Errorf("fast-forward ancestry: %w", err)
			}
			if cs.projectID != m.ProjectID {
				return fmt.Errorf("%w: ancestry crossed project boundary", ErrMergeShape)
			}
			queue = append(queue, cs.parentIDs...)
		}
		return fmt.Errorf("%w: new head is not a descendant of the expected head", ErrMergeShape)
	default:
		return fmt.Errorf("%w: %s", ErrInvalidStrategy, m.Strategy)
	}
}

type changesetSummary struct {
	projectID string
	parentIDs []string
}

// loadChangesetSummaryTx loads just the fields merge-shape enforcement needs,
// inside the authorization transaction.
func loadChangesetSummaryTx(ctx context.Context, tx *sql.Tx, id string) (*changesetSummary, error) {
	var summary changesetSummary
	var parentsJSON string
	err := tx.QueryRowContext(ctx, `
		SELECT project_id, parent_ids_json FROM memory_changesets WHERE changeset_id = ?
	`, id).Scan(&summary.projectID, &parentsJSON)
	if err == sql.ErrNoRows {
		return nil, ErrChangesetNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(parentsJSON), &summary.parentIDs); err != nil {
		return nil, fmt.Errorf("changeset %s parent JSON does not decode: %w", id, err)
	}
	return &summary, nil
}

// ListProtectedRefAdvances returns durable protected-ref movement records for
// reconciliation and audit.
func (s *Store) ListProtectedRefAdvances(ctx context.Context, projectID, refName string, limit int) ([]*models.ProtectedRefAdvance, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.QueryContext(ctx, `
		SELECT advance_id, project_id, ref_name, expected_head, new_head,
			merge_proposal_id, proposal_digest, reviewer_principal, merger_principal,
			strategy, authorization_nonce, created_at
		FROM memory_protected_ref_advances
		WHERE project_id = ? AND ref_name = ?
		ORDER BY created_at DESC LIMIT ?
	`, projectID, refName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*models.ProtectedRefAdvance, 0)
	for rows.Next() {
		var a models.ProtectedRefAdvance
		var createdAt string
		if err := rows.Scan(
			&a.AdvanceID, &a.ProjectID, &a.RefName, &a.ExpectedHead, &a.NewHead,
			&a.ProposalID, &a.ProposalDigest, &a.ReviewerPrincipal, &a.MergerPrincipal,
			&a.Strategy, &a.AuthorizationNonce, &createdAt,
		); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(createdAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}
