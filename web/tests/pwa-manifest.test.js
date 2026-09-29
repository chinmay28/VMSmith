import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { NAV_ITEMS, sectionTitle } from '../src/navigation.js';

const WEB = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const manifest = JSON.parse(readFileSync(path.join(WEB, 'public/manifest.webmanifest'), 'utf8'));
const indexHtml = readFileSync(path.join(WEB, 'index.html'), 'utf8');

test('manifest meets installability basics', () => {
  assert.equal(manifest.start_url, '/');
  assert.equal(manifest.scope, '/');
  assert.equal(manifest.display, 'standalone');
  assert.ok(manifest.name && manifest.short_name);
  const sizes = manifest.icons.filter((i) => i.type === 'image/png').map((i) => i.sizes);
  assert.ok(sizes.includes('192x192'), '192px PNG icon');
  assert.ok(sizes.includes('512x512'), '512px PNG icon');
  assert.ok(manifest.icons.some((i) => i.purpose === 'maskable'), 'maskable icon');
});

test('every icon referenced by the manifest and index.html exists in public/', () => {
  const refs = [
    ...manifest.icons.map((i) => i.src),
    ...manifest.shortcuts.flatMap((s) => (s.icons || []).map((i) => i.src)),
    ...[...indexHtml.matchAll(/href="(\/icons\/[^"]+)"/g)].map((m) => m[1]),
  ];
  assert.ok(refs.length > 0);
  for (const src of refs) {
    assert.ok(existsSync(path.join(WEB, 'public', src)), `missing ${src}`);
  }
});

test('index.html links the manifest and theme colour', () => {
  assert.match(indexHtml, /<link rel="manifest" href="\/manifest\.webmanifest"/);
  assert.match(indexHtml, new RegExp(`<meta name="theme-color" content="${manifest.theme_color}"`));
  assert.match(indexHtml, /viewport-fit=cover/);
});

test('manifest shortcuts only point at real navigation sections', () => {
  const routes = new Set(NAV_ITEMS.map((i) => i.to));
  for (const s of manifest.shortcuts) {
    assert.ok(routes.has(s.url), `shortcut ${s.name} -> ${s.url} is not in NAV_ITEMS`);
  }
});

test('NAV_ITEMS routes and test ids are unique', () => {
  assert.equal(new Set(NAV_ITEMS.map((i) => i.to)).size, NAV_ITEMS.length);
  assert.equal(new Set(NAV_ITEMS.map((i) => i.testId)).size, NAV_ITEMS.length);
});

test('sectionTitle picks the owning section for nested routes', () => {
  assert.equal(sectionTitle('/'), 'Dashboard');
  assert.equal(sectionTitle('/vms'), 'Machines');
  assert.equal(sectionTitle('/vms/vm-123'), 'Machines');
  assert.equal(sectionTitle('/settings'), 'Settings');
  assert.equal(sectionTitle('/unknown'), 'Dashboard');
  assert.equal(sectionTitle('/vmsx'), 'Dashboard');
});
