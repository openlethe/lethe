# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build

ENV GOTOOLCHAIN=auto

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

COPY . .

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

EXPOSE 8080

ENTRYPOINT ["/app/lethe"]
CMD ["--db", "/data/lethe.db", "--http", ":8080"]

# Mount volume for persistent data
VOLUME ["/data"]
