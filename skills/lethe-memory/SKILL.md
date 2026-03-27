---
name: lethe-memory
description: "Lethe — one-stop persistent memory for AI agents. Handles startup orientation, active memory queries, and event recording. This skill is the agent's primary memory system. It supersedes any external MEMORY.md or per-session scratch pads. Use this for: startup orientation, memory queries, recording decisions, and flag management."
user-invocable: true
metadata: { "openclaw": { "emoji": "🧠", "requires": { "bins": ["curl", "jq"] } } }
---

# lethe-memory — Persistent Agent Memory (One-Stop Shop)

Lethe is your long-term memory layer and primary orientation system. Every decision, observation, task, and flag persists across sessions. **This skill replaces MEMORY.md as your source of truth.**

---

## Startup Sequence — Run First, Always

> ⚠️ **Critical**: All Lethe API routes are under `/api/`. Never query `/sessions` directly — always use `/api/sessions`, `/api/sessions/{key}/summary`, etc. The base URL is `http://localhost:18483/api`. Missing `/api` is the most common startup mistake.

On every new session (first real message from the user), orient yourself using Lethe.

**Read this skill first** (`~/.openclaw/workspace/skills/lethe-memory/SKILL.md`) for the full orientation workflow. The abbreviated version:

1. Check for active/interrupted sessions: `GET /api/sessions?limit=5`
2. **Single call for full state** — `GET /api/sessions/{key}/summary` returns:
   - `summary` — compressed narrative context
   - `recent_events` — last 20 events (fills any gap between last compaction and now)
   - `checkpoint_count`, `event_count`, `latest_checkpoint`
   Use this as your primary orientation call on every startup.

2. **Optional tail fetch** — only if you need the *very* latest 1-2 events that may not yet be reflected in `recent_events`:
   - `GET /api/sessions/{key}/events?limit=5` — last 5 events, newest first
   This is rarely needed but useful after a long gap between sessions.
3. Check unresolved flags: `GET /api/flags`
4. Orient: what was in progress? what's open? what does the human need?

**Then ask the human what they need.**

---

## Memory Queries — "What was I working on?"

Use Lethe as your active memory during work:

### Recent activity
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20" | jq '.events[] | {event_type, content, created_at}'
```

### Search past events
```bash
# Search by keyword (case-insensitive)
curl -s "http://localhost:18483/api/events/search?q=token+budget&limit=10" | jq '.events[] | {event_type, content, tags}'

# Search by keyword + tag
curl -s "http://localhost:18483/api/events/search?q=dashboard&tag=lethe&limit=10" | jq '.'
```

### Open threads and tasks
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/checkpoints" | jq '.checkpoints[0].snapshot'
```

---

## Before Answering — Drink from Mnemosyne

> The spring Mnemosyne preserves memory; the river Lethe makes souls forget.
> Before answering history questions, check Lethe first. Never invent.

**Trigger:** User asks about past decisions, prior work, preferences, or anything that implies prior context.

**Action:**
1. Search: `GET /api/events/search?q=<relevant terms>`
2. Found → cite it. Not found → say "I don't have that in memory yet."

**Anti-patterns:**
- Don't answer from incomplete memory — search first
- Don't re-reason a decision already recorded — retrieve and cite
- Don't claim prior context without checking

**Common searches:**

| Question | Search for |
|----------|-----------|
| "what were we doing?" | topic/project keywords |
| "did we decide on X?" | "decision" + X |
| "status of Y?" | Y + "status" |
| "approach for Z?" | Z + "decided" |

---

## Recording — When and What

**Auto-recorded by plugin (do not duplicate):**
- Tool calls used (after every real turn)
- Open threads detected (##, TODO, [ ] markers)

**You record manually via lethe-log:**

| Type | When to call |
|------|-------------|
| `record` | Decision made, conclusion reached, direction chosen |
| `log` | Observation, discovery, API behavior noted |
| `flag` | Uncertainty that needs human review later |
| `task` | Work unit created, updated, or blocked |

```bash
# Fastest: use lethe-log (auto-detects active session)
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: use X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log log "API returned 429 — rate limited"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This approach may not scale"
~/.openclaw/workspace/skills/lethe-memory/lethe-log task "Deploy v2" --status done
```

**Never record:**
- Casual chat with no lasting value
- Routine lookups or obvious confirmations
- Content that should stay ephemeral (draft thoughts, brainstorming)

---

## Confidence Scale

| Score | Meaning |
|-------|---------|
| 1.0 | Direct observation or explicit instruction from user |
| 0.9–0.95 | Strong reasoning, minor uncertainty |
| 0.7–0.85 | Plausible, well-reasoned hypothesis |
| 0.5–0.65 | Partial evidence — flag if consequential |
| < 0.5 | Pure speculation — always flag |

---

## Flags Queue

Flags are uncertainties that need human review. Check and resolve them:

```bash
# View unresolved flags
curl -s "http://localhost:18483/api/flags" | jq .

# Mark reviewed
curl -s -X PUT "http://localhost:8080/api/flags/${EVENT_ID}/review"
```

When a flag is resolved: record the resolution as a `log` event, then mark reviewed.

---

## API Reference

> ⚠️ **Base URL**: `http://localhost:18483/api` — all routes are under `/api/`. Example: `/api/sessions`, not `/sessions`.

| Endpoint | Method | Purpose |
|---------|--------|---------|
| `/api/sessions` | GET | List sessions |
| `/api/sessions/{key}` | GET | Get session by key |
| `/api/sessions/{key}/summary` | GET | Get compressed summary + recent events |
| `/api/sessions/{key}/events` | POST/GET | Create/list events |
| `/api/sessions/{key}/checkpoints` | POST/GET | Create/list checkpoints |
| `/api/sessions/{key}/compact` | POST | Trigger compaction |
| `/api/sessions/{key}/heartbeat` | POST | Token budget heartbeat |
| `/api/sessions/{key}/resume` | POST | Resume interrupted session |
| `/api/sessions/{key}/complete` | POST | Dispose session |
| `/api/flags` | GET | List unreviewed flags |
| `/api/flags/{id}/review` | PUT | Mark flag reviewed |
| `/api/stats` | GET | System stats |
| `/api/events/search` | GET | Search events by keyword and/or tag |

Base URL: `http://localhost:18483/api`

---

## IMPORTANT: Parallel Systems — Avoid

**Do NOT maintain a separate `memory/YYYY-MM-DD.md` scratch pad alongside Lethe.** Lethe IS your memory. The skill `lethe-memory` gives you everything you need for orientation, queries, and recording. Do not create parallel scratch files.

**What MEMORY.md files are for:** Only the workspace's `MEMORY.md` (if it exists) should be read for truly static information that never changes — credential file paths, constant endpoint URLs. Everything else lives in Lethe.

**The only valid use of `memory/` directory:** Periodic flushes to disk for git persistence (optional, Lethe DB is the source of truth). The `memory/` directory is gitignored and local-only.

**You have everything you need in this skill:**
- Startup orientation → Steps 1-4 above
- Active memory queries → "Memory Queries" section
- Recording → `lethe-log` commands
- Flags → `/api/flags` endpoints
- Session detail → `http://localhost:8080/ui/` dashboard

**Do not create `context/memory/` files during normal work.** If Lethe is running and recording, you do not need a parallel scratch pad.

---

## Architecture

- **Plugin** (`lethe` extension): handles storage, retrieval, and auto-logging
- **Skill** (this file): handles how to use Lethe at startup and during work
- **Server** (`lethe` binary): SQLite-backed API server on port 18483
- **UI** (`/ui/*`): dashboard, session detail, flags at `http://localhost:18483/ui/`

The plugin handles `bootstrap()` and `assemble()` for automatic context injection.
This skill handles agent-facing guidance for orientation and active memory queries.
