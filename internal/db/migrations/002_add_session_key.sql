-- Migration 002: Add session_key column for OpenClaw sessionKey mapping.
-- Idempotent: handles the case where sessions_new was left behind by a
-- partial previous run (the column exists and sessions_new may or may not exist).

-- Clean up orphan from partial previous run if present.
DROP TABLE IF EXISTS sessions_new;

-- Add session_key column if not already present (defensive, sessions table
-- may already have it from a prior partial run).
ALTER TABLE sessions ADD COLUMN session_key TEXT;
