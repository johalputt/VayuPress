// Console pages run their scripts. admin-os.js is one file of init blocks, one
// per page; a duplicated opening line once wrapped every block after the
// dashboard's activity feed inside it, so on every other page they returned
// before running — the Media library sat on its loading tiles forever and its
// uploads did nothing, with no error anywhere. A page whose script never runs
// looks exactly like a page that is still loading; this waits for the result.
const { test, expect } = require("@playwright/test");

test("the media library loads its list", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const listed = page.waitForResponse((r) => r.url().includes("/os/api/media") && r.status() === 200);
  await page.goto("/os/media");
  await listed;
  await expect(page.locator("[data-media-list] .skeleton")).toHaveCount(0);
  expect(errors).toEqual([]);
});

// The same file once closed its wrapper too EARLY: the j/k/x/Enter/n keyboard
// layer sat after the closing line and called a helper that only exists inside
// it, so every one of those keys threw on every console page and did nothing.
test("the list keyboard shortcuts run", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/os/posts");
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("j");
  await page.keyboard.press("k");
  expect(errors).toEqual([]);
});

// hx-confirm prompts go through the console's own dialog. HTMX falls back to the
// browser's native confirm() unless its htmx:confirm event is answered, and for
// a long time nothing answered it. Cancelled here, so the test changes nothing.
test("an hx-confirm asks in the console's dialog, not the browser's", async ({ page }) => {
  const errors = [];
  let native = 0;
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("dialog", (d) => { native++; d.dismiss(); });
  await page.goto("/os/shield");
  await page.waitForLoadState("networkidle"); // HTMX must have processed the button
  // It lives in a collapsed advanced row; what is under test is the prompt, so
  // the click is dispatched to it directly.
  await page.locator("button", { hasText: "Release all sentences now" }).dispatchEvent("click");
  await expect(page.locator(".vp-confirm")).toBeVisible();
  await page.locator(".vp-confirm").getByRole("button", { name: "Cancel" }).click();
  await expect(page.locator(".vp-confirm")).toHaveCount(0);
  expect(native).toBe(0);
  expect(errors).toEqual([]);
});

// A change re-renders the page in place (vpRefresh) instead of reloading it: a
// reload threw away the toast that confirmed the change. And the controls in
// the re-rendered markup must still work, which is what delegated listeners
// are for — so the second action here is on the refreshed list.
test("a change refreshes the page in place, and the new controls work", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/os/members");
  await page.waitForLoadState("networkidle");
  let navigations = 0;
  page.on("framenavigated", (f) => { if (f === page.mainFrame()) navigations++; });

  const name = "E2E tier " + Date.now();
  await page.locator("[data-new-tier]").click();
  await page.locator("#tier-name").fill(name);
  await page.locator("#tier-save").click();
  await expect(page.locator(".toast", { hasText: "Tier created." })).toBeVisible();
  const row = page.locator(`[data-edit-tier][data-name="${name}"]`);
  await expect(row).toHaveCount(1);

  await row.click();
  await expect(page.locator("#tier-modal")).toBeVisible();
  await expect(page.locator("#tier-name")).toHaveValue(name);
  expect(navigations).toBe(0);
  expect(errors).toEqual([]);
});

// Content › YouTube asks for the channel and answers in place. An address on
// another host is refused before anything is fetched, so this needs no
// network, and the page keeps its form for the next try.
test("YouTube finds a channel in place, and refuses another host's address", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/os/youtube");
  await expect(page.locator("h1")).toContainText("YouTube");
  await page.getByRole("button", { name: "Follow a channel" }).first().click();
  await page.locator("#yt-address").fill("https://example.com/@somebody");
  const answered = page.waitForResponse((r) => r.url().includes("/os/api/youtube/resolve"));
  await page.getByRole("button", { name: "Find the channel" }).click();
  expect((await answered).status()).toBe(200);
  await expect(page.locator("#yt-preview")).toContainText("That address is not on YouTube.");
  await expect(page.locator("#yt-address")).toHaveValue("https://example.com/@somebody");
  expect(errors).toEqual([]);
});

// A script's sentence in the page's language: vpT reads the page's #vp-words
// and fills it as i18n.T does on the server (an exact =1 branch first, then
// the language's own plural rule, # for the number, names inside branches).
// A German block is put on a console page as the server puts it: in the
// <head> of the page as served, before any script runs, since a page's own
// script may say a sentence as it loads and vpT reads the block once.
test("a script says its sentences in the page's language", async ({ page }) => {
  const words = JSON.stringify({
    "{n, plural, =1 {Just one {what}} one {# {what}} other {# {what}s}}": "{n, plural, =1 {Nur eine {what}} one {# {what}} other {# {what}en}}",
    "Uploading {file}…": "{file} wird hochgeladen…",
  });
  await page.route(/\/os\/$/, async (route) => {
    const res = await route.fetch();
    const body = (await res.text())
      .replace(/<html lang="[^"]*"/, '<html lang="de"')
      .replace("</head>", '<script type="application/json" id="vp-words">' + words + "</script></head>");
    await route.fulfill({ response: res, body });
  });
  await page.goto("/os/");
  await expect(page.locator("html")).toHaveAttribute("lang", "de");
  const said = await page.evaluate(() => {
    const count = "{n, plural, =1 {Just one {what}} one {# {what}} other {# {what}s}}";
    return [
      window.vpT("Uploading {file}…", { file: "a.txt" }),
      window.vpT(count, { n: 1, what: "Datei" }),
      window.vpT(count, { n: 3, what: "Datei" }),
      window.vpT("A sentence the block does not have", {}),
    ];
  });
  expect(said).toEqual(["a.txt wird hochgeladen…", "Nur eine Datei", "3 Dateien", "A sentence the block does not have"]);
});

// A site's own code runs on its public pages by its hash, under the page's
// real policy: the hash the server puts in script-src is the one this
// browser computes, or the script is refused and the page says so in the
// console. An inline handler, which no hash can allow, is refused on save.
// The snippet is a script that draws nothing, and it is taken off at the
// end: other specs read the same public pages at the same time.
test("a site's own code runs on its public pages by its hash", async ({ page }) => {
  const save = async (head) => {
    await page.goto("/os/website/services");
    await page.locator('[data-snippet="snip-head"]').fill(head);
    await page.locator('[data-snippet="snip-foot"]').fill("");
    const answered = page.waitForResponse((r) => r.url().includes("/os/api/website/snippets"));
    await page.locator("[data-snippets-save]").click();
    return answered;
  };
  const refused = await save('<button onclick="x()">x</button>');
  expect(refused.status()).toBe(400);
  await expect(page.locator(".toast", { hasText: "inline handler" })).toBeVisible();

  const mark = "e2e" + Date.now().toString(36);
  const saved = await save(`<script>\r\ndocument.documentElement.dataset.vpSnippet = "${mark}";\r\n</script>`);
  expect(saved.status(), await saved.text()).toBe(200);
  try {
    const blocked = [];
    page.on("console", (m) => { if (/Content Security Policy/i.test(m.text())) blocked.push(m.text()); });
    await page.goto("/");
    await expect(page.locator("html")).toHaveAttribute("data-vp-snippet", mark);
    expect(blocked).toEqual([]);
  } finally {
    expect((await save("")).status()).toBe(200);
  }
  await page.goto("/");
  await expect(page.locator("html")).not.toHaveAttribute("data-vp-snippet", mark);
});

// englishLeft is what a console page in the pseudo-language (every sentence
// accented and made longer) still says in English: a word with a plain vowel,
// outside what the install holds (its people, addresses and paths), is a
// sentence that missed i18n.T or vpT. Visible text and the labels a screen
// reader reads both count.
async function englishLeft(page) {
  return page.evaluate(() => {
    // What the install holds (a field's value, a person's name or address)
    // and what is a name rather than a sentence.
    const data = "kbd, code, pre, .mono, textarea, option[lang], [translate=no], .sa-mark, .sa-appname, .sa-menu__name, .sa-sites__btn";
    const plain = /(^|[^\p{L}])([A-Za-z]*[aeiou][A-Za-z]{2,})(?=$|[^\p{L}])/u;
    const names = new Set(["VayuOS", "VayuMail", "VayuPress", "VayuTalk", "VayuMCP", "VayuShield", "VayuVeil", "VayuFlow", "VayuKeep", "YouTube", "Tor", "PGP", "DNS", "SEO", "API", "CSS", "PNG", "ICO", "Analytics", "Audience", "Monetization", "Theme", "Site", "Backups", "Storage", "Shield", "Talk", "Buzz", "Claude", "Code", "Argon2id", "UTC", "ReadWritePaths"]);
    const out = new Set();
    const check = (text, where) => {
      // A {placeholder} a sentence shows as it is, an address, and a path.
      for (const part of text.replace(/\{[a-z]+\}/g, " ").split(/\s+/)) {
        if (part.includes("@") || part.includes("/")) continue;
        const m = part.match(plain);
        if (m && !names.has(m[2])) out.add(where + ": " + text.trim().slice(0, 80));
      }
    };
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const el = n.parentElement;
      if (!el || el.closest(data) || el.closest("script, style, template") || !n.textContent.trim()) continue;
      if (!el.checkVisibility({ visibilityProperty: true })) continue;
      check(n.textContent, el.tagName.toLowerCase());
    }
    for (const el of document.body.querySelectorAll("[aria-label], [title], [placeholder]")) {
      if (el.closest(data)) continue;
      for (const a of ["aria-label", "title", "placeholder"]) if (el.hasAttribute(a)) check(el.getAttribute(a), "@" + a);
    }
    return [...out];
  });
}

// Settings in the pseudo-language has no English left on any of its pages.
test("Settings in another language has no English left on it", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const found = {};
  for (const slug of ["general", "appearance", "navigation", "footer", "design", "writing", "members", "team", "email", "advanced"]) {
    await page.goto("/os/settings/" + slug + "?lang=en-XA");
    await expect(page.locator("html")).toHaveAttribute("lang", "en-XA");
    // The bell is the shell's, and its items are said in the page's
    // language too: open on one page, so its panel is read with the rest.
    if (slug === "general") {
      await page.locator("[data-notif-toggle]").click();
      await expect(page.locator("[data-notif-panel]")).toBeVisible();
    }
    const english = await englishLeft(page);
    if (english.length) found[slug] = english;
  }
  expect(found).toEqual({});
  expect(errors).toEqual([]);
});

// Backups in the pseudo-language has no English left on it: the page, and
// each sheet it opens, read while open.
test("Backups in another language has no English left on it", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/os/vayukeep?lang=en-XA");
  await expect(page.locator("html")).toHaveAttribute("lang", "en-XA");
  const found = {};
  const page1 = await englishLeft(page);
  if (page1.length) found.page = page1;
  const sheets = await page.locator("button[data-sheet]").evaluateAll((bs) => [...new Set(bs.map((b) => b.getAttribute("data-sheet")))]);
  expect(sheets.length).toBeGreaterThan(0);
  for (const id of sheets) {
    await page.locator('button[data-sheet="' + id + '"]').first().click();
    const sheet = page.locator("#" + id);
    await expect(sheet).toBeVisible();
    const english = await englishLeft(page);
    if (english.length) found[id] = english;
    await sheet.locator("[data-sheet-close]").click();
    await expect(sheet).toBeHidden();
  }
  expect(found).toEqual({});
  expect(errors).toEqual([]);
});
