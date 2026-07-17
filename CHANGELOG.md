# Changelog

All notable changes to Lethe are documented here. The project follows semantic versioning once a stable release line is declared.

## Unreleased

### Memory Git

- Add the Memory Git V1 versioned-memory subsystem: immutable semantic history, owned refs, protected `refs/shared/main`, changesets, proposals, reviews, and authorized merges.
- Add the manifest-pinned context bridge: accepted memory is reconstructed at an exact head and pinned in a server-validated context manifest.
- Add merge-authorization verification (`memory-git-merge/v2`) so protected refs move only through Charon-authorized merges.
- Capture the frozen legacy baseline at the synthetic root so pre-Memory-Git events remain visible without silently becoming accepted shared memory.
- Harden protected refs and conflict handling.

### Security

- Strict Bearer token parsing; raw tokens are rejected.
- Explicit trust modes (`private` default, `loopback`); public-network clients are rejected without a token.
- Remove forwarded-header trust and SSE cross-origin exposure.
- Add session/project consistency checks and event/task validation.

### Reliability and plugin

- Inject the newest bounded session events instead of the oldest; report exactly what was injected per turn.
- Add assembly observability in the UI and additive assembly feedback labels.
- Emit throttled warnings on authentication and transport failures instead of silently dropping context.
- Limit plugin search to the configured project.
- Align server, plugin, and engine version reporting at `0.4.0`.

### Project

- Add the MIT license and declare it in the plugin package metadata.
- Add security, contribution, conduct, and support policies.
