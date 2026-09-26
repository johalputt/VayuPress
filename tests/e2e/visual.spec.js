// Still Air as designed: twelve console surfaces in both schemes, compared with
// baselines committed beside this file.
//
// The baselines are rendered by CI's own Chromium, by the e2e workflow run with
// "update_baselines". One made on any other browser build differs on
// anti-aliasing alone and would make this red for nothing. What changes by
// itself — times, counts, the version, the live status — is masked, so a
// difference here is a difference in the design.
const { test, expect } = require("@playwright/test");

// Opens a page and waits until it has drawn: loaded, its fonts in, and the
// network quiet for a moment where a page allows it (Talk keeps a connection
// open, so quiet is bounded rather than awaited).
async function settle(page, path) {
  await page.goto(path);
  await page.waitForLoadState("load");
  await page.waitForLoadState("networkidle", { timeout: 3000 }).catch(() => {});
  await page.evaluate(() => document.fonts.ready);
}

// What moves by itself on a fresh install: clocks, counts, the version, the
// live status, and any sentence that says how long ago something was. Home's
// date, its clock and its install facts (version, uptime, response time)
// change on their own too.
function masks(page) {
  return [
    page.locator("time"),
    page.locator("[data-notif-badge]"),
    page.locator(".sa-status"),
    page.locator(".sa-rail__foot"),
    page.locator(".sa-count"),
    page.locator(".sa-appside__count"),
    page.locator(".sa-home__date"),
    page.locator(".sa-home__state"),
    page.locator('section[aria-labelledby="sa-install"]'),
    page.getByText(/\b\d+ (second|minute|hour|day)s? ago\b/),
    page.getByText(/Now: \d{4}-\d{2}-\d{2} \d{2}:\d{2}/),
  ];
}

const desktop = [
  { name: "home", path: "/os/" },
  { name: "posts", path: "/os/posts" },
  { name: "editor", path: "/os/editor" },
  { name: "media", path: "/os/media" },
  { name: "mail", path: "/os/vayumail" },
  { name: "talk", path: "/os/talk" },
  { name: "settings", path: "/os/settings" },
  { name: "system-state", path: "/os/modes" },
  { name: "outside-services", path: "/os/website/services" },
  {
    name: "command-bar", path: "/os/posts",
    open: async (page) => {
      await page.keyboard.press("Control+k");
      await page.locator(".cmd-input").fill("site");
      await expect(page.locator(".cmd-item--active")).toHaveCount(1);
    },
  },
  {
    name: "bell", path: "/os/posts",
    open: async (page) => {
      await page.locator("[data-notif-toggle]").click();
      await expect(page.locator("[data-notif-panel]")).toBeVisible();
    },
  },
];

// Two boots render these pixel for pixel alike, so the tolerance is only for
// anti-aliasing on a different runner CPU. Playwright's defaults (a per-pixel
// colour threshold of 0.2, and here 1% of pixels) passed a page whose current
// sidebar item had lost its tint: Still Air draws with tints that small.
const shot = { threshold: 0.05, maxDiffPixelRatio: 0.001, animations: "disabled", caret: "hide" };

// The console's service worker takes control on a first visit and reloads the
// page once, which lands in the middle of a screenshot. A picture of the
// design does not need it.
test.use({ serviceWorkers: "block" });

for (const scheme of ["light", "dark"]) {
  test.describe(`desktop, ${scheme}`, () => {
    test.use({ colorScheme: scheme, viewport: { width: 1280, height: 900 }, reducedMotion: "reduce" });
    for (const s of desktop) {
      test(`${s.name} looks as designed`, async ({ page }) => {
        await settle(page, s.path);
        if (s.open) await s.open(page);
        await expect(page).toHaveScreenshot(`${s.name}-${scheme}.png`, { ...shot, mask: masks(page) });
      });
    }
  });
  test.describe(`phone, ${scheme}`, () => {
    test.use({ colorScheme: scheme, viewport: { width: 390, height: 844 }, hasTouch: true, reducedMotion: "reduce" });
    test("home looks as designed", async ({ page }) => {
      await settle(page, "/os/");
      await expect(page).toHaveScreenshot(`phone-home-${scheme}.png`, { ...shot, mask: masks(page) });
    });
  });
}
