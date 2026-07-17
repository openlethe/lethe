#!/bin/bash
# Lethe Docker Startup Script
# Auto-generates API key if not set, then starts Lethe via docker-compose

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/.env"

# Create .env from example if it doesn't exist
if [ ! -f "$ENV_FILE" ]; then
    if [ -f "${ENV_FILE}.example" ]; then
        cp "${ENV_FILE}.example" "$ENV_FILE"
        echo "Created .env from .env.example — please edit it and set LETHE_API_KEY"
    fi
fi

# Load .env if present
if [ -f "$ENV_FILE" ]; then
    export $(grep -v '^#' "$ENV_FILE" | grep -E '^[A-Z_]+=' | xargs 2>/dev/null || true)
fi

# Auto-generate API key if not set
if [ -z "$LETHE_API_KEY" ]; then
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║  Lethe API Key Auto-Generation                               ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""

    NEW_KEY=$(openssl rand -hex 32)

    # Write to .env file
    if [ -f "$ENV_FILE" ]; then
        # Update existing .env
        if grep -q "^LETHE_API_KEY=" "$ENV_FILE"; then
            # macOS vs Linux sed compatibility
            if [[ "$OSTYPE" == "darwin"* ]]; then
                sed -i '' "s/^LETHE_API_KEY=.*/LETHE_API_KEY=${NEW_KEY}/" "$ENV_FILE"
            else
                sed -i "s/^LETHE_API_KEY=.*/LETHE_API_KEY=${NEW_KEY}/" "$ENV_FILE"
            fi
        else
            echo "LETHE_API_KEY=${NEW_KEY}" >> "$ENV_FILE"
        fi
    else
        echo "LETHE_API_KEY=${NEW_KEY}" > "$ENV_FILE"
    fi

    export LETHE_API_KEY="$NEW_KEY"

    echo "Generated LETHE_API_KEY and saved to .env"
    echo ""
    echo "Key: ${NEW_KEY}"
    echo ""
    echo "IMPORTANT: Share this key with your MCP clients / OpenClaw plugins."
    echo "           It will not be shown again."
    echo ""
fi

# Ensure data directory exists
DATA_DIR="${LETHE_DATA_DIR:-./lethe-data}"
mkdir -p "$DATA_DIR"

# Fix permissions if needed (UID/GID 1000 is appuser in the image)
if [ -d "$DATA_DIR" ] && [ "$(stat -c %u "$DATA_DIR" 2>/dev/null || echo 0)" != "1000" ]; then
    echo "Note: Setting data directory ownership to UID 1000 (appuser)..."
    chown -R 1000:1000 "$DATA_DIR" 2>/dev/null || \
        echo "WARNING: Could not chown ${DATA_DIR}. You may need to run: sudo chown -R 1000:1000 ${DATA_DIR}"
fi

# Start Lethe
echo "Starting Lethe..."
docker compose up -d

# Verify health
echo "Waiting for Lethe to start..."
for i in {1..30}; do
    if curl -s http://localhost:18483/api/health >/dev/null 2>&1; then
        echo ""
        echo "╔══════════════════════════════════════════════════════════════╗"
        echo "║  Lethe is running!                                           ║"
        echo "╚══════════════════════════════════════════════════════════════╝"
        echo ""
        echo "  Health:    http://localhost:18483/api/health"
        echo "  UI:        http://localhost:18483/ui/"
        echo "  API Key:   ${LETHE_API_KEY:0:8}... (from .env)"
        echo ""
        exit 0
    fi
    sleep 1
done

echo "WARNING: Lethe health check failed after 30s"
exit 1
