// Playwright config for real E2E GUI tests (against a live vmsmith daemon).
//
// Usage:
//   npx playwright test --config tests/e2e/playwright.config.js

const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: ".",
  testMatch: "gui-e2e.spec.js",
  timeout: 300_000, // 5 min — real VMs take time
  retries: 0,
  workers: 1, // serial — tests depend on each other
  use: {
    baseURL: process.env.VMSMITH_GUI_URL || "http://localhost:8080",
    headless: true,
    screenshot: "only-on-failure",
    trace: "on-first-retry",
    actionTimeout: 30_000,
  },
  reporter: [["list"]],
});
