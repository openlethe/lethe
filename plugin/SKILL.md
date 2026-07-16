---
name: lethe-memory
description: "Use Lethe for manifest-pinned accepted project memory, session continuity, explicit recording, flags, and compaction."
user-invocable: true
metadata:
  openclaw:
    emoji: "🧠"
    requires:
      bins: ["curl", "jq"]
    notes:
      - "Local API base is http://localhost:18483/api; every route includes /api."
      - "Keep Lethe bound to loopback and send Authorization: Bearer when LETHE_API_KEY is configured."
      - "Never put credentials or raw secrets into durable memory."
---

# Lethe Memory

Lethe has two related layers:

1. Session memory: summaries, recent events, checkpoints, tasks, and flags.
2. Memory Git: attributed semantic changesets and refs. `refs/shared/main` is
   canonical accepted project memory; agent/session/topic refs are proposed work.

The context engine automatically reconstructs accepted `refs/shared/main`
memory, pins the exact selected IDs/head in an input manifest, then combines it
with the session summary and bounded recent events.

## Startup and continuity

The plugin normally bootstraps the stable OpenClaw session key and assembles
context automatically. For manual diagnosis:

```bash
curl -sS -H "Authorization: Bearer $LETHE_API_KEY" \
  "http://localhost:18483/api/sessions/${SESSION_KEY}/summary"

curl -sS -X POST -H "Authorization: Bearer $LETHE_API_KEY" \
  -H "Content-Type: application/json" \
  "http://localhost:18483/api/memory/default/context" \
  -d "{\"ref_name\":\"refs/shared/main\",\"session_id\":\"${SESSION_KEY}\",\"actor_id\":\"archimedes\",\"create_manifest\":true,\"limit\":20}"
```

Treat returned accepted memories as canonical. Treat unresolved conflicts as
review items, not facts. Never read an arbitrary unmerged head under the label
of shared accepted memory.

## Recall

Search legacy events when the user asks about prior work that may not have been
migrated or accepted through Memory Git:

```bash
curl -sS -H "Authorization: Bearer $LETHE_API_KEY" \
  "http://localhost:18483/api/events/search?q=<terms>&projectId=default&limit=10"
```

Search once narrowly, broaden once, then say the fact is not recorded. Do not
invent continuity.

## Durable writes

With `autoLog=false`, ordinary conversation and tool calls are not durable
events. Checkpoints still record session progress. Record only deliberate,
future-useful items: decisions, completed work, reusable discoveries, risks,
and durable tasks.

Use Charon's Memory Git tools for cross-model shared memory:

- `memory_status`, `memory_log`, `memory_show`, `memory_diff`
- `memory_context_at`
- `memory_branch_create`, `memory_changeset_create`
- `memory_merge_propose`, `memory_merge_review`, `memory_merge`

Commit only to owned non-protected refs. Propose reviewed merges into
`refs/shared/main`; never bypass Charon's policy or protected compare-and-swap
path.

Use legacy `record`, `log`, `flag`, and `task` events only when the
original event system is specifically appropriate. Direct post-root events do
not silently become accepted Memory Git state.

## Compaction and recovery

Compact long sessions through
`POST /api/sessions/${SESSION_KEY}/compact`. Compaction updates the session
summary; it does not rewrite Memory Git history.

If Lethe returns 401/403, fix endpoint/API-key configuration before relying on
memory. If it is unreachable, retry once with a short timeout, continue without
claiming continuity, and surface the failure. Never suppress authentication
failures or print secret values.
