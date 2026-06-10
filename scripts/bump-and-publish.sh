#!/usr/bin/env bash
set -euo pipefail
#
# bump-and-publish.sh — Lethe Plugin Release Script
#
# Usage:
#   ./scripts/bump-and-publish.sh              # auto-bump patch (0.3.1 → 0.3.2)
#   ./scripts/bump-and-publish.sh 0.4.0        # explicit version
#   ./scripts/bump-and-publish.sh --dry-run      # preview only, no git/clawhub changes
#

LETHE_DIR="${LETHE_DIR:-$(cd "$(dirname "$0")/.." && pwd)}"
PLUGIN_SOURCE="$LETHE_DIR/plugin"
PLUGIN_DIST="$LETHE_DIR/plugins/lethe"

DRY_RUN=false
REQUESTED_VERSION=""

# ──────────────────── helpers ────────────────────
get_openclaw_version() {
    # Query the canonical upstream: openclaw/openclaw GitHub releases
    local gh_release
    gh_release=$(curl -sL --max-time 10 \
        "https://api.github.com/repos/openclaw/openclaw/releases/latest" 2>/dev/null |
        jq -r '.tag_name // empty' 2>/dev/null)

    if [[ -n "$gh_release" ]]; then
        # Strip leading 'v' if present: "v2026.6.5" → "2026.6.5"
        echo "${gh_release#v}"
        return 0
    fi

    # Fallback: installed binary
    if command -v openclaw &>/dev/null; then
        local v
        v=$(openclaw --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
        [[ -n "$v" ]] && { echo "$v"; return 0; }
    fi

    # Fallback: npm registry
    if command -v npm &>/dev/null; then
        local v
        v=$(npm show openclaw version 2>/dev/null)
        [[ -n "$v" ]] && { echo "$v"; return 0; }
    fi

    # Fallback: local installed package.json
    local pkg_paths=(
        "/opt/homebrew/lib/node_modules/openclaw/package.json"
        "/usr/local/lib/node_modules/openclaw/package.json"
        "$HOME/.nvm/versions/node/*/lib/node_modules/openclaw/package.json"
    )
    for pp in "${pkg_paths[@]}"; do
        for f in $pp; do
            if [[ -f "$f" ]]; then
                local v
                v=$(jq -r '.version // empty' "$f" 2>/dev/null)
                [[ -n "$v" ]] && { echo "$v"; return 0; }
            fi
        done
    done

    echo "ERROR: Cannot detect OpenClaw version. Check network or set OPENCLAW_VERSION" >&2
    return 1
}

bump_patch() {
    local cur="$1"
    local major minor patch
    IFS='.' read -r major minor patch <<<"$cur"
    echo "$major.$minor.$((patch + 1))"
}

update_pkg() {
    local file="$1" plugin_ver="$2" oc_ver="$3"
    jq --arg pv "$plugin_ver" --arg ov "$oc_ver" '
        .version = $pv |
        .openclaw.compat.pluginApi = $ov |
        .openclaw.build.openclawVersion = $ov
    ' "$file" >"${file}.tmp" && mv "${file}.tmp" "$file"
    echo "  ✓ $(basename "$(dirname "$file")")/package.json → $plugin_ver (oc $oc_ver)"
}

# ──────────────────── args ────────────────────
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=true ;;
        -h|--help)
            sed -n '2,8p' "$0"
            exit 0
            ;;
        *) REQUESTED_VERSION="$arg" ;;
    esac
done

# ──────────────────── detect versions ────────────────────
echo "=== Lethe Plugin Release ==="
echo ""

cd "$LETHE_DIR"

CURRENT_VERSION=$(jq -r '.version' "$PLUGIN_DIST/package.json")
[[ -n "$REQUESTED_VERSION" ]] && NEW_VERSION="$REQUESTED_VERSION" || NEW_VERSION=$(bump_patch "$CURRENT_VERSION")

echo "Current version:  $CURRENT_VERSION"
echo "New version:      $NEW_VERSION"

OPENCLAW_VERSION="${OPENCLAW_VERSION:-$(get_openclaw_version)}"
echo "OpenClaw version: $OPENCLAW_VERSION"
echo ""

if $DRY_RUN; then
    echo "[DRY RUN] No files, git, or registry will be touched."
    echo ""
fi

# ──────────────────── sanity checks ────────────────────
if ! command -v jq &>/dev/null; then
    echo "ERROR: jq is required (brew install jq)" >&2
    exit 1
fi

if [[ ! -f "$PLUGIN_SOURCE/package.json" ]] || [[ ! -f "$PLUGIN_DIST/package.json" ]]; then
    echo "ERROR: Missing package.json in plugin/ or plugins/lethe/" >&2
    exit 1
fi

if [[ -n $(git status --short) ]]; then
    echo "WARNING: Working tree has uncommitted changes."
    if ! $DRY_RUN; then
        read -rp "Continue anyway? [y/N] " resp
        [[ "$resp" =~ ^[Yy]$ ]] || exit 1
    fi
fi

$DRY_RUN && { echo ""; echo "Dry run complete."; exit 0; }

# ──────────────────── 1) bump package.json files ────────────────────
echo "==> Bumping package.json files..."
update_pkg "$PLUGIN_SOURCE/package.json" "$NEW_VERSION" "$OPENCLAW_VERSION"
update_pkg "$PLUGIN_DIST/package.json"  "$NEW_VERSION" "$OPENCLAW_VERSION"
echo ""

# ──────────────────── 2) rebuild dist ────────────────────
echo "==> Building distribution..."
cd "$PLUGIN_SOURCE"
npm ci --silent 2>/dev/null || npm install --silent
npm run clean 2>/dev/null || true
npm run build
echo ""

# ──────────────────── 3) sync dist/ to plugins/lethe ────────────────────
echo "==> Syncing dist/ to plugins/lethe/dist/..."
mkdir -p "$PLUGIN_DIST/dist"
rsync -a --delete "$PLUGIN_SOURCE/dist/" "$PLUGIN_DIST/dist/"

# Also sync top-level JS files that plugins/lethe expects at root
cp -f "$PLUGIN_SOURCE/index.js" "$PLUGIN_DIST/index.js" 2>/dev/null || true
echo "  ✓ dist/ synced"
echo ""

# ──────────────────── 4) git commit + tag ────────────────────
echo "==> Committing and tagging..."
git add -A
git commit -m "chore(release): bump Lethe plugin to v$NEW_VERSION (OpenClaw $OPENCLAW_VERSION)"
git tag -a "v$NEW_VERSION" -m "Lethe v$NEW_VERSION — OpenClaw $OPENCLAW_VERSION"
echo "  ✓ Commit + tag v$NEW_VERSION"
echo ""

# ──────────────────── 5) publish to ClawHub ────────────────────
echo "==> Publishing to ClawHub..."

if ! command -v clawhub &>/dev/null; then
    echo "ERROR: clawhub CLI not found. Install: npm i -g clawhub" >&2
    exit 1
fi

if ! clawhub whoami &>/dev/null; then
    echo "ERROR: Not authenticated with clawhub. Run: clawhub login" >&2
    exit 1
fi

cd "$PLUGIN_DIST"
# NOTE: Plugin uses "clawhub package publish", not "clawhub publish"
if clawhub package publish . \
    --family code-plugin \
    --name "@mentholmike/lethe" \
    --display-name "Lethe" \
    --version "$NEW_VERSION" \
    --changelog "Bump to $NEW_VERSION (OpenClaw $OPENCLAW_VERSION)" \
    --tags latest; then
    echo "  ✓ Published @mentholmike/lethe@$NEW_VERSION"
else
    echo "  ✗ Publish failed — see error above." >&2
    echo "" >&2
    echo "Common fixes:" >&2
    echo "  1. Update clawhub: npm i -g clawhub@latest" >&2
    echo "  2. Ensure SKILL.md exists in plugins/lethe/" >&2
    echo "  3. Verify 'clawhub whoami' works" >&2
    exit 1
fi
echo ""

# ──────────────────── 6) push ────────────────────
echo "==> Pushing to origin..."
git push origin HEAD
git push origin "v$NEW_VERSION"
echo "  ✓ Pushed"
echo ""

# ──────────────────── done ────────────────────
echo "=== Done ==="
echo "Version:  v$NEW_VERSION"
echo "Tag:      v$NEW_VERSION"
echo "OpenClaw: $OPENCLAW_VERSION"
echo ""
echo "Verify:   clawhub list | grep lethe"
echo "           npm show @mentholmike/lethe version"
