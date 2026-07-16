#!/bin/sh
set -eu

output=${1:-.env.git}

if [ -e "$output" ]; then
  printf 'refusing to overwrite existing %s\n' "$output" >&2
  exit 1
fi
if ! command -v openssl >/dev/null 2>&1; then
  printf 'openssl is required\n' >&2
  exit 1
fi

umask 077
api_key=$(openssl rand -hex 32)
merge_key=$(openssl rand -hex 32)

printf '%s\n' \
  '# Local-only Lethe Git + test Charon secrets. Do not commit.' \
  "LETHE_API_KEY=$api_key" \
  "CHARON_MERGE_HMAC_KEY=$merge_key" \
  'LETHE_GIT_DATA_DIR=./lethe-git-data' \
  'CHARON_MEMORY_GIT_DATA_DIR=./charon-memory-git-data' \
  > "$output"
chmod 600 "$output"

printf 'created %s with mode 600; secret values were not printed\n' "$output"
