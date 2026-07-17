# Build stage — Go toolchain pinned by digest (Go 1.25.12, matches Charon's builder)
FROM golang:1.25.12-alpine@sha256:56961d79ea8129efddcc0b8643fd8a5416b4e6228cfd477e3fd61deb2672c587 AS builder

WORKDIR /build

# Copy only go.mod and go.sum first — no local source to confuse module resolution.
# go mod download downloads deps from the proxy; local packages don't exist yet so
# they don't interfere. GOTOOLCHAIN=local forces the pinned 1.25.12 builder instead
# of resolving a toolchain from mutable external state; go.mod pins the same version.
ENV GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download

# Now copy source and build
COPY . .
RUN go mod tidy -diff
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o lethe ./cmd/lethe

# Runtime stage — minimal alpine image, pinned by digest (matches Charon's runtime base)
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

# Non-root user and data directory for SQLite/WAL files. UID/GID 1000 is kept
# for compatibility with existing bind-mounted data directories (see
# docker-compose.yml and start.sh); /data itself is owner-only.
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 1000 appuser \
    && adduser -S -D -H -u 1000 -G appuser appuser \
    && install -d -o appuser -g appuser -m 0700 /data \
    && install -d -o appuser -g appuser -m 0755 /app

WORKDIR /app

COPY --from=builder --chown=appuser:appuser /build/lethe /app/lethe
COPY --chown=appuser:appuser docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod 0555 /app/lethe /app/docker-entrypoint.sh

USER 1000:1000

EXPOSE 18483

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["--db", "/data/lethe.db", "--http", ":18483"]

# Mount volume for persistent data
VOLUME ["/data"]
