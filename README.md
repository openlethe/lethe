# Lethe — Persistent Memory for AI Agents

Lethe is a self-hosted persistent memory platform for AI agents. It gives
agents durable, reviewable memory that survives sessions, restarts, and model
changes — without surrendering that memory to a third party.

Any agent can use Lethe: ChatGPT, Claude, Claude Code, Cursor, VS Code
agents, OpenClaw, custom MCP clients, local IDE assistants, or backend
services. Lethe is a general-purpose memory platform; the
[OpenClaw plugin](docs/openclaw.md) is one excellent integration, not a
requirement.

## Why it exists

Every agent session starts from zero. The same question, the same reasoning,
the same dead ends — every time. Lethe breaks that loop with two
complementary memory systems:

- **Session memory** — events, summaries, checkpoints, and interruption
  recovery so an agent resumes exactly where it stopped.
- **Versioned memory** — durable, shared, review-gated knowledge with
  immutable history, provenance, branching, and merging (see
  [Memory Git](docs/memory-git.md)).

## Who it is for

- **Individuals** giving a single agent (ChatGPT, Claude, Cursor, an IDE
  assistant) a long memory.
- **Teams** running several agents against one shared, reviewed memory
  backend.
- **Operators** who need memory to stay on their own infrastructure, in one
  container, with their keys.

## Three runtime modes

| Mode | Purpose | Choose it when |
|---|---|---|
| **Legacy** | Session-oriented memory: events, summaries, checkpoints, interruption recovery, task tracking | You use OpenClaw, or want session continuity only |
| **Git** | Versioned persistent memory for any agent: durable knowledge, decisions, shared multi-agent memory, reviewable history | You want persistent, shared, or governed memory — with or without OpenClaw |
| **Hybrid** | Both memory systems in one instance | Migration, development, or when you want session *and* versioned memory together |

Details and decision guidance: [docs/runtime-modes.md](docs/runtime-modes.md).

## Install

```bash
# legacy session memory, port 18483
docker run -d --name lethe \
  -p 127.0.0.1:18483:18483 \
  -v ./lethe-data:/data \
  ghcr.io/openlethe/lethe:latest
```

Memory Git (git mode) runs from `docker-compose.git.yml`; hybrid mode is one
instance with both surfaces. Full instructions, all compose variations, and
source builds: [docs/installation.md](docs/installation.md) ·
[docs/docker-compose.md](docs/docker-compose.md)

## Documentation

| Topic | Document |
|---|---|
| Platform overview & concepts | [docs/overview.md](docs/overview.md) |
| Installation | [docs/installation.md](docs/installation.md) |
| Runtime modes (legacy / git / hybrid) | [docs/runtime-modes.md](docs/runtime-modes.md) |
| Memory Git user guide | [docs/memory-git.md](docs/memory-git.md) |
| Legacy mode | [docs/legacy-mode.md](docs/legacy-mode.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| Configuration (env vars) | [docs/configuration.md](docs/configuration.md) |
| Deployment & operations | [docs/deployment.md](docs/deployment.md) |
| Compose variations | [docs/docker-compose.md](docs/docker-compose.md) |
| HTTP API reference | [docs/api.md](docs/api.md) |
| OpenClaw integration | [docs/openclaw.md](docs/openclaw.md) |
| Client integrations (ChatGPT, Claude Code, Cursor, MCP) | [docs/integrations.md](docs/integrations.md) |
| Migration & upgrading | [docs/migration.md](docs/migration.md) |
| FAQ | [docs/faq.md](docs/faq.md) |
| Memory Git protocol (`memory_git/v1`) | [docs/memory-git-v1.md](docs/memory-git-v1.md) |
| Context projection (`memory-context/v1`) | [docs/memory-context-bridge.md](docs/memory-context-bridge.md) |
| Metrics & observability | [docs/observability.md](docs/observability.md) |

## Related projects

- **[Charon](https://github.com/openlethe/charon)** — the MCP authorization
  and governance gateway for Lethe: scoped principals, independent review,
  replay-proof protected merges, and a fail-closed audit ledger. Recommended
  for shared or multi-agent deployments.
- **OpenClaw plugin** — optional context engine for OpenClaw, published on
  ClawHub as `lethe`. Optional; everything works without it.

## Compatibility

| Component | Release |
|---|---|
| Lethe | `v0.4.0-beta.1` |
| Charon | `v0.1.0-beta.1` |
| Memory Git schema | `memory_git/v1` |
| Merge authorization | `memory-git-merge/v2` |
| Lethe OpenClaw plugin | matching Lethe release |

## License

See [LICENSE](LICENSE). Issues and contributions:
[CONTRIBUTING.md](CONTRIBUTING.md) · Security reports:
[SECURITY.md](SECURITY.md)
