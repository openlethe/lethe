-- Migration 008: Context assembly observability.
-- Records the exact Lethe summary snapshot and event IDs selected by a client
-- after the client completes its own assembly process.
--
-- This is telemetry for debugging current behavior. It is not the parked
-- Context OS manifest model.

CREATE TABLE context_assemblies (
  assembly_id TEXT PRIMARY KEY,

  session_id TEXT NOT NULL
    REFERENCES sessions(session_id)
    ON DELETE CASCADE,

  project_id TEXT NOT NULL
    REFERENCES projects(project_id),

  source TEXT NOT NULL,
  plugin_version TEXT,
  assembler_version TEXT NOT NULL,

  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  message_count INTEGER NOT NULL DEFAULT 0
    CHECK (message_count >= 0),

  provided_token_budget INTEGER
    CHECK (provided_token_budget IS NULL OR provided_token_budget >= 0),

  estimator_id TEXT,

  summary_estimated_tokens INTEGER
    CHECK (summary_estimated_tokens IS NULL OR summary_estimated_tokens >= 0),

  recent_estimated_tokens INTEGER
    CHECK (recent_estimated_tokens IS NULL OR recent_estimated_tokens >= 0),

  conversation_estimated_tokens INTEGER
    CHECK (
      conversation_estimated_tokens IS NULL
      OR conversation_estimated_tokens >= 0
    ),

  total_estimated_tokens INTEGER
    CHECK (total_estimated_tokens IS NULL OR total_estimated_tokens >= 0),

  packed_bytes INTEGER NOT NULL DEFAULT 0
    CHECK (packed_bytes >= 0),

  recent_skipped INTEGER NOT NULL DEFAULT 0
    CHECK (recent_skipped IN (0, 1)),

  skip_reason TEXT,

  notes TEXT
);

CREATE TABLE context_assembly_items (
  assembly_id TEXT NOT NULL
    REFERENCES context_assemblies(assembly_id)
    ON DELETE CASCADE,

  ordinal INTEGER NOT NULL
    CHECK (ordinal >= 0),

  item_kind TEXT NOT NULL
    CHECK (item_kind IN ('summary', 'event')),

  bucket TEXT NOT NULL,

  event_id TEXT
    REFERENCES events(event_id)
    ON DELETE SET NULL,

  content_snapshot TEXT,
  content_sha256 TEXT NOT NULL,

  packed_bytes INTEGER NOT NULL
    CHECK (packed_bytes >= 0),

  estimated_tokens INTEGER
    CHECK (estimated_tokens IS NULL OR estimated_tokens >= 0),

  PRIMARY KEY (assembly_id, ordinal),

  CHECK (
    (
      item_kind = 'summary'
      AND event_id IS NULL
      AND content_snapshot IS NOT NULL
    )
    OR
    (
      item_kind = 'event'
      AND event_id IS NOT NULL
    )
  )
);

CREATE TABLE context_assembly_feedback (
  feedback_id TEXT PRIMARY KEY,

  assembly_id TEXT NOT NULL
    REFERENCES context_assemblies(assembly_id)
    ON DELETE CASCADE,

  verdict TEXT NOT NULL
    CHECK (verdict IN (
      'good',
      'stale_included',
      'missing_memory',
      'too_large',
      'irrelevant',
      'other'
    )),

  related_event_id TEXT
    REFERENCES events(event_id)
    ON DELETE SET NULL,

  note TEXT,

  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_context_assemblies_session_created
  ON context_assemblies(session_id, created_at DESC);

CREATE INDEX idx_context_assemblies_project_created
  ON context_assemblies(project_id, created_at DESC);

CREATE INDEX idx_context_assembly_items_event
  ON context_assembly_items(event_id)
  WHERE event_id IS NOT NULL;

CREATE INDEX idx_context_assembly_feedback_assembly
  ON context_assembly_feedback(assembly_id, created_at DESC);
