---
name: lethe-memory
description: Persist decisions, observations, tasks, and flags to Lethe — a long-term memory layer for AI agents. Use when: making a non-trivial decision, discovering something new, completing a task, encountering an error and how you fixed it, raising an uncertainty flag for human review, or asked to remember something. Triggers on: "remember", "log this", "record this", "don't forget", task completions, error recoveries, design decisions, API discoveries, or any session- spanning context that should survive restarts.
user-invocable: true
metadata: { "openclaw": { "emoji": "🧠", "requires": { "bins": ["curl"] }, "primaryEnv": "LETHE_API" } }
---

# lethe-memory — Persistent Memory for AI Agents

Lethe is a session-spanning memory layer. Every record, observation, flag, and task persists across restarts and compactions.

## When to Record

**Always record:**
- Design decisions and the reasoning behind them
- Error recoveries — what broke, how you fixed it
- First-time discoveries about an API, tool, or system
- Task completions with outcome summary
- User corrections or clarifications ("actually, prefer X over Y")
- Anything the user asks to remember
- External actions (API calls, deploys, file changes)
- Flags — uncertainty that needs human review

**Never bother recording:**
- Casual chat with no lasting value
- Routine lookups or simple confirmations
- Obvious fixes with no new reasoning
- Simple Q&A

## Recording Types

| Type | Use when |
|------|----------|
| `record` | A decision was made or conclusion reached |
| `log` | An observation or finding from research/probing |
| `flag` | Uncertainty that needs human review |
| `task` | A unit of work exists (create as `todo`, update to `done` or `blocked`) |

## Lethe API

Base URL: `http://localhost:8080`

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/sessions` | POST | Create or get current session |
| `/api/sessions/{id}/events` | POST | Record an event |
| `/api/sessions/{id}/events` | GET | List events |
| `/api/sessions/{id}/summary` | GET | Get session summary |
| `/api/sessions/{id}/checkpoints` | POST | Create a checkpoint |
| `/api/sessions/{id}/compact` | POST | Trigger compaction |
| `/api/sessions/{id}/complete` | POST | Close session with summary |
| `/api/flags` | GET | List unreviewed flags |

## Session Lifecycle

Start a session at the beginning of every conversation:

```bash
curl -s -X POST http://localhost:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"session_key": "agent:project:channel", "agent_id": "archimedes", "project_id": "archimedes"}'
```

Keep it alive with heartbeats. Close it when done.

## Quick Record (low friction)

For trivial records, skip the session ceremony. Just POST to the active session:

```bash
# Get active session ID from LETHE_SESSION_ID env, then:
curl -s -X POST "http://localhost:8080/api/sessions/$LET_HE_SESSION_ID/events" \
  -H "Content-Type: application/json" \
  -d '{"event_type": "record", "content": "Decision: use X instead of Y because Z", "confidence": 0.9, "tags": ["decision"]}'
```

## The lethe-log Script

For maximum convenience, use the bundled script:

```bash
# Record a decision
lethe-log record "Fixed routing shadowing by pre-fetching data"

# Record an error recovery
lethe-log log "API returned 429 — added exponential backoff"

# Flag uncertainty
lethe-log flag "This approach works but may not scale"

# Create a task
lethe-log task "Push Lethe UI changes to GitHub" --status todo

# Update a task
lethe-log task "Push Lethe UI changes to GitHub" --status done
```

See `scripts/lethe-log` for full usage.

## Confidence Scale

Always include a confidence score (0.0–1.0):

| Score | Meaning |
|-------|---------|
| 1.0 | Certain — direct observation or explicit instruction |
| 0.9–0.95 | Near certain — strong reasoning, minor uncertainty |
| 0.7–0.85 | High confidence — plausible, well-reasoned |
| 0.5–0.65 | Moderate — hypothesis or partial evidence |
| < 0.5 | Low — speculation, flag for review |

## Flags Queue

When uncertain about something consequential, flag it:

```json
{
  "event_type": "flag",
  "content": "This approach works but may not scale past 100 concurrent users",
  "confidence": 0.6,
  "tags": ["architecture", "scalability"]
}
```

Flags appear in the Lethe dashboard at `/ui/flags` for human review.

## Checkpoints

Create a checkpoint after meaningful units of work:

```bash
curl -s -X POST "http://localhost:8080/api/sessions/$LET_HE_SESSION_ID/checkpoints" \
  -H "Content-Type: application/json" \
  -d '{"snapshot": {"current_task": "Deploy v2 API", "last_tool": "exec", "open_threads": ["auth-refactor"]}}'
```

Checkpoints are synthesized into the session summary during compaction — they don't need to be detailed.
