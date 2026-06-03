-- Migration 007: Unique lookup index for OpenClaw session keys.
-- NULL session_key values are allowed and can appear multiple times; non-NULL
-- session_key values are stable external identifiers and must be unique.
--
-- Older releases had no database constraint, so a concurrent/bootstrap bug
-- could leave duplicate non-NULL keys behind. Keep the newest heartbeat for
-- each key and detach older duplicates before adding the unique index; this
-- preserves startup on existing databases instead of bricking the migration.
WITH ranked_session_keys AS (
    SELECT
        rowid AS rid,
        ROW_NUMBER() OVER (
            PARTITION BY session_key
            ORDER BY COALESCE(last_heartbeat_at, started_at, '') DESC,
                     started_at DESC,
                     session_id DESC
        ) AS rn
    FROM sessions
    WHERE session_key IS NOT NULL
)
UPDATE sessions
SET session_key = NULL
WHERE rowid IN (SELECT rid FROM ranked_session_keys WHERE rn > 1);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_session_key ON sessions(session_key) WHERE session_key IS NOT NULL;
