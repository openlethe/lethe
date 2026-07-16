# Local Lethe Git + Charon Memory Git verification — 2026-07-14

## Scope and recovery exception

All work remained on the local machine in:

- `/Users/michaelwyatt/.openclaw/workspace/lethe`
- `/Users/michaelwyatt/.openclaw/workspace/charon`

Nothing was pushed, published, tagged, released, or written to a remote
repository or registry. The final Lethe image was rebuilt from an already-local
image with `--pull=false` and `--network=none`.

Docker Desktop stopped during the first attempt after host disk exhaustion and
BuildKit I/O errors. Mike explicitly authorized a Docker Desktop reset. That
reset necessarily changed the original containers' start times and checkpointed
or reopened their SQLite files, so the historic pre-reset byte hashes cannot be
claimed as unchanged. After recovery, the original container identities,
images, mounts, ports, restart counts, and database hashes remained stable
through every isolated workflow and restart.

No shared Docker prune was run. Final Docker accounting reported 78 GiB free and
zero BuildKit cache.

## Local source state

- Lethe branch: `lethe-git`
- Charon branch: `memory-git-local`

Both intentionally dirty working trees were preserved. No commit or remote
mutation was performed.

`.env.git` is Git-ignored, mode `0600`, and contains only the local test keys
`LETHE_API_KEY`, `CHARON_HMAC_KEY`, `LETHE_GIT_DATA_DIR`, and
`CHARON_MEMORY_GIT_DATA_DIR`. Secret values and full Obol tokens were never
printed. Both Compose files use explicit environment allowlists.

## Verified topology

| Service | Container | Host binding | Mode/upstream | Data |
|---|---|---|---|---|
| Existing OpenLethe | `lethe-server` | `127.0.0.1:18483` | existing service | `/Users/michaelwyatt/docker/lethe/lethe-data` |
| Existing Charon | `charon-charon-1` | existing `18484` binding | existing direct service | existing `charon-data` |
| Isolated Lethe Git | `lethe-git-local` | `127.0.0.1:18485 -> 18483` | `LETHE_MODE=git` | `lethe/lethe-git-data` |
| Isolated test Charon | `charon-memory-git-test` | `127.0.0.1:18487 -> 18484` | `memory-git`, Obol, upstream only `http://host.docker.internal:18485` | `charon/charon-memory-git-data` |

The isolated services were healthy. Neither isolated bind used `0.0.0.0`, the
test Charon did not point to `18483`, and neither test database reused an
original-service directory.

Final isolated images:

- Lethe Git: `sha256:2adaf39d2c7a42baf7846ab38ae26c6f3f69eb07b5bbc5c2d02c2de2ddc6e164`
- Charon test: `sha256:f136eddba20f223f87cc8011455a8f20ac5d1b4837dae5bc4bc8bfd4bd27532e`

## Trusted exact-project principals

- Author: `principal_7b7b63819619ee85`
  - exact project `lethe-git-e2e`
  - `memory.read`, `memory.search`, `memory.branch`, `memory.commit`, `memory.propose`
- Reviewer: `principal_2a7fed640296ceb0`
  - exact project `lethe-git-e2e`
  - `memory.read`, `memory.search`, `memory.review`, `memory.merge`

The author has no review or merge scope. The reviewer has no branch or commit
scope. Separate trusted Obols were stored only in the ignored, dedicated test
data directory with restrictive permissions.

## End-to-end proof on the final local image

The harness verified the exact 13-tool Memory Git MCP surface and passed:

1. `memory_repo_init` on protected `refs/shared/main`;
2. exact initial `memory_context_at` pull and manifest;
3. owned session branch creation from the exact shared head;
4. semantic changeset commit using compare-and-swap;
5. protected merge proposal;
6. self-review rejection;
7. self-merge rejection;
8. separate reviewer finding;
9. reviewer-authorized protected merge;
10. a new MCP session pulling accepted memory and an exact manifest.

Latest ledger evidence:

- sequence 32: author `memory_merge_review` -> `denied: missing scope`
- sequence 33: author `memory_merge` -> `denied: missing scope`
- sequence 34: reviewer `memory_merge_review` -> `success`
- sequence 35: reviewer `memory_merge` -> `success`

Approved proposal:

- proposal: `019f63de-5535-76f4-8beb-1f22b76b7154`
- originator: `principal_7b7b63819619ee85`
- reviewer/decider: `principal_2a7fed640296ceb0`
- target: `refs/shared/main`
- base: `019f63c5-9f72-7bb0-9b6d-32e4bd87c169`
- accepted head: `019f63de-5532-71e8-8d61-93ceeb5e7c4f`

After restarting only `lethe-git-local` and `charon-memory-git-test`,
`-verify-state` passed. Both the source branch and `refs/shared/main` resolve to
the accepted head above.

Exact persisted context:

- manifest: `019f63de-81b9-7bb2-9215-d5fc4f77ccd6`
- project: `lethe-git-e2e`
- ref: `refs/shared/main`
- session: `e2e-persisted-18c2599f46acce60`
- head: `019f63de-5532-71e8-8d61-93ceeb5e7c4f`
- required accepted memory: `mem-e2e-18c2599f46acce60`
- exact selected list:
  - `mem-e2e-18c2599f46acce60`
  - `mem-e2e-18c258263b6e2348`
  - `mem-e2e-18c2549a42742b40`
  - `mem-e2e-18c25476d6b72290`

## Original-service comparison

Recovered originals remained healthy and stable throughout the isolated proof:

- `lethe-server`
  - container `bc99ddb1ed0a47d69d5aa3ff701d02bd6bdf6488f3fac0347471abfed6a7cbe6`
  - image `sha256:5adb0a1e9769a9147fb416163e8e7a236d0da1440f9ed05825e340da8107d1c6`
  - recovered start `2026-07-15T02:46:58.476791213Z`, restart count `0`
  - mount and loopback port unchanged
- `charon-charon-1`
  - container `317c8081e096272982d68fe756575183597b37a9a9a1bdeaa04fb0b6b69c1639`
  - image `sha256:d1c2ac3144d1609a08d42dc0b3a1e2181a42cacd1543ff09239cbace928be306`
  - recovered start `2026-07-15T02:46:58.478276379Z`, restart count `0`
  - all four original mounts and the existing port binding unchanged

Recovered-run hashes before the requested final Lethe audit log:

- OpenLethe `lethe.db`: `d53cfe825148b64e26b0a2d09d67e7aef96bb5e207625e8826c45bd293a5a421`
- OpenLethe WAL: empty SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
- OpenLethe SHM: `fd4c9fda9cd3f9ae7c962b0ddf37232294d55580e1aa165aa06129b8549389eb`
- Charon `charon.db`: `775f3cc57562dfbc36336869594cb13648aa4f73b78f87442dccddeeeeba3ebf`
- Charon WAL: empty SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
- Charon SHM: `fd4c9fda9cd3f9ae7c962b0ddf37232294d55580e1aa165aa06129b8549389eb`

These exactly matched the recovered baseline. The subsequent requested audit log
to OpenLethe is an explicit, expected write and is not part of the isolation
hash comparison.

## Verification gates

Lethe passed `go test ./...`, `go test -race ./...`, `go vet ./...`,
`go build ./cmd/lethe`, `git diff --check`, Compose validation, and all 11 plugin
tests. Charon passed `go test ./...`, `go test -race ./...`, `go vet ./...`, both
command builds, `git diff --check`, and Compose validation.

Focused security tests cover the OAuth mutation-scope cap, unknown-client and
missing-principal fail-closed behavior, live grant revocation, exact-project
repo initialization, CAS, branch ownership, self-review, self-merge, and the
independent-review requirement.

Autoreview produced actionable Lethe findings that were fixed with regression
tests: branch-delta conflict projection, originating metadata, shared ancestry,
accepted-context token budgeting, bounded legacy-event loading, projected-base
semantics, and fail-closed unresolved-conflict projection. A later Charon review
completed clean. The final Lethe retry could not complete because the external
Codex account hit its usage limit; it is recorded as unavailable, not passed.

## Remaining ChatGPT authorization gap

The current automatic authorization-code OAuth path deliberately strips Memory
Git mutation scopes. It must continue to exclude `memory.branch` and
`memory.commit`; trusted authoring in this proof used the separate Obol path.

Preferred future design: an authenticated owner-consent flow that binds the
canonical principal, client ID, exact redirect URI, and PKCE; requires explicit
consent for exact project IDs and mutation scopes; re-intersects every request
with current database grants; and rejects wildcard mutation grants.

A confidential client is acceptable only with an out-of-source, rotatable and
revocable secret, a reconciled exact-project service principal, scope-limited
tokens, and live grant checks. The current synthetic default-project behavior is
not sufficient for elevated authoring.

## Failed or qualified checks

- Docker's authorized reset prevents claiming unchanged historic start times or
  pre-reset SQLite byte hashes.
- The final Lethe autoreview retry hit an external account usage limit.
- No other verification check failed.
