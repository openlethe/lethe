# Local OpenLethe + Lethe Git topology

This repository supports two explicit runtime surfaces. They share code, but they
must not share a database or public API surface while Memory Git is under
development.

## Product boundary

| Surface | Purpose | Mode | Port | Data |
|---|---|---|---:|---|
| OpenLethe | Existing OpenClaw session/event/checkpoint memory | `legacy` | 18483 | Existing OpenLethe bind mount |
| Existing Charon | Existing OpenLethe gateway | `direct` | 18484 | Existing Charon policy/audit database |
| Lethe Git | Versioned memory graph and context manifests | `git` | 18485 | Fresh `lethe-git-data` bind mount |
| Test Charon | Isolated MCP policy, branch ownership, review, and protected merge | `memory-git` | 18487 | Fresh `charon-memory-git-data` bind mount |

`hybrid` mode exists only for migration and tests. It exposes both route
families and should not be the normal local topology.

## Source-control boundary

Keep the released OpenLethe line on its existing stable branch. Develop this
system on a local `lethe-git` branch until the API, migration, and merge
contracts are stable.

The branch separation is a release boundary, not the Memory Git data model.
Runtime memory still uses refs such as:

- `refs/shared/main` for reviewed, accepted memory;
- `refs/agents/<agent>/main` for an agent's work;
- `refs/sessions/<agent>/<session>` for resumable session work;
- `refs/topics/<owner>/<topic>` for bounded investigations.

Do not point the `lethe-git` process at the OpenLethe database. Additive Memory
Git tables may exist in an older development copy, but new work belongs in the
fresh Git-mode database.

## Local startup

Create a local-only Lethe Git environment without printing secret values:

```bash
sh scripts/prepare-local-memory-git-env.sh
```

The script creates ignored `.env.git` with mode `0600`, fresh random API/HMAC
keys, and the isolated data-directory settings. It refuses to overwrite an
existing file.

Then build and start the isolated service:

```bash
docker compose -f docker-compose.git.yml --env-file .env.git up -d --build
```

Start the isolated test Charon from the Charon repository using the same
ignored environment file. This avoids copying or printing the shared keys:

```bash
cd ../charon
docker compose -f docker-compose.memory-git-test.yml \
  --env-file ../lethe/.env.git up -d --build
```

The test gateway binds only to `127.0.0.1:18487`, stores policy state in
`charon-memory-git-data`, and points exclusively to Lethe Git on port `18485`.
The existing OpenLethe service on `18483` and existing direct-mode Charon on
`18484` are outside this stack and must remain running unchanged.

## Enforcement

The boundary is enforced at both ends:

1. A Git-mode Lethe process registers health/stats and `/api/memory/*` only.
   Session, event, checkpoint, thread, assembly, and UI routes are absent.
2. A `memory-git` Charon process registers only the thirteen Memory Git tools.
   Legacy `memory_write`, `memory_propose`, `memory_checkpoint`,
   `memory_search`, and thread tools do not exist in that MCP surface.
3. Charon owns project grants, branch ownership, review separation, audit, and
   the HMAC-signed protected-ref compare-and-swap.
4. Lethe remains the source of truth for changesets, refs, projection, and exact
   context manifests.

This is stronger than detecting a Charon header: neither process has a route or
tool that can silently fall back to unversioned event memory.

## OpenClaw compatibility

The OpenClaw plugin does not query Memory Git by default. Its
`memoryGitContext` compatibility switch is false unless explicitly enabled for
migration testing. The intended versioned-memory client is Charon.

## Context lifecycle

A frontier-model session should:

1. on first use only, call `memory_repo_init` with exact-project commit authority;
2. call `memory_context_at` on `refs/shared/main`;
3. create or resume an owned agent/session ref;
4. record decisions as semantic operations in a changeset;
5. propose the ref head for merge;
6. receive an independent review;
7. merge through Charon's signed protected-ref CAS;
8. load the new accepted head in a later session.

A context manifest is a reproducibility receipt: it records the exact head and
selected memory IDs used by a model. It is not an OpenClaw-specific object.

## Authentication boundary

The current authorization-code flow auto-approves one configured principal, so
Charon caps those tokens to safe read/proposal scopes. A ChatGPT connector can
pull accepted context but cannot initialize, branch, or commit yet. Local
end-to-end authoring uses a trusted Obol with `memory.branch`,
`memory.commit`, and an exact project grant. Authenticated consent or a
confidential OAuth client is required before model-facing OAuth write scopes are
enabled.

## Operating rules

- do not reuse the OpenLethe data directory;
- do not expose port 18485 outside loopback;
- do not let a proposal originator act as its reviewer or merger; use a separate reviewer/merger principal.

## Combined deployment walkthrough

This document covers the Lethe side of the boundary. For the complete
operator walkthrough across both services — bootstrap, credential generation
for Lethe and Charon (including exactly which credentials persist across a
container restart and which regenerate), the three agent roles (maintainer,
proposer, reviewer), a full propose → review → merge run, and the restart,
rotation, and backup drills — see `docs/full-run.md` in the Charon
repository, with per-role playbooks in its `skills/` directory
(`charon-maintainer`, `charon-proposer`, `charon-reviewer`).
