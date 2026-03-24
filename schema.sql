-- Lethe: Agent Memory Layer
-- Schema v0.2.0
--
-- v0.1.0: Initial schema — agents, projects, sessions, checkpoints, events, session_links
-- v0.2.0: Added task event type (memory.task with todo/in_progress/done/blocked)
--
-- IMPORTANT: When event_type = 'task', task_title MUST be non-null.
-- SQLite does not support conditional CHECK constraints; enforce at API layer.

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE agents (
    agent_id    TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME
);

CREATE TABLE projects (
    project_id  TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE sessions (
    session_id        TEXT PRIMARY KEY,
    session_key       TEXT UNIQUE,  -- OpenClaw's stable sessionKey, nullable until first mapped
    agent_id          TEXT NOT NULL REFERENCES agents(agent_id),
    project_id        TEXT NOT NULL REFERENCES projects(project_id),
    state             TEXT DEFAULT 'active' CHECK (state IN ('active','interrupted','completed')),
    started_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat_at DATETIME,
    ended_at          DATETIME,
    summary           TEXT
);

CREATE TABLE checkpoints (
    checkpoint_id  TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL REFERENCES sessions(session_id),
    seq            INTEGER NOT NULL,
    snapshot       TEXT NOT NULL,  -- JSON: open_threads, recent_event_ids, current_task, last_tool
    created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, seq)
);

CREATE TABLE events (
    event_id         TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES sessions(session_id),
    parent_event_id   TEXT REFERENCES events(event_id),
    event_type       TEXT NOT NULL CHECK (event_type IN ('record','log','flag','task')),
    content          TEXT NOT NULL,
    confidence       REAL,  -- 0.0-1.0, agents self-report uncertainty
    tags             TEXT,  -- JSON: freeform tags
    embedding_id     TEXT,  -- reserved for pgvector migration
    -- Task fields (set when event_type = 'task')
    task_title       TEXT,  -- required when event_type = 'task', enforced at API layer
    task_status      TEXT CHECK (task_status IN ('todo','in_progress','done','blocked')),
    status_changed_at DATETIME,
    -- Human review (for flag events)
    human_reviewed_at DATETIME,
    reviewer_id      TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Task history via recursive CTE:
-- WITH RECURSIVE task_chain AS (
--   SELECT * FROM events WHERE event_id = ?
--   UNION ALL
--   SELECT e.* FROM events e JOIN task_chain tc ON e.parent_event_id = tc.event_id
-- )
-- SELECT * FROM task_chain ORDER BY created_at;

CREATE TABLE session_links (
    session_id       TEXT NOT NULL REFERENCES sessions(session_id),
    prior_session_id TEXT NOT NULL REFERENCES sessions(session_id),
    link_type        TEXT DEFAULT 'resume' CHECK (link_type IN ('resume','fork','parallel')),
    PRIMARY KEY (session_id, prior_session_id)
);

-- Indexes
CREATE INDEX idx_events_session   ON events(session_id);
CREATE INDEX idx_events_type      ON events(event_type);
CREATE INDEX idx_events_parent    ON events(parent_event_id);
CREATE INDEX idx_events_conf      ON events(confidence) WHERE confidence IS NOT NULL;
CREATE INDEX idx_events_task      ON events(task_status) WHERE task_status IS NOT NULL;
CREATE INDEX idx_sessions_project ON sessions(project_id);
CREATE INDEX idx_sessions_state   ON sessions(state);
CREATE INDEX idx_sessions_agent   ON sessions(agent_id);
CREATE INDEX idx_checkpoints_session ON checkpoints(session_id);
CREATE INDEX idx_session_links_prior ON session_links(prior_session_id);
