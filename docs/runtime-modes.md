# Runtime Modes

Lethe runs in one of three modes, selected by `LETHE_MODE` (or `--mode`):
`legacy`, `git`, or `hybrid`. The mode decides which API surface and which
memory system an instance serves.

## At a glance

| | Legacy | Git | Hybrid |
|---|---|---|---|
| Purpose | Session continuity | Versioned durable memory | Both in one process |
| Data model | sessions, events, checkpoints, threads, flags, assemblies | changesets, refs, projections, manifests, conflicts | both |
| Default port | `18483` | `18485` (reference Compose) | `18483` |
| History | append-only events | immutable semantic changesets with parent links | both |
| Multi-agent sharing | not designed for it | first-class (owned refs + reviewed merges) | via git surface |
| Typical client | OpenClaw plugin | ChatGPT, Claude Code, Cursor, MCP clients, Charon | either |

## Legacy mode

Session-oriented memory. An agent (or the OpenClaw plugin) creates sessions,
logs events, checkpoints periodically, and resumes from the last checkpoint
after any interruption.

You get:

- session history and summaries
- typed events (`record`, `log`, `flag`, `task`) with confidence scores
- periodic checkpoints and interruption recovery
- open-thread and flag tracking across sessions
- assemblies (bounded context packs) with optional quality feedback

Choose legacy when you use OpenClaw (it is the plugin's required mode), or
when all you need is one agent's continuity. See
[legacy-mode.md](legacy-mode.md).

## Git mode (Memory Git)

Versioned persistent memory for any AI agent, using a Git-inspired semantic
model (not filesystem Git). Accepted knowledge lives on a protected ref
(`refs/shared/main`); agents work on owned refs and bring changes back
through proposals and reviewed, signed merges.

Typical contents:

- project knowledge and architecture decisions
- implementation notes and durable preferences
- long-lived context for a single agent
- collaborative, multi-agent shared memory
- local IDE memory that must survive restarts and model changes

Git mode needs no OpenClaw anything: a local IDE assistant, Claude Code,
ChatGPT, Cursor, or any MCP client can use it directly over the HTTP API,
and most deployments put [Charon](https://github.com/openlethe/charon) in
front for scoped, reviewed access. See [memory-git.md](memory-git.md).

## Hybrid mode

Both surfaces in one Lethe instance: the full legacy session API and the
full Memory Git API on one port, one process, one database (each system
keeps its own tables).

Choose hybrid for:

- **migration** — moving from session memory to versioned memory gradually
- **development and testing** — one container covers every surface
- **mixed use** — an OpenClaw-style session flow and a Memory Git flow at
  the same time

```bash
docker run -d -p 127.0.0.1:18483:18483 -v "$PWD/lethe-data:/data" \
  -e LETHE_MODE=hybrid ghcr.io/openlethe/lethe:latest
```

A combined hybrid Compose file is in
[docker-compose.md](docker-compose.md#variation-c--hybrid-single-instance).

## Decision guide

```text
Do you use OpenClaw?                          -> legacy
Do you need shared/reviewed memory
  for one or more agents?                     -> git (add Charon)
Both, or you're migrating between them?       -> hybrid
Unsure?                                       -> git; it is the default
                                                 direction of the project
```

Memory written in one mode is not automatically visible in the other —
legacy events and Memory Git changesets are separate tables with separate
histories. Migration paths are covered in [migration.md](migration.md).
