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

// A block or lift that worked redraws the page's content in place: the page is
// not reloaded (a value set on the window outlives it) and nothing says the
// write went wrong. It used to reload the whole page, and before that to say
// "That did not go through (200)" while it did.
test("a block that worked redraws the page in place and says nothing went wrong", async ({ page }) => {
  await page.goto("/os/shield/sign-ins?days=1");
  await page.evaluate(() => {
    window.vpKept = true;
    window.vpToasts = [];
    const say = window.vpToast;
    window.vpToast = (msg, kind) => {
      window.vpToasts.push(kind + ": " + msg);
      return say(msg, kind);
    };
    const mark = document.createElement("i");
    mark.id = "vp-before-redraw";
    document.getElementById("main-content").appendChild(mark);
  });
  await page.evaluate(() => window.htmx.ajax("POST", "/os/api/shield/sign-ins/lift?id=999999999", { target: "body", swap: "none" }));
  await expect(page.locator("#vp-before-redraw")).toHaveCount(0);
  await expect(page.locator("#main-content h1")).toContainText("Sign-in attempts");
  expect(await page.evaluate(() => window.vpKept === true)).toBe(true);
  expect(await page.evaluate(() => window.vpToasts)).toEqual([]);
});

// A sign-in link for an address that is not a member yet is mailed only for
// a proof of work, which the site's own form does in the browser. Sign-in
// attempts says which request was sent and which was not: the browser's, and
// a script's posting the same form without the proof.
test("a stranger's sign-in link is sent for the form's proof of work, and not for a bare post", async ({ page, browser }) => {
  const reader = `new-${Date.now()}@example.com`;
  const bot = `bot-${Date.now()}@example.com`;
  const stranger = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  const visit = await stranger.newPage();
  await visit.goto("/signup");
  await visit.fill("#su-email", reader);
  const [sent] = await Promise.all([
    visit.waitForRequest((r) => r.method() === "POST" && new URL(r.url()).pathname === "/members/login"),
    visit.click(".su-btn"),
  ]);
  const form = new URLSearchParams(sent.postData() || "");
  expect(form.get("email")).toBe(reader);
  expect(form.get("nonce"), "the form was sent with its proof").toMatch(/^\d+$/);
  expect(JSON.parse(form.get("proof") || "{}").sig, "the proof is the challenge the install signed").toBeTruthy();
  const bare = await stranger.request.post("/members/login", { form: { email: bot }, maxRedirects: 0 });
  expect(bare.status(), "a refused post is answered as a sent one").toBe(303);
  await stranger.close();

  // Links are not failures: they have a section of their own, and the
  // source's attempts name each address with what became of it.
  const links = page.locator("section", { has: page.locator("h2", { hasText: "Sign-in links" }) });
  await expect(async () => {
    await page.goto("/os/shield/sign-ins?days=1");
    await expect(links.locator(".section-head__hint")).toContainText("not sent", { timeout: 1000 });
  }).toPass({ timeout: 30000 });
  await Promise.all([page.waitForURL(/source=/), links.locator("tbody a").first().click()]);
  await expect(page.locator("tr", { hasText: reader })).toContainText("Asked for a sign-in link");
  await expect(page.locator("tr", { hasText: reader })).not.toContainText("not sent");
  await expect(page.locator("tr", { hasText: bot })).toContainText("Asked for a sign-in link, not sent");
});

// On a phone the Sign-in links table folds into cards, and a card puts its
// Block button at the left: the menu opens from it inside the screen, whole,
// and over the cards below rather than under or beside them.
test("a source's Block menu opens whole on a phone", async ({ page, browser }) => {
  const stranger = await browser.newContext({ storageState: { cookies: [], origins: [] } });
  await stranger.request.post("/members/login", { form: { email: `phone-${Date.now()}@example.com` }, maxRedirects: 0 });
  await stranger.close();
  await page.setViewportSize({ width: 390, height: 844 });
  const links = page.locator("section", { has: page.locator("h2", { hasText: "Sign-in links" }) });
  await expect(async () => {
    await page.goto("/os/shield/sign-ins?days=1");
    await expect(links.locator("details.sa-pop").first()).toBeVisible({ timeout: 1000 });
  }).toPass({ timeout: 30000 });
  const menu = links.locator("details.sa-pop").first();
  await menu.locator("summary").click();
  const panel = menu.locator(".sa-pop__panel");
  await expect(panel).toBeVisible();
  const box = await panel.boundingBox();
  expect(box.x, "the menu starts inside the screen").toBeGreaterThanOrEqual(0);
  expect(box.x + box.width, "the menu ends inside the screen").toBeLessThanOrEqual(390);
  // The menu is what a tap at its first choice lands on: nothing clips it
  // and no card is drawn over it.
  const item = panel.locator(".sa-menu__item").first();
  const at = await item.boundingBox();
  const hit = await page.evaluate(([x, y]) => {
    const el = document.elementFromPoint(x, y);
    return !!(el && el.closest(".sa-pop__panel"));
  }, [at.x + at.width / 2, at.y + at.height / 2]);
  expect(hit, "a tap on the first choice lands on the menu").toBe(true);
});

// Observe-only turns every gate into a counter, so saving it on is asked
// about first, in the console's dialog; answered no, nothing is saved and it
// stays off. (It is never answered yes here: the install is shared with the
// specs running beside this one.)
test("saving observe-only on asks first, and no leaves it off", async ({ page }) => {
  await page.goto("/os/shield");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const observe = page.locator("#sh_observe");
  await expect(observe).not.toBeChecked();
  let saved = false;
  page.on("request", (r) => { if (r.method() === "POST" && r.url().includes("/os/api/shield/settings")) saved = true; });
  await observe.check({ force: true });
  await page.getByRole("button", { name: "Save & apply" }).click();
  const ask = page.locator(".vp-confirm");
  await expect(ask).toContainText("Turn on observe-only?");
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(ask).toHaveCount(0);
  expect(saved, "answered no, nothing was saved").toBe(false);
  await page.reload();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(page.locator("#sh_observe")).not.toBeChecked();
});
