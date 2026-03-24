-- Migration 002: Add session_key column for OpenClaw sessionKey mapping
-- SQLite cannot ALTER TABLE ADD COLUMN with UNIQUE constraint on existing data,
-- so we recreate the table with the new column and copy data.

-- Create new sessions table with session_key column
CREATE TABLE sessions_new (
    session_id         TEXT PRIMARY KEY,
    session_key        TEXT,
    agent_id           TEXT NOT NULL REFERENCES agents(agent_id),
    project_id         TEXT NOT NULL REFERENCES projects(project_id),
    state              TEXT DEFAULT 'active' CHECK (state IN ('active','interrupted','completed')),
    started_at         DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat_at  DATETIME,
    ended_at           DATETIME,
    summary            TEXT
);

-- Copy all data from old table
INSERT INTO sessions_new (session_id, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary)
SELECT session_id, agent_id, project_id, state, started_at, last_heartbeat_at, ended_at, summary FROM sessions;

-- Drop old table and rename new one
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;

-- Recreate indexes
CREATE INDEX idx_sessions_project ON sessions(project_id);
CREATE INDEX idx_sessions_state ON sessions(state);
CREATE INDEX idx_sessions_agent ON sessions(agent_id);
