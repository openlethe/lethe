# Legacy Mode

Legacy mode is Lethe's session-oriented memory: the original OpenLethe
service. It gives one agent continuity across sessions — what happened, what
was decided, what remains open — resumable after any interruption. It is the
required mode for the [OpenClaw plugin](openclaw.md) and the right choice
when session continuity is all you need.

## What it records

| Type | Use case |
|---|---|
| `record` | A decision made, a direction chosen, a conclusion reached |
| `log` | An observation, a discovery, something worth noting |
| `flag` | Uncertainty that needs human review before proceeding |
| `task` | A work unit with a status chain: `todo → in_progress → done` |

Records carry confidence scores (0.0–1.0). Flags persist across sessions
until explicitly reviewed. Task events chain so you can trace the history of
any piece of work.

## How it works

1. **Sessions** — each agent session gets a stable key. State: `active`,
   `interrupted`, or `completed`.
2. **Events** — every meaningful action is logged: decisions, observations,
   flags, tasks. Events are append-only and threaded by `parent_event_id`.
3. **Checkpoints** — periodic snapshots; an interrupted session resumes from
   the last checkpoint, not from zero.
4. **Compaction** — long sessions synthesize events into a narrative summary
   while the full history stays in the database.
5. **Threads** — flags and open questions group into named threads tracked
   across sessions.
6. **Assemblies** — bounded context packs built at read time (summary first,
   then recent events to the token budget), with optional quality feedback
   (`good`, `stale_included`, `missing_memory`, `too_large`, `irrelevant`,
   `other`).

## Deploying it

```bash
docker run -d --name lethe \
  -p 127.0.0.1:18483:18483 \
  -v "$PWD/lethe-data:/data" \
  ghcr.io/openlethe/lethe:latest
```

Loopback needs no key. For anything beyond loopback, set `LETHE_API_KEY`
(see [configuration.md](configuration.md)). The dashboard is at
`http://127.0.0.1:18483/ui/dashboard`.

## OpenClaw (the recommended experience)

For OpenClaw users, the plugin automates the whole loop: it registers as a
context engine, loads the previous session summary and recent events at
startup, conditions the agent to record as it works, checkpoints every 60
seconds in the background, and surfaces flags in the review board.

```bash
openclaw skills install lethe-memory
```

The plugin is optional and OpenClaw-specific — see
[openclaw.md](openclaw.md). Any other client can drive the same API
directly (see [api.md](api.md)).

## When not to choose it

- You need **shared, multi-agent memory** — legacy sessions are
  single-agent streams. Use [Memory Git](memory-git.md) (optionally with
  Charon for scoped review).
- You need **review-gated, versioned history** — Memory Git's
  changesets/refs/proposals are the tool for that, in git or
  [hybrid](runtime-modes.md#hybrid-mode) mode.
