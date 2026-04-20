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

  test("back link returns to VM list", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("back-link").click();

    // Should be back on VM list
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
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
// Full E2E lifecycle test
// ============================================================
test.describe("Full Lifecycle", () => {
  test("create VM, snapshot, stop, start, delete", async ({ page }) => {
    await page.goto(BASE_URL);

    // 1. Go to machines and create a new VM
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();
    await page.getByTestId("input-vm-name").fill("e2e-test-vm");
    await page.getByTestId("input-vm-image").fill("ubuntu-22.04");
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
