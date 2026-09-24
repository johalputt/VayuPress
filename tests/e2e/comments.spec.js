// Web addresses in comments become links — safely. The comments API is stubbed
// with fixed seeds so the real widget, served by the real server, renders them;
// posting a real comment needs a signed-in member, which this run does not have.
// One seed per rule, so each rule can fail on its own.
const { test, expect } = require("@playwright/test");

const ARTICLE_SLUG = process.env.ARTICLE_SLUG || "hello-vayupress";

test("comment links are clickable, credited as visitor links, and never markup", async ({ page }) => {
  const seeds = [
    "Try https://gitcomet.dev.", // trailing full stop belongs to the sentence
    "(see https://example.org/wiki/A_(b)) and (https://example.org/c)", // balanced parens kept, unbalanced dropped
    "javascript:alert(1), data:text/html,x and https://[bad are not links",
    "<b>not bold</b> https://example.org/?q=<img>",
    "", // filled in below with a link to this site itself
  ];
  // The same base URL playwright.config.js serves this run from.
  seeds[4] = "More here: " + new URL(`/${ARTICLE_SLUG}`, process.env.BASE_URL || "http://localhost:8088").href;
  await page.route(`**/api/v1/articles/${ARTICLE_SLUG}/comments*`, (route) => {
    if (route.request().method() !== "GET") return route.continue();
    return route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ comments: seeds.map((body, i) => ({
        id: "c" + i, author: "Reader " + i, body, created_at: "2026-09-01T10:00:00Z",
      })) }),
    });
  });
  await page.goto(`/${ARTICLE_SLUG}`);
  const bodies = page.locator(".vayu-comment-body");
  await expect(bodies).toHaveCount(seeds.length);

  const first = bodies.nth(0).locator("a");
  await expect(first).toHaveAttribute("href", "https://gitcomet.dev/");
  await expect(first).toHaveText("https://gitcomet.dev");
  await expect(first).toHaveAttribute("rel", "ugc nofollow noopener");
  await expect(first).toHaveAttribute("target", "_blank");

  const parens = bodies.nth(1).locator("a");
  await expect(parens).toHaveCount(2);
  await expect(parens.nth(0)).toHaveText("https://example.org/wiki/A_(b)");
  await expect(parens.nth(1)).toHaveText("https://example.org/c");

  await expect(bodies.nth(2).locator("a")).toHaveCount(0);

  await expect(bodies.nth(3).locator("b, img")).toHaveCount(0);
  await expect(bodies.nth(3)).toContainText("<b>not bold</b>");

  // A link to this site is an internal link: same tab, followed.
  const own = bodies.nth(4).locator("a");
  await expect(own).toHaveCount(1);
  expect(await own.getAttribute("rel")).toBeNull();
  expect(await own.getAttribute("target")).toBeNull();
});
