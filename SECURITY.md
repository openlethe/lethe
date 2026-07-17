# Security Policy

Lethe is the canonical persistent memory database behind Charon. Reports involving authentication, trust modes, project isolation, protected refs, merge authorization, manifest integrity, durable audit behavior, or memory content exposure are treated as security issues.

## Supported versions

Security fixes are applied to the latest released minor version and the current `main` branch. Older releases may be asked to upgrade before receiving a patch.

## Reporting a vulnerability

Do not open a public issue with exploit details, credentials, memory content, or deployment identifiers.

Use GitHub's private vulnerability reporting or Security Advisory flow for this repository. Include:

- the affected version or commit;
- deployment mode (`legacy` or `git`) and trust mode;
- a minimal reproduction;
- expected and observed behavior;
- impact and prerequisites;
- any proposed mitigation.

Remove API keys, HMAC keys, tokens, memory contents, and personal data from the report.

A maintainer should acknowledge a complete report within seven days. Disclosure timing will be coordinated after impact and a remediation path are confirmed.

## Security expectations

Production deployments should:

- keep Memory Git Lethe private and reachable only from Charon;
- set a bearer token whenever Lethe is reachable beyond a trusted private network;
- choose `LETHE_TRUST=loopback` unless private-network clients are intentional;
- never rely on forwarded headers for trust decisions;
- configure `CHARON_MERGE_HMAC_KEY` (`CHARON_HMAC_KEY` legacy fallback) so protected-merge authorization can be verified;
- back up SQLite with the online backup mechanism rather than copying live WAL files;
- rotate credentials after suspected exposure;
- keep the Go toolchain, container bases, and dependencies current.

The repository's Compose files are hardened single-host references, not a complete multi-tenant security architecture.
