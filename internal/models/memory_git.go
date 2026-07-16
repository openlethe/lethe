package models

import "time"

// Memory Git V1 schema version for changesets.
const MemoryGitSchemaVersion = "memory_git/v1"

// Shared main ref for accepted canonical project memory.
const RefSharedMain = "refs/shared/main"

// SemanticOpType enumerates V1 changeset operations.
type SemanticOpType string

const (
	OpAddMemory          SemanticOpType = "add_memory"
	OpAddRelationship    SemanticOpType = "add_relationship"
	OpCorrectMemory      SemanticOpType = "correct_memory"
	OpSupersedeMemory    SemanticOpType = "supersede_memory"
	OpMarkDuplicate      SemanticOpType = "mark_duplicate"
	OpProposeDeprecation SemanticOpType = "propose_deprecation"
	OpAttachEvidence     SemanticOpType = "attach_evidence"
	OpAttachVerification SemanticOpType = "attach_verification"
)

// ValidSemanticOp reports whether op is a supported V1 semantic operation.
func ValidSemanticOp(op string) bool {
	switch SemanticOpType(op) {
	case OpAddMemory, OpAddRelationship, OpCorrectMemory, OpSupersedeMemory,
		OpMarkDuplicate, OpProposeDeprecation, OpAttachEvidence, OpAttachVerification:
		return true
	default:
		return false
	}
}

// MemoryChangeset is an immutable versioned group of semantic memory operations.
type MemoryChangeset struct {
	ChangesetID       string             `json:"changeset_id"`
	SchemaVersion     string             `json:"schema_version"`
	ProjectID         string             `json:"project_id"`
	RefName           string             `json:"ref_name"`
	ParentIDs         []string           `json:"parent_ids"`
	AuthorPrincipal   string             `json:"author_principal"`
	PersonaID         string             `json:"persona_id,omitempty"`
	ActorID           string             `json:"actor_id,omitempty"`
	Surface           string             `json:"surface,omitempty"`
	Model             string             `json:"model,omitempty"`
	Environment       string             `json:"environment,omitempty"`
	SessionID         string             `json:"session_id,omitempty"`
	TopicID           string             `json:"topic_id,omitempty"`
	ContextManifestID string             `json:"context_manifest_id,omitempty"`
	Message           string             `json:"message"`
	CreatedAt         time.Time          `json:"created_at"`
	IdempotencyKey    string             `json:"idempotency_key"`
	Ops               []MemorySemanticOp `json:"ops,omitempty"`
	Evidence          []map[string]any   `json:"evidence,omitempty"`
	Verification      []map[string]any   `json:"verification,omitempty"`
	IntegrityDigest   string             `json:"integrity_digest"`
}

// MemorySemanticOp is one ordered operation inside a changeset.
type MemorySemanticOp struct {
	Ordinal          int            `json:"ordinal"`
	OpType           SemanticOpType `json:"op_type"`
	TargetEventID    string         `json:"target_event_id,omitempty"`
	ResultingEventID string         `json:"resulting_event_id,omitempty"`
	Payload          map[string]any `json:"payload,omitempty"`
}

// MemoryRef is a project-scoped pointer to a head changeset.
type MemoryRef struct {
	ProjectID          string    `json:"project_id"`
	RefName            string    `json:"ref_name"`
	HeadChangesetID    string    `json:"head_changeset_id"`
	Protected          bool      `json:"protected"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	CreatedByPrincipal string    `json:"created_by_principal,omitempty"`
}

// MemoryConflict records a reviewable non-auto-resolved conflict. Identity is
// deterministic (see db.DeterministicConflictID); Status moves through the
// explicit lifecycle open → resolved | rejected | superseded | canceled
// (deferred optional). ProposalID binds the conflict to the proposal or merge
// attempt that persisted it; pre-lifecycle rows have no binding.
type MemoryConflict struct {
	ConflictID       string         `json:"conflict_id"`
	ProjectID        string         `json:"project_id"`
	BaseChangesetID  string         `json:"base_changeset_id,omitempty"`
	LeftChangesetID  string         `json:"left_changeset_id"`
	RightChangesetID string         `json:"right_changeset_id"`
	ConflictType     string         `json:"conflict_type"`
	Severity         string         `json:"severity"`
	Summary          string         `json:"summary"`
	Details          map[string]any `json:"details,omitempty"`
	Status           string         `json:"status"`
	ProposalID       string         `json:"proposal_id,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	ResolvedAt       *time.Time     `json:"resolved_at,omitempty"`
	ResolutionNote   string         `json:"resolution_note,omitempty"`
}

// MemoryManifest pins or records the exact memory view used by a session turn.
type MemoryManifest struct {
	ManifestID           string            `json:"manifest_id"`
	Direction            string            `json:"direction"` // input | output
	ProjectID            string            `json:"project_id"`
	RefName              string            `json:"ref_name"`
	HeadChangesetID      string            `json:"head_changeset_id"`
	BaseChangesetID      string            `json:"base_changeset_id,omitempty"`
	ResultingChangesetID string            `json:"resulting_changeset_id,omitempty"`
	ProposedTargetRef    string            `json:"proposed_target_ref,omitempty"`
	MergeProposalID      string            `json:"merge_proposal_id,omitempty"`
	SelectedMemoryIDs    []string          `json:"selected_memory_ids"`
	InclusionReasons     map[string]string `json:"inclusion_reasons,omitempty"`
	ExclusionReasons     map[string]string `json:"exclusion_reasons,omitempty"`
	UnresolvedConflicts  []string          `json:"unresolved_conflicts,omitempty"`
	SessionID            string            `json:"session_id,omitempty"`
	ActorID              string            `json:"actor_id,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
}

// MemoryContext is a reproducible projection of semantic memory at one ref/head.
// Memories contains only active records selected for the caller's context budget.
type MemoryContext struct {
	ProjectID           string               `json:"project_id"`
	RefName             string               `json:"ref_name"`
	HeadChangesetID     string               `json:"head_changeset_id"`
	ManifestID          string               `json:"manifest_id,omitempty"`
	ProjectionVersion   string               `json:"projection_version"`
	TotalActive         int                  `json:"total_active"`
	Memories            []AcceptedMemory     `json:"memories"`
	Relationships       []MemoryRelationship `json:"relationships,omitempty"`
	UnresolvedConflicts []string             `json:"unresolved_conflicts,omitempty"`
	InclusionReasons    map[string]string    `json:"inclusion_reasons,omitempty"`
	ExclusionReasons    map[string]string    `json:"exclusion_reasons,omitempty"`
}

// AcceptedMemory is one active semantic memory reconstructed from immutable
// legacy events and accepted Memory Git operations.
type AcceptedMemory struct {
	MemoryID              string           `json:"memory_id"`
	Content               string           `json:"content"`
	EventType             string           `json:"event_type,omitempty"`
	Kind                  string           `json:"kind,omitempty"`
	Scope                 string           `json:"scope,omitempty"`
	Visibility            string           `json:"visibility,omitempty"`
	Tags                  []string         `json:"tags,omitempty"`
	Confidence            *float64         `json:"confidence,omitempty"`
	Status                string           `json:"status"`
	Source                string           `json:"source"`
	SourceEventID         string           `json:"source_event_id,omitempty"`
	IntroducedChangesetID string           `json:"introduced_changeset_id,omitempty"`
	LastChangesetID       string           `json:"last_changeset_id,omitempty"`
	Evidence              []map[string]any `json:"evidence,omitempty"`
	Verification          []map[string]any `json:"verification,omitempty"`
	Payload               map[string]any   `json:"payload,omitempty"`
	Order                 int              `json:"-"`
	Active                bool             `json:"-"`
}

// MemoryRelationship is a relationship accepted at the projected head.
type MemoryRelationship struct {
	FromMemoryID string         `json:"from_memory_id"`
	ToMemoryID   string         `json:"to_memory_id"`
	Kind         string         `json:"kind,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	ChangesetID  string         `json:"changeset_id"`
}

// SemanticDiff is a deterministic report of changes between two changesets.
type SemanticDiff struct {
	ProjectID           string         `json:"project_id"`
	BaseChangesetID     string         `json:"base_changeset_id"`
	TargetChangesetID   string         `json:"target_changeset_id"`
	MemoriesAdded       []DiffItem     `json:"memories_added"`
	Corrections         []DiffItem     `json:"corrections_proposed"`
	Superseded          []DiffItem     `json:"records_superseded"`
	RelationshipsAdded  []DiffItem     `json:"relationships_added"`
	DecisionsChanged    []DiffItem     `json:"decisions_changed"`
	TasksFlagsChanged   []DiffItem     `json:"tasks_or_flags_changed"`
	Duplicates          []DiffItem     `json:"duplicates_detected"`
	VisibilityAffected  []DiffItem     `json:"permissions_or_visibility_affected"`
	EvidenceChanged     []DiffItem     `json:"evidence_added_or_removed"`
	UnresolvedConflicts []string       `json:"unresolved_conflicts"`
	KindNotes           []DiffKindNote `json:"kind_notes,omitempty"`
}

// DiffItem is one entry in a semantic diff section.
type DiffItem struct {
	OpOrdinal        int            `json:"op_ordinal"`
	OpType           SemanticOpType `json:"op_type"`
	ChangesetID      string         `json:"changeset_id"`
	TargetEventID    string         `json:"target_event_id,omitempty"`
	ResultingEventID string         `json:"resulting_event_id,omitempty"`
	Summary          string         `json:"summary"`
	Kind             string         `json:"kind,omitempty"` // temporal_update | direct_contradiction | ""
	Payload          map[string]any `json:"payload,omitempty"`
}

// DiffKindNote documents temporal vs contradiction classification.
type DiffKindNote struct {
	LeftEventID  string `json:"left_event_id,omitempty"`
	RightEventID string `json:"right_event_id,omitempty"`
	Kind         string `json:"kind"` // temporal_update | direct_contradiction
	Reason       string `json:"reason"`
}

// IsProtectedRef reports whether a ref name is protected by convention.
func IsProtectedRef(refName string) bool {
	return len(refName) >= len("refs/shared/") &&
		(refName == RefSharedMain || hasPrefix(refName, "refs/shared/"))
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
