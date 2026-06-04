---
name: lethe-memory
description: "Use Lethe persistent memory for startup orientation, recall, logging, flags, and session compaction."
user-invocable: true
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["curl", "jq"]
      anyBins: ["docker"]
    notes:
      - "Local API base is http://localhost:18483/api; every route includes /api."
      - "Server is local-only. Do not expose port 18483."
      - "Do not set LETHE_API unless the user explicitly opts into remote storage."
      - "lethe-log lives in this skill directory."
---

# Lethe Memory

Lethe is the primary long-term memory source. Prefer it over memory files, scratch pads, and the Library for prior decisions, open work, flags, and user/project context.

The plugin handles bootstrap, context assembly, and automatic session events. This skill covers explicit orientation, search, recording, flags, compaction, and recovery.

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

`SESSION_KEY` should be injected by the plugin. If it is empty or returns 404, create a new session and use the returned `session_id` only for the current session:

```bash
curl -s -X POST "http://localhost:18483/api/sessions" \
  -H "Content-Type: application/json" \
  -d '{"agent_id": "archimedes", "project_id": "default"}'
```

Never hardcode session IDs or query routes without `/api`.

## Recall

Search Lethe before answering when the user asks about prior work, decisions, preferences, status, open threads, or says things like "remember", "did we", "what was", "last time", or "log this".

```bash
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10" | jq '.events[] | {event_type, content, created_at}'
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/events?limit=20"
```

If search finds the answer, cite the recorded event plainly. If not, broaden the search once; after that say it is not in memory. Do not invent prior context.

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

## Compaction

Compact when a session has many events, the summary is stale before a long continuation, or you are closing out significant work:

```bash
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"
```

Compaction writes a narrative summary and may prune old raw events. It does not erase the summary; the plugin prepends it on later resumes.

## Recovery

- Server unreachable: retry once with `curl --max-time 3`; if still down, continue without memory and flag/log when Lethe is reachable again.
- Empty search: broaden once, then say the fact is not in memory.
- Session not found: create a new session; do not reuse another session ID.
- Conflicting records: present both and ask for a decision instead of choosing silently.
- Remote storage: leave `LETHE_API` unset unless the user explicitly asks; setting it can send memory data off-host.

## Quick Reference

```bash
curl -s "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"
curl -s "http://localhost:18483/api/flags"
curl -s "http://localhost:18483/api/events/search?q=<terms>&limit=10"
curl -s -X POST "http://localhost:18483/api/sessions/${SESSION_KEY}/compact"
open http://localhost:18483/ui/
```
