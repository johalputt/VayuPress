// The contact widget's honeypot. A submission with it filled is discarded as
// spam while the sender is told it was sent, so a honeypot a visitor can see
// loses real messages without a trace. It was visible twice, each time on a
// page whose stylesheet lacked the rule that hid it; the widget now hides it
// itself. This mounts the widget on a page with every stylesheet removed and
// measures where the field is.
const { test, expect } = require("@playwright/test");

test("the contact honeypot is out of sight on a page with no stylesheet", async ({ page }) => {
  await page.goto("/");
  await page.evaluate(() => {
    document.querySelectorAll('link[rel="stylesheet"], style').forEach((n) => n.remove());
    document.body.innerHTML = '<div id="vayu-contact"></div>';
    const s = document.createElement("script");
    s.src = "/static/js/contact.js";
    document.body.appendChild(s);
  });
  const hp = page.locator('#vayu-contact input[name="website"]');
  await expect(hp).toHaveCount(1);
  const box = await hp.boundingBox();
  const seen = await page.evaluate(() => {
    const r = document.querySelector('#vayu-contact input[name="website"]').getBoundingClientRect();
    return r.right > 0 && r.bottom > 0 && r.width > 1 && r.height > 1;
  });
  expect(seen, `the honeypot is on screen at ${JSON.stringify(box)}`).toBe(false);
  // The fields a visitor fills are still there and visible.
  await expect(page.locator("#vayu-contact textarea")).toBeVisible();
});
