# Contributing to Lethe

Thank you for improving Lethe. Changes to this repository may affect the canonical memory store, protected refs, merge authorization, or the context projections other tools depend on, so evidence and narrowly scoped changes matter.

## Development setup

Requirements:

- Go 1.25.12 or newer within the supported 1.25 release line;
- Docker with Compose for container validation;
- Node.js and npm for the OpenClaw plugin under `plugin/`.

Run the local verification set before submitting a change:

```bash
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.22.5 -quiet ./...
docker compose config --quiet
docker build -t lethe:review .
cd plugin && npm ci && npm test
```

CI also requires a clean `gofmt -l .` result and validates shell scripts, Compose files, and the image's non-root user.

## Change discipline

- Preserve immutable memory history; corrections and supersedes are new operations, not edits.
- Keep protected refs movable only through authorized, verified merges.
- Validate every semantic operation before immutable insertion.
- Generate or validate manifests on the server; never trust caller-claimed heads or selections.
- Preserve idempotency and compare-and-swap behavior at every mutation boundary.
- Fail closed when durable audit intent cannot be persisted.
- Never log credentials or raw memory content.
- Add negative tests for authorization failures and stale state.
- Avoid unrelated refactors in security fixes.

## Pull requests

A pull request should explain:

- the user-visible or security problem;
- the affected trust boundary;
- the smallest compatible fix;
- tests and behavior proof;
- deployment or migration impact;
- remaining limitations.

Security-sensitive changes should receive an independent review. Do not include generated binaries, local databases, environment files, tokens, or runtime state. The packaged plugin bundle under `plugins/lethe/` is intentionally tracked; rebuild it through the documented release process instead of editing it by hand.

## Commit and release hygiene

Source commits should remain buildable from a clean checkout. Container images and plugin packages are produced by the tagged release workflows and should not be committed into the repository.
