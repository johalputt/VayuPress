// @ts-check
// Talk (render 05) on the install with Mail on: two of its mailboxes in a real
// conversation. An administrator may chat as any mailbox on the install, so
// one browser page is Ankush and another is Priya, on one relay.
const { test, expect } = require("@playwright/test");

const base = process.env.MAIL_BASE_URL;
if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
test.use({ baseURL: base });
// The tests share the relay and the two mailboxes' conversations.
test.describe.configure({ mode: "default" });

// Talk speaks as mailboxes the install holds as accounts (with a key each).
// Made through the console's own endpoint; mail.spec.js makes the same two,
// and whichever runs second finds them made.
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

const ANKUSH = "ankush@mail.test", PRIYA = "priya@mail.test";

// One page chatting as `who`, connected.
async function talkAs(browser, who, viewport = { width: 1440, height: 900 }) {
  const page = await (await browser.newContext({ baseURL: base, viewport })).newPage();
  await page.goto("/os/talk");
  const as = page.locator("#vtalk-as");
  if ((await as.inputValue()) !== who) await as.selectOption(who);
  await expect(page.locator("#vtalk-status")).toHaveAttribute("data-state", "online");
  return page;
}

async function startChat(page, peer) {
  await page.locator(".vtalk-new > summary").click();
  await page.locator("#vtalk-peer").fill(peer);
  await page.locator("#vtalk-newchat button[type=submit]").click();
  await expect(page.locator(".vtalk-head-addr")).toHaveText(peer);
}

async function send(page, text, burn) {
  if (burn) await page.locator("#vtalk-ttl").selectOption(burn);
  await page.locator("#vtalk-input").fill(text);
  await page.locator("#vtalk-input").press("Enter");
  await expect(page.locator(".vtalk-msg--out .vtalk-bubble-text", { hasText: text })).toBeVisible();
}

test("a conversation has its bar, its pills and its rows", async ({ browser }) => {
  const a = await talkAs(browser, ANKUSH);
  const p = await talkAs(browser, PRIYA);
  await startChat(a, PRIYA);
  // The bar names the conversation; New chat closed behind it.
  await expect(a.locator("#vtalk-crumb")).toBeVisible();
  await expect(a.locator(".vtalk-new")).not.toHaveAttribute("open", "");
  // What this tab knows about the other side, and the burn control, in the bar.
  const head = a.locator("#vtalk-thread-head");
  await expect(head.locator(".vtalk-verify-btn")).toHaveText("Compare safety numbers");
  await expect(head.locator(".vtalk-pill--e2e")).toHaveText("End-to-end encrypted");
  await expect(head.locator("#vtalk-ttl")).toBeVisible();
  await expect(a.locator("#vtalk-input")).toHaveAttribute("placeholder", /^Message /);

  // A person has one colour in Mail and Talk: the row's tint is the one the
  // server gives Mail for the same address (the fixture has no pictures).
  const tone = (await (await a.request.get("/os/vayumail/compose/recipient?addr=" + encodeURIComponent(PRIYA))).json()).tone;
  await expect(a.locator(".vtalk-convo", { hasText: "priya" }).first().locator(".vtalk-avatar")).toHaveClass(new RegExp("\\bvm-av--" + tone + "\\b"));

  await send(a, "Hello from the conversation spec");
  const row = a.locator(".vtalk-convo", { hasText: "priya" }).first();
  await expect(row.locator(".vtalk-convo-pv")).toHaveText("You: Hello from the conversation spec");
  await expect(row.locator(".vtalk-convo-time")).toHaveText("Now");

  // It arrives, and reading it starts its burn.
  const inRow = p.locator(".vtalk-convo", { hasText: "ankush" }).first();
  await expect(inRow.locator(".vtalk-convo-pv")).toHaveText("Hello from the conversation spec");
  await inRow.click();
  const msg = p.locator(".vtalk-msg--in", { hasText: "Hello from the conversation spec" });
  await expect(msg.locator(".vtalk-bubble-burn")).toHaveText(/^Burns in \d/);
  await expect(p.locator(".vtalk-day").first()).toHaveText("Today");

  // Comparing numbers and saying they match turns the pill into the verdict.
  await p.locator(".vtalk-verify-btn").click();
  await p.locator(".vtalk-verify-toggle input").check();
  await expect(p.locator(".vtalk-verify-btn")).toHaveText("Safety number verified");
  await a.context().close();
  await p.context().close();
});

// A burned message leaves nothing behind it: not its bubble, not its preview
// on the list, not a day heading over nothing.
test("a burned message takes its preview with it", async ({ browser }) => {
  const a = await talkAs(browser, ANKUSH);
  const p = await talkAs(browser, PRIYA);
  await startChat(a, PRIYA);
  await send(a, "This one stays a while", "300");
  await send(a, "This one burns at once", "5");
  const inRow = p.locator(".vtalk-convo", { hasText: "ankush" }).first();
  await expect(inRow.locator(".vtalk-convo-pv")).toHaveText("This one burns at once");
  await inRow.click();
  const fast = p.locator(".vtalk-msg--in", { hasText: "This one burns at once" });
  await expect(fast.locator(".vtalk-bubble-burn")).toHaveText(/^Burns in [1-5]s$/);
  await expect(fast).toHaveCount(0, { timeout: 12000 });
  await expect(inRow.locator(".vtalk-convo-pv")).toHaveText("This one stays a while");
  await expect(p.locator(".vtalk-msg--in", { hasText: "This one stays a while" })).toBeVisible();
  await a.context().close();
  await p.context().close();
});

// "Chatting as" changes whose safety number the panel shows: a number read out
// for the mailbox just switched away from would vouch for the wrong key.
test("your safety number follows who you are chatting as", async ({ browser }) => {
  const a = await talkAs(browser, ANKUSH);
  await a.locator("summary.vtalk-tool").first().click();
  const num = a.locator("[data-self-safety]");
  await expect(num).not.toHaveText("—");
  const ankushNumber = await num.textContent();
  await a.locator("summary.vtalk-tool").first().click();
  await a.locator("#vtalk-as").selectOption(PRIYA);
  await a.locator("summary.vtalk-tool").first().click();
  await expect(num).not.toHaveText("—");
  await expect(num).not.toHaveText(ankushNumber || "");
  await a.context().close();
});

// A link with an address opens New chat with it filled in; Start is still the
// reader's to press.
test("a chat link opens New chat with the address", async ({ page }) => {
  await page.goto("/os/talk?t=" + encodeURIComponent(PRIYA));
  await expect(page.locator(".vtalk-new")).toHaveAttribute("open", "");
  await expect(page.locator("#vtalk-peer")).toHaveValue(PRIYA);
  await expect(page.locator("#vtalk-main")).toHaveAttribute("data-empty", "1");
});

// On a phone the list is a screen and a conversation is pushed over it, with
// its own way back.
test("on a phone, a conversation is pushed over the list", async ({ browser }) => {
  const a = await talkAs(browser, ANKUSH, { width: 390, height: 844 });
  await startChat(a, PRIYA);
  await expect(a.locator(".vtalk")).toHaveAttribute("data-view", "thread");
  await expect(a.locator(".vtalk-side")).toBeHidden();
  await expect(a.locator(".vtalk-top")).toBeHidden();
  await a.locator(".vtalk-back").click();
  await expect(a.locator(".vtalk")).toHaveAttribute("data-view", "list");
  await expect(a.locator(".vtalk-side")).toBeVisible();
  await expect(a.locator("#vtalk-main")).toBeHidden();
  expect(await a.evaluate(() => document.scrollingElement.scrollWidth <= window.innerWidth), "the page scrolls sideways").toBe(true);
  await a.context().close();
});

// Talk asks only for pictures that exist: a mailbox with one shows it, and a
// mailbox without one is never asked for (each such ask was a 404, an error
// in the console for every contact without a picture).
test("a picture is shown where there is one, and never asked for otherwise", async ({ browser }) => {
  const admin = await browser.newPage({ baseURL: base });
  await admin.goto("/os/vayumail/accounts");
  const csrf = decodeURIComponent((await admin.context().cookies()).find((c) => c.name === "vp_csrf").value);
  const local = "pic" + Date.now().toString(36), pictured = local + "@mail.test";
  expect((await admin.request.post("/os/vayumail/accounts/create", { headers: { "X-CSRF-Token": csrf }, data: { local, pass: "e2e-mailbox-pass-" + local, role: "mailbox" } })).ok()).toBe(true);
  const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");
  const up = await admin.request.post("/os/vayumail/accounts/avatar", { headers: { "X-CSRF-Token": csrf }, multipart: { email: pictured, avatar: { name: "a.png", mimeType: "image/png", buffer: png } } });
  expect(up.ok(), "the picture upload: " + up.status()).toBe(true);
  await admin.close();

  const asked = [];
  const a = await (await browser.newContext({ baseURL: base, viewport: { width: 1440, height: 900 } })).newPage();
  a.on("request", (r) => { if (r.url().includes("/os/vayumail/accounts/avatar?")) asked.push(new URL(r.url()).searchParams.get("email")); });
  await a.goto("/os/talk");
  if ((await a.locator("#vtalk-as").inputValue()) !== ANKUSH) await a.locator("#vtalk-as").selectOption(ANKUSH);
  await expect(a.locator("#vtalk-status")).toHaveAttribute("data-state", "online");
  await startChat(a, pictured);
  await expect(a.locator(".vtalk-convo", { hasText: local }).first().locator(".vtalk-avatar img")).toBeVisible();
  await startChat(a, PRIYA);
  await expect(a.locator(".vtalk-convo", { hasText: "priya" }).first().locator(".vtalk-avatar")).toHaveText(/^[A-Z]/);
  expect(asked.filter((e) => e !== pictured), "pictures asked for mailboxes that have none").toEqual([]);
  await a.context().close();
});
