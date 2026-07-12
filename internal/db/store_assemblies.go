package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/openlethe/lethe/internal/models"
)

// Sentinel errors for assembly operations.
var (
	ErrAssemblyNotFound       = errors.New("assembly not found")
	ErrAssemblyConflict       = errors.New("assembly id conflict")
	ErrAssemblyEventMismatch  = errors.New("assembly event does not belong to session")
	ErrSessionProjectMismatch = errors.New("session project mismatch")
)

// CreateContextAssembly creates an assembly and its items atomically.
func (s *Store) CreateContextAssembly(ctx context.Context, assembly *models.ContextAssembly) error {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Validate session exists and belongs to project.
	var sessProjectID string
	var sessSessionID string
	err = tx.QueryRowContext(ctx,
		`SELECT session_id, project_id FROM sessions WHERE session_id=?`,
		assembly.SessionID,
	).Scan(&sessSessionID, &sessProjectID)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}
	if sessProjectID != assembly.ProjectID {
		return ErrSessionProjectMismatch
	}

	// Insert assembly.
	q := `INSERT INTO context_assemblies
	      (assembly_id, session_id, project_id, source, plugin_version, assembler_version,
	       message_count, provided_token_budget, estimator_id,
	       summary_estimated_tokens, recent_estimated_tokens, conversation_estimated_tokens,
	       total_estimated_tokens, packed_bytes, recent_skipped, skip_reason, notes, created_at)
	      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx, q,
		assembly.AssemblyID, assembly.SessionID, assembly.ProjectID,
		assembly.Source, nullString(assembly.PluginVersion), assembly.AssemblerVersion,
		assembly.MessageCount, intOrNull(assembly.ProvidedTokenBudget), nullString(assembly.EstimatorID),
		intOrNull(assembly.SummaryEstimatedTokens), intOrNull(assembly.RecentEstimatedTokens),
		intOrNull(assembly.ConversationEstimatedTokens), intOrNull(assembly.TotalEstimatedTokens),
		assembly.PackedBytes, boolInt(assembly.RecentSkipped), nullString(assembly.SkipReason),
		nullString(assembly.Notes), now,
	)
	if err != nil {
		return fmt.Errorf("insert assembly: %w", err)
	}

	// Insert items.
	itemQ := `INSERT INTO context_assembly_items
	           (assembly_id, ordinal, item_kind, bucket, event_id, content_snapshot,
	            content_sha256, packed_bytes, estimated_tokens)
	           VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, item := range assembly.Items {
		_, err = tx.ExecContext(ctx, itemQ,
			assembly.AssemblyID, item.Ordinal, item.ItemKind, item.Bucket,
			nullString(item.EventID), nullString(item.ContentSnapshot), item.ContentSHA256,
			item.PackedBytes, intOrNull(item.EstimatedTokens),
		)
		if err != nil {
			return fmt.Errorf("insert item %d: %w", item.Ordinal, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	assembly.CreatedAt = now
	return nil
}

// ListContextAssemblies returns assemblies for a session, newest first.
func (s *Store) ListContextAssemblies(ctx context.Context, sessionID string, limit int) ([]*models.ContextAssembly, error) {
	q := `SELECT assembly_id, session_id, project_id, source, plugin_version, assembler_version,
	             message_count, provided_token_budget, estimator_id,
	             summary_estimated_tokens, recent_estimated_tokens, conversation_estimated_tokens,
	             total_estimated_tokens, packed_bytes, recent_skipped, skip_reason, notes, created_at
	      FROM context_assemblies
	      WHERE session_id=?
	      ORDER BY created_at DESC LIMIT ?`
	rows, err := s.QueryContext(ctx, q, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assemblies []*models.ContextAssembly
	for rows.Next() {
		var a models.ContextAssembly
		var pluginVer, estimator, skipReason, notes sql.NullString
		var provBudget, summaryTok, recentTok, convTok, totalTok sql.NullInt64
		var recentSkipped int
		err := rows.Scan(
			&a.AssemblyID, &a.SessionID, &a.ProjectID, &a.Source, &pluginVer, &a.AssemblerVersion,
			&a.MessageCount, &provBudget, &estimator,
			&summaryTok, &recentTok, &convTok, &totalTok,
			&a.PackedBytes, &recentSkipped, &skipReason, &notes, &a.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if pluginVer.Valid {
			a.PluginVersion = pluginVer.String
		}
		if estimator.Valid {
			a.EstimatorID = estimator.String
		}
		if skipReason.Valid {
			a.SkipReason = skipReason.String
		}
		if notes.Valid {
			a.Notes = notes.String
		}
		a.RecentSkipped = recentSkipped != 0
		if provBudget.Valid {
			v := int(provBudget.Int64)
			a.ProvidedTokenBudget = &v
		}
		if summaryTok.Valid {
			v := int(summaryTok.Int64)
			a.SummaryEstimatedTokens = &v
		}
		if recentTok.Valid {
			v := int(recentTok.Int64)
			a.RecentEstimatedTokens = &v
		}
		if convTok.Valid {
			v := int(convTok.Int64)
			a.ConversationEstimatedTokens = &v
		}
		if totalTok.Valid {
			v := int(totalTok.Int64)
			a.TotalEstimatedTokens = &v
		}
		assemblies = append(assemblies, &a)
	}
	return assemblies, rows.Err()
}

// GetContextAssembly returns a single assembly with its items.
func (s *Store) GetContextAssembly(ctx context.Context, assemblyID string) (*models.ContextAssembly, error) {
	q := `SELECT assembly_id, session_id, project_id, source, plugin_version, assembler_version,
	             message_count, provided_token_budget, estimator_id,
	             summary_estimated_tokens, recent_estimated_tokens, conversation_estimated_tokens,
	             total_estimated_tokens, packed_bytes, recent_skipped, skip_reason, notes, created_at
	      FROM context_assemblies
	      WHERE assembly_id=?`
	var a models.ContextAssembly
	var pluginVer, estimator, skipReason, notes sql.NullString
	var provBudget, summaryTok, recentTok, convTok, totalTok sql.NullInt64
	var recentSkipped int
	err := s.QueryRowContext(ctx, q, assemblyID).Scan(
		&a.AssemblyID, &a.SessionID, &a.ProjectID, &a.Source, &pluginVer, &a.AssemblerVersion,
		&a.MessageCount, &provBudget, &estimator,
		&summaryTok, &recentTok, &convTok, &totalTok,
		&a.PackedBytes, &recentSkipped, &skipReason, &notes, &a.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAssemblyNotFound
		}
		return nil, err
	}
	if pluginVer.Valid {
		a.PluginVersion = pluginVer.String
	}
	if estimator.Valid {
		a.EstimatorID = estimator.String
	}
	if skipReason.Valid {
		a.SkipReason = skipReason.String
	}
	if notes.Valid {
		a.Notes = notes.String
	}
	a.RecentSkipped = recentSkipped != 0
	if provBudget.Valid {
		v := int(provBudget.Int64)
		a.ProvidedTokenBudget = &v
	}
	if summaryTok.Valid {
		v := int(summaryTok.Int64)
		a.SummaryEstimatedTokens = &v
	}
	if recentTok.Valid {
		v := int(recentTok.Int64)
		a.RecentEstimatedTokens = &v
	}
	if convTok.Valid {
		v := int(convTok.Int64)
		a.ConversationEstimatedTokens = &v
	}
	if totalTok.Valid {
		v := int(totalTok.Int64)
		a.TotalEstimatedTokens = &v
	}

	// Load items.
	items, err := s.listAssemblyItems(ctx, assemblyID)
	if err != nil {
		return nil, err
	}
	a.Items = items

	return &a, nil
}

func (s *Store) listAssemblyItems(ctx context.Context, assemblyID string) ([]models.ContextAssemblyItem, error) {
	q := `SELECT ordinal, item_kind, bucket, event_id, content_snapshot, content_sha256,
	             packed_bytes, estimated_tokens
	      FROM context_assembly_items
	      WHERE assembly_id=?
	      ORDER BY ordinal ASC`
	rows, err := s.QueryContext(ctx, q, assemblyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.ContextAssemblyItem
	for rows.Next() {
		var item models.ContextAssemblyItem
		var eventID, contentSnapshot, contentSHA256 sql.NullString
		var estimatedToks sql.NullInt64
		err := rows.Scan(
			&item.Ordinal, &item.ItemKind, &item.Bucket, &eventID, &contentSnapshot,
			&contentSHA256, &item.PackedBytes, &estimatedToks,
		)
		if err != nil {
			return nil, err
		}
		if eventID.Valid {
			item.EventID = eventID.String
		}
		if contentSnapshot.Valid {
			item.ContentSnapshot = contentSnapshot.String
		}
		item.ContentSHA256 = contentSHA256.String
		if estimatedToks.Valid {
			v := int(estimatedToks.Int64)
			item.EstimatedTokens = &v
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateContextAssemblyFeedback records feedback on an assembly.
func (s *Store) CreateContextAssemblyFeedback(ctx context.Context, feedback *models.ContextAssemblyFeedback) error {
	q := `INSERT INTO context_assembly_feedback
	      (feedback_id, assembly_id, verdict, related_event_id, note, created_at)
	      VALUES (?, ?, ?, ?, ?, ?)`
	now := time.Now().UTC()
	_, err := s.ExecContext(ctx, q,
		feedback.FeedbackID, feedback.AssemblyID, feedback.Verdict,
		nullString(feedback.RelatedEventID), nullString(feedback.Note), now,
	)
	if err != nil {
		return err
	}
	feedback.CreatedAt = now
	return nil
}

// ListContextAssemblyFeedback returns feedback for an assembly.
func (s *Store) ListContextAssemblyFeedback(ctx context.Context, assemblyID string) ([]*models.ContextAssemblyFeedback, error) {
	q := `SELECT feedback_id, assembly_id, verdict, related_event_id, note, created_at
	      FROM context_assembly_feedback
	      WHERE assembly_id=?
	      ORDER BY created_at DESC`
	rows, err := s.QueryContext(ctx, q, assemblyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feedbacks []*models.ContextAssemblyFeedback
	for rows.Next() {
		var f models.ContextAssemblyFeedback
		var relatedEventID, note sql.NullString
		err := rows.Scan(&f.FeedbackID, &f.AssemblyID, &f.Verdict, &relatedEventID, &note, &f.CreatedAt)
		if err != nil {
			return nil, err
		}
		if relatedEventID.Valid {
			f.RelatedEventID = relatedEventID.String
		}
		if note.Valid {
			f.Note = note.String
		}
		feedbacks = append(feedbacks, &f)
	}
	return feedbacks, rows.Err()
}

// PruneContextAssemblies deletes old assemblies and their items/feedback.
func (s *Store) PruneContextAssemblies(ctx context.Context, olderThan time.Time, maxPerSession int, deleteLimit int) (int64, error) {
	// Delete assemblies older than the cutoff, keeping maxPerSession newest per session.
	// This is a two-step process: first identify sessions with excess assemblies,
	// then delete the oldest excess ones.

	// Simple approach: delete assemblies older than cutoff, limited to deleteLimit.
	q := `DELETE FROM context_assemblies
	      WHERE assembly_id IN (
	        SELECT assembly_id FROM context_assemblies
	        WHERE created_at < ?
	        ORDER BY created_at ASC
	        LIMIT ?
	      )`
	result, err := s.ExecContext(ctx, q, olderThan, deleteLimit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ComputeAssemblyHash computes a SHA256 hash of the canonical assembly content.
func ComputeAssemblyHash(assembly *models.ContextAssembly) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", assembly.AssemblyID)
	fmt.Fprintf(h, "%s\n", assembly.SessionID)
	fmt.Fprintf(h, "%s\n", assembly.ProjectID)
	fmt.Fprintf(h, "%s\n", assembly.Source)
	fmt.Fprintf(h, "%s\n", assembly.AssemblerVersion)
	fmt.Fprintf(h, "%d\n", assembly.MessageCount)
	fmt.Fprintf(h, "%d\n", assembly.PackedBytes)
	fmt.Fprintf(h, "%v\n", assembly.RecentSkipped)
	for _, item := range assembly.Items {
		fmt.Fprintf(h, "%d:%s:%s:%s\n", item.Ordinal, item.ItemKind, item.Bucket, item.EventID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Helper to convert *int to sql.NullInt64.
func intOrNull(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
