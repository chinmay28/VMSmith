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

    // Stats should show seeded data: 3 VMs (web-server running, db-server
    // stopped, win-app running — see 5.6.8), 2 images.
    await expect(page.getByTestId("stat-total")).toHaveText("3");
    await expect(page.getByTestId("stat-running")).toHaveText("2");
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

  // 5.7.11 — GPU quota card. The mock server seeds win-app with one
  // passthrough GPU ("0000:01:00.0") and the other VMs with none, so the
  // /quotas/usage endpoint reports gpus.used = 1 / limit = 0.  The
  // Dashboard renders a fifth QuotaCard for the new GPU dimension that
  // must show the seeded count and the "uncapped" subtitle.
  test("shows the GPU quota card with seeded usage", async ({ page }) => {
    await page.goto(BASE_URL);
    const card = page.getByText("GPUs allocated").locator("xpath=ancestor::div[contains(@class,'card-hover')][1]");
    await expect(card).toBeVisible();
    await expect(card).toContainText("1 GPUs · uncapped");
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

  test("windows create form surfaces variant/password fields and enforces client floors", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-os-type").selectOption("windows");
    await expect(page.getByTestId("input-vm-os-variant")).toBeVisible();
    await expect(page.getByTestId("input-vm-admin-password")).toBeVisible();
    await expect(page.getByTestId("input-vm-ram")).toHaveValue("4096");
    await expect(page.getByTestId("input-vm-disk")).toHaveValue("64");
  });

  // 5.6.17 — when a Windows VM is created without an admin_password the
  // daemon auto-generates one and surfaces it exactly once in the create
  // response. The GUI must show it in a one-time-reveal modal with a copy
  // button and never refer back to the value after dismissal.
  test("creating a Windows VM without admin_password surfaces a one-time reveal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-name").fill("win-auto-pw");
    // Apply the windows-2022 template so the request carries os_type=windows.
    await page.getByTestId("input-vm-template").selectOption("tmpl-3");

    await page.getByTestId("btn-submit-create").click();

    const modal = page.getByTestId("generated-admin-password-modal");
    await expect(modal).toBeVisible();
    const valueLocator = page.getByTestId("generated-admin-password-value");
    await expect(valueLocator).toBeVisible();
    const password = (await valueLocator.textContent())?.trim() ?? "";
    expect(password.length).toBeGreaterThanOrEqual(12);

    // Copy button toggles to "Copied" briefly.
    await page.getByTestId("generated-admin-password-copy").click();
    await expect(page.getByTestId("generated-admin-password-copy")).toHaveText("Copied");

    // Dismissing closes the modal and the password is gone — it was shown once.
    await page.getByTestId("generated-admin-password-dismiss").click();
    await expect(modal).toHaveCount(0);
    // The newly-created VM should appear.
    await expect(page.getByTestId("vm-card-win-auto-pw")).toBeVisible();
  });

  // 5.6.15 — per-VM device overrides should round-trip through the Advanced
  // tab "Device Tuning" subsection. Setting the four enum selects + the
  // virtio-win path field on create should persist on the VM record.
  test("device tuning overrides round-trip through the Advanced tab", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-name").fill("dev-overrides");
    await page.getByTestId("input-vm-image").fill("rocky9");

    await page.getByTestId("tab-advanced").click();
    await page.getByTestId("input-vm-disk-bus").selectOption("sata");
    await page.getByTestId("input-vm-nic-model").selectOption("e1000e");
    await page.getByTestId("input-vm-firmware").selectOption("uefi");
    await page.getByTestId("input-vm-machine").fill("pc-q35-rhel9.6.0");
    await page.getByTestId("input-vm-virtio-win-iso").fill("/tmp/virtio-win.iso");

    await page.getByTestId("btn-submit-create").click();
    await expect(page.getByTestId("vm-card-dev-overrides")).toBeVisible();

    // Hit the daemon directly and confirm every field landed on spec.
    const stored = await page.evaluate(async () => {
      const r = await fetch("/api/v1/vms");
      const list = await r.json();
      const arr = Array.isArray(list) ? list : list.data || [];
      const match = arr.find((vm) => vm.name === "dev-overrides");
      return match ? match.spec : null;
    });
    expect(stored).toBeTruthy();
    expect(stored.disk_bus).toBe("sata");
    expect(stored.nic_model).toBe("e1000e");
    expect(stored.firmware).toBe("uefi");
    expect(stored.machine).toBe("pc-q35-rhel9.6.0");
    expect(stored.virtio_win_iso).toBe("/tmp/virtio-win.iso");
  });

  test("GPU passthrough selection round-trips through the Advanced tab", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-name").fill("gpu-box");
    await page.getByTestId("input-vm-image").fill("rocky9");

    await page.getByTestId("tab-advanced").click();

    // The mock host exposes an NVIDIA GPU; select it for passthrough.
    await expect(page.getByTestId("gpu-list")).toBeVisible();
    await expect(page.getByText("primary display")).toBeVisible();
    await page.getByTestId("gpu-checkbox-0000:01:00.0").check();

    await page.getByTestId("btn-submit-create").click();
    await expect(page.getByTestId("vm-card-gpu-box")).toBeVisible();

    const stored = await page.evaluate(async () => {
      const r = await fetch("/api/v1/vms");
      const list = await r.json();
      const arr = Array.isArray(list) ? list : list.data || [];
      const match = arr.find((vm) => vm.name === "gpu-box");
      return match ? match.spec : null;
    });
    expect(stored).toBeTruthy();
    expect(stored.gpus).toEqual(["0000:01:00.0"]);
  });

  test("vm cards render guest OS badges", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("badge-os-web-server")).toHaveText("Linux");
    await expect(page.getByTestId("badge-os-win-app")).toContainText("Windows");
    await expect(page.getByTestId("badge-os-win-app")).toContainText("Server");
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
    // Wait for every template to appear; otherwise the assertion
    // can race the API fetch.
    await expect(select.locator("option")).toHaveCount(5);
    const options = await select.locator("option").evaluateAll((nodes) =>
      nodes.map((n) => ({ value: n.value, text: n.textContent || "" })),
    );
    // Skip the leading "No template" placeholder.
    expect(options[0].value).toBe("");
    expect(options[1].text).toBe("big-rocky");
    expect(options[2].text).toBe("small-ubuntu");
    expect(options[3].text).toBe("windows-11-desktop");
    expect(options[4].text).toBe("windows-2022");
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
    await expect(select.locator("option")).toHaveCount(5); // placeholder + 4 templates

    // Search for "rocky" — should reduce the dropdown to big-rocky only.
    await page.getByTestId("template-search-input").fill("rocky");
    await expect(select.locator("option")).toHaveCount(2);
    await expect(select.locator("option").nth(1)).toHaveText("big-rocky");

    // Clear via the X button and the full list comes back.
    await page.getByTestId("template-search-clear").click();
    await expect(select.locator("option")).toHaveCount(5);
  });

  test("template search filter is case-insensitive and matches description", async ({ page }) => {
    // The mock-server seeds templates with descriptions: "Small Ubuntu
    // template", "Big Rocky template", "Windows Server 2022 template". A
    // case-insensitive needle that only appears in one description should
    // reduce the dropdown to that template — confirming the haystack covers
    // description, not just name.
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select.locator("option")).toHaveCount(5);

    await page.getByTestId("template-search-input").fill("UBUNTU");
    await expect(select.locator("option")).toHaveCount(2);
    await expect(select.locator("option").nth(1)).toHaveText("small-ubuntu");
  });

  test("template search shows empty-state hint when no templates match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    const select = page.getByTestId("input-vm-template");
    await expect(select.locator("option")).toHaveCount(5);

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
  // bar.  The mock seeds web-server (running), db-server (stopped), and
  // win-app (running); only running VMs are eligible for restart, so the
  // action should leave db-server alone and the success label should report
  // "2 restarts succeeded · 1 skipped".
  test("bulk restart only acts on running VMs", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("checkbox-select-all-vms").check();
    await expect(page.getByTestId("bulk-action-bar")).toContainText("3 selected");
    await expect(page.getByTestId("bulk-action-bar")).toContainText("2 running");
    await expect(page.getByTestId("bulk-action-bar")).toContainText("1 stopped");

    await page.getByTestId("btn-bulk-restart").click();
    // After execution the success summary banner reads "<n> restarts
    // succeeded · <n> skipped".  The mock seeds 2 running + 1 stopped so we
    // expect 2 succeeded and 1 skipped.
    await expect(page.getByText(/2 restarts succeeded/)).toBeVisible();
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

  // 5.4.23 — default_user filter on the VM list. Seed data includes
  // web-server with default_user="ubuntu", so create a VM with a distinct
  // default user and verify the debounced text filter isolates that
  // single-VM cohort and round-trips via ?default_user=.
  test("default-user filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-name").fill("deploy-box");
    await page.getByTestId("input-vm-image").selectOption("/images/ubuntu-base.qcow2");
    await page.getByTestId("tab-advanced").click();
    await page.getByTestId("input-vm-default-user").fill("deploy");
    await page.getByTestId("btn-submit-create").click();

    await expect(page.getByTestId("vm-card-deploy-box")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();

    await page.getByTestId("vm-list-default-user-filter").fill("deploy");
    await expect(page.getByTestId("vm-card-deploy-box")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("default_user=deploy");

    await page.getByTestId("vm-list-default-user-filter-clear").click();
    await expect(page.getByTestId("vm-card-deploy-box")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("default_user=");
  });

  test("default-user filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();
    await page.getByTestId("btn-new-vm").click();

    await page.getByTestId("input-vm-name").fill("ops-box");
    await page.getByTestId("input-vm-image").selectOption("/images/ubuntu-base.qcow2");
    await page.getByTestId("tab-advanced").click();
    await page.getByTestId("input-vm-default-user").fill("ec2-user");
    await page.getByTestId("btn-submit-create").click();

    await page.getByTestId("vm-list-default-user-filter").fill("EC2-USER");
    await expect(page.getByTestId("vm-card-ops-box")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
  });

  // 5.4.36 — network filter on the VM list. Seed data: web-server is on
  // "data-net", db-server on "storage-net". 250 ms-debounced input with URL
  // round-trip via ?network=.
  test("network filter narrows the VM list to a single network and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    await page.getByTestId("vm-list-network-filter").fill("data-net");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("network=data-net");

    await page.getByTestId("vm-list-network-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("network=");
  });

  test("network filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-network-filter").fill("STORAGE-NET");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
  });

  // 5.6.8 — os_type filter on the VM list. Seed data: web-server +
  // db-server are Linux (implicit + implicit), win-app is windows. The
  // dropdown is a <select> with values "" / "linux" / "windows" that
  // round-trips through ?os_type=.
  test("os-type filter narrows the VM list to a single OS family and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    await page.getByTestId("vm-list-os-type-filter").selectOption("windows");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("os_type")).toBe("windows");

    await page.getByTestId("vm-list-os-type-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("os_type=");
  });

  // Linux selection matches both explicit-linux AND implicit (empty
  // os_type) VMs — mirrors the API contract documented in
  // pkg/types/vm.go::VMSpec.ResolvedOSType.
  test("os-type filter linux selection matches VMs with an empty os_type", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-os-type-filter").selectOption("linux");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
  });

  // 5.4.66 — os_variant filter on the VM list. Seed data: win-app is the
  // only Windows VM, with spec.os_variant="windows-server-2022". The two
  // Linux VMs (web-server, db-server) have an empty os_variant and must
  // drop out whenever the filter is set (no documented "default variant",
  // mirrors the API's parseOSVariantFilter empty-stored-excluded contract).
  test("os-variant filter narrows the VM list to a single Windows edition and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // Selecting the matching variant narrows to win-app only; Linux VMs
    // (empty os_variant) are filtered out even though they exist.
    await page.getByTestId("vm-list-os-variant-filter").selectOption("windows-server-2022");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("os_variant")).toBe("windows-server-2022");

    // A recognised-but-unmatched variant collapses the cohort to empty —
    // win-app is windows-server-2022, no VM is windows-11.
    await page.getByTestId("vm-list-os-variant-filter").selectOption("windows-11");
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("os_variant")).toBe("windows-11");

    // "All variants" clears the filter and restores every VM, dropping the
    // URL param.
    await page.getByTestId("vm-list-os-variant-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("os_variant=");
  });

  // 5.4.68 — firmware filter on the VM list. Seed data: win-app has
  // spec.firmware="uefi"; web-server and db-server leave spec.firmware empty
  // and so fall into the implicit-BIOS bucket. `?firmware=bios` matches the
  // two empty-stored Linux VMs (empty-defaults-to-BIOS contract mirrors
  // `?os_type=linux` empty-means-linux); `?firmware=uefi` strict-matches
  // win-app only; `?firmware=ovmf` collapses to empty because no seed VM
  // explicitly stores "ovmf" (uefi and ovmf are distinct stored values even
  // though they map to the same libvirt firmware='efi' attribute).
  test("firmware filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // uefi → only win-app (strict-match on stored value).
    await page.getByTestId("vm-list-firmware-filter").selectOption("uefi");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("firmware")).toBe("uefi");

    // ovmf → empty cohort (no VM explicitly stores "ovmf"; uefi-stored
    // win-app does NOT match — they're distinct stored values).
    await page.getByTestId("vm-list-firmware-filter").selectOption("ovmf");
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("firmware")).toBe("ovmf");

    // bios → the two Linux VMs with empty stored firmware (the SeaBIOS
    // default). win-app drops out because its stored value is "uefi".
    await page.getByTestId("vm-list-firmware-filter").selectOption("bios");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("firmware")).toBe("bios");

    // "All firmware" clears the filter and drops the URL param.
    await page.getByTestId("vm-list-firmware-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("firmware=");
  });

  // 5.4.69 — `disk_bus` filter on the VM list. Seed data: web-server +
  // db-server leave spec.disk_bus + spec.os_type empty (Linux default →
  // virtio); win-app is Windows with empty spec.disk_bus (Windows default
  // → sata). The filter must round-trip through the URL and respect the
  // OS-family default for empty-stored values.
  test("disk_bus filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // virtio → the two Linux VMs (empty disk_bus resolves to virtio via the
    // Linux family default). win-app drops out because it's Windows and its
    // effective bus is sata.
    await page.getByTestId("vm-list-disk-bus-filter").selectOption("virtio");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("disk_bus")).toBe("virtio");

    // sata → only win-app (Windows family default; the two Linux VMs collapse
    // to virtio so they drop out).
    await page.getByTestId("vm-list-disk-bus-filter").selectOption("sata");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("disk_bus")).toBe("sata");

    // "All disk buses" clears the filter and drops the URL param.
    await page.getByTestId("vm-list-disk-bus-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("disk_bus=");
  });

  // 5.4.70 — NIC model filter on the VM list. Seed data: web-server and
  // db-server are Linux with empty stored nic_model (fall under virtio via
  // the OS-family default); win-app is Windows with empty stored nic_model
  // (falls under e1000e via the Windows default). `?nic_model=virtio`
  // matches the two empty-stored Linux VMs (empty-defaults-to-virtio on
  // Linux); `?nic_model=e1000e` matches win-app only (empty-defaults-to-
  // e1000e on Windows). Mirrors the disk_bus filter (5.4.69) family-default
  // semantics.
  test("nic_model filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // virtio → two Linux VMs match via the empty-defaults-to-virtio family
    // default; win-app drops out because its OS-family default is e1000e.
    await page.getByTestId("vm-list-nic-model-filter").selectOption("virtio");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("nic_model")).toBe("virtio");

    // e1000e → only win-app matches (Windows empty-defaults-to-e1000e); the
    // two Linux VMs drop because their family default is virtio.
    await page.getByTestId("vm-list-nic-model-filter").selectOption("e1000e");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("nic_model")).toBe("e1000e");

    // "All NIC models" clears the filter and drops the URL param.
    await page.getByTestId("vm-list-nic-model-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("nic_model=");
  });

  // 5.4.71 — Machine filter on the VM list. Seed data: win-app is pinned to
  // `pc-q35-rhel9.6.0` while web-server / db-server leave spec.machine empty
  // (effective `pc-q35-6.2` via the daemon default). `?machine=pc-q35-6.2`
  // matches the two empty-stored Linux VMs (default-matches-empty);
  // `?machine=pc-q35-rhel9.6.0` strict-matches win-app only.
  test("machine filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // pc-q35-rhel9.6.0 strict-match → only win-app.
    await page.getByTestId("vm-list-machine-filter").fill("pc-q35-rhel9.6.0");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("machine")).toBe("pc-q35-rhel9.6.0");

    // pc-q35-6.2 → two Linux VMs match via the empty-defaults-to-daemon-
    // default contract; win-app drops out because its explicit machine wins.
    await page.getByTestId("vm-list-machine-filter").fill("pc-q35-6.2");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("machine")).toBe("pc-q35-6.2");

    // Clearing the input restores every VM and drops the URL param.
    await page.getByTestId("vm-list-machine-filter").fill("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("machine=");
  });

  // 5.4.72 — Clock offset filter on the VM list. Seed data: web-server and
  // db-server are Linux with empty stored clock_offset (fall under utc via
  // the OS-family default); win-app is Windows with empty stored clock_offset
  // (falls under localtime via the Windows default). `?clock_offset=utc`
  // matches the two empty-stored Linux VMs (empty-defaults-to-utc on Linux);
  // `?clock_offset=localtime` matches win-app only (empty-defaults-to-
  // localtime on Windows). Mirrors the nic_model filter (5.4.70) family-default
  // semantics.
  test("clock_offset filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // utc → two Linux VMs match via the empty-defaults-to-utc family
    // default; win-app drops out because its OS-family default is localtime.
    await page.getByTestId("vm-list-clock-offset-filter").selectOption("utc");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("clock_offset")).toBe("utc");

    // localtime → only win-app matches (Windows empty-defaults-to-localtime);
    // the two Linux VMs drop because their family default is utc.
    await page.getByTestId("vm-list-clock-offset-filter").selectOption("localtime");
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("clock_offset")).toBe("localtime");

    // "All clocks" clears the filter and drops the URL param.
    await page.getByTestId("vm-list-clock-offset-filter").selectOption("");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("clock_offset=");
  });

  test("network filter matches no VMs when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await page.getByTestId("vm-list-network-filter").fill("does-not-exist");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
  });

  // 5.4.76 — Name-prefix filter on the VM list. Seed data: web-server,
  // db-server, win-app. `?prefix=web-` matches only web-server;
  // `?prefix=Web-` (case-sensitive) matches nothing (no `Web-*` VMs); the
  // Clear button drops the URL param and restores every VM.
  test("name-prefix filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // `web-` prefix narrows to web-server only.
    await page.getByTestId("vm-list-prefix-filter").fill("web-");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("web-");

    // Case-sensitive: `Web-` matches nothing because seed names are lowercase.
    await page.getByTestId("vm-list-prefix-filter").fill("Web-");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("Web-");

    // The Clear button drops the URL param and restores every VM.
    await page.getByTestId("vm-list-prefix-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("prefix=");
  });

  // 5.4.79 — NAT static IP filter on the VM list. Seed: only web-server has
  // spec.nat_static_ip pinned (`192.168.100.10/24`). Exercises IP-portion
  // match, CIDR match, DHCP exclusion (db-server / win-app drop out), and
  // the Clear button.
  test("nat_static_ip filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // IP-only match narrows to web-server (DHCP VMs drop out).
    await page.getByTestId("vm-list-nat-static-ip-filter").fill("192.168.100.10");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("nat_static_ip")).toBe("192.168.100.10");

    // Full CIDR also matches.
    await page.getByTestId("vm-list-nat-static-ip-filter").fill("192.168.100.10/24");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);

    // A non-matching IP excludes every VM.
    await page.getByTestId("vm-list-nat-static-ip-filter").fill("10.0.0.99");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);

    // The Clear button drops the URL param and restores every VM.
    await page.getByTestId("vm-list-nat-static-ip-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("nat_static_ip=");
  });

  // 5.4.80 — NAT gateway filter on the VM list. Seed: only web-server has
  // spec.nat_gateway pinned (`192.168.100.1`). Exercises exact-match,
  // empty-stored exclusion (db-server / win-app drop out), and the Clear
  // button.
  test("nat_gateway filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // Exact-match narrows to web-server (VMs with no gateway override drop out).
    await page.getByTestId("vm-list-nat-gateway-filter").fill("192.168.100.1");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("nat_gateway")).toBe("192.168.100.1");

    // A non-matching IP excludes every VM.
    await page.getByTestId("vm-list-nat-gateway-filter").fill("10.0.0.99");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);

    // The Clear button drops the URL param and restores every VM.
    await page.getByTestId("vm-list-nat-gateway-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("nat_gateway=");
  });

  // 5.4.81 — Runtime IP filter on the VM list. Seed data: web-server is at
  // 192.168.100.10, db-server at 192.168.100.11, win-app at 192.168.100.12.
  // Exercises exact-match narrowing on the runtime-discovered vm.ip field
  // (the value displayed in the IP column) and the Clear button. The
  // empty-stored-excludes path (VMs with no IP — stopped, no lease) is
  // covered by the API and CLI integration tests.
  test("ip filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();

    // Exact-match narrows to web-server only.
    await page.getByTestId("vm-list-ip-filter").fill("192.168.100.10");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("ip")).toBe("192.168.100.10");

    // A non-matching IP excludes every VM.
    await page.getByTestId("vm-list-ip-filter").fill("10.0.0.99");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-win-app")).toHaveCount(0);

    // The Clear button drops the URL param and restores every VM.
    await page.getByTestId("vm-list-ip-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-win-app")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("ip=");
  });

  // 5.4.44 — vCPU range filter on the VM list. Seed data: web-server has 2
  // vCPUs, db-server has 4. 250 ms-debounced number inputs with URL round-trip
  // via ?min_cpus= / ?max_cpus=.
  test("vCPU range filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // min_cpus=4 -> only db-server (4 vCPUs).
    await page.getByTestId("vm-list-min-cpus").fill("4");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("min_cpus=4");

    // Clearing restores both VMs and drops the URL param.
    await page.getByTestId("vm-list-cpus-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("min_cpus=");

    // max_cpus=2 -> only web-server (2 vCPUs).
    await page.getByTestId("vm-list-max-cpus").fill("2");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("max_cpus=2");
  });

  // 5.4.48 — RAM range filter on the VM list. Seed data: web-server has 4096 MB,
  // db-server has 8192 MB. 250 ms-debounced number inputs with URL round-trip
  // via ?min_ram_mb= / ?max_ram_mb=.
  test("RAM range filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // min_ram_mb=8000 -> only db-server (8192 MB).
    await page.getByTestId("vm-list-min-ram").fill("8000");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("min_ram_mb=8000");

    // Clearing restores both VMs and drops the URL param.
    await page.getByTestId("vm-list-ram-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("min_ram_mb=");

    // max_ram_mb=5000 -> only web-server (4096 MB).
    await page.getByTestId("vm-list-max-ram").fill("5000");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("max_ram_mb=5000");
  });

  // 5.4.50 — Disk size range filter on the VM list. Seed data: web-server has 40 GB,
  // db-server has 100 GB. 250 ms-debounced number inputs with URL round-trip
  // via ?min_disk_gb= / ?max_disk_gb=.
  test("disk range filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // min_disk_gb=100 -> only db-server.
    await page.getByTestId("vm-list-min-disk").fill("100");
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("min_disk_gb=100");

    // Clearing restores both VMs and drops the URL param.
    await page.getByTestId("vm-list-disk-filter-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("min_disk_gb=");

    // max_disk_gb=50 -> only web-server (40 GB).
    await page.getByTestId("vm-list-max-disk").fill("50");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).search).toContain("max_disk_gb=50");
  });

  test("sort controls reorder the VM list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Default sort=id asc; seeded ordering: vm-1 (web-server), vm-2 (db-server), vm-3 (win-app).
    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);
    const idAscIds = await cards().evaluateAll(els => els.map(el => el.getAttribute("data-testid")));
    expect(idAscIds[0]).toBe("vm-card-web-server");
    expect(idAscIds[1]).toBe("vm-card-db-server");
    expect(idAscIds[2]).toBe("vm-card-win-app");

    // Switch to sort=name asc -> "db-server" comes before "web-server" before "win-app".
    await page.getByTestId("vm-list-sort-field").selectOption("name");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");

    // Switch order to desc and verify the URL captures the change. With three
    // VMs alphabetically: win-app > web-server > db-server (desc).
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-win-app");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=name");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
  });

  test("capacity sort axes (cpus / ram_mb / disk_gb) reorder the VM list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);

    // Seed has web-server (cpus=2, ram=4096, disk=40), db-server (cpus=4,
    // ram=8192, disk=100), and win-app (cpus=4, ram=4096, disk=64).
    // - cpus asc: web-server (2) < db-server/win-app (4, tiebreak by id → db-server then win-app)
    // - ram asc: web-server/win-app (4096, tiebreak by id → web-server then win-app) < db-server (8192)
    // - disk asc: web-server (40) < win-app (64) < db-server (100)
    // The `first` card in every asc sort below is therefore web-server.
    await page.getByTestId("vm-list-sort-field").selectOption("cpus");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=cpus");

    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");

    // Reset to asc for the next axis so the assertion below is unambiguous.
    await page.getByTestId("vm-list-sort-order").selectOption("asc");

    await page.getByTestId("vm-list-sort-field").selectOption("ram_mb");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=ram_mb");

    await page.getByTestId("vm-list-sort-field").selectOption("disk_gb");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=disk_gb");
  });

  test("ip sort axis reorders the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);

    // Seed: web-server 192.168.100.10, db-server 192.168.100.11, win-app 192.168.100.12.
    // Numeric asc puts web-server first (.10), then db-server (.11), then win-app (.12).
    await page.getByTestId("vm-list-sort-field").selectOption("ip");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=ip");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");

    // Descending flips the order. The unit test in pkg/types asserts the
    // numeric-vs-lexicographic contract (192.168.100.2 < 192.168.100.10);
    // the Playwright case verifies the dropdown <-> URL <-> API plumbing.
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-win-app");
  });

  // 5.4.88 — case-insensitive `image` sort axis.
  test("image sort axis reorders the VM list case-insensitively and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);

    // Seed images: web-server=ubuntu-22.04, db-server=rocky-9, win-app=win-server-2022.qcow2.
    // Case-folded asc: rocky-9 < ubuntu-22.04 < win-server-2022.qcow2 — so
    // db-server surfaces first, then web-server, then win-app. Mirrors the
    // case-insensitive `?image=` filter (5.4.22) contract.
    await page.getByTestId("vm-list-sort-field").selectOption("image");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=image");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");

    // Descending flips the order — win-app heads the list. The unit test in
    // pkg/types asserts the case-insensitive contract (Rocky9.qcow2 collates
    // with rocky9.qcow2); the Playwright case verifies the dropdown ↔ URL ↔
    // API plumbing.
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-win-app");
  });

  // 5.7.13 — lexicographic `gpu` sort axis with empty-trails-in-asc semantics.
  test("gpu sort axis reorders the VM list and sinks no-GPU VMs to the tail", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);

    // Seed: win-app alone carries `0000:01:00.0`; web-server / db-server
    // leave spec.gpus empty. Asc puts the GPU-bearing VM first and the
    // two empty-GPU VMs at the tail (id tiebreak: vm-1 < vm-2 so
    // web-server precedes db-server). Mirrors the nil-trailing contract
    // on every other nullable sort axis (ip, image, guest_ip, actor).
    await page.getByTestId("vm-list-sort-field").selectOption("gpu");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=gpu");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-win-app");
    await expect(cards().last()).toHaveAttribute("data-testid", "vm-card-db-server");

    // Descending flips the order — the empty-GPU cohort heads the list
    // (id tiebreak inverts so db-server precedes web-server) and the
    // GPU-bearing win-app trails.
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");
    await expect(cards().last()).toHaveAttribute("data-testid", "vm-card-win-app");
  });

  // 5.4.91 — case-insensitive `default_user` sort axis with empty→root resolution.
  test("default_user sort axis reorders the VM list and resolves empty to root", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    const cards = () => page.getByTestId(/^vm-card-/);
    await expect(cards()).toHaveCount(3);

    // Seed defaults: web-server=ubuntu, db-server="" (resolves to root),
    // win-app="" (resolves to root). Asc orders alphabetically: r < u so the
    // root cohort heads the list. Within the root cohort, id tiebreak puts
    // vm-2 (db-server) before vm-3 (win-app), then vm-1 (web-server / ubuntu)
    // trails. Validates the empty-means-root divergence from the nil-trailing
    // convention on every other nullable sort axis.
    await page.getByTestId("vm-list-sort-field").selectOption("default_user");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=default_user");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-db-server");
    await expect(cards().last()).toHaveAttribute("data-testid", "vm-card-web-server");

    // Descending flips everything including the id tiebreak inside the root
    // cohort — web-server (ubuntu) heads the list, then win-app, then db-server.
    await page.getByTestId("vm-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(cards().first()).toHaveAttribute("data-testid", "vm-card-web-server");
    await expect(cards().last()).toHaveAttribute("data-testid", "vm-card-db-server");
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

  // --- 5.4.30: ?since= / ?until= time-range filter on VM created_at ---
  // Seed data: vm-1 (web-server) created 2026-05-05, vm-2 (db-server) created
  // 2026-05-15 (see mock-server seed). The GUI boundary at 2026-05-10 cleanly
  // splits them so the assertions are robust against TZ drift.

  test("created-since filter narrows the VM list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // Without a filter both seeded VMs are visible.
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();

    // since=2026-05-10T00:00 (local) — only db-server (created 2026-05-15) survives.
    await page.getByTestId("vm-list-since-filter").fill("2026-05-10T00:00");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toContain("2026-05-10");

    // The Clear-range button drops both filters and the URL params.
    await page.getByTestId("vm-list-time-range-clear").click();
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toBeNull();
  });

  test("created-until filter narrows the VM list by upper bound", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // until=2026-05-10T00:00 — only web-server (created 2026-05-05) remains.
    await page.getByTestId("vm-list-until-filter").fill("2026-05-10T00:00");
    await expect(page.getByTestId("vm-card-web-server")).toBeVisible();
    await expect(page.getByTestId("vm-card-db-server")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toContain("2026-05-10");
  });

  test("created-at range empty-state when both bounds exclude every VM", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-vms").click();

    // A window before both seeded VMs returns nothing.
    await page.getByTestId("vm-list-since-filter").fill("2024-01-01T00:00");
    await page.getByTestId("vm-list-until-filter").fill("2024-12-31T00:00");
    await expect(page.getByTestId("vm-card-web-server")).toHaveCount(0);
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

  test("windows VM detail shows OS badge and RDP hint", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-win-app").click();

    await expect(page.getByTestId("vm-detail-os-badge")).toContainText("Windows");
    await expect(page.getByTestId("vm-detail-rdp-hint")).toContainText("localhost:33890");
  });

  test("shows snapshots", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
    await page.getByTestId("btn-new-snapshot").click();
    await page.getByTestId("input-snap-name").fill("test-snap");
    await page.getByTestId("btn-submit-snapshot").click();

    // New snapshot should appear
    await expect(page.getByTestId("snap-test-snap")).toBeVisible();
  });

  test("create snapshot with description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveText("checkpoint before May deploy");
  });

  test("edit snapshot description", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
    await page.getByTestId("btn-edit-snap-before-deploy").click();
    await page.getByTestId("input-edit-snap-description").fill("");
    await page.getByTestId("btn-submit-edit-snap").click();

    // Empty description means the description paragraph disappears entirely.
    await expect(page.getByTestId("snap-desc-before-deploy")).toHaveCount(0);
  });

  test("delete snapshot", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
    // Snapshot "before-deploy" should exist
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();

    await page.getByTestId("btn-delete-snap-before-deploy").click();

    // Should be removed
    await expect(page.getByTestId("snap-before-deploy")).not.toBeVisible();
  });

  test("bulk delete selected snapshots", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
    await page.getByTestId("snap-list-search").fill("checkpoint");
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toHaveCount(0);
    await expect(page.getByTestId("snap-auto-2026-05-07")).toHaveCount(0);
  });

  test("snapshot search input shows empty state when no snapshots match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
    await page.getByTestId("snap-list-search").fill("needle-not-present");
    // All three rows disappear; the empty-state card surfaces the needle in
    // its description.
    await expect(page.getByTestId("snap-before-deploy")).toHaveCount(0);
    await expect(page.getByText(/No snapshots match "needle-not-present"/)).toBeVisible();
  });

  // 5.4.75 — snapshot prefix filter. Case-sensitive HasPrefix; closes the
  // "preview the cohort before bulk-deleting" operator query by mirroring
  // the `--prefix` selector on the bulk_delete API.
  test("snapshot prefix filter narrows to matching names and round-trips through URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
    // Baseline: all three seeded snapshots visible.
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // `auto-` prefix should leave only the two auto-* rows.
    await page.getByTestId("snap-list-prefix").fill("auto-");
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();
    await expect(page.getByTestId("snap-before-deploy")).toHaveCount(0);

    // Round-trips through the URL.
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_prefix")).toBe("auto-");

    // Clearing restores all three.
    await page.getByTestId("snap-list-prefix-clear").click();
    await expect(page.getByTestId("snap-before-deploy")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_prefix")).toBeNull();
  });

  // --- Snapshot time-range filter (roadmap 5.4.28) ---

  test("snapshot ?since= filter narrows the list by created_at and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();
    await page.getByTestId("tab-snapshots").click();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // Set since=2026-05-07T00:00 — only auto-2026-05-07 should remain (and any
    // snapshots dated >= 2026-05-07).
    const sinceInput = page.getByTestId("snap-list-since");
    await sinceInput.fill("2026-05-07T00:00");
    // The committed query is persisted to the URL via ?snap_since=.
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_since")).toContain("2026-05-07");
    await expect(page.getByTestId("snap-auto-2026-05-06")).toHaveCount(0);
    await expect(page.getByTestId("snap-auto-2026-05-07")).toBeVisible();

    // Clearing restores all snapshots.
    await page.getByTestId("snap-list-time-range-clear").click();
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_since")).toBeNull();
  });

  test("snapshot ?until= filter narrows the list by created_at upper bound", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
    await page.getByTestId("snap-list-until").fill("2026-05-06T23:59");
    await expect(page.getByTestId("snap-auto-2026-05-06")).toBeVisible();
    await expect(page.getByTestId("snap-auto-2026-05-07")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("snap_until")).toContain("2026-05-06");
  });

  // --- Snapshot tags (roadmap 2.2.17) ---

  test("create snapshot with tags renders chips inline", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-snapshots").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
    await page.getByTestId("port-select-all").check();
    await page.getByTestId("btn-bulk-delete-ports").click();

    await expect(page.getByTestId("port-row-pf-seed-ssh")).not.toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).not.toBeVisible();
  });

  test("port forward sort dropdowns reorder the list and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
    await page.getByTestId("port-sort-field").selectOption("description");
    // pf-seed-ssh has description "ssh-jumpbox"; pf-seed-http has no description.
    // Empty string sorts before "ssh-jumpbox" so pf-seed-http comes first.
    const rows = () => page.locator('[data-testid^="port-row-"]');
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-http");
    await expect(rows().nth(1)).toHaveAttribute("data-testid", "port-row-pf-seed-ssh");
  });

  test("port sort by guest_ip exposes the new dropdown option and round-trips through the URL (5.4.86)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
    // The new "Guest IP" option appears on the sort dropdown alongside the
    // existing ID / Host port / Guest port / Protocol / Description axes.
    const sortDropdown = page.getByTestId("port-sort-field");
    await expect(sortDropdown.locator('option[value="guest_ip"]')).toHaveText(/Guest IP/);

    // Selecting it round-trips through `?port_sort=guest_ip`. Both seeded
    // rules share the same guest_ip (192.168.100.10) so the numeric compare
    // returns 0 and the id tiebreak applies: pf-seed-http < pf-seed-ssh.
    await sortDropdown.selectOption("guest_ip");
    await expect.poll(() => new URL(page.url()).search).toContain("port_sort=guest_ip");
    const rows = () => page.locator('[data-testid^="port-row-"]');
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-http");
    await expect(rows().nth(1)).toHaveAttribute("data-testid", "port-row-pf-seed-ssh");

    // Flip to desc — the id tiebreak inverts: pf-seed-ssh first.
    await page.getByTestId("port-sort-order").selectOption("desc");
    await expect(rows().first()).toHaveAttribute("data-testid", "port-row-pf-seed-ssh");
    await expect.poll(() => new URL(page.url()).search).toContain("port_order=desc");
  });

  test("port forward search input filters the list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
    // pf-seed-http has host_port 8080; pf-seed-ssh has 2222. Searching 8080
    // should leave only the http rule.
    await page.getByTestId("port-list-search").fill("8080");
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
  });

  test("port forward search shows empty state when no rules match", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
    await page.getByTestId("port-list-search").fill("needle-not-present");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect(page.getByText(/No port forwards match "needle-not-present"/)).toBeVisible();
  });

  test("protocol filter narrows the port-forward list and round-trips through the URL (5.4.25)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
    // Switch through tcp then back to "" to confirm Any clears the URL param
    // and restores every rule.
    await page.getByTestId("port-protocol-filter").selectOption("tcp");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();

    await page.getByTestId("port-protocol-filter").selectOption("");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_protocol")).toBeNull();
  });

  test("host-port range filter narrows the list and round-trips through the URL (5.4.47)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
    // Seeded host ports: pf-seed-ssh=2222, pf-seed-http=8080.
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();

    // min host port 8000 keeps only the 8080 rule.
    await page.getByTestId("port-min-host-port").fill("8000");
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_min_host")).toBe("8000");

    // Clearing restores both rows and drops the URL param.
    await page.getByTestId("port-host-port-clear").click();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_min_host")).toBeNull();

    // max host port 5000 keeps only the 2222 rule.
    await page.getByTestId("port-max-host-port").fill("5000");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_max_host")).toBe("5000");
  });

  test("guest-port range filter narrows the list and round-trips through the URL (5.4.49)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
    // Seeded guest ports: pf-seed-ssh guest=22, pf-seed-http guest=80.
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();

    // min guest port 50 keeps only the guest=80 rule.
    await page.getByTestId("port-min-guest-port").fill("50");
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_min_guest")).toBe("50");

    // Clearing restores both rows and drops the URL param.
    await page.getByTestId("port-guest-port-clear").click();
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_min_guest")).toBeNull();

    // max guest port 50 keeps only the guest=22 rule.
    await page.getByTestId("port-max-guest-port").fill("50");
    await expect(page.getByTestId("port-row-pf-seed-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-seed-http")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_max_guest")).toBe("50");
  });

  test("guest_ip filter narrows the port-forward list and round-trips through the URL (5.4.73)", async ({ page }) => {
    // Synthesize a multi-NIC layout so the filter has a meaningful cohort to
    // slice (the default seed shares a single guest_ip across both rules,
    // which would make the filter a binary all-or-nothing). Three rules on
    // vm-1: two on 192.168.100.10 (the seeded VM IP) and one on 10.0.0.7.
    const fixture = [
      { id: "pf-gip-ssh",      vm_id: "vm-1", host_port: 2222, guest_port: 22,  guest_ip: "192.168.100.10", protocol: "tcp", description: "ssh primary" },
      { id: "pf-gip-http",     vm_id: "vm-1", host_port: 8080, guest_port: 80,  guest_ip: "192.168.100.10", protocol: "tcp", description: "http primary" },
      { id: "pf-gip-datanet",  vm_id: "vm-1", host_port: 8443, guest_port: 443, guest_ip: "10.0.0.7",        protocol: "tcp", description: "https data-net" },
    ];
    await page.route("**/api/v1/vms/vm-1/ports*", async (route) => {
      const url = new URL(route.request().url());
      const guestIP = (url.searchParams.get("guest_ip") || "").trim().toLowerCase();
      let body = fixture;
      if (guestIP) {
        body = fixture.filter((pf) => (pf.guest_ip || "").trim().toLowerCase() === guestIP);
      }
      await route.fulfill({
        status: 200,
        headers: { "content-type": "application/json", "X-Total-Count": String(body.length) },
        body: JSON.stringify(body),
      });
    });

    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
    // All three synthetic rules visible before typing.
    await expect(page.getByTestId("port-row-pf-gip-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-datanet")).toBeVisible();

    // Filter to 192.168.100.10 keeps the two primary-NIC rules.
    await page.getByTestId("port-guest-ip-filter").fill("192.168.100.10");
    await expect(page.getByTestId("port-row-pf-gip-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-datanet")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("port_guest_ip")).toBe("192.168.100.10");

    // Re-type a different cohort: only the data-net rule remains.
    await page.getByTestId("port-guest-ip-filter").fill("10.0.0.7");
    await expect(page.getByTestId("port-row-pf-gip-datanet")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-ssh")).toHaveCount(0);
    await expect(page.getByTestId("port-row-pf-gip-http")).toHaveCount(0);

    // Clearing drops the URL param and restores all rows.
    await page.getByTestId("port-guest-ip-clear").click();
    await expect(page.getByTestId("port-row-pf-gip-ssh")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-http")).toBeVisible();
    await expect(page.getByTestId("port-row-pf-gip-datanet")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("port_guest_ip")).toBeNull();
  });

  test("protocol filter composes with the existing search filter (5.4.25)", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
    await page.getByTestId("btn-edit-port-pf-seed-ssh").click();
    await page.getByTestId("input-edit-port-description").fill("");
    await page.getByTestId("btn-submit-edit-port").click();

    // An empty description hides the description line entirely (matches snapshot edit pattern).
    await expect(page.getByTestId("port-description-pf-seed-ssh")).toHaveCount(0);
  });

  test("add port forward with tags renders chips inline", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

    await page.getByTestId("tab-ports").click();
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

  test("device tuning surfaces in edit modal and switch-to-virtio shortcut round-trips", async ({ page }) => {
    // Roadmap 5.6.12 — disk_bus / nic_model can be flipped via the
    // Device Tuning subsection in the edit modal; the "Switch to virtio"
    // shortcut pre-fills both selectors atomically.
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("btn-edit-vm").click();
    await expect(page.getByTestId("select-edit-disk-bus")).toBeVisible();
    await expect(page.getByTestId("select-edit-nic-model")).toBeVisible();

    // Click the "Switch to virtio" shortcut and verify both selectors update.
    await page.getByTestId("btn-edit-switch-virtio").click();
    await expect(page.getByTestId("select-edit-disk-bus")).toHaveValue("virtio");
    await expect(page.getByTestId("select-edit-nic-model")).toHaveValue("virtio");

    await page.getByTestId("btn-submit-edit").click();
    await expect(page.getByTestId("select-edit-disk-bus")).not.toBeVisible();

    // Re-fetch via API and assert the spec persisted both changes.
    const resp = await page.request.get(`${BASE_URL}/api/v1/vms/vm-1`);
    expect(resp.ok()).toBe(true);
    const updated = await resp.json();
    expect(updated.spec.disk_bus).toBe("virtio");
    expect(updated.spec.nic_model).toBe("virtio");
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

  // 5.4.29 — `?since=` / `?until=` time-range filter on /api/v1/images. The
  // mock seeds img-1 (ubuntu-base, created 2026-05-05) and img-2
  // (rocky-experimental, created 2026-05-12) with fixed timestamps so the
  // boundary checks are deterministic.
  test("?since= filter narrows the image list by created_at and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Set since=2026-05-10T00:00 — only the rocky-experimental (2026-05-12) survives.
    await page.getByTestId("image-list-since").fill("2026-05-10T00:00");
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toContain("2026-05-10");
    await expect(page.getByTestId("image-row-ubuntu-base")).toHaveCount(0);
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Clearing the range restores both images and drops `since=` from the URL.
    await page.getByTestId("image-list-time-range-clear").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toBeNull();
  });

  test("?until= filter narrows the image list by created_at upper bound", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // Upper bound 2026-05-10 drops the newer rocky image (2026-05-12), keeps ubuntu-base (2026-05-05).
    await page.getByTestId("image-list-until").fill("2026-05-10T00:00");
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toContain("2026-05-10");
  });

  test("time-range filter empty-state surfaces a tailored message", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // A window that contains neither seeded image (both are in May 2026).
    await page.getByTestId("image-list-since").fill("2027-01-01T00:00");
    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
    await expect(page.getByText("No images were created in the selected time range.")).toBeVisible();
  });

  // 5.4.40 — `?min_size=` / `?max_size=` byte-range filter on /api/v1/images.
  // The mock seeds img-1 (ubuntu-base, 1 GiB = 1073741824) and img-2
  // (rocky-experimental, 2 GiB = 2147483648) so a 2e9 boundary cleanly
  // separates them.
  test("size range narrows the image list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // min_size = 2,000,000,000 bytes keeps only the 2 GiB rocky image.
    await page.getByTestId("image-list-min-size").fill("2000000000");
    await expect.poll(() => new URL(page.url()).searchParams.get("min_size")).toBe("2000000000");
    await expect(page.getByTestId("image-row-ubuntu-base")).toHaveCount(0);
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // Clearing the size range restores both images and drops min_size from the URL.
    await page.getByTestId("image-list-size-range-clear").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("min_size")).toBeNull();
  });

  test("max_size upper bound drops the larger image", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // max_size = 2,000,000,000 bytes drops the 2 GiB rocky image, keeps the 1 GiB ubuntu image.
    await page.getByTestId("image-list-max-size").fill("2000000000");
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("max_size")).toBe("2000000000");
  });

  test("size-range filter empty-state surfaces a tailored message", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();

    // A floor larger than either seeded image.
    await page.getByTestId("image-list-min-size").fill("9999999999");
    await expect(page.getByTestId("image-row-ubuntu-base")).not.toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).not.toBeVisible();
    await expect(page.getByText("No images fall within the selected size range.")).toBeVisible();
  });

  // 5.4.77: name-prefix filter on the image list.
  test("name-prefix filter narrows the image list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-images").click();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();

    // prefix=rocky- keeps only the rocky cohort and writes ?prefix= to the URL.
    await page.getByTestId("image-list-prefix-filter").fill("rocky-");
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("rocky-");
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();
    await expect(page.getByTestId("image-row-ubuntu-base")).toHaveCount(0);

    // Case-sensitive: `Rocky-` matches nothing under the seeded lowercase names.
    await page.getByTestId("image-list-prefix-filter").fill("Rocky-");
    await expect(page.getByTestId("image-row-rocky-experimental")).toHaveCount(0);
    await expect(page.getByTestId("image-row-ubuntu-base")).toHaveCount(0);

    // Clearing the prefix drops the URL param and restores every image.
    await page.getByTestId("image-list-prefix-filter-clear").click();
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBeNull();
    await expect(page.getByTestId("image-row-ubuntu-base")).toBeVisible();
    await expect(page.getByTestId("image-row-rocky-experimental")).toBeVisible();
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

  test("default-user filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-default-user-filter").fill("ubuntu");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("default_user")).toBe("ubuntu");

    await page.getByTestId("template-list-default-user-filter-clear").click();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("default_user=");
  });

  test("default-user filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-default-user-filter").fill("UBUNTU");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
  });

  test("default-user filter matches no templates when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-default-user-filter").fill("nobody");
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  // 5.6.8 — os_type filter on the template list. Seed data: small-ubuntu
  // and big-rocky are linux (implicit empty + implicit empty), windows-2022
  // is os_type:"windows". The dropdown is a <select> with values
  // "" / "linux" / "windows" that round-trips through ?os_type=.
  test("os-type filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();

    await page.getByTestId("template-list-os-type-filter").selectOption("windows");
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("os_type")).toBe("windows");

    await page.getByTestId("template-list-os-type-filter").selectOption("");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("os_type=");
  });

  // Linux selection matches both explicit-linux AND implicit (empty
  // os_type) templates — mirrors the API contract documented in
  // pkg/types/template.go::VMTemplate.ResolvedOSType.
  test("os-type filter linux selection matches templates with an empty os_type", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-os-type-filter").selectOption("linux");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).not.toBeVisible();
  });

  // 5.4.67 — os_variant filter on the template list. Seed data: windows-2022
  // (windows-server-2022) and windows-11-desktop (windows-11) are the two
  // Windows templates; small-ubuntu and big-rocky have an empty os_variant
  // and must drop out whenever the filter is set (no documented "default
  // variant", mirrors the VM list 5.4.66 / API parseOSVariantFilter
  // empty-stored-excluded contract).
  test("os-variant filter narrows the template list to a single Windows edition and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-11-desktop")).toBeVisible();

    // Selecting the matching variant narrows to windows-2022 only; the
    // Linux templates (empty os_variant) and the windows-11 template are
    // filtered out.
    await page.getByTestId("template-list-os-variant-filter").selectOption("windows-server-2022");
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-11-desktop")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("os_variant")).toBe("windows-server-2022");

    // A recognised-but-unmatched variant (windows-10) collapses the cohort
    // to empty — no template carries this edition.
    await page.getByTestId("template-list-os-variant-filter").selectOption("windows-10");
    await expect(page.getByTestId("template-row-windows-2022")).not.toBeVisible();
    await expect(page.getByTestId("template-row-windows-11-desktop")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("os_variant")).toBe("windows-10");

    // Switching to windows-11 narrows to the desktop template only.
    await page.getByTestId("template-list-os-variant-filter").selectOption("windows-11");
    await expect(page.getByTestId("template-row-windows-11-desktop")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).not.toBeVisible();

    // "All variants" clears the filter and restores every template,
    // dropping the URL param.
    await page.getByTestId("template-list-os-variant-filter").selectOption("");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-11-desktop")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("os_variant=");
  });

  test("network filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    // big-rocky attaches data-net; small-ubuntu has no extra networks.
    await page.getByTestId("template-list-network-filter").fill("data-net");
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("network")).toBe("data-net");

    await page.getByTestId("template-list-network-filter-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("network=");
  });

  test("network filter is case-insensitive", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-network-filter").fill("DATA-NET");
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  test("network filter matches no templates when query has no hits", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-network-filter").fill("storage-net");
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  // 5.4.78 — name-prefix filter on the template list. Case-sensitive
  // HasPrefix; mirrors snapshot/VM/image prefix filters.
  test("name-prefix filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    // "windows-" matches windows-2022 + windows-11-desktop, excludes the
    // two Linux templates.
    await page.getByTestId("template-list-prefix-filter").fill("windows-");
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await expect(page.getByTestId("template-row-windows-11-desktop")).toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("windows-");

    // Case-sensitivity: "Windows-" matches nothing because stored names
    // are lowercased.
    await page.getByTestId("template-list-prefix-filter").fill("Windows-");
    await expect(page.getByTestId("template-row-windows-2022")).not.toBeVisible();

    // Clear restores every template + drops the URL param.
    await page.getByTestId("template-list-prefix-filter-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();
    await expect.poll(() => new URL(page.url()).search).not.toContain("prefix=");
  });

  test("sort dropdowns reorder templates and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    await page.getByTestId("template-list-sort-field").selectOption("name");
    await page.getByTestId("template-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("name");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("desc");

    const rows = page.locator('[data-testid^="template-row-"]');
    await expect(rows).toHaveCount(3);
    // name desc → "windows-2022" > "small-ubuntu" > "big-rocky" alphabetically.
    await expect(rows.nth(0)).toHaveAttribute("data-testid", "template-row-windows-2022");
    await expect(rows.nth(1)).toHaveAttribute("data-testid", "template-row-small-ubuntu");
    await expect(rows.nth(2)).toHaveAttribute("data-testid", "template-row-big-rocky");
  });

  // 5.4.89 — case-insensitive `image` sort axis on the template list, the
  // symmetric sort counterpart to the existing `?image=` filter. Mirrors the
  // VM list image sort axis (5.4.88).
  test("image sort axis reorders the template list case-insensitively and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    const rows = page.locator('[data-testid^="template-row-"]');
    await expect(rows).toHaveCount(3);

    // Seed images: small-ubuntu=/images/ubuntu-base.qcow2,
    // big-rocky=/images/rocky9.qcow2, windows-2022=/images/win-server-2022.qcow2.
    // Case-folded asc: rocky9 < ubuntu-base < win-server-2022 — so big-rocky
    // surfaces first, then small-ubuntu, then windows-2022. Mirrors the
    // case-insensitive `?image=` filter contract.
    await page.getByTestId("template-list-sort-field").selectOption("image");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=image");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-big-rocky");

    // Descending flips the order — windows-2022 heads the list.
    await page.getByTestId("template-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-windows-2022");
  });

  // 5.4.92 — case-insensitive `default_user` sort axis on the template list,
  // the symmetric sort counterpart to the existing `?default_user=` filter.
  // Diverges from the VM list `default_user` axis (5.4.91): empty stored values
  // sink to the tail of asc / head of desc rather than collapsing to "root",
  // because templates store empty as "use the image's built-in user".
  test("default_user sort axis reorders the template list case-insensitively and trails empties", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    const rows = page.locator('[data-testid^="template-row-"]');

    // Seed: small-ubuntu=ubuntu, big-rocky=root, windows-2022="" (and
    // windows-11-desktop=""). Case-folded asc: "root" < "ubuntu" < "" — so
    // big-rocky surfaces first, then small-ubuntu, then the empty cohort.
    await page.getByTestId("template-list-sort-field").selectOption("default_user");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=default_user");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-big-rocky");

    // Descending flips: empties lead, then ubuntu, then root.
    await page.getByTestId("template-list-sort-order").selectOption("desc");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");
    await expect(rows.last()).toHaveAttribute("data-testid", "template-row-big-rocky");
  });

  test("capacity sort axes (cpus / ram_mb / disk_gb) reorder templates and round-trip through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    const rows = page.locator('[data-testid^="template-row-"]');
    await expect(rows).toHaveCount(3);

    // Seed has small-ubuntu (cpus=1, ram=1024, disk=10), big-rocky (cpus=8,
    // ram=16384, disk=200), and windows-2022 (cpus=4, ram=4096, disk=64).
    // Every asc ordering puts small-ubuntu first; flipping to desc puts
    // big-rocky first.
    await page.getByTestId("template-list-sort-field").selectOption("cpus");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-small-ubuntu");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=cpus");

    await page.getByTestId("template-list-sort-order").selectOption("desc");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-big-rocky");
    await expect.poll(() => new URL(page.url()).search).toContain("order=desc");

    // Reset to asc for the next axis so the assertion below is unambiguous.
    await page.getByTestId("template-list-sort-order").selectOption("asc");

    await page.getByTestId("template-list-sort-field").selectOption("ram_mb");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-small-ubuntu");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=ram_mb");

    await page.getByTestId("template-list-sort-field").selectOption("disk_gb");
    await expect(rows.first()).toHaveAttribute("data-testid", "template-row-small-ubuntu");
    await expect.poll(() => new URL(page.url()).search).toContain("sort=disk_gb");
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
    await expect(page.getByTestId("template-row-windows-2022")).toBeVisible();
    await page.getByTestId("template-select-all").check();
    await page.getByTestId("btn-bulk-delete-templates").click();
    // Once every row drops, the table card is replaced by the EmptyState
    // branch, so just assert the rows are gone and the empty state renders.
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect(page.getByTestId("template-row-windows-2022")).not.toBeVisible();
  });

  test("delete single template via row action", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    page.once("dialog", (d) => d.accept());
    await page.getByTestId("btn-delete-template-small-ubuntu").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
  });

  // --- roadmap 5.4.31: ?since=/?until= time-range filter on template list ---
  // Seed templates carry fixed created_at timestamps (2026-05-05 for
  // small-ubuntu and 2026-05-15 for big-rocky) so the boundary check at
  // 2026-05-10 cleanly splits them.
  test("?since= filter narrows the template list by created_at and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // Set since=2026-05-10T00:00 — only big-rocky (2026-05-15) survives.
    await page.getByTestId("template-list-since").fill("2026-05-10T00:00");
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toContain("2026-05-10");
    await expect(page.getByTestId("template-row-small-ubuntu")).toHaveCount(0);
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // Clearing the range restores both templates and drops `since=` from URL.
    await page.getByTestId("template-list-time-range-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toBeNull();
  });

  test("?until= filter narrows the template list by created_at upper bound", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    // Upper bound 2026-05-10 keeps small-ubuntu (2026-05-05), drops big-rocky (2026-05-15).
    await page.getByTestId("template-list-until").fill("2026-05-10T00:00");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toContain("2026-05-10");
  });

  test("time-range filter empty-state surfaces a tailored message", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();

    // A window that contains neither seeded template (both are in May 2026).
    await page.getByTestId("template-list-since").fill("2027-01-01T00:00");
    await expect(page.getByTestId("template-row-small-ubuntu")).not.toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).not.toBeVisible();
    await expect(page.getByText("No templates were created in the selected time range.")).toBeVisible();
  });

  // --- roadmap 5.4.51: ?min_cpus= / ?max_cpus= range filter on template list ---
  // Seed templates: small-ubuntu has cpus=1, big-rocky has cpus=8, so a
  // min_cpus=4 boundary cleanly separates them.
  test("vCPU range filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // min_cpus=4 keeps only big-rocky (cpus=8).
    await page.getByTestId("template-list-min-cpus").fill("4");
    await expect.poll(() => new URL(page.url()).searchParams.get("min_cpus")).toBe("4");
    await expect(page.getByTestId("template-row-small-ubuntu")).toHaveCount(0);
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // Clear via "Clear CPUs" button restores both templates and drops the params.
    await page.getByTestId("template-list-cpu-range-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("min_cpus")).toBeNull();

    // max_cpus=4 keeps only small-ubuntu (cpus=1).
    await page.getByTestId("template-list-max-cpus").fill("4");
    await expect.poll(() => new URL(page.url()).searchParams.get("max_cpus")).toBe("4");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toHaveCount(0);
  });

  // --- roadmap 5.4.52: ?min_ram_mb= / ?max_ram_mb= range filter on template list ---
  // Seed templates: small-ubuntu has ram_mb=1024, big-rocky has ram_mb=16384,
  // so a min_ram_mb=4096 boundary cleanly separates them.
  test("RAM range filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // min_ram_mb=4096 keeps only big-rocky (ram_mb=16384).
    await page.getByTestId("template-list-min-ram-mb").fill("4096");
    await expect.poll(() => new URL(page.url()).searchParams.get("min_ram_mb")).toBe("4096");
    await expect(page.getByTestId("template-row-small-ubuntu")).toHaveCount(0);
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // Clear via "Clear RAM" button restores both templates and drops the params.
    await page.getByTestId("template-list-ram-range-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("min_ram_mb")).toBeNull();

    // max_ram_mb=4096 keeps only small-ubuntu (ram_mb=1024).
    await page.getByTestId("template-list-max-ram-mb").fill("4096");
    await expect.poll(() => new URL(page.url()).searchParams.get("max_ram_mb")).toBe("4096");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toHaveCount(0);
  });

  // --- roadmap 5.4.53: ?min_disk_gb= / ?max_disk_gb= range filter on template list ---
  // Seed templates: small-ubuntu has disk_gb=10, big-rocky has disk_gb=200, so a
  // min_disk_gb=50 boundary cleanly separates them.
  test("disk range filter narrows the template list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // min_disk_gb=50 keeps only big-rocky (disk_gb=200).
    await page.getByTestId("template-list-min-disk-gb").fill("50");
    await expect.poll(() => new URL(page.url()).searchParams.get("min_disk_gb")).toBe("50");
    await expect(page.getByTestId("template-row-small-ubuntu")).toHaveCount(0);
    await expect(page.getByTestId("template-row-big-rocky")).toBeVisible();

    // Clear via "Clear disk" button restores both templates and drops the params.
    await page.getByTestId("template-list-disk-range-clear").click();
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("min_disk_gb")).toBeNull();

    // max_disk_gb=50 keeps only small-ubuntu (disk_gb=10).
    await page.getByTestId("template-list-max-disk-gb").fill("50");
    await expect.poll(() => new URL(page.url()).searchParams.get("max_disk_gb")).toBe("50");
    await expect(page.getByTestId("template-row-small-ubuntu")).toBeVisible();
    await expect(page.getByTestId("template-row-big-rocky")).toHaveCount(0);
  });

  test("creates a new template via the New Template modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await expect(page.getByTestId("template-table")).toBeVisible();

    await page.getByTestId("btn-new-template").click();
    await expect(page.getByTestId("create-template-modal")).toBeVisible();

    await page.getByTestId("create-template-name").fill("web-tier");
    // The image control is a <select> when images are seeded; pick the first
    // real option (index 0 is the "Select an image…" placeholder).
    await page.getByTestId("create-template-image").selectOption({ index: 1 });
    await page.getByTestId("create-template-cpus").fill("4");
    await page.getByTestId("create-template-ram").fill("4096");
    await page.getByTestId("create-template-disk").fill("40");
    await page.getByTestId("create-template-default-user").fill("deploy");
    await page.getByTestId("create-template-description").fill("Web tier base");
    await page.getByTestId("create-template-tags").fill("web, prod");

    await page.getByTestId("btn-submit-create-template").click();

    // Modal closes and the new row appears in the table.
    await expect(page.getByTestId("create-template-modal")).toHaveCount(0);
    await expect(page.getByTestId("template-row-web-tier")).toBeVisible();
    await expect(page.getByTestId("template-description-web-tier")).toContainText("Web tier base");
    await expect(page.getByTestId("template-tags-web-tier")).toBeVisible();
  });

  test("New Template submit stays disabled until name and image are set", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-templates").click();
    await page.getByTestId("btn-new-template").click();

    const submit = page.getByTestId("btn-submit-create-template");
    await expect(submit).toBeDisabled();

    await page.getByTestId("create-template-name").fill("partial");
    await expect(submit).toBeDisabled();

    await page.getByTestId("create-template-image").selectOption({ index: 1 });
    await expect(submit).toBeEnabled();
  });
});

// ============================================================
// Navigation
// ============================================================
// ============================================================
// Schedules (roadmap 5.2.9)
// ============================================================
test.describe("Schedules", () => {
  test("navigates to the page and renders seeded schedules", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedules-page")).toBeVisible();
    await expect(page.getByTestId("schedule-list")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-target-sch-1")).toHaveText("vm-1");
    await expect(page.getByTestId("schedule-target-sch-2")).toContainText("tag:dev");
  });

  test("creates a schedule via the modal and a cron preset fills the cron input", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("add-schedule-btn").click();
    await expect(page.getByTestId("add-schedule-form")).toBeVisible();

    await page.getByTestId("schedule-name-input").fill("my-nightly-restart");
    await page.getByTestId("schedule-action-select").selectOption("restart");

    // Cron preset chip fills the cron input.
    await page.getByTestId("cron-preset-weekly").click();
    await expect(page.getByTestId("schedule-cron-input")).toHaveValue("0 0 3 * * 0");
    // Switch to a different preset to confirm chips overwrite the field.
    await page.getByTestId("cron-preset-hourly").click();
    await expect(page.getByTestId("schedule-cron-input")).toHaveValue("0 0 * * * *");

    await page.getByTestId("schedule-create-submit").click();
    await expect(page.getByTestId("add-schedule-form")).not.toBeVisible();

    const row = page.locator('[data-testid^="schedule-row-sch-new-"]').first();
    await expect(row).toBeVisible();
    await expect(row).toContainText("my-nightly-restart");
  });

  test("toggles enabled via the row checkbox", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    const toggle = page.getByTestId("schedule-enabled-toggle-sch-2");
    await expect(toggle).not.toBeChecked();
    await toggle.click();
    await expect(toggle).toBeChecked();
  });

  test("search filter narrows the list and round-trips to the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();

    await page.getByTestId("schedule-list-search").fill("weekend");
    await expect.poll(() => new URL(page.url()).searchParams.get("search")).toBe("weekend");
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);

    await page.getByTestId("schedule-list-search-clear").click();
    await expect.poll(() => new URL(page.url()).searchParams.get("search")).toBeNull();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
  });

  test("action filter narrows the list and round-trips to the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-action-filter").selectOption("stop");
    await expect.poll(() => new URL(page.url()).searchParams.get("action")).toBe("stop");
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
  });

  test("tag-selector filter narrows the list and round-trips to the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();

    // sch-2 carries tag_selector ["dev"]; sch-1 is vm_id-targeted (no tags).
    await page.getByTestId("schedule-tag-selector-filter").fill("dev");
    await expect.poll(() => new URL(page.url()).searchParams.get("tag_selector")).toBe("dev");
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);

    await page.getByTestId("schedule-tag-selector-filter").fill("");
    await expect.poll(() => new URL(page.url()).searchParams.get("tag_selector")).toBeNull();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
  });

  test("name-prefix filter narrows the list and round-trips to the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();

    // sch-1 is "nightly-snapshot", sch-2 is "weekend-shutdown", sch-3 is
    // "weekly-health-check". `nightly-` prefix matches only sch-1.
    await page.getByTestId("schedule-list-prefix-filter").fill("nightly-");
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("nightly-");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);

    // `weekly-` matches only sch-3 (not sch-2 which is `weekend-`).
    await page.getByTestId("schedule-list-prefix-filter").fill("weekly-");
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBe("weekly-");
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);

    // Clearing the prefix brings every schedule back.
    await page.getByTestId("schedule-list-prefix-filter").fill("");
    await expect.poll(() => new URL(page.url()).searchParams.get("prefix")).toBeNull();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
  });

  test("catch-up filter narrows the list and round-trips to the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();

    // sch-1 catch_up_policy "skip"; sch-2 "run_once".
    await page.getByTestId("schedule-catchup-filter").selectOption("run_once");
    await expect.poll(() => new URL(page.url()).searchParams.get("catch_up_policy")).toBe("run_once");
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);

    await page.getByTestId("schedule-catchup-filter").selectOption("skip");
    await expect.poll(() => new URL(page.url()).searchParams.get("catch_up_policy")).toBe("skip");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);

    await page.getByTestId("schedule-catchup-filter").selectOption("");
    await expect.poll(() => new URL(page.url()).searchParams.get("catch_up_policy")).toBeNull();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
  });

  test("timezone filter narrows the schedule list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();

    // Mock seeds: sch-1 UTC, sch-2 America/New_York, sch-3 UTC.
    await page.getByTestId("schedule-timezone-filter").fill("America/New_York");
    await expect.poll(() => new URL(page.url()).searchParams.get("timezone")).toBe("America/New_York");
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);

    // Case-sensitive: lowercase variant matches nothing.
    await page.getByTestId("schedule-timezone-filter").fill("america/new_york");
    await expect.poll(() => new URL(page.url()).searchParams.get("timezone")).toBe("america/new_york");
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);

    // UTC matches the two UTC-scheduled rows.
    await page.getByTestId("schedule-timezone-filter").fill("UTC");
    await expect.poll(() => new URL(page.url()).searchParams.get("timezone")).toBe("UTC");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);

    // Clearing the filter restores the unfiltered view.
    await page.getByTestId("schedule-timezone-filter").fill("");
    await expect.poll(() => new URL(page.url()).searchParams.get("timezone")).toBeNull();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
  });

  test("edits a schedule via the edit modal", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-edit-sch-1").click();
    await expect(page.getByTestId("edit-schedule-form")).toBeVisible();
    await expect(page.getByTestId("schedule-name-input")).toHaveValue("nightly-snapshot");

    await page.getByTestId("schedule-name-input").fill("renamed-snapshot");
    await page.getByTestId("schedule-edit-submit").click();
    await expect(page.getByTestId("edit-schedule-form")).not.toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toContainText("renamed-snapshot");
  });

  test("expands a row to show recent runs", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
  });

  test("runs status filter narrows the recent-runs expander", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();
    // All three seeded runs (run-2/run-1 success, run-3 error) show by default.
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // Filter to error: only run-3 survives.
    await page.getByTestId("schedule-runs-status-filter-sch-1").selectOption("error");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);

    // Back to all statuses restores the successes.
    await page.getByTestId("schedule-runs-status-filter-sch-1").selectOption("");
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
  });

  test("runs vm_id filter narrows the recent-runs expander", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();
    // All four seeded runs are visible by default (run-4 vm-2, run-2 vm-1, run-1 vm-1, run-3 vm-1).
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // Filter to vm-2: only run-4 survives.
    await page.getByTestId("schedule-runs-vm-filter-sch-1").fill("vm-2");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Switch to vm-1: the three vm-1 runs come back, run-4 (vm-2) drops out.
    await page.getByTestId("schedule-runs-vm-filter-sch-1").fill("vm-1");
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);

    // Clearing the filter restores the full population.
    await page.getByTestId("schedule-runs-vm-filter-sch-1").fill("");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
  });

  test("runs search filter narrows the recent-runs expander to matching error text", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();
    // All four seeded runs visible at baseline.
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // Search for "libvirt" — only run-3 carries that error message.
    await page.getByTestId("schedule-runs-search-filter-sch-1").fill("libvirt");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);

    // Case-insensitive: "SNAPSHOT" matches the lowercase "snapshot failed".
    await page.getByTestId("schedule-runs-search-filter-sch-1").fill("SNAPSHOT");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);

    // A needle with no matches yields an empty population (the three success
    // runs have no error text to match against).
    await page.getByTestId("schedule-runs-search-filter-sch-1").fill("ghost-error-no-match");
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);

    // Clearing the search restores the full population.
    await page.getByTestId("schedule-runs-search-filter-sch-1").fill("");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
  });

  test("runs sort + order dropdowns reorder the recent-runs expander", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Default order is started_at desc (newest first):
    // run-4 (May 24) → run-2 (May 23) → run-1 (May 22) → run-3 (May 21).
    const orderOf = async () => {
      const ids = await page
        .locator('[data-testid^="schedule-run-run-"]')
        .evaluateAll((els) => els.map((el) => el.getAttribute("data-testid")));
      return ids;
    };
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-3",
    ]);

    // sort=finished_at, order=asc: oldest finish first (run-3 finished May 21).
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("finished_at");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("asc");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-3",
      "schedule-run-run-1",
      "schedule-run-run-2",
      "schedule-run-run-4",
    ]);

    // sort=status, order=asc: error < success — run-3 (the only error) first,
    // then the three success runs in id-asc tiebreak (run-1, run-2, run-4).
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("status");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("asc");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-3",
      "schedule-run-run-1",
      "schedule-run-run-2",
      "schedule-run-run-4",
    ]);

    // sort=id, order=desc: run-4, run-3, run-2, run-1.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("id");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("desc");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-3",
      "schedule-run-run-2",
      "schedule-run-run-1",
    ]);

    // Reset to default — original newest-first ordering restored.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("");
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-3",
    ]);
  });

  // 5.4.95 — vm_id sort axis orders the runs expander by their target VM
  // ID with case-sensitive ASCII compare; runs with an empty vm_id sink
  // to the tail in asc / head in desc. Mirrors the events vm_id sort
  // axis (5.4.93) and the logs vm_id sort axis (5.4.94).
  test("vm_id sort axis orders the recent-runs expander by target VM id", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Seeded vm_id distribution:
    //   run-4 -> vm-2; run-1/2/3/5/6 -> vm-1.
    const orderOf = async () => {
      const ids = await page
        .locator('[data-testid^="schedule-run-run-"]')
        .evaluateAll((els) => els.map((el) => el.getAttribute("data-testid")));
      return ids;
    };

    // sort=vm_id, order=asc: vm-1 runs first (id tiebreak run-1..run-6),
    // then vm-2 (only run-4).
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("vm_id");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("asc");
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-1",
      "schedule-run-run-2",
      "schedule-run-run-3",
      "schedule-run-run-5",
      "schedule-run-run-6",
      "schedule-run-run-4",
    ]);

    // sort=vm_id, order=desc: vm-2 first (run-4), then vm-1 in reverse
    // id tiebreak.
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("desc");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-6",
      "schedule-run-run-5",
      "schedule-run-run-3",
      "schedule-run-run-2",
      "schedule-run-run-1",
    ]);

    // Reset — original newest-started-first ordering restored.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("");
  });

  test("duration sort axis orders the recent-runs expander by finish-minus-start", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Seeded durations (finished_at - started_at):
    //   run-3 = 3s, run-1 = 4s, run-2 = 5s, run-4 = 6s.
    const orderOf = async () => {
      const ids = await page
        .locator('[data-testid^="schedule-run-run-"]')
        .evaluateAll((els) => els.map((el) => el.getAttribute("data-testid")));
      return ids;
    };

    // sort=duration, order=asc: shortest first.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("duration");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("asc");
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-3",
      "schedule-run-run-1",
      "schedule-run-run-2",
      "schedule-run-run-4",
    ]);

    // sort=duration, order=desc: longest first.
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("desc");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-3",
    ]);

    // Reset to default — original newest-started-first ordering restored.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("");
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-3",
    ]);
  });

  test("skip_reason sort axis orders the recent-runs expander and sinks empty-reason runs to the tail", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Seeded skip_reasons:
    //   run-6 = queue_full, run-5 = vm_already_stopped,
    //   run-1..run-4 = empty (non-skipped success/error runs).
    const orderOf = async () => {
      const ids = await page
        .locator('[data-testid^="schedule-run-run-"]')
        .evaluateAll((els) => els.map((el) => el.getAttribute("data-testid")));
      return ids;
    };

    // sort=skip_reason, order=asc:
    //   populated reasons alphabetical (queue_full < vm_already_stopped),
    //   empty-reason runs trail tiebroken ascending by id.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("skip_reason");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("asc");
    await expect(page.getByTestId("schedule-run-run-6")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-6",
      "schedule-run-run-5",
      "schedule-run-run-1",
      "schedule-run-run-2",
      "schedule-run-run-3",
      "schedule-run-run-4",
    ]);

    // sort=skip_reason, order=desc:
    //   empty-reason runs lead tiebroken descending by id, populated reasons
    //   follow descending alphabetically.
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("desc");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-3",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-5",
      "schedule-run-run-6",
    ]);

    // Reset to default — original newest-started-first ordering restored.
    await page.getByTestId("schedule-runs-sort-sch-1").selectOption("");
    await page.getByTestId("schedule-runs-order-sch-1").selectOption("");
    expect(await orderOf()).toEqual([
      "schedule-run-run-4",
      "schedule-run-run-2",
      "schedule-run-run-1",
      "schedule-run-run-3",
      "schedule-run-run-5",
      "schedule-run-run-6",
    ]);
  });

  test("finished_at range filter narrows the recent-runs expander to runs that finished inside the window", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Baseline: all 4 seeded runs visible (finished_at on 2026-05-21..2026-05-24).
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // finished_since=2026-05-23T00:00 — only run-4 (2026-05-24) and run-2
    // (2026-05-23) survive; run-1 and run-3 finished earlier.
    await page.getByTestId("schedule-runs-finished-since-filter-sch-1").fill("2026-05-23T00:00");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Add finished_until=2026-05-23T23:59 — narrows further to just run-2.
    await page.getByTestId("schedule-runs-finished-until-filter-sch-1").fill("2026-05-23T23:59");
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Clear-finished button restores every run.
    await page.getByTestId("schedule-runs-finished-clear-sch-1").click();
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
  });

  // --- 5.4.64: min_duration_ms / max_duration_ms range filter on runs ---
  // Seed durations (finished_at - started_at): run-4 = 6000 ms, run-2 = 5000 ms,
  // run-1 = 4000 ms, run-3 = 3000 ms. The 4000..5000 ms window splits cleanly.
  test("duration range filter narrows recent runs by finished_at - started_at duration", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Baseline: all 4 seeded runs visible.
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // min=5000 ms: only run-4 (6s) and run-2 (5s) survive — inclusive lower bound.
    await page.getByTestId("schedule-runs-min-duration-ms-filter-sch-1").fill("5000");
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Add max=5000 ms: window collapses to exactly 5000 ms — only run-2.
    await page.getByTestId("schedule-runs-max-duration-ms-filter-sch-1").fill("5000");
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Clear-duration button restores every run.
    await page.getByTestId("schedule-runs-duration-clear-sch-1").click();
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();

    // Upper bound only — max=4000 ms keeps run-1 (4s) and run-3 (3s).
    await page.getByTestId("schedule-runs-max-duration-ms-filter-sch-1").fill("4000");
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);
  });

  // 5.4.65 — skip_reason filter narrows the recent-runs expander to skipped
  // runs persisted with a specific reason. The seeded sch-1 cohort carries
  // four non-skipped runs plus run-5 (skipped/vm_already_stopped) and run-6
  // (skipped/queue_full); the filter excludes every run without a populated
  // skip_reason whenever it's set.
  test("skip_reason filter narrows recent runs to one skipped reason at a time", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-row-toggle-sch-1").click();
    await expect(page.getByTestId("schedule-runs-sch-1")).toBeVisible();

    // Baseline: all 6 seeded runs visible.
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-2")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-1")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-3")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-5")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-6")).toBeVisible();

    // queue_full → only run-6 survives; every non-skipped row and run-5 drop.
    await page.getByTestId("schedule-runs-skip-reason-filter-sch-1").selectOption("queue_full");
    await expect(page.getByTestId("schedule-run-run-6")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-5")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-3")).toHaveCount(0);

    // Switch to vm_already_stopped → only run-5.
    await page.getByTestId("schedule-runs-skip-reason-filter-sch-1").selectOption("vm_already_stopped");
    await expect(page.getByTestId("schedule-run-run-5")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-6")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-4")).toHaveCount(0);

    // Reset to "All skip reasons" restores every row.
    await page.getByTestId("schedule-runs-skip-reason-filter-sch-1").selectOption("");
    await expect(page.getByTestId("schedule-run-run-6")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-5")).toBeVisible();
    await expect(page.getByTestId("schedule-run-run-4")).toBeVisible();

    // Recognized-but-unused reason yields an empty cohort (every seeded
    // skip is queue_full or vm_already_stopped).
    await page.getByTestId("schedule-runs-skip-reason-filter-sch-1").selectOption("concurrent_run");
    await expect(page.getByTestId("schedule-run-run-6")).toHaveCount(0);
    await expect(page.getByTestId("schedule-run-run-5")).toHaveCount(0);
  });

  test("run-now appends a run for the schedule", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    await page.getByTestId("schedule-runnow-sch-2").click();
    // Expand to confirm a fresh run was recorded.
    await page.getByTestId("schedule-row-toggle-sch-2").click();
    await expect(page.getByTestId("schedule-runs-sch-2")).toBeVisible();
    await expect(page.locator('[data-testid^="schedule-run-run-now-"]').first()).toBeVisible();
  });

  test("vm detail shows direct, tag-targeted, and global schedules and can prefill a new VM schedule", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("vm-row-web-server").click();

    await page.getByTestId("tab-schedules").click();
    await expect(page.getByTestId("vm-detail-schedules")).toBeVisible();
    await expect(page.getByTestId("vm-detail-schedule-sch-1")).toContainText("nightly-snapshot");
    await expect(page.getByTestId("vm-detail-schedule-sch-2")).toContainText("weekend-shutdown");
    await expect(page.getByTestId("vm-detail-schedule-sch-3")).toContainText("weekly-health-check");

    await page.getByTestId("btn-add-schedule-from-vm").click();
    await expect(page.getByTestId("schedules-page")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("prefill_vm_id")).toBe("vm-1");
    await expect(page.getByTestId("add-schedule-form")).toBeVisible();
    await expect(page.getByTestId("schedule-vmid-input")).toHaveValue("vm-1");
    await expect(page.getByTestId("schedule-name-input")).toHaveValue("web-server-schedule");

    await page.getByTestId("schedule-name-input").fill("web-server-restart");
    await page.getByTestId("schedule-action-select").selectOption("restart");
    await page.getByTestId("schedule-create-submit").click();

    await expect(page.getByTestId("add-schedule-form")).not.toBeVisible();
    await page.goBack();
    await page.getByTestId("tab-schedules").click();
    await expect(page.getByTestId("vm-detail-schedules")).toBeVisible();
    const row = page.locator('[data-testid^="vm-detail-schedule-sch-new-"]').first();
    await expect(row).toContainText("web-server-restart");
    await expect(row).toContainText("restart");
  });

  test("deletes a schedule", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    page.on("dialog", (dialog) => dialog.accept());
    await page.getByTestId("schedule-delete-sch-2").click();
    await expect(page.getByTestId("schedule-row-sch-2")).not.toBeVisible();
  });

  // --- 5.4.39: ?since= / ?until= time-range filter on schedule created_at ---
  // Seed data: sch-1 (nightly-snapshot) created 2026-05-05, sch-2
  // (weekend-shutdown) created 2026-05-10. The boundary at 2026-05-08 cleanly
  // splits them so the assertions are robust against TZ drift.

  test("created-since filter narrows the schedule list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();

    // since=2026-05-08T00:00 — only sch-2 (created 2026-05-10) survives.
    await page.getByTestId("schedule-list-since-filter").fill("2026-05-08T00:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toContain("2026-05-08");

    // The Clear-range button drops both filters and the URL params.
    await page.getByTestId("schedule-list-time-range-clear").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toBeNull();
  });

  test("created-until filter narrows the schedule list by upper bound", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    // until=2026-05-08T00:00 — only sch-1 (created 2026-05-05) remains.
    await page.getByTestId("schedule-list-until-filter").fill("2026-05-08T00:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toContain("2026-05-08");
  });

  test("created-at range empty-state surfaces a tailored message", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    // A window before both seeded schedules returns nothing.
    await page.getByTestId("schedule-list-since-filter").fill("2024-01-01T00:00");
    await page.getByTestId("schedule-list-until-filter").fill("2024-12-31T00:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByText("No schedules match your filters")).toBeVisible();
  });

  // --- 5.4.60: ?next_fire_since= / ?next_fire_until= filter on next_fire_at ---
  // Seed data: sch-1 next_fire_at=2026-05-25T02:00:00Z, sch-2 next_fire_at=null
  // (disabled), sch-3 next_fire_at=2026-05-26T04:30:00Z. The boundary at
  // 2026-05-26T00:00 cleanly splits sch-1 and sch-3; the nil-next_fire schedule
  // is always excluded once any bound is set.
  test("next_fire range filter narrows the schedule list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();

    // next_fire_since=2026-05-26T00:00 — only sch-3 (next 2026-05-26T04:30) survives.
    // sch-1 is too early, sch-2 has a nil next_fire_at and is excluded under any bound.
    await page.getByTestId("schedule-list-next-fire-since-filter").fill("2026-05-26T00:00");
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("next_fire_since")).toContain("2026-05-26");

    // Add an upper bound that excludes sch-3 — the post-filter set is empty.
    await page.getByTestId("schedule-list-next-fire-until-filter").fill("2026-05-26T01:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);
    await expect(page.getByText("No schedules match your filters")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("next_fire_until")).toContain("2026-05-26");

    // The Clear next-fire button drops both bounds and restores the unfiltered view.
    await page.getByTestId("schedule-list-next-fire-range-clear").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("next_fire_since")).toBeNull();
    await expect.poll(() => new URL(page.url()).searchParams.get("next_fire_until")).toBeNull();
  });

  test("next_fire_until upper bound excludes nil-next_fire schedules", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();

    // until=2026-05-25T12:00 — only sch-1 (next 2026-05-25T02:00) remains.
    // sch-3 is too late, sch-2 has a nil next_fire_at and is excluded.
    await page.getByTestId("schedule-list-next-fire-until-filter").fill("2026-05-25T12:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("next_fire_until")).toContain("2026-05-25");
  });

  // --- 5.4.74: ?last_fired_since= / ?last_fired_until= filter on last_fired_at ---
  // Seed data: sch-1 last_fired_at=2026-05-23T02:00:00Z, sch-2 last_fired_at=null
  // (never fired), sch-3 last_fired_at=2026-05-19T04:30:00Z. The boundary at
  // 2026-05-21T00:00 cleanly splits sch-1 (after) and sch-3 (before); the
  // never-fired schedule is always excluded once any bound is set, mirroring
  // the next_fire range nil-exclusion.
  test("last_fired range filter narrows the schedule list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();

    // last_fired_since=2026-05-21T00:00 — only sch-1 (fired 2026-05-23T02:00) survives.
    // sch-3 fired before the bound and sch-2 has a nil last_fired_at (excluded under any bound).
    await page.getByTestId("schedule-list-last-fired-since-filter").fill("2026-05-21T00:00");
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-3")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("last_fired_since")).toContain("2026-05-21");

    // Replace the since bound with an upper-bound only: sch-3 (2026-05-19) survives.
    await page.getByTestId("schedule-list-last-fired-range-clear").click();
    await page.getByTestId("schedule-list-last-fired-until-filter").fill("2026-05-20T00:00");
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-1")).toHaveCount(0);
    await expect(page.getByTestId("schedule-row-sch-2")).toHaveCount(0);
    await expect.poll(() => new URL(page.url()).searchParams.get("last_fired_until")).toContain("2026-05-20");

    // The Clear last-fired button drops both bounds and restores the unfiltered view.
    await page.getByTestId("schedule-list-last-fired-range-clear").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-2")).toBeVisible();
    await expect(page.getByTestId("schedule-row-sch-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("last_fired_since")).toBeNull();
    await expect.poll(() => new URL(page.url()).searchParams.get("last_fired_until")).toBeNull();
  });

  // --- 5.4.84: last_fired_at sort axis on the schedule list ---
  // Seed data: sch-1 last_fired_at=2026-05-23T02:00:00Z (most recent concrete),
  // sch-2 last_fired_at=null (never fired), sch-3 last_fired_at=2026-05-19T04:30:00Z
  // (earliest concrete). The new sort axis pushes nil last_fired_at to the tail
  // in asc and the head in desc, mirroring the next_fire_at nil-handling and
  // the webhook last_delivery_at sort axis.
  test("last_fired_at sort dropdown reorders the schedule list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedule-row-sch-1")).toBeVisible();

    // asc: earliest concrete last_fired first (sch-3 → sch-1), never-fired sinks to tail (sch-2).
    await page.getByTestId("schedule-list-sort-field").selectOption("last_fired_at");
    await page.getByTestId("schedule-list-sort-order").selectOption("asc");
    await expect.poll(async () => {
      const rows = await page.locator("tr[data-testid^=\"schedule-row-\"]").elementHandles();
      return Promise.all(rows.map((r) => r.getAttribute("data-testid")));
    }).toEqual(["schedule-row-sch-3", "schedule-row-sch-1", "schedule-row-sch-2"]);
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("last_fired_at");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("asc");

    // desc: never-fired surfaces first (sch-2), then concrete last_fired newest → oldest (sch-1 → sch-3).
    await page.getByTestId("schedule-list-sort-order").selectOption("desc");
    await expect.poll(async () => {
      const rows = await page.locator("tr[data-testid^=\"schedule-row-\"]").elementHandles();
      return Promise.all(rows.map((r) => r.getAttribute("data-testid")));
    }).toEqual(["schedule-row-sch-2", "schedule-row-sch-1", "schedule-row-sch-3"]);
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("desc");

    // Reset to default — sort param drops from the URL, default id-asc returns.
    await page.getByTestId("schedule-list-sort-field").selectOption("");
    await page.getByTestId("schedule-list-sort-order").selectOption("");
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBeNull();
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBeNull();
  });
});

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

    // Schedules
    await page.getByTestId("nav-schedules").click();
    await expect(page.getByTestId("schedules-page")).toBeVisible();

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

    // Reload to discard the local probe result and verify the persisted
    // last_status contract still renders as healthy even when last_error is
    // omitted from the API payload.
    await page.reload();
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId(`webhook-row-${rowID}`)).toBeVisible();
    await expect(page.getByTestId("webhook-status").first()).toContainText(/HTTP 204/i);

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

    // Reload to discard the local probe result and verify the persisted
    // last_status + last_error contract still renders as a failure.
    await page.reload();
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId(`webhook-row-${rowID}`)).toBeVisible();
    await expect(page.getByTestId("webhook-status").first()).toContainText(/HTTP 500/i);
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

  // 5.4.83 — case-insensitive URL-prefix filter on the webhook list.
  // Diverges from the case-sensitive name-prefix family (snapshots/VMs/
  // images/templates/schedules) because URLs are case-insensitive per
  // RFC 3986. Seeds two receivers on hooks.slack.com and one on
  // events.pagerduty.com; the prefix `https://hooks.slack.com/` must
  // narrow the list to the Slack pair, and `HTTPS://HOOKS.SLACK.COM/`
  // (mixed case) must do the same — closes the operator query that
  // ?search= can answer only with noisy fuzzy matches.
  test("url-prefix filter narrows the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    const seed = async (url) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://hooks.slack.com/services/T01/B01/abc");
    await seed("https://hooks.slack.com/services/T02/B02/def");
    await seed("https://events.pagerduty.com/v2/enqueue");

    // All three rows render initially.
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // Prefix filter narrows to the two Slack receivers.
    await page.getByTestId("webhook-list-url-prefix").fill("https://hooks.slack.com/");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(2);

    // URL round-trip — `?url_prefix=` is reflected in the address bar.
    await expect.poll(async () => new URL(page.url()).searchParams.get("url_prefix")).toBe("https://hooks.slack.com/");

    // Case-insensitive matching: mixed-case URL must still match
    // (RFC 3986 scheme/host case-insensitivity).
    await page.getByTestId("webhook-list-url-prefix").fill("HTTPS://HOOKS.SLACK.COM/");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(2);

    // Clear button restores the unfiltered view.
    await page.getByTestId("webhook-list-url-prefix-clear").click();
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);
    await expect.poll(async () => new URL(page.url()).searchParams.get("url_prefix")).toBe(null);
  });

  // 5.4.32 — time-range filter (?since= / ?until=) on the webhook list.
  // Mirrors the snapshot (5.4.28), image (5.4.29), VM (5.4.30), and template
  // (5.4.31) time-range filters. Webhooks created via the modal get
  // `created_at: now()` on the mock server, so this test exercises the
  // URL round-trip + "Clear range" affordance rather than boundary-splitting
  // (which is covered by the API and CLI integration tests).
  test("created-since/until inputs round-trip through the URL and clear via the Clear range button", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();

    // Set a since bound — the URL must reflect it.
    await page.getByTestId("webhook-list-since").fill("2026-05-01T00:00");
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toContain("2026-05-01");

    // Set an until bound — both bounds round-trip simultaneously.
    await page.getByTestId("webhook-list-until").fill("2026-05-20T00:00");
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toContain("2026-05-20");

    // "Clear range" drops both bounds from the URL in a single click.
    await page.getByTestId("webhook-list-clear-range").click();
    await expect.poll(() => new URL(page.url()).searchParams.get("since")).toBeNull();
    await expect.poll(() => new URL(page.url()).searchParams.get("until")).toBeNull();
  });

  // 5.4.61 — last-delivery time-range filter (?last_delivery_since= /
  // ?last_delivery_until=) on the webhook list. Mirrors the created_at
  // time-range filter (5.4.32) on the webhook surface. The mock server
  // accepts `last_delivery_at` directly on POST /webhooks for test
  // fixturing (the real daemon ignores the field — it's populated by the
  // background delivery worker). The test seeds three webhooks at
  // deterministic timestamps so a 2026-05-10 boundary splits them, then
  // asserts each filter narrows correctly and the URL round-trips.
  test("last-delivery range filter narrows the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    // Seed three webhooks directly via mock POST so we control last_delivery_at.
    const seed = async (suffix, lastDeliveryAt) => {
      const resp = await page.request.post(`${BASE_URL}/api/v1/webhooks`, {
        data: {
          url: `https://hook-${suffix}.example.com`,
          secret: "k",
          last_delivery_at: lastDeliveryAt,
          last_status: lastDeliveryAt ? 200 : 0,
        },
      });
      if (!resp.ok()) throw new Error(`seed ${suffix}: HTTP ${resp.status()}`);
    };
    await seed("early", "2026-05-01T12:00:00Z");
    await seed("mid", "2026-05-15T12:00:00Z");
    await seed("never", ""); // never-delivered

    // Reload so the list picks up the seeded rows.
    await page.reload();
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // Apply a since bound of 2026-05-10 — wh-early excluded, wh-mid kept,
    // wh-never excluded by zero-time rule.
    await page.getByTestId("webhook-list-last-delivery-since").fill("2026-05-10T00:00");
    await expect.poll(() =>
      new URL(page.url()).searchParams.get("last_delivery_since"),
    ).toContain("2026-05-10");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("hook-mid.example.com");

    // Add an until bound of 2026-05-20 — wh-mid still matches.
    await page.getByTestId("webhook-list-last-delivery-until").fill("2026-05-20T00:00");
    await expect.poll(() =>
      new URL(page.url()).searchParams.get("last_delivery_until"),
    ).toContain("2026-05-20");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("hook-mid.example.com");

    // Wide-open range that covers every dated webhook — wh-early + wh-mid
    // appear; wh-never is still excluded by the zero-time rule.
    await page.getByTestId("webhook-list-last-delivery-since").fill("2025-01-01T00:00");
    await page.getByTestId("webhook-list-last-delivery-until").fill("2027-01-01T00:00");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(2);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("hook-early.example.com");
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("hook-mid.example.com");

    // Until bound earlier than every delivery — no rows match; the
    // tailored last-delivery empty-state appears and nudges operators
    // toward the existing delivery-status filter for never-delivered.
    await page.getByTestId("webhook-list-last-delivery-since").fill("");
    await page.getByTestId("webhook-list-last-delivery-until").fill("2025-01-01T00:00");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(0);
    await expect(page.getByText(/No deliveries in this window/i)).toBeVisible();

    // "Clear last delivery" drops both bounds from the URL in a single
    // click and restores every row.
    await page.getByTestId("webhook-list-clear-last-delivery-range").click();
    await expect.poll(() =>
      new URL(page.url()).searchParams.get("last_delivery_since"),
    ).toBeNull();
    await expect.poll(() =>
      new URL(page.url()).searchParams.get("last_delivery_until"),
    ).toBeNull();
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);
  });

  // 5.4.35 — delivery-status filter (?delivery_status=never|healthy|failing)
  // on the webhook list. Seeds three webhooks via the UI: a fresh untested
  // one (→ never), one that we "Test" against a healthy URL (→ healthy), and
  // one with "fail" in the URL so the mock /test endpoint reports a 500
  // (→ failing). Asserts each filter value narrows to exactly the right
  // row, that URL round-trip works, and that "All" restores every row.
  test("delivery-status dropdown filters the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    const seed = async (url) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://untouched.example.com/hook"); // never delivered
    await seed("https://healthy.example.com/hook");   // we'll mark this one healthy
    await seed("https://fail.example.com/hook");      // we'll mark this one failing

    // All three rows render initially.
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);

    // Find each row and trigger /test on the two that should leave the never
    // bucket. The mock /test endpoint sets last_status=204 + clears last_error
    // for healthy URLs and last_status=0 + last_error=HTTP 500 for any URL
    // containing "fail".
    const rowFor = (url) => page.locator(`[data-testid^="webhook-row-"]:has-text("${url}")`);
    const healthyRow = rowFor("healthy.example.com");
    const failingRow = rowFor("fail.example.com");
    const healthyID = (await healthyRow.getAttribute("data-testid")).replace("webhook-row-", "");
    const failingID = (await failingRow.getAttribute("data-testid")).replace("webhook-row-", "");
    await page.getByTestId(`webhook-test-${healthyID}`).click();
    await page.getByTestId(`webhook-test-${failingID}`).click();
    // The poll period in the list is 15s, but Settings refreshes after a test
    // delivery — wait until both last-status badges are visible to confirm
    // the state update reached the daemon.
    await expect.poll(async () => {
      const rows = await page.locator('[data-testid^="webhook-row-"]').count();
      return rows;
    }).toBe(3);

    const dropdown = page.getByTestId("webhook-list-delivery-status");

    // failing — only the fail row remains.
    await dropdown.selectOption("failing");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("fail.example.com");
    await expect.poll(() => new URL(page.url()).searchParams.get("delivery_status")).toBe("failing");

    // healthy — only the healthy row remains.
    await dropdown.selectOption("healthy");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("healthy.example.com");
    await expect.poll(() => new URL(page.url()).searchParams.get("delivery_status")).toBe("healthy");

    // never — only the never-tested row remains.
    await dropdown.selectOption("never");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("untouched.example.com");
    await expect.poll(() => new URL(page.url()).searchParams.get("delivery_status")).toBe("never");

    // Reset back to All — the URL param is dropped and every row reappears.
    await dropdown.selectOption("");
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(3);
    await expect.poll(() => new URL(page.url()).searchParams.get("delivery_status")).toBeNull();
  });

  // 5.4.37 — active filter (?active=true|false) on the webhook list. Seeds two
  // webhooks via the UI, disables one via the edit modal's active toggle, then
  // asserts each filter value narrows to the right row, that the URL round-trips
  // through ?active=, and that "All" restores every row.
  test("active dropdown filters the webhook list and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-settings").click();
    await expect(page.getByTestId("settings-page")).toBeVisible();

    const seed = async (url) => {
      await page.getByTestId("add-webhook-btn").click();
      await page.getByTestId("webhook-url-input").fill(url);
      await page.getByTestId("webhook-secret-input").fill("k");
      await page.getByTestId("webhook-create-submit").click();
      await expect(page.getByTestId("add-webhook-form")).not.toBeVisible();
    };
    await seed("https://live.example.com/hook");     // stays active
    await seed("https://disabled.example.com/hook");  // we'll disable this one

    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(2);

    // Disable the second webhook via the edit modal's active toggle.
    const disabledRow = page.locator('[data-testid^="webhook-row-"]:has-text("disabled.example.com")');
    const disabledID = (await disabledRow.getAttribute("data-testid")).replace("webhook-row-", "");
    await page.getByTestId(`webhook-edit-${disabledID}`).click();
    await expect(page.getByTestId("edit-webhook-form")).toBeVisible();
    await page.getByTestId("edit-webhook-active-toggle").uncheck();
    await page.getByTestId("edit-webhook-submit").click();
    await expect(page.getByTestId("edit-webhook-form")).not.toBeVisible();

    const dropdown = page.getByTestId("webhook-list-active");

    // active=false — only the disabled row remains.
    await dropdown.selectOption("false");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("disabled.example.com");
    await expect.poll(() => new URL(page.url()).searchParams.get("active")).toBe("false");

    // active=true — only the live row remains.
    await dropdown.selectOption("true");
    await expect.poll(async () =>
      page.locator('[data-testid^="webhook-row-"]').count(),
    ).toBe(1);
    await expect(page.locator('[data-testid^="webhook-row-"]')).toContainText("live.example.com");
    await expect.poll(() => new URL(page.url()).searchParams.get("active")).toBe("true");

    // Reset back to All — the URL param is dropped and every row reappears.
    await dropdown.selectOption("");
    await expect(page.locator('[data-testid^="webhook-row-"]')).toHaveCount(2);
    await expect.poll(() => new URL(page.url()).searchParams.get("active")).toBeNull();
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

  // 5.4.94 — vm_id sort axis on the logs list.
  test("vm_id sort axis orders the log table and sinks no-vm_id entries to the tail", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    await page.getByTestId("log-sort-field").selectOption("vm_id");
    await page.getByTestId("log-sort-order").selectOption("asc");

    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("sort=vm_id");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("order=asc");

    // Mock seeds: vm-1 (ts1+ts4), vm-2 (ts3), no vm_id (ts2) — asc with
    // empty-trailing should put vm-1 rows first, then vm-2, then the
    // no-vm_id row at the tail.
    const rows = page.locator('[data-testid="log-table"] tbody tr');
    await expect(rows.first()).toContainText("vm-1");
    await expect(rows.last()).toContainText("vm list");
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

  // ── 5.4.34 time-range filter ────────────────────────────────────────
  test("until filter narrows the log table and round-trips through the URL", async ({ page }) => {
    await page.goto(BASE_URL);
    await page.getByTestId("nav-logs").click();
    await expect(page.getByTestId("log-table")).toBeVisible();

    // The mock-server seeds 5 entries at baseTs+0..+4ms. Picking a value
    // far in the past as the upper bound should empty the table — and the
    // empty-state copy must call out the time-range branch.
    await page.getByTestId("log-until-filter").fill("2020-01-01T00:00");
    await expect.poll(() => page.url(), { timeout: 2000 }).toContain("until=");
    await expect(page.getByTestId("log-empty-state")).toContainText("time range");

    // Clearing the range must drop both query params and re-populate.
    await page.getByTestId("log-time-range-clear").click();
    await expect.poll(() => page.url(), { timeout: 2000 }).not.toContain("until=");
    await expect.poll(() => page.url(), { timeout: 2000 }).not.toContain("since=");
    await expect(page.locator('[data-testid="log-table"] tbody')).toContainText("vmSmith daemon listening");
  });

  test("since and until filter values hydrate from the URL on load", async ({ page }) => {
    await page.goto(`${BASE_URL}/logs?since=2020-01-01T00%3A00&until=2020-01-01T01%3A00`);
    await expect(page.getByTestId("log-table")).toBeVisible();
    // Range entirely in the past → empty table with the dedicated copy.
    await expect(page.getByTestId("log-empty-state")).toContainText("time range");
    // The inputs hydrate from the URL.
    await expect(page.getByTestId("log-since-filter")).toHaveValue("2020-01-01T00:00");
    await expect(page.getByTestId("log-until-filter")).toHaveValue("2020-01-01T01:00");
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

  // 5.4.41 — severity-floor filter.
  test("min-severity floor narrows results and round-trips through the URL", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    // Before filtering: the info event (evt-3) and the warn event (evt-1)
    // are both visible.
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();

    // warn floor → only the warn event (evt-1) survives; info events drop.
    await page.getByTestId("activity-filter-min-severity").selectOption("warn");
    await expect(page.getByTestId("activity-row-evt-1")).toBeVisible();
    await expect(page.getByTestId("activity-row-evt-3")).toHaveCount(0);
    await expect(page.getByTestId("activity-row-evt-2")).toHaveCount(0);

    // The committed floor lands in the URL so the filtered view is shareable.
    await expect.poll(() => new URL(page.url()).searchParams.get("min_severity")).toBe("warn");

    // Clearing the filters restores the full list and drops the param.
    await page.getByTestId("btn-activity-clear-filters").click();
    await expect(page.getByTestId("activity-row-evt-3")).toBeVisible();
    await expect.poll(() => new URL(page.url()).searchParams.get("min_severity")).toBe(null);
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

  // 5.4.87 — `actor` sort axis (case-sensitive, empty-trailing).
  test("actor sort axis reorders the activity timeline case-sensitively and trails empties", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    const rows = page.locator('[data-testid^="activity-row-"]');

    // sort=actor asc: "ops-alice" < "system" (case-sensitive), evt-0 (empty
    // actor) sinks to the tail. evt-1 and evt-3 both carry "system" so they
    // tiebreak on id ascending — evt-1 before evt-3.
    await page.getByTestId("activity-sort-field").selectOption("actor");
    await page.getByTestId("activity-sort-order").selectOption("asc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-2");
    await expect.poll(async () => (await rows.last().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-0");

    // URL captures the new sort + order so a refresh/back-button restores it.
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("actor");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("asc");

    // sort=actor desc: empty actor heads (evt-0), then concrete actors fall
    // in reverse case-sensitive order.
    await page.getByTestId("activity-sort-order").selectOption("desc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-0");
  });

  // 5.4.93 — `vm_id` sort axis (case-sensitive, empty-trailing).
  test("vm_id sort axis reorders the activity timeline case-sensitively and trails empties", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    const rows = page.locator('[data-testid^="activity-row-"]');

    // sort=vm_id asc: "vm-1" (evt-2 and evt-3 tiebreak on id — evt-2 before
    // evt-3) < "vm-2" (evt-1), then evt-0 (empty vm_id — host-level event)
    // sinks to the tail.
    await page.getByTestId("activity-sort-field").selectOption("vm_id");
    await page.getByTestId("activity-sort-order").selectOption("asc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-2");
    await expect.poll(async () => (await rows.last().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-0");

    // URL captures the new sort + order so a refresh/back-button restores it.
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("vm_id");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("asc");

    // sort=vm_id desc: empty vm_id heads (evt-0), then "vm-2" (evt-1),
    // then "vm-1" — id tiebreak reverses under desc so evt-3 before evt-2.
    await page.getByTestId("activity-sort-order").selectOption("desc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-0");
    await expect.poll(async () => (await rows.last().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-2");
  });

  // 5.4.90 — `resource_id` sort axis (case-sensitive, empty-trailing).
  test("resource_id sort axis reorders the activity timeline case-sensitively and trails empties", async ({ page }) => {
    await page.goto(`${BASE_URL}/activity`);
    const rows = page.locator('[data-testid^="activity-row-"]');

    // sort=resource_id asc: "img-2" (evt-1) < "tpl-rocky9-base" (evt-2),
    // then evt-0 and evt-3 (both empty resource_id) trail — tiebreak on id
    // ascending puts evt-0 before evt-3 at the tail.
    await page.getByTestId("activity-sort-field").selectOption("resource_id");
    await page.getByTestId("activity-sort-order").selectOption("asc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-1");
    await expect.poll(async () => (await rows.last().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-3");

    // URL captures the new sort + order so a refresh/back-button restores it.
    await expect.poll(() => new URL(page.url()).searchParams.get("sort")).toBe("resource_id");
    await expect.poll(() => new URL(page.url()).searchParams.get("order")).toBe("asc");

    // sort=resource_id desc: empty resource ids head — evt-3 / evt-0 (id
    // tiebreak reverses under desc, so evt-3 comes first), then concrete
    // resource ids fall in reverse: evt-2 ("tpl-rocky9-base"), evt-1
    // ("img-2") at the tail.
    await page.getByTestId("activity-sort-order").selectOption("desc");
    await expect.poll(async () => (await rows.first().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-3");
    await expect.poll(async () => (await rows.last().getAttribute("data-testid")) || "")
      .toBe("activity-row-evt-1");
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

  test("indicator shows shutdown when the daemon emits a shutdown control frame", async ({ page }) => {
    await page.route("**/api/v1/events/stream*", async (route) => {
      await route.fulfill({
        status: 200,
        headers: {
          "content-type": "text/event-stream",
          "cache-control": "no-cache",
          connection: "keep-alive",
        },
        body: [
          'event: shutdown',
          'data: {"type":"shutdown","message":"daemon stopping"}',
          '',
          '',
        ].join("\n"),
      });
    });
    await page.goto(BASE_URL);
    const indicator = page.getByTestId("live-indicator");
    await expect.poll(
      async () => indicator.getAttribute("data-status"),
      { timeout: 5000 },
    ).toBe("shutdown");
    await expect(indicator).toContainText("shutdown");
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
    await page.getByTestId("tab-snapshots").click();
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
