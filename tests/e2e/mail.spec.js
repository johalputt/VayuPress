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
  const reply = page.locator('#vm-readpane [aria-label="Reply"]');
  await reply.click();
  const sheet = page.locator("[data-mx-compose-host] .mx-compose");
  await expect(sheet.locator("[data-c-title]")).toHaveText("Re: the migration window on Saturday");
  await expect(page).toHaveURL(/\/os\/vayumail\/inbox/);
  await expect(sheet.locator("[data-c-body]")).toHaveValue("");
  await expect(sheet.locator(".mx-quoted > summary")).toHaveText("Show the quoted message");
  await expect(sheet.locator("[data-c-from]")).toHaveValue("ankush@mail.test");
  const origin = await page.evaluate(async () => {
    const panel = document.querySelector("[data-mx-compose-host] .mx-compose"), trig = document.querySelector('#vm-readpane [aria-label="Reply"]');
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

// The compose page is the same sheet, full width: a deep link, or a browser
// without JavaScript.
test("the compose page is the sheet, full width", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/vayumail/compose?user=ankush");
  await expect(page.locator(".mx-compose--page [data-c-title]")).toHaveText("New message");
  await expect(page.locator("[data-mx-compose-host]")).toHaveCount(0);
  await expect(page.locator('.mx-compose--page a[aria-label="Close"]')).toHaveAttribute("href", "/os/vayumail/inbox?user=ankush");
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
  await rows.nth(2).hover();
  await rows.nth(2).locator(".mx-check").click();
  await expect(list).toHaveClass(/is-selecting/);
  await expect(list.locator(".mx-head__sel [data-vm-bulkcount]")).toHaveText("1 selected");
  await expect.poll(() => list.locator(".mx-head__main").evaluate((e) => getComputedStyle(e).opacity)).toBe("0");
  expect(await list.locator(".mx-head__main").evaluate((e) => e.inert), "the folder's header stays reachable under the count").toBe(true);
  // A click on the next row picks it; the message open stays open.
  await rows.nth(3).locator(".mx-row__open").click();
  await expect(list.locator("[data-vm-bulkcount]")).toHaveText("2 selected");
  await expect(page.locator("#vm-readpane .mx-subject")).toHaveText(subject);
  const corners = await rows.nth(2).evaluate((a) => [getComputedStyle(a).borderBottomLeftRadius, getComputedStyle(a.nextElementSibling).borderTopLeftRadius]);
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
test("the command bar runs the open message's actions", async ({ page }) => {
  await openInbox(page);
  const subject = (await page.locator("#vm-readpane .mx-subject").textContent()).trim();
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
