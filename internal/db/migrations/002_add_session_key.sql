-- Migration 002: Add session_key column for OpenClaw sessionKey mapping.
-- Idempotent: the column check is done at the Go level before running migrations.

ALTER TABLE sessions ADD COLUMN session_key TEXT;
