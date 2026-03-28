# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /build

# Copy only go.mod and go.sum first — no local source to confuse module resolution.
# go mod download downloads deps from the proxy; local packages don't exist yet so
# they don't interfere. We use GOTOOLCHAIN=auto to let Go fetch a newer toolchain
# if the go.mod says so (e.g. go 1.25.0 with deps requiring 1.25+).
ENV GOTOOLCHAIN=auto
COPY go.mod go.sum ./
RUN go mod download

# Now copy source and build
COPY . .
RUN go mod tidy
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
