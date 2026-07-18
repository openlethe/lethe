# Docker Compose Variations

Reference Compose files for the supported deployments. All of them share the
hardened baseline: non-root UID 1000, `cap_drop: ALL`, read-only root
filesystem, `no-new-privileges`, resource limits, bind-mounted data,
token-free healthcheck.

## Variation A — Legacy only (session memory)

```yaml
services:
  lethe:
    image: ghcr.io/openlethe/lethe:latest
    container_name: lethe-server
    restart: unless-stopped
    ports:
      - "127.0.0.1:18483:18483"
    environment:
      LETHE_MODE: legacy
      LETHE_API_KEY: ${LETHE_API_KEY:-}        # optional on loopback
      LETHE_TRUST: ${LETHE_TRUST:-loopback}
    volumes:
      - ${LETHE_DATA_DIR:-./lethe-data}:/data
```

## Variation B — Memory Git only

Ships in the repo as `docker-compose.git.yml`:

```yaml
services:
  lethe-git:
    image: ghcr.io/openlethe/lethe:latest   # multi-arch; uncomment build: . to compile from source
    # build: .
    container_name: lethe-git-local
    restart: unless-stopped
    ports:
      - "127.0.0.1:18485:18483"
    environment:
      LETHE_MODE: git
      LETHE_API_KEY: ${LETHE_API_KEY:?required}
      CHARON_MERGE_HMAC_KEY: ${CHARON_MERGE_HMAC_KEY:?required for protected merges}
      LETHE_TRUST: loopback
    volumes:
      # SQLite data must persist on a bind mount — never a named volume.
      - ${LETHE_GIT_DATA_DIR:-./lethe-git-data}:/data
```

Pair it with `scripts/prepare-local-memory-git-env.sh` to generate
`.env.git` (mode 0600) with both keys.

## Variation C — Hybrid, single instance

Both memory systems in one process on the familiar port — the "one
container, everything" layout:

```yaml
services:
  lethe:
    image: ghcr.io/openlethe/lethe:latest
    container_name: lethe-hybrid
    restart: unless-stopped
    ports:
      - "127.0.0.1:18483:18483"
    environment:
      LETHE_MODE: hybrid
      LETHE_API_KEY: ${LETHE_API_KEY:-}
      CHARON_MERGE_HMAC_KEY: ${CHARON_MERGE_HMAC_KEY:-}
      LETHE_TRUST: ${LETHE_TRUST:-loopback}
    volumes:
      - ${LETHE_DATA_DIR:-./lethe-data}:/data
```

One database, both table sets, one port. The sessions dashboard is at
`/ui/dashboard`, the Memory Git browser at `/ui/memory`.

## Variation D — Governed stack (Lethe Git + Charon)

Two services per role; Charon's Compose pulls its published image. Lethe Git
stays loopback-only; agents meet Charon, never Lethe.

```yaml
services:
  lethe-git:                        # as Variation B
    image: ghcr.io/openlethe/lethe:latest
    ports: ["127.0.0.1:18485:18483"]
    environment:
      LETHE_MODE: git
      LETHE_API_KEY: ${LETHE_API_KEY:?}
      CHARON_MERGE_HMAC_KEY: ${CHARON_MERGE_HMAC_KEY:?}
    volumes:
      - ${LETHE_GIT_DATA_DIR:-./lethe-git-data}:/data

  charon:                           # from the charon repository's compose
    image: ghcr.io/openlethe/charon:latest
    environment:
      CHARON_MODE: memory-git
      CHARON_AUTH_MODE: oauth
      CHARON_UPSTREAM: http://host.docker.internal:18485
      CHARON_ALLOW_INSECURE_UPSTREAM: "1"   # local Docker Desktop only
      LETHE_API_KEY: ${LETHE_API_KEY:?}
      CHARON_OBOL_HMAC_KEY: ${CHARON_OBOL_HMAC_KEY:?}
      CHARON_OAUTH_HMAC_KEY: ${CHARON_OAUTH_HMAC_KEY:?}
      CHARON_MERGE_HMAC_KEY: ${CHARON_MERGE_HMAC_KEY:?}
      CHARON_PUBLIC_URL: ${CHARON_PUBLIC_URL:?}
      CHARON_OAUTH_DEFAULT_USER: ${CHARON_OAUTH_DEFAULT_USER:?}
    volumes:
      - charon-data:/data
    ports: ["127.0.0.1:18484:18484"]

  charon-reviewer:
    image: ghcr.io/openlethe/charon:latest
    environment:
      CHARON_MODE: memory-git
      CHARON_AUTH_MODE: obol
      # ... same shared keys and upstream ...
    volumes:
      - charon-data:/data
    ports: ["127.0.0.1:18486:18484"]

volumes:
  charon-data:
```

The authoritative versions of the Charon services (with full hardening:
non-root 10001, `cap_drop: ALL`, read-only FS, limits) live in the
[charon repository](https://github.com/openlethe/charon/blob/main/docker-compose.yml).

## Rules of thumb

- Bind mounts for data, owned by UID 1000; named volumes only for Charon's
  policy DB (created automatically).
- `CHARON_MERGE_HMAC_KEY` must match on Lethe and Charon; the three Charon
  HMAC purposes must be distinct from each other.
- Never publish Lethe Git (`18485`) or the reviewer gateway (`18486`)
  beyond loopback.
- Compose fails at startup if a `:?`-marked variable is missing — that is
  intentional.
