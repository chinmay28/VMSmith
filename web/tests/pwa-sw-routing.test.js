import test from 'node:test';
import assert from 'node:assert/strict';
import {
  RUNTIME_CACHE,
  Strategy,
  classifyRequest,
  isApiPath,
  isCacheableResponse,
  shellCacheName,
  staleCacheNames,
} from '../src/pwa/swRouting.js';

const ORIGIN = 'https://vmsmith.example:8443';
const get = (path, mode = 'cors') => ({ method: 'GET', url: `${ORIGIN}${path}`, mode });

test('API traffic is never handled by the service worker', () => {
  for (const path of [
    '/api/v1/vms',
    '/api/v1/events/stream',
    '/api/v1/vms/vm-1/console?ticket=abc&intent=vnc',
    '/api/version',
    '/api/docs',
    '/api',
  ]) {
    assert.equal(classifyRequest(get(path), ORIGIN), Strategy.BYPASS, path);
    assert.equal(classifyRequest(get(path, 'navigate'), ORIGIN), Strategy.BYPASS, `${path} (navigate)`);
  }
});

test('non-GET requests bypass (uploads, lifecycle actions)', () => {
  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE', 'HEAD']) {
    assert.equal(classifyRequest({ method, url: `${ORIGIN}/`, mode: 'navigate' }, ORIGIN), Strategy.BYPASS, method);
  }
});

test('cross-origin requests bypass', () => {
  const req = { method: 'GET', url: 'https://fonts.gstatic.com/s/dmsans.woff2', mode: 'cors' };
  assert.equal(classifyRequest(req, ORIGIN), Strategy.BYPASS);
});

test('the worker script itself bypasses', () => {
  assert.equal(classifyRequest(get('/sw.js'), ORIGIN), Strategy.BYPASS);
});

test('SPA navigations are network-first with shell fallback', () => {
  for (const path of ['/', '/vms', '/vms/vm-1/console', '/schedules?page=2']) {
    assert.equal(classifyRequest(get(path, 'navigate'), ORIGIN), Strategy.NAVIGATION, path);
  }
});

test('hashed build assets are cache-first; other static files are SWR', () => {
  assert.equal(classifyRequest(get('/assets/index-abc123.js'), ORIGIN), Strategy.CACHE_FIRST);
  assert.equal(classifyRequest(get('/icons/icon-192.png'), ORIGIN), Strategy.STALE_WHILE_REVALIDATE);
  assert.equal(classifyRequest(get('/manifest.webmanifest'), ORIGIN), Strategy.STALE_WHILE_REVALIDATE);
});

test('malformed input bypasses instead of throwing', () => {
  assert.equal(classifyRequest(null, ORIGIN), Strategy.BYPASS);
  assert.equal(classifyRequest({ method: 'GET', url: 'not a url' }, ORIGIN), Strategy.BYPASS);
});

test('isApiPath does not match look-alike prefixes', () => {
  assert.equal(isApiPath('/api/v1/vms'), true);
  assert.equal(isApiPath('/apiary'), false);
  assert.equal(isApiPath('/assets/api.js'), false);
});

test('staleCacheNames keeps the current shell + runtime and ignores foreign caches', () => {
  const current = shellCacheName('b2');
  const stale = staleCacheNames(
    ['vmsmith-shell-b1', current, RUNTIME_CACHE, 'some-other-app', 'vmsmith-shell-b0'],
    'b2',
  );
  assert.deepEqual(stale.sort(), ['vmsmith-shell-b0', 'vmsmith-shell-b1']);
});

test('shellCacheName requires a build id', () => {
  assert.throws(() => shellCacheName(''), /buildId/);
});

test('isCacheableResponse only accepts complete same-origin 200s', () => {
  assert.equal(isCacheableResponse({ status: 200, type: 'basic' }), true);
  assert.equal(isCacheableResponse({ status: 206, type: 'basic' }), false);
  assert.equal(isCacheableResponse({ status: 404, type: 'basic' }), false);
  assert.equal(isCacheableResponse({ status: 200, type: 'opaque' }), false);
  assert.equal(isCacheableResponse(undefined), false);
});
