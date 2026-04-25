#!/usr/bin/env node
const { chromium } = require('playwright');
const http = require('http');
const fs = require('fs');
const path = require('path');

const HOST = '127.0.0.1';
const PORT = 4174;
const BASE = `http://${HOST}:${PORT}`;
const DIST_DIR = path.join(__dirname, '..', '..', 'internal', 'web', 'dist');

function contentType(filePath) {
  if (filePath.endsWith('.html')) return 'text/html; charset=utf-8';
  if (filePath.endsWith('.js')) return 'application/javascript; charset=utf-8';
  if (filePath.endsWith('.css')) return 'text/css; charset=utf-8';
  if (filePath.endsWith('.png')) return 'image/png';
  if (filePath.endsWith('.svg')) return 'image/svg+xml';
  if (filePath.endsWith('.json')) return 'application/json; charset=utf-8';
  return 'application/octet-stream';
}

function createStaticServer() {
  return http.createServer((req, res) => {
    const url = new URL(req.url, BASE);
    if (url.pathname.startsWith('/api/')) {
      res.writeHead(404, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ error: `unmocked API path: ${url.pathname}` }));
      return;
    }

    let filePath = path.join(DIST_DIR, url.pathname.replace(/^\//, ''));
    if (url.pathname === '/' || !path.extname(url.pathname)) {
      filePath = path.join(DIST_DIR, 'index.html');
    }

    if (!filePath.startsWith(DIST_DIR) || !fs.existsSync(filePath)) {
      filePath = path.join(DIST_DIR, 'index.html');
    }

    res.writeHead(200, { 'content-type': contentType(filePath) });
    fs.createReadStream(filePath).pipe(res);
  });
}

async function expectVisible(locator, message) {
  try {
    await locator.waitFor({ state: 'visible', timeout: 5000 });
  } catch (err) {
    throw new Error(message || err.message);
  }
}

async function runTest(name, fn) {
  try {
    await fn();
    console.log(`  ✓ ${name}`);
  } catch (err) {
    console.log(`  ✗ ${name}`);
    console.log(`    ${err.message}`);
    throw err;
  }
}

(async () => {
  if (!fs.existsSync(path.join(DIST_DIR, 'index.html'))) {
    console.error(`Built frontend not found at ${DIST_DIR}. Run 'make web' first.`);
    process.exit(1);
  }

  const server = createStaticServer();
  await new Promise((resolve) => server.listen(PORT, HOST, resolve));

  let browser;
  try {
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    const pageErrors = [];

    page.on('pageerror', (err) => pageErrors.push(err.message));

    await page.route('**/api/v1/vms*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          null,
          {
            id: 'vm-1',
            name: 'broken-spec-vm',
            state: 'running',
            ip: '192.168.100.50',
            created_at: new Date().toISOString(),
          },
          {
            tags: 'not-an-array',
            spec: 'not-an-object',
          },
        ]),
      });
    });

    await page.route('**/api/v1/images*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          {
            id: 'img-1',
            name: 'ubuntu-base',
            path: '/images/ubuntu-base.qcow2',
            format: 'qcow2',
            size_bytes: 1073741824,
            created_at: new Date().toISOString(),
          },
        ]),
      });
    });

    await page.route('**/api/v1/templates*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      });
    });

    await page.route('**/api/v1/host/interfaces', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([
          { name: 'eth0', ips: ['10.0.0.2/24'], mac: '00:11:22:33:44:55', is_up: true, is_physical: true },
          { name: 'vmsmith0', ips: ['192.168.100.1/24'], mac: '52:54:00:00:00:99', is_up: true, is_physical: false },
        ]),
      });
    });

    await page.route('**/api/v1/host/stats*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          cpu: { percentage: 12 },
          ram: { used: 4 * 1024 * 1024 * 1024, total: 8 * 1024 * 1024 * 1024, available: 4 * 1024 * 1024 * 1024 },
          disk: { used: 20 * 1024 * 1024 * 1024, total: 100 * 1024 * 1024 * 1024, available: 80 * 1024 * 1024 * 1024 },
          vm_count: 3,
        }),
      });
    });

    await page.route('**/api/v1/quotas/usage*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          vms: { used: 3, limit: 10 },
          cpus: { used: 2, limit: 16 },
          ram_mb: { used: 2048, limit: 32768 },
          disk_gb: { used: 20, limit: 500 },
        }),
      });
    });

    await runTest('dashboard survives VMs with missing spec data', async () => {
      await page.goto(BASE, { waitUntil: 'networkidle' });
      await expectVisible(page.getByRole('heading', { name: 'Dashboard' }), 'dashboard heading not visible');
      await expectVisible(page.getByText('broken-spec-vm'), 'dashboard did not render the VM row');
      if (pageErrors.length > 0) {
        throw new Error(`unexpected page errors: ${pageErrors.join(' | ')}`);
      }
    });

    await runTest('/vms survives malformed list entries and still opens create modal', async () => {
      await page.goto(`${BASE}/vms`, { waitUntil: 'networkidle' });
      await expectVisible(page.getByRole('heading', { name: 'Machines' }), '/vms heading not visible');
      await expectVisible(page.getByText('broken-spec-vm'), 'VM card did not render');
      await expectVisible(page.getByText('unnamed-vm'), 'fallback VM name did not render');
      await page.getByRole('button', { name: /new machine/i }).click();
      await expectVisible(page.getByText('Create Machine'), 'create modal did not open');
      await expectVisible(page.getByTestId('input-vm-name'), 'name input missing');
      if (pageErrors.length > 0) {
        throw new Error(`unexpected page errors: ${pageErrors.join(' | ')}`);
      }
    });

    console.log('\nBuilt frontend regression checks passed.');
  } finally {
    if (browser) await browser.close().catch(() => {});
    await new Promise((resolve) => server.close(resolve));
  }
})().catch((err) => {
  console.error(`\nBuilt frontend regression checks failed: ${err.message}`);
  process.exit(1);
});
