# OpenClaw Integration

OpenClaw is Lethe's first-class integration — one excellent client, not a
requirement. For OpenClaw users, **Legacy mode is the recommended
deployment**; the plugin automates the entire session-memory loop.

## What the plugin does

- Registers as a **context engine** (`bootstrap`, `assemble`, `compact`
  hooks) so memory loads before the agent processes its first message.
- Runs **two-stage retrieval** at every session start: previous session
  summary first, then recent events within the token budget.
- Conditions the agent to **record as it works** (`lethe.record`,
  `lethe.log`, `lethe.flag`, `lethe.task`, `lethe_search`) — no manual
  tagging.
- Writes a **heartbeat checkpoint every 60 seconds**; crashes resume from
  the last snapshot automatically.
- Surfaces flags and assemblies in the review board so a human can approve,
  and the next session knows what was resolved.

## Install

```bash
# 1 · legacy-mode Lethe on 18483 (see installation.md)
docker run -d --name lethe -p 127.0.0.1:18483:18483 \
  -v "$PWD/lethe-data:/data" ghcr.io/openlethe/lethe:latest

# 2 · the plugin from ClawHub
openclaw skills install lethe-memory

# 3 · only needed beyond loopback: a key
./lethe keygen && export LETHE_API_KEY=lethe_…
```

Plugin configuration lives in `openclaw.plugin.json` (endpoint, optional
API key, project, agent ID). If `LETHE_API_KEY` is set on the server, the
same value belongs in the plugin config.

## Relationship to Memory Git

The plugin automates **session** memory. It does not write to Memory Git by
default; an experimental `memoryGitContext` compatibility switch exists for
migration testing, but the intended versioned-memory client is
[Charon](https://github.com/openlethe/charon) with any MCP harness (see
[integrations.md](integrations.md)). Both systems can run side by side —
that is exactly what [hybrid mode](runtime-modes.md#hybrid-mode) is for.

## When you outgrow it

- Several agents need one shared, reviewed memory → Memory Git + Charon.
- Memory must be provable, branched, and replay-protected → Memory Git.
- The plugin stays useful in parallel for OpenClaw's own session continuity.
