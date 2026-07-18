# Deployment & Operations

## Production checklist

- [ ] Pin an exact image tag (`ghcr.io/openlethe/lethe:<version>`), not `latest`.
- [ ] `LETHE_API_KEY` set (required for any non-loopback bind), generated with `openssl rand -hex 32`.
- [ ] Git mode: `CHARON_MERGE_HMAC_KEY` set and identical on Lethe and Charon.
- [ ] Data on bind-mounted host directories owned by UID 1000 — never named volumes for persistent data.
- [ ] `LETHE_TRUST=loopback` unless you have a reason to widen it; never expose the port beyond loopback without a key.
- [ ] Backup schedule in place; restore rehearsed (see below).
- [ ] Metrics scraped (`GET /api/metrics`) and alerts wired ([observability.md](observability.md)).

## Ports and exposure

| Port | Service | Exposure guidance |
|---|---|---|
| `18483` | legacy/hybrid Lethe | loopback, or behind a reverse proxy with the API key |
| `18485` | Memory Git | **loopback only** — reach it through Charon from agents |
| `18484` | Charon primary (OAuth) | the only agent-facing port worth tunneling (HTTPS) |
| `18486` | Charon reviewer (Obol) | loopback only |

## Upgrading

1. Back up the database (`sqlite3 <db> ".backup backups/lethe-<date>.db"` and `PRAGMA integrity_check;`).
2. Read the release notes; schema migrations are additive and automatic on startup.
3. Pull the new pinned tag and recreate the container.
4. Watch startup logs: migration lines, then health `200`/`401` as expected.
5. Rollback: previous image tag + restore the pre-upgrade backup.

Current migration path: v0.3.x → v0.4.0 via automatic additive migration
008. Memory Git runs on separate tables and does not migrate legacy events;
see [migration.md](migration.md).

## Backup and restore

**Backup (no downtime):**

```bash
sqlite3 /path/to/lethe.db ".backup backups/lethe-$(date +%Y%m%d-%H%M).db"
sqlite3 backups/lethe-*.db "PRAGMA integrity_check;"
```

**Restore:** stop the container, replace the database file, start with
`LETHE_RECOVERY_READONLY=1`, validate with `sqlite3 <db> "PRAGMA integrity_check;"`,
then clear the flag.

**Charon side:** the policy database has its own backup/restore runbook in
[Charon's operations guide](https://github.com/openlethe/charon/blob/main/docs/operations.md),
including coordinated two-database backup and `charon reconcile`.

## Container hardening (as shipped)

Non-root UID 1000, `cap_drop: ALL`, read-only root filesystem,
`no-new-privileges`, PID/memory/CPU limits, and a token-free healthcheck
(`/api/health` answers 401 with a key set, 200 without). Compose references
live in [docker-compose.md](docker-compose.md).

## Observability

- `GET /api/health`, `/readyz` — liveness/readiness (auth-aware).
- `GET /api/metrics` — Prometheus metrics for both modes; SLOs and alert
  thresholds in [observability.md](observability.md).
- Logs are structured per request; secret values are never logged.

## Failure behavior to expect

Lethe fails closed. Common named errors:

- startup: `LETHE_API_KEY is required` (non-loopback bind without a key),
  merge-key missing in git mode, unsupported storage durability
- request: `401` (bad/missing bearer), `403` (protected ref written
  directly, merge authorization invalid), `409` (CAS conflict — reread and
  retry), `400` (validation: closed payload keys, size caps, unknown ref)

A `409` on a changeset or merge is never corruption — it is the
compare-and-swap working. Reread the current head, reconcile, resubmit with
a new idempotency key.
