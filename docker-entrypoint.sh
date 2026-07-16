#!/bin/sh
set -eu

# Lethe container entrypoint.
# Restrict the umask before starting the server so the SQLite database and
# its WAL/SHM files are created owner-only (0600) on the data volume.
umask 077

exec /app/lethe "$@"
