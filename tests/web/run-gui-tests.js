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

async function runTest(name, fn, page) {
  try {
    await fn(page);
    passed++;
    console.log(`  ✓ ${name}`);
  } catch (err) {
    failed++;
    errors.push({ name, error: err.message });
    console.log(`  ✗ ${name}`);
    console.log(`    ${err.message}`);
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
    browser = await chromium.launch({ headless: true });
    const context = await browser.newContext();

    // ================== Dashboard Tests ==================
    console.log("\nDashboard:");

    let page = await context.newPage();
    await page.goto(BASE);
    await page.waitForTimeout(1000);

    await runTest("shows stats on load", async (p) => {
      await assertText(p, "stat-total", "2");
      await assertText(p, "stat-running", "1");
      await assertText(p, "stat-images", "2");
    }, page);

    await runTest("displays seeded VMs in table", async (p) => {
      await assertVisible(p, "vm-row-web-server");
      await assertVisible(p, "vm-row-db-server");
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
      await p.locator('[data-testid="btn-new-snapshot"]').click();
      await p.waitForTimeout(300);
      await p.locator('[data-testid="input-snap-name"]').fill("test-snap");
      await p.locator('[data-testid="btn-submit-snapshot"]').click();
      await p.waitForTimeout(1000);
      await assertVisible(p, "snap-test-snap");
    }, page);

    await runTest("create snapshot with description", async (p) => {
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
      await p.locator('[data-testid="btn-delete-snap-before-deploy"]').click();
      await p.waitForTimeout(1000);
      await assertNotVisible(p, "snap-before-deploy");
    }, page);

    await runTest("bulk-delete selected snapshots", async (p) => {
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

    await runTest("bulk-delete selected port forwards", async (p) => {
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

    await runTest("auto-start summary card and edit toggle", async (p) => {
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

      // 3. Create snapshot
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
