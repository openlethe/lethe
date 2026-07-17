package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/openlethe/lethe/internal/models"
)

// memoryGraph is a bulk-loaded, deterministic slice of changeset history.
// Order is exactly the recursive DFS post-order the original implementation
// produced (parents before children, first parent first), so projection and
// conflict semantics are unchanged; only the loading strategy differs.
type memoryGraph struct {
	nodes map[string]*models.MemoryChangeset
	order []*models.MemoryChangeset
}

// maxHistoryNodes bounds graph traversal. LETHE_MEMORY_GIT_MAX_HISTORY
// overrides the default; exceeding the limit is an explicit error, never
// silent truncation (a truncated history would silently change projection).
const defaultMaxHistoryNodes = 100_000

func maxHistoryNodesFromEnv() int {
	if v := os.Getenv("LETHE_MEMORY_GIT_MAX_HISTORY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultMaxHistoryNodes
}

// ErrHistoryLimit is returned when a traversal exceeds the configured node
// limit. Callers must narrow the requested head or raise the limit knowingly.
var ErrHistoryLimit = fmt.Errorf("memory history exceeds configured node limit")

// loadMemoryGraph loads the full ancestry of headID in O(depth) queries
// instead of O(nodes): a recursive CTE discovers the (id, parents) skeleton
// in one round trip, changesets and ops are then bulk-loaded in batches, and
// the exact legacy DFS post-order is reconstructed in memory without
// recursion. Cancellation is checked between batches.
func (s *Store) loadMemoryGraph(ctx context.Context, projectID, headID string, maxNodes int) (*memoryGraph, error) {
	if maxNodes <= 0 {
		maxNodes = maxHistoryNodesFromEnv()
	}

	// Phase 1: discover the ancestry skeleton with a recursive CTE. UNION
	// (not UNION ALL) dedupes rows, which also terminates on graph cycles;
	// the project predicate keeps the walk inside the trust boundary. The
	// json_each CROSS JOIN form lets SQLite drive the recursion with primary-
	// key lookups; an IN-subquery form scans the whole project per step.
	type skeletonRow struct {
		id       string
		parentsJ string
	}
	var skeleton []skeletonRow
	rows, err := s.QueryContext(ctx, `
		WITH RECURSIVE reach(changeset_id, parent_ids_json) AS (
			SELECT changeset_id, parent_ids_json FROM memory_changesets
			WHERE changeset_id = ? AND project_id = ?
			UNION
			SELECT mc.changeset_id, mc.parent_ids_json
			FROM reach r
			CROSS JOIN json_each(r.parent_ids_json) j
			CROSS JOIN memory_changesets mc ON mc.changeset_id = j.value AND mc.project_id = ?
		)
		SELECT changeset_id, parent_ids_json FROM reach LIMIT ?
	`, headID, projectID, projectID, maxNodes+1)
	if err != nil {
		return nil, fmt.Errorf("discover memory history: %w", err)
	}
	defer rows.Close()
	parents := make(map[string][]string)
	for rows.Next() {
		var row skeletonRow
		if err := rows.Scan(&row.id, &row.parentsJ); err != nil {
			return nil, err
		}
		var parentIDs []string
		if err := json.Unmarshal([]byte(row.parentsJ), &parentIDs); err != nil {
			return nil, fmt.Errorf("changeset %s parent JSON does not decode: %w", row.id, err)
		}
		parents[row.id] = parentIDs
		skeleton = append(skeleton, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(skeleton) > maxNodes {
		return nil, fmt.Errorf("%w (%d > %d)", ErrHistoryLimit, len(skeleton), maxNodes)
	}
	if len(skeleton) == 0 {
		return nil, ErrChangesetNotFound
	}

	// Phase 2: bulk-load changeset rows in batches.
	nodes := make(map[string]*models.MemoryChangeset, len(skeleton))
	ids := make([]string, 0, len(skeleton))
	for _, row := range skeleton {
		ids = append(ids, row.id)
	}
	const batchSize = 500
	for start := 0; start < len(ids); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+batchSize, len(ids))
		if err := s.loadChangesetBatch(ctx, ids[start:end], nodes); err != nil {
			return nil, err
		}
	}
	// Ops bulk-load (one batched query per batch of changesets).
	for start := 0; start < len(ids); start += batchSize {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(start+batchSize, len(ids))
		if err := s.loadOpsBatch(ctx, ids[start:end], nodes); err != nil {
			return nil, err
		}
	}

	// Phase 3: integrity verification. The legacy per-node path verified every
	// digest through GetChangeset; the bulk path verifies identically after
	// loading so tampering still fails closed on every reconstruction.
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cs, ok := nodes[id]
		if !ok {
			continue
		}
		if err := VerifyChangesetDigest(cs); err != nil {
			return nil, err
		}
	}

	// Phase 4: reconstruct the exact legacy DFS post-order iteratively.
	order, err := dfsPostOrder(headID, parents, nodes)
	if err != nil {
		return nil, err
	}
	return &memoryGraph{nodes: nodes, order: order}, nil
}

// loadChangesetBatch loads full changeset rows (without ops) into nodes.
func (s *Store) loadChangesetBatch(ctx context.Context, ids []string, nodes map[string]*models.MemoryChangeset) error {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	rows, err := s.QueryContext(ctx, `
		SELECT changeset_id, schema_version, project_id, ref_name, parent_ids_json,
			author_principal, COALESCE(persona_id,''), COALESCE(actor_id,''),
			COALESCE(surface,''), COALESCE(model,''), COALESCE(environment,''),
			COALESCE(session_id,''), COALESCE(topic_id,''), COALESCE(context_manifest_id,''),
			message, created_at, idempotency_key, evidence_json, verification_json, integrity_digest
		FROM memory_changesets WHERE changeset_id IN (`+strings.Join(placeholders, ",")+`)
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cs models.MemoryChangeset
		var parentsJSON, evidenceJSON, verificationJSON string
		var createdAt string
		if err := rows.Scan(
			&cs.ChangesetID, &cs.SchemaVersion, &cs.ProjectID, &cs.RefName, &parentsJSON,
			&cs.AuthorPrincipal, &cs.PersonaID, &cs.ActorID, &cs.Surface, &cs.Model, &cs.Environment,
			&cs.SessionID, &cs.TopicID, &cs.ContextManifestID, &cs.Message, &createdAt,
			&cs.IdempotencyKey, &evidenceJSON, &verificationJSON, &cs.IntegrityDigest,
		); err != nil {
			return err
		}
		cs.CreatedAt = parseTime(createdAt)
		if err := json.Unmarshal([]byte(parentsJSON), &cs.ParentIDs); err != nil {
			return fmt.Errorf("changeset %s parents do not decode: %w", cs.ChangesetID, err)
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &cs.Evidence); err != nil {
			return fmt.Errorf("changeset %s evidence does not decode: %w", cs.ChangesetID, err)
		}
		if err := json.Unmarshal([]byte(verificationJSON), &cs.Verification); err != nil {
			return fmt.Errorf("changeset %s verification does not decode: %w", cs.ChangesetID, err)
		}
		if cs.ParentIDs == nil {
			cs.ParentIDs = []string{}
		}
		nodes[cs.ChangesetID] = &cs
	}
	return rows.Err()
}

// loadOpsBatch loads ops for a batch of changesets, ordered per changeset.
func (s *Store) loadOpsBatch(ctx context.Context, ids []string, nodes map[string]*models.MemoryChangeset) error {
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	rows, err := s.QueryContext(ctx, `
		SELECT changeset_id, ordinal, op_type, COALESCE(target_event_id,''), COALESCE(resulting_event_id,''), payload_json
		FROM memory_changeset_ops WHERE changeset_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY changeset_id, ordinal ASC
	`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	opsByChangeset := make(map[string][]models.MemorySemanticOp, len(ids))
	for rows.Next() {
		var changesetID string
		var op models.MemorySemanticOp
		var payloadJSON, opType string
		if err := rows.Scan(&changesetID, &op.Ordinal, &opType, &op.TargetEventID, &op.ResultingEventID, &payloadJSON); err != nil {
			return err
		}
		op.OpType = models.SemanticOpType(opType)
		if err := json.Unmarshal([]byte(payloadJSON), &op.Payload); err != nil {
			return fmt.Errorf("op payload of changeset %s does not decode: %w", changesetID, err)
		}
		if op.Payload == nil {
			op.Payload = map[string]any{}
		}
		opsByChangeset[changesetID] = append(opsByChangeset[changesetID], op)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Assign only this batch's IDs: iterating the whole nodes map here would
	// wipe ops loaded by earlier batches.
	for _, id := range ids {
		cs, ok := nodes[id]
		if !ok {
			continue
		}
		cs.Ops = opsByChangeset[id]
		if cs.Ops == nil {
			cs.Ops = []models.MemorySemanticOp{}
		}
	}
	return nil
}

// dfsPostOrder reproduces, iteratively, the exact visitation order of the
// original recursive implementation: parents visited before their changeset,
// first parent first, with cycle detection.
func dfsPostOrder(headID string, parents map[string][]string, nodes map[string]*models.MemoryChangeset) ([]*models.MemoryChangeset, error) {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current path
		black = 2 // fully explored
	)
	color := make(map[string]int, len(nodes))
	order := make([]*models.MemoryChangeset, 0, len(nodes))

	type frame struct {
		id         string
		nextParent int
	}
	stack := []frame{{id: headID}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		cs, ok := nodes[top.id]
		if !ok {
			return nil, fmt.Errorf("changeset %s referenced but not loaded", top.id)
		}
		if color[top.id] == black {
			stack = stack[:len(stack)-1]
			continue
		}
		if color[top.id] == white {
			color[top.id] = gray
		}
		if top.nextParent < len(cs.ParentIDs) {
			parentID := cs.ParentIDs[top.nextParent]
			top.nextParent++
			switch color[parentID] {
			case black:
				continue
			case gray:
				return nil, fmt.Errorf("cycle detected in memory changeset graph at %s", parentID)
			default:
				// Missing parents (dangling or cross-project references) fail
				// hard, exactly as the recursive implementation did.
				stack = append(stack, frame{id: parentID})
			}
			continue
		}
		color[top.id] = black
		order = append(order, cs)
		stack = stack[:len(stack)-1]
	}
	return order, nil
}

// subgraph restricts the graph to the ancestry of headID, in memory with no
// further database access. Returns false when headID is not reachable.
func (g *memoryGraph) subgraph(headID string) (*memoryGraph, bool) {
	if _, ok := g.nodes[headID]; !ok {
		return nil, false
	}
	parents := make(map[string][]string, len(g.nodes))
	for id, cs := range g.nodes {
		parents[id] = cs.ParentIDs
	}
	// Collect the reachable set from headID.
	reachable := map[string]bool{headID: true}
	stack := []string{headID}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, parentID := range parents[id] {
			if !reachable[parentID] {
				if _, ok := g.nodes[parentID]; ok {
					reachable[parentID] = true
					stack = append(stack, parentID)
				}
			}
		}
	}
	nodes := make(map[string]*models.MemoryChangeset, len(reachable))
	for id := range reachable {
		nodes[id] = g.nodes[id]
	}
	order, err := dfsPostOrder(headID, parents, nodes)
	if err != nil {
		return nil, false
	}
	return &memoryGraph{nodes: nodes, order: order}, true
}
