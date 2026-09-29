/* global self, caches, fetch */
// VM Smith service worker. Bundled to /sw.js as a classic script by
// web/build/swPlugin.js, which substitutes the two build-time constants:
//   __VMSMITH_BUILD_ID__  content hash of the whole build output
//   __VMSMITH_PRECACHE__  absolute URLs of every file in the build output
// The routing policy lives in ./swRouting.js (unit-tested separately).

import {
  RUNTIME_CACHE,
  SHELL_URL,
  Strategy,
  classifyRequest,
  isCacheableResponse,
  shellCacheName,
  staleCacheNames,
} from './swRouting.js';

const BUILD_ID = __VMSMITH_BUILD_ID__;
const PRECACHE = __VMSMITH_PRECACHE__;
const SHELL_CACHE = shellCacheName(BUILD_ID);

self.addEventListener('install', (event) => {
  // cache: 'reload' skips the HTTP cache so a new build never precaches a
  // stale copy of index.html. We deliberately do not skipWaiting() here:
  // the page shows an "update available" prompt and posts SKIP_WAITING
  // when the operator accepts, so an open console session is never swapped
  // out from under them.
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) =>
      cache.addAll(PRECACHE.map((url) => new Request(url, { cache: 'reload' }))),
    ),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((names) => Promise.all(staleCacheNames(names, BUILD_ID).map((name) => caches.delete(name))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('message', (event) => {
  const type = event.data && event.data.type;
  if (type === 'SKIP_WAITING') {
    self.skipWaiting();
  } else if (type === 'GET_BUILD_ID' && event.ports && event.ports[0]) {
    event.ports[0].postMessage({ buildId: BUILD_ID });
  }
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  switch (classifyRequest(request, self.location.origin)) {
    case Strategy.NAVIGATION:
      event.respondWith(networkFirstNavigation(request));
      return;
    case Strategy.CACHE_FIRST:
      event.respondWith(cacheFirst(request));
      return;
    case Strategy.STALE_WHILE_REVALIDATE:
      event.respondWith(staleWhileRevalidate(request, event));
      return;
    default:
      // BYPASS: no respondWith — the browser performs the request natively.
      return;
  }
});

async function networkFirstNavigation(request) {
  try {
    return await fetch(request);
  } catch (err) {
    const shell = await caches.match(SHELL_URL, { cacheName: SHELL_CACHE });
    if (shell) return shell;
    throw err;
  }
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (isCacheableResponse(response)) {
    const cache = await caches.open(RUNTIME_CACHE);
    await cache.put(request, response.clone());
  }
  return response;
}

async function staleWhileRevalidate(request, event) {
  const cached = await caches.match(request);
  const refresh = fetch(request)
    .then(async (response) => {
      if (isCacheableResponse(response)) {
        const cache = await caches.open(RUNTIME_CACHE);
        await cache.put(request, response.clone());
      }
      return response;
    });
  if (cached) {
    event.waitUntil(refresh.catch(() => undefined));
    return cached;
  }
  return refresh;
}
