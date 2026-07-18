# Memory Git

Memory Git is Lethe's versioned memory system: durable, reviewable,
branchable memory for any AI agent. It borrows Git's semantics — refs,
changesets, branching, merging, immutable history — and applies them to
memory records. **It is not filesystem Git**: no `.git` directory, no blobs
of source code; the objects are semantic memory records.

## What it enables

- **Persistent AI memory** — knowledge that survives sessions, restarts,
  and model changes, reconstructed exactly at a changeset head.
- **Collaborative knowledge** — many agents, one accepted history, each
  writing to its own refs.
- **Reviewable memory** — nothing reaches shared memory without a proposal
  and an independent review.
- **Immutable history** — every changeset is validated, then frozen forever;
  corrections and supersedes are new entries, never rewrites.
- **Provenance** — every record carries its author principal, parent
  changesets, and the review that admitted it.
- **Branching and merging** — `refs/agents/*`, `refs/sessions/*`,
  `refs/topics/*` for working memory; `refs/shared/*` for accepted memory.
- **Versioned context** — read memory at any exact head, optionally pinned
  in a context manifest.

## The objects

| Object | Meaning |
|---|---|
| **changeset** | The immutable unit: an ordered list of semantic operations (`add_memory`, `correct_memory`, `supersede_memory`, `mark_duplicate`, `add_relationship`, `attach_evidence`, `attach_verification`, `propose_deprecation`) committed with compare-and-swap and an idempotency key |
| **ref** | A named pointer to a changeset. `refs/shared/*` is protected accepted memory; `refs/agents/<actor>/*`, `refs/sessions/<actor>/*`, `refs/topics/*` are owned by their creating principal |
| **proposal** | A merge request from an owned ref head into a protected ref, pinned to the exact source changeset at creation |
| **review** | An `approve` / `request_changes` / `reject` verdict bound to the proposal snapshot. Originators can never review or merge their own proposals |
| **merge authorization** | A `memory-git-merge/v2` envelope: HMAC-signed, ≤15-minute TTL, single-use nonce, bound to project, ref, heads, proposal digest, reviewer, and merger. Lethe verifies it independently and consumes the nonce atomically with the CAS — replays are rejected even if the ref cycles back |
| **context projection** | `memory_context_at` reconstructs accepted memory at an exact ref head — precisely what was approved, optionally pinned in a manifest |

## The everyday flow

1. **Orient** — read accepted memory: `memory_context_at` on
   `refs/shared/main`.
2. **Branch** — create or reuse an owned ref: `refs/agents/<you>/main` or a
   topic ref.
3. **Commit** — write a changeset with compare-and-swap
   (`expected_head` + idempotency key). Payloads are closed-key validated at
   Charon and again at Lethe before anything becomes immutable.
4. **Propose** — `memory_merge_propose` into `refs/shared/main`.
5. **Independent review** — a different principal records a verdict bound to
   the exact snapshot. Self-review and self-merge are denied in code.
6. **Merge** — the reviewer applies it; the signed envelope advances the
   protected ref; the ledger records everything.
7. **Read later** — any reader reconstructs accepted memory at the new head.

Retrying the identical request with the same idempotency key returns the
original changeset exactly once; a changed request under a reused key fails
closed instead of silently discarding work.

## Using it without OpenClaw

Memory Git needs nothing OpenClaw-specific. Direct over HTTP:

```bash
curl -H "Authorization: Bearer $LETHE_API_KEY" \
  http://127.0.0.1:18485/api/memory/<project>/refs
```

Or via MCP through [Charon](https://github.com/openlethe/charon) for scoped,
reviewed access — see [integrations.md](integrations.md) for
ChatGPT, Claude Code, Cursor, and generic MCP setups, and the
[full governed walkthrough](https://github.com/openlethe/charon/blob/main/docs/full-run.md).

## Further reading

- [memory-git-v1.md](memory-git-v1.md) — the full protocol: changeset model,
  merge shapes, conflict lifecycle, envelope fields, durability and recovery
- [memory-context-bridge.md](memory-context-bridge.md) — the
  `memory-context/v1` projection and manifest API
- [runtime-modes.md](runtime-modes.md) — when to run git vs legacy vs hybrid
- [api.md](api.md) — the Memory Git HTTP routes
