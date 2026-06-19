// Playwright config for VayuPress E2E.
//
// The server is booted by CI (see .github/workflows/e2e.yml). All requests go
// through the screenshot-proxy, which injects the X-API-Key header so the
// Admin v2 pages are reachable without embedding the key in the tests. The
// public pages are unaffected by the extra header.
//
// BASE_URL defaults to the proxy; override to run against any instance.
const { defineConfig, devices } = require("@playwright/test");

module.exports = defineConfig({
  testDir: ".",
  timeout: 30000,
  expect: { timeout: 10000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["list"]] : "list",
  use: {
    baseURL: process.env.BASE_URL || "http://localhost:8088",
    trace: "on-first-retry",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
});
