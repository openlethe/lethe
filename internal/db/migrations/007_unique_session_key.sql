-- Migration 007: Unique lookup index for OpenClaw session keys.
-- NULL session_key values are allowed and can appear multiple times; non-NULL
-- session_key values are stable external identifiers and must be unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_session_key ON sessions(session_key) WHERE session_key IS NOT NULL;
