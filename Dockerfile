# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /build

ENV GOTOOLCHAIN=auto

# Copy everything first so module resolution works locally
COPY . .

# Download deps (source is already present, so this finds local packages)
RUN go mod download

# Build static binary (pure Go, no CGO needed)
RUN GOOS=linux go build -ldflags="-s -w" -o lethe ./cmd/lethe

# Runtime stage — minimal alpine image
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /build/lethe /app/lethe

# Create non-root user for security
RUN adduser -D -g '' appuser
USER appuser

EXPOSE 18483

ENTRYPOINT ["/app/lethe"]
CMD ["--db", "/data/lethe.db", "--http", ":18483"]

# Mount volume for persistent data
VOLUME ["/data"]
