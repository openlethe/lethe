# Lethe — Persistent Memory for AI Agents

**"AI that remembers what you decided."**

Every AI agent starts from scratch. Same question, same reasoning, same dead ends — every single session. Lethe breaks that loop.

Lethe is a persistent memory layer for AI agents. It records reasoning chains, decisions, observations, flagged uncertainty, and task history across sessions — so your agent carries context forward instead of rebuilding it from nothing.

---

## What It Records

| Type | Use Case |
|------|----------|
| `record` | A decision made, a direction chosen, a conclusion reached |
| `log` | An observation, a discovery, something worth noting |
| `flag` | Uncertainty that needs human review before proceeding |
| `task` | A work unit with a status chain: todo → in_progress → done |

Records carry confidence scores (0.0–1.0). Flags persist across sessions until explicitly reviewed. Task events chain together so you can trace the history of any piece of work.

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

1. **Sessions** — Each agent session gets a stable key. State: `active`, `interrupted`, or `completed`.
2. **Events** — Every meaningful agent action is logged: decisions, observations, flags, tasks.
3. **Checkpoints** — Periodic snapshots so the agent can resume exactly where it left off.
4. **Compaction** — When a session grows long, events are synthesized into a narrative summary, preserving the full history in compressed form.
5. **Threads** — Flags and uncertainty can be grouped into named threads for tracking open questions across sessions.

On session resume, the plugin assembles context: session summary + recent events, prepended to the LLM prompt. The agent picks up exactly where it left off.

---

## What Makes It Different

Most agent memory is just conversation storage. Lethe is different:

- **Structured, not soup** — Records, logs, flags, and tasks have meaning. It's a memory schema, not a chatlog.
- **Confidence scoring** — Every record carries a confidence score so the agent can calibrate how much to trust prior conclusions.
- **Flag system** — Uncertainty gets surfaced and tracked, not buried in context.
- **Crash recovery** — Interrupted sessions resume from the last checkpoint, not from zero.
- **No external dependencies** — SQLite only. No API keys, no embedding models, no vector search to tune.
- **Lightweight** — A single Docker container. Drop it into any agent stack.

---

## Quick Start

### Docker

```bash
docker run -d \
  --name lethe \
  -v lethe-data:/data \
  -p 18483:18483 \
  ghcr.io/openlethe/lethe:latest
```

### From Source

```bash
go build ./cmd/lethe
./lethe --db ./lethe.db --http :18483
```

The server starts on port 18483 with a built-in UI at `http://localhost:18483/ui/`.

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
| `GET` | `/api/flags` | List unreviewed flags |
| `PUT` | `/api/flags/{id}/review` | Review a flag |
| `GET` | `/api/stats` | Dashboard stats |

Full API reference at [docs.openlethe.ai](https://docs.openlethe.ai).

---

## Integrations

### OpenClaw Plugin

The Lethe plugin for OpenClaw handles bootstrap and context assembly automatically. Install the plugin and configure your endpoint:

```json
{
  "id": "lethe",
  "endpoint": "http://localhost:18483",
  "agentId": "archimedes",
  "projectId": "default"
}
```

### Roll Your Own

Any agent that can make HTTP calls can use Lethe. The plugin protocol is just a session lifecycle + context assembly API. See the API reference for details.

---

## Architecture

```
cmd/lethe/main.go        — entry point, wires server + store + session manager
internal/api/            — HTTP handlers, Chi router, request/response types
internal/db/             — SQLite store, embedded migrations, all CRUD
internal/models/         — domain types (Session, Event, Checkpoint, Flag, Thread)
internal/session/        — session lifecycle state machine and manager
internal/ui/             — embedded UI (Go templates, HTMX)
```


