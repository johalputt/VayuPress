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
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // The console crossfades a same-origin navigation (@view-transition,
        // ADR-0136). Driven by Playwright 1.61, Chromium 149 never paints the
        // page that navigation opens: no animation frame runs, so every click
        // there waits for "stable" until the test times out. Without
        // automation the same Chrome paints it at once, so the crossfade stays
        // in the product and only the test browser goes without it. Links that
        // swap in place (HTMX) keep their transitions here.
        launchOptions: { args: ["--disable-features=ViewTransitionOnNavigation"] },
      },
    },
  ],
});
