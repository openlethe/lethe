# Architecture

## Components

```text
                 agents
   ChatGPT · Claude · Claude Code · Cursor · IDE assistants · MCP clients
                |
                | OAuth (S256 PKCE) or Obol — MCP (streamable HTTP)
                v
   ┌─────────────────────────────┐
   | Charon (optional gateway)   |  authorization, project grants, scopes,
   |  :18484 OAuth · :18486 Obol |  owned refs, proposals, independent review,
   └──────────────┬──────────────┘  protected merges, audit ledger
                  | bearer `LETHE_API_KEY`, private typed API
                  v
   ┌─────────────────────────────┐
   | Lethe                       |  canonical persistent memory
   |  :18483 legacy · :18485 git |
   |  SQLite (WAL, FULL sync)    |
   └─────────────────────────────┘

   OpenClaw (optional) → plugin → legacy Lethe :18483 (session memory)
```

Lethe runs fine without Charon (direct bearer-key API). Charon is required
only when you want scoped principals, review separation, and
replay-protected merges enforced in front of the store.

## The boundary

- **Lethe owns the truth**: changesets, refs, the CAS, context projection,
  manifests, conflicts, and (in legacy mode) sessions, events, checkpoints,
  threads, flags, assemblies.
- **Charon owns policy**: principals, scopes, project grants, ref ownership,
  proposals, review findings, merge authorization signing, and the audit
  ledger. Lethe independently *verifies* every signed merge envelope —
  trust is checked on both sides.
- **OpenClaw is a client, not a component**: the plugin automates session
  memory in legacy mode. It is optional and never required for Memory Git.

## Data model

One embedded SQLite database per instance (WAL mode, `synchronous=FULL`,
busy timeout enforced, startup-verified durability):

| System | Tables | Notes |
|---|---|---|
| Legacy | sessions, events, checkpoints, threads, flags, assemblies | append-only events, `parent_event_id` threading |
| Memory Git | changesets, changeset ops, refs, manifests, conflicts, merge-authorization nonces | immutable changesets, parent-linked; consumed merge nonces survive restarts (replay protection) |

Git mode is served from its own data directory by convention
(`docker-compose.git.yml`, port `18485`) so it never touches an existing
OpenLethe store. Hybrid mode serves both systems from one database.

## Modes and surfaces

| Mode | HTTP surface |
|---|---|
| `legacy` | legacy session API + dashboard UI |
| `git` | Memory Git API + repository-style Memory Git UI |
| `hybrid` | both APIs and both UIs on one port |

Details: [runtime-modes.md](runtime-modes.md) · [api.md](api.md).

## Security posture

- **Fail closed everywhere**: missing keys, mismatched merge keys, malformed
  operations, stale bases, replayed authorizations — all hard errors.
- **Credentials are operator-generated** (`openssl rand -hex 32`,
  `./lethe keygen`, or `scripts/prepare-local-memory-git-env.sh`), mode
  0600, never committed, never stored in the database.
- **Strict bearer parsing** for the API key; network trust modes
  (`loopback`/`private`) apply only when no key is set; non-loopback binds
  without a key are refused at startup.
- **Merge authorization** is a signed, expiring, single-use,
  proposal-digest-bound envelope (`memory-git-merge/v2`), independently
  verified by Lethe, with the nonce consumed atomically against the CAS.
- **Hardened image**: non-root UID 1000, `cap_drop: ALL`, read-only root
  filesystem, `no-new-privileges`, resource limits, token-free healthcheck.

## Availability and recovery

- Write-ahead logging with `synchronous=FULL`; the server runs recovery on
  startup and can boot read-only (`LETHE_RECOVERY_READONLY`) until
  reconciliation passes.
- Coordinated backups via `sqlite3 .backup` (zero downtime) or the Charon
  backup CLI; restores are validated before mutations resume.
- Upgrades pin image tags; rollback is an image tag plus the previous
  database backup. See [deployment.md](deployment.md) and
  [migration.md](migration.md).
