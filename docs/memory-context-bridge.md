# Memory Git Context Bridge

Status: implemented and locally tested on `v0.4-local-staging`  
Projection contract: `memory-context/v1`

## Purpose

This bridge makes accepted Memory Git history affect model context. The canonical
input is the exact head of `refs/shared/main`, not every event in the legacy
event table and not an unmerged agent branch.

## Data flow

```
legacy events at legacy-root creation ─┐
                                      ├─ deterministic projection at head
accepted changeset DAG on shared/main ─┘
                         │
                         ├─ apply add/correct/supersede/duplicate/deprecation
                         ├─ rank active memories against the current prompt
                         └─ create exact input manifest
                                      │
                                      v
OpenClaw context engine ─ accepted memory + session summary + recent events
                                      │
                                      v
assembly ledger ─ manifest ID + head changeset ID + token counts
```

## Frozen legacy baseline

Migration `010_memory_context_bridge.sql` adds
`memory_legacy_baselines`. The first legacy root freezes every project event
whose creation time is at or before the root changeset. Events written directly
after the root are not silently accepted into Memory Git. They must be
represented by an accepted semantic operation.

Existing databases are repaired lazily: when the legacy root is next ensured or
projected, Lethe reconstructs the baseline using the root creation timestamp and
stores the exact IDs.

## Projection semantics

`BuildMemoryContext` walks the complete changeset DAG from roots to the pinned
head in deterministic parent order and applies:

- `add_memory`: creates or replaces an active semantic memory.
- `correct_memory`: overlays the target while retaining immutable history. A
  distinct resulting ID retires the target and creates the replacement.
- `supersede_memory`: retires the target and optionally creates a replacement.
- `mark_duplicate`: removes the duplicate from active context.
- `propose_deprecation`: keeps the memory active and marks the proposal.
- `attach_evidence` / `attach_verification`: attach metadata to the target.
- `add_relationship`: records an accepted relationship.

Historical heads are allowed only when they are reachable from the named ref.
This prevents an unmerged branch head from being presented as accepted shared
memory.

Selection is deterministic. Query token overlap ranks first, then most recently
changed active memories, then memory ID. The hard maximum is 100 memories.

## API

### Retrieve without creating a manifest

```http
GET /api/memory/{project}/context?ref=refs%2Fshared%2Fmain&head={optional}&query={optional}&limit=20
Authorization: Bearer {LETHE_API_KEY}
```

### Retrieve and pin an input manifest

```http
POST /api/memory/{project}/context
Authorization: Bearer {LETHE_API_KEY}
Content-Type: application/json

{
  "ref_name": "refs/shared/main",
  "head_changeset_id": "",
  "query": "current user prompt",
  "limit": 12,
  "session_id": "stable-session-key",
  "actor_id": "archimedes",
  "create_manifest": true
}
```

The response includes `manifest_id`, `head_changeset_id`,
`projection_version`, selected memories, and unresolved conflict IDs. Direct
`POST /api/memory/manifests` calls accept either a stable session key or the
canonical Lethe session ID; the server stores the canonical ID so later assembly
validation is exact.

## OpenClaw assembly

The context engine requests a projected view on every model assembly, drops
whole low-ranked memories until the final rendered text fits the remaining token
budget, and only then creates the exact input manifest. It injects accepted
project memory before the session summary and bounded recent events. The
assembly ledger records:

- `memory_manifest_id`
- `memory_head_changeset_id`
- `accepted_estimated_tokens`
- the existing summary and recent-event items

Authentication and transport failures are rate-limited warnings rather than
silent failures. Normal agent execution continues if Lethe is unavailable.

## Charon surface

Charon exposes `memory_context_at`. It requires `memory.read`, enforces
project grants, allows shared refs, and restricts non-shared refs to the
principal's owned agent/session/topic namespace. Callers with an exact project
grant plus `memory.commit` or `memory.write` receive an exact input manifest;
read-only wildcard callers get a non-mutating projection. Every call is recorded
in Charon's audit ledger.

## Verification

The automated suite covers:

- a frozen legacy baseline;
- exclusion of post-root unversioned events;
- accepted add/correct/supersede/duplicate behavior;
- historical reconstruction;
- rejection of unmerged heads under `refs/shared/main`;
- API-created manifests and assembly linkage;
- OpenClaw injection and manifest/head reporting;
- Charon project/ref policy;
- full-branch-delta conflict detection.

Run:

```bash
go test ./...
go test -race ./...
go vet ./...
(cd plugin && npm test)
```
