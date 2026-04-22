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
      - "Lethe server runs LOCALLY ONLY — default http://localhost:18483. Do NOT expose externally."
      - "Docker is required to run the Lethe server container (ghcr.io/openlethe/lethe)"
      - "lethe-log CLI is in the skill directory, NOT a scripts/ subdirectory"
      - "LETHE_API is reserved for a future optional SaaS mode — leave unset for local installations. If set, data leaves your machine."
---

# Lethe — Persistent Agent Memory

Lethe is your long-term memory. Every decision, observation, bug, discovery, and flag persists across sessions and survives container restarts. The plugin handles context assembly automatically. This skill handles orientation, queries, recording, and error recovery.

**Lethe is the single source of truth.** The Library (markdown files in `workspace/Library/`) is deprecated — derived snapshots are fine, but all knowledge lives in Lethe events first.

**Mental model:**
- Events are append-only. Nothing is ever deleted unless you explicitly delete it.
- Compaction synthesizes events into a narrative summary — it does not delete history.
- Memory search retrieves facts. Recording captures decisions. Flags surface uncertainty.
- Threads are persistent work items. Events attach to threads.

---

## Startup Sequence — Run First, Always

On every new session, orient yourself before answering the user.

> ⚠️ **Critical**: All Lethe API routes are under `/api/`. Base URL is `http://localhost:18483/api`.

**Step 1 — Get session state:**
```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
```

> **SESSION_KEY handling:** The Lethe plugin injects `SESSION_KEY` automatically. If the variable is empty or the API returns "session not found", create a new session:
> ```bash
> curl -s -X POST "http://localhost:18483/api/sessions" \
>   -H "Content-Type: application/json" \
>   -d '{"agent_id": "archimedes", "project_id": "default"}'
> ```
> Use the returned `session_id` as your `SESSION_KEY`. Do not hardcode session IDs — they are session-specific and mixing them conflates unrelated conversations.

**Step 2 — Check flags:**
```bash
curl -s "http://localhost:18483/api/flags"
```

**Step 3 — Orient:**
- What was in progress?
- What was decided?
- What is open?
- What does the human need?

**Then ask the human what they need.**

---

## Proactive Recall — Check Before Re-reasoning

> Before answering questions about past decisions or prior context — always check Lethe first. Never invent. Never re-reason when the answer already exists.

**Activation triggers:**
- User asks about past decisions, prior work, or previous conversations
- User says "remember", "did we", "were we", "what was"
- User references something from a previous session
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

**Search → Found:** Cite the specific event. "On March 24 I recorded: [content]."

**Search → Not found:** "I don't have that in memory yet." Do not invent.

---

## The Logging Reflex — Log Immediately, Every Time

**The non-negotiable rule:** After every non-trivial action, log it. Not next session. Not when prompted. Immediately.

> If your response contains "done", "fixed", "deployed", "built", "changed" — and you haven't logged it yet — stop and log it first.

**The 30-second rule:** If the answer to "what were we working on?" takes more than 30 seconds to figure out, something should have been logged.

**Anti-patterns — these mean you failed to log:**
- ❌ "I think we were working on..." — should have been logged
- ❌ "Let me check what happened last session..." — startup should have already done this
- ❌ "I'm not sure what approach we decided on..." — the decision was not logged
- ❌ Re-doing something already decided — the decision was not logged

**Fastest path — use `lethe-log`:**
```bash
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: use X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log log "Fixed: docker build failed because strings import was missing"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This approach may not scale if user count grows"
~/.openclaw/workspace/skills/lethe-memory/lethe-log task "Deploy v2" --status done
```

Or via direct API:
```bash
curl -s -X POST "http://localhost:18483/api/events" \
  -H "Content-Type: application/json" \
  -d "{\"session_id\": \"$SESSION_KEY\", \"project_id\": \"default\", \"event_type\": \"log\", \"content\": \"...\", \"tags\": [\"tag1\"], \"confidence\": 0.95}"
```

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
log: "Discovery: modernc.org/sqlite v1.47 requires Go 1.25+, can't build locally. Must use Docker."
flag: "Incomplete: server-14 UI changes tested but not verified in browser — Mike should confirm."
```

**Bad log entry:**
```
log: "Did some stuff."
record: "X approach is probably fine."
flag: "Not sure."
task: "done"
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

**The auto-thread rule:** When you raise a flag and no thread_id is provided, the server should auto-create a thread. If it doesn't, create one manually:

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
4. Prunes very old raw events ( compaction is lossy — the summary is kept)

**After compaction:** The session summary endpoint returns the new summary. assemble() will prepend it to the LLM prompt on next resume.

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
1. Log the resolution: `log: "Flag resolved: chose X over Y because Z"`
2. The flag remains in history but marked reviewed

---

## Error Handling

### Lethe server unreachable
```
Connection refused on port 18483
  → Try once more with 3-second timeout
  → Still fails: log "Lethe unreachable — continuing without memory"
  → Continue without memory access
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
  → Session was completed or never existed
  → Create a new session: POST /api/sessions with agent_id and project_id
  → Use the returned session_id as SESSION_KEY going forward
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
| **Plugin** (`lethe` extension) | Bootstrap, assemble, compact, event auto-logging |
| **Skill** (this file) | Agent guidance: orientation, queries, recording, error recovery |
| **Server** (`lethe` binary) | SQLite-backed HTTP API on port 18483 |
| **UI** (`/ui/*`) | Dashboard, session detail, flags review board |

**Lethe is append-only.** Events are never overwritten — only compacted into summaries.

---

## Deployment & Trust Model

- **Server runs locally only.** The Lethe server binds to `localhost:18483` — it does not expose an external port. Data never leaves the host machine.
- **Container image:** `ghcr.io/openlethe/lethe` — open-source, auditable. Source at `github.com/openlethe/lethe`.
- **No telemetry.** The server has no outbound network calls. It accepts inbound API requests and reads/writes a local SQLite file.
- **LETHE_API (SaaS):** Reserved for a future optional cloud mode. If set, event data is sent to an external endpoint. **Do not set this variable unless you explicitly opt in and trust the external service's data policies.**

---

## Data Retention & Privacy

- **Storage:** All data lives in a single SQLite file on the host filesystem. Default: `~/docker/lethe/lethe-data/lethe.db`.
- **Retention:** Events persist indefinitely until manually deleted or compacted. Compaction synthesizes old events into summaries and prunes raw events older than the compaction window.
- **Encryption:** The SQLite file is not encrypted at rest. Protect the host filesystem. If encryption is required, use filesystem-level encryption (FileVault, LUKS, etc.).
- **Access control:** The API has no built-in authentication for local mode. It relies on localhost binding for security. Do not expose port 18483 beyond localhost.
- **Sensitive data:** The agent logs actions, decisions, and observations. It does NOT log API keys, credentials, or secrets. If sensitive data is inadvertently logged, delete the event via the API or compact the session.
- **Backups:** Daily hot-backups via `sqlite3 .backup`, gzip compressed, 7-day retention. Backup directory is local only.

---

## Quick Reference

```bash
# Orient — get full session state
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"

# Orient (if SESSION_KEY missing, create new session)
# curl -s -X POST "http://localhost:18483/api/sessions" \
#   -H "Content-Type: application/json" \
#   -d '{"agent_id": "archimedes", "project_id": "default"}'
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"

# Check flags
curl -s "http://localhost:18483/api/flags"

# Search memory
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"

# Recent events
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"

# Record / log / flag (fastest)
~/.openclaw/workspace/skills/lethe-memory/lethe-log record "Decision: X because Y"
~/.openclaw/workspace/skills/lethe-memory/lethe-log flag "This might not work because Z"

# Compact session
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"

# UI dashboard
open http://localhost:18483/ui/
```
