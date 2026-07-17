package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/openlethe/lethe/internal/metrics"
	"github.com/openlethe/lethe/internal/models"
)

const memoryProjectionVersion = "memory-context/v1"

// EnsureLegacyBaseline freezes the IDs of events that existed when the
// synthetic legacy root was created. Direct event writes after that point do
// not silently become accepted Memory Git state.
func (s *Store) EnsureLegacyBaseline(ctx context.Context, root *models.MemoryChangeset) error {
	if root == nil || root.ProjectID == "" || root.ChangesetID == "" {
		return errors.New("legacy root required")
	}
	var exists int
	if err := s.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_legacy_baselines WHERE project_id = ?`,
		root.ProjectID,
	).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}

	rows, err := s.QueryContext(ctx, `
		SELECT event_id
		FROM events
		WHERE project_id = ? AND created_at <= ?
		ORDER BY created_at ASC, event_id ASC
	`, root.ProjectID, root.CreatedAt)
	if err != nil {
		return err
	}
	defer rows.Close()

	eventIDs := make([]string, 0)
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			return err
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	encoded, err := json.Marshal(eventIDs)
	if err != nil {
		return err
	}
	_, err = s.ExecContext(ctx, `
		INSERT OR IGNORE INTO memory_legacy_baselines (
			project_id, root_changeset_id, event_ids_json, captured_through, created_at
		) VALUES (?, ?, ?, ?, ?)
	`, root.ProjectID, root.ChangesetID, string(encoded), root.CreatedAt, time.Now().UTC())
	return err
}

func (s *Store) loadLegacyBaselineIDs(ctx context.Context, projectID, rootID string) ([]string, error) {
	var raw string
	err := s.QueryRowContext(ctx, `
		SELECT event_ids_json
		FROM memory_legacy_baselines
		WHERE project_id = ? AND root_changeset_id = ?
	`, projectID, rootID).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// BuildMemoryContext reconstructs and selects semantic memory at an exact
// project/ref/head. A historical head must be reachable from the named ref so
// an unmerged branch cannot be mislabeled as accepted shared memory.
func (s *Store) BuildMemoryContext(
	ctx context.Context,
	projectID, refName, headID, query string,
	limit int,
) (*models.MemoryContext, error) {
	started := time.Now()
	defer func() {
		metrics.Inc(metrics.RebuildOps)
		metrics.Add(metrics.RebuildDurationMS, time.Since(started).Milliseconds())
	}()
	if projectID == "" {
		return nil, errors.New("project_id required")
	}
	if refName == "" {
		refName = models.RefSharedMain
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	ref, err := s.GetMemoryRef(ctx, projectID, refName)
	if err != nil {
		return nil, err
	}
	if ref == nil && refName == models.RefSharedMain && !s.recoveryReadOnly {
		_, ref, err = s.EnsureLegacyRoot(ctx, projectID, "system")
		if err != nil {
			return nil, err
		}
	}
	if ref == nil {
		return nil, ErrRefNotFound
	}
	if headID == "" {
		headID = ref.HeadChangesetID
	}

	// One bulk traversal serves both the reachability proof and the
	// reconstruction: the requested head's history is a subgraph of the ref
	// head's history, computed in memory without a second database pass.
	graph, err := s.loadMemoryGraph(ctx, projectID, ref.HeadChangesetID, 0)
	if err != nil {
		return nil, err
	}
	if headID != ref.HeadChangesetID {
		sub, ok := graph.subgraph(headID)
		if !ok {
			return nil, fmt.Errorf("head changeset %s is not reachable from ref %s", headID, refName)
		}
		graph = sub
	}
	history := graph.order
	metrics.Add(metrics.RebuildChangesets, int64(len(history)))

	memories := make(map[string]*models.AcceptedMemory)
	relationships := make([]models.MemoryRelationship, 0)
	exclusionReasons := make(map[string]string)
	order := 0

	for _, cs := range history {
		if cs.IdempotencyKey != "legacy-root" {
			continue
		}
		if s.recoveryReadOnly {
			// Recovery mode forbids every implicit write, including baseline
			// capture. A restored/upgraded database lacking the baseline must
			// have it captured deliberately outside recovery mode.
			var exists int
			if err := s.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_legacy_baselines WHERE project_id = ?`, projectID).Scan(&exists); err != nil {
				return nil, err
			}
			if exists == 0 {
				return nil, fmt.Errorf("recovery read-only mode: legacy baseline for project %s is not captured; capture it outside recovery mode before reading context", projectID)
			}
		} else if err := s.EnsureLegacyBaseline(ctx, cs); err != nil {
			return nil, fmt.Errorf("ensure legacy baseline: %w", err)
		}
		ids, err := s.loadLegacyBaselineIDs(ctx, projectID, cs.ChangesetID)
		if err != nil {
			return nil, fmt.Errorf("load legacy baseline: %w", err)
		}
		events, err := s.loadLegacyEvents(ctx, projectID, ids)
		if err != nil {
			return nil, err
		}
		for _, event := range events {
			order++
			memories[event.EventID] = &models.AcceptedMemory{
				MemoryID:      event.EventID,
				Content:       event.Content,
				EventType:     string(event.EventType),
				Kind:          legacyKind(event),
				Scope:         projectID,
				Tags:          parseTags(event.Tags),
				Confidence:    event.Confidence,
				Status:        "active",
				Source:        "legacy_event",
				SourceEventID: event.EventID,
				Payload:       map[string]any{},
				Order:         order,
				Active:        true,
			}
		}
	}

	for _, cs := range history {
		for _, op := range cs.Ops {
			order++
			switch op.OpType {
			case models.OpAddMemory:
				id := resultingMemoryID(cs.ChangesetID, op)
				if content := stringPayload(op.Payload, "content"); content != "" {
					memory := &models.AcceptedMemory{
						MemoryID:              id,
						Content:               content,
						Status:                "active",
						Source:                "memory_git",
						IntroducedChangesetID: cs.ChangesetID,
						LastChangesetID:       cs.ChangesetID,
						Payload:               clonePayload(op.Payload),
						Order:                 order,
						Active:                true,
					}
					overlayMemoryPayload(memory, op.Payload)
					memories[id] = memory
				}

			case models.OpCorrectMemory:
				targetID := op.TargetEventID
				if targetID == "" {
					targetID = resultingMemoryID(cs.ChangesetID, op)
				}
				target := memories[targetID]
				if target == nil {
					exclusionReasons[targetID] = "correction target was not present at this head"
					continue
				}
				if op.ResultingEventID != "" && op.ResultingEventID != targetID {
					target.Active = false
					target.Status = "corrected"
					exclusionReasons[targetID] = "corrected by " + op.ResultingEventID
					replacement := cloneAcceptedMemory(target)
					replacement.MemoryID = op.ResultingEventID
					replacement.Active = true
					replacement.Status = "active"
					replacement.Source = "memory_git"
					replacement.IntroducedChangesetID = cs.ChangesetID
					replacement.LastChangesetID = cs.ChangesetID
					replacement.Order = order
					overlayMemoryPayload(replacement, op.Payload)
					memories[replacement.MemoryID] = replacement
				} else {
					target.Active = true
					target.Status = "active"
					target.Source = "memory_git"
					target.LastChangesetID = cs.ChangesetID
					target.Order = order
					overlayMemoryPayload(target, op.Payload)
				}

			case models.OpSupersedeMemory:
				if target := memories[op.TargetEventID]; target != nil {
					target.Active = false
					target.Status = "superseded"
					target.LastChangesetID = cs.ChangesetID
					exclusionReasons[target.MemoryID] = "superseded at " + cs.ChangesetID
				}
				if content := stringPayload(op.Payload, "content"); content != "" {
					id := resultingMemoryID(cs.ChangesetID, op)
					memory := &models.AcceptedMemory{
						MemoryID: id, Content: content, Status: "active", Source: "memory_git",
						IntroducedChangesetID: cs.ChangesetID, LastChangesetID: cs.ChangesetID,
						Payload: clonePayload(op.Payload), Order: order, Active: true,
					}
					overlayMemoryPayload(memory, op.Payload)
					memories[id] = memory
				}

			case models.OpMarkDuplicate:
				duplicateID := op.TargetEventID
				if duplicateID == "" {
					duplicateID = stringPayload(op.Payload, "duplicate_id")
				}
				if duplicate := memories[duplicateID]; duplicate != nil {
					duplicate.Active = false
					duplicate.Status = "duplicate"
					duplicate.LastChangesetID = cs.ChangesetID
					canonicalID := stringPayload(op.Payload, "duplicate_of")
					if canonicalID == "" {
						canonicalID = op.ResultingEventID
					}
					exclusionReasons[duplicateID] = "duplicate of " + canonicalID
				}

			case models.OpProposeDeprecation:
				if memory := memories[op.TargetEventID]; memory != nil {
					memory.Status = "deprecation_proposed"
					memory.LastChangesetID = cs.ChangesetID
					memory.Order = order
				}

			case models.OpAttachEvidence:
				if memory := memories[op.TargetEventID]; memory != nil {
					memory.Evidence = append(memory.Evidence, clonePayload(op.Payload))
					memory.LastChangesetID = cs.ChangesetID
					memory.Order = order
				}

			case models.OpAttachVerification:
				if memory := memories[op.TargetEventID]; memory != nil {
					memory.Verification = append(memory.Verification, clonePayload(op.Payload))
					memory.LastChangesetID = cs.ChangesetID
					memory.Order = order
				}

			case models.OpAddRelationship:
				fromID := op.TargetEventID
				if fromID == "" {
					fromID = stringPayload(op.Payload, "from_memory_id")
				}
				toID := op.ResultingEventID
				if toID == "" {
					toID = stringPayload(op.Payload, "to_memory_id")
				}
				if fromID != "" && toID != "" {
					relationships = append(relationships, models.MemoryRelationship{
						FromMemoryID: fromID,
						ToMemoryID:   toID,
						Kind:         stringPayload(op.Payload, "kind"),
						Payload:      clonePayload(op.Payload),
						ChangesetID:  cs.ChangesetID,
					})
				}
			}
		}
	}

	// Resolve source event IDs for Memory Git memories in one batched pass
	// instead of a per-memory lookup.
	if err := s.resolveSourceEventIDs(ctx, projectID, memories); err != nil {
		return nil, err
	}

	conflicts, conflictedMemoryIDs, err := s.openConflictsForHistory(ctx, projectID, history)
	if err != nil {
		return nil, err
	}
	for memoryID, conflictID := range conflictedMemoryIDs {
		if memory := memories[memoryID]; memory != nil {
			memory.Active = false
			memory.Status = "conflicted"
			exclusionReasons[memoryID] = "withheld by unresolved conflict " + conflictID
		}
	}
	if len(conflictedMemoryIDs) > 0 {
		filtered := relationships[:0]
		for _, relationship := range relationships {
			if _, blocked := conflictedMemoryIDs[relationship.FromMemoryID]; blocked {
				continue
			}
			if _, blocked := conflictedMemoryIDs[relationship.ToMemoryID]; blocked {
				continue
			}
			filtered = append(filtered, relationship)
		}
		relationships = filtered
	}

	active := make([]models.AcceptedMemory, 0, len(memories))
	for _, memory := range memories {
		if memory.Active && strings.TrimSpace(memory.Content) != "" {
			active = append(active, *memory)
		}
	}
	totalActive := len(active)
	queryTokens := memoryQueryTokens(query)
	sort.SliceStable(active, func(i, j int) bool {
		leftScore := memoryQueryScore(active[i], queryTokens)
		rightScore := memoryQueryScore(active[j], queryTokens)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if active[i].Order != active[j].Order {
			return active[i].Order > active[j].Order
		}
		return active[i].MemoryID < active[j].MemoryID
	})
	if len(active) > limit {
		for _, omitted := range active[limit:] {
			exclusionReasons[omitted.MemoryID] = "outside selected context limit"
		}
		active = active[:limit]
	}

	inclusionReasons := make(map[string]string, len(active))
	for _, memory := range active {
		score := memoryQueryScore(memory, queryTokens)
		if score > 0 {
			inclusionReasons[memory.MemoryID] = fmt.Sprintf("query relevance score %d", score)
		} else {
			inclusionReasons[memory.MemoryID] = "most recent active accepted memory"
		}
	}

	return &models.MemoryContext{
		ProjectID:           projectID,
		RefName:             refName,
		HeadChangesetID:     headID,
		ProjectionVersion:   memoryProjectionVersion,
		TotalActive:         totalActive,
		Memories:            active,
		Relationships:       relationships,
		UnresolvedConflicts: conflicts,
		InclusionReasons:    inclusionReasons,
		ExclusionReasons:    exclusionReasons,
	}, nil
}

func (s *Store) memoryHistoryAt(ctx context.Context, projectID, headID string) ([]*models.MemoryChangeset, error) {
	graph, err := s.loadMemoryGraph(ctx, projectID, headID, 0)
	if err != nil {
		return nil, err
	}
	return graph.order, nil
}

func (s *Store) loadLegacyEvents(ctx context.Context, projectID string, ids []string) ([]*models.Event, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Keep well below SQLite's host-parameter limit and query only the frozen
	// baseline IDs. Post-root project history must not make every projection
	// progressively more expensive.
	const batchSize = 500
	out := make([]*models.Event, 0, len(ids))
	for start := 0; start < len(ids); start += batchSize {
		end := start + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		args = append(args, projectID)
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		rows, err := s.QueryContext(ctx, `
			SELECT event_id, session_id, project_id, parent_event_id, event_type, content,
			       confidence, tags, embedding_id, task_title, task_status,
			       status_changed_at, human_reviewed_at, reviewer_id, thread_id, created_at
			FROM events
			WHERE project_id = ? AND event_id IN (`+strings.Join(placeholders, ",")+`)
		`, args...)
		if err != nil {
			return nil, err
		}
		events, scanErr := scanEvents(rows)
		closeErr := rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out = append(out, events...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].EventID < out[j].EventID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *Store) openConflictsForHistory(ctx context.Context, projectID string, history []*models.MemoryChangeset) ([]string, map[string]string, error) {
	reachable := make(map[string]bool, len(history))
	changesets := make(map[string]*models.MemoryChangeset, len(history))
	for _, cs := range history {
		reachable[cs.ChangesetID] = true
		changesets[cs.ChangesetID] = cs
	}
	rows, err := s.QueryContext(ctx, `
		SELECT conflict_id, left_changeset_id, right_changeset_id
		FROM memory_conflicts
		WHERE project_id = ? AND status = 'open'
		ORDER BY created_at ASC, conflict_id ASC
	`, projectID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []string
	conflictedMemoryIDs := make(map[string]string)
	for rows.Next() {
		var id, left, right string
		if err := rows.Scan(&id, &left, &right); err != nil {
			return nil, nil, err
		}
		if reachable[left] || reachable[right] {
			out = append(out, id)
		}
		// A single accepted side has deterministic projection order. When both
		// unresolved sides are ancestors of this head, fail closed rather than
		// choosing whichever parent happened to be traversed last.
		if reachable[left] && reachable[right] {
			for _, changesetID := range []string{left, right} {
				for _, op := range changesets[changesetID].Ops {
					for _, memoryID := range conflictOpMemoryIDs(changesetID, op) {
						if _, exists := conflictedMemoryIDs[memoryID]; !exists {
							conflictedMemoryIDs[memoryID] = id
						}
					}
				}
			}
		}
	}
	return out, conflictedMemoryIDs, rows.Err()
}

func conflictOpMemoryIDs(changesetID string, op models.MemorySemanticOp) []string {
	ids := make([]string, 0, 2)
	if op.TargetEventID != "" {
		ids = append(ids, op.TargetEventID)
	}
	if op.ResultingEventID != "" {
		ids = append(ids, op.ResultingEventID)
	} else if op.OpType == models.OpAddMemory || op.OpType == models.OpSupersedeMemory {
		ids = append(ids, resultingMemoryID(changesetID, op))
	}
	return ids
}

func resultingMemoryID(changesetID string, op models.MemorySemanticOp) string {
	if op.ResultingEventID != "" {
		return op.ResultingEventID
	}
	for _, key := range []string{"memory_id", "event_id"} {
		if id := stringPayload(op.Payload, key); id != "" {
			return id
		}
	}
	return fmt.Sprintf("mem:%s:%d", changesetID, op.Ordinal)
}

func overlayMemoryPayload(memory *models.AcceptedMemory, payload map[string]any) {
	if memory.Payload == nil {
		memory.Payload = map[string]any{}
	}
	for key, value := range payload {
		memory.Payload[key] = value
	}
	if value := stringPayload(payload, "content"); value != "" {
		memory.Content = value
	}
	if value := stringPayload(payload, "event_type"); value != "" {
		memory.EventType = value
	}
	if value := stringPayload(payload, "kind"); value != "" {
		memory.Kind = value
	}
	if value := stringPayload(payload, "scope"); value != "" {
		memory.Scope = value
	}
	if value := stringPayload(payload, "visibility"); value != "" {
		memory.Visibility = value
	}
	if tags, ok := payload["tags"]; ok {
		memory.Tags = anyTags(tags)
	}
	if confidence, ok := payload["confidence"].(float64); ok {
		memory.Confidence = &confidence
	}
}

func cloneAcceptedMemory(memory *models.AcceptedMemory) *models.AcceptedMemory {
	clone := *memory
	clone.Tags = append([]string(nil), memory.Tags...)
	clone.Evidence = append([]map[string]any(nil), memory.Evidence...)
	clone.Verification = append([]map[string]any(nil), memory.Verification...)
	clone.Payload = clonePayload(memory.Payload)
	return &clone
}

func clonePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		out[key] = value
	}
	return out
}

func stringPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func legacyKind(event *models.Event) string {
	switch event.EventType {
	case models.EventTask:
		return "task"
	case models.EventFlag:
		return "flag"
	case models.EventRecord:
		return "record"
	default:
		return "observation"
	}
}

func parseTags(raw string) []string {
	if raw == "" {
		return nil
	}
	var tags []string
	if json.Unmarshal([]byte(raw), &tags) == nil {
		return tags
	}
	return anyTags(raw)
}

func anyTags(value any) []string {
	switch tags := value.(type) {
	case []string:
		return append([]string(nil), tags...)
	case []any:
		out := make([]string, 0, len(tags))
		for _, tag := range tags {
			if text, ok := tag.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		fields := strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' })
		return fields
	default:
		return nil
	}
}

func memoryQueryTokens(query string) map[string]bool {
	tokens := make(map[string]bool)
	for _, token := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len(token) >= 3 {
			tokens[token] = true
		}
	}
	return tokens
}

func memoryQueryScore(memory models.AcceptedMemory, queryTokens map[string]bool) int {
	if len(queryTokens) == 0 {
		return 0
	}
	haystack := strings.ToLower(strings.Join([]string{
		memory.Content, memory.Kind, memory.Scope, strings.Join(memory.Tags, " "),
	}, " "))
	score := 0
	for token := range queryTokens {
		if strings.Contains(haystack, token) {
			score++
		}
	}
	return score
}

// resolveSourceEventIDs backfills SourceEventID for Memory Git memories whose
// identity also exists as a legacy event, in batched IN queries.
func (s *Store) resolveSourceEventIDs(ctx context.Context, projectID string, memories map[string]*models.AcceptedMemory) error {
	ids := make([]string, 0, len(memories))
	for id, memory := range memories {
		if memory.Source == "memory_git" {
			ids = append(ids, id)
		}
	}
	const batchSize = 500
	for start := 0; start < len(ids); start += batchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+batchSize, len(ids))
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, len(batch)+1)
		args = append(args, projectID)
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		rows, err := s.QueryContext(ctx, `
			SELECT event_id FROM events
			WHERE project_id = ? AND event_id IN (`+strings.Join(placeholders, ",")+`)
		`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				// #nosec G104 -- already returning the scan error; Close failure is immaterial.
				rows.Close()
				return err
			}
			if memory, ok := memories[id]; ok {
				memory.SourceEventID = id
			}
		}
		if err := rows.Err(); err != nil {
			// #nosec G104 -- already returning the iteration error; Close failure is immaterial.
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
