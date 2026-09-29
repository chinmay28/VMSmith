// Pure request-routing and cache-housekeeping rules for the VM Smith
// service worker. Kept free of any ServiceWorkerGlobalScope references so the
// policy can be unit-tested under plain Node (web/tests/pwa-sw-routing.test.js)
// and bundled into public sw.js by web/build/swPlugin.js.
//
// Contract (see docs/PWA.md):
//   - /api/* is NEVER handled by the service worker. Responses are live,
//     auth-bearing, and include SSE streams, websockets, and uploads —
//     caching any of them would serve stale fleet state or leak data across
//     API keys.
//   - Navigations are network-first and fall back to the precached app shell,
//     so the installed app opens (and shows the offline banner) when the
//     daemon is unreachable.
//   - Content-hashed build output under /assets/ is cache-first: a hash
//     change is a new URL, so a cached entry can never be stale.
//   - Every other same-origin GET (icons, manifest, mascot) is
//     stale-while-revalidate.

export const Strategy = Object.freeze({
  BYPASS: 'bypass',
  NAVIGATION: 'navigation',
  CACHE_FIRST: 'cache-first',
  STALE_WHILE_REVALIDATE: 'stale-while-revalidate',
});

export const CACHE_PREFIX = 'vmsmith-';
export const RUNTIME_CACHE = `${CACHE_PREFIX}runtime`;
export const SHELL_URL = '/index.html';
export const SW_URL = '/sw.js';

export function shellCacheName(buildId) {
  if (!buildId) throw new Error('shellCacheName: buildId is required');
  return `${CACHE_PREFIX}shell-${buildId}`;
}

export function isApiPath(pathname) {
  return pathname === '/api' || pathname.startsWith('/api/');
}

// classifyRequest decides how the service worker treats one request.
// `req` only needs {method, url, mode}; `origin` is the service worker's
// own origin (self.location.origin).
export function classifyRequest(req, origin) {
  if (!req || req.method !== 'GET') return Strategy.BYPASS;

  let url;
  try {
    url = new URL(req.url);
  } catch {
    return Strategy.BYPASS;
  }

  // Cross-origin (Google Fonts, etc.) is left to the browser HTTP cache.
  if (url.origin !== origin) return Strategy.BYPASS;
  if (isApiPath(url.pathname)) return Strategy.BYPASS;
  if (url.pathname === SW_URL) return Strategy.BYPASS;

  if (req.mode === 'navigate') return Strategy.NAVIGATION;
  if (url.pathname.startsWith('/assets/')) return Strategy.CACHE_FIRST;
  return Strategy.STALE_WHILE_REVALIDATE;
}

// staleCacheNames returns the VM Smith caches that should be dropped when a
// new service worker activates: every cache we own except the current shell
// cache and the runtime cache. Caches with other prefixes are never touched.
export function staleCacheNames(existing, buildId) {
  const keep = new Set([shellCacheName(buildId), RUNTIME_CACHE]);
  return existing.filter((name) => name.startsWith(CACHE_PREFIX) && !keep.has(name));
}

// isCacheableResponse guards what we are willing to persist: complete,
// successful, same-origin ("basic") responses only.
export function isCacheableResponse(res) {
  return Boolean(res) && res.status === 200 && res.type === 'basic';
}
