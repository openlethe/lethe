---
name: lethe-memory
description: "Lethe — persistent memory layer for AI agents. Handles startup orientation, active memory queries, proactive recall, decision recording, and flag management. Use when: the user mentions memory, prior decisions, past work, open threads, flags, or anything that implies context from previous sessions. Triggers on: 'remember', 'did we decide', 'what were we working on', 'check memory', 'prior context', 'past decision', 'open threads', 'log this', 'record this', 'flag', 'forget', 'mnemosyne', or any question about history, preferences, or previous conclusions. This skill is the agent's primary memory system — it supersedes scratch pads and MEMORY.md files."
user-invocable: true
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["curl", "jq"]
      anyBins: ["docker"]
    notes:
      - "SESSION_KEY is injected by the OpenClaw Lethe plugin at runtime — do not set manually"
      - "LETHE_API is for future SaaS mode; leave unset for local Docker installations"
      - "Docker is required to run the Lethe server container (ghcr.io/openlethe/lethe)"
      - "lethe-log CLI (~/.openclaw/skills/lethe-memory/scripts/lethe-log) writes events to the local Lethe server"
---

# Lethe — Persistent Agent Memory

Lethe is your long-term memory layer. Every decision, observation, task, and flag persists across sessions and survives container restarts. The plugin handles bootstrap and context assembly automatically. This skill handles orientation, queries, recording, and error recovery.

**Mental model:**
- Events are append-only. Nothing is ever deleted unless you explicitly delete it.
- Compaction synthesizes events into a narrative summary — it does not delete history.
- Memory search retrieves facts. Recording captures decisions. Flags surface uncertainty.
- The plugin fires on `bootstrap()` and `assemble()` — you handle everything else.

---

## Startup Sequence — Run First, Always

On every new session (first real message from the user), orient yourself before answering.

> ⚠️ **Critical**: All Lethe API routes are under `/api/`. Base URL is `http://localhost:18483/api`. Never query `/sessions` directly — always use `/api/sessions`.

**Step 1 — Get full session state (single call):**
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
```

This returns:
- `summary` — compressed narrative from all prior compactions
- `recent_events` — last 20 events (fills gap since last compaction)
- `checkpoint_count`, `event_count`, `latest_checkpoint`
- `session.state` — `active`, `interrupted`, or `completed`

**Step 2 — Check flags:**
```bash
curl -s "http://localhost:18483/api/flags"
```

Unresolved flags from prior sessions are carry-forward items. Surface them.

**Step 3 — Orient:**
- What was in progress?
- What was decided?
- What is open?
- What does the human need?

**Then ask the human what they need.**

---

## Proactive Recall — Before Answering, Drink from Mnemosyne

> The river Lethe makes souls forget. The spring Mnemosyne preserves all memory.
> Before answering questions about past decisions or prior context — always check Lethe first.
> Never invent. Never re-reason when the answer already exists.

**Activation triggers — check memory before answering when:**
- User asks about past decisions, prior work, or previous conversations
- User says "remember", "did we", "were we", "what was"
- User references something from a previous session or days ago
- User's question implies knowledge that should exist in memory
- You are about to re-reason something you might already know

**The decision tree:**

```
User asks about prior context
    │
    ├─ "remember" / "did we" / "were we" / "what was"
    │   └─ Search: GET /api/events/search?q=<relevant terms>
    │
    ├─ "status of X" / "open threads"
    │   └─ Search: GET /api/events/search?q=X status
    │
    └─ General prior context question
        └─ GET /api/sessions/${KEY}/events?limit=10
```

**Search → Found:** Cite the specific event. "I recorded on March 24: [content]."

**Search → Not found:** "I don't have that in memory yet." Do not invent.

**Common search patterns:**

| Question | Search for |
|----------|-----------|
| "were we working on X?" | X + project/app keywords |
| "did we decide on Y?" | "decision" + Y |
| "what's the status of Z?" | Z + "status" + "open" |
| "what approach for W?" | W + "approach" + "decided" |
| "who was supposed to do V?" | V + "task" + "todo" |
| Preferences / tastes | "preference" + "like" + "dislike" |

**Anti-patterns:**
- ❌ "Based on our previous conversation..." — unless you've actually searched
- ❌ Re-reasoning a decision that exists in Lethe — retrieve and cite it
- ❌ Answering from incomplete fragments — say "let me check" and search
- ❌ Dropping flags silently — surface them to the user

---

## Memory Queries

### Search by keyword
```bash
curl -s "http://localhost:18483/api/events/search?q=token+budget&limit=10" | jq '.events[] | {event_type, content, created_at}'
```

### Search by keyword + tag
```bash
curl -s "http://localhost:18483/api/events/search?q=dashboard&tag=lethe&limit=10" | jq '.'
```

### Recent events for a session
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20" | jq '.events[] | {event_type, content, created_at}'
```

### Session snapshot (checkpoints)
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/checkpoints" | jq '.checkpoints[0].snapshot'
```

### Full session summary
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
```

---

## Recording — When and What

**Auto-recorded by the plugin (do not duplicate):**
- Tool calls after every real agent turn
- Session state transitions (active → interrupted → completed)

**You record manually via `lethe-log`:**

| Type | Use when | Example |
|------|----------|---------|
| `record` | Decision made, conclusion reached, direction chosen | "Decision: use X because Y" |
| `log` | Observation, discovery, API behavior, something worth noting | "API returned 429 — rate limited" |
| `flag` | Uncertainty that needs human review | "This approach may not scale" |
| `task` | Work unit created, updated, or resolved | "Deploy v2 — done" |

**Fastest path — use `lethe-log`:**
```bash
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: use X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log log "API returned 429 — rate limited"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This approach may not scale"
~/.openclaw/workspace/skills/lethe-memory/lethe-log task "Deploy v2" --status done
```

**Always include a confidence score:**

| Score | Meaning |
|-------|---------|
| 1.0 | Certain — direct observation or explicit user instruction |
| 0.9–0.95 | Near certain — strong reasoning, minor uncertainty |
| 0.7–0.85 | High confidence — plausible, well-reasoned hypothesis |
| 0.5–0.65 | Moderate — partial evidence, flag if consequential |
| < 0.5 | Pure speculation — always flag |

**Never record:**
- Casual chat with no lasting value
- Routine lookups or obvious confirmations
- Ephemeral brainstorming that shouldn't persist

---

## Flags Queue

Flags are uncertainties that need human review. They persist across sessions.

**Raise a flag when:**
- You are acting on incomplete information but need to proceed
- A consequential decision has multiple plausible paths and you're not sure which is right
- You see a risk that the human should be aware of before continuing
- Something broke in an unexpected way and the cause is uncertain

**Check unresolved flags:**
```bash
curl -s "http://localhost:18483/api/flags" | jq '.'
```

**When a flag is resolved:**
1. Record the resolution as a `log` event: "Flag resolved: [what was decided]"
2. The flag remains in history but is marked reviewed

**Flag → Record → Resolve flow:**
```
Uncertainty identified
  → raise flag (event_type: flag, confidence: 0.6)
  → human reviews in UI at /ui/flags
  → decision made
  → log: "Flag resolved: chose X over Y because Z"
```

---

## Session Lifecycle

### When to complete a session
- Task is done and the human has confirmed completion
- Conversation has a natural end point and no follow-up is expected
- You are going offline and the session should not resume

### When to interrupt (trigger checkpoint)
- Container is shutting down or restarting
- You have been idle for an extended period
- The human has asked you to pause and pick up later

### When to trigger compaction
- Session has accumulated many events (100+) without a summary
- You want to compress history before a long conversation continues
- The session summary is stale (new events since last compact)

```bash
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"
```

### Session state reference
| State | Meaning |
|-------|---------|
| `active` | Session is running, accepting events |
| `interrupted` | Session was paused (crash, restart, or manual) — resumable |
| `completed` | Session was explicitly closed — not resumable |

---

## Error Handling — Decision Trees

### Lethe server unreachable
```
Connection refused on port 18483
  → Try once more with 3-second timeout
  → Still fails: log "Lethe unreachable — continuing without memory"
  → Continue the conversation without memory access
  → Raise a flag: "Lethe server unreachable — memory operations suspended"
  → Do not crash, guess wildly, or tell the user "I have no memory"
```

### Empty search results
```
Search returned no events
  → Try broader search terms
  → Try GET /api/sessions/${KEY}/events?limit=20
  → Still nothing: "I don't have that in memory yet."
  → Do not invent or guess — say you don't know
```

### Token budget exceeded in assemble
```
Plugin returns token budget exceeded
  → Session needs compaction before resuming
  → Trigger: POST /api/sessions/${KEY}/compact
  → Retry assemble after compaction
  → If still failing: surface the issue to the human
```

### Session not found
```
GET /api/sessions/${KEY} returns 404
  → Session was completed or never existed
  → Create new session: POST /api/sessions
  → Inform the human: "I don't have memory of that session."
```

### Conflicting records
```
Two record events contradict each other
  → Do not choose — flag the conflict for human resolution
  → Log: "Flag: conflicting records about X — human review needed"
  → Present both options to the human and ask for a decision
```

### Flag surfaces unresolved across sessions
```
Flag from prior session still unresolved
  → Surface it to the user proactively
  → "Before we continue — there's an open flag from last session: [flag content]"
  → Do not ignore or drop flags silently
```

---

## Architecture — What Each Piece Does

| Component | Responsibility |
|-----------|----------------|
| **Plugin** (`lethe` extension) | Bootstrap, assemble, compact, heartbeat, event auto-logging |
| **Skill** (this file) | Agent-facing guidance: orientation, queries, recording, error recovery |
| **Server** (`lethe` binary) | SQLite-backed HTTP API on port 18483 |
| **UI** (`/ui/*`) | Dashboard, session detail, flags review board |

**Plugin handles:** storage, retrieval, automatic context injection, session lifecycle, crash recovery.

**Skill handles:** how the agent thinks about and uses memory — orientation, search, recording, flags, error recovery.

**The server is append-only.** Events are never overwritten — only compacted into summaries.

---

## Quick Reference

```bash
# Orient — get full session state
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"

# Check flags
curl -s "http://localhost:18483/api/flags"

# Search memory
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"

# Recent events
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"

# Record
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: X because Y"

# Flag uncertainty
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This might not work because Z"

# Compact session
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"

# UI dashboard
# http://localhost:18483/ui/
```
