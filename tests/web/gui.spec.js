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

    // Stats should show seeded data (2 VMs, 2 images)
    await expect(page.getByTestId("stat-total")).toHaveText("2");
    await expect(page.getByTestId("stat-running")).toHaveText("1");
    await expect(page.getByTestId("stat-images")).toHaveText("2");
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

  test("template selection surfaces description and tag chips", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-template").selectOption("tmpl-1");

    // Description text from the seeded template (mock-server seeds
    // "Small Ubuntu template" on tmpl-1) must render alongside the hint.
    const desc = page.getByTestId("template-description");
    await expect(desc).toBeVisible();
    await expect(desc).toContainText("Small Ubuntu template");

    const chips = page.getByTestId("template-tag-chips");
    await expect(chips).toBeVisible();
    await expect(chips).toContainText("starter");
    await expect(chips).toContainText("ubuntu");

    // Switching back to "No template" should remove both pieces.
    await page.getByTestId("input-vm-template").selectOption("");
    await expect(page.getByTestId("template-description")).toHaveCount(0);
    await expect(page.getByTestId("template-tag-chips")).toHaveCount(0);
  });

  test("template selector lists templates alphabetically", async ({ page }) => {
    // The Create-VM modal asks the API for `?sort=name` so the dropdown
    // is alphabetical regardless of insertion order. The mock-server seeds
    // tmpl-1 (small-ubuntu) before tmpl-2 (big-rocky), so without the
    // server-side sort the dropdown would show small-ubuntu first.
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select).toBeVisible();
    // Wait for the second template to appear; otherwise the assertion
    // can race the API fetch.
    await expect(select.locator("option")).toHaveCount(3);
    const options = await select.locator("option").evaluateAll((nodes) =>
      nodes.map((n) => ({ value: n.value, text: n.textContent || "" })),
    );
    // Skip the leading "No template" placeholder.
    expect(options[0].value).toBe("");
    expect(options[1].text).toBe("big-rocky");
    expect(options[2].text).toBe("small-ubuntu");
  });

  test("cancel create modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("btn-cancel-create").click();
    await expect(page.getByTestId("input-vm-name")).not.toBeVisible();
  });

  // 2.3.8 — bulk lifecycle: select multiple VMs and apply a lifecycle verb
  // (restart / force-stop / reboot / suspend / resume) from the bulk-action
  // bar.  The mock seeds web-server (running) and db-server (stopped); only
  // running VMs are eligible for restart, so the action should leave
  // db-server alone and the success label should report "1 restarts
  // succeeded".
  test("bulk restart only acts on running VMs", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("checkbox-select-all-vms").check();
    await expect(page.getByTestId("bulk-action-bar")).toContainText("2 selected");
    await expect(page.getByTestId("bulk-action-bar")).toContainText("1 running");
    await expect(page.getByTestId("bulk-action-bar")).toContainText("1 stopped");

    await page.getByTestId("btn-bulk-restart").click();
    // After execution the success summary banner reads "<n> restarts
    // succeeded · <n> skipped".  The mock seeds 1 running + 1 stopped so we
    // expect 1 succeeded and 1 skipped.
    await expect(page.getByText(/1 restart succeeded/)).toBeVisible();
    await expect(page.getByText(/1 skipped/)).toBeVisible();
  });

  test("bulk reboot button is disabled when no running VM is selected", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Tick only the stopped VM (db-server).
    await page.getByTestId("checkbox-select-vm-db-server").check();
    await expect(page.getByTestId("btn-bulk-reboot")).toBeDisabled();
    await expect(page.getByTestId("btn-bulk-suspend")).toBeDisabled();
    // Force-stop also requires a running selection.
    await expect(page.getByTestId("btn-bulk-force-stop")).toBeDisabled();
  });

  test("search input filters the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Both seeded VMs visible without a search.
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // Typing "web" filters down to only the web-server card and persists in URL.
    await page.getByTestId("vm-list-search").fill("web");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("search=web");

    // Clearing via the X button restores both VMs and removes the URL param.
    await page.getByTestId("vm-list-search-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("search=");
  });

  test("search input matches no VMs when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-search").fill("zzz-needle-not-present");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
  });

  test("sort controls reorder the VM list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Default sort=id asc; the seeded "vm-1" (web-server) is rendered before vm-2 (db-server).
    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(2);
    const idAscIds = await cards().evaluateAll(els => els.map(el => el.getAttribute("data-testid")));
    expect(idAscIds[0]).toBe("vm-card-web-server");
    expect(idAscIds[1]).toBe("vm-card-db-server");

    // Switch to sort=name asc -> "db-server" comes before "web-server".
    await page.getByTestId("vm-list-sort-field").selectOption("name");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");

    // Switch order to desc and verify the URL captures the change.
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=name");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
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

  test("shows VM activity timeline in the detail tab", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-activity").click();

    await expect(page.getByTestId("vm-detail-activity")).toBeVisible();
    await expect(page.getByTestId("activity-table")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-1")).not.toBeVisible();
  });

  test("shows empty activity state when the selected VM has no events", async ({ page }) => {
    await page.route("**/api/v1/events?*", async (route) => {
      const url = route.request().url();
      if (url.includes("vm_id=vm-1")) {
        await route.fulfill({
          status: 200,
          headers: {
            "content-type": "application/json",
            "X-Total-Count": "0",
          },
          body: JSON.stringify([]),
        });
        return;
      }
      await route.continue();
    });

    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();
    await page.getByTestId("tab-activity").click();

    await expect(page.getByText("No events yet")).toBeVisible();
    await expect(page.getByText("No lifecycle events for this VM.")).toBeVisible();
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

  test("restart running VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Restart button is only shown while running.
    await expect(page.getByTestId("btn-restart")).toBeVisible();
    await page.getByTestId("btn-restart").click();

    // After restart the VM should still be running.
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
    await expect(page.getByTestId("btn-restart")).toBeVisible();
  });

  test("force-stop running VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Force-stop button is only shown while running.
    await expect(page.getByTestId("btn-force-stop")).toBeVisible();

    // The handler asks for confirmation since this skips graceful shutdown.
    page.once("dialog", (dialog) => dialog.accept());
    await page.getByTestId("btn-force-stop").click();

    // After force-stop the VM is stopped and the Force-stop button disappears.
    await expect(page.getByTestId("vm-detail-state")).toHaveText("stopped");
    await expect(page.getByTestId("btn-force-stop")).toHaveCount(0);
    await expect(page.getByTestId("btn-start")).toBeVisible();
  });

  test("reboot running VM keeps it running", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");

    // Reboot button only appears while running.
    await expect(page.getByTestId("btn-reboot")).toBeVisible();
    await page.getByTestId("btn-reboot").click();

    // Reboot keeps the VM in running state (in-guest reboot, no power cycle).
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
    await expect(page.getByTestId("btn-reboot")).toBeVisible();
  });

  test("suspend running VM and resume", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");

    // Suspend button is only shown while running.
    await expect(page.getByTestId("btn-suspend")).toBeVisible();
    await page.getByTestId("btn-suspend").click();

    // After suspend, state flips to paused and resume button appears.
    await expect(page.getByTestId("vm-detail-state")).toHaveText("paused");
    await expect(page.getByTestId("btn-resume")).toBeVisible();
    await expect(page.getByTestId("btn-suspend")).toHaveCount(0);
    await expect(page.getByTestId("btn-stop")).toHaveCount(0);

    // Resume puts the VM back into running.
    await page.getByTestId("btn-resume").click();
    await expect(page.getByTestId("vm-detail-state")).toHaveText("running");
    await expect(page.getByTestId("btn-suspend")).toBeVisible();
    await expect(page.getByTestId("btn-resume")).toHaveCount(0);
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

  test("create snapshot with description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("noted-snap");
    await page.getByTestId("input-snap-description").fill("captured before risky upgrade");
    await page.getByTestId("btn-submit-snapshot").click();

    await expect(page.getByTestId("snap-noted-snap")).toBeVisible();
    await expect(page.getByTestId("snap-desc-noted-snap")).toHaveText("captured before risky upgrade");
  });

  test("renders existing snapshot description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveText("checkpoint before May deploy");
  });

  test("edit snapshot description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Pre-existing snapshot before-deploy renders its seeded description.
    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveText("checkpoint before May deploy");

    await page.getByTestId("btn-edit-snap-before-deploy").click();
    const ta = page.getByTestId("input-edit-snap-description");
    await expect(ta).toHaveValue("checkpoint before May deploy");
    await ta.fill("rewritten via UI edit");
    await page.getByTestId("btn-submit-edit-snap").click();

    // Description updates inline once the modal closes.
    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveText("rewritten via UI edit");
  });

  test("clear snapshot description via edit", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-snap-before-deploy").click();
    await page.getByTestId("input-edit-snap-description").fill("");
    await page.getByTestId("btn-submit-edit-snap").click();

    // Empty description means the description paragraph disappears entirely.
    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveCount(0);
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

  test("bulk delete selected snapshots", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Three seeded snapshots: before-deploy + two auto-* dailies
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // Tick the two automatic ones
    await page.getByTestId("snap-checkbox-auto-2026-05-06").check();
    await page.getByTestId("snap-checkbox-auto-2026-05-07").check();

    await page.getByTestId("btn-bulk-delete-snaps").click();

    // Both should disappear; manual one is preserved.
    await expect(page.getByTestId("snap-auto-2026-05-06")).not.toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).not.toBeVisible();
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();

    // Result summary shows
    await expect(page.getByTestId("snap-bulk-result")).toContainText("2 of 2 succeeded");
  });

  test("bulk delete via select-all snapshots", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("snap-select-all").check();
    await page.getByTestId("btn-bulk-delete-snaps").click();

    // All three seeded snapshots gone.
    await expect(page.getByTestId("snap-before-deploy")).not.toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).not.toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).not.toBeVisible();
  });

  test("sort controls reorder the snapshot list", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // All three seeded snapshots render before we touch the dropdowns.
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // Helper that pulls every snapshot-row testid in DOM order — each row's
    // testid is exactly `snap-<name>`, with no extra hyphen-segmented suffix
    // (snap-checkbox-*, snap-desc-*, btn-edit-snap-*, snap-bulk-result, ...).
    const rowOrder = () =>
      page.evaluate(() => {
        const seeded = ["before-deploy", "auto-2026-05-06", "auto-2026-05-07"];
        return seeded
          .map((n) => {
            const el = document.querySelector(`[data-testid="snap-${n}"]`);
            return { name: n, top: el ? el.getBoundingClientRect().top : Infinity };
          })
          .sort((a, b) => a.top - b.top)
          .map((r) => r.name);
      });

    // Sort by name ascending -> the two auto-* names come before
    // before-deploy alphabetically.
    await page.getByTestId("snap-sort-field").selectOption("name");
    await page.getByTestId("snap-sort-order").selectOption("asc");
    await expect.poll(rowOrder).toEqual([
      "auto-2026-05-06",
      "auto-2026-05-07",
      "before-deploy",
    ]);

    // Switch to name desc -> before-deploy moves to the top.
    await page.getByTestId("snap-sort-order").selectOption("desc");
    await expect.poll(rowOrder).toEqual([
      "before-deploy",
      "auto-2026-05-07",
      "auto-2026-05-06",
    ]);
  });

  test("snapshot list rejects invalid sort with API error surfaced", async ({ page }) => {
    // The mock-server returns 400 invalid_sort if anything but the whitelisted
    // values is supplied. The UI never sends an invalid value directly, but
    // this test asserts the mock-server contract used by other suites.
    const resp = await page.request.get(`${BASE_URL}/api/v1/vms/vm-1/snapshots?sort=description`);
    expect(resp.status()).toBe(400);
    const body = await resp.json();
    expect(body.code).toBe("invalid_sort");
  });

  test("add port forward with description and see it inline", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-port").click();
    await page.getByTestId("input-host-port").fill("2222");
    await page.getByTestId("input-guest-port").fill("22");
    await page.getByTestId("input-port-description").fill("ssh-jumpbox");
    await page.getByTestId("btn-submit-port").click();

    // Description should render under the new port forward row
    const newDescription = page.locator('[data-testid^="port-description-"]').filter({ hasText: "ssh-jumpbox" });
    await expect(newDescription).toBeVisible();
  });

  test("bulk delete selected port forwards", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Two seeded port forwards on web-server
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();

    await page.getByTestId("port-checkbox-pf-seed-http").check();
    await page.getByTestId("btn-bulk-delete-ports").click();

    // The HTTP rule should be gone; SSH rule should still be there.
    await expect(page.getByTestId("port-row-pf-seed-http")).not.toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();

    await expect(page.getByTestId("port-bulk-result")).toContainText("1 of 1 succeeded");
  });

  test("bulk delete via select-all port forwards", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("port-select-all").check();
    await page.getByTestId("btn-bulk-delete-ports").click();

    await expect(page.getByTestId("port-row-pf-seed-ssh")).not.toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).not.toBeVisible();
  });

  test("port forward sort dropdowns reorder the list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Default sort=id asc — both seeded rows are visible in id order.
    // ID "pf-seed-http" < "pf-seed-ssh" lexicographically.
    const rows = () => page.locator('[data-testid^="port-row-"]');
    await expect(rows()).toHaveCount(2);
    let firstId = await rows().first().getAttribute("data-testid");
    expect(firstId).toBe("port-row-pf-seed-http");

    // Switch to sort=host_port asc → 2222 (ssh) < 8080 (http).
    await page.getByTestId("port-sort-field").selectOption("host_port");
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-ssh");
    await expect.poll(() => new URL(page.url()).search).toContain("port_sort=host_port");

    // Flip to desc → 8080 (http) first.
    await page.getByTestId("port-sort-order").selectOption("desc");
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-http");
    await expect.poll(() => new URL(page.url()).search).toContain("port_order=desc");
  });

  test("port sort by description orders alphabetically (case-insensitive)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("port-sort-field").selectOption("description");
    // pf-seed-ssh has description "ssh-jumpbox"; pf-seed-http has no description.
    // Empty string sorts before "ssh-jumpbox" so pf-seed-http comes first.
    const rows = () => page.locator('[data-testid^="port-row-"]');
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-http");
    await expect(rows().nth(1)).toHaveAttribute("data-testid", "port-row-pf-seed-ssh");
  });

  test("edit port forward description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Seeded SSH rule has a description; verify it renders, then edit it.
    await expect(page.getByTestId("port-description-pf-seed-ssh")).toBeVisible();

    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    const input = page.getByTestId("input-edit-port-description");
    await expect(input).toBeVisible();
    await input.fill("rewritten via UI edit");
    await page.getByTestId("btn-submit-edit-port").click();

    await expect(page.getByTestId("port-description-pf-seed-ssh")).toHaveText("rewritten via UI edit");
  });

  test("clear port forward description via edit", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    await page.getByTestId("input-edit-port-description").fill("");
    await page.getByTestId("btn-submit-edit-port").click();

    // An empty description hides the description line entirely (matches snapshot edit pattern).
    await expect(page.getByTestId("port-description-pf-seed-ssh")).toHaveCount(0);
  });

  test("port modal rejects descriptions over 256 chars", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-port").click();
    await page.getByTestId("input-host-port").fill("4444");
    await page.getByTestId("input-guest-port").fill("80");
    await page.getByTestId("input-port-description").fill("x".repeat(257));
    // The maxLength attribute clamps the typed value to 256, so the field
    // should never exceed the cap.
    const value = await page.getByTestId("input-port-description").inputValue();
    expect(value.length).toBeLessThanOrEqual(256);
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

  test("locked checkbox toggles delete-protection in edit modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Detail summary shows the current Locked state.
    await expect(page.getByTestId("vm-detail-locked")).toContainText(/Locked|Unlocked/);

    await page.getByTestId("btn-edit-vm").click();

    const cb = page.getByTestId("input-edit-locked");
    await expect(cb).toBeVisible();

    // Flip it on, save, and confirm the summary card now reads "Locked".
    await cb.check();
    await page.getByTestId("btn-submit-edit").click();
    await expect(page.getByTestId("input-edit-locked")).not.toBeVisible();
    await expect(page.getByTestId("vm-detail-locked")).toContainText("Locked");

    // Locked badge appears next to the name in the VM list.
    await page.goto(BASE_URL);
    await expect(page.getByTestId("badge-locked-web-server")).toBeVisible();
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

  test("clone VM shows API validation errors and stays on source VM", async ({ page }) => {
    await page.route("**/api/v1/vms/vm-1/clone", async (route) => {
      await route.fulfill({
        status: 400,
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          code: "invalid_name",
          message: 'vm with name "db-server" already exists',
        }),
      });
    });

    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-clone-vm").click();
    await page.getByTestId("input-clone-name").fill("db-server");
    await page.getByTestId("btn-submit-clone").click();

    await expect(page).toHaveURL(/\/vms\/vm-1$/);
    await expect(page.getByTestId("input-clone-name")).toHaveValue("db-server");
    await expect(page.getByText('vm with name "db-server" already exists')).toBeVisible();
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

  test("metrics tab renders the four uPlot charts for a running VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    await expect(page.getByTestId("metric-chart-cpu")).toBeVisible();
    await expect(page.getByTestId("metric-chart-memory")).toBeVisible();
    await expect(page.getByTestId("metric-chart-disk-i/o")).toBeVisible();
    await expect(page.getByTestId("metric-chart-network")).toBeVisible();

    // Each chart wrapper hosts at least one canvas once uPlot mounts.
    await expect(page.locator('[data-testid="metric-chart-cpu"] canvas').first()).toBeVisible();
  });

  test("metrics tab transitions to live status after SSE connects", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    // The mock SSE endpoint emits frames after connecting; the indicator
    // flips from loading -> live as soon as onopen fires.
    await expect(page.getByTestId("live-indicator")).toHaveAttribute("data-status", "live", { timeout: 5000 });
    await expect(page.getByTestId("metrics-live-status")).toHaveText("live");
  });

  test("metrics charts include legend labels for multi-series charts", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-metrics").click();
    // Disk I/O is a two-series chart (Read + Write); verify the legend chips render.
    await expect(page.getByTestId("metric-chart-legend-read")).toBeVisible();
    await expect(page.getByTestId("metric-chart-legend-write")).toBeVisible();
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

  test("renders description and tag badges from seeded image", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Seeded image carries description "Stock Ubuntu cloud image" and tags "ubuntu" + "stable".
    await expect(page.getByTestId("image-description-ubuntu-base")).toContainText("Stock Ubuntu");
    const tags = page.getByTestId("image-tags-ubuntu-base");
    await expect(tags).toContainText("ubuntu");
    await expect(tags).toContainText("stable");
  });

  test("filters images by tag chip", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Both seeded images should be visible at first.
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Filter to ubuntu — rocky row drops out.
    await page.getByTestId("image-tag-filter-ubuntu").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();

    // Reset.
    await page.getByTestId("image-tag-filter-all").click();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();
  });

  test("edit modal updates description and tags", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    await page.getByTestId("btn-edit-image-rocky-experimental").click();
    await expect(page.getByTestId("edit-image-modal")).toBeVisible();

    await page.getByTestId("edit-image-description").fill("Promoted to release candidate");
    await page.getByTestId("edit-image-tags").fill("rocky,rc");
    await page.getByTestId("btn-save-image").click();

    await expect(page.getByTestId("edit-image-modal")).not.toBeVisible();
    await expect(page.getByTestId("image-description-rocky-experimental")).toContainText("release candidate");
    const tags = page.getByTestId("image-tags-rocky-experimental");
    await expect(tags).toContainText("rocky");
    await expect(tags).toContainText("rc");
  });

  test("bulk delete selected images", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Two seeded images visible
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    await page.getByTestId("image-checkbox-rocky-experimental").check();
    await page.getByTestId("btn-bulk-delete-images").click();

    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-bulk-result")).toContainText("1 of 1 succeeded");
  });

  test("bulk delete via select-all images", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    await page.getByTestId("image-select-all").check();
    await page.getByTestId("btn-bulk-delete-images").click();

    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
  });

  test("sort controls reorder the image list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Default sort=id asc; the seeded "img-1" (ubuntu-base) renders before img-2 (rocky-experimental).
    const rows = () => page.getByTestId(/^image-row-/);
    await expect(rows()).toHaveCount(2);
    const idAsc = await rows().evaluateAll(els => els.map(el => el.getAttribute("data-testid")));
    expect(idAsc[0]).toBe("image-row-ubuntu-base");
    expect(idAsc[1]).toBe("image-row-rocky-experimental");

    // Switch to sort=name asc -> "rocky-experimental" (r < u) comes first.
    await page.getByTestId("image-list-sort-field").selectOption("name");
    await expect(rows().first()).toHaveAttribute("data-testid", "image-row-rocky-experimental");

    // sort=size desc -> rocky (2 GB) before ubuntu (1 GB). URL captures the change.
    await page.getByTestId("image-list-sort-field").selectOption("size");
    await page.getByTestId("image-list-sort-order").selectOption("desc");
    await expect(rows().first()).toHaveAttribute("data-testid", "image-row-rocky-experimental");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=size");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
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

    // Settings
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    // Back to dashboard
    await page.getByTestId("nav-dashboard").click();
    await expect(page.getByTestId("stat-total")).toBeVisible();
  });
});

// ============================================================
// Settings — Webhooks (roadmap 4.2.16)
// ============================================================
test.describe("Settings — Webhooks", () => {
  test("create, send test event, and delete a webhook", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    // Create.
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/hook");
    await page.getByTestId("webhook-secret-input").fill("topsecret");
    await page.getByTestId("webhook-event-types-input").fill("vm.started, system.*");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

    // Send test event — mock-server reports a 204 success.
    await page.getByTestId(`webhook-test-${rowID}`).click();
    const status = page.getByTestId("webhook-status").first();
    await expect(status).toContainText(/204|test ok/i);

    // Delete.
    page.on("dialog", (dialog) => dialog.accept());
    await page.getByTestId(`webhook-delete-${rowID}`).click();
    await expect(page.getByTestId(`webhook-row-${rowID}`)).not.toBeVisible();
  });

  test("send-test surfaces failure for failing receivers", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    // Mock-server returns failure when URL contains "fail".
    await page.getByTestId("webhook-url-input").fill("https://example.com/fail");
    await page.getByTestId("webhook-secret-input").fill("k");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");
    await page.getByTestId(`webhook-test-${rowID}`).click();
    await expect(page.getByTestId("webhook-status").first()).toContainText(/HTTP 500|failed|500/i);
  });

  // 2.2.14 — editable webhook config (URL / secret / event types / active).
  test("edit webhook URL and event-type filter via PATCH modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/hook");
    await page.getByTestId("webhook-secret-input").fill("topsecret");
    await page.getByTestId("webhook-event-types-input").fill("vm.started");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await expect(page.getByTestId("edit-webhook-form")).toBeVisible();

    // The form pre-fills the current URL.
    await expect(page.getByTestId("edit-webhook-url-input")).toHaveValue("https://example.com/hook");

    // Mutate URL and replace event-type filter list.
    await page.getByTestId("edit-webhook-url-input").fill("https://example.com/new-hook");
    await page.getByTestId("edit-webhook-event-types-input").fill("vm.created, vm.deleted");
    await page.getByTestId("edit-webhook-submit").click();

    // Modal closes and the row reflects the new URL + filter chips.
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    await expect(row).toContainText("https://example.com/new-hook");
    await expect(row).toContainText("vm.created");
    await expect(row).toContainText("vm.deleted");
  });

  test("edit webhook can clear event-type filter to subscribe-all", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/clear");
    await page.getByTestId("webhook-secret-input").fill("k");
    await page.getByTestId("webhook-event-types-input").fill("vm.stopped");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await page.getByTestId("edit-webhook-subscribe-all").check();
    await page.getByTestId("edit-webhook-submit").click();

    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    await expect(row).toContainText(/all events/i);
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

  test("search input filters the activity timeline and round-trips through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // All three seeded events visible before typing.
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();

    // Type a substring that only matches evt-1's message ("database-staging").
    await page.getByTestId("activity-filter-search").fill("database-staging");

    // Debounce settles after 250 ms; evt-1 stays, evt-2 and evt-3 go away.
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-2")).toHaveCount(0);

    // The committed query lands in the URL so the filtered view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("search")).toBe("database-staging");

    // Clearing via the in-input X removes the param and restores the list.
    await page.getByTestId("btn-activity-clear-search").click();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("search")).toBe(null);
  });

  test("search input matches no events when query has no hits", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    await page.getByTestId("activity-filter-search").fill("needle-not-present");
    // Empty-state copy shows up after the debounced fetch returns zero rows.
    await expect(page.getByText("No events yet")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
  });

  test("search input matches against attribute values", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // The mock server seeds evt-2 with attributes.template = "rocky9-base"; a
    // substring match should keep evt-2 and hide the others.
    await page.getByTestId("activity-filter-search").fill("rocky9");
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-1")).toHaveCount(0);
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

// ============================================================
// Layout footer / build info
// ============================================================
test.describe("Layout footer", () => {
  test("renders the build version returned by /api/version", async ({ page }) => {
    await page.goto(BASE_URL);
    const footer = page.getByTestId("layout-version");
    await expect(footer).toBeVisible();
    await expect(footer).toContainText("VM Smith v0.0.0-mock");
    const title = await footer.getAttribute("title");
    expect(title).toContain("commit mockcommit");
    expect(title).toContain("2026-05-06T00:00:00Z");
  });

  test("falls back to a static label when /api/version fails", async ({ page }) => {
    await page.route("**/api/version", (route) => route.fulfill({ status: 500, body: "{}" }));
    await page.goto(BASE_URL);
    const footer = page.getByTestId("layout-version");
    await expect(footer).toBeVisible();
    await expect(footer).toHaveText("VM Smith");
  });
});
