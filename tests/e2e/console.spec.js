// Console pages run their scripts. admin-os.js is one file of init blocks, one
// per page; a duplicated opening line once wrapped every block after the
// dashboard's activity feed inside it, so on every other page they returned
// before running — the Media library sat on its loading tiles forever and its
// uploads did nothing, with no error anywhere. A page whose script never runs
// looks exactly like a page that is still loading; this waits for the result.
const { test, expect } = require("@playwright/test");

test("the media library loads its list", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const listed = page.waitForResponse((r) => r.url().includes("/os/api/media") && r.status() === 200);
  await page.goto("/os/media");
  await listed;
  await expect(page.locator("[data-media-grid] .skeleton")).toHaveCount(0);
  expect(errors).toEqual([]);
});
