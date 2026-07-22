#!/usr/bin/env node
// tests/web/run-gui-tests.js
// Standalone E2E test runner using Playwright's chromium API.
// Usage: node tests/web/run-gui-tests.js

// Import playwright-core for browser automation
const { chromium } = require("playwright");
const { execSync, spawn } = require("child_process");
const fs = require("fs");
const path = require("path");

const REPO_ROOT = path.resolve(__dirname, "../..");
const WEB_DIR = path.join(REPO_ROOT, "web");
const DIST_INDEX = path.join(REPO_ROOT, "internal", "web", "dist", "index.html");

const BASE = "http://localhost:4173";
let passed = 0;
let failed = 0;
const errors = [];

// ============================================================
// Minimal test framework
// ============================================================
async function assert(condition, message) {
  if (!condition) {
    throw new Error(`Assertion failed: ${message}`);
  }
}

async function assertVisible(page, testId, msg) {
  const el = page.locator(`[data-testid="${testId}"]`);
  const visible = await el.isVisible({ timeout: 5000 }).catch(() => false);
  if (!visible) throw new Error(`${msg || testId} not visible`);
}

async function assertText(page, testId, expected) {
  const el = page.locator(`[data-testid="${testId}"]`);
  const text = await el.textContent({ timeout: 5000 });
  if (!text.includes(expected)) {
    throw new Error(`${testId}: expected "${expected}", got "${text}"`);
  }
}

async function assertNotVisible(page, testId) {
  const el = page.locator(`[data-testid="${testId}"]`);
  // Wait briefly then check
  await page.waitForTimeout(500);
  const visible = await el.isVisible().catch(() => false);
  if (visible) throw new Error(`${testId} should not be visible`);
}

async function dismissOpenModal(page) {
  await page.keyboard.press("Escape").catch(() => {});
  await page.waitForTimeout(150);
  const closeButton = page
    .locator('button[aria-label="Close modal"], [data-testid^="btn-cancel-"]')
    .first();
  if (await closeButton.isVisible().catch(() => false)) {
    await closeButton.click().catch(() => {});
    await page.waitForTimeout(150);
  }
}

// PR #414 collapsed FilterPanel by default and split VM detail into tabs.
// `openFilterPanel` expands a collapsed FilterPanel so its inputs render in
// the DOM; `openVMTab` switches the VM detail page to a non-overview tab
// (snapshots / ports / schedules / metrics / activity).
async function openFilterPanel(page, panelTestId) {
  const toggle = page.locator(`[data-testid="${panelTestId}-toggle"]`);
  await toggle.waitFor({ state: "visible", timeout: 5000 });
  const expanded = await toggle.getAttribute("aria-expanded");
  if (expanded !== "true") {
    await toggle.click();
    await page.waitForTimeout(150);
  }
}

async function openVMTab(page, tab) {
  const el = page.locator(`[data-testid="tab-${tab}"]`);
  await el.waitFor({ state: "visible", timeout: 5000 });
  await el.click();
  await page.waitForTimeout(200);
}

async function runTest(name, fn, page) {
  try {
    await dismissOpenModal(page);
    await fn(page);
    passed++;
    console.log(`  ✓ ${name}`);
  } catch (err) {
    failed++;
    errors.push({ name, error: err.message });
    console.log(`  ✗ ${name}`);
    console.log(`    ${err.message}`);
  } finally {
    await dismissOpenModal(page).catch(() => {});
  }
}

// ============================================================
// Tests
// ============================================================
async function main() {
  console.log("Building frontend bundle for GUI tests...");
  execSync("npm run build", {
    cwd: WEB_DIR,
    stdio: "inherit",
  });

  if (!fs.existsSync(DIST_INDEX)) {
    throw new Error(`Frontend bundle missing after build: ${DIST_INDEX}`);
  }

  // Start mock server
  const server = spawn("node", [path.join(__dirname, "mock-server.js")], {
    stdio: ["ignore", "pipe", "pipe"],
    env: { ...process.env, PORT: "4173" },
  });

  // Wait for server to start
  await new Promise((resolve) => {
    server.stdout.on("data", (data) => {
      if (data.toString().includes("Mock vmSmith server")) resolve();
    });
    setTimeout(resolve, 3000);
  });

  let browser;
  try {
    browser = await chromium.launch({ headless: true, executablePath: process.env.PW_CHROMIUM_PATH || undefined });
    const context = await browser.newContext();

    // ================== Dashboard Tests ==================
    console.log("\nDashboard:");

    let page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(1000);

    await runTest("shows stats on load", async (p) => {
      // 5.6.8: seed now carries 3 VMs (web-server running, db-server stopped,
      // win-app running) and 2 images. The Windows VM lets the os-type
      // filter narrow meaningfully on every list endpoint.
      await assertText(p, "stat-total", "3");
      await assertText(p, "stat-running", "2");
      await assertText(p, "stat-images", "2");
    }, page);

    await runTest("displays seeded VMs in table", async (p) => {
      await assertVisible(p, "vm-row-web-server");
      await assertVisible(p, "vm-row-db-server");
    }, page);

    // 5.7.11 — GPU quota card. The mock server seeds win-app with one
    // passthrough GPU ("0000:01:00.0") and the other VMs with none, so
    // GET /api/v1/quotas/usage now reports gpus.used = 1 / limit = 0.
    // The Dashboard renders a fifth QuotaCard for the new GPU dimension
    // that must surface the seeded count and the "uncapped" subtitle.
    await runTest("gpu quota card on dashboard", async (p) => {
      await assertVisible(p, "quota-card-gpus");
      const text = await p.locator('[data-testid="quota-card-gpus"]').textContent();
      await assert(
        text && text.includes("1") && text.includes("GPUs"),
        `quota-card-gpus expected to surface seeded used=1 GPUs, got: ${text}`,
      );
      await assert(
        text && text.includes("uncapped"),
        `quota-card-gpus expected the "uncapped" subtitle when limit=0, got: ${text}`,
      );
    }, page);

    await runTest("layout footer shows mock build version", async (p) => {
      await assertVisible(p, "layout-version");
      await assertText(p, "layout-version", "VM Smith v0.0.0-mock");
    }, page);

    await runTest("clicking VM row navigates to detail", async (p) => {
      await p.locator('[data-testid="vm-row-web-server"]').click();
      await p.waitForTimeout(500);
      await assertText(p, "vm-detail-name", "web-server");
    }, page);

    await page.close();

    // ----- Top VMs leaderboard -----
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(1000);

    await runTest("renders top VMs leaderboard", async (p) => {
      await assertVisible(p, "top-vms-card");
      await assertVisible(p, "top-vms-table");
      await assertVisible(p, "top-vm-row-web-server");
    }, page);

    await runTest("changing the metric reloads the leaderboard", async (p) => {
      await p.locator('[data-testid="top-vms-metric"]').selectOption("mem");
      await p.waitForTimeout(500);
      await assertVisible(p, "top-vms-table");
      await assertVisible(p, "top-vm-row-web-server");
    }, page);

    await runTest("clicking a top-VM row opens its detail page", async (p) => {
      await p.locator('[data-testid="top-vm-row-web-server"]').click();
      await p.waitForTimeout(500);
      await assertText(p, "vm-detail-name", "web-server");
    }, page);

    await page.close();

    // Empty leaderboard state — intercept the endpoint and return zero items.
    page = await context.newPage();
    await page.route("**/api/v1/vms/stats/top*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ metric: "cpu", limit: 5, state: "running", items: [] }),
      });
    });
    await page.goto(BASE);
    await page.waitForTimeout(1000);

    await runTest("shows empty state when no VMs reported metrics", async (p) => {
      await assertVisible(p, "top-vms-empty");
    }, page);

    await page.close();

    page = await context.newPage();
    await page.route("**/api/v1/vms*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "application/json",
          "X-Total-Count": "42",
        },
        body: JSON.stringify([
          {
            id: "vm-1",
            name: "web-server",
            state: "running",
            ip: "192.168.100.10",
            spec: { cpus: 2, ram_mb: 4096 },
          },
        ]),
      });
    });
    await page.route("**/api/v1/images*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "application/json",
          "X-Total-Count": "7",
        },
        body: JSON.stringify([
          {
            id: "img-1",
            name: "ubuntu-base",
            path: "/images/ubuntu-base.qcow2",
            format: "qcow2",
            size_bytes: 1073741824,
            created_at: new Date().toISOString(),
          },
        ]),
      });
    });
    await page.goto(BASE);
    await page.waitForTimeout(1000);

    await runTest("uses pagination metadata for dashboard totals", async (p) => {
      await assertText(p, "stat-total", "42");
      await assertText(p, "stat-running", "1");
      await assertText(p, "stat-images", "7");
    }, page);

    await page.close();

    // ================== VM List Tests ==================
    console.log("\nVM List:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="nav-vms"]').click();
    await page.waitForTimeout(500);

    await runTest("lists VMs with cards", async (p) => {
      await assertVisible(p, "vm-card-web-server");
      await assertVisible(p, "vm-card-db-server");
    }, page);

    await runTest("new VM button opens create modal", async (p) => {
      await p.locator('[data-testid="btn-new-vm"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "input-vm-name");
      await assertVisible(p, "input-vm-image");
      // Close it
      await p.locator('[data-testid="btn-cancel-create"]').click();
      await p.waitForTimeout(300);
    }, page);

    await runTest("create VM flow", async (p) => {
      await p.locator('[data-testid="btn-new-vm"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-vm-name"]').fill("test-new-vm");
      await p.locator('[data-testid="input-vm-image"]').selectOption('/images/ubuntu-base.qcow2');
      await p.locator('[data-testid="input-vm-cpus"]').fill("4");
      await p.locator('[data-testid="input-vm-ram"]').fill("8192");
      await p.locator('[data-testid="btn-submit-create"]').click();
      await p.waitForTimeout(1000);
      await assertNotVisible(p, "input-vm-name"); // modal closed
      await assertVisible(p, "vm-card-test-new-vm"); // new VM in list
    }, page);

    await runTest("advanced tab exposes auto-start checkbox", async (p) => {
      await p.locator('[data-testid="btn-new-vm"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="tab-advanced"]').click();
      await p.waitForTimeout(150);
      await assertVisible(p, "input-vm-auto-start");
      // Cancel out of the modal so subsequent tests start clean.
      await p.keyboard.press("Escape");
      await p.waitForTimeout(200);
    }, page);

    await runTest("gpu passthrough selection surfaces primary-display risk and persists to VM detail", async (p) => {
      // Reload to dismiss any modal still open from the previous test (the
      // "advanced tab" test presses Escape but the modal backdrop can outlive
      // the keypress and intercept the next btn-new-vm click in CI).
      await p.goto(BASE);
      await p.waitForTimeout(300);
      await p.locator('[data-testid="nav-vms"]').click();
      await p.waitForTimeout(300);

      await p.locator('[data-testid="btn-new-vm"]').click();
      await p.waitForTimeout(250);
      await p.locator('[data-testid="input-vm-name"]').fill("gpu-worker");
      await p.locator('[data-testid="input-vm-image"]').selectOption('/images/ubuntu-base.qcow2');
      await p.locator('[data-testid="tab-advanced"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "gpu-option-0000:00:02.0");
      await assertVisible(p, "gpu-checkbox-0000:00:02.0");
      await assertVisible(p, "gpu-checkbox-0000:01:00.0");
      await p.getByText('primary display').first().waitFor({ state: 'visible' });
      await p.locator('[data-testid="gpu-checkbox-0000:01:00.0"]').check();
      await p.waitForTimeout(150);
      await p.locator('[data-testid="btn-submit-create"]').click();
      await p.waitForTimeout(800);
      await assertVisible(p, "vm-card-gpu-worker");
      await p.locator('[data-testid="vm-card-gpu-worker"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "vm-detail-gpus");
      await p.getByText('0000:01:00.0').waitFor({ state: 'visible' });
    }, page);

    // 5.4.22 — image filter narrows the VM list to a single base image and
    // round-trips through the URL.  Seed data: web-server uses image
    // "ubuntu-22.04", db-server uses image "rocky-9".  The image input is
    // a 250 ms-debounced text box; the X clear button restores the
    // unfiltered view.
    await runTest("image filter narrows the VM list and round-trips through the URL", async (p) => {
      // Reload to dismiss any modal still open from the previous test and
      // guarantee a clean URL for the round-trip assertion below.
      await p.goto(BASE);
      await p.waitForTimeout(300);
      await p.locator('[data-testid="nav-vms"]').click();
      await p.waitForTimeout(300);

      await assertVisible(p, "vm-card-web-server");
      await assertVisible(p, "vm-card-db-server");

      // FilterPanel is collapsed by default (PR #414); expand before use.
      await openFilterPanel(p, "vm-list-filters");

      await p.locator('[data-testid="vm-list-image-filter"]').fill("ubuntu-22.04");
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-web-server");
      await assertNotVisible(p, "vm-card-db-server");

      const urlAfter = new URL(p.url());
      await assert(urlAfter.searchParams.get("image") === "ubuntu-22.04",
        `expected ?image=ubuntu-22.04, got ${urlAfter.searchParams.get("image")}`);

      await p.locator('[data-testid="vm-list-image-filter-clear"]').click();
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-web-server");
      await assertVisible(p, "vm-card-db-server");

      const urlCleared = new URL(p.url());
      await assert(!urlCleared.searchParams.has("image"),
        `expected ?image= to be cleared, got ${urlCleared.searchParams.get("image")}`);
    }, page);

    await runTest("image filter is case-insensitive", async (p) => {
      await p.locator('[data-testid="vm-list-image-filter"]').fill("ROCKY-9");
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-db-server");
      await assertNotVisible(p, "vm-card-web-server");
      // Reset for the next test.
      await p.locator('[data-testid="vm-list-image-filter-clear"]').click();
      await p.waitForTimeout(400);
    }, page);

    await runTest("image filter matches no VMs when query has no hits", async (p) => {
      await p.locator('[data-testid="vm-list-image-filter"]').fill("does-not-exist.qcow2");
      await p.waitForTimeout(400);
      await assertNotVisible(p, "vm-card-web-server");
      await assertNotVisible(p, "vm-card-db-server");
      // Reset for any subsequent tests on this page.
      await p.locator('[data-testid="vm-list-image-filter-clear"]').click();
      await p.waitForTimeout(400);
    }, page);

    // 5.7.13 — `sort=gpu` axis on the VM list. Seed data: win-app alone
    // carries `0000:01:00.0`; web-server / db-server leave spec.gpus
    // empty so the no-GPU cohort sinks to the tail in asc and leads in
    // desc, mirroring the nil-trailing contract on every other nullable
    // sort axis (ip / image / guest_ip / actor / last_fired_at).
    await runTest("gpu sort axis reorders the VM list and sinks no-GPU VMs to the tail", async (p) => {
      await openFilterPanel(p, "vm-list-filters");
      await p.locator('[data-testid="vm-list-sort-field"]').selectOption("gpu");
      await p.waitForTimeout(200);
      // Asc: win-app first (only GPU-bearing VM), then web-server (vm-1)
      // and db-server (vm-2) tied on empty-trails-in-asc with id tiebreak.
      const cards1 = await p.locator('[data-testid^="vm-card-"]').all();
      const firstId1 = await cards1[0].getAttribute('data-testid');
      const lastId1 = await cards1[cards1.length - 1].getAttribute('data-testid');
      await assert(firstId1 === 'vm-card-win-app',
        `expected win-app first under sort=gpu, got ${firstId1}`);
      await assert(lastId1 === 'vm-card-db-server',
        `expected db-server last under sort=gpu (empty-trails-in-asc, id tiebreak), got ${lastId1}`);
      const urlAsc = new URL(p.url());
      await assert(urlAsc.searchParams.get("sort") === "gpu",
        `expected ?sort=gpu, got ${urlAsc.searchParams.get("sort")}`);

      // Desc: empty-leads-in-desc inverts the id tiebreak too, so
      // db-server heads the list and win-app trails at the tail.
      await p.locator('[data-testid="vm-list-sort-order"]').selectOption("desc");
      await p.waitForTimeout(200);
      const cards2 = await p.locator('[data-testid^="vm-card-"]').all();
      const firstId2 = await cards2[0].getAttribute('data-testid');
      const lastId2 = await cards2[cards2.length - 1].getAttribute('data-testid');
      await assert(firstId2 === 'vm-card-db-server',
        `expected db-server first under sort=gpu&order=desc, got ${firstId2}`);
      await assert(lastId2 === 'vm-card-win-app',
        `expected win-app last under sort=gpu&order=desc, got ${lastId2}`);

      // Reset for any subsequent tests on this page.
      await p.locator('[data-testid="vm-list-sort-field"]').selectOption("id");
      await p.locator('[data-testid="vm-list-sort-order"]').selectOption("asc");
      await p.waitForTimeout(200);
    }, page);

    // 5.7.9 — `?gpu=<pci-addr>` filter on the VM list. Seed data: win-app
    // alone carries `0000:01:00.0`; web-server / db-server leave spec.gpus
    // empty so the empty-stored-excludes path is exercised. 250 ms debounce
    // and URL round-trip via `?gpu=`.
    await runTest("gpu filter narrows the VM list and round-trips through the URL", async (p) => {
      await openFilterPanel(p, "vm-list-filters");
      await p.locator('[data-testid="vm-list-gpu-filter"]').fill("0000:01:00.0");
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-win-app");
      await assertNotVisible(p, "vm-card-web-server");
      await assertNotVisible(p, "vm-card-db-server");
      const urlAfter = new URL(p.url());
      await assert(urlAfter.searchParams.get("gpu") === "0000:01:00.0",
        `expected ?gpu=0000:01:00.0, got ${urlAfter.searchParams.get("gpu")}`);

      // Short form must match the long-form stored VM via normalisation.
      await p.locator('[data-testid="vm-list-gpu-filter"]').fill("01:00.0");
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-win-app");

      // Clearing restores the unfiltered view and drops the URL param.
      await p.locator('[data-testid="vm-list-gpu-filter-clear"]').click();
      await p.waitForTimeout(400);
      await assertVisible(p, "vm-card-web-server");
      await assertVisible(p, "vm-card-db-server");
      await assertVisible(p, "vm-card-win-app");
      const urlCleared = new URL(p.url());
      await assert(!urlCleared.searchParams.has("gpu"),
        `expected ?gpu= to be cleared, got ${urlCleared.searchParams.get("gpu")}`);
    }, page);

    await page.close();

    // ================== VM Detail Tests ==================
    console.log("\nVM Detail:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="vm-row-web-server"]').click();
    await page.waitForTimeout(500);

    await runTest("shows VM information", async (p) => {
      await assertText(p, "vm-detail-name", "web-server");
      await assertText(p, "vm-detail-state", "running");
      await assertText(p, "vm-detail-ip", "192.168.100.10");
      await assertText(p, "vm-detail-image", "ubuntu-22.04");
      await assertText(p, "vm-detail-resources", "2 vCPU");
    }, page);

    await runTest("shows existing snapshots", async (p) => {
      // PR #414 split VM detail into tabs; snapshots live on tab-snapshots.
      await openVMTab(p, "snapshots");
      await assertVisible(p, "snap-before-deploy");
      await assertText(p, "snap-desc-before-deploy", "checkpoint before May deploy");
    }, page);

    await runTest("stop running VM", async (p) => {
      await assertVisible(p, "btn-stop");
      await p.locator('[data-testid="btn-stop"]').click();
      await p.waitForTimeout(1000);
      await assertText(p, "vm-detail-state", "stopped");
      await assertVisible(p, "btn-start");
    }, page);

    await runTest("start stopped VM", async (p) => {
      await p.locator('[data-testid="btn-start"]').click();
      await p.waitForTimeout(1000);
      await assertText(p, "vm-detail-state", "running");
    }, page);

    await runTest("create snapshot", async (p) => {
      await openVMTab(p, "snapshots");
      await p.locator('[data-testid="btn-new-snapshot"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-snap-name"]').fill("test-snap");
      await p.locator('[data-testid="btn-submit-snapshot"]').click();
      await p.waitForTimeout(1000);
      await assertVisible(p, "snap-test-snap");
    }, page);

    await runTest("create snapshot with description", async (p) => {
      await openVMTab(p, "snapshots");
      await p.locator('[data-testid="btn-new-snapshot"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-snap-name"]').fill("noted-snap");
      await p.locator('[data-testid="input-snap-description"]').fill("captured before risky upgrade");
      await p.locator('[data-testid="btn-submit-snapshot"]').click();
      await p.waitForTimeout(1000);
      await assertVisible(p, "snap-noted-snap");
      await assertText(p, "snap-desc-noted-snap", "captured before risky upgrade");
    }, page);

    await runTest("delete snapshot", async (p) => {
      await openVMTab(p, "snapshots");
      await p.locator('[data-testid="btn-delete-snap-before-deploy"]').click();
      await p.waitForTimeout(1000);
      await assertNotVisible(p, "snap-before-deploy");
    }, page);

    await runTest("bulk-delete selected snapshots", async (p) => {
      await openVMTab(p, "snapshots");
      // Auto- snapshots are seeded alongside the manual one; tick both and
      // confirm they vanish while no surviving non-auto snapshot does.
      await assertVisible(p, "snap-auto-2026-05-06");
      await assertVisible(p, "snap-auto-2026-05-07");
      await p.locator('[data-testid="snap-checkbox-auto-2026-05-06"]').check();
      await p.locator('[data-testid="snap-checkbox-auto-2026-05-07"]').check();
      await p.locator('[data-testid="btn-bulk-delete-snaps"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "snap-auto-2026-05-06");
      await assertNotVisible(p, "snap-auto-2026-05-07");
      await assertText(p, "snap-bulk-result", "2 of 2 succeeded");
    }, page);

    await runTest("create snapshot with tags renders chips and edit clears them", async (p) => {
      await openVMTab(p, "snapshots");
      // Roadmap 2.2.17 — tags on snapshots.
      await p.locator('[data-testid="btn-new-snapshot"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-snap-name"]').fill("jsdom-tagged-snap");
      // Mixed-case + duplicate to exercise mock-server normalisation.
      await p.locator('[data-testid="input-snap-tags"]').fill("Production, audit, production");
      await p.locator('[data-testid="btn-submit-snapshot"]').click();
      await p.waitForTimeout(800);
      await assertVisible(p, "snap-jsdom-tagged-snap");

      const chipBox = p.locator('[data-testid="snap-tags-jsdom-tagged-snap"]');
      await chipBox.waitFor({ state: "visible", timeout: 3000 });
      const chipText = (await chipBox.textContent()) || "";
      await assert(/audit/.test(chipText), "tag chip row should include audit");
      await assert(/production/.test(chipText), "tag chip row should include production");

      // Edit and clear via empty input.
      await p.locator('[data-testid="btn-edit-snap-jsdom-tagged-snap"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-edit-snap-tags"]').fill("");
      await p.locator('[data-testid="btn-submit-edit-snap"]').click();
      await p.waitForTimeout(800);
      await assert(
        (await chipBox.count()) === 0,
        "tag chips should disappear after clearing the input",
      );
    }, page);

    await runTest("sort snapshots by name desc", async (p) => {
      await openVMTab(p, "snapshots");
      // Re-seed all three snapshots via creates so the prior bulk-delete
      // test doesn't leave the list in an emptied state.
      for (const name of ["before-deploy", "auto-2026-05-06", "auto-2026-05-07"]) {
        await p.locator('[data-testid="btn-new-snapshot"]').click();
        await p.waitForTimeout(200);
        await p.locator('[data-testid="input-snap-name"]').fill(name);
        await p.locator('[data-testid="btn-submit-snapshot"]').click();
        await p.waitForTimeout(400);
      }
      // Sort by name desc -> before-deploy moves to the top, auto-* below.
      await p.locator('[data-testid="snap-sort-field"]').selectOption("name");
      await p.locator('[data-testid="snap-sort-order"]').selectOption("desc");
      await p.waitForTimeout(400);
      const order = await p.evaluate(() => {
        const seeded = ["before-deploy", "auto-2026-05-06", "auto-2026-05-07"];
        return seeded
          .map((n) => {
            const el = document.querySelector(`[data-testid="snap-${n}"]`);
            return { name: n, top: el ? el.getBoundingClientRect().top : Infinity };
          })
          .sort((a, b) => a.top - b.top)
          .map((r) => r.name);
      });
      const expected = ["before-deploy", "auto-2026-05-07", "auto-2026-05-06"];
      if (JSON.stringify(order) !== JSON.stringify(expected)) {
        throw new Error(`sort desc order = ${JSON.stringify(order)}, want ${JSON.stringify(expected)}`);
      }
    }, page);

    await runTest("bulk-delete selected port forwards", async (p) => {
      // PR #414 split VM detail into tabs; port forwards live on tab-ports.
      await openVMTab(p, "ports");
      // Two seeded port forwards on web-server: ssh-jumpbox + http.
      // Tick the http row, bulk-delete, confirm only ssh-jumpbox remains.
      await assertVisible(p, "port-row-pf-seed-ssh");
      await assertVisible(p, "port-row-pf-seed-http");
      await p.locator('[data-testid="port-checkbox-pf-seed-http"]').check();
      await p.locator('[data-testid="btn-bulk-delete-ports"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "port-row-pf-seed-http");
      await assertVisible(p, "port-row-pf-seed-ssh");
      await assertText(p, "port-bulk-result", "1 of 1 succeeded");
    }, page);

    await runTest("edit port forward description", async (p) => {
      await openVMTab(p, "ports");
      // ssh-jumpbox seeded rule still exists; click its edit button, rewrite
      // the description, save, and confirm the inline description updated.
      await assertVisible(p, "port-description-pf-seed-ssh");
      await p.locator('[data-testid="btn-edit-port-pf-seed-ssh"]').click();
      await p.waitForTimeout(200);
      await assertVisible(p, "input-edit-port-description");
      await p.locator('[data-testid="input-edit-port-description"]').fill("rewritten via JSDOM");
      await p.locator('[data-testid="btn-submit-edit-port"]').click();
      await p.waitForTimeout(800);
      await assertText(p, "port-description-pf-seed-ssh", "rewritten via JSDOM");
    }, page);

    await runTest("edit port forward tags renders chips and clears", async (p) => {
      await openVMTab(p, "ports");
      // Set tags via the edit modal, verify the chips, then clear them.
      await p.locator('[data-testid="btn-edit-port-pf-seed-ssh"]').click();
      await p.waitForTimeout(200);
      await assertVisible(p, "input-edit-port-tags");
      await p.locator('[data-testid="input-edit-port-tags"]').fill("audit, jumpbox");
      await p.locator('[data-testid="btn-submit-edit-port"]').click();
      await p.waitForTimeout(800);
      await assertText(p, "port-tags-pf-seed-ssh", "audit");
      await assertText(p, "port-tags-pf-seed-ssh", "jumpbox");

      // Now clear them.
      await p.locator('[data-testid="btn-edit-port-pf-seed-ssh"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="input-edit-port-tags"]').fill("");
      await p.locator('[data-testid="btn-submit-edit-port"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "port-tags-pf-seed-ssh");
    }, page);

    await runTest("auto-start summary card and edit toggle", async (p) => {
      // Summary cards live on the overview tab; the prior port-forward
      // tests switched to tab-ports.
      await openVMTab(p, "overview");
      await assertVisible(p, "vm-detail-auto-start");
      // Open the edit modal and flip the checkbox on.
      await p.locator('[data-testid="btn-edit-vm"]').click();
      await p.waitForTimeout(200);
      await assertVisible(p, "input-edit-auto-start");
      await p.locator('[data-testid="input-edit-auto-start"]').check();
      await p.locator('[data-testid="btn-submit-edit"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "input-edit-auto-start");
      // Summary card should now read "On".
      await assertText(p, "vm-detail-auto-start", "On");
    }, page);

    await runTest("locked summary card and edit toggle", async (p) => {
      await openVMTab(p, "overview");
      await assertVisible(p, "vm-detail-locked");
      await p.locator('[data-testid="btn-edit-vm"]').click();
      await p.waitForTimeout(200);
      await assertVisible(p, "input-edit-locked");
      await p.locator('[data-testid="input-edit-locked"]').check();
      await p.locator('[data-testid="btn-submit-edit"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "input-edit-locked");
      // Summary card now reads "Locked".
      await assertText(p, "vm-detail-locked", "Locked");
    }, page);

    // 5.4.73 — guest_ip filter on the port-forward list. The shared mock-server
    // seeds two rules with the same guest_ip, which would make the filter a
    // binary all-or-nothing. Inject a multi-NIC fixture via route interception
    // on a fresh page so the filter has a meaningful cohort to slice.
    const guestIPPage = await context.newPage();
    const guestIPFixture = [
      { id: "pf-gip-ssh",     vm_id: "vm-1", host_port: 2222, guest_port: 22,  guest_ip: "192.168.100.10", protocol: "tcp", description: "ssh primary" },
      { id: "pf-gip-http",    vm_id: "vm-1", host_port: 8080, guest_port: 80,  guest_ip: "192.168.100.10", protocol: "tcp", description: "http primary" },
      { id: "pf-gip-datanet", vm_id: "vm-1", host_port: 8443, guest_port: 443, guest_ip: "10.0.0.7",        protocol: "tcp", description: "https data-net" },
    ];
    await guestIPPage.route("**/api/v1/vms/vm-1/ports*", async (route) => {
      const url = new URL(route.request().url());
      const gip = (url.searchParams.get("guest_ip") || "").trim().toLowerCase();
      let body = guestIPFixture;
      if (gip) {
        body = guestIPFixture.filter((pf) => (pf.guest_ip || "").trim().toLowerCase() === gip);
      }
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json", "X-Total-Count": String(body.length) },
        body: JSON.stringify(body),
      });
    });
    await guestIPPage.goto(BASE);
    await guestIPPage.waitForTimeout(500);
    await guestIPPage.locator('[data-testid="vm-row-web-server"]').click();
    await guestIPPage.waitForTimeout(500);

    await runTest("guest_ip filter narrows the port-forward list (5.4.73)", async (p) => {
      await openVMTab(p, "ports");
      // All three synthetic rules visible before typing.
      await assertVisible(p, "port-row-pf-gip-ssh");
      await assertVisible(p, "port-row-pf-gip-http");
      await assertVisible(p, "port-row-pf-gip-datanet");
      // Filter to 192.168.100.10 keeps the two primary-NIC rules.
      await p.locator('[data-testid="port-guest-ip-filter"]').fill("192.168.100.10");
      await p.waitForTimeout(400);
      await assertVisible(p, "port-row-pf-gip-ssh");
      await assertVisible(p, "port-row-pf-gip-http");
      await assertNotVisible(p, "port-row-pf-gip-datanet");
      const urlAfterFilter = p.url();
      if (!urlAfterFilter.includes("port_guest_ip=192.168.100.10")) {
        throw new Error(`URL should round-trip port_guest_ip filter; got ${urlAfterFilter}`);
      }
      // Re-type a different cohort: only the data-net rule remains.
      await p.locator('[data-testid="port-guest-ip-filter"]').fill("10.0.0.7");
      await p.waitForTimeout(400);
      await assertVisible(p, "port-row-pf-gip-datanet");
      await assertNotVisible(p, "port-row-pf-gip-ssh");
      await assertNotVisible(p, "port-row-pf-gip-http");
      // Clearing drops the URL param and restores all rows.
      await p.locator('[data-testid="port-guest-ip-clear"]').click();
      await p.waitForTimeout(400);
      await assertVisible(p, "port-row-pf-gip-ssh");
      await assertVisible(p, "port-row-pf-gip-http");
      await assertVisible(p, "port-row-pf-gip-datanet");
      const urlAfterClear = p.url();
      if (urlAfterClear.includes("port_guest_ip=")) {
        throw new Error(`URL should drop port_guest_ip on clear; got ${urlAfterClear}`);
      }
    }, guestIPPage);

    await guestIPPage.close();

    let clonePage = await context.newPage();
    await clonePage.goto(BASE);
    await clonePage.waitForTimeout(500);
    await clonePage.locator('[data-testid="vm-row-web-server"]').click();
    await clonePage.waitForTimeout(500);

    await runTest("clone VM flow", async (p) => {
      await p.locator('[data-testid="btn-clone-vm"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "input-clone-name");
      await p.locator('[data-testid="input-clone-name"]').fill("web-server-copy");
      await p.locator('[data-testid="btn-submit-clone"]').click();
      await p.waitForTimeout(1000);
      await assertText(p, "vm-detail-name", "web-server-copy");
      await assertText(p, "vm-detail-state", "stopped");
      await assertText(p, "vm-detail-image", "ubuntu-22.04");
      await assertText(p, "vm-detail-resources", "2 vCPU");
    }, clonePage);

    await runTest("back link after clone returns to VM list with cloned VM", async (p) => {
      await p.locator('[data-testid="back-link"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "vm-card-web-server-copy");
      await assertVisible(p, "vm-card-web-server");
    }, clonePage);

    await clonePage.close();

    await runTest("back link returns to VM list", async (p) => {
      await p.locator('[data-testid="back-link"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "vm-card-web-server");
    }, page);

    await page.close();

    // ================== VM Detail Metrics Tests ==================
    console.log("\nVM Detail Metrics:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="vm-row-web-server"]').click();
    await page.waitForTimeout(500);

    await runTest("metrics tab renders current sample for running VM", async (p) => {
      await p.locator('[data-testid="tab-metrics"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "vm-detail-metrics");
      await assertVisible(p, "metrics-table");
      // Mock seeds the most-recent sample at CPU 35% (10 + 5*5).
      await assertText(p, "metric-cpu-current", "35.0%");
      await assertText(p, "metric-mem-used-current", "MB");
      await assertText(p, "metric-net-rx-current", "/s");
      await assertText(p, "metrics-state", "running");
    }, page);

    await runTest("metrics tab history meta shows interval", async (p) => {
      await assertText(p, "metrics-history-meta", "samples");
      await assertText(p, "metrics-history-meta", "10s interval");
    }, page);

    await runTest("metrics tab is independent of overview", async (p) => {
      await p.locator('[data-testid="tab-overview"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "vm-detail-ip");
      await assertNotVisible(p, "vm-detail-metrics");
    }, page);

    await page.close();

    // Stopped-VM empty state on its own page so prior state doesn't bleed in.
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="vm-row-db-server"]').click();
    await page.waitForTimeout(500);

    await runTest("metrics tab shows empty state for stopped VM", async (p) => {
      await p.locator('[data-testid="tab-metrics"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "vm-detail-metrics");
      await assertNotVisible(p, "metrics-table");
    }, page);

    await page.close();

    // ================== Images Tests ==================
    console.log("\nImages:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="nav-images"]').click();
    await page.waitForTimeout(500);

    await runTest("lists images with details", async (p) => {
      await assertVisible(p, "image-table");
      await assertVisible(p, "image-row-ubuntu-base");
    }, page);

    await runTest("renders description and tag badges", async (p) => {
      await assertVisible(p, "image-description-ubuntu-base");
      await assertVisible(p, "image-tags-ubuntu-base");
    }, page);

    await runTest("filters images by tag chip", async (p) => {
      await p.locator('[data-testid="image-tag-filter-rocky"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "image-row-rocky-experimental");
      await assertNotVisible(p, "image-row-ubuntu-base");
      await p.locator('[data-testid="image-tag-filter-all"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "image-row-ubuntu-base");
    }, page);

    await runTest("edit modal updates description and tags", async (p) => {
      await p.locator('[data-testid="btn-edit-image-rocky-experimental"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "edit-image-modal");
      await p.locator('[data-testid="edit-image-description"]').fill("Promoted to release candidate");
      await p.locator('[data-testid="edit-image-tags"]').fill("rocky,rc");
      await p.locator('[data-testid="btn-save-image"]').click();
      await p.waitForTimeout(500);
      await assertNotVisible(p, "edit-image-modal");
    }, page);

    await runTest("bulk-delete selected images", async (p) => {
      // After the edit test above, both seeded images are still on screen.
      // Tick the rocky one and confirm only it is removed.
      await assertVisible(p, "image-row-rocky-experimental");
      await assertVisible(p, "image-row-ubuntu-base");
      await p.locator('[data-testid="image-checkbox-rocky-experimental"]').check();
      await p.locator('[data-testid="btn-bulk-delete-images"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "image-row-rocky-experimental");
      await assertVisible(p, "image-row-ubuntu-base");
      await assertText(p, "image-bulk-result", "1 of 1 succeeded");
    }, page);

    await page.close();

    // ImageList source_vm filter (5.4.27) — reload the page so the mock-server
    // seed is fresh (the bulk-delete test above removed img-2 from the in-
    // memory store).
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);
    await page.locator('[data-testid="nav-images"]').click();
    await page.waitForTimeout(400);

    await runTest("image source-vm filter narrows the image list and round-trips through the URL", async (p) => {
      // page.goto(BASE) above re-seeded the mock-server state, so both
      // ubuntu-base (source_vm=vm-1) and rocky-experimental (no source_vm)
      // are present.
      await assertVisible(p, "image-row-ubuntu-base");
      await assertVisible(p, "image-row-rocky-experimental");
      // FilterPanel is collapsed by default (PR #414); expand before use.
      await openFilterPanel(p, "image-list-filters");
      await assertVisible(p, "image-list-source-vm");

      // Filter by vm-1 — only ubuntu-base survives.
      await p.locator('[data-testid="image-list-source-vm"]').fill("vm-1");
      await p.waitForTimeout(400); // debounce settles
      await assertVisible(p, "image-row-ubuntu-base");
      await assertNotVisible(p, "image-row-rocky-experimental");
      const url = new URL(p.url());
      await assert(url.searchParams.get("source_vm") === "vm-1",
        `expected ?source_vm=vm-1 in URL, got ${url.search}`);

      // Clear via the X button — rocky-experimental returns and the URL
      // drops the param.
      await p.locator('[data-testid="image-list-source-vm-clear"]').click();
      await p.waitForTimeout(400);
      await assertVisible(p, "image-row-rocky-experimental");
      const urlAfter = new URL(p.url());
      await assert(!urlAfter.searchParams.has("source_vm"),
        `expected source_vm param to be cleared, got ${urlAfter.search}`);
    }, page);

    await page.close();

    // ================== Templates Tests ==================
    console.log("\nTemplates:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);
    await page.locator('[data-testid="nav-templates"]').click();
    await page.waitForTimeout(500);

    await runTest("lists seeded templates", async (p) => {
      await assertVisible(p, "template-table");
      await assertVisible(p, "template-row-small-ubuntu");
      await assertVisible(p, "template-row-big-rocky");
    }, page);

    await runTest("renders description and tag chips", async (p) => {
      await assertVisible(p, "template-description-small-ubuntu");
      await assertVisible(p, "template-tags-small-ubuntu");
    }, page);

    await runTest("filters templates by tag chip", async (p) => {
      await p.locator('[data-testid="template-tag-filter-prod"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-big-rocky");
      await assertNotVisible(p, "template-row-small-ubuntu");
      await p.locator('[data-testid="template-tag-filter-all"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-small-ubuntu");
    }, page);

    await runTest("image filter narrows the template list", async (p) => {
      // FilterPanel is collapsed by default (PR #414); expand before use.
      await openFilterPanel(p, "template-list-filters");
      await p.locator('[data-testid="template-list-image-filter"]').fill("/images/rocky9.qcow2");
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-big-rocky");
      await assertNotVisible(p, "template-row-small-ubuntu");
      await p.locator('[data-testid="template-list-image-filter-clear"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-small-ubuntu");
    }, page);

    await runTest("template time-range filter uses local datetime input and clears cleanly", async (p) => {
      await p.locator('[data-testid="template-list-since"]').fill("2026-05-10T00:00");
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-big-rocky");
      await assertNotVisible(p, "template-row-small-ubuntu");
      await p.locator('[data-testid="template-list-time-range-clear"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "template-row-small-ubuntu");
      await assertVisible(p, "template-row-big-rocky");
    }, page);

    await runTest("edit modal updates description and tags", async (p) => {
      await p.locator('[data-testid="btn-edit-template-big-rocky"]').click();
      await p.waitForTimeout(300);
      await assertVisible(p, "edit-template-modal");
      await p.locator('[data-testid="edit-template-description"]').fill("Promoted to GA");
      await p.locator('[data-testid="edit-template-tags"]').fill("rocky,ga");
      await p.locator('[data-testid="btn-save-template"]').click();
      await p.waitForTimeout(500);
      await assertNotVisible(p, "edit-template-modal");
    }, page);

    await runTest("bulk-delete selected templates", async (p) => {
      // After the edit test above, both seeded templates are still on screen.
      await assertVisible(p, "template-row-big-rocky");
      await assertVisible(p, "template-row-small-ubuntu");
      await p.locator('[data-testid="template-checkbox-big-rocky"]').check();
      await p.locator('[data-testid="btn-bulk-delete-templates"]').click();
      await p.waitForTimeout(800);
      await assertNotVisible(p, "template-row-big-rocky");
      await assertVisible(p, "template-row-small-ubuntu");
      await assertText(p, "template-bulk-result", "1 of 1 succeeded");
    }, page);

    await page.close();

    // ================== Navigation Tests ==================
    console.log("\nNavigation:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);

    await runTest("all nav links work", async (p) => {
      // Dashboard (default)
      await assertVisible(p, "stat-total");

      // Machines
      await p.locator('[data-testid="nav-vms"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "btn-new-vm");

      // Images
      await p.locator('[data-testid="nav-images"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "image-table");

      // Templates
      await p.locator('[data-testid="nav-templates"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "template-table");

      // Back to dashboard
      await p.locator('[data-testid="nav-dashboard"]').click();
      await p.waitForTimeout(500);
      await assertVisible(p, "stat-total");
    }, page);

    await page.close();

    // ================== Live Indicator ==================
    console.log("\nLive Indicator:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);

    await runTest("dashboard live indicator reaches live state", async (p) => {
      const indicator = p.locator('[data-testid="live-indicator"]');
      await indicator.waitFor({ state: "visible", timeout: 5000 });
      // Wait until the SSE onopen handler fires.
      let status = "";
      for (let i = 0; i < 50; i++) {
        status = await indicator.getAttribute("data-status");
        if (status === "live") break;
        await p.waitForTimeout(100);
      }
      await assert(status === "live", `expected status=live, got ${status}`);
    }, page);

    await runTest("VM list live indicator is wired", async (p) => {
      await p.locator('[data-testid="nav-vms"]').click();
      await p.waitForTimeout(500);
      const indicator = p.locator('[data-testid="live-indicator"]');
      await indicator.waitFor({ state: "visible", timeout: 5000 });
    }, page);

    await runTest("dashboard exposes SSE connection-count badge from host_stats", async (p) => {
      await p.locator('[data-testid="nav-dashboard"]').click();
      await p.waitForTimeout(500);
      const badge = p.locator('[data-testid="sse-connection-count"]');
      await badge.waitFor({ state: "visible", timeout: 5000 });
      const text = (await badge.textContent()) || "";
      await assert(/\d+\s*sse/i.test(text), `expected "<n> sse" badge, got "${text.trim()}"`);
    }, page);

    await page.close();

    // ================== Settings: Webhooks (4.2.16) ==================
    console.log("\nSettings - Webhooks:");

    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(500);

    await runTest("settings page lists, creates, tests, and deletes webhooks", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);
      await assertVisible(p, "settings-page");

      // No webhooks initially → empty state.
      const list = p.locator('[data-testid="webhook-list"]');
      await assert(!(await list.isVisible()), "webhook-list should be hidden when empty");

      // Create a webhook.
      await p.locator('[data-testid="add-webhook-btn"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="webhook-url-input"]').fill("https://example.com/hook");
      await p.locator('[data-testid="webhook-secret-input"]').fill("topsecret");
      await p.locator('[data-testid="webhook-event-types-input"]').fill("vm.started, system.*");
      await p.locator('[data-testid="webhook-create-submit"]').click();
      await p.waitForTimeout(600);

      const row = p.locator('[data-testid^="webhook-row-"]').first();
      await row.waitFor({ state: "visible", timeout: 5000 });
      const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

      // "Send test event" surfaces an inline success indicator.
      await p.locator(`[data-testid="webhook-test-${rowID}"]`).click();
      await p.waitForTimeout(700);
      await assertVisible(p, "webhook-status");
      const statusText = (await p.locator('[data-testid="webhook-status"]').first().textContent()) || "";
      await assert(/204|test ok/i.test(statusText), `expected success status, got "${statusText.trim()}"`);

      // Delete (accept confirm dialog).
      p.on("dialog", (d) => d.accept());
      await p.locator(`[data-testid="webhook-delete-${rowID}"]`).click();
      await p.waitForTimeout(600);
      const deletedRow = p.locator(`[data-testid="webhook-row-${rowID}"]`);
      await assert(!(await deletedRow.isVisible()), "deleted webhook row should disappear");
    }, page);

    page = await context.newPage();
    await page.route("**/api/v1/webhooks*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "application/json",
          "X-Total-Count": "1",
        },
        body: JSON.stringify([
          {
            id: "wh-failing",
            url: "https://example.com/failing",
            secret: "",
            event_types: ["vm.started"],
            active: true,
            created_at: "2026-05-20T00:00:00Z",
            last_delivery_at: "2026-05-21T00:00:00Z",
            last_status: 500,
            last_error: "HTTP 500",
          },
        ]),
      });
    });
    await page.goto(`${BASE}/settings`);
    await page.waitForTimeout(1000);

    await runTest("persisted non-2xx status with last_error renders as failure", async (p) => {
      await assertVisible(p, "settings-page");
      await assertVisible(p, "webhook-status");
      const statusText = (await p.locator('[data-testid="webhook-status"]').first().textContent()) || "";
      await assert(/HTTP 500|failed|500/i.test(statusText), `expected persisted failing status, got "${statusText.trim()}"`);
      await assert(!/test ok/i.test(statusText), `expected persisted failure, got "${statusText.trim()}"`);
    }, page);

    await page.close();

    await page.close();

    // 2.2.14 — editable webhook config: round-trip the edit modal through
    // PATCH /api/v1/webhooks/{id} and verify the row reflects the new URL
    // and event-type filters.  The mock server applies the same validation
    // (rejecting non-http(s), empty secret, etc.) as the real daemon.
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    await runTest("edit webhook URL and event-type filter via PATCH", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      // Seed a webhook.
      await p.locator('[data-testid="add-webhook-btn"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="webhook-url-input"]').fill("https://example.com/hook");
      await p.locator('[data-testid="webhook-secret-input"]').fill("topsecret");
      await p.locator('[data-testid="webhook-event-types-input"]').fill("vm.started");
      await p.locator('[data-testid="webhook-create-submit"]').click();
      await p.waitForTimeout(600);

      const row = p.locator('[data-testid^="webhook-row-"]').first();
      await row.waitFor({ state: "visible", timeout: 5000 });
      const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

      // Open the edit modal.
      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      await assertVisible(p, "edit-webhook-form");

      // URL field is pre-populated with the current value.
      const urlInput = p.locator('[data-testid="edit-webhook-url-input"]');
      await assert(
        (await urlInput.inputValue()) === "https://example.com/hook",
        "edit modal should pre-fill current URL",
      );

      // Replace URL and event-type filter list.
      await urlInput.fill("https://example.com/new-hook");
      await p.locator('[data-testid="edit-webhook-event-types-input"]').fill("vm.created, vm.deleted");
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);

      // Modal closes and the row reflects the new URL + filter chips.
      const stillOpen = p.locator('[data-testid="edit-webhook-form"]');
      await assert(!(await stillOpen.isVisible()), "edit modal should close on success");

      const refreshed = p.locator(`[data-testid="webhook-row-${rowID}"]`);
      const rowText = (await refreshed.textContent()) || "";
      await assert(rowText.includes("https://example.com/new-hook"), `row should show new URL, got: ${rowText}`);
      await assert(rowText.includes("vm.created"), `row should show vm.created filter chip, got: ${rowText}`);
      await assert(rowText.includes("vm.deleted"), `row should show vm.deleted filter chip, got: ${rowText}`);
    }, page);

    await runTest("edit webhook can clear filter to subscribe-all", async (p) => {
      // Reuse the previous row (a webhook is already registered).  Click Edit,
      // toggle the "subscribe to every event" checkbox, save, and assert the
      // row falls back to the "all events" placeholder.
      const row = p.locator('[data-testid^="webhook-row-"]').first();
      const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="edit-webhook-subscribe-all"]').check();
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);

      const refreshed = p.locator(`[data-testid="webhook-row-${rowID}"]`);
      const rowText = (await refreshed.textContent()) || "";
      await assert(/all events/i.test(rowText), `row should show 'all events' after clearing, got: ${rowText}`);
    }, page);

    await page.close();

    // ================== Settings: Webhook description (2.2.14) ================
    // Fresh page session — navigating to "/" triggers resetState() on the
    // mock server so we start with zero webhooks.
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    await runTest("create webhook with description, edit, and clear", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      await p.locator('[data-testid="add-webhook-btn"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="webhook-url-input"]').fill("https://example.com/desc");
      await p.locator('[data-testid="webhook-secret-input"]').fill("k");
      await p.locator('[data-testid="webhook-description-input"]').fill("Slack notifier");
      await p.locator('[data-testid="webhook-create-submit"]').click();
      await p.waitForTimeout(600);

      const row = p.locator('[data-testid^="webhook-row-"]').first();
      await row.waitFor({ state: "visible", timeout: 5000 });
      const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");
      const descLocator = p.locator(`[data-testid="webhook-description-${rowID}"]`);
      await descLocator.waitFor({ state: "visible", timeout: 3000 });
      await assert(
        (await descLocator.textContent()) === "Slack notifier",
        "row should render description",
      );

      // Edit description.
      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      const descInput = p.locator('[data-testid="edit-webhook-description-input"]');
      await assert(
        (await descInput.inputValue()) === "Slack notifier",
        "edit modal should pre-fill description",
      );
      await descInput.fill("PagerDuty escalation");
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);
      await assert(
        (await descLocator.textContent()) === "PagerDuty escalation",
        "row should reflect updated description",
      );

      // Clear it.
      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="edit-webhook-description-input"]').fill("");
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);
      await assert(
        (await descLocator.count()) === 0,
        "description row should disappear after clearing",
      );
    }, page);

    await page.close();

    // ================== Settings: Webhook tags (2.2.15) ===================
    // Mirror the Playwright suite — same flow exercised against a JSDOM
    // page so we can run without a browser binary. Same surface as
    // description (2.2.14) but for the tag list.
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    await runTest("create webhook with tags, edit, and clear", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      await p.locator('[data-testid="add-webhook-btn"]').click();
      await p.waitForTimeout(200);
      await p.locator('[data-testid="webhook-url-input"]').fill("https://example.com/tags");
      await p.locator('[data-testid="webhook-secret-input"]').fill("k");
      // Mixed-case + duplicate — exercising server-side normalisation.
      await p.locator('[data-testid="webhook-tags-input"]').fill("Production, audit, production");
      await p.locator('[data-testid="webhook-create-submit"]').click();
      await p.waitForTimeout(600);

      const row = p.locator('[data-testid^="webhook-row-"]').first();
      await row.waitFor({ state: "visible", timeout: 5000 });
      const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");
      const tagBox = p.locator(`[data-testid="webhook-tags-${rowID}"]`);
      await tagBox.waitFor({ state: "visible", timeout: 3000 });
      const tagText = (await tagBox.textContent()) || "";
      await assert(/audit/.test(tagText), "row should render audit tag chip");
      await assert(/production/.test(tagText), "row should render production tag chip");

      // Edit: replace the tag list.
      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      const tagInput = p.locator('[data-testid="edit-webhook-tags-input"]');
      await tagInput.fill("staging");
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);
      const tagText2 = (await tagBox.textContent()) || "";
      await assert(/staging/.test(tagText2), "row should reflect updated tag list");
      await assert(!/audit/.test(tagText2), "audit chip should be removed after edit");

      // Clear all tags.
      await p.locator(`[data-testid="webhook-edit-${rowID}"]`).click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="edit-webhook-tags-input"]').fill("");
      await p.locator('[data-testid="edit-webhook-submit"]').click();
      await p.waitForTimeout(700);
      await assert(
        (await tagBox.count()) === 0,
        "tag chips should disappear after clearing",
      );
    }, page);

    await page.close();

    // ================== Settings: Webhooks free-text search ==================
    // Symmetric search surface alongside VMs (2.2.13), images (5.4.9),
    // events (4.2.20), snapshots (5.4.10), port forwards (5.4.11),
    // templates (5.4.12), and logs (5.4.13).  Substring filter is applied
    // server-side over URL + event_types.
    // 2.3.10 — webhook bulk-delete via multi-select + "Delete selected".
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    await runTest("webhook search input filters the list and matches URL or event types", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      const seed = async (url, types) => {
        await p.locator('[data-testid="add-webhook-btn"]').click();
        await p.waitForTimeout(150);
        await p.locator('[data-testid="webhook-url-input"]').fill(url);
        await p.locator('[data-testid="webhook-secret-input"]').fill("k");
        if (types) await p.locator('[data-testid="webhook-event-types-input"]').fill(types);
        await p.locator('[data-testid="webhook-create-submit"]').click();
        await p.waitForTimeout(500);
      };
      await seed("https://hooks.example.com/audit", "vm.started, vm.stopped");
      await seed("https://metrics.example.com/in", "image.created");

      const allRows = p.locator('[data-testid^="webhook-row-"]');
      await assert((await allRows.count()) === 2, `expected 2 seeded rows, got ${await allRows.count()}`);

      // Filter by a URL substring — only the audit row should remain.
      await p.locator('[data-testid="webhook-list-search"]').fill("audit");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 1, `expected 1 row after URL substring filter, got ${await allRows.count()}`);
      const auditText = (await allRows.first().textContent()) || "";
      await assert(auditText.includes("hooks.example.com/audit"), `row should be the audit webhook, got: ${auditText}`);

      // The URL search-param round-trips so a bookmark replays the filter.
      const urlAfter = new URL(p.url());
      await assert(urlAfter.searchParams.get("search") === "audit",
        `expected ?search=audit, got ${urlAfter.searchParams.get("search")}`);

      // Clear control restores the unfiltered view.
      await p.locator('[data-testid="webhook-list-search-clear"]').click();
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 2, `expected 2 rows after clear, got ${await allRows.count()}`);

      // Filter by an event-type substring — only the metrics row should remain.
      await p.locator('[data-testid="webhook-list-search"]').fill("image.created");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 1, `expected 1 row after event-type filter, got ${await allRows.count()}`);
      const metricsText = (await allRows.first().textContent()) || "";
      await assert(metricsText.includes("metrics.example.com"), `row should be the metrics webhook, got: ${metricsText}`);

      // No-match query renders the dedicated empty-state copy.
      await p.locator('[data-testid="webhook-list-search"]').fill("needle-not-anywhere");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 0, "expected zero rows for no-match query");
    }, page);

    // The search test left two seeded webhooks in the mock server state
    // (audit + metrics). Reset before the event-type-filter test so its
    // explicit-membership count assertions hold.
    await page.close();
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    // 5.4.26 — explicit-membership ?event_type= filter mirrored on the
    // Settings page.
    await runTest("webhook event-type filter narrows the list and excludes catch-alls", async (p) => {
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      const seed = async (url, types) => {
        await p.locator('[data-testid="add-webhook-btn"]').click();
        await p.waitForTimeout(150);
        await p.locator('[data-testid="webhook-url-input"]').fill(url);
        await p.locator('[data-testid="webhook-secret-input"]').fill("k");
        if (types) await p.locator('[data-testid="webhook-event-types-input"]').fill(types);
        await p.locator('[data-testid="webhook-create-submit"]').click();
        await p.waitForTimeout(500);
      };
      // vm.created subscriber, image.created subscriber, and a catch-all
      // (no event_types). The catch-all matches every event behaviourally
      // but must NOT be returned by the explicit-membership filter.
      await seed("https://vm.example.com/hook", "vm.created");
      await seed("https://image.example.com/hook", "image.created");
      await seed("https://catchall.example.com/hook", "");

      const allRows = p.locator('[data-testid^="webhook-row-"]');
      await assert((await allRows.count()) === 3, `expected 3 seeded rows, got ${await allRows.count()}`);

      // FilterPanel is collapsed by default (PR #414); expand before use.
      await openFilterPanel(p, "webhook-list-filters");

      // Filter by vm.created — only the explicit vm subscriber remains.
      await p.locator('[data-testid="webhook-list-event-type-filter"]').fill("vm.created");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 1, `expected 1 row after event_type filter, got ${await allRows.count()}`);
      const vmText = (await allRows.first().textContent()) || "";
      await assert(vmText.includes("vm.example.com"), `row should be the vm webhook, got: ${vmText}`);

      // URL round-trip: ?event_type=vm.created lives in the address bar.
      const urlAfter = new URL(p.url());
      await assert(urlAfter.searchParams.get("event_type") === "vm.created",
        `expected ?event_type=vm.created, got ${urlAfter.searchParams.get("event_type")}`);

      // Clear restores all three rows including the catch-all.
      await p.locator('[data-testid="webhook-list-event-type-filter-clear"]').click();
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 3, `expected 3 rows after clear, got ${await allRows.count()}`);

      // Case-insensitive match: VM.CREATED is normalised on the wire.
      await p.locator('[data-testid="webhook-list-event-type-filter"]').fill("VM.CREATED");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 1, `expected 1 row after case-insensitive filter, got ${await allRows.count()}`);

      // No-match shows the dedicated empty-state copy referencing the filter.
      await p.locator('[data-testid="webhook-list-event-type-filter"]').fill("snapshot.taken");
      await p.waitForTimeout(450);
      await assert((await allRows.count()) === 0, "expected zero rows for no-match event type");
      const empty = (await p.locator('text=/No webhooks explicitly subscribe/i').first().textContent()) || "";
      await assert(empty.length > 0, "expected explicit-membership empty-state copy");
    }, page);

    // Reset state for the bulk-delete suite — the event-type test left three
    // webhooks behind.
    await page.close();
    page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(400);

    await runTest("bulk-delete selected webhooks via checkbox + Delete-selected", async (p) => {
      p.on("dialog", (d) => d.accept());
      await p.locator('[data-testid="nav-settings"]').click();
      await p.waitForTimeout(400);

      // Seed three webhooks.
      const urls = ["https://example.com/bulk-a", "https://example.com/bulk-b", "https://example.com/bulk-c"];
      for (const url of urls) {
        await p.locator('[data-testid="add-webhook-btn"]').click();
        await p.waitForTimeout(150);
        await p.locator('[data-testid="webhook-url-input"]').fill(url);
        await p.locator('[data-testid="webhook-secret-input"]').fill("s");
        await p.locator('[data-testid="webhook-create-submit"]').click();
        await p.waitForTimeout(500);
      }
      const rows = p.locator('[data-testid^="webhook-row-"]');
      let count = await rows.count();
      await assert(count === 3, `expected 3 webhook rows after seed, got ${count}`);

      // Select two of three by ticking their checkboxes.
      const firstID = (await rows.nth(0).getAttribute("data-testid")).replace("webhook-row-", "");
      const secondID = (await rows.nth(1).getAttribute("data-testid")).replace("webhook-row-", "");
      await p.locator(`[data-testid="webhook-checkbox-${firstID}"]`).check();
      await p.locator(`[data-testid="webhook-checkbox-${secondID}"]`).check();

      // Bulk-delete.
      await p.locator('[data-testid="btn-bulk-delete-webhooks"]').click();
      await p.waitForTimeout(800);

      // Only one survivor remains, and the result banner appears.
      count = await p.locator('[data-testid^="webhook-row-"]').count();
      await assert(count === 1, `expected 1 surviving webhook row, got ${count}`);
      await assertText(p, "webhook-bulk-result", "2 of 2 succeeded");
    }, page);

    await runTest("select-all then Delete-selected sweeps every webhook", async (p) => {
      // Continues from the previous test — 1 row left. Add another, then sweep.
      await p.locator('[data-testid="add-webhook-btn"]').click();
      await p.waitForTimeout(150);
      await p.locator('[data-testid="webhook-url-input"]').fill("https://example.com/sweep-extra");
      await p.locator('[data-testid="webhook-secret-input"]').fill("s");
      await p.locator('[data-testid="webhook-create-submit"]').click();
      await p.waitForTimeout(500);

      const before = await p.locator('[data-testid^="webhook-row-"]').count();
      await assert(before >= 2, `expected >= 2 rows before sweep, got ${before}`);

      await p.locator('[data-testid="webhook-select-all"]').check();
      await p.locator('[data-testid="btn-bulk-delete-webhooks"]').click();
      await p.waitForTimeout(800);

      const after = await p.locator('[data-testid^="webhook-row-"]').count();
      await assert(after === 0, `expected zero rows after select-all sweep, got ${after}`);
    }, page);

    // Roadmap 5.4.19 — pagination on the webhook list. Seed a couple of
    // webhooks, then verify the PaginationControls render the post-filter
    // total reading from the X-Total-Count header so the GUI can paginate
    // beyond the first page without losing the total.
    await runTest("settings webhook list shows pagination controls with X-Total-Count total", async (p) => {
      for (let i = 0; i < 3; i++) {
        await p.locator('[data-testid="add-webhook-btn"]').click();
        await p.waitForTimeout(150);
        await p.locator('[data-testid="webhook-url-input"]').fill(`https://example.com/page-${i}`);
        await p.locator('[data-testid="webhook-secret-input"]').fill("s");
        await p.locator('[data-testid="webhook-create-submit"]').click();
        await p.waitForTimeout(300);
      }

      // The PaginationControls render "Showing 1-3 of 3 webhooks" when total
      // is read from the X-Total-Count header. If the meta wiring broke we'd
      // see "of 0" or no controls at all.
      const indicator = p.locator('text=/Showing\\s+1-3\\s+of\\s+3\\s+webhooks/');
      await indicator.waitFor({ state: "visible", timeout: 3000 });
    }, page);

    await page.close();

    // ================== Full Lifecycle E2E ==================
    console.log("\nFull Lifecycle E2E:");

    page = await context.newPage();

    await runTest("create → snapshot → stop → start → delete", async (p) => {
      await p.goto(BASE);
      await p.waitForTimeout(500);

      // 1. Go to machines and create a new VM
      await p.locator('[data-testid="nav-vms"]').click();
      await p.waitForTimeout(500);
      await p.locator('[data-testid="btn-new-vm"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-vm-name"]').fill("e2e-lifecycle");
      await p.locator('[data-testid="input-vm-image"]').selectOption('/images/ubuntu-base.qcow2');
      await p.locator('[data-testid="btn-submit-create"]').click();
      await p.waitForTimeout(1000);

      // 2. Click into VM detail
      await assertVisible(p, "vm-card-e2e-lifecycle");
      await p.locator('[data-testid="vm-card-e2e-lifecycle"]').click();
      await p.waitForTimeout(500);
      await assertText(p, "vm-detail-name", "e2e-lifecycle");
      await assertText(p, "vm-detail-state", "running");

      // 3. Create snapshot (PR #414 split VM detail into tabs)
      await openVMTab(p, "snapshots");
      await p.locator('[data-testid="btn-new-snapshot"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-snap-name"]').fill("e2e-checkpoint");
      await p.locator('[data-testid="btn-submit-snapshot"]').click();
      await p.waitForTimeout(1000);
      await assertVisible(p, "snap-e2e-checkpoint");

      // 4. Stop
      await p.locator('[data-testid="btn-stop"]').click();
      await p.waitForTimeout(1000);
      await assertText(p, "vm-detail-state", "stopped");

      // 5. Start
      await p.locator('[data-testid="btn-start"]').click();
      await p.waitForTimeout(1000);
      await assertText(p, "vm-detail-state", "running");

      // 6. Delete (accept confirm dialog)
      p.on("dialog", (d) => d.accept());
      await p.locator('[data-testid="btn-delete"]').click();
      await p.waitForTimeout(1000);

      // 7. Should be back on VM list
      await assertNotVisible(p, "vm-card-e2e-lifecycle");
    }, page);

    await page.close();

    // ================== VM Console (5.1.7 / 5.1.9) ==================
    console.log("\nVM Console:");

    page = await context.newPage();

    await runTest("console page connects VNC and mounts canvas", async (p) => {
      await p.goto(`${BASE}/vms/vm-1/console`);
      await assertVisible(p, "vm-console-page");
      await p.waitForSelector('[data-testid="console-status"][data-status="connected"]', { timeout: 15000 });
      await p.waitForSelector('[data-testid="vnc-canvas-container"] canvas', { timeout: 10000 });
    }, page);

    await runTest("serial tab opens an echoing terminal", async (p) => {
      await p.goto(`${BASE}/vms/vm-1/console`);
      await p.locator('[data-testid="tab-serial"]').click();
      await p.waitForSelector('[data-testid="console-status"][data-status="connected"]', { timeout: 15000 });
      await p.waitForFunction(
        () => document.querySelector('[data-testid="serial-terminal-container"]')?.textContent?.includes("mock-serial login:"),
        { timeout: 10000 }
      );
      await p.keyboard.type("uname");
      await p.waitForFunction(
        () => document.querySelector('[data-testid="serial-terminal-container"]')?.textContent?.includes("uname"),
        { timeout: 10000 }
      );
    }, page);

    await runTest("VMDetail console button opens for running VM only", async (p) => {
      await p.goto(`${BASE}/vms/vm-1`);
      await p.waitForSelector('[data-testid="btn-console"]', { timeout: 5000 });
      await p.goto(`${BASE}/vms/vm-2`);
      await p.waitForSelector('[data-testid="vm-detail-name"]', { timeout: 5000 });
      await assertNotVisible(p, "btn-console");
    }, page);

    await page.close();

  } finally {
    if (browser) await browser.close();
    server.kill();
  }

  // Summary
  console.log(`\n${"=".repeat(50)}`);
  console.log(`Results: ${passed} passed, ${failed} failed, ${passed + failed} total`);
  if (errors.length > 0) {
    console.log("\nFailed tests:");
    errors.forEach((e) => console.log(`  ✗ ${e.name}: ${e.error}`));
  }
  console.log("=".repeat(50));

  process.exit(failed > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("Fatal:", err);
  process.exit(1);
});
