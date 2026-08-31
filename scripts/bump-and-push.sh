#!/usr/bin/env bash
set -euo pipefail

# Bump Lethe plugin metadata and push only the release metadata files.
#
# Usage:
#   ./scripts/bump-and-push.sh              # bump patch version
#   ./scripts/bump-and-push.sh 0.4.8        # use an explicit plugin version
#   ./scripts/bump-and-push.sh --dry-run   # preview without changing git/files
#
# Set OPENCLAW_VERSION to override the npm registry lookup when needed.

LETHE_DIR="${LETHE_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
PLUGIN_DIR="$LETHE_DIR/plugins/lethe"
SOURCE_PACKAGE_JSON="$LETHE_DIR/plugin/package.json"
SOURCE_MANIFEST_JSON="$LETHE_DIR/plugin/openclaw.plugin.json"
PACKAGE_JSON="$PLUGIN_DIR/package.json"
MANIFEST_JSON="$PLUGIN_DIR/openclaw.plugin.json"

DRY_RUN=false
REQUESTED_VERSION=""

usage() {
    sed -n '4,10p' "$0"
}

bump_patch() {
    local version="$1"
    local major minor patch
    IFS='.' read -r major minor patch <<<"$version"
    echo "$major.$minor.$((patch + 1))"
}

latest_openclaw() {
    local version
    version="$(npm view openclaw version --silent 2>/dev/null || true)"
    if [[ -z "$version" ]] && command -v openclaw >/dev/null 2>&1; then
        version="$(openclaw --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)"
    fi
    [[ -n "$version" ]] || {
        echo "ERROR: Could not determine the latest OpenClaw version." >&2
        echo "       Set OPENCLAW_VERSION explicitly and retry." >&2
        return 1
    }
    echo "$version"
}

for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        -h|--help) usage; exit 0 ;;
        -*) echo "ERROR: Unknown option: $arg" >&2; usage >&2; exit 1 ;;
        *)
            [[ -z "$REQUESTED_VERSION" ]] || {
                echo "ERROR: Only one plugin version may be provided." >&2
                exit 1
            }
            REQUESTED_VERSION="$arg"
            ;;
    esac
done

command -v jq >/dev/null 2>&1 || {
    echo "ERROR: jq is required." >&2
    exit 1
}

for file in "$SOURCE_PACKAGE_JSON" "$SOURCE_MANIFEST_JSON" "$PACKAGE_JSON" "$MANIFEST_JSON"; do
    [[ -f "$file" ]] || { echo "ERROR: Missing $file" >&2; exit 1; }
done

CURRENT_VERSION="$(jq -er '.version' "$PACKAGE_JSON")"
NEW_VERSION="${REQUESTED_VERSION:-$(bump_patch "$CURRENT_VERSION")}"
[[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    echo "ERROR: Invalid plugin version: $NEW_VERSION" >&2
    exit 1
}

OPENCLAW_VERSION="${OPENCLAW_VERSION:-$(latest_openclaw)}"
[[ "$OPENCLAW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
    echo "ERROR: Invalid OpenClaw version: $OPENCLAW_VERSION" >&2
    exit 1
}

echo "Current plugin: $CURRENT_VERSION"
echo "New plugin:     $NEW_VERSION"
echo "OpenClaw:       $OPENCLAW_VERSION"
echo "Files:          plugin/package.json"
echo "                plugin/openclaw.plugin.json"
echo "                plugins/lethe/package.json"
echo "                plugins/lethe/openclaw.plugin.json"

if $DRY_RUN; then
    echo "Dry run: no files, commit, or push performed."
    exit 0
fi

SOURCE_PACKAGE_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-source-package.XXXXXX")"
SOURCE_MANIFEST_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-source-manifest.XXXXXX")"
PACKAGE_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-package.XXXXXX")"
MANIFEST_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-manifest.XXXXXX")"
cleanup() {
    rm -f "$SOURCE_PACKAGE_TMP" "$SOURCE_MANIFEST_TMP" "$PACKAGE_TMP" "$MANIFEST_TMP"
}
trap cleanup EXIT

jq --arg plugin_version "$NEW_VERSION" --arg openclaw_version "$OPENCLAW_VERSION" '
    .version = $plugin_version
    | .openclaw.compat.pluginApi = $openclaw_version
    | .openclaw.build.openclawVersion = $openclaw_version
' "$SOURCE_PACKAGE_JSON" >"$SOURCE_PACKAGE_TMP"
jq --arg plugin_version "$NEW_VERSION" \
    '.version = $plugin_version' "$SOURCE_MANIFEST_JSON" >"$SOURCE_MANIFEST_TMP"
jq --arg plugin_version "$NEW_VERSION" --arg openclaw_version "$OPENCLAW_VERSION" '
    .version = $plugin_version
    | .openclaw.compat.pluginApi = $openclaw_version
    | .openclaw.build.openclawVersion = $openclaw_version
' "$PACKAGE_JSON" >"$PACKAGE_TMP"
jq --arg plugin_version "$NEW_VERSION" \
    '.version = $plugin_version' "$MANIFEST_JSON" >"$MANIFEST_TMP"

mv "$SOURCE_PACKAGE_TMP" "$SOURCE_PACKAGE_JSON"
mv "$SOURCE_MANIFEST_TMP" "$SOURCE_MANIFEST_JSON"
mv "$PACKAGE_TMP" "$PACKAGE_JSON"
mv "$MANIFEST_TMP" "$MANIFEST_JSON"
trap - EXIT

if git -C "$LETHE_DIR" diff --quiet HEAD -- \
    plugin/package.json plugin/openclaw.plugin.json \
    plugins/lethe/package.json plugins/lethe/openclaw.plugin.json; then
    echo "No metadata changes to commit."
    exit 0
fi

git -C "$LETHE_DIR" diff --check HEAD -- \
    plugin/package.json plugin/openclaw.plugin.json \
    plugins/lethe/package.json plugins/lethe/openclaw.plugin.json
git -C "$LETHE_DIR" commit --only -m "chore(release): bump Lethe plugin to v$NEW_VERSION" -- \
    plugin/package.json plugin/openclaw.plugin.json \
    plugins/lethe/package.json plugins/lethe/openclaw.plugin.json
git -C "$LETHE_DIR" push origin HEAD

echo "Updated and pushed Lethe plugin v$NEW_VERSION for OpenClaw $OPENCLAW_VERSION."
