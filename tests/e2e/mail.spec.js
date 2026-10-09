// @ts-check
// Mail with mail running (Mail plan §10). Runs against the second install the
// boot starts with Mail on and scripts/mail-fixture's mailboxes: the first
// install is "localhost", for which Mail is off.
const { test, expect } = require("@playwright/test");

const base = process.env.MAIL_BASE_URL;
if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
test.use({ baseURL: base });
// One mailbox serves every test here, and they change it: a message opened
// is read two seconds later, others are archived, snoozed or sent. Run in
// parallel (the config's fullyParallel), one test's reader marked another's
// message read under it. In order, each test meets the state it expects;
// "default" rather than "serial", so one failure does not hide the rest.
test.describe.configure({ mode: "default" });

// The fixture writes mailboxes as Maildirs; an install also holds them as
// accounts, with a name and a PGP key each (provisionMailbox). Made once for
// this file, through the console's own endpoint, so compose has senders and
// a recipient with a key on file. A second run finds them already made.
test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage({ baseURL: base });
  await page.goto("/os/vayumail/accounts");
  const csrf = (await page.context().cookies()).find((c) => c.name === "vp_csrf");
  for (const [local, name] of [["ankush", "Ankush Johal"], ["priya", "Priya Raman"]]) {
    const r = await page.request.post("/os/vayumail/accounts/create", {
      headers: { "X-CSRF-Token": csrf ? decodeURIComponent(csrf.value) : "" },
      data: { local, name, pass: "e2e-mailbox-pass-" + local, role: "mailbox" },
    });
    if (!r.ok() && !/exist/i.test(await r.text())) throw new Error("could not make the " + local + " account: " + r.status());
  }
  await page.close();
});

const inbox = "/os/vayumail/inbox?user=ankush";
async function openInbox(page, path = inbox) {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(path);
  await expect(page.locator("#vm-inbox-list .mx-row").first()).toBeVisible();
}

// The newest message as the next test needs it, unread, whatever the tests
// before it did: each one leaves the shared mailbox as it found it or not.
async function newestUnread(page, path = inbox) {
  await openInbox(page, path);
  const id = await page.locator("#vm-readpane [data-mx-reader]").evaluate((r) => new URL(r.getAttribute("data-mx-href"), location.href).searchParams.get("id"));
  const csrf = (await page.context().cookies()).find((c) => c.name === "vp_csrf");
  await page.request.post("/os/vayumail/inbox/action", {
    headers: { "X-CSRF-Token": csrf ? decodeURIComponent(csrf.value) : "" },
    form: { action: "mark", mark: "unread", user: "ankush", folder: "Inbox", id },
  });
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
  await expect(page.locator(".mx-head__main .mx-head__title")).toContainText("Unread");
  const rows = page.locator("#vm-inbox-list .mx-row");
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeLessThan(all);
  expect(await page.locator("#vm-inbox-list .mx-row:not(.is-unread)").count()).toBe(0);
  await page.locator('#vm-folders a[aria-current="true"]').click();
  await expect(page.locator(".mx-head__main .mx-head__title")).toHaveText("Inbox");
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
  await newestUnread(page);
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
  await expect(reader.locator(".mx-dock__field")).toHaveAttribute("placeholder", "Reply to Priya…");
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
  // The pressed colour fades in (a colour transition), so it is polled for.
  await expect.poll(() => account.evaluate((e) => getComputedStyle(e).backgroundColor), { message: "the account block is not pressed while the switcher is open" }).not.toBe(rest);
  const origin = await page.evaluate(async () => {
    const panel = document.querySelector("[data-mx-switch] .mx-switch__panel"), trig = document.querySelector("[data-mx-switch] > summary");
    // The shell restarts the entrance from the control as the menu opens, which
    // cancels the one already running: wait until none is left running.
    for (let i = 0; i < 3 && panel.getAnimations().length; i++) await Promise.all(panel.getAnimations().map((a) => a.finished.catch(() => {})));
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

// Compose is a sheet over the reader (Mail plan §5, §8 item 4): Reply raises
// it from the toolbar with the quote folded away, the list beside it stays
// live, and the mailbox answering is the sender.
test("reply opens the compose sheet over the reader", async ({ page }) => {
  await openInbox(page);
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  const reply = page.locator('#vm-readpane .mx-rtools [aria-label="Reply"]');
  await reply.click();
  const sheet = page.locator("[data-mx-compose-host] .mx-compose");
  await expect(sheet.locator("[data-c-title]")).toHaveText("Re: the migration window on Saturday");
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox/);
  await expect(sheet.locator("[data-c-body]")).toHaveValue("");
  await expect(sheet.locator(".mx-quoted > summary")).toHaveText("Show the quoted message");
  await expect(sheet.locator("[data-c-from]")).toHaveValue("ankush@mail.test");
  const origin = await page.evaluate(async () => {
    const panel = document.querySelector("[data-mx-compose-host] .mx-compose"), trig = document.querySelector('#vm-readpane .mx-rtools [aria-label="Reply"]');
    // The shell restarts the entrance from the control as the menu opens, which
    // cancels the one already running: wait until none is left running.
    for (let i = 0; i < 3 && panel.getAnimations().length; i++) await Promise.all(panel.getAnimations().map((a) => a.finished.catch(() => {})));
    const pr = panel.getBoundingClientRect(), tr = trig.getBoundingClientRect();
    const [ox, oy] = getComputedStyle(panel).transformOrigin.split(" ").map(parseFloat);
    const x = pr.left + ox, y = pr.top + oy;
    return Math.hypot(Math.max(tr.left - x, 0, x - tr.right), Math.max(tr.top - y, 0, y - tr.bottom));
  });
  expect(origin, "the sheet does not rise from the Reply that opened it").toBeLessThanOrEqual(12);
  // Only the reader is under the sheet: a row in the list is still what is
  // under the pointer there.
  const row = page.locator("#vm-inbox-list .mx-row").nth(2);
  const box = await row.boundingBox();
  expect(await page.evaluate(([x, y]) => !!document.elementFromPoint(x, y).closest("#vm-inbox-list"), [box.x + box.width / 2, box.y + box.height / 2])).toBe(true);
});

// The line beside Send says what the send will do, per recipient: encrypted
// only while every one of them has a key, and readable when the sender turns
// it off. Keys typed in the composer never reach the list behind it.
test("the compose line says whether the message goes encrypted", async ({ page }) => {
  await openInbox(page);
  const rows = await page.locator("#vm-inbox-list .mx-row").count();
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("c");
  const sheet = page.locator("[data-mx-compose-host] .mx-compose");
  const to = sheet.locator('[data-c-chips="to"] input');
  await expect(to).toBeFocused();
  await to.fill("priya@mail.test");
  await to.press("Enter");
  const line = sheet.locator("[data-c-enc]");
  await expect(line).toHaveText("Encrypted for priya", { timeout: 15000 });
  await expect(line).toHaveClass(/mx-compose__enc--ok/);
  await expect(sheet.locator(".vm-chip").first().locator(".vm-chip__lock")).toHaveCount(1);
  await to.fill("nobody@mail.test"); // a valid address with no key on file
  await to.press("Enter");
  await expect(line).toHaveText("Sent as readable text: no key for nobody", { timeout: 15000 });
  await expect(sheet.locator(".vm-chip").nth(1).locator(".vm-chip__lock")).toHaveCount(0);
  await line.click();
  await expect(line).toHaveText("Sent as readable text: encryption is off");
  // Nothing clips an address (plan §2.6), a recipient's included.
  await to.fill("a-very-long-mailbox-name-for-the-wrapping-test@mail.test");
  await to.press("Enter");
  await expect(sheet.locator(".vm-chip")).toHaveCount(3);
  const clipped = await page.evaluate(() => [...document.querySelectorAll(".mx-compose .vm-chip > span:not(.vm-av):not(.vm-chip__lock)")]
    .filter((e) => e.scrollWidth > e.clientWidth + 1).map((e) => e.textContent));
  expect(clipped).toEqual([]);
  await sheet.locator("[data-c-body]").click();
  await page.keyboard.type("e#jk x");
  await expect(page.locator("#vm-inbox-list .mx-row")).toHaveCount(rows);
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox/);
  // A button in the composer holds focus too; j there must not walk the list.
  const open = await page.locator("#vm-inbox-list .mx-row.vm-active").textContent();
  await sheet.locator("[data-c-format]").focus();
  await page.keyboard.press("j");
  await page.waitForLoadState("networkidle");
  expect(await page.locator("#vm-inbox-list .mx-row.vm-active").textContent(), "j in the composer opened another message").toBe(open);
});

// Escape minimises the sheet to a bar at the reader's foot and the list is
// in reach; the bar opens it again; closing keeps what was written in Drafts.
// On a phone, compose's More options opens wholly inside the sheet, which
// clips it: its right edge is the panel's own, not the sheet's edge cutting
// through it.
test("on a phone, compose's More options is not cut off by the sheet", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/os/vayumail/compose?user=ankush");
  await page.locator('.mx-compose__more > summary').click();
  const panel = page.locator(".mx-compose__more .sa-pop__panel");
  await expect(panel).toBeVisible();
  await page.waitForTimeout(400);
  const edge = await panel.evaluate((p) => {
    const r = p.getBoundingClientRect(), y = r.top + r.height / 2;
    return { left: p.contains(document.elementFromPoint(r.left + 2, y)), right: p.contains(document.elementFromPoint(r.right - 2, y)) };
  });
  expect(edge, "the panel is cut off at an edge").toEqual({ left: true, right: true });
});

test("the compose sheet minimises, and closing keeps a draft", async ({ page }) => {
  await openInbox(page);
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("c");
  const host = page.locator("[data-mx-compose-host]");
  const subject = "Draft kept on close " + Date.now();
  await host.locator("[data-c-subject]").fill(subject);
  await expect(host.locator("[data-c-title]")).toHaveText(subject);
  await page.keyboard.press("Escape");
  await expect(host).toHaveClass(/is-min/);
  await expect(host.locator("[data-c-body]")).toBeHidden();
  await host.locator("[data-c-title]").click();
  await expect(host).not.toHaveClass(/is-min/);
  await host.locator("[data-c-close]").click();
  await expect(host).toBeHidden();
  const drafts = await page.evaluate(async () => (await fetch("/os/vayumail/inbox/fragment?user=ankush&folder=Drafts")).text());
  expect(drafts, "closing the sheet lost what was written").toContain(subject);
});

// A message sent from the sheet goes, after the hold for Undo, and the sheet
// lets go of the mailbox where it was.
test("a message sent from the sheet arrives", async ({ page }) => {
  test.setTimeout(60000);
  await openInbox(page);
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("c");
  const host = page.locator("[data-mx-compose-host]");
  const subject = "Sent from the sheet " + Date.now();
  const to = host.locator('[data-c-chips="to"] input');
  await to.fill("priya@mail.test");
  await to.press("Enter");
  await host.locator("[data-c-subject]").fill(subject);
  await host.locator("[data-c-body]").fill("Saturday works.");
  await host.locator("[data-c-body]").press("Control+Enter");
  await expect(host.locator("[data-c-undobar]")).toBeVisible();
  await expect(host).toBeHidden({ timeout: 20000 });
  await expect.poll(async () => page.evaluate(async () => (await fetch("/os/vayumail/inbox/fragment?user=priya&folder=Inbox")).text()), { timeout: 20000 }).toContain(subject);
});

// Send later holds the message under Scheduled until its time, sends
// nothing yet, and Cancel brings it back to Drafts. In Priya's mailbox, so no
// other spec's sidebar shows a Scheduled it did not make.
test("a message sent later waits under Scheduled, and Cancel returns it to Drafts", async ({ page }) => {
  test.setTimeout(60000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/inbox?user=priya");
  await expect(page.locator("#vm-inbox-list .mx-head")).toBeVisible();
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("c");
  const host = page.locator("[data-mx-compose-host]");
  const subject = "Sent later " + Date.now();
  const to = host.locator('[data-c-chips="to"] input');
  await to.fill("ankush@mail.test");
  await to.press("Enter");
  await host.locator("[data-c-subject]").fill(subject);
  await host.locator("[data-c-body]").fill("For the morning.");
  await host.locator("[data-c-later] summary").click();
  const morning = host.locator('[data-c-later-preset="morning"]');
  await expect(morning).toContainText("Tomorrow morning");
  await expect(morning.locator(".mx-compose__at")).not.toBeEmpty();
  await morning.click();
  await expect(host).toBeHidden({ timeout: 20000 });
  const fragment = (folder) => page.evaluate(async (f) => (await fetch("/os/vayumail/inbox/fragment?user=priya&folder=" + f)).text(), folder);
  expect(await fragment("Sent"), "a message held for later was filed as sent").not.toContain(subject);
  const scheduled = page.locator('#vm-folders a[href*="folder=Scheduled"]');
  await expect(scheduled).toBeVisible();
  await scheduled.click();
  const row = page.locator("#vm-inbox-list .mx-row--held", { hasText: subject });
  await expect(row).toContainText("Sends ");
  await row.getByRole("button", { name: "Cancel" }).click();
  await expect(row).toHaveCount(0);
  await expect.poll(() => fragment("Drafts"), { timeout: 10000 }).toContain(subject);
});

// A template is saved from what is being written, under a name; Enter in the
// name saves it and does not send the message (left to the form it would).
// The next message takes its subject and text, and Delete asks first.
// Priya's mailbox, so no other spec sees the template.
test("a template is saved from compose and put into the next message", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/compose?user=priya");
  const c = page.locator(".mx-compose--page");
  const name = "Thanks " + Date.now();
  const to = c.locator('[data-c-chips="to"] input');
  await to.fill("ankush@mail.test");
  await to.press("Enter");
  await c.locator("[data-c-subject]").fill("Thank you");
  await c.locator("[data-c-body]").fill("Thanks for this, it helps.");
  // The list read as the menu opens answers after the save, as it did on
  // CI's runner, where it took the save's note away.
  await page.route("**/os/vayumail/templates?*", async (route) => {
    await new Promise((r) => setTimeout(r, 1500));
    await route.continue();
  });
  await c.locator("[data-c-tpl] summary").click();
  await c.locator("[data-c-tpl-name]").fill(name);
  await c.locator("[data-c-tpl-name]").press("Enter");
  await expect(c.locator("[data-c-tpl-note]")).toHaveText("Saved as “" + name + "”.");
  await expect(c.locator("[data-c-undobar]"), "Enter in the template's name sent the message").toBeHidden();
  await page.waitForTimeout(2000);
  await expect(c.locator("[data-c-tpl-note]"), "the late list took the save's note").toHaveText("Saved as “" + name + "”.");
  await expect(c.locator("[data-c-tpl-use]", { hasText: name }), "the late list took the new template").toHaveCount(1);
  await page.unroute("**/os/vayumail/templates?*");

  await page.goto("/os/vayumail/compose?user=priya");
  await c.locator("[data-c-tpl] summary").click();
  await c.locator("[data-c-tpl-use]", { hasText: name }).click();
  await expect(c.locator("[data-c-subject]")).toHaveValue("Thank you");
  await expect(c.locator("[data-c-body]")).toHaveValue("Thanks for this, it helps.");

  await c.locator("[data-c-tpl] summary").click();
  const row = c.locator(".mx-compose__tpl-row", { hasText: name });
  await row.locator("[data-c-tpl-del]").click();
  await page.locator(".vp-confirm button", { hasText: "Delete" }).click();
  await expect(row).toHaveCount(0);
});

// A folder of your own: made from the sidebar, it opens; renamed and deleted
// from its header's menu, and deleting it opens Inbox. Priya's mailbox, so
// no other spec's sidebar shows it.
test("a folder of your own is made, renamed and deleted", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/inbox?user=priya");
  await expect(page.locator("#vm-inbox-list .mx-head")).toBeVisible();
  const title = page.locator("#vm-inbox-list .mx-head__main .mx-head__title");
  const name = "Projects " + Date.now();
  await page.locator("#vm-folders .mx-newfolder summary").click();
  await page.locator("#vm-folders .mx-newfolder__name").fill(name);
  await page.locator("#vm-folders .mx-newfolder__form button").click();
  await expect(title).toHaveText(name);
  await expect(page).toHaveURL(/folder=Projects/);
  await expect(page.locator("#vm-folders a[aria-current=page]")).toContainText(name);
  await page.locator('#vm-inbox-list [aria-label="Folder options"]').click();
  const renamed = "Done " + Date.now();
  await page.locator("#vm-inbox-list .mx-folderform input[name=name]").fill(renamed);
  await page.locator("#vm-inbox-list .mx-folderform button").click();
  await expect(title).toHaveText(renamed);
  await expect(page.locator("#vm-folders")).not.toContainText(name);
  await page.locator('#vm-inbox-list [aria-label="Folder options"]').click();
  await page.locator("#vm-inbox-list .mx-folderform__delete").click();
  const dialog = page.locator(".vp-confirm");
  await expect(dialog).toContainText("Its mail moves to Trash.");
  await dialog.getByRole("button", { name: "Confirm" }).click();
  await expect(title).toHaveText("Inbox");
  await expect(page.locator("#vm-folders")).not.toContainText(renamed);
});

// The compose page is the same sheet, full width: a deep link, or a browser
// without JavaScript.
test("the compose page is the sheet, full width", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/compose?user=ankush");
  await expect(page.locator(".mx-compose--page [data-c-title]")).toHaveText("New message");
  await expect(page.locator("[data-mx-compose-host]")).toHaveCount(0);
  await expect(page.locator('.mx-compose--page a[aria-label="Close"]')).toHaveAttribute("href", "/os/vayumail/inbox?user=ankush");
});

// Discard on the page goes back to the mailbox it was opened from. The way
// back is built by the script, never read from the page's markup (CodeQL #120).
test("discard on the compose page goes back to its mailbox", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/compose?user=ankush");
  await page.locator(".mx-compose--page [data-c-discard]").click();
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox\?user=ankush$/);
});

// Selection mode (Mail plan §7, render 04; motion §8 items 2 and 3). A row's
// round check starts it: the header becomes the count, a click on a row picks
// it instead of opening it, neighbours picked together square off into one
// block, and the reader column shows what is picked. Done ends it.
test("selection mode picks rows and says what is picked", async ({ page }) => {
  await openInbox(page);
  const list = page.locator("#vm-inbox-list");
  const rows = list.locator(".mx-row");
  const subject = await page.locator("#vm-readpane .mx-subject").textContent();
  // Two rows that are neighbours on the page, past the open one. The rows
  // sit under date headings worked out from the clock, so which two rows
  // share a heading moves with the time of day the suite runs.
  const at = await rows.evaluateAll((rs) => rs.findIndex((r, i) => i >= 2 && r.nextElementSibling === rs[i + 1]));
  expect(at, "no two neighbouring rows to pick").toBeGreaterThanOrEqual(2);
  await rows.nth(at).hover();
  await rows.nth(at).locator(".mx-check").click();
  await expect(list).toHaveClass(/is-selecting/);
  await expect(list.locator(".mx-head__sel [data-vm-bulkcount]")).toHaveText("1 selected");
  await expect.poll(() => list.locator(".mx-head__main").evaluate((e) => getComputedStyle(e).opacity)).toBe("0");
  expect(await list.locator(".mx-head__main").evaluate((e) => e.inert), "the folder's header stays reachable under the count").toBe(true);
  // A click on the next row picks it; the message open stays open.
  await rows.nth(at + 1).locator(".mx-row__open").click();
  await expect(list.locator("[data-vm-bulkcount]")).toHaveText("2 selected");
  await expect(page.locator("#vm-readpane .mx-subject")).toHaveText(subject);
  const corners = await rows.nth(at).evaluate((a) => [getComputedStyle(a).borderBottomLeftRadius, getComputedStyle(a.nextElementSibling).borderTopLeftRadius]);
  expect(corners, "two picked neighbours do not meet as one block").toEqual(["0px", "0px"]);
  const panel = page.locator(".mx-select-host .mx-selpanel");
  await expect(panel.locator("[data-vm-sel-title]")).toHaveText("2 messages selected");
  await expect(panel.locator("[data-vm-sel-from]")).toHaveText(/^From .+ and .+$/);
  await expect(panel.locator(".mx-selpanel__card")).toHaveCount(2);
  await list.locator("[data-vm-select-done]").click();
  await expect(list).not.toHaveClass(/is-selecting/);
  await expect(page.locator(".mx-select-host")).toBeHidden();
  await expect(list.locator("[data-vm-check]:checked")).toHaveCount(0);
});

// What is picked is acted on together: Mark unread from the panel, and
// Snooze from the keyboard (s), which moves the picked message out of the
// folder until it wakes.
test("the picked messages are acted on together", async ({ page }) => {
  await openInbox(page);
  const list = page.locator("#vm-inbox-list");
  const row = (text) => list.locator(".mx-row", { hasText: text });
  for (const t of ["Keys for the backup bucket", "The Shield posture numbers"]) {
    await row(t).hover();
    await row(t).locator(".mx-check").click();
  }
  await page.locator(".mx-select-host").getByRole("button", { name: "Mark read" }).click();
  await expect(list).not.toHaveClass(/is-selecting/);
  await expect(row("Keys for the backup bucket")).not.toHaveClass(/is-unread/);
  await expect(row("The Shield posture numbers")).not.toHaveClass(/is-unread/);

  await row("The Shield posture numbers").hover();
  await row("The Shield posture numbers").locator(".mx-check").click();
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("s");
  await page.locator(".mx-select-host").getByRole("menuitem", { name: "Later today" }).click();
  await expect(row("The Shield posture numbers")).toHaveCount(0);
});

// The keys of plan §6 that are not the list's own: g then a folder's letter,
// s and v opening the reader's menus, and ? listing them all.
test("the mail keys go to folders and open the menus", async ({ page }) => {
  await openInbox(page);
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("v");
  await expect(page.locator('#vm-readpane details.sa-pop:has(> summary[aria-label="Move to"])')).toHaveAttribute("open", "");
  await page.keyboard.press("Escape");
  await page.keyboard.press("?");
  await expect(page.locator(".vm-help")).toContainText("g then i · s · d");
  await page.keyboard.press("?");
  await page.keyboard.press("g");
  await page.keyboard.press("s");
  await expect(page.locator(".mx-head__main .mx-head__title")).toHaveText("Sent");
});

// The command bar offers what the page offers, by name (plan §6): the open
// message's actions, run as the toolbar runs them.
// The keys keep out of what is being written (Mail plan §10): one seed per
// guard. A letter typed in a field is the field's, and a key pressed with
// focus anywhere in the composer, a button included, is the composer's. Either
// way the open message behind is not archived.
test("the mail keys keep out of fields and the composer", async ({ page }) => {
  await openInbox(page);
  const acted = [];
  // An archive is a move to Archive; the newest message's own read mark is not.
  page.on("request", (r) => { if (r.method() === "POST" && /Archive/.test(r.postData() || "")) acted.push(r.url() + " " + r.postData()); });
  const search = page.locator('input[type="search"][name="q"]');
  await search.focus();
  await page.keyboard.press("e");
  await expect(search).toHaveValue("e");
  await page.waitForTimeout(600);
  expect(acted, "a letter typed in the search field archived the open message").toEqual([]);
  await search.fill("");
  await search.blur();

  await page.locator('#vm-readpane [aria-label="Reply"]').first().click();
  const cc = page.locator("[data-mx-compose-host] [data-c-toggle-cc]");
  await expect(cc).toBeVisible();
  await cc.focus();
  await page.keyboard.press("e");
  await page.waitForTimeout(600);
  expect(acted, "a key pressed on the composer's button archived the message behind it").toEqual([]);
});

test("the command bar runs the open message's actions", async ({ page }) => {
  await openInbox(page);
  // An older message, not the newest the other tests read.
  await page.locator("#vm-inbox-list .mx-row__open", { hasText: "Invoice #2026-091" }).click();
  await expect(page.locator("#vm-readpane .mx-subject")).toHaveText("Invoice #2026-091");
  const subject = "Invoice #2026-091";
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("Control+k");
  // Only what is on screen and in use: the selection's own controls are not
  // offered while nothing is selected.
  await page.locator("#cmd-input").fill("select every");
  await expect(page.locator("#cmd-results .cmd-item", { hasText: "Select every message" })).toHaveCount(0);
  await page.locator("#cmd-input").fill("archive");
  const item = page.locator("#cmd-results .cmd-item", { hasText: "Archive" }).first();
  await expect(item).toContainText("This message");
  await page.keyboard.press("Enter");
  await expect(page.locator("#vm-inbox-list .mx-row", { hasText: subject })).toHaveCount(0);
});

// Search in place (Mail plan §8, item 8): the field rises into the header as
// the title folds away, the scope bar comes down, results take the list's
// place as the list's own rows, and Escape gives the list back where it was.
test("search rises into the header and gives the list back", async ({ page }) => {
  await openInbox(page);
  const list = page.locator("#vm-inbox-list");
  await list.evaluate((e) => { e.scrollTop = 600; });
  await page.locator(".mx-search__input").focus();
  await expect(list).toHaveClass(/is-searching/);
  await expect.poll(() => list.locator(".mx-head__main").evaluate((e) => getComputedStyle(e).opacity)).toBe("0");
  await expect(page.locator(".mx-scope")).toBeVisible();
  await page.keyboard.type("bucket");
  const results = page.locator("#vm-search-results");
  await expect(results.locator(".mx-results__count")).toContainText("for “bucket”");
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toHaveCount(1);
  // The result's heading sticks under the raised search, and not lower: a
  // heading stuck below where it sits covers the first result's sender.
  const overlap = await results.evaluate((r) => r.querySelector(".mx-group").getBoundingClientRect().bottom - r.querySelector(".mx-row").getBoundingClientRect().top);
  expect(overlap, "the result's heading covers the first result").toBeLessThanOrEqual(0);
  await expect(list.locator("> .mx-list")).toBeHidden();
  await page.locator(".mx-scope__opt", { hasText: "Has attachments" }).click();
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toHaveCount(0);
  await page.locator(".mx-search__input").fill("runbook");
  await expect(results.locator(".mx-row", { hasText: "migration window" }).first()).toBeVisible();
  await page.locator(".mx-search__input").press("Escape");
  await expect(list).not.toHaveClass(/is-searching/);
  await expect(results).toBeEmpty();
  await expect(list.locator("> .mx-list")).toBeVisible();
  expect(await list.evaluate((e) => e.scrollTop), "the list did not come back where it was").toBe(600);
});

// The list's own refresh (the poller, fired when a message is read or moved)
// waits while a search is on screen, so the results stay under the reader,
// and runs once the search is let go of.
test("the list does not redraw under a search", async ({ page }) => {
  await openInbox(page);
  await page.locator(".mx-search__input").focus();
  await page.keyboard.type("bucket");
  await page.locator(".mx-scope__opt", { hasText: "All mail" }).click();
  const results = page.locator("#vm-search-results");
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toHaveCount(1);
  const polls = [];
  page.on("request", (r) => { if (r.url().includes("/os/vayumail/inbox/fragment")) polls.push(r.url()); });
  await page.evaluate(() => document.body.dispatchEvent(new CustomEvent("vm-mail-changed")));
  await page.waitForTimeout(800);
  expect(polls, "the list was redrawn under the search").toHaveLength(0);
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toBeVisible();
  await expect(page.locator(".mx-search__input")).toHaveValue("bucket");
  const redraw = page.waitForRequest((r) => r.url().includes("/os/vayumail/inbox/fragment"));
  await page.locator(".mx-search__input").press("Escape");
  await redraw;
});

// A search is saved from its results to the sidebar's Searches, and runs
// from there across all mail as if typed; Forget takes it off again.
test("a search is saved to the sidebar, and runs from there", async ({ page }) => {
  await openInbox(page);
  await page.locator(".mx-search__input").focus();
  await page.keyboard.type("bucket");
  await page.locator(".mx-scope__opt", { hasText: "All mail" }).click();
  const results = page.locator("#vm-search-results");
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toHaveCount(1);
  await results.locator(".mx-results__save", { hasText: "Save this search" }).click();
  await expect(results.locator(".mx-results__save")).toHaveText("Saved · Forget");
  const saved = page.locator("#vm-searches a", { hasText: "bucket" });
  await expect(saved).toBeVisible();
  await saved.click();
  await expect(page).toHaveURL(/[?&]search=bucket/);
  await expect(page.locator(".mx-search__input")).toHaveValue("bucket");
  await expect(page.locator('.mx-scope input[value="all"]')).toBeChecked();
  await expect(results.locator(".mx-row", { hasText: "Keys for the backup bucket" })).toHaveCount(1);
  // CI's runner has twice found this button present but never clickable,
  // where no local run has; if it happens, say what the page held.
  const forget = results.locator(".mx-results__save", { hasText: "Saved · Forget" });
  try {
    await expect(forget).toBeVisible();
    await forget.click({ timeout: 10000 });
  } catch (e) {
    const state = await page.evaluate(() => {
      const b = document.querySelector("#vm-search-results .mx-results__save");
      const box = (el) => { const r = el.getBoundingClientRect(); return [r.x, r.y, r.width, r.height].map(Math.round).join(","); };
      const hidden = [];
      for (let n = b; n && n !== document.body; n = n.parentElement) {
        const cs = getComputedStyle(n);
        if (cs.display === "none" || cs.visibility !== "visible" || cs.opacity !== "1" || cs.animationName !== "none") hidden.push((n.id || n.className) + " " + cs.display + "/" + cs.visibility + "/" + cs.opacity + "/" + cs.animationName);
      }
      const r = b && b.getBoundingClientRect();
      const top = r && document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return { box: b && box(b), ancestors: hidden, list: document.getElementById("vm-inbox-list").className, screen: document.body.getAttribute("data-mx-screen"), screens: document.body.hasAttribute("data-mx-screens"), focus: document.activeElement && document.activeElement.className, top: top && (top.id || top.className), viewport: innerWidth + "x" + innerHeight };
    });
    throw new Error(e.message + "\npage state: " + JSON.stringify(state));
  }
  await expect(page.locator("#vm-searches a")).toHaveCount(0);
});

// On a phone a message opened from a folder with one message in it is shown
// whole: the screen it is pushed onto is the height of the phone, not of the
// one-row list beneath it.
test("on a phone, a message from a short folder is shown whole", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/os/vayumail/inbox?user=ankush&folder=Sent");
  await page.locator("#vm-inbox-list .mx-row__open").first().click();
  const body = page.locator("#vm-readpane .mx-msg__body").first();
  await expect(body).toBeVisible();
  await expect(page.locator("body")).toHaveAttribute("data-mx-screen", "message");
  await page.waitForTimeout(400);
  // The middle of what of the body is on the screen: Sent's newest message
  // may be longer than a phone, and a point below the screen hits nothing.
  const shown = await body.evaluate((b) => {
    const r = b.getBoundingClientRect();
    return b.contains(document.elementFromPoint(r.left + 8, (r.top + Math.min(r.bottom, innerHeight)) / 2));
  });
  expect(shown, "the message body is cut off below the list's height").toBe(true);
});

// A group made in Contacts goes into To as its people when its name is
// chosen; it is deleted again so the mailbox is left as it was found.
test("a contact group goes into To as its people", async ({ page }) => {
  await openInbox(page);
  await page.locator("button.sa-appside__item", { hasText: "Contacts" }).click();
  await page.locator(".vm-contacts summary", { hasText: "New group" }).click();
  const form = page.locator(".vm-keys-add .vm-group__form");
  await form.locator('input[name="name"]').fill("Crew");
  await form.locator('textarea[name="members"]').fill("priya@mail.test\nrohan@h.test");
  await form.locator('button[value="save"]').click();
  await expect(page.locator(".vm-contacts")).toContainText("Saved Crew.");
  await page.goto("/os/vayumail/compose?user=ankush");
  const to = page.locator('[data-c-chips="to"]');
  await to.locator("[data-c-chip-input]").fill("crew");
  await to.locator("[data-c-chip-input]").press("Enter");
  await expect(to.locator('.vm-chip span[title="priya@mail.test"]')).toHaveCount(1);
  await expect(to.locator('.vm-chip span[title="rohan@h.test"]')).toHaveCount(1);
  await expect(to.locator(".vm-chip")).toHaveCount(2);
  const csrf = (await page.context().cookies()).find((c) => c.name === "vp_csrf");
  const r = await page.request.post("/os/vayumail/contacts/groups", {
    headers: { "X-CSRF-Token": csrf ? decodeURIComponent(csrf.value) : "" },
    form: { user: "ankush", action: "delete", was: "Crew" },
  });
  expect(r.ok()).toBe(true);
});

// A label is put on from the reader's Label menu: it shows under the
// subject and in the sidebar's Labels, whose link finds the message across
// all mail; ticked off again, it is gone from both.
test("a label is put on a message and found from the sidebar", async ({ page }) => {
  await openInbox(page);
  const pane = page.locator("#vm-readpane");
  const subject = (await pane.locator(".mx-subject").textContent()).trim();
  await pane.locator('summary[aria-label="Label"]').click();
  const field = pane.locator('.mx-labelmenu__new input[name="label"]');
  await field.fill("Follow up");
  await field.press("Enter");
  await expect(pane.locator(".mx-rbody .mx-label", { hasText: "Follow up" })).toBeVisible();
  const side = page.locator("#vm-labels a", { hasText: "Follow up" });
  await expect(side).toBeVisible();
  await side.click();
  await expect(page.locator(".mx-search__input")).toHaveValue('label:"Follow up"');
  await expect(page.locator("#vm-search-results .mx-row", { hasText: subject }).first()).toBeVisible();
  await page.locator("#vm-search-results .mx-row__open", { hasText: subject }).first().click();
  await pane.locator('summary[aria-label="Label"]').click();
  await pane.locator('.mx-labelmenu [role="menuitemcheckbox"]', { hasText: "Follow up" }).click();
  await expect(pane.locator(".mx-rbody .mx-label")).toHaveCount(0);
  await expect(page.locator("#vm-labels a")).toHaveCount(0);
});

// An attachment's download fills a ring on its tile and settles to a tick,
// and arrives under its own name; a file added to compose slides into the
// attachment row (Mail plan §8, item 7).
test("an attachment downloads with its ring, and a new one slides in", async ({ page }) => {
  await openInbox(page);
  const card = page.locator("#vm-readpane a[data-mx-file]").first();
  await card.evaluate((c) => {
    window.__ring = false;
    new MutationObserver(() => { if (c.classList.contains("is-loading")) window.__ring = true; }).observe(c, { attributes: true });
  });
  const download = page.waitForEvent("download");
  await card.click();
  expect((await download).suggestedFilename()).toBe("migration-runbook-v3.pdf");
  await expect(card).toHaveClass(/is-done/);
  expect(await page.evaluate(() => window.__ring), "the download showed no ring").toBe(true);

  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("c");
  await page.locator("[data-mx-compose-host] [data-c-files]").setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("notes") });
  await expect(page.locator("[data-mx-compose-host] .vm-attach-chip").last()).toHaveClass(/is-new/);
});

// A reader narrower than its tools (three columns on a laptop) keeps every
// tool inside it: they wrap, and nothing scrolls sideways (pipeline 3y: More,
// and Print in it, sat past the reader's edge on a 14-inch screen).
test("a narrow reader keeps its tools in reach", async ({ page }) => {
  await openInbox(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  const pane = page.locator("#vm-readpane");
  await expect(pane.locator(".mx-reader")).toBeVisible();
  const fit = await pane.evaluate((p) => {
    const r = p.getBoundingClientRect();
    const out = [...p.querySelectorAll(".mx-rtools .mx-tool, .mx-rtools .mx-rtools__pos")].filter((t) => t.getClientRects().length && t.getBoundingClientRect().right > r.right + 1);
    return { sideways: p.scrollWidth - p.clientWidth, past: out.map((t) => t.getAttribute("aria-label") || t.textContent) };
  });
  expect(fit.sideways, "the reader scrolls sideways").toBe(0);
  expect(fit.past, "tools past the reader's edge").toEqual([]);
  expect(await pane.locator(".mx-rtools__pos").evaluate((e) => e.getClientRects().length), "the position breaks across lines").toBe(1);
});

// Full width (pipeline 3y), as compose's Expand: the open message is pushed
// over the list and takes its place, Mail's sidebar kept, and the bar's back
// button, Escape, w or the browser's Back return to the columns.
test("a message opens full width and comes back to the columns", async ({ page }) => {
  await openInbox(page);
  const pane = page.locator("#vm-readpane");
  const list = page.locator("#vm-inbox-list");
  // What a click at the middle of the list would land on.
  const over = () => list.evaluate((l) => { const b = l.getBoundingClientRect(); const at = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2); return at && at.closest("#vm-readpane") ? "reader" : at && at.closest("#vm-inbox-list") ? "list" : "other"; });
  expect(await over()).toBe("list");
  const subject = await pane.locator(".mx-subject").textContent();
  await pane.getByRole("button", { name: "Full width" }).click();
  await expect.poll(over, { message: "the message is not over the list" }).toBe("reader");
  await expect(page.locator(".sa-appside .mx-account")).toBeVisible();
  await expect(pane.locator(".mx-subject")).toHaveText(subject);
  await expect(pane.getByRole("button", { name: "Full width" })).toBeHidden();
  await expect(pane.locator(".mx-back")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect.poll(over, { message: "Escape did not give the list back" }).toBe("list");
  await expect.poll(() => page.evaluate(() => document.body.hasAttribute("data-mx-screens")), { message: "the columns did not come back" }).toBe(false);
  await expect(pane.locator(".mx-reader"), "the message did not stay open beside the list").toBeVisible();
  await expect(pane.locator(".mx-subject")).toHaveText(subject);
  await page.keyboard.press("w");
  await expect.poll(over).toBe("reader");
  await page.goBack();
  await expect.poll(over, { message: "Back did not give the list back" }).toBe("list");
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox/);
});

// Reading set to full width (pipeline 3y) is the person's own choice, kept on
// their account; this run signs in with the API key, which has none, so the
// page is served as the server draws it for that person (the server's side is
// TestTheMailboxOpensFullWidthForWhoChoseIt). The list comes first at full
// width, the newest message waits unread behind it, a message opens over it,
// and Escape gives the list back.
test("reading full width: the list first, a message over it", async ({ page }) => {
  await newestUnread(page);
  await page.route(/\/os\/vayumail\/inbox\?user=ankush$/, async (route) => {
    const res = await route.fetch();
    route.fulfill({ response: res, body: (await res.text()).replace('<div class="vm-split" data-page-kind="app">', '<div class="vm-split" data-page-kind="app" data-mx-layout="full">') });
  });
  await openInbox(page);
  const pane = page.locator("#vm-readpane");
  const list = page.locator("#vm-inbox-list");
  const split = await page.locator(".vm-split").evaluate((e) => e.getBoundingClientRect().width);
  await expect(pane).toBeHidden();
  expect(Math.abs((await list.evaluate((e) => e.getBoundingClientRect().width)) - split), "the list is not full width").toBeLessThan(2);
  await page.waitForTimeout(2600);
  await expect(list.locator(".mx-row").filter({ has: page.locator(".mx-row__dot") }).first(), "the newest message was read behind the list").toHaveClass(/is-unread/);
  await list.locator(".mx-row__open").nth(1).click();
  await expect(pane.locator(".mx-reader")).toBeVisible();
  expect(Math.abs((await pane.evaluate((e) => e.getBoundingClientRect().width)) - split), "the message is not full width").toBeLessThan(2);
  await expect(pane.getByRole("button", { name: "Full width" })).toBeHidden();
  await page.keyboard.press("Escape");
  await expect(pane).toBeHidden();
  await expect(list).toBeVisible();
});

// Not junk (pipeline 3ab): in Junk, ! (the key that files a message there
// everywhere else) sends it back to the Inbox and remembers the sender; the
// message's own page has the same as a button.
test("a message in Junk is not junk, by its key or its page", async ({ page }) => {
  const junk = "/os/vayumail/inbox?user=priya&folder=Junk";
  await openInbox(page, junk);
  const pane = page.locator("#vm-readpane");
  await expect(pane.getByRole("button", { name: "Not junk" })).toBeVisible();
  await expect(pane.getByRole("button", { name: "Junk", exact: true })).toHaveCount(0);
  const subject = (await pane.locator(".mx-subject").textContent()).trim();
  await page.locator("body").press("!");
  await expect(pane).toContainText("is no longer filed as junk");
  await expect(page.locator("#vm-inbox-list")).not.toContainText(subject);
  await page.goto("/os/vayumail/inbox?user=priya");
  await expect(page.locator("#vm-inbox-list")).toContainText(subject);
  // The other, from its own page.
  await page.goto(junk);
  const link = page.locator("#vm-inbox-list .mx-row__open").first();
  const other = (await link.locator(".mx-row__subj-text").textContent()).trim();
  await page.goto(await link.getAttribute("href"));
  await page.locator("[data-mail-actions]").getByRole("button", { name: "Not junk" }).click();
  await page.waitForURL(/\/os\/vayumail\/inbox/);
  await page.goto("/os/vayumail/inbox?user=priya");
  await expect(page.locator("#vm-inbox-list")).toContainText(other);
});

// The reply field under a message (Mail plan §3.3, pipeline 3z): what is
// typed there becomes the reply. The first keystroke opens the reply sheet,
// and every word typed, those typed while it loads among them, is carried
// into its body, with the caret after them.
test("typing in the reply field writes the reply", async ({ page }) => {
  await openInbox(page);
  const field = page.locator("#vm-readpane .mx-dock__field");
  await field.click();
  await page.keyboard.type("Saturday works for us, see you then", { delay: 15 });
  const sheet = page.locator("[data-mx-compose-host] .mx-compose");
  const body = sheet.locator("[data-c-body]");
  await expect(body).toBeFocused();
  await page.keyboard.type(".");
  await expect(body).toHaveValue(/^Saturday works for us, see you then\./);
  expect(await body.evaluate((b) => b.selectionStart), "the caret is not after the words").toBe("Saturday works for us, see you then.".length);
  await expect(field).toHaveValue("");
  await expect(sheet.locator("[data-c-subject]")).toHaveValue(/^Re: /);
});

// Print prints the open message, all of it, and nothing of the console
// (pipeline 3y): the old print rules left the reader in its clipped column,
// and the page came out with its address and no mail.
test("print gives the message and nothing else", async ({ page }) => {
  await openInbox(page);
  const reader = page.locator("#vm-readpane .mx-reader");
  await expect(reader).toBeVisible();
  const words = (await reader.locator(".mx-msg__body").innerText()).trim().slice(0, 40);
  // A window shorter than the message, so a column that clips would show it.
  await page.setViewportSize({ width: 1440, height: 200 });
  await page.emulateMedia({ media: "print" });
  const printed = await reader.evaluate((r) => ({
    others: [...document.querySelectorAll("body *")].filter((e) => e.getClientRects().length && !r.contains(e) && !e.contains(r)).map((e) => e.className || e.tagName).slice(0, 5),
    tools: !!r.querySelector(".mx-rtools").getClientRects().length,
    clipped: [r, ...(function up(n, a) { return n ? up(n.parentElement, a.concat(n)) : a; })(r.parentElement, [])].some((n) => n.scrollHeight > n.clientHeight + 1 && getComputedStyle(n).overflowY !== "visible"),
    text: r.innerText,
  }));
  await page.emulateMedia({ media: "screen" });
  expect(printed.others, "the console prints beside the message").toEqual([]);
  expect(printed.tools, "the message's tools print").toBe(false);
  expect(printed.clipped, "the message is clipped by a scrolling column").toBe(false);
  expect(printed.text).toContain(words);
});

// On a phone Mail is three screens (Mail plan §7, render 05; motion §8 item
// 6): the list first, the mailboxes above it, a message pushed over it with
// its own bar where the tab bar was. The newest message, not on screen, is
// not read; back is the phone's own back, and lands where the list was.
test("on a phone, Mail is screens pushed and popped", async ({ page }) => {
  await newestUnread(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(inbox);
  const body = page.locator("body");
  await expect(body).toHaveAttribute("data-mx-screen", "list");
  await expect(page.locator("#vm-readpane")).toBeHidden();
  const newest = page.locator("#vm-inbox-list .mx-row.vm-active");
  await page.waitForTimeout(2600);
  await expect(newest, "the newest message was marked read without being shown").toHaveClass(/is-unread/);

  // The way to every folder says so: named Mailboxes, it read as a list of
  // accounts, and on a phone Sent went unfound behind it.
  await expect(page.locator(".mx-back--boxes")).toHaveText("Folders");
  await page.locator(".mx-back--boxes").click();
  await expect(body).toHaveAttribute("data-mx-screen", "boxes");
  await expect(page.locator(".mx-boxes__title")).toHaveText("Folders");
  await expect(page.locator("#vm-folders a", { hasText: "Sent" })).toBeVisible();
  // Contacts opens into the reading pane, so on a phone it is pushed as a
  // screen, with its own way back.
  await page.locator("button.sa-appside__item", { hasText: "Contacts" }).click();
  await expect(body).toHaveAttribute("data-mx-screen", "message");
  await expect(page.locator(".vm-contacts-title", { hasText: "Contacts" })).toBeVisible();
  await page.locator(".vm-contacts .mx-back").click();
  await expect(body).toHaveAttribute("data-mx-screen", "list");
  await page.locator(".mx-back--boxes").click();
  await expect(body).toHaveAttribute("data-mx-screen", "boxes");
  // The folder swaps its list in over htmx. Network idle comes before the
  // swap settles, and a scroll set in between is lost under the new rows.
  const settled = page.evaluate(() => new Promise((done) => {
    document.body.addEventListener("htmx:afterSettle", function f(e) {
      if (e.detail.target && e.detail.target.id === "vm-inbox-list") { document.body.removeEventListener("htmx:afterSettle", f); done(); }
    });
  }));
  await page.locator("#vm-folders a", { hasText: "Inbox" }).click();
  await expect(body).toHaveAttribute("data-mx-screen", "list");
  await settled;

  const list = page.locator("#vm-inbox-list");
  // As far as 300px goes: earlier tests take messages out of the Inbox, so
  // the list may end sooner, and back is held to where it actually was.
  const was = await list.evaluate((e) => { e.scrollTop = 300; return e.scrollTop; });
  expect(was, "the list is too short to scroll").toBeGreaterThan(100);
  await expect(list).toHaveClass(/is-scrolled/);
  await page.evaluate(() => {
    window.__moves = [];
    new MutationObserver(() => { const m = document.body.getAttribute("data-mx-move"); if (m) window.__moves.push(m); }).observe(document.body, { attributes: true });
  });
  // A row in view where the list stands: one above it would be scrolled to
  // by the click itself. Where the list was is read as the row is opened.
  const inView = await list.evaluate((l) => {
    const top = l.getBoundingClientRect().top + 120, rows = [...l.querySelectorAll(".mx-row__open")];
    l.addEventListener("click", () => { window.__openedAt = l.scrollTop; }, { capture: true, once: true });
    return rows.findIndex((r) => r.getBoundingClientRect().top > top);
  });
  // The folder's swap plays a view transition, and until it ends a tap lands
  // on its overlay, not on the row: wait until the row is what a tap finds.
  const row = list.locator(".mx-row__open").nth(inView);
  await expect.poll(() => row.evaluate((r) => {
    const b = r.getBoundingClientRect();
    return r.contains(document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2));
  })).toBe(true);
  const depth = await page.evaluate(() => history.length);
  await row.click();
  await expect(body).toHaveAttribute("data-mx-screen", "message");
  expect(await page.evaluate(() => history.length), "opening a message was not a history entry").toBe(depth + 1);
  await expect(page.locator("#vm-readpane .mx-phonebar")).toBeVisible();
  await expect.poll(() => page.locator(".sa-tabbar").evaluate((e) => getComputedStyle(e).transform)).not.toBe("none");
  const read = list.locator(".mx-row.vm-active");
  await read.evaluate((r) => { window.__tint = false; new MutationObserver(() => { if (r.classList.contains("is-just-read")) window.__tint = true; }).observe(r, { attributes: true }); });
  await page.goBack();
  await expect(body).toHaveAttribute("data-mx-screen", "list");
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox/);
  expect(await page.evaluate(() => window.__openedAt), "the click moved the list before opening").toBe(was);
  expect(await list.evaluate((e) => e.scrollTop), "back did not land where the list was").toBe(was);
  expect(await page.evaluate(() => window.__tint), "the row just read was not tinted").toBe(true);
  expect(await page.evaluate(() => window.__moves), "the screens did not push and pop").toEqual(["push", "pop"]);
});

// Text holds its contrast where Mail paints its own colours (Mail plan §10):
// the selected row and its avatar, the previews and times, and the seal, in
// both schemes. A colour is composited over every background beneath it on a
// canvas, so a translucent token or an oklch() value is measured as it is
// drawn.
for (const scheme of ["light", "dark"]) {
  test.describe(`in the ${scheme} scheme`, () => {
    test.use({ colorScheme: scheme });
    test(`Mail's text holds its contrast (${scheme})`, async ({ page }) => {
      await openInbox(page);
      await page.locator(".mx-row", { hasText: "Newsletter draft, October" }).locator(".mx-row__open").click();
      await expect(page.locator("#vm-readpane .mx-seal")).toBeVisible();
      // A label is drawn on the selected row and under the subject; it is put
      // on here and taken off at the end so no other test sees it.
      const pane = page.locator("#vm-readpane");
      await pane.locator('summary[aria-label="Label"]').click();
      await pane.locator('.mx-labelmenu__new input[name="label"]').fill("Ink");
      await pane.locator('.mx-labelmenu__new input[name="label"]').press("Enter");
      await expect(page.locator("#vm-inbox-list .mx-row.vm-active .mx-label", { hasText: "Ink" })).toBeVisible();
      // The selection fades in over --duration-micro while its text turns at
      // once, so a reading inside those 90 ms is white on near-white. What is
      // judged is the colour the row settles on: wait out its transitions.
      await page.locator("#vm-inbox-list .mx-row.vm-active").evaluate((r) =>
        Promise.all(r.getAnimations({ subtree: true }).map((a) => a.finished)));
      const measured = await page.evaluate(() => {
        const cv = document.createElement("canvas"); cv.width = cv.height = 1;
        const x = cv.getContext("2d", { willReadFrequently: true });
        const paint = (c) => { x.fillStyle = c; x.fillRect(0, 0, 1, 1); return Array.from(x.getImageData(0, 0, 1, 1).data).slice(0, 3); };
        const lum = (rgb) => { const [r, g, b] = rgb.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * r + 0.7152 * g + 0.0722 * b; };
        const ratio = (el) => {
          const chain = []; for (let n = el; n; n = n.parentElement) chain.unshift(getComputedStyle(n).backgroundColor);
          paint("#000"); chain.forEach(paint);
          const bg = paint("rgba(0,0,0,0)"); const fg = paint(getComputedStyle(el).color);
          const [a, b] = [lum(fg), lum(bg)].sort((p, q) => q - p);
          return (a + 0.05) / (b + 0.05);
        };
        const out = {};
        const sel = document.querySelector("#vm-inbox-list .mx-row.vm-active");
        const other = document.querySelector("#vm-inbox-list .mx-row:not(.vm-active)");
        for (const [name, el] of [
          ["selected sender", sel && sel.querySelector(".mx-row__who")], ["selected subject", sel && sel.querySelector(".mx-row__subj-text")],
          ["selected preview", sel && sel.querySelector(".mx-row__pv")], ["selected time", sel && sel.querySelector(".mx-row__time")],
          ["selected initials", sel && sel.querySelector(".mx-row__open > .vm-av")],
          ["preview", other && other.querySelector(".mx-row__pv")], ["time", other && other.querySelector(".mx-row__time")],
          ["seal", document.querySelector("#vm-readpane .mx-seal span")],
          ["selected label", sel && sel.querySelector(".mx-label")], ["label", document.querySelector("#vm-readpane .mx-rbody .mx-label")],
        ]) out[name] = el ? Math.round(ratio(el) * 100) / 100 : "missing";
        return out;
      });
      for (const [name, r] of Object.entries(measured)) {
        expect(r, `${name}: no element to measure`).not.toBe("missing");
        expect(r, `${name} is ${r}:1 in ${scheme}, under 4.5:1`).toBeGreaterThanOrEqual(4.5);
      }
      await pane.locator('summary[aria-label="Label"]').click();
      await pane.locator('.mx-labelmenu [role="menuitemcheckbox"]', { hasText: "Ink" }).click();
      await expect(pane.locator(".mx-rbody .mx-label")).toHaveCount(0);
    });
  });
}

// A new mailbox is made in its sheet (fidelity plan §6a): a link to the sheet
// opens it with the page, Create closes it, and the mailbox's row is in the
// list without a reload; the page's button is what opens it again.
test("a new mailbox is made in its sheet, and its row appears", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const local = "sheet" + Date.now().toString(36);
  await page.goto("/os/vayumail/accounts#new-mailbox");
  const sheet = page.locator("dialog#new-mailbox");
  await expect(sheet).toHaveJSProperty("open", true);
  await sheet.locator("[data-a-local]").fill(local);
  await sheet.locator("[data-a-pass]").fill("e2e-mailbox-pass-" + local);
  await sheet.getByRole("button", { name: "Create mailbox" }).click();
  await expect(sheet).toHaveJSProperty("open", false);
  await expect(page.locator("#vm-accounts-list")).toContainText(local + "@");
  await page.locator('main [data-sheet="new-mailbox"]').click();
  await expect(sheet).toHaveJSProperty("open", true);
});

// An app password is made in its sheet on Connect (8.12): Create closes the
// sheet so the password, shown once, can be read beside its new row.
test("an app password is made in its sheet and shown once", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const label = "Sheet " + Date.now().toString(36);
  await page.goto("/os/vayumail/connect#new-app-password");
  const sheet = page.locator("dialog#new-app-password");
  await expect(sheet).toHaveJSProperty("open", true);
  await sheet.locator('input[name="label"]').fill(label);
  await sheet.getByRole("button", { name: "Create app password" }).click();
  await expect(sheet).toHaveJSProperty("open", false);
  const list = page.locator("#vm-apppw-card");
  await expect(list.locator(".callout--ok .vm-apppw-secret")).toHaveText(/^[A-Za-z0-9]{4}(-[A-Za-z0-9]{4})+$/);
  await expect(list.locator("tr", { hasText: label })).toHaveCount(1);
  // Its Download works: the page loads the script that drives it.
  const [file] = await Promise.all([page.waitForEvent("download"), list.getByRole("button", { name: "Download .txt" }).click()]);
  expect(file.suggestedFilename()).toBe("vayumail-app-password.txt");
});
