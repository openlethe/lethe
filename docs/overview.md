# Overview

Lethe is a self-hosted persistent memory platform for AI agents. It runs as a
single container with an embedded SQLite store, exposes a typed HTTP API, and
— through [Charon](https://github.com/openlethe/charon) — a scoped MCP
surface for agents.

## The problem

Agents forget. Between sessions, between restarts, between models, every
conversation rebuilds context from nothing. Lethe provides two ways out of
that loop, selectable per deployment (or combined):

1. **Session memory (legacy mode)** — continuity for a single agent: what
   happened, what was decided, what is still open, resumable after any
   interruption.
2. **Versioned memory (git mode)** — durable knowledge with history:
   project knowledge, architecture decisions, implementation notes, durable
   preferences, collaborative and multi-agent shared memory — written
   through review, never overwritten silently.

## What Lethe is not

- **Not a chatlog store.** Records are semantic — decisions, facts, tasks,
  flags, outcomes — with confidence scores and provenance, not raw transcripts.
- **Not a vector database.** Retrieval is exact: by session, by ref, by head.
  No embeddings are required and none are tuned for you.
- **Not a hosted service.** Lethe runs on your infrastructure, with your
  keys, in one non-root container. Nothing leaves your network unless you
  tunnel it deliberately.
- **Not filesystem Git.** Memory Git borrows Git's *semantics* — refs,
  changesets, branching, merging, immutable history — applied to memory
  records. There are no `.git` directories involved.

## Who uses it

- **Single-agent setups** — ChatGPT, Claude, Claude Code, Cursor, a local
  IDE assistant, each with durable memory of their own.
- **Multi-agent setups** — several agents writing to owned branches, one
  reviewed shared memory on `refs/shared/main`, with Charon enforcing who
  may write, review, and merge.
- **OpenClaw users** — the OpenClaw plugin (legacy mode) provides automatic
  context injection as a first-class integration.

## Design principles

- **Exact over approximate.** Accepted memory is reconstructed at an exact
  changeset head. Readers get precisely what was reviewed — nothing more.
- **Review before acceptance.** Shared memory moves only through proposal,
  independent review, and a signed, single-use merge authorization.
- **Fail closed.** Missing keys, mismatched keys, malformed operations,
  stale bases, and replayed authorizations are all hard errors, never silent
  degradation.
- **One container, your keys.** Non-root image, embedded SQLite with WAL and
  `synchronous=FULL`, operator-generated credentials, no required external
  services.

## The ecosystem

```text
ChatGPT / Claude / Claude Code / Cursor / IDE assistants / MCP clients
                |
                |  OAuth (S256 PKCE) or Obol
                v
             Charon        — authorization, scopes, review, protected merges
                |
                |  private typed API (bearer)
                v
              Lethe        — canonical persistent memory (legacy + Memory Git)

OpenClaw (optional) — plugin injects session memory automatically (legacy mode)
```

Lethe works without Charon (direct bearer-key API access) and without
OpenClaw (any MCP client or plain HTTP client). Charon is the recommended
gateway once memory is shared between agents or exposed beyond loopback.

## Next steps

- [Install Lethe](installation.md)
- [Choose a runtime mode](runtime-modes.md)
- [Learn Memory Git](memory-git.md)
- [Connect your agent](integrations.md)
