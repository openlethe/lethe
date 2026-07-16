# Lethe - Persistent Memory for AI Agents

**"AI that remembers what you decided."**

Every AI agent starts from scratch. Same question, same reasoning, same dead ends - every single session. Lethe breaks that loop.

Lethe is a persistent memory layer for AI agents. It records reasoning chains, decisions, observations, flagged uncertainty, and task history across sessions - so your agent carries context forward instead of rebuilding it from nothing.

The default `legacy` mode is OpenLethe: the existing OpenClaw-compatible
session/event/checkpoint service. The local `git` mode is a separate,
Charon-governed versioned-memory service with its own port and database. See
[the local dual-service topology](docs/local-memory-git.md).

---

## What It Records

| Type | Use Case |
|------|----------|
| `record` | A decision made, a direction chosen, a conclusion reached |
| `log` | An observation, a discovery, something worth noting |
| `flag` | Uncertainty that needs human review before proceeding |
| `task` | A work unit with a status chain: todo → in_progress → done |

Records carry confidence scores (0.0-1.0). Flags persist across sessions until explicitly reviewed. Task events chain together so you can trace the history of any piece of work.

---

## How It Works

```
┌─────────────────────────────────────────────────────────────┐
│  Your AI Agent                                              │
│                                                             │
│  ┌─────────────┐    context assembly      ┌──────────────┐  │
│  │  Lethe      │◄────────────────────────   LLM prompt │ │
│  │  Plugin     │                          │   (prepend)  │  │
│  └──────┬──────┘                          └──────────────┘  │
│         │                                                   │
│         │ HTTP                                              │
│         ▼                                                   │
│  ┌──────────────┐   ┌─────────────────────────────────────┐ │
│  │  Lethe       │   │  Lethe Server (Go + SQLite)         │ │
│  │  Plugin      │   │                                     │ │
│  │  (Node.js)   │   │  Sessions · Events · Checkpoints    │ │
│  └──────────────┘   │  Flags · Threads · Task chains      │ │
│                     └────────────────────────────────────┘
└─────────────────────────────────────────────────────────────┘
```

1. **Sessions** - Each agent session gets a stable key. State: `active`, `interrupted`, or `completed`.
2. **Events** - Every meaningful agent action is logged: decisions, observations, flags, tasks.
3. **Checkpoints** - Periodic snapshots so the agent can resume exactly where it left off.
4. **Compaction** - When a session grows long, events are synthesized into a narrative summary, preserving the full history in compressed form.
5. **Threads** - Flags and uncertainty can be grouped into named threads for tracking open questions across sessions.

In the default OpenLethe path, the plugin assembles only session summaries and bounded recent events. Memory Git context is owned by Charon and is not queried by the plugin unless the experimental `memoryGitContext` compatibility switch is explicitly enabled.

---

## What Makes It Different

Most agent memory is just conversation storage. Lethe is different:

- **Structured, not soup** - Records, logs, flags, and tasks have meaning. It's a memory schema, not a chatlog.
- **Confidence scoring** - Every record carries a confidence score so the agent can calibrate how much to trust prior conclusions.
- **Flag system** - Uncertainty gets surfaced and tracked, not buried in context.
- **Crash recovery** - Interrupted sessions resume from the last checkpoint, not from zero.
- **No external dependencies** - SQLite only. No API keys, no embedding models, no vector search to tune.
- **Lightweight** - A single Docker container. Drop it into any agent stack.

---

## What's New in 0.4.0

Lethe 0.4 makes the current memory path easier to trust.

- **Context correctness** - The OpenClaw plugin now injects the newest bounded session events, not the oldest. Selection uses the summary endpoint's `recent_events`, eliminating the recency bug.
- **Assembly observability** - The plugin reports exactly what Lethe summary and event references were injected on each turn. The UI shows "What Lethe added" with assembly history, summary snapshots, and event ordering.
- **Memory Git subsystem** - In `git` mode, accepted semantic history at `refs/shared/main` is reconstructed at an exact head and can be pinned in an input manifest. Corrections, supersedes, duplicates, historical heads, and unresolved conflicts remain explicit. Charon is the intended client.
- **Frozen legacy baseline** - Pre-Memory-Git event IDs are captured once at the synthetic root; later direct event writes do not silently become accepted shared memory.
- **Visible failures** - Authentication and transport failures now emit throttled warnings instead of silently dropping context and checkpoints.
- **Feedback labels** - Users can mark assemblies as good, stale, missing memory, too large, irrelevant, or other. Feedback is additive and never mutates memory.
- **Security hardening** - Strict Bearer parsing, explicit trust modes (`private`/`loopback`), no forwarded-header trust, SSE same-origin removal, session/project consistency checks, and event/task validation.
- **Project-scoped search** - Plugin search is limited to its configured project.
- **Version alignment** - Server, plugin, and engine info all report `0.4.0`.

Recommended image tags:

```bash
docker pull ghcr.io/openlethe/lethe:0.4.0
# or
docker pull ghcr.io/openlethe/lethe:latest
```

---

## Quick Start

### Docker

```bash
# The image runs as UID 1000 and keeps state in /data. On Linux, Docker
# creates a missing bind-mount directory as root-owned, so create it first
# with the right ownership (one-time):
install -d -o 1000 -g 1000 "$PWD/lethe-data" 2>/dev/null || {
  mkdir -p "$PWD/lethe-data" && sudo chown 1000:1000 "$PWD/lethe-data"; }

docker run -d \
  --name lethe \
  -v "$PWD/lethe-data:/data" \
  -p 127.0.0.1:18483:18483 \
  ghcr.io/openlethe/lethe:0.4.0
```

### From Source

```bash
go build ./cmd/lethe
./lethe --db ./lethe.db --http localhost:18483
```

The server starts in `legacy` mode on port 18483 with a built-in UI at
`http://localhost:18483/ui/`. To run the isolated local Memory Git service on
port 18485, use `docker-compose.git.yml` and a fresh `lethe-git-data`
directory; do not reuse the OpenLethe database.

### Security / Auth

By default Lethe runs in trusted local/private-network mode: local Docker Desktop
and private-network clients can connect without a token, while public-network
clients are rejected. If you expose Lethe beyond localhost or a trusted private
network, set a bearer token and configure clients/plugins with the same value:

```bash
export LETHE_API_KEY="change-me"
./lethe --db ./lethe.db --http :18483
# or: ./lethe --api-key "change-me" ...
```

Clients must send `Authorization: Bearer <token>`. Raw tokens (without the `Bearer`
prefix) are rejected.

For stricter unauthenticated access, use `LETHE_TRUST=loopback` to allow only
loopback connections instead of the default `private` mode (loopback + private
networks + link-local).

When a reverse proxy, tunnel, or shared network is used, a bearer token is mandatory.
Forwarded headers (X-Forwarded-For, etc.) are never used for trust decisions.

---

## Upgrading and Rollback

### Upgrade from v0.3.x to v0.4.0

```bash
# 1. Stop Lethe cleanly
pkill lethe

# 2. Back up the database
cp /data/lethe.db /data/lethe.db.v0.3.x-backup
cp /data/lethe.db-wal /data/lethe.db-wal.v0.3.x-backup 2>/dev/null || true
cp /data/lethe.db-shm /data/lethe.db-shm.v0.3.x-backup 2>/dev/null || true

# 3. Start v0.4.0
#    Migration 008 runs automatically (additive: assembly tables only).
#    Existing data is preserved.
docker run -d \
  --name lethe \
  -v "$PWD/lethe-data:/data" \
  -p 127.0.0.1:18483:18483 \
  ghcr.io/openlethe/lethe:0.4.0

# 4. Verify
#    - Health endpoint: curl http://localhost:18483/api/health
#    - UI: open http://localhost:18483/ui/
#    - Check assembly history appears in the UI for a new session
```

### Rollback to v0.3.x

If you need to roll back:

```bash
# 1. Stop Lethe
docker stop lethe

# 2. Restore the backed-up database
cp /data/lethe.db.v0.3.x-backup /data/lethe.db
# (optional) restore WAL/SHM if they were backed up

# 3. Start v0.3.x image
#    v0.3.x ignores unknown tables (migration 008 tables are harmless orphans).
docker run -d \
  --name lethe \
  -v "$PWD/lethe-data:/data" \
  -p 127.0.0.1:18483:18483 \
  ghcr.io/openlethe/lethe:0.3.0
```

### Safe backup behavior

SQLite WAL databases must be backed up carefully. For a live database, use SQLite's
backup mechanism instead of raw `cp`:

```bash
sqlite3 /data/lethe.db ".backup '/data/lethe.db.backup'"
```

For offline backups after a clean shutdown, regular file copies are safe.

---

## API Overview

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/sessions` | Create a session |
| `GET` | `/api/sessions/{key}` | Get session |
| `GET` | `/api/sessions/{key}/summary` | Session summary + recent events |
| `POST` | `/api/sessions/{key}/heartbeat` | Heartbeat ping |
| `POST` | `/api/sessions/{key}/interrupt` | Interrupt session |
| `POST` | `/api/sessions/{key}/complete` | Complete session |
| `POST` | `/api/sessions/{key}/compact` | Trigger compaction |
| `GET` | `/api/sessions/{key}/events` | List events |
| `POST` | `/api/sessions/{key}/events` | Create event |
| `GET` | `/api/sessions/{key}/checkpoints` | List checkpoints |
| `POST` | `/api/sessions/{key}/checkpoints` | Create checkpoint |
| `GET` | `/api/sessions/{key}/assemblies` | List context assemblies |
| `POST` | `/api/sessions/{key}/assemblies` | Record context assembly |
| `GET` | `/api/assemblies/{id}` | Get assembly detail |
| `POST` | `/api/assemblies/{id}/feedback` | Submit assembly feedback |
| `GET` | `/api/flags` | List unreviewed flags |
| `PUT` | `/api/flags/{id}/review` | Review a flag |
| `GET` | `/api/stats` | Dashboard stats |

Full API reference at [docs.openlethe.ai](https://docs.openlethe.ai).

---

## Integrations

### OpenClaw Plugin

The Lethe plugin for OpenClaw handles bootstrap and context assembly automatically. It reads accepted project memory from `refs/shared/main`, creates an exact input manifest, and combines that view with the session summary and recent session events. Install the plugin and configure your endpoint:

```json
{
  "id": "lethe",
  "endpoint": "http://localhost:18483",
  "apiKey": "change-me-if-LETHE_API_KEY-is-set",
  "agentId": "archimedes",
  "projectId": "default",
  "autoLog": false
}
```

### Roll Your Own

Any agent that can make HTTP calls can use Lethe. The plugin protocol is just a session lifecycle + context assembly API. See the API reference for details.

---

## Architecture

```
cmd/lethe/main.go        - entry point, wires server + store + session manager
internal/api/            - HTTP handlers, Chi router, request/response types
internal/db/             - SQLite store, embedded migrations, all CRUD
internal/models/         - domain types (Session, Event, Checkpoint, Flag, Thread)
internal/session/        - session lifecycle state machine and manager
internal/ui/             — embedded UI (Go templates, HTMX)
```

## Memory Git and Future Direction

Memory Git V1 and its manifest-pinned context bridge are implemented locally. See [Memory Git V1](docs/memory-git-v1.md) and [Memory Git Context Bridge](docs/memory-context-bridge.md).

FTS, embeddings, hybrid retrieval, automatic semantic conflict resolution, and offline clone/pull/push remain future work. The current projector and selector are deterministic and intentionally avoid silently resolving substantive conflicts.


