// Vite plugin that emits /sw.js after every production build.
//
// It runs in closeBundle (after Vite has written the bundle AND copied
// public/), walks the output directory, derives the precache list and a
// content-addressed build id, and bundles src/pwa/sw.js into a classic
// script with esbuild (already a Vite dependency — no extra packages).
// Because the precache list is derived from the build output, new pages,
// chunks, and vendored bundles are picked up automatically; nothing needs
// to be registered by hand when the GUI grows.

import { build } from 'esbuild';
import { readdir, readFile, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { computeBuildId, selectPrecacheFiles } from './precache.js';

async function listFiles(root, dir = root) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...await listFiles(root, full));
    else if (entry.isFile()) {
      out.push({ path: path.relative(root, full).split(path.sep).join('/'), size: (await stat(full)).size });
    }
  }
  return out;
}

export default function vmsmithServiceWorker({ entry }) {
  let outDir;
  return {
    name: 'vmsmith-service-worker',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir);
    },
    async closeBundle() {
      const precache = selectPrecacheFiles(await listFiles(outDir));
      const entries = await Promise.all(precache.map(async (url) => ({
        url,
        content: await readFile(path.join(outDir, url.slice(1))),
      })));
      const buildId = computeBuildId(entries);

      const result = await build({
        entryPoints: [entry],
        bundle: true,
        format: 'iife',
        target: 'es2019',
        minify: true,
        write: false,
        define: {
          __VMSMITH_BUILD_ID__: JSON.stringify(buildId),
          __VMSMITH_PRECACHE__: JSON.stringify(precache),
        },
      });
      await writeFile(path.join(outDir, 'sw.js'), result.outputFiles[0].contents);
      this.info?.(`sw.js: build ${buildId}, ${precache.length} precached files`);
    },
  };
}
