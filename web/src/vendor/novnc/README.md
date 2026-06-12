# Vendored noVNC

Upstream:  https://github.com/novnc/noVNC
Package:   @novnc/novnc (pinned at 1.5.0 in `web/package.json` devDependencies)
Version:   1.5.0 (tag v1.5.0)
License:   MPL-2.0 (see LICENSE.txt in this directory)

This directory contains the unmodified ES-module RFB client from the noVNC
v1.5.0 source tree:

- `core/`        — the RFB implementation (entry point: `core/rfb.js`)
- `vendor/pako/` — noVNC's bundled zlib implementation, referenced by
                   `core/inflator.js` / `core/deflator.js` via relative imports
- `LICENSE.txt`  — the MPL-2.0 license text

The app imports `RFB` from `src/vendor/novnc/core/rfb.js` (NOT from
node_modules) so this pinned copy is the source of truth at build time. The
`@novnc/novnc` devDependency exists purely as provenance and as the update
mechanism: the npm package ships only a CommonJS `lib/` build, so updating
means re-copying `core/` + `vendor/pako/` + `LICENSE.txt` from the matching
upstream git tag and bumping the pinned devDependency version to match.
