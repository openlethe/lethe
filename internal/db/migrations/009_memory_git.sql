-- Migration 009: Memory Git V1 storage.
-- Versioned changesets, project-scoped refs, semantic ops, conflicts, and
-- context manifests above immutable events. Does not rewrite event history.

PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS memory_changesets (
    changeset_id         TEXT PRIMARY KEY,
    schema_version       TEXT NOT NULL DEFAULT 'memory_git/v1',
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    ref_name             TEXT NOT NULL,
    parent_ids_json      TEXT NOT NULL DEFAULT '[]',
    author_principal     TEXT NOT NULL,
    persona_id           TEXT,
    actor_id             TEXT,
    surface              TEXT,
    model                TEXT,
    environment          TEXT,
    session_id           TEXT,
    topic_id             TEXT,
    context_manifest_id  TEXT,
    message              TEXT NOT NULL DEFAULT '',
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    idempotency_key      TEXT NOT NULL,
    evidence_json        TEXT NOT NULL DEFAULT '[]',
    verification_json    TEXT NOT NULL DEFAULT '[]',
    integrity_digest     TEXT NOT NULL,
    UNIQUE (project_id, author_principal, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_memory_changesets_project_created
    ON memory_changesets(project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_changesets_ref
    ON memory_changesets(project_id, ref_name, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_changeset_ops (
    changeset_id   TEXT NOT NULL REFERENCES memory_changesets(changeset_id) ON DELETE CASCADE,
    ordinal        INTEGER NOT NULL CHECK (ordinal >= 0),
    op_type        TEXT NOT NULL CHECK (op_type IN (
        'add_memory',
        'add_relationship',
        'correct_memory',
        'supersede_memory',
        'mark_duplicate',
        'propose_deprecation',
        'attach_evidence',
        'attach_verification'
    )),
    target_event_id    TEXT,
    resulting_event_id TEXT,
    payload_json       TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (changeset_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_memory_changeset_ops_target
    ON memory_changeset_ops(target_event_id)
    WHERE target_event_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS memory_refs (
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    ref_name             TEXT NOT NULL,
    head_changeset_id    TEXT NOT NULL REFERENCES memory_changesets(changeset_id),
    protected            INTEGER NOT NULL DEFAULT 0 CHECK (protected IN (0, 1)),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by_principal TEXT,
    PRIMARY KEY (project_id, ref_name)
);

CREATE INDEX IF NOT EXISTS idx_memory_refs_head
    ON memory_refs(head_changeset_id);

CREATE TABLE IF NOT EXISTS memory_conflicts (
    conflict_id          TEXT PRIMARY KEY,
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    base_changeset_id    TEXT,
    left_changeset_id    TEXT NOT NULL,
    right_changeset_id   TEXT NOT NULL,
    conflict_type        TEXT NOT NULL,
    severity             TEXT NOT NULL DEFAULT 'blocking'
        CHECK (severity IN ('info', 'warning', 'blocking')),
    summary              TEXT NOT NULL,
    details_json         TEXT NOT NULL DEFAULT '{}',
    status               TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'resolved', 'rejected', 'deferred')),
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at          DATETIME,
    resolution_note      TEXT
);

CREATE INDEX IF NOT EXISTS idx_memory_conflicts_project_status
    ON memory_conflicts(project_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_manifests (
    manifest_id          TEXT PRIMARY KEY,
    direction            TEXT NOT NULL CHECK (direction IN ('input', 'output')),
    project_id           TEXT NOT NULL REFERENCES projects(project_id),
    ref_name             TEXT NOT NULL,
    head_changeset_id    TEXT NOT NULL,
    base_changeset_id    TEXT,
    resulting_changeset_id TEXT,
    proposed_target_ref  TEXT,
    merge_proposal_id    TEXT,
    selected_memory_ids_json TEXT NOT NULL DEFAULT '[]',
    inclusion_reasons_json   TEXT NOT NULL DEFAULT '{}',
    exclusion_reasons_json   TEXT NOT NULL DEFAULT '{}',
    unresolved_conflicts_json TEXT NOT NULL DEFAULT '[]',
    session_id           TEXT,
    actor_id             TEXT,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_memory_manifests_project_created
    ON memory_manifests(project_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_manifests_head
    ON memory_manifests(head_changeset_id);
