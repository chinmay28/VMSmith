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

    await page.route(/\/api\/v1\/vms(?:\/.*)?(?:\?.*)?$/, async (route) => {
      const url = new URL(route.request().url());
      if (url.pathname === '/api/v1/vms') {
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
        return;
      }

      if (url.pathname === '/api/v1/vms/vm-1') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            id: 'vm-1',
            name: 'broken-spec-vm',
            state: 'running',
            ip: '192.168.100.50',
            created_at: new Date().toISOString(),
            spec: 'not-an-object',
            tags: 'not-an-array',
          }),
        });
        return;
      }

      await route.fulfill({
        status: 404,
        contentType: 'application/json',
        body: JSON.stringify({ error: `unmocked VM API path: ${url.pathname}` }),
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

    await page.route(/\/api\/v1\/vms\/[^/]+\/snapshots(?:\/.*)?(?:\?.*)?$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: 'null',
      });
    });

    await page.route(/\/api\/v1\/vms\/[^/]+\/ports(?:\/.*)?(?:\?.*)?$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: 'null',
      });
    });

    await page.route('**/api/v1/templates*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: 'null',
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

    await runTest('/vms/:id survives malformed spec data and opens edit modal', async () => {
      await page.goto(`${BASE}/vms/vm-1`, { waitUntil: 'networkidle' });
      const editButton = page.getByTestId('btn-edit-vm');
      await expectVisible(editButton, 'detail edit button not visible');
      await editButton.click();
      await expectVisible(page.getByTestId('input-edit-cpus'), 'edit modal did not open');
      if (pageErrors.length > 0) {
        throw new Error(`unexpected page errors: ${pageErrors.join(' | ')}`);
      }
    });

    await runTest('edit modal preserves typed CPU/RAM through background polling and PATCHes them', async () => {
      const patches = [];

      // Override the catch-all VM route for vm-2 so we can serve a well-formed
      // VM payload and capture the eventual PATCH body. Routes registered later
      // win in Playwright, so this takes precedence over the malformed-spec
      // route added above.
      await page.route('**/api/v1/vms/vm-2', async (route, request) => {
        if (request.method() === 'GET') {
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
              id: 'vm-2',
              name: 'edit-test-vm',
              state: 'running',
              ip: '192.168.100.51',
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
              spec: {
                name: 'edit-test-vm',
                image: 'ubuntu-22.04',
                cpus: 2,
                ram_mb: 4096,
                disk_gb: 20,
              },
              tags: [],
              description: '',
            }),
          });
          return;
        }
        if (request.method() === 'PATCH') {
          let body = {};
          try { body = JSON.parse(request.postData() || '{}'); } catch { /* keep {} */ }
          patches.push(body);
          await route.fulfill({
            status: 200,
            contentType: 'application/json',
            body: JSON.stringify({
              id: 'vm-2',
              name: 'edit-test-vm',
              state: 'running',
              ip: '192.168.100.51',
              created_at: new Date().toISOString(),
              updated_at: new Date().toISOString(),
              spec: {
                name: 'edit-test-vm',
                image: 'ubuntu-22.04',
                cpus: body.cpus || 2,
                ram_mb: body.ram_mb || 4096,
                disk_gb: body.disk_gb || 20,
              },
              tags: [],
              description: '',
            }),
          });
          return;
        }
        await route.fulfill({ status: 405, contentType: 'application/json', body: '{}' });
      });

      await page.goto(`${BASE}/vms/vm-2`, { waitUntil: 'networkidle' });

      const editButton = page.getByTestId('btn-edit-vm');
      await expectVisible(editButton, 'edit button not visible');
      await editButton.click();

      const cpuInput = page.getByTestId('input-edit-cpus');
      const ramInput = page.getByTestId('input-edit-ram');
      await expectVisible(cpuInput, 'edit modal did not open');

      const initialCpu = await cpuInput.inputValue();
      const initialRam = await ramInput.inputValue();
      if (initialCpu !== '2') throw new Error(`pre-fill cpus = "${initialCpu}", want "2"`);
      if (initialRam !== '4096') throw new Error(`pre-fill ram_mb = "${initialRam}", want "4096"`);

      await cpuInput.fill('8');
      await ramInput.fill('16384');

      // VMDetail polls vms.get every 5s. Wait long enough for at least one
      // background refresh to fire while the modal is open. Before the fix
      // the form-init effect re-ran on every `vm` prop change and reset
      // these inputs back to the API's current values, so the diff check
      // in handleSubmit silently dropped cpus/ram_mb from the PATCH body.
      await page.waitForTimeout(6000);

      const cpuAfterPoll = await cpuInput.inputValue();
      const ramAfterPoll = await ramInput.inputValue();
      if (cpuAfterPoll !== '8') {
        throw new Error(`cpus input reset to "${cpuAfterPoll}" by polling refresh, want "8"`);
      }
      if (ramAfterPoll !== '16384') {
        throw new Error(`ram input reset to "${ramAfterPoll}" by polling refresh, want "16384"`);
      }

      await page.getByTestId('btn-submit-edit').click();
      await page.getByTestId('input-edit-cpus').waitFor({ state: 'hidden', timeout: 5000 });

      if (patches.length === 0) throw new Error('no PATCH /vms/vm-2 captured');
      const last = patches[patches.length - 1];
      if (last.cpus !== 8) throw new Error(`PATCH cpus = ${JSON.stringify(last.cpus)}, want 8`);
      if (last.ram_mb !== 16384) throw new Error(`PATCH ram_mb = ${JSON.stringify(last.ram_mb)}, want 16384`);

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
