# Memory Git V1 — Local Design

Status: implemented and locally verified; live rollout requires container/plugin restart
Date: 2026-07-14  
Constraint: no external publish, no production deploy, no history rewrite.

## Goal

Permission-aware, Git-inspired versioning above Lethe's immutable event history,
integrated with Charon's existing proposal workflow.

## Non-goals

- Actual Git repository storage
- A second proposal system
- In-place edits of accepted memory
- Offline clone/pull/push protocol (V1)
- Rewriting pre-Memory-Git event IDs

## Layering

```
MCP client / local CLI
        |
        v
     Charon
  auth · scopes · proposals · audit · MCP tools
        |
        | private upstream (LETHE_API_KEY)
        v
      Lethe
  immutable events · changesets · refs · diffs · checkout
```

| Concern | Owner |
|---------|-------|
| Immutable memory objects | Lethe `events` |
| Changesets + ops + digests | Lethe `memory_changesets*` |
| Project-scoped refs / CAS | Lethe `memory_refs` |
| Semantic diff computation | Lethe |
| Context manifests (pin) | Lethe `memory_manifests` |
| Conflicts | Lethe `memory_conflicts` + Charon review |
| Merge proposals / approval | Charon `proposals` (extended) |
| Principal scopes / project grants | Charon |
| Hash-chained audit | Charon ledger |
| Idempotency | Charon (tool) + Lethe (changeset key) |

## Required refs

All refs are project-scoped. Primary key is `(project_id, ref_name)`.

| Ref | Purpose |
|-----|---------|
| `refs/shared/main` | Accepted canonical project memory |
| `refs/agents/<actor_id>/main` | Agent working branch |
| `refs/sessions/<actor_id>/<session_id>` | Session-isolated work |
| `refs/topics/<topic_id>` | Topic/thread branch |
| `refs/repos/<repo>/<branch>` | Optional DevSpace association |

Protected: `refs/shared/*` requires merge/review scope. Models may commit to
agent/session/topic refs they own; they may only *propose* merges into shared.

## Changeset model

Immutable record:

- `changeset_id` (UUIDv7)
- `schema_version` (`memory_git/v1`)
- `project_id`
- `ref_name` (branch that created it)
- `parent_ids` (1+ for merge)
- author principal / persona / actor
- surface / model / environment (optional)
- session_id / topic_id (optional)
- context_manifest_id (optional)
- message
- created_at
- idempotency_key (unique per project+principal)
- ordered semantic operations
- evidence / artifact refs
- verification records
- integrity_digest (SHA-256 over canonical JSON)

### Semantic operations (V1)

| Op | Meaning |
|----|---------|
| `add_memory` | Append new accepted-or-proposed memory object |
| `add_relationship` | Link two memory objects |
| `correct_memory` | Correcting overlay (does not erase target) |
| `supersede_memory` | Mark target superseded by new memory |
| `mark_duplicate` | Duplicate detection record |
| `propose_deprecation` | Soft deprecation proposal |
| `attach_evidence` | Evidence attachment |
| `attach_verification` | Verification attachment |

Operations reference Lethe event IDs when materializing accepted memory.
Branch commits may stage ops before merge materializes events on shared.

### Semantic validation (memory_git/v1)

Lethe validates every operation before it can enter immutable,
integrity-digested history (Charon validates the same structure client-side
first). Ops apply sequentially: a later op may target memory introduced by an
earlier op in the same changeset. Contract:

- `add_memory`: non-empty trimmed `content`; `kind` in
  `observation|fact|decision|task|flag|record` when present; `visibility` in
  `private|internal|shared|public` when present; `confidence` in [0,1] when
  present; `tags` is an array of non-empty strings when present; an explicit
  resulting identity must not collide with an active memory at the parent.
- `correct_memory`: `target_event_id` required and must exist at the parent
  state; payload carries at least one correction field; `resulting_event_id`
  differs from the target when present.
- `supersede_memory`: target exists; no self-supersede; a lower-trust
  inference (`source_trust` = `inference`/`model_inference`) cannot replace a
  `user_approved` target.
- `mark_duplicate`: duplicate and canonical targets both required, distinct,
  and existing at the parent.
- `add_relationship`: from/to required, distinct (self-relations are not
  allowed), existing; non-empty `kind`.
- `propose_deprecation`: target exists; non-empty `reason`.
- `attach_evidence` / `attach_verification`: with a target, the target must
  exist. Without a target the op is a merge/review attestation marker and
  requires non-empty `summary` plus one provenance field (`reviewer`,
  `proposal_id`, `source_changeset_id`, `rejected_from`, `cherrypicked_from`,
  `left_branch`, `right_branch`).

All identities are project-local by construction; cross-project targets fail.


## Legacy baseline

Existing events remain readable. On first Memory Git use for a project:

1. Create synthetic root changeset `legacy-root` with empty ops.
2. Point `refs/shared/main` at it if no ref exists.
3. Optionally attach a baseline snapshot listing known event IDs as evidence
   only — do not rewrite event rows.

## Ref safety

- Updates use compare-and-swap: `UPDATE ... WHERE head_changeset_id = ?expected`
- Concurrent CAS miss → `409 conflict` with current head (never silent discard)
- Divergence is explicit: caller must rebase, multi-parent merge, or reject
- Models cannot set arbitrary ref pointers; only service paths with scope
- Generic branch creation and changeset/ref advance reject protected refs at the
  store boundary. Protected advances use a separate Charon-signed merge CAS.
- The protected merge signature uses the purpose-specific
  `CHARON_MERGE_HMAC_KEY`, separate from `LETHE_API_KEY` (legacy
  `CHARON_HMAC_KEY` is still accepted as a fallback); Lethe verifies project,
  ref, expected/new heads, proposal ID, and reviewer principal before
  applying CAS.

## Semantic diff

Deterministic report from base → target:

- memories added
- corrections proposed
- records superseded
- relationships added
- decisions changed
- tasks/flags changed
- duplicates detected
- permissions/visibility affected
- evidence added/removed
- unresolved conflicts

When both records share a lineage via supersede/correct, label as
**temporal update**. When two accepted facts conflict without lineage, label as
**direct contradiction**.

## Merge / conflicts

Charon proposals gain optional Memory Git fields:

- `kind = merge_request | changeset | memory` (existing memory/checkpoint remain)
- `base_changeset_id`, `head_changeset_id`
- `target_ref`
- `selected_ops_json` (cherry-pick)
- `conflict_ids_json`
- `review_findings_json`

Supported merge shapes:

- fast-forward
- reviewed multi-parent merge changeset
- cherry-pick selected ops
- reject with history preserved
- cancel / expire
- review comments / findings

Conflict detectors (V1 minimum):

1. Incompatible accepted facts, same scope + valid time
2. Contradicts protected accepted decision
3. Duplicate semantic content
4. Stale base / non-fast-forward
5. Private → broader scope information flow
6. User-approved memory replaced by lower-trust inference
7. Project / topic / actor / namespace boundary violations

Never auto-resolve substantive conflicts by "newest wins".

### Conflict detection purity and lifecycle

Conflict **analysis is pure**: `POST /api/memory/{project}/conflicts/detect`
never writes; repeated analysis returns identical results with no side
effects. Conflicts persist only as part of an explicit proposal operation
(`POST /conflicts/persist`), bound to their proposal (`proposal_id`).

Conflict identity is **deterministic** — a SHA-256 digest of project, base,
left, right, type, and the affected semantic identity — so equivalent
retries, replays, and re-detections converge on one row instead of
duplicating. Resolving the canonical row retires every equivalent blocker at
once (`POST /conflicts/{id}/resolve`).

Lifecycle: `open → resolved | rejected | superseded | canceled`
(`deferred` optional). Canceling or rejecting a proposal retires its
conflicts (`POST /conflicts/retire`); a landed merge marks them superseded.
Accepted-context reconstruction exposes only `open` conflicts relevant to the
exact requested head — abandoned-proposal conflicts can no longer pollute
manifests or withhold memory.

### Protected-merge authorization (memory-git-merge/v2)

Every protected-ref movement requires a Charon-signed, **single-use,
expiring** envelope: version, project, ref, expected head, new head, proposal
ID, proposal-state digest, reviewer and merger principals, merge strategy
(`fast_forward` | `merge_commit` | `cherry_pick`), issued-at, expiry (short;
≤15m enforced), a cryptographic nonce, and a key ID — HMAC-SHA256 over the
canonical envelope bytes.

Lethe verifies the signature against the key the envelope names (rotation
overlap via `CHARON_MERGE_HMAC_KEYS`), validates every field against the
request, enforces expiry and clock tolerance, then **atomically consumes the
nonce with the protected-ref CAS** in one transaction — a captured
authorization can never be replayed, even if the ref later cycles back to the
same head. Lethe independently enforces the merge shape and the new head's
project, and writes a durable advancement record
(`memory_protected_ref_advances`) for reconciliation.

Keys: merge HMAC material is purpose-specific (`CHARON_MERGE_HMAC_KEY` /
`CHARON_MERGE_HMAC_KEYS` with key IDs). The generic `CHARON_HMAC_KEY`
fallback is formally deprecated (startup warning) and must not be used in
production.


## Revert / checkout

- Revert = new correcting changeset (`correct_memory` / `supersede_memory`)
- Checkout = resolve project ref at changeset ID or timestamp
- Manifest pins make historical reconstruction exact
- Redaction remains separate privileged privacy path

## Manifests

Input manifest pins:

- project
- memory ref
- head changeset ID
- exact selected memory IDs
- inclusion / exclusion reasons

Output manifest records:

- base changeset
- resulting session changeset
- proposed target ref
- unresolved conflicts
- merge proposal ID if created

## MCP / CLI (Charon surface)

| Tool / command | Role |
|----------------|------|
| `memory_status` | Project ref heads + principal capabilities |
| `memory_log` | Changeset log for a ref |
| `memory_show` | Show one changeset |
| `memory_branch_create` | Create branch from expected head |
| `memory_diff` | Semantic diff |
| `memory_changeset_create` | Commit to authorized non-protected ref |
| `memory_merge_propose` | Proposal into protected/shared |
| `memory_merge_review` | Attach findings / comments |
| `memory_merge` | Approve + apply (privileged) |
| `memory_revert_propose` | Propose correcting revert |
| `memory_context_at` | Reconstruct accepted view |
| `memory_ref_list` | List refs in authorized projects |

CLI aliases (when present): `lethe memory status|log|diff|branch|show|merge`
are thin clients over the same APIs. Prefer Charon for policy-bound access.

## Scopes (Charon)

| Scope | Capability |
|-------|------------|
| `memory.search` / `memory.read` | Read accepted memory + diffs of authorized refs |
| `memory.branch` | Create agent/session/topic branches |
| `memory.commit` | Commit to owned non-protected refs |
| `memory.propose` | Create merge/revert proposals |
| `memory.review` | Attach review findings |
| `memory.merge` | Approve/apply merges into protected refs |
| `memory.write` | Trusted direct writes (Archimedes local) |

ChatGPT default: read + branch + commit(owned) + propose. No self-merge.

## Implementation order

1. Lethe schema + store + unit tests (changesets, refs CAS, ops, legacy root)
2. Semantic diff + conflict detection pure packages
3. Lethe HTTP API for memory-git endpoints
4. Charon proposal extension + scopes + MCP tools
5. Manifest pin/read integration with assemblies
6. Acceptance test harness (disposable DBs, two principals)
7. Local CLI thin wrappers

## Implemented context bridge

The `memory-context/v1` projector now freezes the legacy event baseline,
reconstructs an exact ref/head, applies semantic overlays, rejects heads that
are not reachable from the named ref, and creates input manifests for selected
memories. OpenClaw injects the manifest-pinned accepted view and records its
manifest/head in the assembly ledger. Charon exposes the same operation as
`memory_context_at` with project and ref-ownership policy.

Conflict detection now evaluates the complete changeset delta from the declared
base, rather than only the two tip changesets.

Operational details and verification commands are in
[`memory-context-bridge.md`](memory-context-bridge.md).

## Durability and recovery

Durability policy: WAL + `synchronous=FULL` + `foreign_keys=ON` +
`busy_timeout` + WAL autocheckpoint (env-tunable via `LETHE_SQLITE_*`);
verified at startup — unsupported storage fails closed. Committed
transactions survive process and container kills (tested); clean shutdown
checkpoints the WAL. RPO is the transaction boundary; storage must honor
fsync.

Coordinated backup/restore: `charon backup` snapshots both databases with a
manifest; after restore, run in recovery read-only mode
(`LETHE_RECOVERY_READONLY=1`, `CHARON_RECOVERY_READONLY=1`), then
`charon reconcile` plus `lethe verify-chain <project>`; only lift read-only
after a clean report.

Transport: loopback-only trust by default; non-loopback binds require
`LETHE_API_KEY`; Charon requires HTTPS, a Unix socket, or loopback HTTP for
its upstream. Network locality is never principal identity.

## Acceptance

See parent Memory Git V1 acceptance checklist (16 steps). Definition of done:

Multiple authorized models can branch from the same accepted memory, commit
isolated attributed changesets, compare semantic differences, propose reviewed
merges, preserve conflicts and rejected work, revert without deleting history,
and reproduce the exact memory state used by any session manifest.
