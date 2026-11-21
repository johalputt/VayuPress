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
    // A word is English when every letter of it is plain ASCII: the
    // pseudo-language accents its words, and a, e, i, o and u always. A
    // sentence's word is lowercase or capitalised, three letters or more:
    // "Add the slot" is English; "px", an acronym (URL, CLI), a token (AIza…)
    // and "try", which has no letter the pseudo-language accents, are not.
    const english = (w) => w.length >= 3 && /^[A-Z]?[a-z]+$/.test(w) && /[aeiou]/i.test(w);
    // A product's name shown whole, the capability tokens a page lists (its
    // data-section values) and the themes it lists (data-preset, data-name)
    // are names rather than sentences.
    const products = new Set(["IndexNow", "OpenRouter", "OpenAI", "Pixabay", "YouTube Data API", "Vayu Compatibility Bible"]);
    const tokens = new Set([...document.querySelectorAll("[data-section]")].map((el) => el.getAttribute("data-section")));
    for (const el of document.querySelectorAll("[data-preset], [data-name]")) tokens.add(el.getAttribute("data-preset") || el.getAttribute("data-name"));
    const names = new Set(["Authorization", "Bearer", "VayuOS", "VayuMail", "VayuPress", "VayuTalk", "VayuMCP", "VayuShield", "VayuVeil", "VayuFlow", "VayuKeep", "YouTube", "Tor", "PGP", "DNS", "SEO", "API", "CSS", "PNG", "ICO", "Analytics", "Audience", "Monetization", "Theme", "Site", "Backups", "Storage", "Shield", "Talk", "Buzz", "Claude", "Code", "Argon2id", "UTC", "ReadWritePaths"]);
    const out = new Set();
    const check = (text, where) => {
      if (products.has(text.trim())) return;
      // A {placeholder} or <placeholder> a sentence shows as it is, an
      // address, a path and a section:action token.
      for (const part of text.replace(/\{[a-z]+\}|<[a-z]+>/g, " ").split(/\s+/)) {
        if (part.includes("@") || part.includes("/") || part.includes(":") || /\w\.\w/.test(part)) continue;
        // A header's name (X-API-Key) is code.
        if (/^[A-Z][A-Za-z]*(-[A-Z][A-Za-z]*)+\W*$/.test(part)) continue;
        for (const w of part.match(/\p{L}+/gu) || []) {
          if (english(w) && !names.has(w) && !tokens.has(w)) { out.add(where + ": " + text.trim().slice(0, 80)); break; }
        }
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

// pseudoPageAndSheets is what a console page in the pseudo-language still
// says in English: the page itself, then each sheet it opens, read while open.
// withSheets says the page opens at least one, or is asked of the page as
// drawn: a page whose sheets went missing would otherwise pass with nothing
// read.
// ready, when given, waits for what the page draws after it loads.
async function pseudoPageAndSheets(page, path, withSheets, ready) {
  await page.goto(path + "?lang=en-XA");
  await expect(page.locator("html")).toHaveAttribute("lang", "en-XA");
  if (ready) await ready(page);
  const found = {};
  const own = await englishLeft(page);
  if (own.length) found.page = own;
  const sheets = await page.locator("button[data-sheet]").evaluateAll((bs) => [...new Set(bs.map((b) => b.getAttribute("data-sheet")))]);
  expect(sheets.length > 0).toBe(typeof withSheets === "function" ? await withSheets(page) : withSheets);
  for (const id of sheets) {
    await page.locator('button[data-sheet="' + id + '"]').first().click();
    const sheet = page.locator("#" + id);
    await expect(sheet).toBeVisible();
    const english = await englishLeft(page);
    if (english.length) found[id] = english;
    await sheet.locator("[data-sheet-close]").click();
    await expect(sheet).toBeHidden();
  }
  return found;
}

// The spec install has no mail relay, so the Newsletter is its setup page,
// with no composer to open; the composer's words are checked in Go.
// Advertising is its setup page until stillair.spec.js turns it on, at a
// moment of its own, and that file's setup test needs it off: this one
// changes nothing, and reads the page it finds, its sheets when it is on.
const adsIsOn = async (page) => (await page.locator("main [data-page-kind]").first().getAttribute("data-page-kind")) === "settings";
// The Theme Studio writes its status once its saved theme has loaded, and is
// read after that.
const studioReady = (page) => expect(page.locator("[data-theme-status]")).not.toBeEmpty();
for (const [name, path, withSheets, ready] of [["Backups", "/os/vayukeep", true], ["API keys", "/os/apikeys", true], ["Newsletter", "/os/newsletter", false],
  ["YouTube", "/os/youtube", true], ["Advertising", "/os/ads", adsIsOn], ["VayuTor", "/os/tor", false], ["Tools and plugins", "/os/tools", false],
  ["Theme Studio", "/os/theme", false, studioReady], ["The theme store", "/os/theme/store", false]]) {
  test(name + " in another language has no English left on it", async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    expect(await pseudoPageAndSheets(page, path, withSheets, ready)).toEqual({});
    expect(errors).toEqual([]);
  });
}

// Updates checks for a release as it loads, and draws the release's notes
// only where the release feed answers: CI's install reaches it, this one's
// may not. The check is answered here with a release of its own, so the
// notes' card is always drawn, and read once it is. Its change lines are
// the release's words, as written.
test("Updates in another language has no English left on it", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const notes = ["## [9.9.9] — 2026-11-21", "", "### Security", "", "- **A security change.**", "", "### Added", "",
    "- **An addition.**", "- **Another addition.**", "", "### Upgrade Notes", "", "- **Read this first.**", "",
    "### Something else", "", "- **A change.**", "- **One more change.**", "- **And the last.**"].join("\n");
  await page.route("**/os/api/update/check*", (r) => r.fulfill({
    json: { latest: "v9.9.9", available: true, canApply: false, source: "github", notes },
  }));
  const ready = (p) => expect(p.locator("[data-update-notes] .upd-notes__more")).toBeVisible();
  expect(await pseudoPageAndSheets(page, "/os/update", false, ready)).toEqual({});
  expect(errors).toEqual([]);
});
