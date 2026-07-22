# Vendored noVNC

- **Package:** `@novnc/novnc`
- **Version:** 1.5.0 (pinned)
- **License:** MPL-2.0 (see `LICENSE.txt`, copied verbatim from the upstream
  package; third-party notices for vendored Pako/base64 code are included in
  the upstream license file)

`rfb.js` is a single-file ESM bundle of the upstream `lib/rfb.js` entry point
(the full RFB/VNC client), produced with esbuild so the Vite build can import
it from `src/` without CommonJS interop plugins:

```bash
npm pack @novnc/novnc@1.5.0
tar xzf novnc-novnc-1.5.0.tgz
npx esbuild package/lib/rfb.js --bundle --format=esm --outfile=rfb.js
```

Import through `index.js` (`import RFB from '../vendor/novnc'`), which
unwraps the Babel CJS interop default export.

To upgrade: bump the version in the commands above, regenerate `rfb.js`,
re-copy `LICENSE.txt`, and update this README.
