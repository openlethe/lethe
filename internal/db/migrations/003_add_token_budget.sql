-- Add token_budget column to sessions for live context token tracking.
-- This is updated on every heartbeat from the OpenClaw plugin.
ALTER TABLE sessions ADD COLUMN token_budget INTEGER DEFAULT 0;
