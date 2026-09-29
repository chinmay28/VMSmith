# VM Smith as a Progressive Web App

The VM Smith web GUI is an installable Progressive Web App (PWA). It is **not a second app**.
The same React SPA that the daemon embeds and serves on `daemon.listen` is also the PWA. Every
GUI feature (VMs, consoles, images, templates, schedules, activity, logs, settings) is available
in the installed app, and any feature added to the GUI later ships in the PWA automatically.

## Installing

| Platform | How |
|---|---|
| Chrome / Edge (desktop, Android) | Use **Install app** in the sidebar footer or in **Settings → App**, or the browser's install icon in the address bar |
| Safari (iOS / iPadOS) | Share → **Add to Home Screen** |
| Safari (macOS 14+) | File → **Add to Dock** |

**A secure origin is required.** Browsers only allow service workers and installs on HTTPS
or on `localhost`. When you open the GUI over plain HTTP from another machine
(`http://10.0.0.5:8080`), it still works as a normal web page, but it cannot be installed and
has no offline shell. **Settings → App** tells you when this is the case. To enable it, serve
the daemon over TLS (`daemon.tls.cert_file` / `key_file`, or `daemon.tls.auto_cert`), or put it
behind a TLS-terminating reverse proxy.

## What the PWA adds on top of the web GUI

- **Installable app window:** includes a manifest, icons (with a maskable variant), a theme
  color, and shortcuts for Machines, Images, Schedules and Activity.
- **Offline app shell:** the installed app opens even when the daemon is unreachable. An
  offline banner says so, and actions resume once the daemon is back.
- **Update prompt:** when the daemon is upgraded, the new GUI build installs in the background
  and a toast asks you to **Reload**. It never swaps builds under an open console on its own.
- **Phone and tablet layout:** below the `lg` breakpoint (1024 px) the sidebar becomes a
  slide-over drawer behind a top bar. Tables scroll horizontally inside their cards, modals
  are height-capped with a scrolling body, and safe-area insets are respected.

## Caching contract

The service worker (`web/src/pwa/sw.js`, with its routing policy in `web/src/pwa/swRouting.js`)
applies these rules:

| Request | Strategy |
|---|---|
| Anything under `/api/` (REST, SSE `/events/stream`, console websockets, uploads, `/api/docs`) | **Never touched.** Always goes to the network. |
| Non-GET, cross-origin (e.g. Google Fonts) | Never touched |
| Page navigations | Network-first, falling back to the precached `index.html` |
| `/assets/*` (Vite content-hashed output) | Cache-first |
| Other same-origin static files (icons, manifest, mascot) | Stale-while-revalidate |

API responses are never cached. They are live and depend on the API key, so a cache would show
stale fleet state or leak data between keys. The offline experience is therefore "the app opens
and tells you it is offline", not "browse stale data".

The build plugin (`web/build/swPlugin.js`) derives the precache list from the build output.
Media over 512 KiB (the sidebar mascot) is left out of the precache and cached the first time
it is used. The service-worker version is a content hash of every precached file, so any
change to the shipped bytes triggers an update.

The daemon's embedded file server (`internal/web/embed.go`) serves these headers:

| Path | Cache-Control | Notes |
|---|---|---|
| `/assets/*` | `public, max-age=31536000, immutable` | A missing `/assets/*` path returns 404, never the HTML shell |
| `/sw.js` | `no-cache` | Also sends `Service-Worker-Allowed: /` |
| `index.html`, SPA routes, manifest, icons | `no-cache` | The manifest is served as `application/manifest+json` |

## Keeping the PWA up to date

Because the PWA *is* the GUI, feature parity needs no extra work. Follow these rules when you
change the frontend:

1. **Add new top-level sections to `web/src/navigation.js`** (`NAV_ITEMS`). The sidebar, the
   mobile drawer, the mobile title bar, and the mobile no-horizontal-scroll test all read
   from it. Manifest `shortcuts` must point at a `NAV_ITEMS` route, and
   `web/tests/pwa-manifest.test.js` enforces this.
2. **Don't make the service worker handle `/api/`.** If a feature needs offline data, design it
   explicitly: think about per-key isolation, and document it here.
3. **Nothing needs to be registered for caching.** New pages, chunks and vendored bundles are
   precached automatically.
4. **Build responsive layouts.** Let header and action rows wrap (`flex-wrap`) and keep wide
   content inside a scroll container. The mobile GUI tests fail on any page whose `<main>`
   scrolls horizontally at 390 px.
5. **Don't register the service worker in dev.** `main.jsx` only starts it when
   `import.meta.env.PROD` is set, because a worker would interfere with Vite HMR.

## Tests

| Layer | Command | Covers |
|---|---|---|
| Frontend unit (node:test) | `make test-web-unit` | Routing policy, precache selection and build id, the registration/update/install/offline controller, and manifest ↔ navigation consistency |
| Go | `go test ./internal/web/` | Cache and content-type headers, 404 for missing assets, SPA fallback |
| Browser (Playwright, mock server) | `make test-web` | Manifest served and linked, service worker controls the page, shell precached with no `/api` entries, offline reload shows the banner, Settings → App status, mobile drawer, and no horizontal scroll on any section at 390 px |

## Icons

The PNG icons in `web/public/icons/` are committed. To regenerate them after changing the
artwork in `web/build/render-icons.mjs`:

```bash
PW_CHROMIUM_PATH=/path/to/chromium node web/build/render-icons.mjs   # PW_CHROMIUM_PATH optional
```
