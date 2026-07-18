# Configuration

Lethe is configured with environment variables (or the equivalent CLI flags
for a source run). Write `.env` by hand — no script is required. Rules that
apply everywhere:

- Generate secrets with `openssl rand -hex 32` or `./lethe keygen`.
- Keep every secret purpose distinct; never reuse a value across purposes.
- `chmod 600 .env` and never commit it (`.gitignore` covers `.env`,
  `.env.*`).
- The server validates configuration at startup and fails closed with a
  named error.

## Core

| Variable | Default | Meaning |
|---|---|---|
| `LETHE_API_KEY` | unset (loopback ok) | Bearer key for the HTTP API and UI. **Required** for any non-loopback bind; never stored in the DB. |
| `LETHE_MODE` | `legacy` | `legacy`, `git`, or `hybrid`. See [runtime-modes.md](runtime-modes.md). |
| `LETHE_TRUST` | `loopback` | Unauthenticated trust when no key is set: `loopback` or `private` (loopback + private/link-local networks). |
| `LETHE_ALLOW_INSECURE_BIND` | `0` | Dev-only bypass of the "non-loopback requires a key" startup check (logged loudly). |
| `LETHE_HTTP` | `0.0.0.0:18483` (container) | HTTP listen address inside the container. |
| `LETHE_DATA_DIR` | `./lethe-data` | Legacy-mode data directory (must be writable by UID 1000). |
| `LETHE_GIT_DATA_DIR` | `./lethe-git-data` | Git-mode data directory — never point it at an OpenLethe data directory. |

## Memory Git authorization

| Variable | Default | Meaning |
|---|---|---|
| `CHARON_MERGE_HMAC_KEY` | git mode: required | Verifies signed `memory-git-merge/v2` envelopes. Must be identical to Charon's value. |
| `CHARON_MERGE_HMAC_KEYS` | unset | Rotation form: comma-separated `keyid=secret` pairs; all configured IDs verify during overlap. |
| `CHARON_HMAC_KEY` | unset | **Deprecated** generic fallback; startup warning when used. |
| `LETHE_MEMORY_GIT_MAX_HISTORY` | `100000` | History graph/reconstruction node cap; fails closed beyond it. |

## Durability and recovery

| Variable | Default | Meaning |
|---|---|---|
| `LETHE_SQLITE_SYNCHRONOUS` | `FULL` | SQLite durability level, verified at startup. |
| `LETHE_SQLITE_BUSY_TIMEOUT_MS` | `5000` | Write-contention timeout. |
| `LETHE_SQLITE_AUTOCHECKPOINT` | `1000` | WAL autocheckpoint interval. |
| `LETHE_RECOVERY_READONLY` | `false` | Block all mutations after a restore until reconciliation passes. |

## Legacy assemblies

| Variable | Default | Meaning |
|---|---|---|
| `LETHE_ASSEMBLY_MAX_PER_SESSION` | bounded | Assembly count cap per session. |
| `LETHE_ASSEMBLY_RETENTION_DAYS` | bounded | Age-based assembly retention. |

## CLI equivalents (source run)

`--api-key`, `--api-port`, `--api-url`, `--db`, `--mode`, `--trust`,
`--assembly-retention-days`, `--assembly-max-per-session`. `./lethe keygen`
prints a fresh `LETHE_API_KEY=lethe_<64 hex>`.

## Charon

Charon has its own environment (`CHARON_OBOL_HMAC_KEY`,
`CHARON_OAUTH_HMAC_KEY`, `CHARON_MERGE_HMAC_KEY`, OAuth, upstream, limits,
ledger, recovery). The full annotated reference is
[`.env.example` in the Charon repository](https://github.com/openlethe/charon/blob/main/.env.example)
and the combined setup is in
[Charon's full-run guide](https://github.com/openlethe/charon/blob/main/docs/full-run.md).
The one value that must always match across both services is
`CHARON_MERGE_HMAC_KEY`.
