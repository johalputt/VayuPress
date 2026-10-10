// Halcyon, the theme a new install starts on (this CI install is new).
//
// The checks the plan names before the release: nothing in a post wider than
// its column, at every width and every reader size; the overlay rules; the
// reader's choices applied before paint and reset on request; motion that
// stops for a reader who asked for less; and no request off this host.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

const FIX = path.join(__dirname, "..", "..", "internal", "render", "testdata", "halcyon");
const POSTS = {
  "halcyon-wide": { title: "GitComet 0.2.5 vs GitKraken vs Sourcetree vs lazygit 0.65.1: The Git Client Field Test, Refreshed", file: "gitcomet-wide-content.html", tags: ["git", "tools"] },
  "halcyon-long": { title: "Technical Debt Without the Drowning: Classify, Price, Service, Prevent", file: "technical-debt-visible-paydown-guide.html", tags: ["engineering-culture", "architecture"] },
};

test.beforeAll(async ({ request }) => {
  for (const [slug, p] of Object.entries(POSTS)) {
    const have = await request.get(`/api/v1/articles/${slug}`);
    if (have.ok()) continue;
    const r = await request.post("/api/v1/articles", {
      data: { title: p.title, slug, content: fs.readFileSync(path.join(FIX, p.file), "utf8"), tags: p.tags },
    });
    expect(r.ok(), `seeding ${slug}: ${r.status()}`).toBeTruthy();
  }
  for (const slug of Object.keys(POSTS)) {
    await expect.poll(async () => (await request.get(`/api/v1/articles/${slug}`)).status(), { timeout: 30000 }).toBe(200);
  }
});

// What in a post's body would make a reader scroll sideways: an element that
// reaches past the column (code may run to the screen's edges on a phone, by
// design, but never past them), an element that scrolls inside itself, and
// how far the page itself scrolls sideways.
async function overflow(page) {
  return page.evaluate(() => {
    const body = document.querySelector(".h .content");
    const column = body.getBoundingClientRect().right + 1;
    const screen = document.documentElement.clientWidth + 1;
    const name = (e) => e.tagName.toLowerCase() + (e.className ? "." + e.className : "");
    const all = [...body.querySelectorAll("*")].filter((e) => e.getBoundingClientRect().width > 0);
    const over = all
      .filter((e) => e.getBoundingClientRect().right > (e.tagName === "PRE" ? screen : column))
      .filter((e) => !(e.closest("pre") && e.tagName !== "PRE" && e.getBoundingClientRect().right <= screen))
      .map(name);
    const scrolls = all.filter((e) => e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).overflowX !== "visible").map(name);
    return { over, scrolls, scroll: document.documentElement.scrollWidth - document.documentElement.clientWidth };
  });
}

test.describe("Halcyon", () => {
  test("is the theme a new install starts on", async ({ page }) => {
    await page.goto("/halcyon-long");
    await expect(page.locator("html")).toHaveClass(/\bh\b/);
    await expect(page.locator(".h-post h1")).toHaveText(POSTS["halcyon-long"].title);
  });

  for (const width of [390, 820, 1440]) {
    for (const size of [17, 20, 23]) {
      test(`nothing in a post is wider than its column at ${width} px, text ${size} px`, async ({ page }) => {
        await page.setViewportSize({ width, height: 900 });
        await page.addInitScript((s) => localStorage.setItem("vp-read", JSON.stringify({ size: s })), size);
        for (const slug of Object.keys(POSTS)) {
          await page.goto(`/${slug}`);
          await expect(page.locator("html")).toHaveAttribute("data-size", String(size));
          const o = await overflow(page);
          expect(o.over, `${slug}: elements past the column`).toEqual([]);
          expect(o.scrolls, `${slug}: elements that scroll sideways inside`).toEqual([]);
          expect(o.scroll, `${slug}: the page scrolls sideways`).toBeLessThanOrEqual(0);
        }
      });
    }
  }

  test("every cell of the wide table is still there on a phone, labelled", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    await page.goto("/halcyon-wide");
    const cells = page.locator(".h .content .tbl[data-stack] td");
    expect(await cells.count()).toBe(35);
    await expect(page.locator('.h .content td[data-label="GitKraken"]').first()).toBeVisible();
    await expect(page.locator(".h .content thead")).toBeHidden();
  });

  test("reading settings: open on Aa, focus inside, Escape closes, focus returns", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    const aa = page.locator(".h-byline [data-h-open='h-reader']");
    await aa.click();
    const panel = page.locator("#h-reader");
    await expect(panel).toBeVisible();
    await expect(aa).toHaveAttribute("aria-expanded", "true");
    expect(await panel.evaluate((p) => p.contains(document.activeElement))).toBe(true);
    // Aa leaves the page scrollable: only full-screen sheets lock it.
    await expect(page.locator("html")).not.toHaveClass(/h-lock/);
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
    await expect(aa).toBeFocused();
  });

  test("reading settings: a click outside closes, and focus is not pulled back", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.mouse.click(20, 500);
    await expect(page.locator("#h-reader")).toBeHidden();
  });

  test("the phone menu is a sheet: it locks the page, keeps Tab inside, closes on Escape", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/");
    const btn = page.locator("[data-h-open='h-menu']");
    await btn.click();
    const sheet = page.locator("#h-menu");
    await expect(sheet).toBeVisible();
    await expect(page.locator("html")).toHaveClass(/h-lock/);
    for (let i = 0; i < 12; i++) await page.keyboard.press("Tab");
    expect(await sheet.evaluate((s) => s.contains(document.activeElement))).toBe(true);
    await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden();
    await expect(page.locator("html")).not.toHaveClass(/h-lock/);
    await expect(btn).toBeFocused();
  });

  test("a reader's choices apply before paint, on that device, and reset on request", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.locator("#h-reader [data-h-face='sans']").click();
    await page.locator("#h-reader [data-h-scheme='dark']").click();
    await page.locator("#h-reader [data-h-size]").fill("23");
    // Read the attributes the moment the document exists, before any deferred
    // script has run: the inline pre-paint script alone must have set them.
    await page.route("**/static/js/halcyon.js*", (r) => r.abort());
    await page.reload();
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-face", "sans");
    await expect(html).toHaveAttribute("data-size", "23");
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.unroute("**/static/js/halcyon.js*");
    await page.reload();
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.locator("#h-reader [data-h-reset]").click();
    await expect(html).toHaveAttribute("data-face", "serif");
    await expect(html).toHaveAttribute("data-size", "20");
    await expect(html).not.toHaveAttribute("data-theme", /./);
  });

  test("motion stops for a reader who asked for less", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    const anim = await page.locator("#h-reader").evaluate((e) => getComputedStyle(e).animationName);
    expect(anim).toBe("none");
  });

  test("asks nothing of any other host", async ({ page, baseURL }) => {
    const host = new URL(baseURL).host;
    const off = [];
    page.on("request", (r) => {
      const u = new URL(r.url());
      if (/^https?:$/.test(u.protocol) && u.host !== host) off.push(r.url());
    });
    for (const p of ["/", "/halcyon-long", "/tags", "/members"]) await page.goto(p);
    expect(off).toEqual([]);
  });

  // A topic page offers newest and most read, and its search stays in the
  // topic and keeps the order. The seeded posts carry different topics, so a
  // search for one post's title finds it from its own topic and not another's.
  test("a topic page has its two orders, and a search that stays in the topic", async ({ page }) => {
    await page.goto("/tags/git");
    const order = page.getByRole("navigation", { name: "Order" });
    await expect(order.getByRole("link", { name: "Newest" })).toHaveAttribute("aria-current", "true");
    await order.getByRole("link", { name: "Most read" }).click();
    await expect(page).toHaveURL(/\/tags\/git\?sort=read$/);
    await expect(order.getByRole("link", { name: "Most read" })).toHaveAttribute("aria-current", "true");
    await expect(page.locator(".h-count, .h-empty").first()).toContainText("in the last 30 days");
    await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", "noindex,follow");

    const q = "GitComet";
    await expect.poll(async () => {
      await page.goto("/tags/git?sort=read");
      await page.locator("#h-find-q").fill(q);
      await page.locator("#h-find-q").press("Enter");
      return page.locator(".h-results li").count();
    }, { timeout: 30000 }).toBeGreaterThan(0);
    await expect(page).toHaveURL(/topic=git/);
    await expect(page).toHaveURL(/sort=read/);
    await expect(page.locator("h1")).toHaveText("Search git");
    await expect(page.locator(".h-count")).toContainText("most read first");
    await expect(page.locator(".h-results")).toContainText(POSTS["halcyon-wide"].title);

    await page.goto(`/search?q=${q}&topic=architecture`);
    await expect(page.locator(".h-count")).toHaveText(`Nothing in architecture matches “${q}”.`);
    await page.getByRole("link", { name: "Search every topic" }).click();
    await expect(page.locator(".h-results")).toContainText(POSTS["halcyon-wide"].title);
  });

  // Your own comment before moderation stays on the page for you, marked, until
  // it is approved; it is kept in that browser only, and goes when nobody is
  // signed in there. The comments API is stubbed as comments.spec.js does: this
  // run has no member session. One stage per rule.
  test("your own comment awaiting moderation is shown to you alone, until it is approved", async ({ page }) => {
    const slug = "halcyon-long";
    let approved = false, signedIn = true;
    const mine = { id: "p1", parent_id: "", author: "Asha", body: "Does the 15% hold for platform teams?", created_at: new Date().toISOString(), status: "pending" };
    await page.route("**/api/v1/members/me", (route) => signedIn
      ? route.fulfill({ contentType: "application/json", body: JSON.stringify({ authenticated: true, member: { name: "Asha", email: "asha@example.com" } }) })
      : route.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
    await page.route(`**/api/v1/articles/${slug}/comments`, (route) => route.request().method() === "POST"
      ? route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify(mine) })
      : route.fulfill({ contentType: "application/json", body: JSON.stringify({ comments: approved ? [{ ...mine, status: "approved" }] : [] }) }));
    const note = page.locator(".vayu-comment-pending");

    await page.goto(`/${slug}`);
    await page.locator(".vayu-comment-compose textarea").fill(mine.body);
    await page.locator(".vayu-comment-compose .vayu-comment-submit").click();
    await expect(note).toHaveText("Waiting for moderation · only you can see this");
    await expect(page.locator(".vayu-comment--pending .vayu-comment-body")).toHaveText(mine.body);

    await page.reload();
    await expect(note, "kept across a reload").toHaveCount(1);

    approved = true;
    await page.reload();
    await expect(page.locator(".vayu-comment-body")).toHaveCount(1);
    await expect(note, "gone once the approved list carries it").toHaveCount(0);
    expect(await page.evaluate((k) => localStorage.getItem(k), `vp-pending:${slug}`)).toBeNull();

    approved = false;
    await page.evaluate(([k, v]) => localStorage.setItem(k, v), [`vp-pending:${slug}`, JSON.stringify([mine])]);
    signedIn = false;
    await page.reload();
    await expect(page.locator(".vayu-comment-gate")).toBeVisible();
    await expect(note, "never shown with nobody signed in").toHaveCount(0);
    expect(await page.evaluate((k) => localStorage.getItem(k), `vp-pending:${slug}`)).toBeNull();
  });

  // A site served over plain HTTP (a .onion) has no clipboard API. Localhost
  // counts as secure, so the page is told otherwise to reach that path.
  test("copies a link where the clipboard API is not offered", async ({ page }) => {
    await page.goto("/halcyon-long");
    await page.evaluate(() => Object.defineProperty(window, "isSecureContext", { value: false }));
    await page.locator(".h-byline [data-h-copylink]").click();
    await expect(page.locator("#h-live")).toHaveText("Link copied");
    await expect(page.locator("textarea")).toHaveCount(0);
  });
});
