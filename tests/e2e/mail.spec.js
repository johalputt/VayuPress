// @ts-check
// Mail with mail running (Mail plan §10). Runs against the second install the
// boot starts with Mail on and scripts/mail-fixture's mailboxes: the first
// install is "localhost", for which Mail is off.
const { test, expect } = require("@playwright/test");

const base = process.env.MAIL_BASE_URL;
if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
test.use({ baseURL: base });

const inbox = "/os/vayumail/inbox?user=ankush";
async function openInbox(page, path = inbox) {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(path);
  await expect(page.locator("#vm-inbox-list .mx-row").first()).toBeVisible();
}

// The list is rows under date groups, pinned first; a conversation is one row
// with its size; each row says what the message is before it is opened.
test("the list is grouped rows with previews and marks", async ({ page }) => {
  await openInbox(page);
  const groups = page.locator("#vm-inbox-list .mx-group");
  await expect(groups.first()).toHaveText("Pinned");
  await expect(groups.nth(1)).toHaveText("Today");
  const thread = page.locator(".mx-row", { hasText: "Re: the migration window on Saturday" });
  await expect(thread).toHaveCount(1);
  await expect(thread.locator(".mx-thread")).toHaveText(/^3/); // with my reply, in Sent
  await expect(thread).toHaveClass(/is-unread/);
  await expect(thread.locator(".mx-mark")).toHaveCount(1); // the attachment
  await expect(page.locator(".mx-row", { hasText: "Keys for the backup bucket" }).locator(".mx-row__pv")).toHaveText("Encrypted message");
  await expect(page.locator(".mx-row", { hasText: "Release v3.17.94" }).locator(".mx-row__pv")).toHaveText(/^The release workflow completed: 14 assets signed/);
});

// A view filters the folder in place, and choosing it again shows the folder.
test("a view filters the folder, and lets go of it", async ({ page }) => {
  await openInbox(page);
  const all = await page.locator("#vm-inbox-list .mx-row").count();
  await page.locator('#vm-folders a[href*="view=unread"]').click();
  await expect(page.locator(".mx-head__title")).toContainText("Unread");
  const rows = page.locator("#vm-inbox-list .mx-row");
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeLessThan(all);
  expect(await page.locator("#vm-inbox-list .mx-row:not(.is-unread)").count()).toBe(0);
  await page.locator('#vm-folders a[aria-current="true"]').click();
  await expect(page.locator(".mx-head__title")).toHaveText("Inbox");
});

// Each column scrolls on its own and the page never does (plan §2.5).
test("the columns scroll alone and the page never does", async ({ page }) => {
  await openInbox(page);
  const list = page.locator("#vm-inbox-list");
  await list.hover();
  await page.mouse.wheel(0, 4000);
  await expect.poll(() => list.evaluate((e) => e.scrollTop)).toBeGreaterThan(500);
  expect(await page.evaluate(() => document.scrollingElement.scrollTop)).toBe(0);
  expect(await page.locator("#vm-readpane").evaluate((e) => e.scrollTop)).toBe(0);
});

// Opening a message keeps the list where it was (plan §8, item 5): j opens the
// row on screen, it turns selected in place, and the list did not move.
test("opening a message keeps the list where it was", async ({ page }) => {
  await openInbox(page);
  const list = page.locator("#vm-inbox-list");
  await list.evaluate((e) => { e.scrollTop = 2400; });
  const before = await list.evaluate((e) => e.scrollTop);
  // The list itself, kept to see whether opening redraws it.
  await page.evaluate(() => { window.__mxList = document.querySelector("#vm-inbox-list .mx-list"); });
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("j");
  await expect(page.locator("#vm-readpane .vm-reader")).toBeVisible();
  await page.waitForLoadState("networkidle"); // whatever opening sets off has landed
  const active = page.locator("#vm-inbox-list .mx-row.vm-active");
  await expect(active).toHaveCount(1);
  expect(await list.evaluate((e) => e.scrollTop)).toBe(before);
  await expect(active).toBeInViewport();
  await page.keyboard.press("j");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("#vm-inbox-list .mx-row.vm-active")).toHaveCount(1);
  expect(await list.evaluate((e) => e.scrollTop)).toBe(before);
  expect(await page.evaluate(() => document.contains(window.__mxList)), "opening a message redrew the list").toBe(true);
});

// Nothing clips an address (plan §2.6): it wraps instead, in the account block
// and in a row, for the longest the fixture holds.
test("no address is clipped", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const path of ["/os/vayumail/inbox?user=a-very-long-mailbox-name-for-the-wrapping-test", inbox]) {
    await page.goto(path);
    await expect(page.locator(".mx-account__addr")).toBeVisible();
    const clipped = await page.evaluate(() => [...document.querySelectorAll(".mx-account__addr, .mx-row__who")]
      .filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent));
    expect(clipped, path).toEqual([]);
  }
});

// The newest message is open when Mail opens, and is marked read only after
// two seconds on screen (Mail plan, decision 3).
test("the newest message is open, and read after two seconds", async ({ page }) => {
  await openInbox(page);
  const reader = page.locator("#vm-readpane [data-mx-reader]");
  await expect(reader.locator(".mx-subject")).toHaveText("Re: the migration window on Saturday");
  // What the mailbox itself holds, not only the row's look: unread while it
  // is being glanced at, read once the two seconds are up.
  const stored = () => page.evaluate(async () => {
    const r = await fetch("/os/vayumail/inbox/fragment?user=ankush&folder=Inbox");
    const doc = new DOMParser().parseFromString(await r.text(), "text/html");
    const row = [...doc.querySelectorAll(".mx-row")].find((e) => e.textContent.includes("Re: the migration window on Saturday"));
    return row ? (row.classList.contains("is-unread") ? "unread" : "read") : "missing";
  });
  expect(await stored(), "opening the newest message marked it read at once").toBe("unread");
  const row = page.locator(".mx-row.vm-active");
  await expect(row).toHaveCount(1);
  await expect(row).toHaveClass(/is-unread/);
  await expect(row).not.toHaveClass(/is-unread/, { timeout: 5000 });
  expect(await stored()).toBe("read");
});

// The reader holds the whole conversation (their message, my reply in Sent),
// folded above the newest, and a folded line opens in place.
test("the reader shows the conversation, the attachment and the reply field", async ({ page }) => {
  await openInbox(page);
  const reader = page.locator("#vm-readpane [data-mx-reader]");
  await expect(reader.locator(".mx-subline")).toContainText("3 messages");
  const earlier = reader.locator(".mx-earlier");
  await expect(earlier).toHaveCount(2);
  await expect(earlier.nth(1)).toContainText("Ankush Johal");
  await earlier.first().locator("summary").click();
  await expect(earlier.first().locator(".mx-earlier__body")).toContainText("We need to move the database this month.");
  await expect(reader.locator(".mx-file")).toContainText("migration-runbook-v3.pdf");
  await expect(reader.locator(".mx-file__type")).toHaveText("PDF");
  await expect(reader.locator(".mx-dock__field")).toHaveText("Reply to Priya…");
});

// The seal says what was verified: a signed message from someone whose key
// is not on file is signed, and neutral, never green.
test("the seal of a signature that cannot be checked is neutral", async ({ page }) => {
  await openInbox(page);
  await page.locator(".mx-row", { hasText: "Newsletter draft, October" }).locator(".mx-row__open").click();
  const seal = page.locator("#vm-readpane .mx-seal");
  await expect(seal).toHaveText("Signed · no key on file for ananya@iyer.example to check it with");
  await expect(seal).toHaveClass(/mx-seal--plain/);
});

// The next message comes up from below (plan §8, item 5).
test("an older message arrives from below", async ({ page }) => {
  await openInbox(page);
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("j");
  await expect(page.locator("#vm-readpane [data-mx-reader]")).toHaveAttribute("data-mx-from", "older");
});

// The mailbox switcher (Mail plan §4, §8 item 1) grows out of the account
// block, which stays pressed while it is open; its search has the keys at
// once; Enter opens the first match; Escape closes it back into the block.
test("the mailbox switcher opens from the account and switches", async ({ page }) => {
  await openInbox(page);
  const account = page.locator("[data-mx-switch] > summary");
  const panel = page.locator("[data-mx-switch] .mx-switch__panel");
  await page.mouse.move(900, 500);
  const rest = await account.evaluate((e) => getComputedStyle(e).backgroundColor);
  await page.keyboard.press("Control+Shift+M");
  await expect(panel).toBeVisible();
  await expect(page.locator("[data-mx-switch-find]")).toBeFocused();
  expect(await account.evaluate((e) => getComputedStyle(e).backgroundColor), "the account block is not pressed while the switcher is open").not.toBe(rest);
  const origin = await page.evaluate(async () => {
    const panel = document.querySelector("[data-mx-switch] .mx-switch__panel"), trig = document.querySelector("[data-mx-switch] > summary");
    await Promise.all(panel.getAnimations().map((a) => a.finished));
    const pr = panel.getBoundingClientRect(), tr = trig.getBoundingClientRect();
    const [ox, oy] = getComputedStyle(panel).transformOrigin.split(" ").map(parseFloat);
    const x = pr.left + ox, y = pr.top + oy;
    return Math.hypot(Math.max(tr.left - x, 0, x - tr.right), Math.max(tr.top - y, 0, y - tr.bottom));
  });
  expect(origin, "the switcher opens away from the account block").toBeLessThanOrEqual(12);
  // The sidebar is a scroll container; the wider panel must not widen it, or
  // focusing the search scrolls the whole sidebar sideways.
  expect(await page.locator("nav.sa-appside").evaluate((e) => [e.scrollLeft, e.scrollWidth - e.clientWidth]), "the switcher pushed the sidebar sideways").toEqual([0, 0]);

  const rows = panel.locator(".mx-switch__row:visible");
  await expect(rows).toHaveCount(3);
  await expect(panel.locator(".mx-switch__dom")).toHaveText(["mail.test3"]);
  await expect(panel.locator('.mx-switch__row[aria-current="true"]')).toContainText("ankush@mail.test");
  await expect(panel.locator(".mx-switch__row", { hasText: "ankush@mail.test" }).locator(".mx-switch__count")).toHaveText(/^\d+$/);
  await page.keyboard.type("zzz");
  await expect(rows).toHaveCount(0);
  await expect(panel.locator(".mx-switch__none")).toBeVisible();
  // Whether the exit ran at all: it lasts 90 ms, too short to catch by polling.
  await panel.evaluate((p) => {
    window.__mxExit = false;
    new MutationObserver(() => { if (p.classList.contains("is-closing")) window.__mxExit = true; }).observe(p, { attributes: true });
  });
  await page.keyboard.press("Escape");
  await expect(panel).toBeHidden();
  expect(await page.evaluate(() => window.__mxExit), "the switcher vanished instead of closing back into the account block").toBe(true);
  await expect(account).toBeFocused();

  await account.click();
  const find = page.locator("[data-mx-switch-find]");
  await find.fill("PRI");
  await expect(rows).toHaveCount(1);
  await find.press("Enter");
  await expect(page).toHaveURL(/user=priya/);
  await expect(page.locator(".mx-account__addr")).toHaveText("priya@mail.test");
});
