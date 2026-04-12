---
name: lethe-memory
description: "Lethe — persistent memory layer for AI agents. Handles startup orientation, active memory queries, proactive recall, decision recording, and flag management. This is the agent's primary memory system — it supersedes all other memory files and scratch pads."
user-invocable: true
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["curl", "jq"]
      anyBins: ["docker"]
    notes:
      - "Docker is required to run the Lethe server container (ghcr.io/openlethe/lethe)"
---

# Lethe — Persistent Agent Memory

Lethe is your long-term memory. Every decision, observation, bug, discovery, and flag persists across sessions and survives container restarts. The plugin handles context assembly and auto-logging automatically. This skill handles orientation, proactive recall, recording discipline, and error recovery.

**Lethe is the single source of truth.** The Library (markdown files in `workspace/Library/`) is deprecated — derived snapshots are fine, but all knowledge lives in Lethe events first.

**Mental model:**
- Events are append-only. Nothing is ever deleted unless you explicitly delete it.
- Compaction synthesizes events into a narrative summary — it does not delete history.
- Memory search retrieves facts. Recording captures decisions. Flags surface uncertainty.
- Threads are persistent work items. Events attach to threads.

---

## What the Plugin Handles Automatically

You do **not** need to manually do these — the Lethe plugin handles them every turn:

| Hook | What it does |
|------|-------------|
| **`bootstrap()`** | On session start: fetches session summary, injects as system context |
| **`assemble()`** | Before every reply: fetches summary + up to 5 recent events, prepends to LLM context |
| **`ingest()`** | Passively logs user/assistant messages as Lethe events |
| **`afterTurn()`** | Auto-logs tool calls and open threads; handles heartbeat pings |
| **`compact()`** | Lethe-owned compaction (synthesizes events into summary) |

**What you still need to do manually:**
- Search memory when you need to recall something specific
- Record deliberate decisions with reasoning
- Log observations, bugs, discoveries, direction changes
- Flag uncertainties
- Track task status transitions
- Surface unresolved flags to the user

---

## Startup Sequence — Run First, Always

On every new session, orient yourself before answering the user.

> ⚠️ **Critical**: All Lethe API routes are under `/api/`. Base URL is `http://localhost:18483/api`.

**Step 1 — Get session state:**
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
```

> **SESSION_KEY fallback:** If `SESSION_KEY` is empty or the API returns "session not found", use the Discord channel session ID directly:
> `SESSION_KEY="86e09dc7-7ed9-4776-9b0e-d8c7678a0357"` (Archimedes Discord channel session)

**Step 2 — Check flags:**
```bash
curl -s "http://localhost:18483/api/flags"
```

**Step 3 — Orient:**
- What was in progress?
- What was decided?
- What is open?
- What does the human need?

**Then greet the human and ask what they need.**

---

## Proactive Recall — Use memory_search First

> Before answering questions about past decisions or prior context — **always use the `memory_search` tool**. Never invent. Never re-reason when the answer already exists.

**The `memory_search` tool** searches Lethe directly via its API — no embedding provider needed. It searches across all sessions by default, falls back to session-scoped search if needed.

**Parameters:**
- `query` (required) — search terms
- `limit` (optional, default 10) — max results
- `sessionKey` (optional) — override session for scoped search
- `eventType` (optional) — filter by type: `record`, `log`, `flag`, `task`

**Activation triggers:**
- User asks about past decisions, prior work, or previous conversations
- User says "remember", "did we", "were we", "what was"
- User references something from a previous session
- You are about to re-reason something you might already know
- You need context from a different session

**Search → Found:** Cite the specific event. "On March 24 I recorded: [content]."

**Search → Not found:** "I don't have that in memory yet." Do not invent.

**Fallback (if tool unavailable):**
```bash
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"
```

---

## The Logging Reflex — Log Immediately, Every Time

**The non-negotiable rule:** After every non-trivial action, log it. Not next session. Not when prompted. Immediately.

> If your response contains "done", "fixed", "deployed", "built", "changed" — and you haven't logged it yet — stop and log it first.

**The 30-second rule:** If the answer to "what were we working on?" takes more than 30 seconds to figure out, something should have been logged.

**Primary method — use the Lethe tools (preferred):**
- `memory.record` — deliberate decisions with reasoning
- `memory.log` — observations, events, notes
- `memory.flag` — uncertainties with confidence scores
- `memory.task` — task status transitions

**Fallback — use the CLI:**
```bash
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: use X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log log "Fixed: docker build failed"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This may not scale"
~/.openclaw/workspace/skills/lethe-memory/lethe-log task "Deploy v2" --status done
```

**Anti-patterns — these mean you failed to log:**
- ❌ "I think we were working on..." — should have been logged
- ❌ "Let me check what happened last session..." — startup should have already done this
- ❌ "I'm not sure what approach we decided on..." — the decision was not logged
- ❌ Re-doing something already decided — the decision was not logged

**Mandatory logging triggers:**

| Trigger | Type | Example |
|---------|------|---------|
| Task completed | `task` or `log` | "RSS heartbeat deployed — runs every 4h" |
| Decision made | `record` | "Decision: use X instead of Y because Z" |
| Bug or error fixed | `log` | "Fixed: docker build failed because strings import was missing" |
| Something built or deployed | `log` | "Built lethe:006, deployed to production" |
| Something discovered through research | `log` | "Discovery: modernc.org/sqlite requires Go 1.25+" |
| Direction changed mid-task | `log` | "Changed approach: abandoned X for Y after discovering Z" |
| Open thread created or resolved | `log` | "Thread resolved: decided on ingress-nginx over Traefik" |
| Mike says "remember" or "log this" | `log` | "Logged per Mike's request" |
| Flag raised | `flag` | "Flag: may need to revisit X if Y changes" |
| Flag resolved | `log` | "Flag resolved: chose X after testing Y and Z" |

**Good log entry:**
```
record: "Decision: use compose-goDownstream instead of kpt because Go template support is better for our use case. Mike approved."
log: "Bug fixed: docker build failed because strings import was missing. Added to ui.go imports."
flag: "Incomplete: server-14 UI changes tested but not verified in browser — Mike should confirm."
```

**Bad log entry:**
```
log: "Did some stuff."
record: "X approach is probably fine."
flag: "Not sure."
```

**The test:** If Mike asked "what did we just do?", could you answer from a Lethe query? If not, you should have logged it.

**Confidence scores:**
- 1.0 = Direct observation or explicit user instruction
- 0.9–0.95 = Near certain, minor uncertainty
- 0.7–0.85 = High confidence, plausible hypothesis
- 0.5–0.65 = Moderate, partial evidence — flag if consequential
- < 0.5 = Pure speculation — always flag

---

## When to Use Threads vs Logging

**Use a thread when:**
- The work spans multiple sessions
- There's an ongoing uncertainty that needs tracking
- You want to group related events together
- Mike asks "what's the status of X?" — threads answer this

**Use logging when:**
- Something is done and done
- A decision has been made and recorded
- A one-time discovery happened

**Create a thread manually:**
```bash
curl -s -X POST "http://localhost:18483/api/threads" \
  -H "Content-Type: application/json" \
  -d "{\"session_id\": \"$SESSION_KEY\", \"project_id\": \"default\", \"name\": \"auth-approach\", \"title\": \"Auth approach decision\"}"
```

---

## Compaction — When and How

Compaction synthesizes recent events into a narrative summary. It does not delete history.

**When to trigger:**
- Session has 100+ events without a summary
- The session summary is stale before a long conversation continues
- Before completing a significant work session

```bash
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"
```

**What compaction does:**
1. Reads all events since last compaction
2. Synthesizes them into a prose summary
3. Stores the summary linked to the session
4. Prunes very old raw events (compaction is lossy — the summary is kept)

**After compaction:** The session summary endpoint returns the new summary. `assemble()` will prepend it to the LLM prompt on next resume.

---

## Flags Queue

Flags are uncertainties that need human review. They persist across sessions.

**Raise a flag when:**
- Acting on incomplete information that could be consequential
- Multiple plausible paths and you're not sure which is right
- A risk exists that Mike should be aware of before continuing

**Check unresolved flags:**
```bash
curl -s "http://localhost:18483/api/flags"
```

**When a flag is resolved:**
1. Log the resolution: `memory.log: "Flag resolved: chose X over Y because Z"`
2. The flag remains in history but marked reviewed

---

## Error Handling

### Lethe server unreachable
```
Connection refused on port 18483
  → Try once more with 3-second timeout
  → Still fails: continue without memory access
  → Raise a flag: "Lethe server unreachable — memory operations suspended"
```

### Empty search results
```
Search returned no events
  → Try broader search terms
  → Try GET /api/sessions/${KEY}/events?limit=20
  → Still nothing: "I don't have that in memory yet."
  → Do not invent or guess
```

### Session not found
```
GET /api/sessions/${KEY} returns 404
  → Use the fallback SESSION_KEY (86e09dc7-7ed9-4776-9b0e-d8c7678a0357)
  → Or create new session: POST /api/sessions
```

### Flag surfaces from prior session
```
Flag from prior session still unresolved
  → Surface it to the user proactively
  → "Before we continue — there's an open flag from last session: [flag content]"
```

---

## Architecture — What Each Piece Does

| Component | Responsibility |
|-----------|----------------|
| **Plugin** (`mentholmike-lethe` extension) | Bootstrap, assemble, compact, ingest, afterTurn, auto-logging + native tools (memory.record, memory.log, memory.flag, memory.task, memory_search) |
| **Skill** (this file) | Agent guidance: orientation, recall, recording discipline, error recovery |
| **Server** (`lethe` binary) | SQLite-backed HTTP API on port 18483 |
| **UI** (`/ui/*`) | Dashboard, session detail, flags review board |

**Lethe is append-only.** Events are never overwritten — only compacted into summaries.

---

## Quick Reference

```bash
# Orient — get full session state
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"

# Check flags
curl -s "http://localhost:18483/api/flags"

# Search memory (curl fallback)
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"

# Recent events
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"

# CLI logging (fallback)
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This might not work"

# Compact session
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"

# UI dashboard
open http://localhost:18483/ui/
```

**Native tools (preferred over curl/CLI):**
- `memory.record` — Record a deliberate decision
- `memory.log` — Log an observation or event
- `memory.flag` — Flag uncertainty with confidence
- `memory.task` — Track task status transitions
- `memory_search` — Search Lethe events across sessions