---
name: lethe-memory
description: "Persistent memory for AI agents: startup, recall, recording, flags, threads, compaction, assembly feedback."
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["curl", "jq"]
---

# Lethe Memory

Lethe is the primary long-term memory source. Prefer it over memory files, scratch pads, and the Library for prior decisions, open work, flags, and user/project context.

The OpenClaw plugin handles bootstrap, context assembly, and automatic session events. This skill covers explicit orientation, search, recording, flags, threads, compaction, recovery, and assembly feedback.

## What It Records

| Type | Use | Example |
|------|-----|---------|
| `record` | Decision, conclusion, commitment | "Decision: use SQLite WAL mode" |
| `log` | Fix, discovery, status update | "Fixed: race in compaction worker" |
| `flag` | Unresolved risk or uncertainty | "Risk: assembly retention may drop data" |
| `task` | Durable work item state | "task: Deploy v0.4 — status: done" |
| `thread` | Cross-session topic or open question | "thread: Kubernetes migration approach" |

Events carry confidence scores (0.0-1.0). Flags persist across sessions until resolved. Task events chain together so you can trace the history of any work item.

## Use When

- User asks about prior work, decisions, or status
- User says "remember", "did we", "what was", "last time"
- Completing non-trivial work that should persist
- Making or changing a decision
- Fixing a bug or deployment issue
- Raising or resolving uncertainty
- Closing out significant work (compact)
- User explicitly asks you to remember, log, record, or flag something

## Contract

- **Prefer Lethe first** — search Lethe before answering questions about prior work, decisions, or context
- **Orient at startup** — check session summary and flags before the first substantive reply
- **Record immediately** — log decisions, fixes, and flags right after they happen, not at session end
- **Cite plainly** — if search finds the answer, cite the recorded event; if not, say it is not in memory
- **Never invent** — do not fabricate prior context; broaden search once, then admit gaps
- **One active goal** — OpenClaw goals track the current session's work; use Lethe threads for cross-session topics
- **Protect secrets** — do not record credentials, API keys, or sensitive personal data
- **Local only** — leave `LETHE_API` unset unless the user explicitly opts into remote storage

## Startup

On the first real user message of a session, orient before answering:

```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
curl -s "http://localhost:18483/api/flags"
```

Use the results to answer:

- What was in progress?
- What decisions or flags carry forward?
- What does the user need now?

`SESSION_KEY` is injected by the plugin. If empty or 404, create a new session:

```bash
curl -s -X POST "http://localhost:18483/api/sessions" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "archimedes", "project_id": "default"}'
```

Never hardcode session IDs or query routes without `/api`.

## Recall

Search Lethe before answering when the user asks about prior work, decisions, preferences, status, open threads, or says things like "remember", "did we", "what was", "last time", or "log this".

```bash
# Search all project events by content
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10" | jq '.events[] | {event_type, content, created_at}'

# Search within a specific event type
curl -s "http://localhost:18483/api/events/search?eventType=flag&limit=20"

# Session events
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"

# Thread events
curl -s "http://localhost:18483/api/threads/${THREAD_ID}/events"
```

If search finds the answer, cite the recorded event plainly. If not, broaden once; after that say it is not in memory. Do not invent prior context.

## Record

Use `lethe-log` for durable events:

```bash
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: use X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log log "Fixed: X failed because Y; changed Z"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "Risk: X may fail if Y changes"
~/.openclaw/workspace/skills/lethe-memory/lethe-log task "Deploy v2" --status done
```

Record immediately after:

- Completing non-trivial work
- Making or changing a decision
- Fixing a bug or deployment issue
- Discovering reusable technical facts
- Creating, updating, or resolving durable tasks
- Raising or resolving uncertainty
- The user explicitly asks you to remember, log, record, or flag something

Use event types this way:

- `record`: decisions, conclusions, commitments
- `log`: fixes, discoveries, status updates, completed work
- `flag`: unresolved risk or uncertainty needing human review
- `task`: durable work item state

Do not record casual chat, secrets, raw credentials, or routine facts with no future value. If sensitive data is accidentally logged, delete it through the API or UI.

## Flags

Check unresolved flags at startup and before continuing old work:

```bash
curl -s "http://localhost:18483/api/flags" | jq '.'
```

Surface relevant unresolved flags to the user. When a flag is resolved, log the resolution; the original flag remains historical.

## Threads vs Goals

**Goals** (OpenClaw native) are session-scoped: "What am I doing right now in this session?" They track the current task, have a measurable objective, and are marked complete when done. One active goal per session.

**Threads** (Lethe) are cross-session topics: "What open question persists across multiple sessions?" They group related events, flags, and records under a named topic that outlives any single session.

Use threads when:

- A topic spans multiple sessions (e.g., "Kubernetes migration approach")
- You want to track the history of an open question over time
- Related flags and records should be grouped together for later review
- The topic is not a single session's goal but a persistent theme

Create a thread:

```bash
curl -s -X POST "http://localhost:18483/api/threads" \
  -H "Content-Type: application/json" \
  -d '{"name": "auth-design", "title": "Auth approach decision", "project_id": "default"}'
```

Attach events to a thread by including `thread_id` when creating events, or use the `lethe-log` helper with `--thread`:

```bash
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "Need to decide: JWT vs session tokens" --thread auth-design
```

List and review threads:

```bash
curl -s "http://localhost:18483/api/threads" | jq '.threads[] | {thread_id, name, status, title}'
curl -s "http://localhost:18483/api/threads/${THREAD_ID}" | jq '.'
```

Update thread status:

```bash
curl -s -X PATCH "http://localhost:18483/api/threads/${THREAD_ID}" \
  -H "Content-Type: application/json" \
  -d '{"status": "resolved"}'
```

## Compaction

Compact when a session has many events, the summary is stale before a long continuation, or you are closing out significant work:

```bash
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"
```

Compaction writes a narrative summary and may prune old raw events. It does not erase the summary; the plugin prepends it on later resumes.

## Assembly Feedback (v0.4+)

After each turn, the plugin records what context was assembled (summary snapshot + event references). You can review assemblies and submit feedback:

```bash
# List assemblies for a session
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/assemblies" | jq '.assemblies[] | {assembly_id, created_at, total_estimated_tokens}'

# Get assembly detail with ordered items
curl -s "http://localhost:18483/api/assemblies/${ASSEMBLY_ID}" | jq '.'

# Submit feedback: good, stale_included, missing_memory, too_large, irrelevant, other
curl -s -X POST "http://localhost:18483/api/assemblies/${ASSEMBLY_ID}/feedback" \
  -H "Content-Type: application/json" \
  -d '{"verdict": "good", "note": "Correct context"}'
```

Assemblies are also visible in the UI under the **Assemblies** tab on any session detail page. Feedback is additive and never mutates memory — it is diagnostic data for understanding context correctness.

## Recovery

- Server unreachable: retry once with `curl --max-time 3`; if still down, continue without memory and flag/log when Lethe is reachable again.
- Empty search: broaden once, then say the fact is not in memory.
- Session not found: create a new session; do not reuse another session ID.
- Conflicting records: present both and ask for a decision instead of choosing silently.
- Remote storage: leave `LETHE_API` unset unless the user explicitly asks; setting it can send memory data off-host.

## UI Quick Reference

```bash
open http://localhost:18483/ui/dashboard
```

| Page | Path | What It Shows |
|------|------|---------------|
| Dashboard | `/ui/dashboard` | Sessions, recent activity, open threads |
| All Memories | `/ui/events` | All events across sessions, filterable by type |
| Live | `/ui/live` | Real-time events from active sessions (SSE) |
| Session Detail | `/ui/sessions/{id}` | Events, checkpoints, and assemblies tabs |
| Assembly Detail | `/ui/assembly/{id}` | Ordered items, snapshots, feedback buttons |
| Threads | `/ui/threads` | Cross-session topic tracking |
| Flags | `/ui/flags` | Unresolved flags needing review |

## API Quick Reference

```bash
# Session
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"

# Search
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"
curl -s "http://localhost:18483/api/events/search?eventType=flag&limit=20"
curl -s "http://localhost:18483/api/events/search?projectId=default&limit=20"

# Flags
curl -s "http://localhost:18483/api/flags"

# Threads
curl -s "http://localhost:18483/api/threads"
curl -s "http://localhost:18483/api/threads/${THREAD_ID}"
curl -s "http://localhost:18483/api/threads/${THREAD_ID}/events"

# Assemblies
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/assemblies"
curl -s "http://localhost:18483/api/assemblies/${ASSEMBLY_ID}"
curl -s -X POST "http://localhost:18483/api/assemblies/${ASSEMBLY_ID}/feedback" \
  -H "Content-Type: application/json" \
  -d '{"verdict": "good"}'

# UI
curl -sL "http://localhost:18483/ui/dashboard"
curl -sL "http://localhost:18483/ui/sessions/${SESSION_ID}"
```
