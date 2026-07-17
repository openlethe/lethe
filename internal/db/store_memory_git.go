package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/openlethe/lethe/internal/models"
)

// Memory Git store errors.
var (
	ErrChangesetNotFound   = errors.New("changeset not found")
	ErrRefNotFound         = errors.New("memory ref not found")
	ErrRefCASConflict      = errors.New("memory ref compare-and-swap conflict")
	ErrInvalidSemanticOp   = errors.New("invalid semantic operation")
	ErrIdempotencyConflict = errors.New("changeset idempotency key already used")
	// ErrIdempotencyMismatch is returned when an idempotency key is replayed
	// with a request whose canonical digest differs from the stored changeset.
	// The original write is preserved and the conflicting request is rejected.
	ErrIdempotencyMismatch = errors.New("changeset idempotency key replayed with a different request")
	ErrProtectedRef        = errors.New("protected ref requires merge path")
	ErrEmptyOps            = errors.New("changeset requires at least one operation")
	// ErrIntegrityDigestMismatch is returned when a stored changeset fails
	// read-time integrity verification, indicating corruption or tampering.
	ErrIntegrityDigestMismatch = errors.New("changeset integrity digest mismatch")
)

// CreateChangesetRequest creates an immutable changeset and optionally advances a ref.
type CreateChangesetRequest struct {
	ProjectID         string                    `json:"project_id"`
	RefName           string                    `json:"ref_name"`
	ParentIDs         []string                  `json:"parent_ids"`
	AuthorPrincipal   string                    `json:"author_principal"`
	PersonaID         string                    `json:"persona_id,omitempty"`
	ActorID           string                    `json:"actor_id,omitempty"`
	Surface           string                    `json:"surface,omitempty"`
	Model             string                    `json:"model,omitempty"`
	Environment       string                    `json:"environment,omitempty"`
	SessionID         string                    `json:"session_id,omitempty"`
	TopicID           string                    `json:"topic_id,omitempty"`
	ContextManifestID string                    `json:"context_manifest_id,omitempty"`
	Message           string                    `json:"message"`
	IdempotencyKey    string                    `json:"idempotency_key"`
	Ops               []models.MemorySemanticOp `json:"ops"`
	Evidence          []map[string]any          `json:"evidence,omitempty"`
	Verification      []map[string]any          `json:"verification,omitempty"`
	// ExpectedHead is required when AdvanceRef is true (CAS).
	ExpectedHead string `json:"expected_head,omitempty"`
	// AdvanceRef updates the ref head after insert when true.
	AdvanceRef bool `json:"advance_ref,omitempty"`
	// CreateRefIfMissing creates the ref pointing at the new changeset when absent.
	CreateRefIfMissing bool `json:"create_ref_if_missing,omitempty"`
	// Protected marks a newly created ref as protected.
	Protected bool `json:"protected,omitempty"`
}

// EnsureLegacyRoot creates a documented legacy root changeset and points
// refs/shared/main at it when the project has no Memory Git state yet.
// Existing events remain readable and are never rewritten.
// Short lock contention is retried with backoff (see withBusyRetry).
func (s *Store) EnsureLegacyRoot(ctx context.Context, projectID, principal string) (*models.MemoryChangeset, *models.MemoryRef, error) {
	var root *models.MemoryChangeset
	var ref *models.MemoryRef
	err := withBusyRetry(ctx, func() error {
		var err error
		root, ref, err = s.ensureLegacyRootOnce(ctx, projectID, principal)
		return err
	})
	return root, ref, err
}

func (s *Store) ensureLegacyRootOnce(ctx context.Context, projectID, principal string) (*models.MemoryChangeset, *models.MemoryRef, error) {
	if projectID == "" {
		return nil, nil, errors.New("project_id required")
	}
	if principal == "" {
		principal = "system"
	}
	now := time.Now().UTC()
	if _, err := s.ExecContext(ctx, `
		INSERT INTO projects (project_id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id) DO NOTHING
	`, projectID, projectID, now, now); err != nil {
		return nil, nil, fmt.Errorf("ensure memory git project: %w", err)
	}

	if ref, err := s.GetMemoryRef(ctx, projectID, models.RefSharedMain); err != nil {
		return nil, nil, err
	} else if ref != nil {
		cs, err := s.GetChangeset(ctx, ref.HeadChangesetID)
		if err != nil {
			return nil, nil, err
		}
		// The shared head may have advanced. Locate the original root in its
		// ancestry so old databases also receive an exact frozen baseline.
		history, err := s.memoryHistoryAt(ctx, projectID, ref.HeadChangesetID)
		if err != nil {
			return nil, nil, err
		}
		for _, ancestor := range history {
			if ancestor.IdempotencyKey == "legacy-root" {
				if err := s.EnsureLegacyBaseline(ctx, ancestor); err != nil {
					return nil, nil, err
				}
				break
			}
		}
		return cs, ref, nil
	}

	// If a legacy root already exists without a ref (partial prior run), reuse it.
	existing, err := s.findLegacyRoot(ctx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if existing != nil {
		if err := s.EnsureLegacyBaseline(ctx, existing); err != nil {
			return nil, nil, err
		}
		ref, err := s.createRef(ctx, projectID, models.RefSharedMain, existing.ChangesetID, principal, true)
		return existing, ref, err
	}

	cs, err := s.insertChangeset(ctx, &models.MemoryChangeset{
		ChangesetID:     uuid.Must(uuid.NewV7()).String(),
		SchemaVersion:   models.MemoryGitSchemaVersion,
		ProjectID:       projectID,
		RefName:         models.RefSharedMain,
		ParentIDs:       []string{},
		AuthorPrincipal: principal,
		ActorID:         "system",
		Message:         "legacy-root: baseline for pre-Memory-Git events (no rewrite)",
		CreatedAt:       time.Now().UTC(),
		IdempotencyKey:  "legacy-root",
		Ops:             nil,
		Evidence: []map[string]any{
			{"kind": "legacy_baseline", "note": "pre-existing events remain readable without ID rewrite"},
		},
		Verification: []map[string]any{},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := s.EnsureLegacyBaseline(ctx, cs); err != nil {
		return cs, nil, err
	}
	ref, err := s.createRef(ctx, projectID, models.RefSharedMain, cs.ChangesetID, principal, true)
	if err != nil {
		return cs, nil, err
	}
	return cs, ref, nil
}

func (s *Store) findLegacyRoot(ctx context.Context, projectID string) (*models.MemoryChangeset, error) {
	var id string
	err := s.QueryRowContext(ctx, `
		SELECT changeset_id FROM memory_changesets
		WHERE project_id = ? AND author_principal = 'system' AND idempotency_key = 'legacy-root'
		LIMIT 1
	`, projectID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetChangeset(ctx, id)
}

// CreateChangeset inserts an immutable changeset. When AdvanceRef is set, the
// target ref is CAS-updated against ExpectedHead. Idempotent replay is strict:
// a reused idempotency key returns the original changeset only when the
// canonical request digest matches; a different request reusing the key is
// rejected with ErrIdempotencyMismatch. Short lock contention is retried with
// backoff; persistent contention returns an error wrapping ErrDatabaseBusy.
func (s *Store) CreateChangeset(ctx context.Context, req CreateChangesetRequest) (*models.MemoryChangeset, error) {
	var cs *models.MemoryChangeset
	err := withBusyRetry(ctx, func() error {
		var err error
		cs, err = s.createChangesetOnce(ctx, req)
		return err
	})
	return cs, err
}

func (s *Store) createChangesetOnce(ctx context.Context, req CreateChangesetRequest) (*models.MemoryChangeset, error) {
	if req.ProjectID == "" {
		return nil, errors.New("project_id required")
	}
	if req.RefName == "" {
		return nil, errors.New("ref_name required")
	}
	if req.AuthorPrincipal == "" {
		return nil, errors.New("author_principal required")
	}
	if req.IdempotencyKey == "" {
		return nil, errors.New("idempotency_key required")
	}
	if len(req.Ops) == 0 {
		return nil, ErrEmptyOps
	}
	for i := range req.Ops {
		if !models.ValidSemanticOp(string(req.Ops[i].OpType)) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidSemanticOp, req.Ops[i].OpType)
		}
		req.Ops[i].Ordinal = i
		if req.Ops[i].Payload == nil {
			req.Ops[i].Payload = map[string]any{}
		}
	}
	if req.ParentIDs == nil {
		req.ParentIDs = []string{}
	}
	if req.CreateRefIfMissing && (req.Protected || models.IsProtectedRef(req.RefName)) {
		return nil, ErrProtectedRef
	}
	if req.AdvanceRef {
		ref, err := s.GetMemoryRef(ctx, req.ProjectID, req.RefName)
		if err != nil {
			return nil, err
		}
		if models.IsProtectedRef(req.RefName) || ref != nil && ref.Protected {
			return nil, ErrProtectedRef
		}
	}
	for _, parentID := range req.ParentIDs {
		parent, err := s.GetChangeset(ctx, parentID)
		if err != nil {
			return nil, fmt.Errorf("parent %s: %w", parentID, err)
		}
		if parent.ProjectID != req.ProjectID {
			return nil, fmt.Errorf("parent %s belongs to project %s, not %s", parentID, parent.ProjectID, req.ProjectID)
		}
	}
	// Semantic validation is authoritative: malformed operations must never
	// enter immutable, integrity-digested history.
	if err := s.validateSemanticOps(ctx, req.ProjectID, req.ParentIDs, req.Ops); err != nil {
		return nil, err
	}

	cs := &models.MemoryChangeset{
		ChangesetID:       uuid.Must(uuid.NewV7()).String(),
		SchemaVersion:     models.MemoryGitSchemaVersion,
		ProjectID:         req.ProjectID,
		RefName:           req.RefName,
		ParentIDs:         append([]string(nil), req.ParentIDs...),
		AuthorPrincipal:   req.AuthorPrincipal,
		PersonaID:         req.PersonaID,
		ActorID:           req.ActorID,
		Surface:           req.Surface,
		Model:             req.Model,
		Environment:       req.Environment,
		SessionID:         req.SessionID,
		TopicID:           req.TopicID,
		ContextManifestID: req.ContextManifestID,
		Message:           req.Message,
		CreatedAt:         time.Now().UTC(),
		IdempotencyKey:    req.IdempotencyKey,
		Ops:               req.Ops,
		Evidence:          req.Evidence,
		Verification:      req.Verification,
	}
	if cs.Evidence == nil {
		cs.Evidence = []map[string]any{}
	}
	if cs.Verification == nil {
		cs.Verification = []map[string]any{}
	}
	// Canonical request digest: computed over the normalized request so an
	// idempotent replay of the same request yields the same digest.
	requestDigest := ComputeChangesetDigest(cs)

	// Idempotent replay: return the original only when request digests match.
	if existing, err := s.FindChangesetByIdempotency(ctx, req.ProjectID, req.AuthorPrincipal, req.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.IntegrityDigest != requestDigest {
			return nil, fmt.Errorf("%w: key %s", ErrIdempotencyMismatch, req.IdempotencyKey)
		}
		return existing, nil
	}

	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := s.insertChangesetTx(ctx, tx, cs); err != nil {
		if isUniqueViolation(err) {
			// Concurrent insert with same idempotency key — return the winner
			// only when it records the same request; otherwise the replay is
			// a conflicting write and must be rejected, not silently dropped.
			existing, findErr := s.FindChangesetByIdempotency(ctx, req.ProjectID, req.AuthorPrincipal, req.IdempotencyKey)
			if findErr != nil {
				return nil, findErr
			}
			if existing != nil {
				if existing.IntegrityDigest != requestDigest {
					return nil, fmt.Errorf("%w: key %s", ErrIdempotencyMismatch, req.IdempotencyKey)
				}
				return existing, nil
			}
			return nil, ErrIdempotencyConflict
		}
		return nil, err
	}

	if req.AdvanceRef || req.CreateRefIfMissing {
		if err := s.advanceOrCreateRefTx(ctx, tx, req, cs.ChangesetID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetChangeset(ctx, cs.ChangesetID)
}

func (s *Store) advanceOrCreateRefTx(ctx context.Context, tx *sql.Tx, req CreateChangesetRequest, newHead string) error {
	var current sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT head_changeset_id FROM memory_refs WHERE project_id = ? AND ref_name = ?
	`, req.ProjectID, req.RefName).Scan(&current)
	if err == sql.ErrNoRows {
		if !req.CreateRefIfMissing {
			return ErrRefNotFound
		}
		protected := req.Protected || models.IsProtectedRef(req.RefName)
		now := time.Now().UTC()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO memory_refs (
				project_id, ref_name, head_changeset_id, protected, created_at, updated_at, created_by_principal
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, req.ProjectID, req.RefName, newHead, boolToInt(protected), now, now, req.AuthorPrincipal)
		return err
	}
	if err != nil {
		return err
	}
	if !req.AdvanceRef {
		return nil
	}
	if req.ExpectedHead == "" {
		return errors.New("expected_head required for ref advance")
	}
	if current.String != req.ExpectedHead {
		return fmt.Errorf("%w: expected %s, current %s", ErrRefCASConflict, req.ExpectedHead, current.String)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE memory_refs
		SET head_changeset_id = ?, updated_at = ?
		WHERE project_id = ? AND ref_name = ? AND head_changeset_id = ?
	`, newHead, time.Now().UTC(), req.ProjectID, req.RefName, req.ExpectedHead)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrRefCASConflict
	}
	return nil
}

func (s *Store) insertChangeset(ctx context.Context, cs *models.MemoryChangeset) (*models.MemoryChangeset, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := s.insertChangesetTx(ctx, tx, cs); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cs, nil
}

func (s *Store) insertChangesetTx(ctx context.Context, tx *sql.Tx, cs *models.MemoryChangeset) (*models.MemoryChangeset, error) {
	cs.IntegrityDigest = ComputeChangesetDigest(cs)

	parentsJSON, _ := json.Marshal(cs.ParentIDs)
	evidenceJSON, _ := json.Marshal(cs.Evidence)
	verificationJSON, _ := json.Marshal(cs.Verification)

	_, err := tx.ExecContext(ctx, `
		INSERT INTO memory_changesets (
			changeset_id, schema_version, project_id, ref_name, parent_ids_json,
			author_principal, persona_id, actor_id, surface, model, environment,
			session_id, topic_id, context_manifest_id, message, created_at,
			idempotency_key, evidence_json, verification_json, integrity_digest
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		cs.ChangesetID, cs.SchemaVersion, cs.ProjectID, cs.RefName, string(parentsJSON),
		cs.AuthorPrincipal, nullString(cs.PersonaID), nullString(cs.ActorID),
		nullString(cs.Surface), nullString(cs.Model), nullString(cs.Environment),
		nullString(cs.SessionID), nullString(cs.TopicID), nullString(cs.ContextManifestID),
		cs.Message, cs.CreatedAt, cs.IdempotencyKey, string(evidenceJSON),
		string(verificationJSON), cs.IntegrityDigest,
	)
	if err != nil {
		return nil, err
	}

	for _, op := range cs.Ops {
		payloadJSON, _ := json.Marshal(op.Payload)
		if op.Payload == nil {
			payloadJSON = []byte("{}")
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO memory_changeset_ops (
				changeset_id, ordinal, op_type, target_event_id, resulting_event_id, payload_json
			) VALUES (?, ?, ?, ?, ?, ?)
		`, cs.ChangesetID, op.Ordinal, string(op.OpType),
			nullString(op.TargetEventID), nullString(op.ResultingEventID), string(payloadJSON))
		if err != nil {
			return nil, err
		}
	}
	return cs, nil
}

// ComputeChangesetDigest returns a stable SHA-256 over canonical changeset
// fields (digest algorithm v2). Parent order is preserved: ordering is
// semantically meaningful for merge commits and ancestry traversal, so two
// records that differ only in parent order must not collide. The digest
// covers every immutable, semantically relevant field. changeset_id and
// created_at are instance metadata assigned at creation time, not semantic
// content — excluding them keeps the digest reproducible from the request,
// which is what strict idempotency replay matching relies on.
func ComputeChangesetDigest(cs *models.MemoryChangeset) string {
	return computeChangesetDigest(cs)
}

// computeChangesetDigestV1 reproduces the legacy digest: identical to v2 but
// with parent_ids sorted before hashing. It exists only so the v2 migration
// can prove a stored row was written by the v1 algorithm before upgrading it;
// a row matching neither algorithm is treated as tampered.
func computeChangesetDigestV1(cs *models.MemoryChangeset) string {
	sorted := *cs
	sorted.ParentIDs = append([]string(nil), cs.ParentIDs...)
	sort.Strings(sorted.ParentIDs)
	return computeChangesetDigest(&sorted)
}

func computeChangesetDigest(cs *models.MemoryChangeset) string {
	type digOp struct {
		Ordinal          int            `json:"ordinal"`
		OpType           string         `json:"op_type"`
		TargetEventID    string         `json:"target_event_id,omitempty"`
		ResultingEventID string         `json:"resulting_event_id,omitempty"`
		Payload          map[string]any `json:"payload,omitempty"`
	}
	parents := append([]string(nil), cs.ParentIDs...)
	ops := make([]digOp, 0, len(cs.Ops))
	for _, op := range cs.Ops {
		ops = append(ops, digOp{
			Ordinal:          op.Ordinal,
			OpType:           string(op.OpType),
			TargetEventID:    op.TargetEventID,
			ResultingEventID: op.ResultingEventID,
			Payload:          op.Payload,
		})
	}
	payload := map[string]any{
		"schema_version":      cs.SchemaVersion,
		"project_id":          cs.ProjectID,
		"ref_name":            cs.RefName,
		"parent_ids":          parents,
		"author_principal":    cs.AuthorPrincipal,
		"persona_id":          cs.PersonaID,
		"actor_id":            cs.ActorID,
		"surface":             cs.Surface,
		"model":               cs.Model,
		"environment":         cs.Environment,
		"session_id":          cs.SessionID,
		"topic_id":            cs.TopicID,
		"context_manifest_id": cs.ContextManifestID,
		"message":             cs.Message,
		"idempotency_key":     cs.IdempotencyKey,
		"ops":                 ops,
		"evidence":            cs.Evidence,
		"verification":        cs.Verification,
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// migrateChangesetDigestsV2 upgrades stored integrity digests to digest
// algorithm v2 (order-preserving parents). Digest v1 sorted parent_ids before
// hashing, so merge records that differed only in parent order collided.
// A row is upgraded only when its stored digest provably matches the legacy
// v1 algorithm; a row matching neither v1 nor v2 was corrupted or tampered
// before the upgrade, so the migration fails closed and names it instead of
// rehashing and blessing it. The read-time verification in GetChangeset then
// protects records going forward. Runs exactly once per database, tracked in
// schema_versions.
func migrateChangesetDigestsV2(db *DB) error {
	const marker = "011_changeset_digests_v2"
	var applied int
	err := db.QueryRow("SELECT 1 FROM schema_versions WHERE name=?", marker).Scan(&applied)
	if err == nil {
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	var table int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='memory_changesets'").Scan(&table); err != nil {
		return err
	}
	if table == 0 {
		_, err := db.Exec("INSERT INTO schema_versions (name) VALUES (?)", marker)
		return err
	}

	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Raw reads: GetChangeset verifies v2 digests, which pre-upgrade rows
	// intentionally do not yet satisfy.
	rows, err := tx.QueryContext(ctx, `
		SELECT changeset_id, schema_version, project_id, ref_name, parent_ids_json,
			author_principal, COALESCE(persona_id,''), COALESCE(actor_id,''),
			COALESCE(surface,''), COALESCE(model,''), COALESCE(environment,''),
			COALESCE(session_id,''), COALESCE(topic_id,''), COALESCE(context_manifest_id,''),
			message, idempotency_key, evidence_json, verification_json, integrity_digest
		FROM memory_changesets
	`)
	if err != nil {
		return err
	}
	type digestUpdate struct {
		id     string
		digest string
	}
	var updates []digestUpdate
	var mismatched []string
	for rows.Next() {
		var cs models.MemoryChangeset
		var parentsJSON, evidenceJSON, verificationJSON string
		if err := rows.Scan(
			&cs.ChangesetID, &cs.SchemaVersion, &cs.ProjectID, &cs.RefName, &parentsJSON,
			&cs.AuthorPrincipal, &cs.PersonaID, &cs.ActorID, &cs.Surface, &cs.Model, &cs.Environment,
			&cs.SessionID, &cs.TopicID, &cs.ContextManifestID, &cs.Message,
			&cs.IdempotencyKey, &evidenceJSON, &verificationJSON, &cs.IntegrityDigest,
		); err != nil {
			// #nosec G104 -- already returning the scan error; Close failure is immaterial.
			rows.Close()
			return err
		}
		_ = json.Unmarshal([]byte(parentsJSON), &cs.ParentIDs)
		_ = json.Unmarshal([]byte(evidenceJSON), &cs.Evidence)
		_ = json.Unmarshal([]byte(verificationJSON), &cs.Verification)
		ops, err := loadChangesetOpsTx(ctx, tx, cs.ChangesetID)
		if err != nil {
			// #nosec G104 -- already returning the load error; Close failure is immaterial.
			rows.Close()
			return err
		}
		cs.Ops = ops
		stored := cs.IntegrityDigest
		switch v2 := ComputeChangesetDigest(&cs); {
		case v2 == stored:
			// Already current.
		case computeChangesetDigestV1(&cs) == stored:
			// Provably written by the legacy algorithm: safe to upgrade.
			updates = append(updates, digestUpdate{id: cs.ChangesetID, digest: v2})
		default:
			// Matches neither algorithm: pre-existing corruption or tamper.
			mismatched = append(mismatched, cs.ChangesetID)
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
	if len(mismatched) > 0 {
		return fmt.Errorf("%w during digest migration (restore from backup or investigate before retrying): %s",
			ErrIntegrityDigestMismatch, strings.Join(mismatched, ", "))
	}

	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, "UPDATE memory_changesets SET integrity_digest = ? WHERE changeset_id = ?", u.digest, u.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_versions (name) VALUES (?)", marker); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if len(updates) > 0 {
		log.Printf("migrations: recomputed %d changeset integrity digests (v2, order-preserving parents)", len(updates))
	}
	return nil
}

func loadChangesetOpsTx(ctx context.Context, tx *sql.Tx, changesetID string) ([]models.MemorySemanticOp, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ordinal, op_type, COALESCE(target_event_id,''), COALESCE(resulting_event_id,''), payload_json
		FROM memory_changeset_ops WHERE changeset_id = ? ORDER BY ordinal ASC
	`, changesetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ops []models.MemorySemanticOp
	for rows.Next() {
		var op models.MemorySemanticOp
		var payloadJSON string
		var opType string
		if err := rows.Scan(&op.Ordinal, &opType, &op.TargetEventID, &op.ResultingEventID, &payloadJSON); err != nil {
			return nil, err
		}
		op.OpType = models.SemanticOpType(opType)
		_ = json.Unmarshal([]byte(payloadJSON), &op.Payload)
		if op.Payload == nil {
			op.Payload = map[string]any{}
		}
		ops = append(ops, op)
	}
	if ops == nil {
		ops = []models.MemorySemanticOp{}
	}
	return ops, rows.Err()
}

// GetChangeset loads a changeset and its ops. Every read re-verifies the
// integrity digest: tampering with a stored row is always detected, never
// masked by a cache.
func (s *Store) GetChangeset(ctx context.Context, id string) (*models.MemoryChangeset, error) {
	row := s.QueryRowContext(ctx, `
		SELECT changeset_id, schema_version, project_id, ref_name, parent_ids_json,
			author_principal, COALESCE(persona_id,''), COALESCE(actor_id,''),
			COALESCE(surface,''), COALESCE(model,''), COALESCE(environment,''),
			COALESCE(session_id,''), COALESCE(topic_id,''), COALESCE(context_manifest_id,''),
			message, created_at, idempotency_key, evidence_json, verification_json, integrity_digest
		FROM memory_changesets WHERE changeset_id = ?
	`, id)

	var cs models.MemoryChangeset
	var parentsJSON, evidenceJSON, verificationJSON string
	var createdAt string
	if err := row.Scan(
		&cs.ChangesetID, &cs.SchemaVersion, &cs.ProjectID, &cs.RefName, &parentsJSON,
		&cs.AuthorPrincipal, &cs.PersonaID, &cs.ActorID, &cs.Surface, &cs.Model, &cs.Environment,
		&cs.SessionID, &cs.TopicID, &cs.ContextManifestID, &cs.Message, &createdAt,
		&cs.IdempotencyKey, &evidenceJSON, &verificationJSON, &cs.IntegrityDigest,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrChangesetNotFound
		}
		return nil, err
	}
	cs.CreatedAt = parseTime(createdAt)
	_ = json.Unmarshal([]byte(parentsJSON), &cs.ParentIDs)
	_ = json.Unmarshal([]byte(evidenceJSON), &cs.Evidence)
	_ = json.Unmarshal([]byte(verificationJSON), &cs.Verification)
	if cs.ParentIDs == nil {
		cs.ParentIDs = []string{}
	}

	ops, err := s.loadOps(ctx, id)
	if err != nil {
		return nil, err
	}
	cs.Ops = ops
	if err := VerifyChangesetDigest(&cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// VerifyChangesetDigest recomputes the canonical digest of a loaded changeset
// and compares it with the stored digest. A mismatch means the stored record
// was corrupted or tampered with after insertion.
func VerifyChangesetDigest(cs *models.MemoryChangeset) error {
	if ComputeChangesetDigest(cs) != cs.IntegrityDigest {
		return fmt.Errorf("%w: %s", ErrIntegrityDigestMismatch, cs.ChangesetID)
	}
	return nil
}

// VerifyChangesetChain performs full-chain verification for a project: every
// changeset's integrity digest is recomputed and every parent reference must
// resolve to a changeset inside the same project. It returns the number of
// verified changesets and one error per integrity failure.
func (s *Store) VerifyChangesetChain(ctx context.Context, projectID string) (int, []error) {
	rows, err := s.QueryContext(ctx, `
		SELECT changeset_id FROM memory_changesets WHERE project_id = ? ORDER BY created_at ASC
	`, projectID)
	if err != nil {
		return 0, []error{err}
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			// #nosec G104 -- already returning the scan error; Close failure is immaterial.
			rows.Close()
			return 0, []error{err}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		// #nosec G104 -- already returning the iteration error; Close failure is immaterial.
		rows.Close()
		return 0, []error{err}
	}
	if err := rows.Close(); err != nil {
		return 0, []error{err}
	}

	verified := 0
	var failures []error
	for _, id := range ids {
		cs, err := s.GetChangeset(ctx, id) // GetChangeset verifies the digest.
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, parentID := range cs.ParentIDs {
			parent, err := s.GetChangeset(ctx, parentID)
			if err != nil {
				failures = append(failures, fmt.Errorf("changeset %s: parent %s: %v", id, parentID, err))
				continue
			}
			if parent.ProjectID != projectID {
				failures = append(failures, fmt.Errorf("changeset %s: parent %s belongs to project %s", id, parentID, parent.ProjectID))
			}
		}
		verified++
	}
	return verified, failures
}

func (s *Store) loadOps(ctx context.Context, changesetID string) ([]models.MemorySemanticOp, error) {
	rows, err := s.QueryContext(ctx, `
		SELECT ordinal, op_type, COALESCE(target_event_id,''), COALESCE(resulting_event_id,''), payload_json
		FROM memory_changeset_ops WHERE changeset_id = ? ORDER BY ordinal ASC
	`, changesetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ops []models.MemorySemanticOp
	for rows.Next() {
		var op models.MemorySemanticOp
		var payloadJSON string
		var opType string
		if err := rows.Scan(&op.Ordinal, &opType, &op.TargetEventID, &op.ResultingEventID, &payloadJSON); err != nil {
			return nil, err
		}
		op.OpType = models.SemanticOpType(opType)
		_ = json.Unmarshal([]byte(payloadJSON), &op.Payload)
		if op.Payload == nil {
			op.Payload = map[string]any{}
		}
		ops = append(ops, op)
	}
	if ops == nil {
		ops = []models.MemorySemanticOp{}
	}
	return ops, rows.Err()
}

// FindChangesetByIdempotency returns an existing changeset for a principal key.
func (s *Store) FindChangesetByIdempotency(ctx context.Context, projectID, principal, key string) (*models.MemoryChangeset, error) {
	var id string
	err := s.QueryRowContext(ctx, `
		SELECT changeset_id FROM memory_changesets
		WHERE project_id = ? AND author_principal = ? AND idempotency_key = ?
	`, projectID, principal, key).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetChangeset(ctx, id)
}

// ListChangesets returns newest-first log for a project ref.
func (s *Store) ListChangesets(ctx context.Context, projectID, refName string, limit int) ([]*models.MemoryChangeset, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	// Get ref head to start ancestry walk
	ref, err := s.GetMemoryRef(ctx, projectID, refName)
	if err != nil {
		return nil, err
	}
	if ref == nil || ref.HeadChangesetID == "" {
		return nil, nil
	}

	var out []*models.MemoryChangeset
	head := ref.HeadChangesetID
	for head != "" && len(out) < limit {
		cs, err := s.GetChangeset(ctx, head)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
		if len(cs.ParentIDs) > 0 {
			head = cs.ParentIDs[0]
		} else {
			head = ""
		}
	}
	return out, nil
}

// GetMemoryRef returns a project-scoped ref.
func (s *Store) GetMemoryRef(ctx context.Context, projectID, refName string) (*models.MemoryRef, error) {
	var ref models.MemoryRef
	var protected int
	var createdAt, updatedAt string
	var createdBy sql.NullString
	err := s.QueryRowContext(ctx, `
		SELECT project_id, ref_name, head_changeset_id, protected, created_at, updated_at, created_by_principal
		FROM memory_refs WHERE project_id = ? AND ref_name = ?
	`, projectID, refName).Scan(
		&ref.ProjectID, &ref.RefName, &ref.HeadChangesetID, &protected, &createdAt, &updatedAt, &createdBy,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ref.Protected = protected == 1
	ref.CreatedAt = parseTime(createdAt)
	ref.UpdatedAt = parseTime(updatedAt)
	if createdBy.Valid {
		ref.CreatedByPrincipal = createdBy.String
	}
	return &ref, nil
}

// ListMemoryRefs lists all refs for a project.
func (s *Store) ListMemoryRefs(ctx context.Context, projectID string) ([]*models.MemoryRef, error) {
	rows, err := s.QueryContext(ctx, `
		SELECT project_id, ref_name, head_changeset_id, protected, created_at, updated_at, COALESCE(created_by_principal,'')
		FROM memory_refs WHERE project_id = ? ORDER BY ref_name ASC
	`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*models.MemoryRef
	for rows.Next() {
		var ref models.MemoryRef
		var protected int
		var createdAt, updatedAt string
		if err := rows.Scan(&ref.ProjectID, &ref.RefName, &ref.HeadChangesetID, &protected, &createdAt, &updatedAt, &ref.CreatedByPrincipal); err != nil {
			return nil, err
		}
		ref.Protected = protected == 1
		ref.CreatedAt = parseTime(createdAt)
		ref.UpdatedAt = parseTime(updatedAt)
		out = append(out, &ref)
	}
	return out, rows.Err()
}

// CreateMemoryBranch creates a branch ref from an expected base head.
// Short lock contention is retried with backoff (see withBusyRetry).
func (s *Store) CreateMemoryBranch(ctx context.Context, projectID, refName, headChangesetID, principal string, protected bool) (*models.MemoryRef, error) {
	var ref *models.MemoryRef
	err := withBusyRetry(ctx, func() error {
		var err error
		ref, err = s.createMemoryBranchOnce(ctx, projectID, refName, headChangesetID, principal, protected)
		return err
	})
	return ref, err
}

func (s *Store) createMemoryBranchOnce(ctx context.Context, projectID, refName, headChangesetID, principal string, protected bool) (*models.MemoryRef, error) {
	if projectID == "" || refName == "" || headChangesetID == "" {
		return nil, errors.New("project_id, ref_name, and head_changeset_id required")
	}
	if protected || models.IsProtectedRef(refName) {
		return nil, ErrProtectedRef
	}
	head, err := s.GetChangeset(ctx, headChangesetID)
	if err != nil {
		return nil, err
	}
	if head.ProjectID != projectID {
		return nil, errors.New("head changeset project mismatch")
	}
	existing, err := s.GetMemoryRef(ctx, projectID, refName)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, fmt.Errorf("ref already exists: %s", refName)
	}
	return s.createRef(ctx, projectID, refName, headChangesetID, principal, protected)
}

func (s *Store) createRef(ctx context.Context, projectID, refName, head, principal string, protected bool) (*models.MemoryRef, error) {
	now := time.Now().UTC()
	_, err := s.ExecContext(ctx, `
		INSERT INTO memory_refs (
			project_id, ref_name, head_changeset_id, protected, created_at, updated_at, created_by_principal
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, projectID, refName, head, boolToInt(protected), now, now, principal)
	if err != nil {
		return nil, err
	}
	return s.GetMemoryRef(ctx, projectID, refName)
}

// CASUpdateRef advances a ref only when the expected head matches.
// Short lock contention is retried with backoff (see withBusyRetry); a CAS
// conflict is not retried — the caller must observe the moved head.
func (s *Store) CASUpdateRef(ctx context.Context, projectID, refName, expectedHead, newHead string) (*models.MemoryRef, error) {
	var ref *models.MemoryRef
	err := withBusyRetry(ctx, func() error {
		var err error
		ref, err = s.casUpdateRefOnce(ctx, projectID, refName, expectedHead, newHead)
		return err
	})
	return ref, err
}

func (s *Store) casUpdateRefOnce(ctx context.Context, projectID, refName, expectedHead, newHead string) (*models.MemoryRef, error) {
	ref, err := s.GetMemoryRef(ctx, projectID, refName)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, ErrRefNotFound
	}
	if ref.Protected || models.IsProtectedRef(refName) {
		return nil, ErrProtectedRef
	}
	return s.casUpdateRef(ctx, projectID, refName, expectedHead, newHead)
}

// CASMergeProtectedRef is the narrowly scoped store primitive used only after
// the API has verified a Charon-signed merge authorization.
// Short lock contention is retried with backoff (see withBusyRetry); a CAS
// conflict is not retried — the caller must observe the moved head.
func (s *Store) CASMergeProtectedRef(ctx context.Context, projectID, refName, expectedHead, newHead string) (*models.MemoryRef, error) {
	var ref *models.MemoryRef
	err := withBusyRetry(ctx, func() error {
		var err error
		ref, err = s.casMergeProtectedRefOnce(ctx, projectID, refName, expectedHead, newHead)
		return err
	})
	return ref, err
}

func (s *Store) casMergeProtectedRefOnce(ctx context.Context, projectID, refName, expectedHead, newHead string) (*models.MemoryRef, error) {
	ref, err := s.GetMemoryRef(ctx, projectID, refName)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, ErrRefNotFound
	}
	if !ref.Protected {
		return nil, errors.New("ref is not protected")
	}
	return s.casUpdateRef(ctx, projectID, refName, expectedHead, newHead)
}

func (s *Store) casUpdateRef(ctx context.Context, projectID, refName, expectedHead, newHead string) (*models.MemoryRef, error) {
	cs, err := s.GetChangeset(ctx, newHead)
	if err != nil {
		return nil, err
	}
	if cs.ProjectID != projectID {
		return nil, errors.New("new head changeset project mismatch")
	}
	res, err := s.ExecContext(ctx, `
		UPDATE memory_refs
		SET head_changeset_id = ?, updated_at = ?
		WHERE project_id = ? AND ref_name = ? AND head_changeset_id = ?
	`, newHead, time.Now().UTC(), projectID, refName, expectedHead)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		cur, getErr := s.GetMemoryRef(ctx, projectID, refName)
		if getErr != nil {
			return nil, getErr
		}
		if cur == nil {
			return nil, ErrRefNotFound
		}
		return cur, fmt.Errorf("%w: expected %s, current %s", ErrRefCASConflict, expectedHead, cur.HeadChangesetID)
	}
	return s.GetMemoryRef(ctx, projectID, refName)
}

// CreateManifest stores an input or output context manifest pin.
// Short lock contention is retried with backoff (see withBusyRetry).
func (s *Store) CreateManifest(ctx context.Context, m *models.MemoryManifest) error {
	return withBusyRetry(ctx, func() error {
		return s.createManifestOnce(ctx, m)
	})
}

func (s *Store) createManifestOnce(ctx context.Context, m *models.MemoryManifest) error {
	if m.ManifestID == "" {
		m.ManifestID = uuid.Must(uuid.NewV7()).String()
	}
	if m.Direction != "input" && m.Direction != "output" {
		return errors.New("direction must be input or output")
	}
	if m.ProjectID == "" || m.RefName == "" || m.HeadChangesetID == "" {
		return errors.New("project_id, ref_name, and head_changeset_id required")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	if m.SelectedMemoryIDs == nil {
		m.SelectedMemoryIDs = []string{}
	}
	if m.InclusionReasons == nil {
		m.InclusionReasons = map[string]string{}
	}
	if m.ExclusionReasons == nil {
		m.ExclusionReasons = map[string]string{}
	}
	if m.UnresolvedConflicts == nil {
		m.UnresolvedConflicts = []string{}
	}

	selectedJSON, _ := json.Marshal(m.SelectedMemoryIDs)
	inclJSON, _ := json.Marshal(m.InclusionReasons)
	exclJSON, _ := json.Marshal(m.ExclusionReasons)
	conflictsJSON, _ := json.Marshal(m.UnresolvedConflicts)

	_, err := s.ExecContext(ctx, `
		INSERT INTO memory_manifests (
			manifest_id, direction, project_id, ref_name, head_changeset_id,
			base_changeset_id, resulting_changeset_id, proposed_target_ref, merge_proposal_id,
			selected_memory_ids_json, inclusion_reasons_json, exclusion_reasons_json,
			unresolved_conflicts_json, session_id, actor_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		m.ManifestID, m.Direction, m.ProjectID, m.RefName, m.HeadChangesetID,
		nullString(m.BaseChangesetID), nullString(m.ResultingChangesetID),
		nullString(m.ProposedTargetRef), nullString(m.MergeProposalID),
		string(selectedJSON), string(inclJSON), string(exclJSON), string(conflictsJSON),
		nullString(m.SessionID), nullString(m.ActorID), m.CreatedAt,
	)
	return err
}

// GetManifest loads a stored context manifest.
func (s *Store) GetManifest(ctx context.Context, id string) (*models.MemoryManifest, error) {
	var m models.MemoryManifest
	var selectedJSON, inclJSON, exclJSON, conflictsJSON string
	var createdAt string
	var base, resulting, target, proposal, sessionID, actorID sql.NullString
	err := s.QueryRowContext(ctx, `
		SELECT manifest_id, direction, project_id, ref_name, head_changeset_id,
			base_changeset_id, resulting_changeset_id, proposed_target_ref, merge_proposal_id,
			selected_memory_ids_json, inclusion_reasons_json, exclusion_reasons_json,
			unresolved_conflicts_json, session_id, actor_id, created_at
		FROM memory_manifests WHERE manifest_id = ?
	`, id).Scan(
		&m.ManifestID, &m.Direction, &m.ProjectID, &m.RefName, &m.HeadChangesetID,
		&base, &resulting, &target, &proposal,
		&selectedJSON, &inclJSON, &exclJSON, &conflictsJSON, &sessionID, &actorID, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.CreatedAt = parseTime(createdAt)
	if base.Valid {
		m.BaseChangesetID = base.String
	}
	if resulting.Valid {
		m.ResultingChangesetID = resulting.String
	}
	if target.Valid {
		m.ProposedTargetRef = target.String
	}
	if proposal.Valid {
		m.MergeProposalID = proposal.String
	}
	if sessionID.Valid {
		m.SessionID = sessionID.String
	}
	if actorID.Valid {
		m.ActorID = actorID.String
	}
	_ = json.Unmarshal([]byte(selectedJSON), &m.SelectedMemoryIDs)
	_ = json.Unmarshal([]byte(inclJSON), &m.InclusionReasons)
	_ = json.Unmarshal([]byte(exclJSON), &m.ExclusionReasons)
	_ = json.Unmarshal([]byte(conflictsJSON), &m.UnresolvedConflicts)
	return &m, nil
}

// CreateConflict stores an explicit reviewable conflict. Identity is
// deterministic when not supplied, so equivalent conflicts converge on one row
// instead of accumulating duplicates. New proposal-time conflicts should go
// through PersistConflicts, which also binds them to their proposal.
func (s *Store) CreateConflict(ctx context.Context, c *models.MemoryConflict) error {
	if c.ConflictID == "" {
		c.ConflictID = DeterministicConflictID(c)
	}
	if c.Severity == "" {
		c.Severity = "blocking"
	}
	if c.Status == "" {
		c.Status = "open"
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if c.Details == nil {
		c.Details = map[string]any{}
	}
	detailsJSON, _ := json.Marshal(c.Details)
	_, err := s.ExecContext(ctx, `
		INSERT INTO memory_conflicts (
			conflict_id, project_id, base_changeset_id, left_changeset_id, right_changeset_id,
			conflict_type, severity, summary, details_json, status, created_at, resolved_at, resolution_note
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		c.ConflictID, c.ProjectID, nullString(c.BaseChangesetID), c.LeftChangesetID, c.RightChangesetID,
		c.ConflictType, c.Severity, c.Summary, string(detailsJSON), c.Status, c.CreatedAt,
		c.ResolvedAt, nullString(c.ResolutionNote),
	)
	return err
}

// DiffChangesets builds a deterministic semantic diff from base to target.
// Walk is parent-chain based from target until base is reached (or roots).
func (s *Store) DiffChangesets(ctx context.Context, projectID, baseID, targetID string) (*models.SemanticDiff, error) {
	if projectID == "" || targetID == "" {
		return nil, errors.New("project_id and target_changeset_id required")
	}
	target, err := s.GetChangeset(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target.ProjectID != projectID {
		return nil, errors.New("target changeset project mismatch")
	}
	if baseID != "" {
		base, err := s.GetChangeset(ctx, baseID)
		if err != nil {
			return nil, err
		}
		if base.ProjectID != projectID {
			return nil, errors.New("base changeset project mismatch")
		}
	}

	chain, err := s.walkAncestry(ctx, targetID, baseID)
	if err != nil {
		return nil, err
	}

	diff := &models.SemanticDiff{
		ProjectID:           projectID,
		BaseChangesetID:     baseID,
		TargetChangesetID:   targetID,
		MemoriesAdded:       []models.DiffItem{},
		Corrections:         []models.DiffItem{},
		Superseded:          []models.DiffItem{},
		RelationshipsAdded:  []models.DiffItem{},
		DecisionsChanged:    []models.DiffItem{},
		TasksFlagsChanged:   []models.DiffItem{},
		Duplicates:          []models.DiffItem{},
		VisibilityAffected:  []models.DiffItem{},
		EvidenceChanged:     []models.DiffItem{},
		UnresolvedConflicts: []string{},
		KindNotes:           []models.DiffKindNote{},
	}

	// Oldest first for stable reporting.
	for i := len(chain) - 1; i >= 0; i-- {
		cs := chain[i]
		for _, op := range cs.Ops {
			item := models.DiffItem{
				OpOrdinal:        op.Ordinal,
				OpType:           op.OpType,
				ChangesetID:      cs.ChangesetID,
				TargetEventID:    op.TargetEventID,
				ResultingEventID: op.ResultingEventID,
				Summary:          summarizeOp(op),
				Payload:          op.Payload,
			}
			switch op.OpType {
			case models.OpAddMemory:
				diff.MemoriesAdded = append(diff.MemoriesAdded, item)
			case models.OpCorrectMemory:
				item.Kind = "temporal_update"
				diff.Corrections = append(diff.Corrections, item)
				diff.KindNotes = append(diff.KindNotes, models.DiffKindNote{
					LeftEventID:  op.TargetEventID,
					RightEventID: op.ResultingEventID,
					Kind:         "temporal_update",
					Reason:       "correct_memory establishes lineage",
				})
			case models.OpSupersedeMemory:
				item.Kind = "temporal_update"
				diff.Superseded = append(diff.Superseded, item)
				diff.KindNotes = append(diff.KindNotes, models.DiffKindNote{
					LeftEventID:  op.TargetEventID,
					RightEventID: op.ResultingEventID,
					Kind:         "temporal_update",
					Reason:       "supersede_memory establishes lineage",
				})
			case models.OpAddRelationship:
				diff.RelationshipsAdded = append(diff.RelationshipsAdded, item)
			case models.OpMarkDuplicate:
				diff.Duplicates = append(diff.Duplicates, item)
			case models.OpProposeDeprecation:
				diff.DecisionsChanged = append(diff.DecisionsChanged, item)
			case models.OpAttachEvidence:
				diff.EvidenceChanged = append(diff.EvidenceChanged, item)
			case models.OpAttachVerification:
				diff.EvidenceChanged = append(diff.EvidenceChanged, item)
			}

			if content, _ := op.Payload["content"].(string); content != "" {
				if strings.Contains(strings.ToLower(content), "decision:") || op.Payload["kind"] == "decision" {
					if op.OpType != models.OpProposeDeprecation {
						diff.DecisionsChanged = append(diff.DecisionsChanged, item)
					}
				}
				if et, _ := op.Payload["event_type"].(string); et == "task" || et == "flag" {
					diff.TasksFlagsChanged = append(diff.TasksFlagsChanged, item)
				}
			}
			if v, ok := op.Payload["visibility"]; ok && v != nil {
				diff.VisibilityAffected = append(diff.VisibilityAffected, item)
			}
		}
	}
	return diff, nil
}

// walkAncestry returns target → ... toward base (exclusive of base).
func (s *Store) walkAncestry(ctx context.Context, targetID, baseID string) ([]*models.MemoryChangeset, error) {
	seen := map[string]bool{}
	var out []*models.MemoryChangeset
	queue := []string{targetID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == "" || id == baseID || seen[id] {
			continue
		}
		seen[id] = true
		cs, err := s.GetChangeset(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
		for _, p := range cs.ParentIDs {
			if p != baseID && !seen[p] {
				queue = append(queue, p)
			}
		}
	}
	return out, nil
}

func summarizeOp(op models.MemorySemanticOp) string {
	if s, ok := op.Payload["summary"].(string); ok && s != "" {
		return s
	}
	if c, ok := op.Payload["content"].(string); ok && c != "" {
		if len(c) > 120 {
			return c[:120]
		}
		return c
	}
	return string(op.OpType)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	// SQLite CURRENT_TIMESTAMP style
	if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}
