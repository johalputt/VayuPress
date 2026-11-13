// Shield › Sign-in attempts, as the operator reads it after a stranger
// guessed at the console: the attempt is there, with its account, its door
// and its source, and the source's address is never written whole.
const { test, expect } = require("@playwright/test");

test("a wrong password at the console shows on Sign-in attempts, its address masked", async ({ page, browser }) => {
  const account = `guess-${Date.now()}@example.com`;
  const stranger = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const guess = await stranger.newPage();
  await guess.goto("/os/login");
  await guess.fill("input[name=email]", account);
  await guess.fill("input[name=password]", "not-the-password");
  await Promise.all([guess.waitForLoadState("load"), guess.click("form.login-form button[type=submit]")]);
  await expect(guess.locator("body")).toContainText("Invalid email or password");
  await stranger.close();

  // Attempts are written every ten seconds; the page reads what is written.
  await expect(async () => {
    await page.goto("/os/shield/sign-ins?days=1");
    await expect(page.locator("main")).toContainText(account, { timeout: 1000 });
  }).toPass({ timeout: 30000 });
  const main = await page.locator("main").innerText();
  expect(main).toContain("The console");
  expect(main).toMatch(/\b\d+\.\d+\.x\.x\b/);
  expect(main).not.toMatch(/\b127\.0\.0\.1\b/);
});

// A failed sign-in from this browser comes from the operator's own address, so its Block menu offers
// no block that would stop the operator's sign-in and says why; each choice
// it does offer has its note under its label, never over it. The lower
// sections fold.
test("the Block menu offers only what it would set, each note under its label", async ({ page, browser }) => {
  const account = `menu-${Date.now()}@example.com`;
  const stranger = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const guess = await stranger.newPage();
  await guess.goto("/os/login");
  await guess.fill("input[name=email]", account);
  await guess.fill("input[name=password]", "not-the-password");
  await Promise.all([guess.waitForLoadState("load"), guess.click("form.login-form button[type=submit]")]);
  await stranger.close();
  await expect(async () => {
    await page.goto("/os/shield/sign-ins?days=1");
    await expect(page.locator("main")).toContainText(account, { timeout: 1000 });
  }).toPass({ timeout: 30000 });
  const row = page.locator("main .table tbody tr", { hasText: "127.0.x.x" }).first();
  await row.locator("details.sa-pop > summary").click();
  const menu = row.locator("details.sa-pop[open] .sa-menu");
  await expect(menu).toContainText("This is your own address");
  await expect(menu.locator(".sa-menu__item", { hasText: "This address, for a day" })).toHaveCount(0);
  const items = menu.locator(".sa-menu__item");
  expect(await items.count()).toBeGreaterThan(0);
  for (const box of await items.evaluateAll((els) => els.map((el) => {
    const label = el.firstElementChild.getBoundingClientRect();
    const note = el.querySelector(".sa-menu__meta");
    const item = el.getBoundingClientRect();
    return { label, note: note && note.getBoundingClientRect(), item };
  }))) {
    expect(box.label.bottom).toBeLessThanOrEqual(box.item.bottom + 0.5);
    if (box.note) {
      expect(box.note.top).toBeGreaterThanOrEqual(box.label.bottom - 0.5);
      expect(box.note.bottom).toBeLessThanOrEqual(box.item.bottom + 0.5);
    }
  }
  await row.locator("details.sa-pop > summary").click();
  const hours = page.locator("details.fold", { hasText: "By hour" });
  await expect(hours).not.toHaveAttribute("open", "");
  await hours.locator("summary").click();
  await expect(hours).toHaveAttribute("open", "");
});
