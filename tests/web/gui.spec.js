// tests/web/gui.spec.mjs
// End-to-end tests for vmSmith web GUI using Playwright.
//
// Run: npx playwright test tests/web/gui.spec.mjs
// (with mock-server.mjs running on port 4173)

const { test, expect } = require("@playwright/test");

const BASE_URL = "http://localhost:4173";

// ============================================================
// Dashboard
// ============================================================
test.describe("Dashboard", () => {
  test("shows stats and VM table on load", async ({ page }) => {
    await page.goto(BASE_URL);

    // Stats should show seeded data (2 VMs, 1 image)
    await expect(page.getByTestId("stat-total")).toHaveText("2");
    await expect(page.getByTestId("stat-running")).toHaveText("1");
    await expect(page.getByTestId("stat-images")).toHaveText("1");
  });

  test("uses pagination metadata for totals", async ({ page }) => {
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

    await page.goto(BASE_URL);

    await expect(page.getByTestId("stat-total")).toHaveText("42");
    await expect(page.getByTestId("stat-running")).toHaveText("1");
    await expect(page.getByTestId("stat-images")).toHaveText("7");
  });

  test("displays seeded VMs in table", async ({ page }) => {
    await page.goto(BASE_URL);

    const table = page.getByTestId("dashboard-vm-table");
    await expect(table).toBeVisible();

    // Check the seeded VMs appear
    await expect(page.getByTestId("vm-row-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-row-db-server")).toBeVisible();
  });

  test("clicking VM row navigates to detail", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Should show VM detail
    await expect(page.getByTestId("vm-detail-name")).toHaveText("web-server");
  });

  test("shows top VMs leaderboard and switches metrics", async ({ page }) => {
    await page.goto(BASE_URL);

    const card = page.getByTestId("top-vms-card");
    await expect(card).toBeVisible();

    // Default metric is CPU; the seeded running VM ("web-server") should appear.
    await expect(page.getByTestId("top-vms-table")).toBeVisible();
    await expect(page.getByTestId("top-vm-row-web-server")).toBeVisible();

    // Switch metric to memory and verify the table re-renders.
    await page.getByTestId("top-vms-metric").selectOption("mem");
    await expect(page.getByTestId("top-vms-table")).toBeVisible();
    await expect(page.getByTestId("top-vm-row-web-server")).toBeVisible();
  });

  test("clicking a top-VM row navigates to its detail page", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("top-vm-row-web-server").click();
    await expect(page.getByTestId("vm-detail-name")).toHaveText("web-server");
  });

  test("shows empty-state message when no VMs reported metrics", async ({ page }) => {
    await page.route("**/api/v1/vms/stats/top*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ metric: "cpu", limit: 5, state: "running", items: [] }),
      });
    });

    await page.goto(BASE_URL);
    await expect(page.getByTestId("top-vms-empty")).toContainText("No samples yet");
  });
});

// ============================================================
// VM List
// ============================================================
test.describe("VM List", () => {
  test("lists VMs with status badges", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
  });

  test("new VM button opens create modal on basic tab", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("btn-new-vm").click();

    // Basic tab fields should be visible by default
    await expect(page.getByTestId("input-vm-name")).toBeVisible();
    await expect(page.getByTestId("input-vm-template")).toBeVisible();
    await expect(page.getByTestId("input-vm-image")).toBeVisible();
    await expect(page.getByTestId("input-vm-cpus")).toBeVisible();
    await expect(page.getByTestId("input-vm-ram")).toBeVisible();
    await expect(page.getByTestId("input-vm-disk")).toBeVisible();
  });

  test("advanced tab shows SSH and network options", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    // Switch to advanced tab
    await page.getByTestId("tab-advanced").click();

    // Advanced-only fields should now be visible
    await expect(page.getByTestId("input-vm-ssh-key")).toBeVisible();
    await expect(page.getByTestId("input-vm-default-user")).toBeVisible();
    await expect(page.getByTestId("input-vm-auto-start")).toBeVisible();
    await expect(page.getByTestId("btn-add-network")).toBeVisible();
  });

  test("extra network uses static IP by default with DHCP checkbox", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();
    await page.getByTestId("tab-advanced").click();

    // Add an extra network
    await page.getByTestId("btn-add-network").click();

    // Static IP field should be visible by default
    await expect(page.getByTestId("input-net-0-static-ip")).toBeVisible();
    await expect(page.getByTestId("input-net-0-gateway")).toBeVisible();

    // Check the DHCP checkbox — static IP fields should disappear
    await page.getByTestId("checkbox-net-0-dhcp").check();
    await expect(page.getByTestId("input-net-0-static-ip")).not.toBeVisible();
  });

  test("create VM flow", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    // Fill the form (basic tab)
    await page.getByTestId("input-vm-name").fill("test-new-vm");
    await page.getByTestId("input-vm-image").selectOption("/images/ubuntu-base.qcow2");
    await page.getByTestId("input-vm-cpus").fill("4");
    await page.getByTestId("input-vm-ram").fill("8192");
    await page.getByTestId("input-vm-disk").fill("50");

    await page.getByTestId("btn-submit-create").click();

    // Modal should close and new VM should appear
    await expect(page.getByTestId("input-vm-name")).not.toBeVisible();
    await expect(page.getByTestId("vm-card-test-new-vm")).toBeVisible();
  });

  test("template selection prefills create form", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-template").selectOption("tmpl-1");

    await expect(page.getByTestId("template-hint")).toBeVisible();
    await expect(page.getByTestId("input-vm-image")).toHaveValue("/images/ubuntu-base.qcow2");
    await expect(page.getByTestId("input-vm-cpus")).toHaveValue("1");
    await expect(page.getByTestId("input-vm-ram")).toHaveValue("1024");
    await expect(page.getByTestId("input-vm-disk")).toHaveValue("12");

    await page.getByTestId("tab-advanced").click();
    await expect(page.getByTestId("input-vm-default-user")).toHaveValue("ubuntu");
  });

  test("cancel create modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("btn-cancel-create").click();
    await expect(page.getByTestId("input-vm-name")).not.toBeVisible();
  });
});

// ============================================================
// VM Detail
// ============================================================
test.describe("VM Detail", () => {
  test("shows VM information", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await expect(page.getByTestId("vm-detail-name")).toHaveText("web-server");
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
    await expect(page.getByTestId("vm-detail-ip")).toHaveText("192.168.100.10");
    await expect(page.getByTestId("vm-detail-image")).toHaveText("ubuntu-22.04");
    await expect(page.getByTestId("vm-detail-resources")).toContainText("2 vCPU");
    await expect(page.getByTestId("vm-detail-resources")).toContainText("4096 MB");
  });

  test("shows snapshots", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
  });

  test("stop running VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Running VM should have stop button
    await expect(page.getByTestId("btn-stop")).toBeVisible();
    await page.getByTestId("btn-stop").click();

    // After stop, state should change
    await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped");
    // Start button should now appear
    await expect(page.getByTestId("btn-start")).toBeVisible();
  });

  test("start stopped VM", async ({ page }) => {
    await page.goto(BASE_URL);

    // Navigate to db-server which is stopped
    await page.getByTestId("vm-row-db-server").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped");

    await page.getByTestId("btn-start").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
  });

  test("create snapshot", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("test-snap");
    await page.getByTestId("btn-submit-snapshot").click();

    // New snapshot should appear
    await expect(page.getByTestId("snap-test-snap")).toBeVisible();
  });

  test("delete snapshot", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Snapshot "before-deploy" should exist
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();

    await page.getByTestId("btn-delete-snap-before-deploy").click();

    // Should be removed
    await expect(page.getByTestId("snap-before-deploy")).not.toBeVisible();
  });

  test("edit button opens edit modal pre-filled with current values", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-vm").click();

    // Modal should appear with current values pre-filled
    await expect(page.getByTestId("input-edit-cpus")).toBeVisible();
    await expect(page.getByTestId("input-edit-ram")).toBeVisible();
    await expect(page.getByTestId("input-edit-disk")).toBeVisible();

    // Values should match the seeded VM (cpus=2, ram=4096, disk=40)
    await expect(page.getByTestId("input-edit-cpus")).toHaveValue("2");
    await expect(page.getByTestId("input-edit-ram")).toHaveValue("4096");
  });

  test("edit VM updates resources", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-vm").click();

    // Change CPU count
    await page.getByTestId("input-edit-cpus").fill("8");
    await page.getByTestId("btn-submit-edit").click();

    // Modal should close
    await expect(page.getByTestId("input-edit-cpus")).not.toBeVisible();

    // Resources should now reflect updated values
    await expect(page.getByTestId("vm-detail-resources")).toContainText("8 vCPU");
  });

  test("edit IP field is visible and accepts new address", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-vm").click();

    // IP field should be visible
    await expect(page.getByTestId("input-edit-nat-ip")).toBeVisible();

    // Change the IP and submit
    await page.getByTestId("input-edit-nat-ip").fill("192.168.100.99");
    await page.getByTestId("btn-submit-edit").click();

    // Modal closes after submit
    await expect(page.getByTestId("input-edit-nat-ip")).not.toBeVisible();
  });

  test("auto-start checkbox surfaces and toggles in edit modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Detail summary shows the current Auto-start state.
    await expect(page.getByTestId("vm-detail-auto-start")).toContainText(/On|Off/);

    await page.getByTestId("btn-edit-vm").click();

    const cb = page.getByTestId("input-edit-auto-start");
    await expect(cb).toBeVisible();

    // Flip it on, save, and confirm the summary card now reads "On".
    await cb.check();
    await page.getByTestId("btn-submit-edit").click();
    await expect(page.getByTestId("input-edit-auto-start")).not.toBeVisible();
    await expect(page.getByTestId("vm-detail-auto-start")).toContainText("On");
  });

  test("cancel edit closes modal without changes", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-vm").click();
    await page.getByTestId("input-edit-cpus").fill("16");
    await page.getByTestId("btn-cancel-edit").click();

    await expect(page.getByTestId("input-edit-cpus")).not.toBeVisible();
    // Original resources unchanged
    await expect(page.getByTestId("vm-detail-resources")).toContainText("2 vCPU");
  });

  test("clone VM opens modal and redirects to cloned machine", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-clone-vm").click();
    await expect(page.getByTestId("input-clone-name")).toHaveValue("web-server-clone");

    await page.getByTestId("input-clone-name").fill("web-server-copy");
    await page.getByTestId("btn-submit-clone").click();

    await expect(page).toHaveURL(/\/vms\/vm-3$/);
    await expect(page.getByTestId("vm-detail-name")).toHaveText("web-server-copy");
    await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped");
  });

  test("back link returns to VM list", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("back-link").click();

    // Should be back on VM list
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
  });
});

// ============================================================
// VM Detail — Metrics Tab
// ============================================================
test.describe("VM Detail Metrics", () => {
  test("metrics tab renders current sample for running VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    await expect(page.getByTestId("vm-detail-metrics")).toBeVisible();
    await expect(page.getByTestId("metrics-table")).toBeVisible();

    // The mock seeds CPU 35% on the most recent sample (10 + 5*5).
    await expect(page.getByTestId("metric-cpu-current")).toHaveText("35.0%");
    await expect(page.getByTestId("metric-mem-used-current")).toContainText("MB");
    await expect(page.getByTestId("metric-net-rx-current")).toContainText("/s");

    // 5-min average column populated from history.
    await expect(page.getByTestId("metric-cpu-avg")).not.toHaveText("n/a");
  });

  test("metrics tab shows history meta and last-sampled timestamp", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    await expect(page.getByTestId("metrics-history-meta")).toContainText("samples");
    await expect(page.getByTestId("metrics-history-meta")).toContainText("10s interval");
    await expect(page.getByTestId("metrics-state")).toHaveText("running");
  });

  test("metrics tab shows empty state when VM is stopped", async ({ page }) => {
    await page.goto(BASE_URL);
    // db-server is seeded as stopped in mock-server.js
    await page.getByTestId("vm-row-db-server").click();

    await page.getByTestId("tab-metrics").click();
    await expect(page.getByTestId("vm-detail-metrics")).toBeVisible();
    // Stopped VM has no current sample → table is hidden, EmptyState message shown.
    await expect(page.getByTestId("metrics-table")).not.toBeVisible();
    await expect(page.getByTestId("vm-detail-metrics")).toContainText(/not running|metrics resume/i);
  });

  test("metrics tab is independent of overview and activity", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    await expect(page.getByTestId("vm-detail-metrics")).toBeVisible();
    await expect(page.getByTestId("vm-detail-ip")).not.toBeVisible();

    await page.getByTestId("tab-overview").click();
    await expect(page.getByTestId("vm-detail-ip")).toBeVisible();
    await expect(page.getByTestId("vm-detail-metrics")).not.toBeVisible();
  });
});

// ============================================================
// Images
// ============================================================
test.describe("Images", () => {
  test("lists images with details", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    const table = page.getByTestId("image-table");
    await expect(table).toBeVisible();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
  });
});

// ============================================================
// Navigation
// ============================================================
test.describe("Navigation", () => {
  test("all nav links work", async ({ page }) => {
    await page.goto(BASE_URL);

    // Dashboard (default)
    await expect(page.getByTestId("stat-total")).toBeVisible();

    // Machines
    await page.getByTestId("nav-vms").click();
    await expect(page.getByTestId("btn-new-vm")).toBeVisible();

    // Images
    await page.getByTestId("nav-images").click();
    await expect(page.getByTestId("image-table")).toBeVisible();

    // Logs
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // Back to dashboard
    await page.getByTestId("nav-dashboard").click();
    await expect(page.getByTestId("stat-total")).toBeVisible();
  });
});

// ============================================================
// Log Viewer
// ============================================================
test.describe("Log Viewer", () => {
  test("shows log table on navigation", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    await expect(page.getByTestId("log-table")).toBeVisible();
  });

  test("displays seeded log entries", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    // Mock server seeds entries with source "daemon", "api", "cli"
    await expect(page.getByTestId("log-source-daemon")).toBeVisible();
    await expect(page.getByTestId("log-source-api")).toBeVisible();
  });

  test("level filter hides lower-level entries", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    // Select error-only filter
    await page.getByTestId("log-level-filter").selectOption("error");

    // info entries should not be visible
    await expect(page.getByTestId("log-level-info")).not.toBeVisible();
  });

  test("source filter shows only selected source", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    await page.getByTestId("log-source-filter").selectOption("daemon");

    // api and cli entries should not be visible
    await expect(page.getByTestId("log-source-api")).not.toBeVisible();
    await expect(page.getByTestId("log-source-daemon")).toBeVisible();
  });

  test("pause button stops auto-refresh", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    await page.getByTestId("btn-log-pause").click();

    // Button text should switch to Resume
    await expect(page.getByTestId("btn-log-pause")).toHaveText("Resume");
  });

  test("resume button restarts auto-refresh", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();

    await page.getByTestId("btn-log-pause").click();
    await page.getByTestId("btn-log-pause").click();

    await expect(page.getByTestId("btn-log-pause")).toHaveText("Pause");
  });
});

// ============================================================
// Activity page
// ============================================================
test.describe("Activity", () => {
  test("nav link reaches Activity page", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-activity").click();
    await expect(page).toHaveURL(/\/activity/);
    await expect(page.getByTestId("activity-table")).toBeVisible();
  });

  test("renders seeded events from mock server", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // Mock server returns three events; at least one row should appear.
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toContainText("vm_started");
  });

  test("source filter narrows results", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    await page.getByTestId("activity-filter-source").selectOption("app");
    // evt-2 is source=app; evt-3 is libvirt and should be hidden after the request.
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
  });

  test("VM detail Activity tab shows scoped timeline", async ({ page }) => {
    await page.goto(`${BASE_URL}/vms`);
    // Click into the first VM card.
    await page.locator('[data-testid^="vm-card-"]').first().click();
    await page.getByTestId("tab-activity").click();
    await expect(page.getByTestId("vm-detail-activity")).toBeVisible();
  });
});

// ============================================================
// Live indicator (useEventStream hook)
// ============================================================
test.describe("Live Indicator", () => {
  test("dashboard shows live indicator that reaches the live state", async ({ page }) => {
    await page.goto(BASE_URL);
    const indicator = page.getByTestId("live-indicator");
    await expect(indicator).toBeVisible();
    await expect.poll(
      async () => indicator.getAttribute("data-status"),
      { timeout: 5000 },
    ).toBe("live");
    await expect(indicator).toContainText("live");
  });

  test("VM list shows live indicator", async ({ page }) => {
    await page.goto(`${BASE_URL}/vms`);
    const indicator = page.getByTestId("live-indicator");
    await expect(indicator).toBeVisible();
    await expect.poll(
      async () => indicator.getAttribute("data-status"),
      { timeout: 5000 },
    ).toBe("live");
  });

  test("indicator falls back to polling when SSE returns 410", async ({ page }) => {
    await page.route("**/api/v1/events/stream*", async (route) => {
      await route.fulfill({
        status: 410,
        contentType: "application/json",
        body: JSON.stringify({ code: "event_stream_replay_window_exceeded", message: "too far behind" }),
      });
    });
    await page.goto(BASE_URL);
    const indicator = page.getByTestId("live-indicator");
    await expect.poll(
      async () => indicator.getAttribute("data-status"),
      { timeout: 8000 },
    ).toMatch(/reconnecting|fallback/);
  });

  test("dashboard shows SSE connection count when host_stats reports >0 consumers", async ({ page }) => {
    await page.goto(BASE_URL);
    const sseBadge = page.getByTestId("sse-connection-count");
    // The mock-server reports event_stream_connections=2.
    await expect(sseBadge).toBeVisible();
    await expect(sseBadge).toContainText("sse");
  });
});

// ============================================================
// Full E2E lifecycle test
// ============================================================
test.describe("Full Lifecycle", () => {
  test("create VM, snapshot, stop, start, delete", async ({ page }) => {
    await page.goto(BASE_URL);

    // 1. Go to machines and create a new VM
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();
    await page.getByTestId("input-vm-name").fill("e2e-test-vm");
    await page.getByTestId("input-vm-image").selectOption("/images/ubuntu-base.qcow2");
    await page.getByTestId("btn-submit-create").click();

    // 2. New VM should appear in list
    await expect(page.getByTestId("vm-card-e2e-test-vm")).toBeVisible();

    // 3. Click into VM detail
    await page.getByTestId("vm-card-e2e-test-vm").click();
    await expect(page.getByTestId("vm-detail-name")).toHaveText("e2e-test-vm");
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");

    // 4. Create a snapshot
    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("e2e-checkpoint");
    await page.getByTestId("btn-submit-snapshot").click();
    await expect(page.getByTestId("snap-e2e-checkpoint")).toBeVisible();

    // 5. Edit resources
    await page.getByTestId("btn-edit-vm").click();
    await page.getByTestId("input-edit-cpus").fill("4");
    await page.getByTestId("btn-submit-edit").click();
    await expect(page.getByTestId("vm-detail-resources")).toContainText("4 vCPU");

    // 6. Stop the VM
    await page.getByTestId("btn-stop").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped");

    // 7. Start it back
    await page.getByTestId("btn-start").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");

    // 8. Delete the VM
    page.on("dialog", (dialog) => dialog.accept());
    await page.getByTestId("btn-delete").click();

    // 9. Should be back on VM list, e2e-test-vm gone
    await expect(page.getByTestId("vm-card-e2e-test-vm")).not.toBeVisible();
  });
});
