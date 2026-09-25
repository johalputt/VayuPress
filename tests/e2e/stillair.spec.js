// The Still Air console, walked the way an operator walks it.
//
// Two kinds of check. The design lint visits every app and section the rail
// offers, at desktop and phone width, and fails on the faults this redesign
// kept finding by eye: text escaped twice, an emoji drawn as an icon, a page wider than the screen,
// a label in capitals, a colour written into the markup, a heading repeated by
// the card beneath it, an icon rendered at the wrong size, a script error. They
// are checked on the rendered page, not by comparing pixels, so a different
// font rasteriser on another machine cannot turn them red.
//
// The keyboard checks drive the command bar, the account menu and the
// confirmation dialog without a pointer.
const { test, expect } = require("@playwright/test");

// Opens the console and waits until the first visit has settled: without a
// session it loads itself once more before the CSRF cookie exists, and a test
// that acts before then is refused.
async function openConsole(page) {
  await page.goto("/os");
  await page.waitForLoadState("networkidle");
  await page.waitForFunction(() => /(?:^|;\s*)vp_csrf=/.test(document.cookie));
  await expect(page.locator("body[data-ui='still-air']")).toHaveCount(1);
}

// What the lint looks for on one rendered page. Returns a list of findings.
function lintPage() {
  const out = [];
  const main = document.querySelector("main#main-content");
  if (!main) return ["no main landmark"];
  const emoji = /[\u{1F000}-\u{1FFFF}☀-⛿✀-➿⬅-⬇⏩-⏺]/u;
  const typographic = /[✓✕✦✗✔✖❯❮]/u;
  const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  let n;
  while ((n = walk.nextNode())) {
    // Text escaped twice shows its entity to the reader ("R&amp;D").
    const ent = n.nodeValue.match(/&(?:amp|lt|gt|quot|#\d+);/);
    if (ent && !n.parentElement.closest("script,style,code,pre,textarea")) {
      out.push(`escaped twice: "${n.nodeValue.trim().slice(0, 40)}"`);
    }
    // Capitals written into the text rather than applied by CSS: a label in
    // capitals reads as shouting. Protocol names are written that way.
    const shouted = n.nodeValue.match(/\b[A-Z]{5,}\b/g);
    if (shouted && n.nodeValue.trim() === n.nodeValue.trim().toUpperCase() && !n.parentElement.closest("script,style,code,pre,kbd,textarea,.mono,.font-mono") &&
        shouted.some((w) => !["STARTTLS", "DMARC"].includes(w))) {
      out.push(`written in capitals: "${n.nodeValue.trim().slice(0, 40)}"`);
    }
    const m = n.nodeValue.match(emoji);
    if (m && !typographic.test(m[0]) && !n.parentElement.closest("script,style")) {
      out.push(`emoji ${m[0]} in "${n.nodeValue.trim().slice(0, 40)}"`);
    }
  }
  if (main.scrollWidth > main.clientWidth + 2) out.push(`page wider than the screen (${main.scrollWidth} > ${main.clientWidth})`);
  document.querySelectorAll(".section-head").forEach((h) => {
    const title = (h.querySelector(".section-head__title") || h).textContent.trim().toLowerCase();
    const next = h.nextElementSibling;
    const inner = next && next.querySelector(".card-title, .mon-acc__title, h2, h3");
    if (inner && inner.textContent.trim().toLowerCase() === title) out.push(`heading "${title}" repeated by the card below it`);
  });
  main.querySelectorAll("*").forEach((e) => {
    if (!e.offsetParent) return;
    const cs = getComputedStyle(e);
    if (cs.textTransform === "uppercase" && e.textContent.trim().length > 3 && !e.closest(".vm-av, .vm-acct__avatar")) {
      out.push(`capitals on <${e.tagName.toLowerCase()} class="${e.className}">`);
    }
  });
  // A colour written into the markup ignores the scheme. Theme swatches and
  // store previews are exempt: they show the colours of a theme on purpose.
  main.querySelectorAll("[style*='color']").forEach((e) => {
    const s = e.getAttribute("style");
    if (/#[0-9a-f]{3,6}|rgb/i.test(s) && e.offsetParent && !e.closest(".cz-a11y, .store-card, [data-swatch]")) {
      out.push(`inline colour on <${e.tagName.toLowerCase()} class="${e.className}">`);
    }
  });
  main.querySelectorAll("svg.sa-ico").forEach((s) => {
    const r = s.getBoundingClientRect();
    if (s.offsetParent !== null && (r.width > 64 || r.height > 64)) out.push(`icon drawn at ${Math.round(r.width)}px`);
  });
  return [...new Set(out)];
}

// The Still Air scale, judged on what the browser rendered rather than on what
// the stylesheet says: a rule can be on the scale and still lose to another, a
// class can be emitted that no rule on the scale reaches, and the browser's
// own defaults (a <strong> is 700) never appear in the sheet at all. Every
// element with a box is held to plan §6: a colour that is a token's value, a
// type size on the scale, weights 400/500/600, Inter or JetBrains Mono, radii
// 4/6/10 (pills only on things that are physically round), elevation shadows
// or state rings, and none of the decoration the classic console wore.
async function scaleLint(colourTokens) {
  // Judge settled values: a colour caught in the middle of a transition (a
  // status flipping from offline to online) is neither token.
  await Promise.all(document.getAnimations().filter((a) => a instanceof CSSTransition).map((a) => a.finished.catch(() => {})));
  const probe = document.createElement("i");
  probe.style.setProperty("transition", "none", "important"); // the reduced-motion rule animates colour
  document.body.appendChild(probe);
  const resolve = (value, prop) => {
    probe.style[prop] = "";
    probe.style[prop] = value;
    return getComputedStyle(probe)[prop];
  };
  const palette = new Set(["rgba(0, 0, 0, 0)"]);
  for (const t of colourTokens) palette.add(resolve(`var(${t})`, "color"));
  const elevation = new Set(["none", ...["--shadow-3", "--shadow-4", "--shadow-5"].map((t) => resolve(`var(${t})`, "boxShadow"))]);
  probe.remove();
  const sizes = new Set(["12px", "12.5px", "13px", "14px", "15px", "20px", "28px"]);
  // A theme's own colours and type are shown on purpose where a theme is
  // previewed or its swatches are picked.
  const showsATheme = ".theme-card__art, .store-card__preview, .theme-card__preview, .cz-a11y, [data-swatch], .customizer__frame, .store-preview, .vp-flag-img, .sa-mark";
  const out = [];
  const name = (e) => `<${e.tagName.toLowerCase()} class="${typeof e.className === "string" ? e.className : ""}">`;
  // A state ring (0 0 0 Npx) or an inset bar, in palette colours, marks a
  // selected or active thing; anything blurred is a glow.
  const isStateMark = (shadow) =>
    shadow.split(/,(?![^(]*\))/).every((part) => {
      const m = part.trim().match(/^(rgba?\([^)]*\)) -?[\d.]+px -?[\d.]+px ([\d.]+)px -?[\d.]+px( inset)?$/);
      return m && palette.has(m[1]) && m[2] === "0";
    });
  const painted = new Set(["rect", "circle", "ellipse", "path", "polygon", "polyline", "text", "tspan"]);
  for (const e of document.body.querySelectorAll("*")) {
    if (!e.getClientRects().length || e.closest(showsATheme) || e.closest("script, style, template")) continue;
    const cs = getComputedStyle(e);
    if (cs.visibility === "hidden") continue;
    const flag = (what) => out.push(`${what} on ${name(e)}`);
    if ([...e.childNodes].some((n) => n.nodeType === 3 && n.nodeValue.trim())) {
      if (!palette.has(cs.color)) flag(`text colour ${cs.color}`);
      if (!sizes.has(cs.fontSize)) flag(`type size ${cs.fontSize}`);
      if (!["400", "500", "600"].includes(cs.fontWeight)) flag(`weight ${cs.fontWeight}`);
      const family = cs.fontFamily.split(",")[0].replace(/["']/g, "").trim();
      if (family !== "Inter" && family !== "JetBrains Mono") flag(`typeface ${family}`);
      if (parseFloat(cs.letterSpacing) > 0) flag(`tracking ${cs.letterSpacing}`);
      if (cs.textShadow !== "none") flag("text shadow");
    }
    if (!palette.has(cs.backgroundColor)) flag(`background ${cs.backgroundColor}`);
    for (const side of ["Top", "Right", "Bottom", "Left"]) {
      if (parseFloat(cs[`border${side}Width`]) > 0 && cs[`border${side}Style`] !== "none" && !palette.has(cs[`border${side}Color`])) {
        flag(`border ${cs[`border${side}Color`]}`);
        break;
      }
    }
    if (painted.has(e.tagName.toLowerCase())) {
      for (const p of ["fill", "stroke"]) {
        if (cs[p] !== "none" && !cs[p].startsWith("url") && !palette.has(cs[p])) flag(`${p} ${cs[p]}`);
      }
    }
    if (/gradient\(/.test(cs.backgroundImage)) flag("gradient");
    // A dropdown looks like one: the Still Air chevron, never the browser's
    // own select and never none at all.
    if (e.tagName === "SELECT" && !e.multiple && e.size <= 1) {
      if (cs.appearance !== "none") flag("a native select");
      else if (!cs.backgroundImage.startsWith('url("data:image/svg+xml')) flag("a select with no arrow");
    }
    if (cs.backdropFilter && cs.backdropFilter !== "none") flag("backdrop blur");
    if (cs.filter !== "none" && e.tagName !== "IMG") flag(`filter ${cs.filter}`);
    if (!elevation.has(cs.boxShadow) && !isStateMark(cs.boxShadow)) flag(`shadow ${cs.boxShadow}`);
    const box = e.getBoundingClientRect();
    // Physically round: a circle, or the one toggle track Still Air draws
    // (30 x 18, radius 9). A toggle at any other size is not the Still Air one.
    const round = Math.abs(box.width - box.height) < 2 || (Math.round(box.width) === 30 && Math.round(box.height) === 18);
    for (const corner of ["TopLeft", "TopRight", "BottomRight", "BottomLeft"]) {
      const r = parseFloat(cs[`border${corner}Radius`]);
      if (!r || [4, 6, 10].includes(r) || (round && r >= Math.min(box.width, box.height) / 2 - 0.5)) continue;
      flag(`radius ${cs[`border${corner}Radius`]}`);
      break;
    }
  }
  return [...new Set(out)];
}

// The colour tokens, read from the stylesheet the server under test ships (it
// is served minified). The first Still Air block is the dark one and names
// every colour token; the light blocks redefine the same names.
async function colourTokens(page) {
  const css = await page.evaluate(() => fetch("/os/static/css/vayuos.css").then((r) => r.text()));
  const start = css.indexOf('.vp-os[data-ui="still-air"]{');
  const block = css.slice(start, css.indexOf("}", start));
  return [...block.matchAll(/(--[a-z0-9-]+):\s*(?:#|rgba?\()/g)].map((m) => m[1]);
}

test("every element of every page is on the Still Air scale, in both schemes", async ({ page }) => {
  test.setTimeout(600000);
  await openConsole(page);
  const tokens = await colourTokens(page);
  expect(tokens.length).toBeGreaterThan(30); // an empty palette would fail everything for the wrong reason
  const hrefs = await page.evaluate(() => [...new Set([...document.querySelectorAll("[data-sa-index] a")].map((a) => a.getAttribute("href")))]);
  expect(hrefs.length).toBeGreaterThan(30);
  const findings = [];
  for (const [scheme, width] of [["dark", 1280], ["light", 1280], ["dark", 390]]) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.setViewportSize({ width, height: 900 });
    for (const href of hrefs) {
      await page.goto(href);
      await page.waitForLoadState("load");
      for (const f of await page.evaluate(scaleLint, tokens)) findings.push(`${scheme} ${width}px ${href}: ${f}`);
    }
  }
  expect(findings).toEqual([]);
});

test("every app and section passes the design lint, on a desktop and a phone", async ({ page }) => {
  test.setTimeout(300000);
  await openConsole(page);
  const hrefs = await page.evaluate(() => [...new Set([...document.querySelectorAll("[data-sa-index] a")].map((a) => a.getAttribute("href")))]);
  expect(hrefs.length).toBeGreaterThan(30); // the index is the rail's, so an empty one means the walk checked nothing

  const findings = [];
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 });
    for (const href of hrefs) {
      const errors = [];
      const onError = (e) => errors.push(e.message);
      page.on("pageerror", onError);
      await page.goto(href);
      await page.waitForLoadState("load");
      const found = await page.evaluate(lintPage);
      page.off("pageerror", onError);
      for (const f of [...found, ...errors.map((e) => "script error: " + e)]) findings.push(`${width}px ${href}: ${f}`);
    }
  }
  expect(findings).toEqual([]);
});

test("the command bar is operated by keyboard alone", async ({ page }) => {
  await openConsole(page);
  await page.keyboard.press("Control+k");
  const input = page.locator(".cmd-input");
  await expect(input).toBeFocused();
  await input.fill("backups");
  await expect(page.locator(".cmd-item", { hasText: "Backups" }).first()).toBeVisible();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/os\/vayukeep/);

  // Escape closes it and hands focus back to what had it.
  await page.locator(".sa-search").focus();
  await page.keyboard.press("Control+k");
  await expect(input).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(input).toBeHidden();
});

test("the account menu is operated by keyboard alone", async ({ page }) => {
  await openConsole(page);
  const menu = page.locator("details.sa-pop").last();
  const summary = menu.locator("summary");
  await summary.focus();
  await page.keyboard.press("Enter");
  await expect(menu).toHaveAttribute("open", "");
  // The arrow keys walk the visible items and the colour-scheme control, in
  // page order. A hidden item (Install app, when the browser offers nothing to
  // install) is skipped.
  const items = menu.locator("[role='menuitem']:not([hidden]), .sa-seg__opt");
  await expect(items.first()).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await expect(items.nth(1)).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).not.toHaveAttribute("open", "");
  await expect(summary).toBeFocused();
});

test("a confirmation dialog keeps focus and gives it back", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/storage");
  const opener = page.locator("[data-cache-clear]");
  await opener.focus();
  await page.keyboard.press("Enter");
  const dialog = page.locator(".vp-confirm");
  await expect(dialog).toBeVisible();
  const confirm = dialog.getByRole("button", { name: "Clear caches" });
  const cancel = dialog.getByRole("button", { name: "Cancel" });
  await expect(confirm).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(cancel).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(confirm).toBeFocused(); // focus stays inside the dialog
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
});

// Settings compares every row with the value it loaded with: the bar counts
// real changes, Discard puts them back, Save sends them and they survive a
// reload. Typing a letter and deleting it is no change.
test("a settings change is marked, counted, discarded and saved", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/settings");
  const tagline = page.locator("#s-tagline");
  const author = page.locator("#s-author");
  const row = (el) => el.locator("xpath=ancestor::div[contains(concat(' ',@class,' '),' settings-row ')][1]");
  const bar = page.locator("[data-settings-bar]");
  const count = page.locator("[data-settings-count]");
  const saved = await tagline.inputValue();

  await tagline.press("End");
  await tagline.type("x");
  await expect(bar).toBeVisible();
  await tagline.press("Backspace");
  await expect(bar).toBeHidden();
  await expect(row(tagline)).not.toHaveClass(/is-changed/);

  await tagline.fill("Written by the e2e walk");
  await author.fill("Someone else");
  await expect(row(tagline)).toHaveClass(/is-changed/);
  await expect(count).toHaveText("2 unsaved changes");
  await page.locator("[data-settings-discard]").click();
  await expect(bar).toBeHidden();
  await expect(tagline).toHaveValue(saved);
  await expect(row(tagline)).not.toHaveClass(/is-changed/);

  await tagline.fill("Written by the e2e walk");
  await expect(count).toHaveText("1 unsaved change");
  await page.locator("[data-settings-save]").click();
  await expect(bar).toBeHidden();
  await page.reload();
  await expect(page.locator("#s-tagline")).toHaveValue("Written by the e2e walk");

  // Leave the install as it was found.
  await page.locator("#s-tagline").fill(saved);
  await page.locator("[data-settings-save]").click();
  await expect(bar).toBeHidden();
});

// The search in the Settings sidebar finds a row by its words and lands on it.
test("search settings finds a row and lands on it", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/settings/appearance");
  const find = page.locator("[data-settings-search]");
  await find.fill("zzzz");
  await expect(page.locator("[data-settings-none]")).toBeVisible();
  await find.fill("time zone");
  const hit = page.locator(".sa-find__item:visible");
  await expect(hit).toHaveCount(1);
  await expect(hit).toContainText("Site identity");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/os\/settings#s-timezone$/);
  await expect(page.locator("#s-timezone").locator("xpath=ancestor::div[contains(concat(' ',@class,' '),' settings-row ')][1]")).toHaveClass(/is-found/);
});
