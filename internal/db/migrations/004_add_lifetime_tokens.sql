-- Migration 004: add total_tokens_consumed to sessions
-- Tracks cumulative tokens consumed across all compacts for a session.
-- token_budget is a snapshot (current context load); total_tokens_consumed is the lifetime accumulator.
ALTER TABLE sessions ADD COLUMN total_tokens_consumed INTEGER NOT NULL DEFAULT 0;
