const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: "./tests/web",
  testMatch: "**/*.spec.js",
  timeout: 30000,
  retries: 0,
  use: {
    baseURL: "http://localhost:4173",
    headless: true,
    screenshot: "only-on-failure",
  },
  webServer: {
    command: "node tests/web/mock-server.js",
    port: 4173,
    reuseExistingServer: true,
    timeout: 10000,
  },
  reporter: [["list"]],
});
