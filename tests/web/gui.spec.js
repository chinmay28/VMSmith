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

  // 5.4.12 — template search filter narrows the Create-VM template dropdown.
  // Mirrors the 5.4.9 / 5.4.10 / 5.4.11 search filter shape: debounced 250 ms
  // input above the dropdown, case-insensitive substring match across name,
  // description, and tags, with an X clear button.
  test("template search filter narrows the template dropdown", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select.locator("option")).toHaveCount(3); // placeholder + 2 templates

    // Search for "rocky" — should reduce the dropdown to big-rocky only.
    await page.getByTestId("template-search-input").fill("rocky");
    await expect(select.locator("option")).toHaveCount(2);
    await expect(select.locator("option").nth(1)).toHaveText("big-rocky");

    // Clear via the X button and the full list comes back.
    await page.getByTestId("template-search-clear").click();
    await expect(select.locator("option")).toHaveCount(3);
  });

  test("template search filter is case-insensitive and matches description", async ({ page }) => {
    // The mock-server seeds two templates with descriptions: "Small Ubuntu
    // template" and "Big Rocky template". A case-insensitive needle that
    // only appears in one description should reduce the dropdown to that
    // template — confirming the haystack covers description, not just name.
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select.locator("option")).toHaveCount(3);

    await page.getByTestId("template-search-input").fill("UBUNTU");
    await expect(select.locator("option")).toHaveCount(2);
    await expect(select.locator("option").nth(1)).toHaveText("small-ubuntu");
  });

  test("template search shows empty-state hint when no templates match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select.locator("option")).toHaveCount(3);

    await page.getByTestId("template-search-input").fill("needle-not-present");
    // Only the placeholder remains; its label is updated to reflect the
    // current needle so the user can see why the list is empty.
    await expect(select.locator("option")).toHaveCount(1);
    await expect(select.locator("option").first()).toHaveText(
      /No templates match "needle-not-present"/
    );
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

  // 5.4.22 — image filter on the VM list.  Seed data: web-server uses
  // image "ubuntu-22.04", db-server uses image "rocky-9".  The image input
  // is a 250 ms-debounced text box with URL round-trip via ?image=.
  test("image filter narrows the VM list to a single base image and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Both seeded VMs visible without an image filter.
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // Filter by exact image "ubuntu-22.04" -> only web-server visible.
    await page.getByTestId("vm-list-image-filter").fill("ubuntu-22.04");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("image=ubuntu-22.04");

    // Clearing via the X button restores both VMs and removes the URL param.
    await page.getByTestId("vm-list-image-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("image=");
  });

  test("image filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-image-filter").fill("ROCKY-9");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
  });

  test("image filter matches no VMs when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-image-filter").fill("does-not-exist.qcow2");
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

  test("auto-start filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Flip auto_start=true on web-server via the API so we have one VM with the flag
    // set and one without. Using page.request keeps the seed shape stable for other
    // tests in this describe block while giving the filter a concrete population.
    await page.request.patch(`${BASE_URL}/api/v1/vms/vm-1`, { data: { auto_start: true } });

    await page.getByTestId("vm-list-auto-start-filter").selectOption("true");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("auto_start=true");

    await page.getByTestId("vm-list-auto-start-filter").selectOption("false");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("auto_start=false");

    // "Any" clears the filter and the URL param.
    await page.getByTestId("vm-list-auto-start-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("auto_start=");
  });

  test("locked filter narrows the VM list", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.request.patch(`${BASE_URL}/api/v1/vms/vm-2`, { data: { locked: true } });

    await page.getByTestId("vm-list-locked-filter").selectOption("true");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("locked=true");
  });

  test("auto-start and locked filter values hydrate from the URL on load", async ({ page }) => {
    // Pre-seed: flip flags on both VMs then load the page with both filters set.
    await page.goto(BASE_URL);
    await page.request.patch(`${BASE_URL}/api/v1/vms/vm-1`, { data: { auto_start: true, locked: true } });

    await page.goto(`${BASE_URL}/vms?auto_start=true&locked=true`);
    await expect(page.getByTestId("vm-list-auto-start-filter")).toHaveValue("true");
    await expect(page.getByTestId("vm-list-locked-filter")).toHaveValue("true");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
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

  test("snapshot search input filters the snapshot list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // All three seeded snapshots are visible before typing.
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // Typing "auto" should leave only the auto-* rows.
    await page.getByTestId("snap-list-search").fill("auto");
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();
    await expect(page.getByTestId("snap-before-deploy")).toHaveCount(0);

    // The committed query is persisted to the URL via ?snap_search=.
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_search")).toBe("auto");

    // Clearing the input restores all three.
    await page.getByTestId("snap-list-search-clear").click();
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_search")).toBeNull();
  });

  test("snapshot search input matches the description field", async ({ page }) => {
    // 'before-deploy' has description "checkpoint before May deploy" — the
    // word "checkpoint" only appears in the description, not in any name.
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("snap-list-search").fill("checkpoint");
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toHaveCount(0);
    await expect(page.getByTestId("snap-auto-2026-05-07")).toHaveCount(0);
  });

  test("snapshot search input shows empty state when no snapshots match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("snap-list-search").fill("needle-not-present");
    // All three rows disappear; the empty-state card surfaces the needle in
    // its description.
    await expect(page.getByTestId("snap-before-deploy")).toHaveCount(0);
    await expect(page.getByText(/No snapshots match "needle-not-present"/)).toBeVisible();
  });

  // --- Snapshot tags (roadmap 2.2.17) ---

  test("create snapshot with tags renders chips inline", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("tagged-snap");
    // Mixed case + duplicate to exercise the mock-server normalisation.
    await page.getByTestId("input-snap-tags").fill("Audit, production, audit");
    await page.getByTestId("btn-submit-snapshot").click();

    const chipBox = page.getByTestId("snap-tags-tagged-snap");
    await expect(chipBox).toBeVisible();
    await expect(chipBox).toHaveText(/audit/);
    await expect(chipBox).toHaveText(/production/);
  });

  test("edit snapshot tags via the edit modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Seed a fresh tagged snapshot via the Create flow so we can edit it.
    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("editable-snap");
    await page.getByTestId("input-snap-tags").fill("staging");
    await page.getByTestId("btn-submit-snapshot").click();
    await expect(page.getByTestId("snap-tags-editable-snap")).toContainText(/staging/);

    await page.getByTestId("btn-edit-snap-editable-snap").click();
    await expect(page.getByTestId("input-edit-snap-tags")).toHaveValue("staging");
    await page.getByTestId("input-edit-snap-tags").fill("production, audit");
    await page.getByTestId("btn-submit-edit-snap").click();

    const chipBox = page.getByTestId("snap-tags-editable-snap");
    await expect(chipBox).toHaveText(/audit/);
    await expect(chipBox).toHaveText(/production/);
    await expect(chipBox).not.toHaveText(/staging/);
  });

  test("clear snapshot tags via empty input", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Seed and clear.
    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("clearable-snap");
    await page.getByTestId("input-snap-tags").fill("doomed");
    await page.getByTestId("btn-submit-snapshot").click();
    await expect(page.getByTestId("snap-tags-clearable-snap")).toContainText(/doomed/);

    await page.getByTestId("btn-edit-snap-clearable-snap").click();
    await page.getByTestId("input-edit-snap-tags").fill("");
    await page.getByTestId("btn-submit-edit-snap").click();

    // Tag chips removed entirely once cleared.
    await expect(page.getByTestId("snap-tags-clearable-snap")).toHaveCount(0);
  });

  test("snapshot search input matches against tag values", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("tagged-search-snap");
    await page.getByTestId("input-snap-tags").fill("rocketscience");
    await page.getByTestId("btn-submit-snapshot").click();
    await expect(page.getByTestId("snap-tagged-search-snap")).toBeVisible();

    // The seed snapshots ("before-deploy", "auto-...") never carry the
    // rocketscience tag, so the search box should narrow the list to
    // just the newly created one.
    await page.getByTestId("snap-list-search").fill("rocketscience");
    await expect(page.getByTestId("snap-tagged-search-snap")).toBeVisible();
    await expect(page.getByTestId("snap-before-deploy")).toHaveCount(0);
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

  test("port forward search input filters the list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Both seeded port forwards visible before typing.
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();

    // Type "jumpbox" — only the ssh rule has that in description.
    await page.getByTestId("port-list-search").fill("jumpbox");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_search")).toBe("jumpbox");

    // Clearing restores both rows.
    await page.getByTestId("port-list-search-clear").click();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_search")).toBeNull();
  });

  test("port forward search matches by host port", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // pf-seed-http has host_port 8080; pf-seed-ssh has 2222. Searching 8080
    // should leave only the http rule.
    await page.getByTestId("port-list-search").fill("8080");
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
  });

  test("port forward search shows empty state when no rules match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("port-list-search").fill("needle-not-present");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect(page.getByText(/No port forwards match "needle-not-present"/)).toBeVisible();
  });

  test("protocol filter narrows the port-forward list and round-trips through the URL (5.4.25)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Both seeded port forwards are tcp — udp filter should show neither.
    await page.getByTestId("port-protocol-filter").selectOption("udp");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_protocol")).toBe("udp");

    // tcp filter shows both seeded rules.
    await page.getByTestId("port-protocol-filter").selectOption("tcp");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_protocol")).toBe("tcp");
  });

  test("protocol filter \"Any protocol\" returns every rule (5.4.25)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Switch through tcp then back to "" to confirm Any clears the URL param
    // and restores every rule.
    await page.getByTestId("port-protocol-filter").selectOption("tcp");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();

    await page.getByTestId("port-protocol-filter").selectOption("");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_protocol")).toBeNull();
  });

  test("protocol filter composes with the existing search filter (5.4.25)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // search for "jumpbox" (matches only pf-seed-ssh) AND protocol=tcp.
    await page.getByTestId("port-protocol-filter").selectOption("tcp");
    await page.getByTestId("port-list-search").fill("jumpbox");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);

    // Same search but protocol=udp — should leave the list empty since
    // pf-seed-ssh is tcp.
    await page.getByTestId("port-protocol-filter").selectOption("udp");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
  });

  // 5.4.20 — paginated port-forward list.
  test("port forward pagination controls reflect X-Total-Count and step pages", async ({ page }) => {
    // Synthesize 30 rules so the pagination widget has multiple pages.
    const FIXTURE_COUNT = 30;
    const synthetic = [];
    for (let i = 1; i <= FIXTURE_COUNT; i += 1) {
      synthetic.push({
        id: `pf-page-${String(i).padStart(2, "0")}`,
        vm_id: "vm-1",
        host_port: 30000 + i,
        guest_port: 22,
        guest_ip: "192.168.100.10",
        protocol: "tcp",
        description: `synthetic rule ${i}`,
      });
    }
    await page.route("**/api/v1/vms/vm-1/ports*", async (route) => {
      const url = new URL(route.request().url());
      const perPageRaw = url.searchParams.get("per_page") || url.searchParams.get("limit") || "";
      const pageRaw = url.searchParams.get("page") || "";
      const perPage = Number.parseInt(perPageRaw, 10);
      let pageNum = Number.parseInt(pageRaw, 10);
      let body = synthetic;
      if (Number.isFinite(perPage) && perPage > 0) {
        if (!Number.isFinite(pageNum) || pageNum < 1) pageNum = 1;
        const start = (pageNum - 1) * perPage;
        body = start >= synthetic.length ? [] : synthetic.slice(start, start + perPage);
      }
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "application/json",
          "X-Total-Count": String(FIXTURE_COUNT),
        },
        body: JSON.stringify(body),
      });
    });

    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    const pagination = page.getByTestId("port-pagination");
    await expect(pagination).toBeVisible();
    // First page: 1-25 of 30 (default perPage=25). The first rule is rendered.
    await expect(pagination).toContainText("1-25");
    await expect(pagination).toContainText("of 30");
    await expect(page.getByTestId("port-row-pf-page-01")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-page-26")).toHaveCount(0);

    // Click Next → page 2 shows rules 26-30 and the first-page rule disappears.
    await pagination.getByRole("button", { name: "Next" }).click();
    await expect(pagination).toContainText("26-30");
    await expect(page.getByTestId("port-row-pf-page-26")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-page-01")).toHaveCount(0);

    // Next is disabled on the last page; Previous is enabled.
    await expect(pagination.getByRole("button", { name: "Next" })).toBeDisabled();
    await expect(pagination.getByRole("button", { name: "Previous" })).toBeEnabled();

    // URL captures the page index so the filtered view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("port_page")).toBe("2");
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

  test("add port forward with tags renders chips inline", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-new-port").click();
    await page.getByTestId("input-host-port").fill("3333");
    await page.getByTestId("input-guest-port").fill("33");
    await page.getByTestId("input-port-tags").fill("PRODUCTION, web");
    await page.getByTestId("btn-submit-port").click();

    // Tag chips render under the new port-forward row.
    const tagsRow = page.locator('[data-testid^="port-tags-"]').last();
    await expect(tagsRow).toContainText("production");
    await expect(tagsRow).toContainText("web");
  });

  test("edit port forward tags and clear via empty input", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // First, set tags via the edit modal.
    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    await page.getByTestId("input-edit-port-tags").fill("audit, jumpbox");
    await page.getByTestId("btn-submit-edit-port").click();

    const tagsRow = page.getByTestId("port-tags-pf-seed-ssh");
    await expect(tagsRow).toContainText("audit");
    await expect(tagsRow).toContainText("jumpbox");

    // Now clear via the edit modal.
    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    await page.getByTestId("input-edit-port-tags").fill("");
    await page.getByTestId("btn-submit-edit-port").click();

    // Tags chip row should disappear entirely.
    await expect(page.getByTestId("port-tags-pf-seed-ssh")).toHaveCount(0);
  });

  test("port search input matches port-forward tags", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    // Set tags on the SSH rule, then verify the search box hits via tag.
    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    await page.getByTestId("input-edit-port-tags").fill("jumpgateway");
    await page.getByTestId("btn-submit-edit-port").click();

    await page.getByTestId("port-list-search").fill("jumpgateway");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
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

  test("search input filters the image list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Both seeded images visible initially.
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Type "rocky" — the ubuntu row drops out after the debounce settles.
    await page.getByTestId("image-list-search").fill("rocky");
    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).toContain("search=rocky");

    // Clear the search — the ubuntu row comes back and the URL drops `search=`.
    await page.getByTestId("image-list-search-clear").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("search=");
  });

  test("search input matches no images when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    await page.getByTestId("image-list-search").fill("needle-not-present");
    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
  });

  // 5.4.27 — `?source_vm=` filter on /api/v1/images. The mock seeds img-1
  // (ubuntu-base) with source_vm = "vm-1" and img-2 (rocky-experimental) with
  // no source_vm, so filtering by "vm-1" should drop the rocky row.
  test("source-vm filter narrows the image list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Both seeded images visible initially.
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Filter by the bastion VM ID — only the ubuntu-base image (exported from
    // vm-1) survives. The rocky image has no source_vm so it drops out.
    await page.getByTestId("image-list-source-vm").fill("vm-1");
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).search).toContain("source_vm=vm-1");

    // Mixed-case input matches case-insensitively.
    await page.getByTestId("image-list-source-vm").fill("VM-1");
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();

    // Clear the filter — rocky comes back and the URL drops the param.
    await page.getByTestId("image-list-source-vm-clear").click();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("source_vm=");
  });

  test("source-vm filter empty-state surfaces a tailored message", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    await page.getByTestId("image-list-source-vm").fill("vm-does-not-exist");
    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
    await expect(page.getByText("No images were exported from source VM \"vm-does-not-exist\".")).toBeVisible();
  });
});

// ============================================================
// Templates admin page (roadmap 2.3.9 GUI follow-up)
// ============================================================
test.describe("Templates", () => {
  test("lists seeded templates with descriptions and tag chips", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-table")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-description-small-ubuntu")).toContainText("Small Ubuntu template");
    await expect(page.getByTestId("template-tags-small-ubuntu")).toBeVisible();
  });

  test("filters templates by tag chip", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await page.getByTestId("template-tag-filter-prod").click();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await page.getByTestId("template-tag-filter-all").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
  });

  test("search input filters templates and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-search").fill("rocky");
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("search")).toBe("rocky");

    await page.getByTestId("template-list-search-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("search=");
  });

  test("image filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-image-filter").fill("/images/rocky9.qcow2");
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("image")).toBe("/images/rocky9.qcow2");

    await page.getByTestId("template-list-image-filter-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("image=");
  });

  test("image filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-image-filter").fill("/IMAGES/ROCKY9.QCOW2");
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  test("image filter matches no templates when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-image-filter").fill("/images/fedora.qcow2");
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  test("sort dropdowns reorder templates and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-sort-field").selectOption("name");
    await page.getByTestId("template-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("name");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("desc");

    const rows = page.locator('[data-testid^="template-row-"]');
    await expect(rows).toHaveCount(2);
    // name desc → "small-ubuntu" precedes "big-rocky" alphabetically (s > b)
    await expect(rows.nth(0)).toHaveAttribute("data-testid", "template-row-small-ubuntu");
    await expect(rows.nth(1)).toHaveAttribute("data-testid", "template-row-big-rocky");
  });

  test("edit modal updates description and tags via PATCH", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await page.getByTestId("btn-edit-template-big-rocky").click();
    await expect(page.getByTestId("edit-template-modal")).toBeVisible();
    await page.getByTestId("edit-template-description").fill("Promoted to GA");
    await page.getByTestId("edit-template-tags").fill("rocky,ga");
    await page.getByTestId("btn-save-template").click();
    await expect(page.getByTestId("edit-template-modal")).not.toBeVisible();
    await expect(page.getByTestId("template-description-big-rocky")).toContainText("Promoted to GA");
  });

  test("bulk delete selected templates", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await page.getByTestId("template-checkbox-big-rocky").check();
    await page.getByTestId("btn-bulk-delete-templates").click();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-bulk-result")).toContainText("1 of 1 succeeded");
  });

  test("bulk delete via select-all sweeps every template", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await page.getByTestId("template-select-all").check();
    await page.getByTestId("btn-bulk-delete-templates").click();
    // Once the second row drops, the table card is replaced by the EmptyState
    // branch, so just assert the rows are gone and the empty state renders.
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  test("delete single template via row action", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    page.once("dialog", (d) => d.accept());
    await page.getByTestId("btn-delete-template-small-ubuntu").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
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

    // Templates
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-table")).toBeVisible();

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

  // 2.2.14 — free-text description on webhooks (create + edit + search).
  test("create webhook with a description renders it in the row", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/hook");
    await page.getByTestId("webhook-secret-input").fill("topsecret");
    await page.getByTestId("webhook-description-input").fill("Slack notifier for crashes");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");
    await expect(page.getByTestId(`webhook-description-${rowID}`)).toHaveText(
      "Slack notifier for crashes",
    );
  });

  test("edit webhook description and clear it via empty input", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/desc");
    await page.getByTestId("webhook-secret-input").fill("k");
    await page.getByTestId("webhook-description-input").fill("Initial label");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

    // Edit: rename the description.
    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await expect(page.getByTestId("edit-webhook-form")).toBeVisible();
    await expect(page.getByTestId("edit-webhook-description-input")).toHaveValue("Initial label");
    await page.getByTestId("edit-webhook-description-input").fill("Renamed: PagerDuty escalation");
    await page.getByTestId("edit-webhook-submit").click();
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    await expect(page.getByTestId(`webhook-description-${rowID}`)).toHaveText(
      "Renamed: PagerDuty escalation",
    );

    // Edit again: clear the description.
    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await page.getByTestId("edit-webhook-description-input").fill("");
    await page.getByTestId("edit-webhook-submit").click();
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    await expect(page.getByTestId(`webhook-description-${rowID}`)).toHaveCount(0);
  });

  test("search input matches webhook description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    const seed = async (url, description) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      if (description) await page.getByTestId("webhook-description-input").fill(description);
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://a.example.com/x", "Slack alerts");
    await seed("https://b.example.com/x", "PagerDuty escalation");
    await seed("https://c.example.com/x", "");

    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // "pagerduty" only appears in the description of webhook #2.
    await page.getByTestId("webhook-list-search").fill("pagerduty");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("b.example.com");
  });

  test("create webhook with tags renders chips in the row", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/tags");
    await page.getByTestId("webhook-secret-input").fill("k");
    // Mixed-case + whitespace + duplicate — must be normalised + deduped
    // before they reach the row chips.
    await page.getByTestId("webhook-tags-input").fill(" Production , audit, production ");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");
    const chipBox = page.getByTestId(`webhook-tags-${rowID}`);
    await expect(chipBox).toBeVisible();
    // Normalisation: lowercased, deduplicated, alphabetised.
    await expect(chipBox).toHaveText(/audit/);
    await expect(chipBox).toHaveText(/production/);
  });

  test("edit webhook tags and clear them via empty input", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/tag-edit");
    await page.getByTestId("webhook-secret-input").fill("k");
    await page.getByTestId("webhook-tags-input").fill("staging");
    await page.getByTestId("webhook-create-submit").click();

    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();
    const rowID = (await row.getAttribute("data-testid")).replace("webhook-row-", "");

    // Edit: replace the tags.
    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await expect(page.getByTestId("edit-webhook-form")).toBeVisible();
    await expect(page.getByTestId("edit-webhook-tags-input")).toHaveValue("staging");
    await page.getByTestId("edit-webhook-tags-input").fill("production, audit");
    await page.getByTestId("edit-webhook-submit").click();
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    const chipBox = page.getByTestId(`webhook-tags-${rowID}`);
    await expect(chipBox).toHaveText(/audit/);
    await expect(chipBox).toHaveText(/production/);
    await expect(chipBox).not.toHaveText(/staging/);

    // Edit again: clear the tags.
    await page.getByTestId(`webhook-edit-${rowID}`).click();
    await page.getByTestId("edit-webhook-tags-input").fill("");
    await page.getByTestId("edit-webhook-submit").click();
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();
    await expect(page.getByTestId(`webhook-tags-${rowID}`)).toHaveCount(0);
  });

  test("search input matches webhook tags", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    const seed = async (url, tagsCsv) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      if (tagsCsv) await page.getByTestId("webhook-tags-input").fill(tagsCsv);
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://a.example.com/x", "production");
    await seed("https://b.example.com/x", "staging");
    await seed("https://c.example.com/x", "");

    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // "staging" only appears as a tag on webhook #2.
    await page.getByTestId("webhook-list-search").fill("staging");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("b.example.com");
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

  // Search filter: 250 ms-debounced free-text filter applied across URL and
  // event-type strings. Mirrors the 5.4.x symmetric search surface (VMs,
  // images, events, snapshots, port forwards, templates, logs).
  test("search input filters the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    // Seed three webhooks: one whose URL contains "audit", one whose URL
    // contains "metrics", and one whose event-type filter contains
    // "image.created".
    const seed = async (url, types) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      if (types) await page.getByTestId("webhook-event-types-input").fill(types);
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://hooks.example.com/audit", "vm.started, vm.stopped");
    await seed("https://metrics.example.com/in", "image.created");
    await seed("https://otherhost.example.com/x", "");

    const search = page.getByTestId("webhook-list-search");
    await expect(search).toHaveAttribute("placeholder", "Search by URL, description, or event type…");

    // All three rows are visible to start.
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // Search by a URL substring — only the audit row should remain.
    await search.fill("audit");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("hooks.example.com/audit");

    // URL round-trip — ?search=audit is reflected in the address bar.
    await expect.poll(async () => new URL(page.url()).searchParams.get("search")).toBe("audit");

    // Clear button restores the unfiltered view.
    await page.getByTestId("webhook-list-search-clear").click();
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);
    await expect.poll(async () => new URL(page.url()).searchParams.get("search")).toBe(null);

    // Search by an event-type substring — only the metrics row should remain
    // (its filter contains "image.created").
    await page.getByTestId("webhook-list-search").fill("image.created");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("metrics.example.com");
  });

  test("search shows empty state when no webhooks match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();

    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://hooks.example.com/audit");
    await page.getByTestId("webhook-secret-input").fill("k");
    await page.getByTestId("webhook-create-submit").click();

    await page.getByTestId("webhook-list-search").fill("needle-not-anywhere");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(0);
    await expect(page.getByText(/No webhooks match your search/i)).toBeVisible();
  });

  // 5.4.26 — explicit-membership event-type filter on the webhook list.
  test("event-type filter narrows the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    // Seed three webhooks: one subscribed to vm.created only, one subscribed
    // to image.created, and one catch-all (no event_types). The catch-all
    // matches every event behaviourally, but must NOT be returned by the
    // explicit-membership filter.
    const seed = async (url, types) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      if (types) await page.getByTestId("webhook-event-types-input").fill(types);
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://vm.example.com/hook", "vm.created");
    await seed("https://image.example.com/hook", "image.created");
    await seed("https://catchall.example.com/hook", ""); // catch-all (no event_types)

    // All three rows render initially.
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // Filter by vm.created — only the vm subscriber should remain; the
    // catch-all webhook must not appear even though it would fire for the
    // event behaviourally.
    await page.getByTestId("webhook-list-event-type-filter").fill("vm.created");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("vm.example.com");

    // URL round-trip — ?event_type=vm.created is reflected in the address bar.
    await expect.poll(async () => new URL(page.url()).searchParams.get("event_type")).toBe("vm.created");

    // Case-insensitive matching: VM.CREATED is normalised to vm.created on
    // the wire so the daemon returns the same row.
    await page.getByTestId("webhook-list-event-type-filter").fill("VM.CREATED");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);

    // Clear button restores the unfiltered view.
    await page.getByTestId("webhook-list-event-type-filter-clear").click();
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);
    await expect.poll(async () => new URL(page.url()).searchParams.get("event_type")).toBe(null);

    // No-match shows the dedicated empty-state copy referencing the filter.
    await page.getByTestId("webhook-list-event-type-filter").fill("snapshot.taken");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(0);
    await expect(page.getByText(/No webhooks explicitly subscribe/i)).toBeVisible();
  });

  // 5.4.15 — sortable webhook list (sort + order dropdowns).
  test("sort dropdowns reorder the webhook list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();

    // Seed three webhooks with URLs that have a deterministic alphabetical
    // order: alpha < bravo < charlie.
    const seed = async (url) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://Charlie.example.com/h");
    await seed("https://alpha.example.com/h");
    await seed("https://Bravo.example.com/h");

    // Sort by URL ascending (case-insensitive) — first row must be alpha.
    await page.getByTestId("webhook-list-sort-field").selectOption("url");
    await expect.poll(async () => {
      const text = await page.locator('[data-testid^="webhook-row-"]').first().innerText();
      return text.toLowerCase();
    }).toContain("alpha.example.com");

    // URL round-trip — ?sort=url is reflected in the address bar.
    await expect.poll(async () => new URL(page.url()).searchParams.get("sort")).toBe("url");

    // Flip the order — first row becomes charlie.
    await page.getByTestId("webhook-list-sort-order").selectOption("desc");
    await expect.poll(async () => {
      const text = await page.locator('[data-testid^="webhook-row-"]').first().innerText();
      return text.toLowerCase();
    }).toContain("charlie.example.com");
    await expect.poll(async () => new URL(page.url()).searchParams.get("order")).toBe("desc");
  });

  // 2.3.10 — webhook bulk-delete.
  test("bulk delete selected webhooks via checkbox + Delete-selected", async ({ page }) => {
    page.on("dialog", (d) => d.accept());
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();

    // Create three webhooks.
    for (const url of ["https://example.com/bulk-a", "https://example.com/bulk-b", "https://example.com/bulk-c"]) {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("s");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.locator(`text=${url}`)).toBeVisible();
    }
    const rows = page.locator('[data-testid^="webhook-row-"]');
    await expect(rows).toHaveCount(3);
    const firstRowID = (await rows.nth(0).getAttribute("data-testid")).replace("webhook-row-", "");
    const secondRowID = (await rows.nth(1).getAttribute("data-testid")).replace("webhook-row-", "");

    // Select two of the three.
    await page.getByTestId(`webhook-checkbox-${firstRowID}`).check();
    await page.getByTestId(`webhook-checkbox-${secondRowID}`).check();
    await page.getByTestId("btn-bulk-delete-webhooks").click();

    // Only the un-selected webhook should remain.
    await expect(rows).toHaveCount(1);
    await expect(page.getByTestId("webhook-bulk-result")).toContainText("2 of 2 succeeded");
  });

  test("select-all then Delete-selected sweeps every webhook", async ({ page }) => {
    page.on("dialog", (d) => d.accept());
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();

    for (const url of ["https://example.com/all-a", "https://example.com/all-b"]) {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("s");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.locator(`text=${url}`)).toBeVisible();
    }

    await page.getByTestId("webhook-select-all").check();
    await page.getByTestId("btn-bulk-delete-webhooks").click();

    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(0);
    await expect(page.getByText(/No webhooks registered/i)).toBeVisible();
  });

  test("Delete-selected button stays disabled when no webhook is selected", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await page.getByTestId("add-webhook-btn").click();
    await page.getByTestId("webhook-url-input").fill("https://example.com/single");
    await page.getByTestId("webhook-secret-input").fill("s");
    await page.getByTestId("webhook-create-submit").click();
    const row = page.locator('[data-testid^="webhook-row-"]').first();
    await expect(row).toBeVisible();

    // No checkbox ticked → button is disabled.
    await expect(page.getByTestId("btn-bulk-delete-webhooks")).toBeDisabled();
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

  // 5.4.13 — Free-text search filter mirrored across API + GUI.
  test("search input filters the log table and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // Mock-server seeds an entry whose message contains "vmSmith daemon listening" on source=daemon.
    await page.getByTestId("log-search").fill("listening");

    // After the 250 ms debounce + fetch, the daemon entry should be the only one visible.
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("search=listening");
    await expect(page.getByTestId("log-source-daemon")).toBeVisible();
    // The api request entry ("GET /api/v1/vms") should be filtered out.
    await expect(page.getByTestId("log-source-api")).toBeHidden();
  });

  test("search input matches against structured field values", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // Mock-server seeds an "api" source entry with fields.status_code=200.
    // Field values are in the haystack, but field keys are not.
    await page.getByTestId("log-search").fill("200");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("search=200");
    await expect(page.getByTestId("log-source-api")).toBeVisible();
  });

  test("search shows empty-state hint when no entries match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    await page.getByTestId("log-search").fill("zzzzz-no-such-needle");
    await expect(page.getByTestId("log-empty-state")).toContainText(
      'No log entries match "zzzzz-no-such-needle".',
    );
  });

  test("search clear button restores the unfiltered view", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    await page.getByTestId("log-search").fill("listening");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("search=listening");

    await page.getByTestId("log-search-clear").click();
    await expect.poll(() => page.url(), { timeout: 2000 }).not.toContain("search=");
    await expect(page.getByTestId("log-source-api")).toBeVisible();
    await expect(page.getByTestId("log-source-daemon")).toBeVisible();
  });

  // 5.4.17 — Sortable log list mirrored across API + CLI + GUI.
  test("sort dropdowns reorder entries and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // Default order is timestamp+asc — the daemon entry is seeded with the
    // earliest timestamp, so it should appear in the first row.
    const firstRowLevel = page.locator('[data-testid="log-table"] tbody tr').first();
    await expect(firstRowLevel).toContainText("daemon");

    // Sort by level + desc → the error entry should rise to the top.
    await page.getByTestId("log-sort-field").selectOption("level");
    await page.getByTestId("log-sort-order").selectOption("desc");

    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("sort=level");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("order=desc");

    // After the round-trip the error row is at position 0.
    const rowAfter = page.locator('[data-testid="log-table"] tbody tr').first();
    await expect(rowAfter).toContainText("error");
  });

  test("sort by source orders alphabetically", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    await page.getByTestId("log-sort-field").selectOption("source");
    await page.getByTestId("log-sort-order").selectOption("asc");

    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("sort=source");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("order=asc");

    // api < cli < daemon alphabetical, so first row should be source=api.
    const firstRow = page.locator('[data-testid="log-table"] tbody tr').first();
    await expect(firstRow).toContainText("api");
  });

  test("sort URL parameters hydrate the dropdowns on page load", async ({ page }) => {
    await page.goto(`${BASE_URL}/logs?sort=level&order=desc`);
    await expect(page.getByTestId("log-table")).toBeVisible();
    // The dropdown values should reflect the URL — proving the URL is the
    // source of truth (deep-linkable filtered view).
    await expect(page.getByTestId("log-sort-field")).toHaveValue("level");
    await expect(page.getByTestId("log-sort-order")).toHaveValue("desc");
    // And the error row is first.
    const firstRow = page.locator('[data-testid="log-table"] tbody tr').first();
    await expect(firstRow).toContainText("error");
  });

  // 5.4.18 — Per-VM log filter mirrored across API + CLI + GUI.
  test("vm_id filter scopes the log table to one VM and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // Mock-server seeds two entries with vm_id=vm-1 (api+error) and one with
    // vm_id=vm-2 (warn daemon). Filter to vm-1 should drop the vm-2 entry.
    await page.getByTestId("log-vm-id-filter").fill("vm-1");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("vm_id=vm-1");

    // vm-1 entries are sourced from api → both api rows still visible.
    await expect(page.getByTestId("log-source-api")).toBeVisible();
    // vm-2 entry (warn daemon) should be filtered out — there is no
    // "port forward restore skipped" row left in the table.
    await expect(page.locator('[data-testid="log-table"] tbody')).not.toContainText("port forward restore skipped");
  });

  test("vm_id filter exact-match prefix does not swallow longer ids", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // `vm-1` should NOT match `vm-12345` — though no `vm-12345` is seeded,
    // a `vm-` substring filter that the daemon happened to accept would
    // return both vm-1 and vm-2 entries. Confirm that the exact-match
    // filter rejects partial IDs by typing one that has no match at all.
    await page.getByTestId("log-vm-id-filter").fill("vm-no-such-id");
    await expect(page.getByTestId("log-empty-state")).toContainText(
      'No log entries for VM "vm-no-such-id".',
    );
  });

  test("vm_id filter clear button restores the unfiltered view", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    await page.getByTestId("log-vm-id-filter").fill("vm-1");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("vm_id=vm-1");

    await page.getByTestId("log-vm-id-filter-clear").click();
    await expect.poll(() => page.url(), { timeout: 2000 }).not.toContain("vm_id=");
    // After clearing, the daemon "vmSmith daemon listening" entry (no vm_id)
    // should reappear since the filter no longer hides it.
    await expect(page.locator('[data-testid="log-table"] tbody')).toContainText("vmSmith daemon listening");
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

  // 4.2.24 — per-resource_id exact-match filter.
  test("resource_id input filters the activity timeline and round-trips through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // All seeded events visible before typing.
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();

    // evt-2 carries resource_id="tpl-rocky9-base" — the input should narrow
    // to only that row after the 250 ms debounce.
    await page.getByTestId("activity-filter-resource-id").fill("tpl-rocky9-base");

    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-1")).toHaveCount(0);

    // The committed filter lands in the URL so the view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("resource_id")).toBe("tpl-rocky9-base");

    // Clearing via the in-input X drops the param and restores the list.
    await page.getByTestId("btn-activity-clear-resource-id").click();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("resource_id")).toBe(null);
  });

  test("resource_id filter matches no events when target is unknown", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    await page.getByTestId("activity-filter-resource-id").fill("snap-does-not-exist");
    // Empty-state copy after the debounced fetch returns zero rows.
    await expect(page.getByText("No events yet")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
  });

  test("resource_id filter narrows further when combined with the source dropdown", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // evt-2 has resource_id=tpl-rocky9-base AND source=app. Narrowing
    // source=libvirt should produce zero rows — the filters compose.
    await page.getByTestId("activity-filter-resource-id").fill("tpl-rocky9-base");
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await page.getByTestId("activity-filter-source").selectOption("libvirt");
    await expect(page.getByTestId("activity-row-evt-2")).toHaveCount(0);
    await expect(page.getByText("No events yet")).toBeVisible();
  });

  // 4.2.25 — type-prefix filter narrows event class without listing each subtype.
  test("type-prefix input filters the activity timeline and round-trips through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // All four seeded events visible before typing.
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-0")).toBeVisible();

    // Only evt-0 has type "vm_template_synced" — the other three are
    // vm_started/vm_created/vm_stopped and should not pass the prefix filter.
    await page.getByTestId("activity-filter-type-prefix").fill("vm_template");

    await expect(page.getByTestId("activity-row-evt-0")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-2")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-1")).toHaveCount(0);

    // Debounced commit lands in the URL so a filtered view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("type_prefix")).toBe("vm_template");

    // In-input X clears the filter and restores the full list.
    await page.getByTestId("btn-activity-clear-type-prefix").click();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("type_prefix")).toBe(null);
  });

  test("type-prefix filter is case-insensitive", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // Uppercase prefix still matches the lowercase-typed events because
    // the daemon's matcher lowercases both sides.
    await page.getByTestId("activity-filter-type-prefix").fill("VM_STOPPED");
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-0")).toHaveCount(0);
  });

  test("type-prefix filter matches no events when no type starts with the value", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    await page.getByTestId("activity-filter-type-prefix").fill("schedule.");
    // Empty-state copy renders once the debounced fetch returns zero rows.
    await expect(page.getByText("No events yet")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
  });


  // 4.2.22 — event details disclosure (actor + resource_id + attributes).
  test("expand reveals actor + resource_id + attributes for events that have them", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // evt-2 (vm_created) is seeded with actor="ops-alice" and
    // attributes.template="rocky9-base" — it should expose a chevron toggle.
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    const toggle = page.getByTestId("activity-row-toggle-evt-2");
    await expect(toggle).toBeVisible();
    // Details row hidden by default.
    await expect(page.getByTestId("activity-details-evt-2")).toHaveCount(0);
    // Click expands and shows actor + attributes.
    await toggle.click();
    await expect(page.getByTestId("activity-details-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-detail-actor-evt-2")).toContainText("ops-alice");
    await expect(page.getByTestId("activity-detail-attrs-evt-2")).toContainText("template");
    await expect(page.getByTestId("activity-detail-attrs-evt-2")).toContainText("rocky9-base");
    // Click again collapses it.
    await toggle.click();
    await expect(page.getByTestId("activity-details-evt-2")).toHaveCount(0);
  });

  test("events with no structured details do not render a disclosure toggle", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // evt-0 is seeded with no actor / attributes / resource_id, so the
    // hasDetails gate at web/src/pages/Activity.jsx should suppress the
    // chevron entirely for that row. Asserting toHaveCount(0) on the
    // toggle exercises the negative branch end-to-end.
    await expect(page.getByTestId("activity-row-evt-0")).toBeVisible();
    await expect(page.getByTestId("activity-row-toggle-evt-0")).toHaveCount(0);
    // Sanity-check the positive path on the same render: evt-3 has
    // actor="system" but no attributes / resource_id, and actor alone is
    // enough to render the toggle.
    const row3Toggle = page.getByTestId("activity-row-toggle-evt-3");
    await expect(row3Toggle).toBeVisible();
    await row3Toggle.click();
    await expect(page.getByTestId("activity-detail-actor-evt-3")).toContainText("system");
  });

  test("row click toggles the details row when details are available", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // Clicking the row body (not the toggle) should also expand it.
    await page.getByTestId("activity-row-evt-2").click();
    await expect(page.getByTestId("activity-details-evt-2")).toBeVisible();
  });

  // 4.2.23 — per-actor exact-match filter on the activity timeline.
  test("actor input filters the activity timeline and round-trips through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // Seeded events: evt-3 actor=system, evt-2 actor=ops-alice, evt-1 actor=system, evt-0 no actor.
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();

    // Type exact alias — debounce settles after 250 ms.
    await page.getByTestId("activity-filter-actor").fill("ops-alice");
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-1")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-0")).toHaveCount(0);

    // URL captures the committed actor so the filtered view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("actor")).toBe("ops-alice");
  });

  test("actor clear button restores the unfiltered view", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    await page.getByTestId("activity-filter-actor").fill("ops-alice");
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);

    await page.getByTestId("btn-activity-clear-actor").click();
    await expect(page.getByTestId("activity-row-evt-2")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("actor")).toBe(null);
  });

  test("actor filter narrows further when combined with the source dropdown", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // actor=system narrows to evt-3 + evt-1; source=app then strips evt-3
    // (libvirt) and evt-1 (libvirt) leaving zero events — empty state.
    await page.getByTestId("activity-filter-actor").fill("system");
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();

    await page.getByTestId("activity-filter-source").selectOption("app");
    await expect(page.getByText("No events yet")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
  });

  // 5.4.16 — sortable events list.
  test("sort dropdowns reorder the activity timeline and round-trip through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);

    // Default ordering puts evt-3 (most recent, newest sequence id) at the top.
    const rows = page.locator('[data-testid^="activity-row-"]');
    await expect(rows.first()).toHaveAttribute("data-testid", "activity-row-evt-3");

    // sort=type asc: case-insensitive — "vm_created" < "vm_started" < "vm_stopped".
    await page.getByTestId("activity-sort-field").selectOption("type");
    await page.getByTestId("activity-sort-order").selectOption("asc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-2");

    // URL captures the new sort + order.
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("type");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("asc");

    // sort=source desc — "libvirt" > "app" so evt-3 (libvirt) comes first.
    await page.getByTestId("activity-sort-field").selectOption("source");
    await page.getByTestId("activity-sort-order").selectOption("desc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-3");
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
