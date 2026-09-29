#!/usr/bin/env node
// Regenerates the PWA icons in web/public/icons/ from the SVG sources below.
// The PNGs are committed, so this only needs to run when the artwork changes:
//
//   node web/build/render-icons.mjs
//
// Uses the repo-root Playwright dependency (already required by the GUI
// tests) to rasterise, so no image tooling needs to be installed.

import { chromium } from 'playwright';
import { writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const OUT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../public/icons');

// Lucide "monitor" glyph (the sidebar logo), 24x24 viewBox.
const GLYPH = `
  <rect width="20" height="14" x="2" y="3" rx="2"/>
  <line x1="8" x2="16" y1="21" y2="21"/>
  <line x1="12" x2="12" y1="17" y2="21"/>`;

// iconSvg draws the glyph scaled to `glyphFraction` of the canvas.
// `rounded` = standalone icon with its own tile; maskable icons are
// full-bleed so the platform mask can crop them (glyph stays inside the
// 80% safe zone).
function iconSvg({ rounded, glyphFraction }) {
  const size = 512;
  const glyph = size * glyphFraction;
  const offset = (size - glyph) / 2;
  const scale = glyph / 24;
  const radius = rounded ? 112 : 0;
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" width="${size}" height="${size}">
  <defs>
    <radialGradient id="bg" cx="30%" cy="25%" r="90%">
      <stop offset="0" stop-color="#0d1a0d"/>
      <stop offset="1" stop-color="#020802"/>
    </radialGradient>
    <filter id="glow" x="-30%" y="-30%" width="160%" height="160%">
      <feGaussianBlur stdDeviation="0.6" result="b"/>
      <feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge>
    </filter>
  </defs>
  <rect width="${size}" height="${size}" rx="${radius}" fill="url(#bg)"/>
  ${rounded ? `<rect x="8" y="8" width="${size - 16}" height="${size - 16}" rx="${radius - 8}" fill="none" stroke="#007a1f" stroke-width="8"/>` : ''}
  <g transform="translate(${offset} ${offset}) scale(${scale})" fill="none" stroke="#00ff41"
     stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" filter="url(#glow)">${GLYPH}
  </g>
</svg>`;
}

const ANY = iconSvg({ rounded: true, glyphFraction: 0.56 });
const MASKABLE = iconSvg({ rounded: false, glyphFraction: 0.5 });

const TARGETS = [
  { file: 'icon-192.png', svg: ANY, size: 192 },
  { file: 'icon-512.png', svg: ANY, size: 512 },
  { file: 'icon-maskable-512.png', svg: MASKABLE, size: 512 },
  // iOS applies its own rounding and ignores transparency: use full-bleed art.
  { file: 'apple-touch-icon.png', svg: MASKABLE, size: 180 },
];

const browser = await chromium.launch({ executablePath: process.env.PW_CHROMIUM_PATH || undefined });
try {
  const page = await browser.newPage();
  for (const { file, svg, size } of TARGETS) {
    await page.setViewportSize({ width: size, height: size });
    await page.setContent(
      `<html><body style="margin:0;background:transparent">${svg.replace(/width="512" height="512"/, `width="${size}" height="${size}"`)}</body></html>`,
    );
    const png = await page.locator('svg').screenshot({ omitBackground: true });
    await writeFile(path.join(OUT, file), png);
    console.log(`wrote ${file}`);
  }
  await writeFile(path.join(OUT, 'icon.svg'), `${ANY}\n`);
  console.log('wrote icon.svg');
} finally {
  await browser.close();
}
