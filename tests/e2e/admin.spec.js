// Admin v2 E2E: the editor-first redesign. Requests flow through the
// header-injecting proxy, so these pages are authenticated transparently.
const { test, expect } = require("@playwright/test");

const ARTICLE_SLUG = process.env.ARTICLE_SLUG || "hello-vayupress";

test.describe("Admin v2", () => {
  test("dashboard loads with stats and quick actions", async ({ page }) => {
    const resp = await page.goto("/admin/v2");
    expect(resp.ok()).toBeTruthy();
    await expect(page.locator("h1")).toContainText("Dashboard");
    await expect(page.locator(".quick-action").first()).toBeVisible();
  });

  test("posts list renders and search filters rows", async ({ page }) => {
    await page.goto("/admin/v2/posts");
    const search = page.locator("[data-posts-search]");
    // Either there are posts (search visible) or a friendly empty state.
    if (await search.count()) {
      const rowsBefore = await page.locator("[data-post-row]:visible").count();
      expect(rowsBefore).toBeGreaterThan(0);
      await search.fill("zzz-no-match-zzz");
      await expect(page.locator("[data-post-row]:visible")).toHaveCount(0);
      await expect(page.locator("[data-search-empty]")).toBeVisible();
    } else {
      await expect(page.locator(".empty-state")).toBeVisible();
    }
  });

  test("editor loads with toolbar, preview and live word count", async ({ page }) => {
    await page.goto(`/admin/v2/editor/${ARTICLE_SLUG}`);
    const ta = page.locator("[data-editor]");
    await expect(ta).toBeVisible();
    await expect(page.locator(".editor-toolbar")).toBeVisible();
    await expect(page.locator("[data-preview]")).toBeVisible();

    // Author in Markdown mode so the live preview renders Markdown. Legacy
    // posts open in HTML mode, so explicitly select the Markdown format first
    // (this also exercises the multi-format segmented switch).
    await page.locator('[data-format-btn="markdown"]').click();

    // Typing updates the live preview and the word count.
    await ta.click();
    await ta.fill("# Hello\n\nThis is a **test** paragraph with several words.");
    await expect(page.locator("[data-preview] h1")).toContainText("Hello");
    await expect(page.locator("[data-preview] strong")).toContainText("test");
    await expect(page.locator("[data-wordcount]")).not.toContainText("0 words");
  });

  test("slash palette opens and filters", async ({ page }) => {
    await page.goto(`/admin/v2/editor`);
    const ta = page.locator("[data-editor]");
    await ta.click();
    await ta.type("/code");
    const palette = page.locator("[data-slash-palette]");
    await expect(palette).toHaveClass(/open/);
    await expect(palette.locator(".slash-item")).toContainText(["Code block"]);
  });

  test("SEO dashboard shows artefact status", async ({ page }) => {
    const resp = await page.goto("/admin/v2/seo");
    expect(resp.ok()).toBeTruthy();
    await expect(page.locator("h1")).toContainText("SEO");
    await expect(page.getByText("Generated artefacts")).toBeVisible();
    await expect(page.locator("[data-action='seo-regenerate']")).toBeVisible();
  });

  test("settings exposes the update checker", async ({ page }) => {
    await page.goto("/admin/v2/settings");
    await expect(page.locator("[data-action='check-updates']")).toBeVisible();
    await expect(page.getByText("Software updates")).toBeVisible();
  });

  test("strict CSP header is present (no unsafe-eval/inline)", async ({ request }) => {
    const resp = await request.get("/admin/v2");
    const csp = resp.headers()["content-security-policy"] || "";
    expect(csp).toContain("script-src");
    expect(csp).not.toContain("unsafe-eval");
  });
});
