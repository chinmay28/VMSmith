// Pure helpers used by swPlugin.js to turn the Vite build output into the
// service worker's precache manifest. Unit-tested in
// web/tests/pwa-precache.test.js.

import { createHash } from 'node:crypto';

// Files that are never precached:
//   sw.js         the worker itself (the browser manages its lifecycle)
//   *.map         sourcemaps are debug-only
//   dotfiles      e.g. the .gitkeep CI drops into internal/web/dist
const EXCLUDED = [
  (p) => p === 'sw.js',
  (p) => p.endsWith('.map'),
  (p) => p.split('/').some((seg) => seg.startsWith('.')),
];

// Media larger than this (e.g. the 2 MB sidebar mascot, which is hidden on
// phones) is not downloaded at install time; the worker's cache-first /
// stale-while-revalidate routes still cache it on first use. Code and the
// shell are always precached regardless of size — the app cannot start
// offline without them.
export const MAX_PRECACHE_MEDIA_BYTES = 512 * 1024;
const ALWAYS_PRECACHE = /\.(html|js|mjs|css|webmanifest|json)$/;

// selectPrecacheFiles maps build-output files ({path, size}; path is
// output-relative with forward slashes) to the sorted, de-duplicated
// absolute URLs the worker precaches. index.html is mandatory: it is the
// offline app shell.
export function selectPrecacheFiles(files, { maxMediaBytes = MAX_PRECACHE_MEDIA_BYTES } = {}) {
  const urls = new Set();
  for (const { path: raw, size = 0 } of files) {
    const p = raw.replace(/\\/g, '/').replace(/^\/+/, '');
    if (!p || EXCLUDED.some((rule) => rule(p))) continue;
    if (!ALWAYS_PRECACHE.test(p) && size > maxMediaBytes) continue;
    urls.add(`/${p}`);
  }
  if (!urls.has('/index.html')) {
    throw new Error('precache: build output has no index.html (the offline app shell)');
  }
  return [...urls].sort();
}

// computeBuildId hashes every precached file's path and content, so any
// change to the shipped bytes — including public/ files that Vite copies
// without hashing — produces a new id, a new sw.js, and therefore a browser
// update cycle.
export function computeBuildId(entries) {
  const hash = createHash('sha256');
  const sorted = [...entries].sort((a, b) => (a.url < b.url ? -1 : a.url > b.url ? 1 : 0));
  for (const { url, content } of sorted) {
    hash.update(url);
    hash.update('\0');
    hash.update(content);
    hash.update('\0');
  }
  return hash.digest('hex').slice(0, 16);
}
