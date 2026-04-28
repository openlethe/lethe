# RELEASE.md — Lethe Plugin Release Workflow

This document describes how to publish a new version of the Lethe OpenClaw plugin to both GitHub and ClawHub.

---

## Repository Structure

```
lethe/
├── plugin/              # Source TypeScript project (development)
│   ├── src/             # TypeScript source files
│   ├── dist/            # Build output (not tracked in git)
│   ├── package.json     # Source package.json
│   └── openclaw.plugin.json
│
├── plugins/lethe/       # Packaged plugin for distribution (THIS GOES TO CLAWHUB)
│   ├── index.js         # Compiled entry point
│   ├── context-engine.js # Compiled modules
│   ├── tools.js
│   ├── package.json     # Points to ./index.js (NOT ./dist/index.js)
│   ├── openclaw.plugin.json
│   └── skills/
│
└── README.md
```

**Critical distinction:**
- `plugin/` = source code (for development)
- `plugins/lethe/` = compiled package (for ClawHub distribution)

---

## Version Bump Checklist

### 1. Update Source Version

In `plugin/package.json` and `plugin/openclaw.plugin.json`:
```json
{
  "version": "x.y.z",
  "openclaw": {
    "compat": {
      "pluginApi": "YYYY.MM.DD"
    },
    "build": {
      "openclawVersion": "YYYY.MM.DD"
    }
  }
}
```

### 2. Build the Plugin

```bash
cd plugin/
npm run build
```

This compiles TypeScript to `plugin/dist/`.

### 3. Sync to Distribution Directory

```bash
# From repo root
cp -r plugin/dist/* plugins/lethe/dist/ 2>/dev/null || true
cp plugin/package.json plugins/lethe/
cp plugin/openclaw.plugin.json plugins/lethe/
cp plugin/index.js plugins/lethe/ 2>/dev/null || true
```

### 4. Verify Distribution `package.json`

Ensure `plugins/lethe/package.json` has:
```json
{
  "main": "index.js",
  "openclaw": {
    "extensions": ["./index.js"]
  }
}
```

**NOT** `./dist/index.js` — the compiled files are at root.

### 5. Update Distribution Version

In `plugins/lethe/package.json` and `plugins/lethe/openclaw.plugin.json`:
```json
{
  "version": "x.y.z"
}
```

### 6. Commit and Push to GitHub

```bash
git add -A
git commit -m "release: bump to vX.Y.Z

- OpenClaw compat: YYYY.MM.DD
- [describe changes]"
git push origin main
```

### 7. Publish to ClawHub

1. Go to [clawhub.ai](https://clawhub.ai)
2. Navigate to your plugin: `@mentholmike/lethe`
3. Click **Update** / **Publish new version**
4. Upload the **`plugins/lethe/`** directory (NOT `plugin/`)
5. Verify the package structure:
   - `index.js` at root
   - `package.json` with correct version
   - `openclaw.plugin.json` with correct `extensions`
6. Submit

### 8. Verify Installation

```bash
# Remove local install if present
openclaw plugins uninstall mentholmike-lethe --force

# Install from ClawHub
openclaw plugins install clawhub:@mentholmike/lethe

# Verify
openclaw plugins list | grep lethe
openclaw plugins info mentholmike-lethe
```

---

## Common Issues

### `extension entry not found: ./dist/index.js`

**Cause:** Uploaded `plugin/` directory instead of `plugins/lethe/`, or `plugins/lethe/package.json` still points to `./dist/index.js`.

**Fix:** Ensure `plugins/lethe/package.json` has `"extensions": ["./index.js"]` and upload `plugins/lethe/` to ClawHub.

### ClawHub Cache Stale

**Cause:** ClawHub caches packages by version. If you re-upload the same version, the old cache may persist.

**Fix:** Bump version (e.g., 0.2.3 → 0.2.4) to force cache refresh.

### Plugin Works Locally but Not from ClawHub

**Cause:** Local install uses `plugin/dist/` which exists after `npm run build`. ClawHub package doesn't have `dist/` if not properly synced.

**Fix:** Always run the sync step (step 3 above) before uploading.

---

## File Reference

| File | Purpose | Updated When |
|------|---------|-------------|
| `plugin/package.json` | Source package config | Every release |
| `plugin/openclaw.plugin.json` | Source manifest | Every release |
| `plugins/lethe/package.json` | Distribution config | Every release (after sync) |
| `plugins/lethe/openclaw.plugin.json` | Distribution manifest | Every release (after sync) |
| `plugins/lethe/index.js` | Compiled entry point | After `npm run build` |
| `plugins/lethe/context-engine.js` | Compiled module | After `npm run build` |
| `plugins/lethe/tools.js` | Compiled module | After `npm run build` |

---

## Release History

| Version | Date | OpenClaw | Changes |
|---------|------|----------|---------|
| 0.2.5 | 2026-04-28 | 2026.4.26 | Fixed extension path, ClawHub package |
| 0.2.4 | 2026-04-28 | 2026.4.26 | Cache refresh attempt |
| 0.2.3 | 2026-04-28 | 2026.4.26 | Bumped OpenClaw compat |
| 0.2.2 | 2026-04-26 | 2026.4.24 | Previous stable |

---

_Last updated: 2026-04-28_
