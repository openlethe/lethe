# HTTP API Reference

All routes require `Authorization: Bearer $LETHE_API_KEY` when a key is
configured (strict Bearer parsing). Without a key, trusted peers
(`LETHE_TRUST`) are admitted.

## Memory Git API — git/hybrid mode

Base: `/api/memory`

| Route | Purpose |
|---|---|
| `GET /projects` | List registered projects |
| `POST /{project}/legacy-root` | Create the synthetic root and freeze the baseline |
| `POST /{project}/branches` · `GET /{project}/refs` · `GET /{project}/refs/resolve?name=` | Ref creation, listing, resolution |
| `POST /{project}/refs/advance` | CAS advance of an unprotected ref (protected refs refused: 403) |
| `POST /{project}/refs/merge` | Protected-ref advance with a valid `memory-git-merge/v2` signed envelope; nonce consumed atomically with the CAS |
| `POST /changesets` · `GET /changesets/{id}` · `GET /{project}/changesets?ref=&limit=` | Changeset creation (idempotency-keyed, digest-bound replay) and retrieval; list defaults to `limit=20`, max `200` |
| `POST /changesets/{id}/diff` | Semantic diff against another changeset |
| `GET · POST /{project}/context?ref=&head=&query=&limit=` | Accepted-context projection at an exact ref/head; manifest pinning on POST with commit/write authority |
| `POST /{project}/conflicts/detect` · `GET /{project}/conflicts` · `POST /conflicts/persist` · `POST /conflicts/retire` · `POST /conflicts/{id}/resolve` | Conflict analysis (pure) and lifecycle management |
| `POST /manifests` · `GET /manifests/{id}` | Context manifests — reproducibility receipts for exactly what was loaded |

## Legacy session API — legacy/hybrid mode

Base: `/api`

| Route | Purpose |
|---|---|
| `GET /api/health` · `/readyz` · `/api/metrics` · `/api/stats` | Health, readiness, Prometheus metrics, stats |
| Sessions CRUD + `GET /api/sessions/{key}/summary` · `POST .../compact` · `.../heartbeat` · `.../interrupt` · `.../resume` · `.../complete` | Session lifecycle and the two-stage resume payload |
| `GET · POST /api/sessions/{key}/events` · `/checkpoints` · `/threads` · `/assemblies` | Per-session memory objects |
| `GET /api/events/search` · `POST /api/events` | Recall and record |
| `GET /api/threads?status=&limit=` · `PATCH /api/threads/{id}` | Cross-session open-thread board |
| `GET /api/flags` · `PUT /api/flags/{id}/review` | Flag review board |
| `GET /api/assemblies/{id}` · `POST /api/assemblies/{id}/feedback` | Assembly retrieval and quality feedback |
| `GET /api/live` (SSE) · `/ui/` | Live event stream and the dashboards |

## Errors

Named, stable error classes: `400` validation (closed payload keys, size
caps, unknown ref, malformed envelope), `401` bearer, `403` protected-ref
direct write / invalid merge authorization, `404` unknown object, `409`
CAS conflict or replay/shape rejection. A `409` is always recoverable:
reread the head, reconcile, resubmit with a new idempotency key.
