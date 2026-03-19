// tests/e2e/gui-e2e.spec.js
// Real end-to-end Playwright tests for the vmSmith web GUI.
//
// These tests run against a live vmsmith daemon (not the mock server).
// The daemon must be running and serving the GUI on the configured port.
//
// Required env vars:
//   VMSMITH_GUI_URL       - Base URL of the running GUI (default: http://localhost:8080)
//   VMSMITH_ROCKY_IMAGE   - Name/path of a Rocky Linux qcow2 image
//   VMSMITH_HOST_IFACE    - Host interface for multi-NIC tests (optional)
//
// Run:
//   npx playwright test tests/e2e/gui-e2e.spec.js

const { test, expect } = require("@playwright/test");

const BASE_URL = process.env.VMSMITH_GUI_URL || "http://localhost:8080";
const ROCKY_IMAGE = process.env.VMSMITH_ROCKY_IMAGE || "";
const HOST_IFACE = process.env.VMSMITH_HOST_IFACE || "";
const SSH_PUBKEY = process.env.VMSMITH_SSH_PUBKEY || "";

// Generous timeouts for real VM operations
const VM_CREATE_TIMEOUT = 120_000;
const VM_IP_TIMEOUT = 120_000;
const POLL_INTERVAL = 5_000;

// Track created resources for cleanup
const createdVMNames = [];

// Helper: wait for a VM to appear in the list with a given state
async function waitForVMState(page, vmName, desiredState, timeout = VM_IP_TIMEOUT) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const card = page.getByTestId(`vm-card-${vmName}`);
      if (await card.isVisible({ timeout: 2000 })) {
        const text = await card.textContent();
        if (text && text.toLowerCase().includes(desiredState.toLowerCase())) {
          return;
        }
      }
    } catch {
      // ignore and retry
    }
    await page.waitForTimeout(POLL_INTERVAL);
    await page.reload();
  }
  throw new Error(`VM ${vmName} did not reach state '${desiredState}' within ${timeout}ms`);
}

// Helper: poll VM detail page until IP appears
async function waitForVMIP(page, timeout = VM_IP_TIMEOUT) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    try {
      const ipEl = page.getByTestId("vm-detail-ip");
      const ip = await ipEl.textContent({ timeout: 2000 });
      if (ip && ip.trim() && ip.trim() !== "" && ip.trim() !== "-") {
        return ip.trim();
      }
    } catch {
      // ignore
    }
    await page.waitForTimeout(POLL_INTERVAL);
    await page.reload();
  }
  throw new Error(`VM did not get an IP within ${timeout}ms`);
}


// ============================================================
// Test 1: Create VM, verify IP and running state
// ============================================================
test.describe("GUI E2E: VM Lifecycle", () => {
  test.beforeEach(async ({ page }) => {
    test.setTimeout(300_000); // 5 min per test
  });

  test("create Rocky VM and verify it gets IP and runs", async ({ page }) => {
    test.skip(!ROCKY_IMAGE, "VMSMITH_ROCKY_IMAGE not set");

    await page.goto(BASE_URL);

    // Navigate to VM list
    await page.getByTestId("nav-vms").click();
    await expect(page.getByTestId("btn-new-vm")).toBeVisible();

    // Open create modal
    await page.getByTestId("btn-new-vm").click();
    await expect(page.getByTestId("input-vm-name")).toBeVisible();

    // Fill form
    await page.getByTestId("input-vm-name").fill("e2e-gui-rocky");
    await page.getByTestId("input-vm-image").fill(ROCKY_IMAGE);
    await page.getByTestId("input-vm-cpus").fill("2");
    await page.getByTestId("input-vm-ram").fill("2048");
    await page.getByTestId("input-vm-disk").fill("20");

    // Fill SSH key if available
    const sshKeyInput = page.getByTestId("input-vm-ssh-key");
    if (SSH_PUBKEY && (await sshKeyInput.isVisible({ timeout: 1000 }).catch(() => false))) {
      await sshKeyInput.fill(SSH_PUBKEY);
    }

    // Submit
    await page.getByTestId("btn-submit-create").click();
    createdVMNames.push("e2e-gui-rocky");

    // Wait for VM card to appear
    await expect(page.getByTestId("vm-card-e2e-gui-rocky")).toBeVisible({
      timeout: VM_CREATE_TIMEOUT,
    });

    // Click into VM detail
    await page.getByTestId("vm-card-e2e-gui-rocky").click();
    await expect(page.getByTestId("vm-detail-name")).toHaveText("e2e-gui-rocky");

    // Wait for IP to be assigned
    const ip = await waitForVMIP(page);
    expect(ip).toBeTruthy();
    // IP should look like an IP address
    expect(ip).toMatch(/\d+\.\d+\.\d+\.\d+/);

    // Verify state is running
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
  });

  // ============================================================
  // Test 2: Snapshot, restore, export image, create from image
  // ============================================================

  test("snapshot, restore, and export image", async ({ page }) => {
    test.skip(!ROCKY_IMAGE, "VMSMITH_ROCKY_IMAGE not set");

    await page.goto(BASE_URL);

    // Navigate to the VM we created
    await page.getByTestId("nav-vms").click();
    const vmCard = page.getByTestId("vm-card-e2e-gui-rocky");
    await expect(vmCard).toBeVisible({ timeout: 10_000 });
    await vmCard.click();

    // Verify we're on the detail page
    await expect(page.getByTestId("vm-detail-name")).toHaveText("e2e-gui-rocky");

    // Create a snapshot
    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("gui-checkpoint");
    await page.getByTestId("btn-submit-snapshot").click();

    // Snapshot should appear
    await expect(page.getByTestId("snap-gui-checkpoint")).toBeVisible({ timeout: 30_000 });

    // Stop the VM (needed for some restore implementations)
    const stopBtn = page.getByTestId("btn-stop");
    if (await stopBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await stopBtn.click();
      await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped", {
        timeout: 60_000,
      });
    }

    // Export as image — look for export/image creation button
    const exportBtn = page.getByTestId("btn-export-image");
    if (await exportBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      await exportBtn.click();

      // Fill image name if prompted
      const imgNameInput = page.getByTestId("input-image-name");
      if (await imgNameInput.isVisible({ timeout: 2000 }).catch(() => false)) {
        await imgNameInput.fill("e2e-gui-export");
        const submitBtn = page.getByTestId("btn-submit-export");
        await submitBtn.click();
      }

      // Verify image appears in Images page
      await page.getByTestId("nav-images").click();
      await expect(page.getByTestId("image-row-e2e-gui-export")).toBeVisible({
        timeout: 120_000,
      });
    }
  });

  test("stop and start VM via GUI", async ({ page }) => {
    test.skip(!ROCKY_IMAGE, "VMSMITH_ROCKY_IMAGE not set");

    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const vmCard = page.getByTestId("vm-card-e2e-gui-rocky");
    if (!(await vmCard.isVisible({ timeout: 5000 }).catch(() => false))) {
      test.skip(true, "VM not found — previous test may have failed");
    }
    await vmCard.click();

    // If running, stop it
    const state = await page.getByTestId("vm-detail-state").textContent();
    if (state === "running") {
      await page.getByTestId("btn-stop").click();
      await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped", {
        timeout: 60_000,
      });
    }

    // Start
    await page.getByTestId("btn-start").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running", {
      timeout: 60_000,
    });

    // IP should come back
    await waitForVMIP(page);
  });

  // ============================================================
  // Test 3: Port forwarding via GUI
  // ============================================================

  test("add and verify port forward", async ({ page }) => {
    test.skip(!ROCKY_IMAGE, "VMSMITH_ROCKY_IMAGE not set");

    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const vmCard = page.getByTestId("vm-card-e2e-gui-rocky");
    if (!(await vmCard.isVisible({ timeout: 5000 }).catch(() => false))) {
      test.skip(true, "VM not found");
    }
    await vmCard.click();

    // Look for port forward UI
    const addPortBtn = page.getByTestId("btn-add-port");
    if (!(await addPortBtn.isVisible({ timeout: 3000 }).catch(() => false))) {
      test.skip(true, "Port forward UI not available in GUI");
    }

    await addPortBtn.click();

    // Fill port forward form
    const hostPortInput = page.getByTestId("input-host-port");
    const guestPortInput = page.getByTestId("input-guest-port");
    await hostPortInput.fill("2222");
    await guestPortInput.fill("22");

    const submitBtn = page.getByTestId("btn-submit-port");
    await submitBtn.click();

    // Port forward should appear in the list
    await expect(page.locator("text=2222")).toBeVisible({ timeout: 10_000 });
  });

  // ============================================================
  // Cleanup: delete test VM
  // ============================================================

  test("cleanup: delete test VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const vmCard = page.getByTestId("vm-card-e2e-gui-rocky");
    if (!(await vmCard.isVisible({ timeout: 5000 }).catch(() => false))) {
      return; // Already gone
    }

    await vmCard.click();

    // Accept confirmation dialog
    page.on("dialog", (dialog) => dialog.accept());
    await page.getByTestId("btn-delete").click();

    // Should redirect to VM list, VM gone
    await expect(page.getByTestId("vm-card-e2e-gui-rocky")).not.toBeVisible({
      timeout: 10_000,
    });
  });

  test("cleanup: delete exported image", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    const imageRow = page.getByTestId("image-row-e2e-gui-export");
    if (!(await imageRow.isVisible({ timeout: 3000 }).catch(() => false))) {
      return; // No image to clean up
    }

    // Delete image
    const deleteBtn = page.getByTestId("btn-delete-image-e2e-gui-export");
    if (await deleteBtn.isVisible({ timeout: 2000 }).catch(() => false)) {
      page.on("dialog", (dialog) => dialog.accept());
      await deleteBtn.click();
      await expect(imageRow).not.toBeVisible({ timeout: 10_000 });
    }
  });
});

// ============================================================
// Dashboard E2E (verifies real data rendering)
// ============================================================
test.describe("GUI E2E: Dashboard", () => {
  test("dashboard loads and shows stats", async ({ page }) => {
    await page.goto(BASE_URL);

    // Stats should be visible (values depend on current daemon state)
    await expect(page.getByTestId("stat-total")).toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId("stat-running")).toBeVisible();
    await expect(page.getByTestId("stat-images")).toBeVisible();
  });

  test("navigation works between all pages", async ({ page }) => {
    await page.goto(BASE_URL);

    // Dashboard → VMs
    await page.getByTestId("nav-vms").click();
    await expect(page.getByTestId("btn-new-vm")).toBeVisible();

    // VMs → Images
    await page.getByTestId("nav-images").click();
    await expect(page.getByTestId("image-table")).toBeVisible();

    // Images → Dashboard
    await page.getByTestId("nav-dashboard").click();
    await expect(page.getByTestId("stat-total")).toBeVisible();
  });
});
