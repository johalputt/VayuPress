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

// The same file once closed its wrapper too EARLY: the j/k/x/Enter/n keyboard
// layer sat after the closing line and called a helper that only exists inside
// it, so every one of those keys threw on every console page and did nothing.
test("the list keyboard shortcuts run", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/os/posts");
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("j");
  await page.keyboard.press("k");
  expect(errors).toEqual([]);
});

// hx-confirm prompts go through the console's own dialog. HTMX falls back to the
// browser's native confirm() unless its htmx:confirm event is answered, and for
// a long time nothing answered it. Cancelled here, so the test changes nothing.
test("an hx-confirm asks in the console's dialog, not the browser's", async ({ page }) => {
  const errors = [];
  let native = 0;
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => { native++; d.dismiss(); });
  await page.goto("/os/shield");
  await page.waitForLoadState("networkidle"); // HTMX must have processed the button
  // It lives in a collapsed advanced row; what is under test is the prompt, so
  // the click is dispatched to it directly.
  await page.locator("button", { hasText: "Release all sentences now" }).dispatchEvent("click");
  await expect(page.locator(".vp-confirm")).toBeVisible();
  await page.locator(".vp-confirm").getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator(".vp-confirm")).toHaveCount(0);
  expect(native).toBe(0);
  expect(errors).toEqual([]);
});
