// Public-site E2E: homepage and article rendering, plus the sovereignty
// invariant that no third-party origins are contacted.
const { test, expect } = require("@playwright/test");

const ARTICLE_SLUG = process.env.ARTICLE_SLUG || "hello-vayupress";

test.describe("public site", () => {
  test("homepage renders and responds 200", async ({ page }) => {
    const resp = await page.goto("/");
    expect(resp.ok()).toBeTruthy();
    // Body has content (not a blank error page).
    await expect(page.locator("body")).not.toBeEmpty();
  });

  test("article page shows the seeded post", async ({ page }) => {
    const resp = await page.goto(`/${ARTICLE_SLUG}`);
    expect(resp.ok()).toBeTruthy();
    await expect(page.locator("body")).toContainText("VayuPress");
  });

  test("no third-party origins are requested (sovereignty)", async ({ page }) => {
    const offHost = [];
    page.on("request", (req) => {
      const url = req.url();
      // Ignore inline/embedded schemes — only real network hosts matter.
      if (!/^https?:\/\//i.test(url)) return;
      const u = new URL(url);
      if (u.hostname !== "localhost" && u.hostname !== "127.0.0.1") {
        offHost.push(url);
      }
    });
    await page.goto("/");
    await page.waitForLoadState("networkidle");
    expect(offHost, `unexpected off-host requests:\n${offHost.join("\n")}`).toEqual([]);
  });

  test("sitemap and feed are served", async ({ request }) => {
    const sm = await request.get("/sitemap.xml");
    expect(sm.ok()).toBeTruthy();
    const feed = await request.get("/feed.xml");
    expect(feed.ok()).toBeTruthy();
  });
});
