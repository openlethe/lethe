#!/usr/bin/env bash
set -euo pipefail

# Bump the packaged Lethe plugin, push GitHub, and publish the same package to
# ClawHub. The packaged directory is the release owner for this helper;
# source metadata and lockfiles are intentionally outside its scope.
#
# Usage:
#   ./scripts/bump-and-push.sh              # bump the plugin patch version
#   ./scripts/bump-and-push.sh 0.4.8        # use an explicit plugin version
#   ./scripts/bump-and-push.sh 0.4.8        # retry publishing that release
#   ./scripts/bump-and-push.sh --dry-run   # preview without changing anything
#
# Set OPENCLAW_VERSION to override the npm registry lookup when needed.

LETHE_DIR="${LETHE_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
PLUGIN_DIR="$LETHE_DIR/plugins/lethe"
PACKAGE_JSON="$PLUGIN_DIR/package.json"
MANIFEST_JSON="$PLUGIN_DIR/openclaw.plugin.json"
CLAWHUB_NAME="${CLAWHUB_NAME:-@mentholmike/lethe}"
CLAWHUB_OWNER="${CLAWHUB_OWNER:-mentholmike}"
SOURCE_REPO="${SOURCE_REPO:-openlethe/lethe}"

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

assert_release_tree_clean() {
    local entry path
    while IFS= read -r entry; do
        [[ -n "$entry" ]] || continue
        path="${entry:3}"
        case "$path" in
            plugins/lethe/package.json|plugins/lethe/openclaw.plugin.json) ;;
            *)
                echo "ERROR: Packaged file is dirty or untracked: $path" >&2
                echo "       Commit or remove it before publishing." >&2
                return 1
                ;;
        esac
    done < <(
        # ClawHub follows the package.json file allowlist; ignored generated
        # files excluded by that allowlist are not part of the release.
        git -C "$LETHE_DIR" status --porcelain=v1 \
            --untracked-files=all -- plugins/lethe
    )
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
command -v git >/dev/null 2>&1 || {
    echo "ERROR: git is required." >&2
    exit 1
}
SOURCE_REF="$(git -C "$LETHE_DIR" branch --show-current)"
[[ -n "$SOURCE_REF" ]] || {
    echo "ERROR: Cannot publish from a detached HEAD." >&2
    exit 1
}

for file in "$PACKAGE_JSON" "$MANIFEST_JSON"; do
    [[ -f "$file" ]] || { echo "ERROR: Missing $file" >&2; exit 1; }
done
assert_release_tree_clean

CURRENT_VERSION="$(jq -er '.version' "$PACKAGE_JSON")"
MANIFEST_VERSION="$(jq -er '.version' "$MANIFEST_JSON")"
[[ "$CURRENT_VERSION" == "$MANIFEST_VERSION" ]] || {
    echo "ERROR: Package and manifest versions differ: $CURRENT_VERSION vs $MANIFEST_VERSION" >&2
    exit 1
}
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
echo "Package:        $PLUGIN_DIR"
echo "ClawHub:        $CLAWHUB_NAME"

if $DRY_RUN; then
    echo "Dry run: no files, commit, push, or ClawHub publish performed."
    exit 0
fi

command -v clawhub >/dev/null 2>&1 || {
    echo "ERROR: clawhub CLI is required to publish $CLAWHUB_NAME." >&2
    exit 1
}

PACKAGE_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-package.XXXXXX")"
MANIFEST_TMP="$(mktemp "${TMPDIR:-/tmp}/lethe-manifest.XXXXXX")"
cleanup() {
    rm -f "$PACKAGE_TMP" "$MANIFEST_TMP"
}
trap cleanup EXIT

jq --arg plugin_version "$NEW_VERSION" --arg openclaw_version "$OPENCLAW_VERSION" '
    .version = $plugin_version
    | .openclaw.compat.pluginApi = $openclaw_version
    | .openclaw.build.openclawVersion = $openclaw_version
' "$PACKAGE_JSON" >"$PACKAGE_TMP"
jq --arg plugin_version "$NEW_VERSION" \
    '.version = $plugin_version' "$MANIFEST_JSON" >"$MANIFEST_TMP"

mv "$PACKAGE_TMP" "$PACKAGE_JSON"
mv "$MANIFEST_TMP" "$MANIFEST_JSON"
trap - EXIT

if git -C "$LETHE_DIR" diff --quiet HEAD -- \
    plugins/lethe/package.json plugins/lethe/openclaw.plugin.json; then
    echo "No metadata changes; reusing the current release commit."
else
    git -C "$LETHE_DIR" diff --check HEAD -- \
        plugins/lethe/package.json plugins/lethe/openclaw.plugin.json
    git -C "$LETHE_DIR" commit --only \
        -m "chore(release): bump Lethe plugin to v$NEW_VERSION" -- \
        plugins/lethe/package.json plugins/lethe/openclaw.plugin.json
fi

COMMIT_SHA="$(git -C "$LETHE_DIR" rev-parse HEAD)"
assert_release_tree_clean
git -C "$LETHE_DIR" push origin HEAD

clawhub --no-input package publish "$PLUGIN_DIR" \
    --family code-plugin \
    --name "$CLAWHUB_NAME" \
    --owner "$CLAWHUB_OWNER" \
    --version "$NEW_VERSION" \
    --changelog "Bump Lethe plugin to v$NEW_VERSION for OpenClaw $OPENCLAW_VERSION." \
    --tags latest \
    --source-repo "$SOURCE_REPO" \
    --source-commit "$COMMIT_SHA" \
    --source-ref "$SOURCE_REF" \
    --source-path plugins/lethe \
    --json

echo "Updated GitHub and published $CLAWHUB_NAME v$NEW_VERSION for OpenClaw $OPENCLAW_VERSION."
