-- Lethe: Agent Memory Layer
-- Migration 001: Initial Schema v0.2.0
-- Task events: memory.task(title, status) with todo/in_progress/done/blocked

PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS agents (
    agent_id     TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen_at DATETIME
);

CREATE TABLE IF NOT EXISTS projects (
    project_id  TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    session_id         TEXT PRIMARY KEY,
    agent_id           TEXT NOT NULL REFERENCES agents(agent_id),
    project_id         TEXT NOT NULL REFERENCES projects(project_id),
    state              TEXT DEFAULT 'active' CHECK (state IN ('active','interrupted','completed')),
    started_at         DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_heartbeat_at DATETIME,
    ended_at           DATETIME,
    summary            TEXT
);

CREATE TABLE IF NOT EXISTS checkpoints (
    checkpoint_id TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES sessions(session_id),
    seq          INTEGER NOT NULL,
    snapshot     TEXT NOT NULL,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(session_id, seq)
);

CREATE TABLE IF NOT EXISTS events (
    event_id         TEXT PRIMARY KEY,
    session_id       TEXT NOT NULL REFERENCES sessions(session_id),
    parent_event_id  TEXT REFERENCES events(event_id),
    event_type       TEXT NOT NULL CHECK (event_type IN ('record','log','flag','task')),
    content          TEXT NOT NULL,
    confidence       REAL,
    tags             TEXT,
    embedding_id     TEXT,
    task_title       TEXT,
    task_status      TEXT CHECK (task_status IN ('todo','in_progress','done','blocked')),
    status_changed_at DATETIME,
    human_reviewed_at DATETIME,
    reviewer_id      TEXT,
    created_at       DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS session_links (
    session_id       TEXT NOT NULL REFERENCES sessions(session_id),
    prior_session_id TEXT NOT NULL REFERENCES sessions(session_id),
    link_type        TEXT DEFAULT 'resume' CHECK (link_type IN ('resume','fork','parallel')),
    PRIMARY KEY (session_id, prior_session_id)
);

CREATE INDEX IF NOT EXISTS idx_events_session    ON events(session_id);
CREATE INDEX IF NOT EXISTS idx_events_type       ON events(event_type);
CREATE INDEX IF NOT EXISTS idx_events_parent     ON events(parent_event_id);
CREATE INDEX IF NOT EXISTS idx_events_conf       ON events(confidence) WHERE confidence IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_events_task       ON events(task_status) WHERE task_status IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_project  ON sessions(project_id);
CREATE INDEX IF NOT EXISTS idx_sessions_state    ON sessions(state);
CREATE INDEX IF NOT EXISTS idx_sessions_agent   ON sessions(agent_id);
CREATE INDEX IF NOT EXISTS idx_checkpoints_session ON checkpoints(session_id);
CREATE INDEX IF NOT EXISTS idx_session_links_prior ON session_links(prior_session_id);
