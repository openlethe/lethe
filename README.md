# Lethe — Agent Memory Layer

A persistent memory layer for AI agents. Records reasoning chains across sessions — decisions, observations, flagged uncertainty, and tasks — so agents don't forget what they figured out.

## Quick Start

```bash
# Build
go build ./cmd/lethe

# Run
./lethe --db ./lethe.db --http :8080
```

## API Reference

Base URL: `http://localhost:8080`

### Health
```
GET /health → {"status":"ok"}
```

### Sessions

**Create session:**
```
POST /sessions
Body: { "agent_id": "...", "project_id": "...", "agent_name": "...", "project_name": "..." }
→ 201 { session_id, agent_id, project_id, state, started_at }
```

**Get session:**
```
GET /sessions/{sessionID}
→ 200 { session_id, agent_id, project_id, state, started_at, ... }
```

**Session summary (dashboard):**
```
GET /sessions/{sessionID}/summary
→ 200 { session, latest_checkpoint, recent_events, checkpoint_count, event_count }
```

**Heartbeat:**
```
POST /sessions/{sessionID}/heartbeat
→ 204 No Content
```

**Interrupt session:**
```
POST /sessions/{sessionID}/interrupt
Body: { "snapshot": { "open_threads": [...], "current_task": "...", "last_tool": "..." } }
→ 200 { session }
```

**Complete session:**
```
POST /sessions/{sessionID}/complete
Body: { "summary": "..." }
→ 200 { session }
```

### Events

**Create event:**
```
POST /sessions/{sessionID}/events
Body: {
  "event_type": "record" | "log" | "flag" | "task",
  "content": "...",
  "confidence": 0.85,        // optional, 0.0-1.0
  "tags": "[...]",           // optional, JSON array string
  "parent_event_id": "...",  // optional, for task chains
  "task_title": "...",       // required when event_type="task"
  "task_status": "todo" | "in_progress" | "done" | "blocked"  // optional
}
→ 201 { "event_id": "...", "created_at": "..." }
```

**Get session events (paginated):**
```
GET /sessions/{sessionID}/events?limit=50&offset=0
→ 200 { "events": [...], "total": N, "limit": N, "offset": N }
```

### Checkpoints

**Create checkpoint:**
```
POST /sessions/{sessionID}/checkpoints
Body: { "snapshot": { "open_threads": [...], "recent_event_ids": [...], "current_task": "...", "last_tool": "..." } }
→ 201 { "checkpoint_id": "...", "seq": N, "created_at": "..." }
```

**List checkpoints:**
```
GET /sessions/{sessionID}/checkpoints
→ 200 { "checkpoints": [...], "total": N }
```

### Flags

**Get unreviewed flags:**
```
GET /flags
→ 200 { "flags": [...], "total": N }
```

**Review a flag:**
```
PUT /flags/{eventID}/review
Body: { "reviewer_id": "human-mike" }
→ 200 { "status": "ok" }
```

### Task Chains

**Get task history:**
```
GET /events/{eventID}/chain
→ 200 { "chain": [...event chain...], "total": N }
```
Returns the full parent chain for a task event (todo → in_progress → done → blocked).

## Architecture

```
cmd/lethe/main.go       — entry point, wires server + store + session manager
internal/api/           — HTTP handlers, Chi router, request/response types
internal/db/            — SQLite store, embedded migrations, all CRUD
internal/models/        — domain types (Agent, Project, Session, Checkpoint, Event)
internal/session/       — session lifecycle state machine and manager
```

## Schema

SQLite with WAL mode. Full schema in `internal/db/migrations/001_init.sql`.

## Project Status

Week 1 (Go server): Complete. All endpoints implemented and tested.

```
scrum/done/
├── server-01-schema-migration.md
├── server-02-db-store.md
├── server-03-session-state-machine.md
├── server-04-checkpoint-write-endpoint.md
├── server-05-two-stage-retrieval-api.md
├── server-06-http-server.md
├── server-07-event-api-record-log-flag.md
├── server-09-session-lifecycle-endpoints.md
└── server-10-session-summary-endpoint.md
```
