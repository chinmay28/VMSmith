import test from 'node:test';
import assert from 'node:assert/strict';
import { MAX_PRECACHE_MEDIA_BYTES, computeBuildId, selectPrecacheFiles } from '../build/precache.js';

const f = (path, size = 100) => ({ path, size });

test('selectPrecacheFiles returns sorted absolute URLs', () => {
  const urls = selectPrecacheFiles([f('index.html'), f('assets/b.js'), f('assets/a.css'), f('icons/icon.svg')]);
  assert.deepEqual(urls, ['/assets/a.css', '/assets/b.js', '/icons/icon.svg', '/index.html']);
});

test('selectPrecacheFiles excludes sw.js, sourcemaps, and dotfiles', () => {
  const urls = selectPrecacheFiles([
    f('index.html'), f('sw.js'), f('assets/index.js.map'), f('.gitkeep'), f('assets/.cache/x.js'),
  ]);
  assert.deepEqual(urls, ['/index.html']);
});

test('selectPrecacheFiles skips large media but never code', () => {
  const big = MAX_PRECACHE_MEDIA_BYTES + 1;
  const urls = selectPrecacheFiles([
    f('index.html'), f('assets/mascot.png', big), f('assets/index.js', big * 4), f('assets/index.css', big),
    f('icons/icon-512.png', 20_000),
  ]);
  assert.deepEqual(urls, ['/assets/index.css', '/assets/index.js', '/icons/icon-512.png', '/index.html']);
});

test('selectPrecacheFiles normalises separators and leading slashes, de-duplicating', () => {
  const urls = selectPrecacheFiles([f('index.html'), f('/index.html'), f('assets\\x.js')]);
  assert.deepEqual(urls, ['/assets/x.js', '/index.html']);
});

test('selectPrecacheFiles requires the index.html app shell', () => {
  assert.throws(() => selectPrecacheFiles([f('assets/x.js')]), /index\.html/);
});

test('computeBuildId is order-independent and content-sensitive', () => {
  const a = { url: '/index.html', content: Buffer.from('<html>1') };
  const b = { url: '/manifest.webmanifest', content: Buffer.from('{}') };
  const id = computeBuildId([a, b]);
  assert.match(id, /^[0-9a-f]{16}$/);
  assert.equal(computeBuildId([b, a]), id);
  assert.notEqual(computeBuildId([a, { ...b, content: Buffer.from('{"x":1}') }]), id);
  // Path participates too: moving bytes to another URL is a new build.
  assert.notEqual(computeBuildId([a, { ...b, url: '/other.json' }]), id);
});
