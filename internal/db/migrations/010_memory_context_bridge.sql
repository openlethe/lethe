-- Migration 010: bridge accepted Memory Git state into context assemblies.
-- Freezes the exact pre-Memory-Git event baseline and links each assembly to
-- the input manifest/head that selected accepted semantic memories.

CREATE TABLE IF NOT EXISTS memory_legacy_baselines (
    project_id        TEXT PRIMARY KEY REFERENCES projects(project_id),
    root_changeset_id TEXT NOT NULL UNIQUE REFERENCES memory_changesets(changeset_id),
    event_ids_json    TEXT NOT NULL DEFAULT '[]',
    captured_through  DATETIME NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE context_assemblies
    ADD COLUMN memory_manifest_id TEXT REFERENCES memory_manifests(manifest_id);

ALTER TABLE context_assemblies
    ADD COLUMN memory_head_changeset_id TEXT;

ALTER TABLE context_assemblies
    ADD COLUMN accepted_estimated_tokens INTEGER
        CHECK (accepted_estimated_tokens IS NULL OR accepted_estimated_tokens >= 0);

CREATE INDEX IF NOT EXISTS idx_context_assemblies_memory_manifest
    ON context_assemblies(memory_manifest_id)
    WHERE memory_manifest_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_legacy_baselines_root
    ON memory_legacy_baselines(root_changeset_id);
