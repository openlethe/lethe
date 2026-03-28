-- Lethe: Agent Memory Layer
-- Migration 005: Thread tracking
-- Threads are named, persistent work items that group related events.
-- A thread answers "what are you working on?" (not just "what happened?")
-- Auto-thread on flag: when flag() is called with no thread_id, server auto-creates thread.

-- Create threads table.
CREATE TABLE IF NOT EXISTS threads (
    thread_id    TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(session_id),
    name         TEXT NOT NULL,          -- slug: "auth-design", "pricing-model"
    title        TEXT NOT NULL,           -- human readable: "Auth approach decision"
    status       TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','blocked')),
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at  DATETIME,               -- NULL until resolved
    UNIQUE(session_id, name)             -- project-scoped: same name allowed on different sessions
);

-- Index for fast open-thread queries per session.
CREATE INDEX IF NOT EXISTS idx_threads_session    ON threads(session_id);
CREATE INDEX IF NOT EXISTS idx_threads_status      ON threads(status);
CREATE INDEX IF NOT EXISTS idx_threads_session_status ON threads(session_id, status);

-- Add thread_id to events table (nullable, backward compatible).
-- Events without thread_id are session-level logs (as before).
-- Events with thread_id belong to a specific thread.
ALTER TABLE events ADD COLUMN thread_id TEXT REFERENCES threads(thread_id);

CREATE INDEX IF NOT EXISTS idx_events_thread ON events(thread_id) WHERE thread_id IS NOT NULL;
