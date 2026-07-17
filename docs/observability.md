# Observability, SLOs, and Operational Controls

This document defines the Charon+Lethe production metrics, service-level
objectives, and alert thresholds. Metrics are exposed in Prometheus text
format with no external dependencies:

- **Charon**: `GET /admin/metrics` on the loopback-only admin listener
  (`CHARON_ADMIN_HTTP`, default `127.0.0.1:18486`).
- **Lethe**: `GET /api/metrics` (bearer-auth like every API route).

Health endpoints distinguish liveness from readiness:

| Endpoint | Meaning |
|---|---|
| `GET /admin/livez` (Charon) | Process is alive |
| `GET /admin/readyz` (Charon) | Store answers AND upstream Lethe is reachable |
| `GET /api/health` (Lethe) | Process is alive (liveness) |
| `GET /api/readyz` (Lethe) | Store answers and migrations are current |

## Charon metrics

| Metric | Type | Meaning |
|---|---|---|
| `charon_authz_denials_total` | counter | Authorization denials (scope, project, ownership) |
| `charon_idempotent_replays_total` | counter | Idempotency-key replays served from the original record |
| `charon_idempotency_mismatches_total` | counter | Same-key different-body rejections (fail-closed) |
| `charon_audit_outbox_backlog` | gauge | Audit intents in ambiguous/unreconciled state |
| `charon_audit_append_failures_total` | counter | Failed audit outcome writes (intents stranded for reconciliation) |
| `charon_ledger_append_failures_total` | counter | Failed ledger appends (includes anchor-append failures) |
| `charon_merge_attempts_total` | counter | Protected merge executions attempted after approval |
| `charon_merge_cas_conflicts_total` | counter | Merge CAS losses (contention on the protected ref) |
| `charon_proposals_created_total` | counter | Merge proposals durably created |
| `charon_strict_parsing_rejections_total` | counter | Malformed write requests rejected before any side effect |

The audit ledger itself is verifiable offline: `charon ledger verify` checks
the hash chain and entry MACs; the external anchor
(`CHARON_LEDGER_ANCHOR_PATH`) detects wholesale chain replacement. Ledger
verification failures are operator-visible through those checks and the
`charon reconcile` command, which also reports the audit outbox backlog.

## Lethe metrics

| Metric | Type | Meaning |
|---|---|---|
| `lethe_sqlite_busy_retries_total` | counter | Lock contention retries absorbed |
| `lethe_sqlite_busy_exhausted_total` | counter | Retries exhausted (503 surfaced) |
| `lethe_changesets_created_total` | counter | Immutable changesets committed |
| `lethe_changesets_rejected_total` | counter | Semantic validation rejections |
| `lethe_ref_cas_conflicts_total` | counter | Ref CAS losses |
| `lethe_conflicts_persisted_total` | counter | Conflicts bound to proposals |
| `lethe_conflicts_retired_total` | counter | Conflicts retired (rejected/canceled/superseded) |
| `lethe_conflicts_resolved_total` | counter | Conflicts explicitly resolved |
| `lethe_merge_authorized_total` | counter | Authorized protected-ref advancements |
| `lethe_merge_replay_rejections_total` | counter | Single-use nonce replays rejected |
| `lethe_context_reconstructions_total` | counter | Accepted-context reconstructions |
| `lethe_context_reconstruction_changesets_total` | counter | Changesets traversed by reconstructions |
| `lethe_context_reconstruction_ms_total` | counter | Total reconstruction latency (ms) |
| `lethe_idempotent_replays_total` | counter | Changeset idempotency replays |
| `lethe_idempotency_mismatches_total` | counter | Changeset idempotency mismatches |

## SLOs

| Objective | Target |
|---|---|
| Availability (Charon MCP + Lethe API) | 99.9% monthly |
| Mutation success rate (excluding client 4xx) | ≥ 99.5% |
| Protected merge latency (Charon request → Lethe CAS commit) | p99 < 5s |
| Accepted-context latency (10k-changeset history) | p99 < 500ms (benchmarked ~140ms) |
| Audit durability | 100% of mutations have a durable audit intent before side effects; ambiguous outcomes < 0.01% and always investigated within 24h |
| Backup freshness | Coordinated backup set ≤ 24h old |
| Restore readiness | Quarterly restore drill passes `charon reconcile` cleanly |

## Alerts

| Alert | Condition | Severity |
|---|---|---|
| Audit persistence failure | `charon_audit_append_failures_total` increases | critical (mutations may be failing closed — expected, investigate immediately) |
| Audit outbox backlog | `charon_audit_outbox_backlog > 0` for > 1h | high |
| Ledger append failures | `charon_ledger_append_failures_total` increases | high (check anchor path and disk) |
| CAS conflict storm | `lethe_ref_cas_conflicts_total` or `charon_merge_cas_conflicts_total` > 10/min sustained 10m | medium |
| SQLite lock exhaustion | `lethe_sqlite_busy_exhausted_total` increases | high |
| Unresolved conflict growth | `lethe_conflicts_persisted_total` minus (`lethe_conflicts_retired_total` + `lethe_conflicts_resolved_total`) grows for > 24h | medium |
| Merge replay attempts | `lethe_merge_replay_rejections_total` increases | medium (possible captured-authorization replay) |
| Idempotency mismatches | `charon_idempotency_mismatches_total` or `lethe_idempotency_mismatches_total` increases | medium (client retry bugs or replay attacks) |
| Semantic rejection growth | `lethe_changesets_rejected_total` increases | low (client contract violations) |
| Backup age | Newest manifest `created_at` > 24h | high |
| Restore drill failure | Scheduled `charon reconcile` run reports errors | high |
| Migration failure | Service exits during startup migration | critical |
| Unexpected direct Lethe access | Lethe request logs show non-Charon principals writing to protected paths (merge endpoints, conflict persistence) | high |
| Disk usage | Data-dir filesystem > 80% | medium |

## Runbook pointers

- **Ambiguous audit intents**: query `audit_outbox` for `status='ambiguous'`,
  compare against Lethe advancement records (`memory_protected_ref_advances`)
  and proposal state, then resolve by completing the operation or marking it
  failed. Never delete rows.
- **Ledger mismatch**: run `charon ledger verify` and compare against the
  anchor file; a tail mismatch means the database was replaced or the anchor
  tampered — treat as a security incident.
- **Restore**: take the latest manifest-validated backup set, restore both
  files, start both services in recovery read-only mode
  (`CHARON_RECOVERY_READONLY=1`, `LETHE_RECOVERY_READONLY=1`), run
  `charon reconcile --charon-data <dir> --lethe-db <path> --manifest <path>`
  and `lethe verify-chain <project>`; only lift read-only mode after a clean
  report.
