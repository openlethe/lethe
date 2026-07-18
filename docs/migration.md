# Migration & Upgrading

## Version migrations

Schema migrations are additive and run automatically at startup. The
supported path today:

- **v0.3.x → v0.4.0** — automatic additive migration 008. Existing sessions,
  events, checkpoints, threads, flags, and assemblies carry over untouched.

Always back up before upgrading (`sqlite3 <db> ".backup <out>.db"` +
`PRAGMA integrity_check;`), pin image tags, and keep the previous tag plus
the pre-upgrade backup as the rollback path.

## Legacy events ↔ Memory Git

The two memory systems are separate tables with separate histories.
Migrating to Memory Git **does not** rewrite or import legacy events:

- A git-mode instance creates a synthetic **legacy root** that freezes the
  pre-Memory-Git baseline once (`POST /api/memory/{project}/legacy-root`).
- Later direct event writes never silently become accepted shared memory —
  memory enters `refs/shared/main` only through changesets, proposals, and
  reviewed merges.
- Run [hybrid mode](runtime-modes.md#hybrid-mode) during transition so both
  surfaces stay available while clients move over.

## Moving an existing deployment to Memory Git

1. Keep the legacy instance running (its data is unaffected).
2. Start a git-mode instance with a **fresh** data directory — never point
   git mode at the OpenLethe data directory
   (`scripts/prepare-local-memory-git-env.sh` + `docker-compose.git.yml`).
3. Add [Charon](https://github.com/openlethe/charon) when you want scoped,
   reviewed writes; reconcile author/reviewer/reader principals per project.
4. Re-record durable knowledge as changesets on owned refs and merge through
   review (bulk import is intentionally absent — reviewed memory is written,
   not dumped).
5. Retire the legacy instance when its sessions close out, or keep it for
   session continuity alongside (hybrid).

## Upgrading the deployment shape

- Single-container users keep one port (`18483`) and one data directory;
  nothing else changes.
- Git-mode users gain `18485` and a second data directory; both are local
  bind mounts owned by UID 1000.
- Charon-governed users add the two Charon services and three distinct HMAC
  keys; the merge key must match Lethe's. The full sequence is in
  [Charon's full-run guide](https://github.com/openlethe/charon/blob/main/docs/full-run.md).

## Rollback

1. Stop the container.
2. Restore the pre-upgrade backup over the data file.
3. Start the previous pinned image tag.
4. Verify health and a representative read path before resuming writes.
