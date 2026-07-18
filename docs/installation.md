# Installation

Lethe ships as a single non-root container image
(`ghcr.io/openlethe/lethe`) and as Go source. Docker is the recommended
install for almost everyone.

## Requirements

- Docker with Compose v2 (container install), or Go 1.25+ (source build)
- `openssl` (for generating keys)

## Quick start (legacy mode)

```bash
# The image runs as UID 1000 and keeps state in /data. On Linux, create the
# bind-mount directory with the right ownership first (one-time):
install -d -o 1000 -g 1000 "$PWD/lethe-data" 2>/dev/null || {
  mkdir -p "$PWD/lethe-data" && sudo chown 1000:1000 "$PWD/lethe-data"; }

docker run -d --name lethe \
  -p 127.0.0.1:18483:18483 \
  -v "$PWD/lethe-data:/data" \
  ghcr.io/openlethe/lethe:latest

curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18483/api/health
# 200 with no key configured (loopback trust), 401 when a key is set
```

Loopback binds work without a key. Any non-loopback exposure requires
`LETHE_API_KEY` — generate one with:

```bash
docker run --rm ghcr.io/openlethe/lethe:latest keygen
# or: openssl rand -hex 32
```

## Git mode (Memory Git)

```bash
sh scripts/prepare-local-memory-git-env.sh      # writes .env.git (mode 0600)
docker compose -f docker-compose.git.yml --env-file .env.git up -d
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18485/api/health   # 401 = enforcing
```

The script generates `LETHE_API_KEY` and `CHARON_MERGE_HMAC_KEY` without
printing them. Git mode binds `127.0.0.1:18485` and uses its own data
directory — never point it at an existing OpenLethe data directory. The
Compose file **pulls `ghcr.io/openlethe/lethe:latest`** (multi-arch:
linux/amd64 + linux/arm64), so no local build or toolchain is required;
uncomment `build: .` in the file to compile from source instead.

## Hybrid mode (both surfaces in one instance)

```bash
docker run -d --name lethe-hybrid \
  -p 127.0.0.1:18483:18483 \
  -v "$PWD/lethe-data:/data" \
  -e LETHE_MODE=hybrid \
  ghcr.io/openlethe/lethe:latest
```

## Source build

```bash
git clone https://github.com/openlethe/lethe.git
cd lethe
go build -o lethe ./cmd/lethe
./lethe keygen
./lethe --db ./lethe.db --http 127.0.0.1:18483
```

## Image tags

- `ghcr.io/openlethe/lethe:latest` — current stable main build (multi-arch,
  non-root UID 1000)
- `ghcr.io/openlethe/lethe:0.4.0-beta.1` — pinned beta release

Production deployments should pin an exact version tag and review the
release notes before upgrading; see [deployment.md](deployment.md) and
[migration.md](migration.md).

## Where to next

- [Choose a runtime mode](runtime-modes.md)
- [Compose variations](docker-compose.md)
- [Configure environment variables](configuration.md)
- [Connect an agent](integrations.md)
