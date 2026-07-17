#!/bin/sh
set -eu

# Lethe container entrypoint.
# Restrict the umask before starting the server so the SQLite database and
# its WAL/SHM files are created owner-only (0600) on the data volume.
umask 077

# Startup contract: a wildcard/container bind without an API key exposes
# unauthenticated memory writes. Fail with actionable guidance instead of a
# bare config error; LETHE_ALLOW_INSECURE_BIND=1 remains the explicit
# development-only escape hatch (and is loudly logged by the server).
api_key="${LETHE_API_KEY:-}"
http_addr=""
prev=""
for arg in "$@"; do
    if [ "$prev" = "--http" ]; then
        http_addr="$arg"
    fi
    if [ "$prev" = "--api-key" ]; then
        api_key="$arg"
    fi
    case "$arg" in
        --http=*) http_addr="${arg#--http=}" ;;
        --api-key=*) api_key="${arg#--api-key=}" ;;
    esac
    prev="$arg"
done
case "$http_addr" in
    ""|:*) http_addr=":18483" ;;
esac
case "$http_addr" in
    127.0.0.1:*|localhost:*|\[::1\]:*) loopback=1 ;;
    *) loopback=0 ;;
esac
if [ "$loopback" -eq 0 ] && [ -z "$api_key" ] && [ "${LETHE_ALLOW_INSECURE_BIND:-}" != "1" ]; then
    echo "lethe: refusing to start: binding '$http_addr' without LETHE_API_KEY would expose" >&2
    echo "lethe: unauthenticated memory writes to the network." >&2
    echo "lethe: set -e LETHE_API_KEY=<openssl rand -hex 32>, pass --api-key, bind a loopback" >&2
    echo "lethe: address, or pass LETHE_ALLOW_INSECURE_BIND=1 for local development only." >&2
    exit 1
fi

exec /app/lethe "$@"
