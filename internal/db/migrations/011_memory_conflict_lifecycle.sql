-- Migration 011: Conflict lifecycle redesign.
--
-- Conflicts gain a complete lifecycle (open, resolved, rejected, superseded,
-- canceled, deferred) and an explicit proposal binding. Detection is pure:
-- rows are written only by explicit proposal operations, and identity is
-- deterministic (computed by the application), so equivalent retries converge
-- on one row instead of accumulating duplicates.
--
-- Legacy rows are preserved; exact semantic duplicates are collapsed to the
-- earliest row. Rows written before this migration have no proposal binding
-- (proposal_id IS NULL) and keep their original identity.

PRAGMA foreign_keys=OFF;

CREATE TABLE memory_conflicts_v2 (
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
        CHECK (status IN ('open', 'resolved', 'rejected', 'superseded', 'canceled', 'deferred')),
    proposal_id          TEXT,
    created_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at          DATETIME,
    resolution_note      TEXT
);

-- Collapse exact legacy duplicates on (project, base, left, right, type,
-- summary), keeping the earliest row and its identity.
INSERT INTO memory_conflicts_v2 (
    conflict_id, project_id, base_changeset_id, left_changeset_id, right_changeset_id,
    conflict_type, severity, summary, details_json, status, proposal_id,
    created_at, resolved_at, resolution_note
)
SELECT
    conflict_id, project_id, base_changeset_id, left_changeset_id, right_changeset_id,
    conflict_type, severity, summary, details_json, status, NULL,
    created_at, resolved_at, resolution_note
FROM memory_conflicts
WHERE rowid IN (
    SELECT MIN(rowid) FROM memory_conflicts
    GROUP BY project_id, IFNULL(base_changeset_id, ''), left_changeset_id, right_changeset_id,
             conflict_type, summary
);

DROP TABLE memory_conflicts;
ALTER TABLE memory_conflicts_v2 RENAME TO memory_conflicts;

CREATE INDEX IF NOT EXISTS idx_memory_conflicts_project_status
    ON memory_conflicts(project_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_conflicts_proposal
    ON memory_conflicts(proposal_id)
    WHERE proposal_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_conflicts_heads
    ON memory_conflicts(project_id, left_changeset_id, right_changeset_id);

PRAGMA foreign_keys=ON;
