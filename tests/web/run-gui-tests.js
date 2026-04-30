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
      await assertText(p, "stat-images", "1");
    }, page);

    await runTest("displays seeded VMs in table", async (p) => {
      await assertVisible(p, "vm-row-web-server");
      await assertVisible(p, "vm-row-db-server");
    }, page);

    await runTest("clicking VM row navigates to detail", async (p) => {
      await p.locator('[data-testid="vm-row-web-server"]').click();
      await p.waitForTimeout(500);
      await assertText(p, "vm-detail-name", "web-server");
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

    await runTest("delete snapshot", async (p) => {
      await p.locator('[data-testid="btn-delete-snap-before-deploy"]').click();
      await p.waitForTimeout(1000);
      await assertNotVisible(p, "snap-before-deploy");
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
