// @ts-check
// The member's account page (3cx) on the install with Mail on, as a reader
// who holds a mailbox: signed in to the site with that mailbox, as the account
// panel's "Sign in with VayuMail" does. Membership is off on that install, so
// the rows that open the account panel (Activity's Advertising) are not drawn.
const { test, expect } = require("@playwright/test");

const base = process.env.MAIL_BASE_URL;
if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
test.use({ baseURL: base });
// The tests share one member, whose choices each test changes.
test.describe.configure({ mode: "default" });

// Priya, one of the two mailboxes mail.spec.js and talk.spec.js make (a third
// account would change the mailbox switcher those specs count). Made here too,
// as they do, so whichever runs first finds it made.
const PRIYA = "priya@mail.test", PASS = "e2e-mailbox-pass-priya";

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage({ baseURL: base });
  await page.goto("/os/vayumail/accounts");
  const csrf = (await page.context().cookies()).find((c) => c.name === "vp_csrf");
  const r = await page.request.post("/os/vayumail/accounts/create", {
    headers: { "X-CSRF-Token": csrf ? decodeURIComponent(csrf.value) : "" },
    data: { local: "priya", name: "Priya Raman", pass: PASS, role: "mailbox" },
  });
  if (!r.ok() && !/exist/i.test(await r.text())) throw new Error("could not make the priya account: " + r.status());
  await page.close();
});

// A fresh browser signed in to the site as Priya, and nobody else.
async function asPriya(browser, viewport = { width: 1440, height: 900 }) {
  const ctx = await browser.newContext({ baseURL: base, viewport, storageState: { cookies: [], origins: [] } });
  const page = await ctx.newPage();
  await page.goto("/");
  const r = await page.request.post("/api/v1/members/vayumail-login", { data: { email: PRIYA, password: PASS } });
  expect(r.status(), await r.text()).toBe(200);
  return page;
}

test("the account page is every control, laid open, at any width", async ({ browser }) => {
  for (const width of [1440, 320]) {
    const page = await asPriya(browser, { width, height: 900 });
    await page.goto("/members/account");
    for (const id of ["profile", "plan", "mail", "security", "emails"]) {
      await expect(page.locator(`section#${id}`), `the ${id} section`).toHaveCount(1);
    }
    await expect(page.locator(".acct-more.is-open"), "nothing is open before it is asked for").toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `no sideways scroll at ${width}px`).toBe(true);
    const unnamed = await page.locator("button, a").evaluateAll((els) => els
      .filter((e) => e.offsetParent !== null && !(e.getAttribute("aria-label") || e.textContent || "").trim()).length);
    expect(unnamed, "every visible action has a name").toBe(0);
    await page.context().close();
  }
});

test("an email choice saves when it is flipped", async ({ browser }) => {
  const page = await asPriya(browser);
  await page.goto("/members/account");
  const news = page.locator("input[name=newsletter]");
  const was = await news.isChecked();
  await page.locator("label.acct-switch", { has: news }).click();
  await expect(page.locator("#emails [data-acct-note]")).toHaveText("Saved");
  await page.reload();
  expect(await page.locator("input[name=newsletter]").isChecked(), "the choice outlives a reload").toBe(!was);
  await page.context().close();
});

test("a picture and a name are set in place, and are the mailbox's too", async ({ browser }) => {
  const page = await asPriya(browser);
  await page.goto("/members/account");
  await page.locator("#picture [data-acct-open]").click();
  await expect(page.locator("#picture-more")).toBeVisible();
  // The automatic picture, which Priya already has: the whole chooser runs
  // (choose, save, come back saying so) and her mailbox's picture, which
  // talk.spec.js reads as her initial, stays as it was.
  await expect(page.locator('[data-acct-avatar=""]')).toHaveAttribute("aria-pressed", "true");
  await Promise.all([page.waitForURL(/saved=picture/), page.locator('[data-acct-avatar=""]').click()]);
  await expect(page.locator(".acct-notice")).toContainText("Your picture is saved");
  await expect(page.locator("#picture .acct-row__v")).toContainText("Made for you");

  const rename = async (to) => {
    await page.locator("#name [data-acct-open]").click();
    await page.locator("#acct-name").fill(to);
    await Promise.all([page.waitForURL(/saved=1/), page.locator("#name-more button[type=submit]").click()]);
    await expect(page.locator(".acct-who__name")).toHaveText(to);
  };
  await rename("Priya R.");
  await rename("Priya Raman"); // as the other specs made her
  await page.context().close();
});

test("a mailbox holder opens VayuTalk from their account", async ({ browser }) => {
  const page = await asPriya(browser);
  await page.goto("/members/account");
  const talk = page.locator("#mail a[href='/os/talk']");
  await expect(talk, "the Mail section offers VayuTalk").toHaveCount(1);
  await talk.click();
  await expect(page, "VayuTalk opens, rather than sending a mailbox holder back to the inbox").toHaveURL(/\/os\/talk$/);
  await page.context().close();
});
