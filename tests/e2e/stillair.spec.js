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
    const inner = next && next.querySelector(".card-title, .settings-block-title, .mon-acc__title, h2, h3");
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
  // Explanation on show. A page says what is and what to do; the why folds
  // into "How this works" (ui.Explain) or a tip (ui.Tip). Only the outermost
  // block is counted, so a sentence is not counted twice. A theme's or a
  // plugin's own description in a catalogue is content, not explanation, and so
  // is a decision record read in Decisions' inspector: it is what the page is for.
  const PROSE = "p, li, .page-sub, .card-sub, .text-sm, .text-xs, .muted, .hint, small";
  const FOLDED = ".sa-tip, details:not([open]) > :not(summary), code, pre";
  const said = (e) => { // the text a reader sees: not a tip's, not a fold's, not code
    const w = document.createTreeWalker(e, NodeFilter.SHOW_TEXT);
    let t = "", x;
    while ((x = w.nextNode())) if (!x.parentElement.closest(FOLDED + ", script, style, template")) t += x.nodeValue;
    return t.replace(/\s+/g, " ").trim();
  };
  const prose = [...main.querySelectorAll(PROSE)].filter((e) =>
    e.offsetParent && !e.parentElement.closest(PROSE) && !e.closest(FOLDED) &&
    !e.closest("table, .sa-facts, .store-card, .theme-card, .adr-doc") && said(e).length >= 60);
  const shown = prose.reduce((n, e) => n + said(e).length, 0);
  if (shown > 900) out.push(`${shown} characters of explanation on show (limit 900): fold the why into How this works`);

  const visible = (e) => e.offsetParent !== null && e.getClientRects().length > 0;
  const lines = (e) => { // how many lines the element's own text is set on
    // Boxes on one line overlap vertically even when their sizes differ (a
    // figure's unit is set smaller on the number's baseline).
    const r = document.createRange(); r.selectNodeContents(e);
    const boxes = [...r.getClientRects()].filter((q) => q.width > 0).sort((a, b) => a.top - b.top);
    let n = 0, bottom = -Infinity;
    for (const q of boxes) { if (q.top >= bottom - 1) { n++; bottom = q.bottom; } else bottom = Math.max(bottom, q.bottom); }
    return n;
  };
  // A figure is read at a glance: one line, all of it. A hostname or a phrase
  // set as a number clipped ("mail.localh") or broke ("Main / domain").
  main.querySelectorAll(".stat-card__value").forEach((e) => {
    if (!visible(e)) return;
    const t = e.textContent.trim();
    if (e.scrollWidth > e.clientWidth + 1) out.push(`figure "${t}" is cut off`);
    else if (lines(e) > 1) out.push(`figure "${t}" breaks over lines`);
  });
  // The identifier that names a table row is one token: ADR-0163 read as
  // "ADR-" over "0163". On a phone the rows stack and a long one may wrap.
  if (innerWidth >= 600) main.querySelectorAll("td:first-child, th:first-child").forEach((cell) => {
    const toks = cell.matches(".mono, code") ? [cell] : [...cell.querySelectorAll("code, .mono, kbd")];
    toks.forEach((e) => {
      const t = e.textContent.trim();
      if (visible(e) && t && !/\s/.test(t) && t.length <= 24 && lines(e) > 1) out.push(`identifier "${t}" breaks over lines`);
    });
  });
  // On a desktop a table fits its page; one that scrolls sideways hides a column.
  if (innerWidth >= 1000) main.querySelectorAll(".table-wrap").forEach((e) => {
    if (visible(e) && e.scrollWidth > e.clientWidth + 1) out.push(`a table scrolls sideways (${e.scrollWidth} > ${e.clientWidth})`);
  });
  // A control's label is read whole; a clipped one ("Open in ne") is a guess.
  main.querySelectorAll("button, .btn, a.btn").forEach((e) => {
    if (visible(e) && e.textContent.trim() && e.scrollWidth > e.clientWidth + 1) out.push(`control "${e.textContent.trim().slice(0, 30)}" is cut off`);
  });
  // An action is a button, not a banner: a filled button stretched across a
  // block shouts the one thing a calm page says quietly.
  if (innerWidth >= 1000) main.querySelectorAll(".btn--primary, button.btn-primary").forEach((e) => {
    if (visible(e) && e.getBoundingClientRect().width > 360) out.push(`button "${e.textContent.trim().slice(0, 30)}" stretched to ${Math.round(e.getBoundingClientRect().width)}px`);
  });
  // The browser's own grey "Choose File" is not a console control: its button
  // is drawn as ours, in the console's face and corner.
  main.querySelectorAll("input[type=file]").forEach((e) => {
    const b = getComputedStyle(e, "::file-selector-button");
    if (visible(e) && (!/Inter/.test(b.fontFamily) || b.borderTopLeftRadius === "0px")) out.push("the browser's own file picker is on show");
  });
  // Words a reader trips on: a count that disagrees with its noun (a unit such
  // as ms is not a plural), a hedged plural, a doubled full stop, a label in
  // title case or capitals.
  const text = said(main);
  const onePlural = text.match(/(?:^|[^\d.,:/])1 (?!(?:is|was|has|does|plus|as|its|this|us|yes|less|pass|process|access|address|status|class|across|ms|rps|qps|fps|[kmg]bps)\b)[a-z]+s\b/);
  if (onePlural) out.push(`count disagrees with its noun: "${onePlural[0].trim()}"`);
  const hedged = text.match(/\w+\(s\)/);
  if (hedged) out.push(`hedged plural: "${hedged[0]}"`);
  const doubled = text.match(/[^.\s]\.\.(?!\.)/);
  if (doubled) out.push(`doubled full stop: "${text.slice(Math.max(0, doubled.index - 20), doubled.index + 3)}"`);
  main.querySelectorAll("button, .btn, a.btn").forEach((e) => {
    const t = said(e);
    // A product's own name keeps its capitals (Theme Studio, Spaces).
    const plain = t.replace(/\b(?:Theme Studio|Theme Store|Spaces|Vayu[A-Z]\w*|Claude \w+|Tor)\b/g, "x");
    if (visible(e) && (/^[A-Z][a-z]+(?: [A-Z][a-z]+)+$/.test(plain) || /\b(?:ON|OFF)\b/.test(t))) out.push(`label not in sentence case: "${t}"`);
  });
  // A title's icon sits on the title's line; one on a line of its own reads as
  // a broken glyph ("▯" above "VayuMail — the official mobile app").
  main.querySelectorAll(".card-title, .section-head__title, h1, h2, h3").forEach((t) => {
    const ico = t.querySelector(":scope > svg.sa-ico");
    if (!ico || !visible(ico) || !t.textContent.trim()) return;
    const r = document.createRange(); r.selectNodeContents(t);
    const text = [...r.getClientRects()].filter((q) => q.width > 0 && !(q.left >= ico.getBoundingClientRect().left && q.right <= ico.getBoundingClientRect().right + 1));
    if (text.length && ico.getBoundingClientRect().bottom <= Math.min(...text.map((q) => q.top)) + 1) out.push(`icon above its title "${t.textContent.trim().slice(0, 30)}"`);
  });
  // A status is a label, set in sentence case like every other: "pending",
  // "not pointed" and "at-risk" were raw machine values printed as they were.
  main.querySelectorAll(".badge, .tag, .sa-tag, .tool-status, .pill, .mon-chip").forEach((e) => {
    const t = said(e);
    if (visible(e) && /^[a-z]/.test(t)) out.push(`status not in sentence case: "${t}"`);
  });
  // A text box too narrow to write in: beside the editor's sidebar, at 1024px
  // the writing column was 150px and broke words in two.
  if (innerWidth >= 600) main.querySelectorAll("textarea, [contenteditable=true]").forEach((e) => {
    if (visible(e) && !e.closest("[aria-hidden=true]") && e.getBoundingClientRect().width < 320) out.push(`text box ${Math.round(e.getBoundingClientRect().width)}px wide, too narrow to write in`);
  });
  // Text left invisible: an entrance animation removed with its opacity:0
  // start kept drew the primary domain's card as an empty box. One still fading
  // in is not left invisible. Controls kept out of sight until wanted must be
  // revealed by focus, which is how both the keyboard and a touch screen reach
  // them: the editor's block controls appeared on hover alone, so a phone and
  // the keyboard never saw them.
  main.querySelectorAll("*").forEach((e) => {
    const cs = getComputedStyle(e);
    if (cs.opacity !== "0" || cs.pointerEvents === "none" || !visible(e) || said(e).length <= 2 || e.closest("[aria-hidden=true]")) return;
    if (e.getAnimations().some((a) => a.playState === "running")) return;
    const where = `<${e.tagName.toLowerCase()} class="${e.className}">`;
    const control = e.querySelector("button, a[href], input, select, textarea");
    if (!control) return out.push(`text drawn invisible in ${where}`);
    const had = document.activeElement;
    e.style.transition = "none";
    control.focus({ preventScroll: true });
    const shown = getComputedStyle(e).opacity !== "0";
    control.blur();
    e.style.transition = "";
    if (had && had !== document.body) had.focus({ preventScroll: true });
    if (!shown) out.push(`controls revealed by hover alone in ${where}`);
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
      // An icon name outside the set draws the dashed missing mark; Posts and
      // Messages shipped one on their date button ("calendar") in 3.17.92,
      // which the chrome-only unit test never renders.
      if (await page.locator(".sa-ico--missing").count()) findings.push(`${scheme} ${width}px ${href}: an icon missing from the set`);
    }
  }
  expect(findings).toEqual([]);
});

// The phone is a touch screen, not a narrow desktop: with no hover, anything
// the page reveals only on hover is never revealed at all.
for (const [device, use] of [
  ["a desktop", { viewport: { width: 1280, height: 900 } }],
  // Where the rail and section list leave the least room beside them.
  ["a small laptop", { viewport: { width: 1024, height: 768 } }],
  ["a phone", { viewport: { width: 390, height: 900 }, hasTouch: true }],
]) {
  test.describe(`on ${device}`, () => {
    test.use(use);
    test(`every app and section passes the design lint, on ${device}`, async ({ page }) => {
      test.setTimeout(300000);
      await openConsole(page);
      const hrefs = await page.evaluate(() => [...new Set([...document.querySelectorAll("[data-sa-index] a")].map((a) => a.getAttribute("href")))]);
      expect(hrefs.length).toBeGreaterThan(30); // the index is the rail's, so an empty one means the walk checked nothing

      const findings = [];
      for (const href of hrefs) {
        const errors = [];
        const onError = (e) => errors.push(e.message);
        page.on("pageerror", onError);
        await page.goto(href);
        await page.waitForLoadState("load");
        const found = await page.evaluate(lintPage);
        page.off("pageerror", onError);
        for (const f of [...found, ...errors.map((e) => "script error: " + e)]) findings.push(`${href}: ${f}`);
      }
      expect(findings).toEqual([]);
    });
  });
}

// On a laptop screen the rail starts as icons, since with an app's section list
// beside it the content column was too narrow for its tables. The toggle still
// opens it, the choice survives a reload, and a collapsed link keeps its name.
test("the rail starts as icons on a small laptop, and the toggle still decides", async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 768 });
  await openConsole(page);
  const rail = page.locator(".sa-rail");
  const toggle = page.locator(".menu-toggle");
  const width = async () => Math.round((await rail.boundingBox()).width);
  expect(await width()).toBeLessThan(80);
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("link", { name: "Mail", exact: false }).first()).toBeVisible();

  await toggle.click();
  expect(await width()).toBeGreaterThan(180);
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await page.reload();
  expect(await width()).toBeGreaterThan(180);

  await toggle.click();
  expect(await width()).toBeLessThan(80);
  await page.setViewportSize({ width: 1440, height: 900 });
  expect(await width()).toBeLessThan(80); // a choice made is kept at any width
});

// The editor rebuilds its blocks on Enter and on a Markdown shortcut. Focus
// used to follow a tick later, and a key typed in that tick was dropped: a
// quick typist lost the first letter of a paragraph.
test("writing straight on through Enter and a shortcut loses no keystroke", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/editor");
  await page.locator(".eblock__text").first().click();
  await page.keyboard.type("one");
  await page.keyboard.press("Enter");
  await page.keyboard.type("two");
  await page.keyboard.press("Enter");
  await page.keyboard.type("## Heading");
  await expect(page.locator(".eblock__text").nth(0)).toHaveValue("one");
  await expect(page.locator(".eblock__text").nth(1)).toHaveValue("two");
  await expect(page.locator(".eblock__heading").first()).toHaveValue("Heading");
});

// The editor is a document (render 02): the inspector stands beside the
// writing, folds away from the bar or in focus mode, and is a drawer where
// there is no room beside the text. Each state is checked by where the
// inspector is, not by a class, since a class can be right while the
// stylesheet still draws the old drawer.
test("the editor's inspector stands beside the writing, folds away, and is a drawer on a narrow screen", async ({ page }) => {
  await openConsole(page);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/os/editor");
  const insp = page.locator("[data-editor-settings]");
  const toggle = page.locator("[data-editor-settings-btn]");
  const canvas = page.locator("[data-editor-canvas]");
  await expect(insp).toBeVisible();
  const ib = await insp.boundingBox(), cb = await canvas.boundingBox();
  expect(ib.x).toBeGreaterThanOrEqual(cb.x + cb.width); // beside, not over
  await expect(page.locator("[data-editor-byline]")).toContainText("not yet published");
  await expect(insp).toContainText("Not saved yet");

  await toggle.click();
  await expect(insp).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
  const wider = await canvas.boundingBox();
  expect(wider.width).toBeGreaterThanOrEqual(cb.width); // the writing takes the room back
  await toggle.click();
  await expect(insp).toBeVisible();

  // Focus mode is a choice in More; choosing it closes the menu.
  const more = page.locator("details.editor-more");
  await more.locator("summary").click();
  await more.locator("[data-editor-focus-btn]").click();
  await expect(more).not.toHaveAttribute("open", "");
  await expect(more.locator("[data-editor-focus-btn]")).toHaveAttribute("aria-checked", "true");
  await expect(insp).toBeHidden();
  await page.keyboard.press("Control+.");
  await expect(insp).toBeVisible();

  await page.setViewportSize({ width: 900, height: 900 });
  await expect(insp).toBeHidden();
  await toggle.click();
  await expect(insp).toBeVisible();
  await expect(page.locator("[data-editor-settings-backdrop]")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(insp).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-pressed", "false");
});

// Every core control has the same nine states (plan §7, render 01). Each is
// put on each control on a real console page, with the real stylesheet, and
// judged by what that state must show, not only by looking different from
// rest: a rule that made every state a colour change would pass a
// difference check and still leave "disabled" looking clickable.
test("every core control shows each of its nine states, each by its own sign", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/settings");
  await page.evaluate(() => {
    const box = document.createElement("div");
    box.id = "sa-states";
    box.innerHTML =
      '<button type="button" class="btn" id="c-btn">Open DNS</button> ' +
      '<button type="button" class="btn btn--primary" id="c-pri">Publish</button> ' +
      '<input class="input" id="c-input" value="johal.in"> ' +
      '<select class="input" id="c-select"><option>One</option></select> ' +
      '<textarea class="textarea" id="c-area">Notes</textarea> ' +
      '<input type="checkbox" id="c-check"> ' +
      '<input type="checkbox" class="toggle" role="switch" id="c-toggle"> ' +
      '<span class="sa-seg"><button type="button" class="sa-seg__opt" id="c-seg" aria-pressed="false">Day</button></span>';
    document.querySelector("main#main-content").prepend(box);
    // Final values, not a frame mid-transition. Set through the CSSOM: the
    // console's CSP refuses an injected stylesheet, as it should.
    box.querySelectorAll("*").forEach((e) => { e.style.transition = "none"; });
  });
  const token = (name) => page.evaluate((n) => {
    const d = document.createElement("div");
    d.style.color = `var(${n})`;
    document.querySelector("main").appendChild(d);
    const c = getComputedStyle(d).color;
    d.remove();
    return c;
  }, name);
  const [focus, ok, danger] = [await token("--focus"), await token("--ok"), await token("--danger")];
  const look = (sel) => page.$eval(sel, (e) => {
    const c = getComputedStyle(e), a = getComputedStyle(e, "::after");
    return {
      border: c.borderTopColor, shadow: c.boxShadow, outline: c.outlineStyle + " " + c.outlineColor,
      bg: c.backgroundColor + " " + c.backgroundImage, color: c.color, transform: c.transform,
      opacity: c.opacity, cursor: c.cursor, anim: c.animationName, after: a.animationName,
      visible: e.matches(":focus-visible"),
    };
  });
  const reset = (sel) => page.$eval(sel, (e) => {
    for (const a of ["disabled", "aria-busy", "data-state", "aria-invalid"]) e.removeAttribute(a);
    if (e.hasAttribute("aria-pressed")) e.setAttribute("aria-pressed", "false");
    if (e.type === "checkbox") e.checked = false;
    e.blur();
  });
  const set = (sel, fn) => page.$eval(sel, fn);
  const field = (sel) => ["#c-input", "#c-select", "#c-area"].includes(sel);
  const fails = [];
  const expectThat = (cond, what) => { if (!cond) fails.push(what); };

  for (const sel of ["#c-btn", "#c-pri", "#c-input", "#c-select", "#c-area", "#c-check", "#c-toggle", "#c-seg"]) {
    await page.mouse.move(0, 0);
    await reset(sel);
    const rest = JSON.stringify(await look(sel));
    const differs = (s) => JSON.stringify(s) !== rest;

    await page.hover(sel);
    expectThat(differs(await look(sel)), `${sel} hover looks like rest`);
    await page.mouse.move(0, 0);

    await page.keyboard.press("Shift");
    await page.focus(sel);
    const f = await look(sel);
    expectThat(f.visible && (f.outline === "solid " + focus || f.border === focus), `${sel} focus shows no focus ring`);
    await reset(sel);

    if (!field(sel)) {
      await page.hover(sel);
      await page.mouse.down();
      const pr = await look(sel);
      await page.mouse.move(0, 0);
      await page.mouse.up();
      expectThat(/matrix\(0\.97/.test(pr.transform), `${sel} pressed does not give under the pointer`);
      await reset(sel);
      await set(sel, (e) => { if (e.type === "checkbox") e.checked = true; else e.setAttribute("aria-pressed", "true"); });
      expectThat(differs(await look(sel)), `${sel} active looks like rest`);
      await reset(sel);
    }

    await set(sel, (e) => { e.disabled = true; });
    expectThat((await look(sel)).cursor === "not-allowed", `${sel} disabled still looks usable`);
    await reset(sel);

    await set(sel, (e) => e.setAttribute("aria-busy", "true"));
    const busy = await look(sel);
    expectThat(busy.after === "vp3-spin" || busy.anim === "sa-busy-bar" || busy.anim === "sa-busy-pulse", `${sel} loading shows nothing working`);
    await reset(sel);

    await set(sel, (e) => e.setAttribute("data-state", "success"));
    const good = await look(sel);
    expectThat(good.border === ok || good.outline === "solid " + ok, `${sel} success is not in the success colour`);
    await reset(sel);

    // A field says it is wrong with aria-invalid; a button or switch has no
    // such attribute, so the write mechanism's data-state carries it.
    await page.$eval(sel, (e, isField) => e.setAttribute(isField ? "aria-invalid" : "data-state", isField ? "true" : "error"), field(sel));
    const bad = await look(sel);
    expectThat(bad.border === danger || bad.outline === "solid " + danger, `${sel} error is not in the danger colour`);
    await reset(sel);
  }
  expect(fails).toEqual([]);
});

// The states are driven, not only drawn: the control a write came from shows
// it working and then how it went. A read leaves it alone, and a write started
// later, from a timer, is not pinned on a button clicked earlier.
test("a write shows loading, then its outcome, on the control that started it", async ({ page }) => {
  await openConsole(page);
  let status = 500;
  await page.route("**/os/__state-probe", async (route) => {
    await new Promise((r) => setTimeout(r, 300));
    await route.fulfill({ status, body: "{}" });
  });
  await page.evaluate(() => {
    const mk = (id, fn) => {
      const b = document.createElement("button");
      b.type = "button"; b.className = "btn"; b.id = id; b.textContent = id;
      b.addEventListener("click", fn);
      document.querySelector("main#main-content").prepend(b);
    };
    mk("w-post", () => fetch("/os/__state-probe", { method: "POST" }));
    mk("w-get", () => fetch("/os/__state-probe"));
    mk("w-late", () => setTimeout(() => fetch("/os/__state-probe", { method: "POST" }), 50));
  });
  const post = page.locator("#w-post");
  await post.click();
  await expect(post).toHaveAttribute("aria-busy", "true");
  await expect(post).toHaveAttribute("data-state", "error");
  await expect(post).not.toHaveAttribute("aria-busy", "true");
  status = 200;
  await post.click();
  await expect(post).toHaveAttribute("data-state", "success");

  // Watched rather than asserted after the fact: a mark on these clears
  // itself, and a retrying negative assertion would wait for it to go.
  await page.evaluate(() => {
    window.__marks = [];
    new MutationObserver((ms) => ms.forEach((m) => window.__marks.push(m.target.id + " " + m.attributeName)))
      .observe(document.querySelector("main"), { subtree: true, attributeFilter: ["aria-busy", "data-state"] });
  });
  await page.locator("#w-get").click();
  await page.locator("#w-late").click();
  await page.waitForTimeout(800);
  expect(await page.evaluate(() => window.__marks)).toEqual([]);
});

// The bell (render 09): what happened carries its time in the viewer's clock
// under Today, and Mark all read clears it for good while what needs the
// operator stays counted.
// The console is a fixed-height shell with one vertical scroller, its content
// area. The document itself never scrolls: a page that does has a second
// scroll bar around the first (the browser's default body margin did this on
// every page). Checked on every page, at desktop and phone width.
test("the console page itself never scrolls; only its content does", async ({ page }) => {
  test.setTimeout(300000);
  await openConsole(page);
  const hrefs = await page.evaluate(() => [...new Set([...document.querySelectorAll("[data-sa-index] a")].map((a) => a.getAttribute("href")))]);
  const scrolling = [];
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 844 });
    for (const href of hrefs) {
      await page.goto(href);
      await page.waitForLoadState("load");
      const over = await page.evaluate(() => document.scrollingElement.scrollHeight - document.scrollingElement.clientHeight);
      if (over > 0) scrolling.push(`${width}px ${href}: the document scrolls by ${over}px`);
    }
  }
  expect(scrolling).toEqual([]);
});

test("the bell dates what happened, Mark all read clears it for good, and Clear all empties it", async ({ page }) => {
  const slug = "bell-" + Date.now();
  const made = await page.request.post("/api/v1/articles", { data: { title: "Bell check " + slug, slug, content: "<p>x</p>" } });
  expect(made.ok()).toBeTruthy();
  await openConsole(page);
  const open = async () => { await page.locator("[data-notif-toggle]").click(); await expect(page.locator("[data-notif-panel]")).toBeVisible(); };
  await open();
  const row = page.locator(".notif-item--event", { hasText: "Bell check " + slug });
  await expect(row).toHaveClass(/is-unread/);
  await expect(page.locator("[data-notif-recent] .notif-group__label").first()).toHaveText("Today");
  await expect(row.locator("time")).not.toContainText("UTC"); // rewritten in the viewer's clock
  const needs = Number(await page.locator("[data-notif]").getAttribute("data-notif-needs"));

  await page.locator("[data-notif-seen]").click();
  await expect(page.locator(".notif-item.is-unread")).toHaveCount(0);
  await expect(page.locator("[data-notif-seen]")).toHaveCount(0);
  const badge = page.locator("[data-notif-badge]");
  if (needs > 0) await expect(badge).toHaveText(needs > 99 ? "99+" : String(needs));
  else await expect(badge).toHaveCount(0);

  await page.reload();
  await open();
  await expect(row).not.toHaveClass(/is-unread/);
  await expect(page.locator("[data-notif-seen]")).toHaveCount(0);

  // Clear all empties the bell, and a reload keeps it empty of what was
  // cleared. Other tests publish meanwhile, so the check is on this post's row.
  await page.locator("[data-notif-clear]").click();
  await expect(page.locator("[data-notif-panel] .notif-item")).toHaveCount(0);
  await expect(page.locator("[data-notif-empty]")).toBeVisible();
  await expect(page.locator("[data-notif-badge]")).toHaveCount(0);
  await page.reload();
  await open();
  await expect(row).toHaveCount(0);
});

// The command bar (render 03): results grouped Go to, Actions, Settings, the
// query marked in each, the highlighted one previewed beside the list, Tab to
// narrow to one kind, and an action that runs and says what the server said.
test("the command bar groups, marks, previews and runs", async ({ page }) => {
  await openConsole(page);
  await page.keyboard.press("Control+k");
  await page.locator(".cmd-input").fill("site");
  const groups = page.locator("#cmd-results .cmd-group-label");
  await expect(groups).toHaveText(["Go to", "Actions", "Settings"]);
  const active = page.locator(".cmd-item--active");
  await expect(active).toHaveCount(1);
  await expect(active.locator("mark").first()).toHaveText(/site/i);
  const preview = page.locator("[data-cmd-preview] .cmd-preview__title");
  const last = async () => (await active.locator(".cmd-item__label").textContent()).split(" › ").pop();
  await expect(preview).toHaveText(await last());
  // A page is previewed as it is now: its own line, read from the page.
  await expect(page.locator("[data-cmd-preview] .cmd-preview__text").first()).not.toBeEmpty();
  await page.keyboard.press("ArrowDown");
  await expect(preview).toHaveText(await last()); // the preview follows the highlight
  await expect(page.locator(".cmd-footer .cmd-footer-hint")).toHaveCount(4);

  await page.keyboard.press("Tab");
  await expect(page.locator("[data-cmd-kind]")).toHaveText("Go to");
  await expect(groups).toHaveText(["Go to"]);
  await page.keyboard.press("Shift+Tab");
  await expect(page.locator("[data-cmd-kind]")).toHaveText("All");
  await page.locator("[data-cmd-kind]").click(); // the same filter without a Tab key
  await expect(groups).toHaveText(["Go to"]);
  await page.keyboard.press("Shift+Tab");

  await page.locator(".cmd-input").fill("refresh every page");
  await expect(active).toContainText("Refresh every page");
  await page.keyboard.press("Enter");
  await expect(page.locator("#cmd-backdrop")).toBeHidden();
  await expect(page.locator(".toast--ok").last()).toBeVisible();
});

// A PNG of noise: incompressible, so its size is what goes over the wire, and
// different on every run, so the library meets it as a new file each time.
function noisePNG(w, h) {
  const zlib = require("zlib");
  const crypto = require("crypto");
  // CRC-32 by hand: zlib.crc32 is newer than some Node 20 releases CI may run.
  const crc32 = (buf) => {
    let c = ~0;
    for (const b of buf) { c ^= b; for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1)); }
    return ~c >>> 0;
  };
  const rows = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) crypto.randomFillSync(rows, y * (w * 3 + 1) + 1, w * 3);
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(td));
    return Buffer.concat([len, td, crc]);
  };
  const ihdr = Buffer.alloc(13); ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2;
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
}

// Media (render 07): an upload says how far along it is and how long is left;
// the file then arrives under the name it had, and is inspected, renamed and
// trashed from the keyboard, through the same menu a right-click opens.
// On johal.in the library's list timed out (the scan for where files are used
// read 234,615 posts) and the first tap redrew it as "No file matches that"
// over 55 files. A list that cannot be read says so and can be tried again;
// and a file nobody has checked yet is never said to be unused.
test("a media list that could not be read says so, and an unchecked file is not called unused", async ({ page }) => {
  await openConsole(page);
  await page.route("**/os/api/media", (r) => r.fulfill({ status: 504, body: "Gateway Timeout" }));
  await page.goto("/os/media");
  const failure = page.locator("[data-media-list] [role=alert]");
  await expect(failure).toContainText("could not be read (HTTP 504)");
  await page.click('[data-media-filter="all"]'); // what the operator tapped
  await expect(failure).toBeVisible();
  await expect(page.locator("[data-media-empty]")).toBeHidden();

  // The list arrives before the server has looked for where its files are used.
  await page.unroute("**/os/api/media");
  await page.route("**/os/api/media", async (r) => {
    const res = await r.fetch();
    const d = await res.json();
    d.items.forEach((it) => { it.uses = null; });
    d.uses_checked = null;
    d.uses_scanning = true;
    return r.fulfill({ response: res, json: d });
  });
  await failure.getByRole("button", { name: "Try again" }).click();
  await expect(failure).toHaveCount(0);
  // A file of this test's own, since the install may not hold one yet.
  const name = `unchecked-${Date.now()}.png`;
  await page.locator("[data-media-input]").setInputFiles({ name, mimeType: "image/png", buffer: noisePNG(64, 64) });
  const row = page.locator("[data-media-list] [data-name]", { hasText: name });
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.locator("[data-media-uses]")).toHaveText("Checking…");
  await page.getByRole("button", { name: "Move to trash" }).click();
  await expect(page.getByText(/has not been checked yet/)).toBeVisible();
  await expect(page.getByText(/Nothing used/)).toHaveCount(0);
  await page.getByRole("button", { name: "Cancel" }).click();

  // A scan that failed is checking nothing, and the page stops saying it is.
  await page.unroute("**/os/api/media");
  await page.route("**/os/api/media", async (r) => {
    const res = await r.fetch();
    const d = await res.json();
    d.items.forEach((it) => { it.uses = null; });
    d.uses_checked = null;
    d.uses_scanning = false;
    return r.fulfill({ response: res, json: d });
  });
  await expect(page.locator("[data-media-uses]")).toHaveText("Not checked");
});

test("a media upload shows its progress, and the file is worked by keyboard", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/media");
  await page.waitForLoadState("networkidle");
  // A slow uplink, so the upload lasts long enough to measure.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: 256 * 1024 });
  await page.evaluate(() => {
    window.__uploadSaid = [];
    const box = document.querySelector("[data-media-uploads]");
    new MutationObserver(() => window.__uploadSaid.push(box.textContent)).observe(box, { subtree: true, childList: true, characterData: true });
  });
  const name = `harbour-${Date.now().toString(36)}.png`;
  await page.locator("[data-media-input]").setInputFiles({ name, mimeType: "image/png", buffer: noisePNG(400, 400) });
  const panel = page.locator("[data-media-uploads]");
  await expect(panel.locator(".media-uploads__title")).toHaveText("1 file uploaded", { timeout: 20000 });
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  const said = await page.evaluate(() => window.__uploadSaid.join("\n"));
  expect(said).toContain("Uploading 1 file");
  expect(said).toContain(name);
  expect(said).toMatch(/\d+% · \d+ s left/);

  const row = page.locator(".media-row", { hasText: name });
  await expect(row).toHaveCount(1);
  await row.click();
  await expect(row).toHaveAttribute("aria-selected", "true");
  const inspector = page.locator("[data-list-inspector]");
  await expect(inspector.locator(".sa-insp__title")).toHaveText(name);
  await expect(inspector.locator(".sa-insp__meta")).toHaveText(/^400 × 400 · PNG image · /);
  await expect(inspector.locator("[data-media-uses]")).toHaveText("Not used anywhere");

  // The menu key opens the menu on the file, and Escape hands focus back.
  await page.keyboard.press("Shift+F10");
  const menu = page.locator("[data-media-menu]");
  await expect(menu.locator(".sa-menu__text")).toHaveText(["Open preview", "Copy link", "Rename", "Show where it’s used", "Download", "Move to trash"]);
  await expect(menu.locator(".sa-menu__item").first()).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(row).toBeFocused();

  const renamed = name.replace("harbour", "Dawn over the harbour");
  await page.keyboard.press("F2");
  await page.locator(".vp-confirm input").fill(renamed);
  await page.keyboard.press("Enter");
  const after = page.locator(".media-row", { hasText: renamed });
  await expect(after).toBeFocused();
  await page.reload();
  await expect(page.locator(".media-row", { hasText: renamed })).toHaveCount(1); // the server kept it

  await page.locator(".media-row", { hasText: renamed }).click();
  await page.keyboard.press("Delete");
  const dialog = page.locator(".vp-confirm");
  await expect(dialog).toContainText(/Nothing used it when last checked, (just now|\d+m ago)\./);
  await dialog.getByRole("button", { name: "Move to trash" }).click();
  await expect(page.locator(".media-row", { hasText: renamed })).toHaveCount(0);
});

// Outside services: each change saves as it is made, a public page is sent
// what was chosen, the console never is, and a refused save says why in the
// server's words (it used to toast "[object Object]").
test("outside services reach the public pages, never the console", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/website/services");
  const csp = async (path) => (await page.request.get(path)).headers()["content-security-policy"] || "";
  // Start from strict, whatever an earlier run left.
  await page.evaluate(() => new Promise((done) => window.vpPost("/os/api/website/csp", { mode: "strict" }, done, done)));
  await page.reload();
  await Promise.all([page.waitForEvent("load"), page.locator('input[name="csp-mode"][value="custom"]').check()]);
  await Promise.all([page.waitForEvent("load"), page.locator('[data-csp-service="stripe"]').check()]);
  await expect(page.locator('[data-csp-service="stripe"]')).toBeChecked();
  expect(await csp("/")).toContain("https://js.stripe.com");
  expect(await csp("/os/posts")).not.toContain("stripe");

  const refused = await page.evaluate(() => new Promise((done) => {
    window.vpPost("/os/api/website/csp", { mode: "custom", sources: { "script-src": ["https://cdn.example.com"] } },
      () => done("saved"), (d, msg) => done(msg));
  }));
  expect(refused).toContain("come only from the listed services");

  await Promise.all([page.waitForEvent("load"), page.locator('input[name="csp-mode"][value="strict"]').check()]);
  expect(await csp("/")).not.toContain("stripe");

  // A source of the operator's own is added from a sheet, not a form in the page.
  const origin = "https://img.e2e.example";
  await expect(page.locator("#csp-add-sheet")).toBeHidden();
  await page.getByRole("button", { name: "Add a source" }).click();
  await expect(page.locator("#csp-add-sheet")).toBeVisible();
  await page.locator("#csp-add-src").fill(origin);
  await Promise.all([page.waitForEvent("load"), page.getByRole("button", { name: "Add the source" }).click()]);
  await expect(page.locator(`[data-origin="${origin}"]`)).toHaveCount(1);
  expect(await csp("/")).toContain(origin);
  await Promise.all([page.waitForEvent("load"), page.locator(`[data-origin="${origin}"]`).click()]);
  await expect(page.locator(`[data-origin="${origin}"]`)).toHaveCount(0);
  await page.evaluate(() => new Promise((done) => window.vpPost("/os/api/website/csp", { mode: "strict" }, done, done)));
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
  const opener = page.locator("[data-pages-refresh]");
  await opener.focus();
  await page.keyboard.press("Enter");
  const dialog = page.locator(".vp-confirm");
  await expect(dialog).toBeVisible();
  const confirm = dialog.getByRole("button", { name: "Refresh every page" });
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

// An app sidebar with more entries than fit scrolls, and squeezes nothing: the
// mailbox address above sixteen mailboxes was cut to half its height. The
// sidebar is filled here with the real stylesheet, so the rule holds for any
// app whose list grows long.
test("a long app sidebar scrolls and squeezes nothing", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/settings");
  await page.setViewportSize({ width: 1280, height: 600 });
  const squeezed = await page.evaluate(() => {
    const side = document.querySelector(".sa-appside");
    if (!side) return ["no app sidebar on /os/settings"];
    const item = side.querySelector(".sa-appside__item");
    side.insertAdjacentHTML("afterbegin", '<div class="sa-appside__title" title="a-long-mailbox-address@example.org">a-long-mailbox-address@example.org</div>');
    for (let i = 0; i < 40; i++) side.appendChild(item.cloneNode(true));
    return [...side.children]
      .filter((el) => el.getBoundingClientRect().height + 1 < el.scrollHeight)
      .map((el) => `${el.className}: ${Math.round(el.getBoundingClientRect().height)}px shown of ${el.scrollHeight}px`);
  });
  expect(squeezed).toEqual([]);
});

// Refresh every page answers at once and reports its progress under the card:
// the pages are rebuilt in the background, none deleted.
test("refresh every page starts at once and shows its progress", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/storage");
  await page.locator("[data-pages-refresh]").click();
  await page.locator(".vp-confirm").getByRole("button", { name: "Refresh every page" }).click();
  await expect(page.locator("[data-cache-msg]")).toContainText(/Every page is being refreshed|already running/);
  await expect(page.locator("#pages-refresh")).toContainText(/Refreshing every page|Last refresh finished/);
  // The post the suite seeds is still served: nothing was deleted to refresh it.
  const post = await page.request.get("/" + (process.env.ARTICLE_SLUG || "hello-vayupress"));
  expect(post.ok()).toBeTruthy();
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

// Theme Studio keeps unapplied work: it is saved a moment after it stops
// changing, offered back on the next visit, and gone once discarded. The
// banner and its buttons existed for a long time with nothing behind them.
test("theme studio offers unapplied work back, and forgets it when discarded", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/theme");
  const css = page.locator("[data-theme-css]");
  await expect(css).toBeAttached();
  const mark = "/* e2e draft " + Date.now() + " */";
  await css.evaluate((el, v) => { el.value = v; el.dispatchEvent(new Event("input", { bubbles: true })); }, mark);
  // The autosave waits for the typing to stop, then posts.
  await page.waitForResponse((r) => r.url().endsWith("/os/api/theme/draft") && r.status() === 200, { timeout: 10000 });

  await page.reload();
  const banner = page.locator("[data-theme-draft]");
  await expect(banner).toBeVisible();
  await banner.locator("[data-draft-resume]").click();
  await expect(banner).toBeHidden();
  await expect(page.locator("[data-theme-css]")).toHaveValue(mark);

  // Discarding clears it for good.
  await page.reload();
  await expect(page.locator("[data-theme-draft]")).toBeVisible();
  const cleared = page.waitForResponse((r) => r.url().endsWith("/os/api/theme/draft") && r.status() === 200);
  await page.locator("[data-draft-discard]").click();
  await cleared;
  await page.reload();
  await expect(page.locator("[data-theme-draft]")).toBeHidden();
});

// A write the server refuses is said out loud. HTMX leaves the page unchanged
// on a 4xx or 5xx, so without this a failed save looked exactly like a
// successful one that did nothing.
test("a refused write is announced, not silent", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/shield");
  const live = page.locator(".page-actions [role='status']");
  await expect(live).toBeAttached();
  await page.evaluate(() => window.htmx.ajax("POST", "/os/api/no-such-write", { target: "body", swap: "none" }));
  await expect(live).toHaveText(/did not go through \(\d{3}\)/);
  await expect(page.locator(".toast", { hasText: "did not go through" })).toBeVisible();
});

// An app that cannot run yet answers with one page that says what it is, what
// it needs with the steps already done ticked, and one button for the next.
// Each used to answer with a sentence of its own, one of them pointing at a
// wizard that does not exist. On this install (localhost, fresh) every one of
// these is off.
const SETUP_PAGES = [
  ["/os/vayumail", /^Mail isn't set up yet$/],
  ["/os/vayumail/inbox", /^Mail isn't set up yet$/],
  ["/os/talk", /^Talk isn't set up yet$/],
  ["/os/tor", /^(Onion services are off|Tor is turned off)$/],
  ["/os/vayukeep", /^Backups aren't set up yet$/],
  ["/os/ads", /^Advertising is off$/],
  ["/os/newsletter", /^The newsletter isn't set up yet$/],
  ["/os/profile", /^You're signed in with the API key$/],
];

// These tests turn apps on and put them back, and the first needs them off:
// they run one after another, never beside each other.
test.describe("apps, off and then on", () => {
test.describe.configure({ mode: "serial" });

test("an app that is not set up says what it needs, and one button does the next step", async ({ page }) => {
  await openConsole(page);
  for (const [href, title] of SETUP_PAGES) {
    await page.goto(href);
    const kinds = await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")));
    expect(kinds, href).toEqual(["setup"]);
    const setup = page.locator(".sa-setup");
    await expect(setup.locator("h1"), href).toHaveText(title);
    expect(await setup.locator(".sa-setup__step").count(), href).toBeGreaterThan(0);
    await expect(setup.locator(".btn--primary:visible"), href).toHaveCount(1);
  }
});

// Something the system does on its own opens on one sentence of state with its
// mark, then its bands: never on a strip of figures, never on a breadcrumb.
// Backups is this kind too, once it is set up; on this fresh install it is
// the Setup page above.
const STATUS_PAGES = ["/os/update", "/os/storage", "/os/monitoring", "/os/vayuflow", "/os/faults", "/os/replay", "/os/governance"];

test("status pages open on their state, not on figures", async ({ page }) => {
  await openConsole(page);
  for (const href of STATUS_PAGES) {
    await page.goto(href);
    const kinds = await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")));
    expect(kinds, href).toEqual(["status"]);
    const state = page.locator(".sa-status__state");
    await expect(state, href).toHaveCount(1);
    expect((await state.innerText()).trim().length, href).toBeGreaterThan(8);
    await expect(page.locator(".sa-status__head .sa-status__mark"), href).toHaveCount(1);
    await expect(page.locator("main .stat-grid"), href).toHaveCount(0);
    // One primary action per page (rule 6).
    expect(await page.locator("main .btn--primary:visible").count(), href).toBeLessThanOrEqual(1);
  }
});

// A list's rows are table rows, each item on one line across the list's width.
// 3.17.92 shipped every list squeezed: a rule left from the old Posts cards
// (.post-row as a two-column grid) matched the new table rows, so each row
// became a grid of two narrow columns, its title clipped to a few letters and
// its state and figures wrapped under it. A fresh install's one short post
// hides a squeezed title, so the display is what is asserted, then the line.
// On a phone Home's date and state each take a line of their own across the
// head, so how long today's weekday and month are cannot move the page. In
// one wrapping row the wrap followed the date, and the phone screenshot of
// Home passed on 29 September and failed on 2 October.
test("home's head keeps its shape whatever the date, on a phone", async ({ page }) => {
  await openConsole(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/os/");
  const shape = await page.evaluate(() => {
    const head = document.querySelector(".sa-home__head").getBoundingClientRect();
    return [".sa-home__date", ".sa-home__state"].map((sel) => {
      const b = document.querySelector(sel).getBoundingClientRect();
      return { sel, left: Math.round(b.left - head.left), width: Math.round(b.width), head: Math.round(head.width) };
    });
  });
  for (const s of shape) {
    expect(s.left, `${s.sel} does not start its own line`).toBe(0);
    expect(s.width, `${s.sel} does not span the head, so its length moves what follows`).toBe(s.head);
  }
});

// The lists a fresh install has rows in; every list shares the row markup.
const LIST_PAGES = ["/os/posts", "/os/adr"];
test("a list's rows are table rows, each item on one line", async ({ page }) => {
  await openConsole(page);
  for (const href of LIST_PAGES) {
    await page.goto(href);
    const rows = await page.locator("main [data-list-row]").evaluateAll((els) => els.map((r) => {
      // A column the list hides at this width (its container query) has no place.
      const cells = [...r.children].filter((c) => getComputedStyle(c).display !== "none");
      const tops = new Set(cells.map((c) => Math.round(c.getBoundingClientRect().top)));
      // A title may be cut to fit; a short value (a record number, a state, a
      // date, a heading) never is. Decisions read "A…" for ADR-0165 when its
      // column took the width Posts gives its checkbox.
      const cut = [...cells, ...r.closest("table").querySelectorAll("th")]
        .filter((c) => c.innerText.trim().length <= 12 && c.scrollWidth > c.clientWidth + 1)
        .map((c) => c.innerText.trim());
      return { text: r.innerText.slice(0, 40), display: getComputedStyle(r).display, lines: tops.size, cut,
        share: r.getBoundingClientRect().width / r.closest("table").getBoundingClientRect().width };
    }));
    expect(rows.length, `${href} has no row to check`).toBeGreaterThan(0);
    for (const r of rows) {
      expect(r.display, `${href}: "${r.text}" is not a table row`).toBe("table-row");
      expect(r.lines, `${href}: "${r.text}" breaks onto ${r.lines} lines`).toBe(1);
      expect(r.share, `${href}: "${r.text}" does not span its list`).toBeGreaterThan(0.95);
      expect(r.cut, `${href}: "${r.text}" has a short value cut to fit`).toEqual([]);
    }
  }
  // j on Posts moves focus to the first row, which the list then opens on Enter.
  await page.goto("/os/posts");
  await page.locator("main h1").click();
  await page.keyboard.press("j");
  expect(await page.evaluate(() => document.activeElement && document.activeElement.hasAttribute("data-list-row")), "j did not reach a row").toBe(true);
});

// Advertising once it is on is a Settings page: slots are a list, a new slot
// rises in a sheet, and the price and networks are rows under the one bar.
test("advertising, once on, lists its slots and adds one from a sheet", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/ads");
  await Promise.all([page.waitForEvent("load"), page.getByRole("button", { name: "Turn on Advertising" }).click()]);
  expect(await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")))).toEqual(["settings"]);
  await expect(page.locator(".page-state")).toContainText("Showing ads");

  const name = `E2E slot ${Date.now()}`;
  await page.getByRole("button", { name: "Add an ad slot" }).click();
  const sheet = page.locator("#ad-new");
  await expect(sheet).toBeVisible();
  await sheet.locator("#ad-name").fill(name);
  await Promise.all([page.waitForEvent("load"), sheet.getByRole("button", { name: "Add the slot" }).click()]);
  const row = page.locator("tr", { hasText: name });
  await expect(row.locator(".sa-indicator")).toHaveText("On");
  await Promise.all([page.waitForEvent("load"), row.getByRole("button", { name: "Turn off" }).click()]);
  await expect(page.locator("tr", { hasText: name }).locator(".sa-indicator")).toHaveText("Off");

  const price = page.locator("#ad-price");
  const before = await price.inputValue();
  await price.fill("700");
  await page.locator("[data-settings-bar]").getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-settings-bar]")).toBeHidden();
  await page.reload();
  await expect(page.locator("#ad-price")).toHaveValue("700");

  // Back as found: the price, no slot, Advertising off.
  await page.locator("#ad-price").fill(before);
  await page.locator("[data-settings-bar]").getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-settings-bar]")).toBeHidden();
  await page.locator("tr", { hasText: name }).getByRole("button", { name: "Delete" }).click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Delete" }).click()]);
  await page.evaluate(() => new Promise((ok, no) => window.vpPost("/os/api/tools/toggle", { id: "ads", enabled: false }, ok, (d, m) => no(new Error(m)))));
  await page.reload();
  await expect(page.locator(".sa-setup h1")).toHaveText("Advertising is off");
});

// Backups turned on from the console, the way the setup page offers it, and
// then used. Both halves were broken on every install not configured by
// environment variables: the passphrase was refused as an unknown credential,
// and past that every control answered "not set up", because the guard asked
// the environment rather than the engine.
test("backups turned on in the console back up, test-restore, and turn off", async ({ page }) => {
  test.setTimeout(120000);
  await openConsole(page);
  await page.goto("/os/vayukeep");
  // Download and restore do not wait on automatic backup: their sheet opens
  // from the setup page, and closes.
  await page.getByRole("button", { name: "Download or restore a copy" }).click();
  const manual = page.locator("#vk-manual-sheet");
  await expect(manual.locator("[data-backup-export]")).toBeVisible();
  await manual.getByRole("button", { name: "Close" }).click();
  await expect(manual).toBeHidden();

  const sheet = page.locator("#vk-setup-sheet");
  await expect(sheet).toBeHidden();
  await page.getByRole("button", { name: "Set up automatic backup" }).click();
  await expect(sheet).toBeVisible();
  await sheet.locator("#vk-target").fill(`/var/tmp/vk-e2e-${Date.now()}`);
  await sheet.getByRole("button", { name: "Generate one" }).click();
  await Promise.all([page.waitForEvent("load"), sheet.getByRole("button", { name: "Turn on automatic backup" }).click()]);

  // The first restore point, then a test restore of it that passes.
  await expect(async () => {
    await page.reload();
    await expect(page.locator("[data-vk-restore]").first()).toBeAttached({ timeout: 1000 });
  }).toPass({ timeout: 60000 });
  await page.locator("[data-vk-drill]").first().click();
  await expect(page.locator("#vk-run")).toContainText("Test restore PASSED", { timeout: 60000 });

  // Back as found: off, and the setup page again.
  await page.locator("[data-vk-disable]").first().click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Turn off" }).click()]);
  await expect(page.locator(".sa-setup h1")).toHaveText("Backups aren't set up yet");
});
});

test("a setup page's command is copied whole", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openConsole(page);
  await page.goto("/os/vayumail");
  const shown = (await page.locator(".sa-setup__cmd").first().textContent()).trim();
  expect(shown).toContain("deploy-vayupress.sh");
  await page.getByRole("button", { name: "Copy the install command" }).click();
  await expect(page.getByRole("button", { name: "copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(shown);
});

// Monetization is a Settings page (render 04): rows edit in place and one bar
// saves them, and each gateway's form rises in a sheet from its row.
test("monetization saves its rows from one bar, and its forms rise in sheets", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/monetization");
  const kinds = await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")));
  expect(kinds).toEqual(["settings"]);

  const currency = page.locator("#mon-currency");
  const before = await currency.inputValue();
  const next = before === "EUR" ? "USD" : "EUR";
  const bar = page.locator("[data-settings-bar]");
  await expect(bar).toBeHidden();
  await currency.fill(next);
  await expect(bar).toBeVisible();
  await expect(bar.locator("[data-settings-count]")).toHaveText("1 unsaved change");
  await bar.getByRole("button", { name: "Save changes" }).click();
  await expect(bar).toBeHidden();
  await page.reload();
  await expect(page.locator("#mon-currency")).toHaveValue(next);
  // Put it back, so the install is as the test found it.
  await page.locator("#mon-currency").fill(before);
  await page.locator("[data-settings-bar]").getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-settings-bar]")).toBeHidden();

  for (const [row, sheet] of [["Cards", "#mon-stripe"], ["PayPal", "#mon-paypal"], ["Crypto, through your BTCPay Server", "#mon-btcpay"], ["Another processor", "#mon-webhook"]]) {
    const r = page.locator(".settings-row", { hasText: row });
    await r.locator("[data-sheet]").click();
    await expect(page.locator(sheet), row).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator(sheet), row).toBeHidden();
  }
  await page.getByRole("button", { name: "Price a post" }).click();
  await expect(page.locator("#mon-paid-posts #pp-slug")).toBeVisible();
  // The page's own script answers, not only the shared one that opened the sheet.
  await page.getByRole("button", { name: "Set the price" }).click();
  await expect(page.locator("#action-msg")).toHaveText("Enter a post slug first");
});

// Every page that grants a key does so: the button reaches the server and the
// key is shown once. These scripts call csrf() from the page-script wrapper,
// and for three days that helper was missing: every Grant threw before any
// request was sent and the page read "Creating key…" forever.
for (const [path, banner] of [["/os/connector", "#cx-token-value"], ["/os/claudecode", "#mx-token-value"], ["/os/buzz", "#mx-token-value"]]) {
  test(`a grant on ${path} mints a key and shows it once`, async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await openConsole(page);
    await page.goto(path);
    await page.locator('[data-mint="posts:read,analytics:read"]').click();
    await expect(page.locator(banner)).toHaveValue(/^vp_/);
    expect(errors).toEqual([]);
  });
}
// Tools and plugins: a module is a row, and its switch saves at once and says
// so as a dot and a word.
test("a module's switch saves at once and its state follows", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/tools");
  expect(await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")))).toEqual(["settings"]);
  const sw = page.locator('[data-tool-toggle="webmentions"]');
  const state = page.locator('[data-tool-card="webmentions"] [data-tool-status] .sa-indicator');
  const was = await sw.isChecked();
  await sw.setChecked(!was);
  await expect(state).toHaveText(was ? "Off" : "On");
  await page.reload();
  await expect(page.locator('[data-tool-toggle="webmentions"]')).toBeChecked({ checked: !was });
  await page.locator('[data-tool-toggle="webmentions"]').setChecked(was);
  await expect(state).toHaveText(was ? "On" : "Off");
});

// API keys: a key is made in its sheet, and the sheet steps aside for the one
// showing of the key. A credential deleted from inside its sheet asks first,
// and the question is answerable: a sheet is a modal dialog and leaves
// everything outside it inert, so a confirmation drawn beside it rather than
// above it could not be clicked.
test("a key is made in a sheet, and a confirmation asked in a sheet can be answered", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/apikeys");
  expect(await page.locator("main [data-page-kind]").evaluateAll((els) => els.map((e) => e.getAttribute("data-page-kind")))).toEqual(["settings"]);

  const label = `e2e sheet key ${Date.now()}`;
  const sheet = page.locator("dialog#ak-create");
  await page.locator('[data-sheet="ak-create"]').click();
  await expect(sheet).toBeVisible();
  await sheet.locator("#ak-create-btn").click();
  // Refused in the sheet, where the operator is looking.
  await expect(sheet.locator("[data-sheet-status]")).toHaveText("Grant at least one permission (or tick Full access)");
  await sheet.locator("#ak-new-label").fill(label);
  await sheet.locator('.ak-perm[data-section="posts"][data-action="read"]').check();
  await sheet.locator("#ak-create-btn").click();
  await expect(sheet).toBeHidden();
  await expect(page.locator("#ak-token-value")).toHaveValue(/^vp_/);
  await Promise.all([page.waitForEvent("load"), page.locator("#ak-token-done").click()]);
  // A key is managed from its sheet, and its confirmations open above it.
  const row = page.locator("tr", { hasText: label });
  await expect(row.locator(".sa-indicator").last()).toHaveText("Active");
  await row.getByRole("button", { name: "Manage" }).click();
  await page.locator("dialog.sa-sheet[open]").getByRole("button", { name: "Revoke" }).click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Revoke" }).click({ timeout: 5000 })]);
  await expect(page.locator("tr", { hasText: label }).locator(".sa-indicator").last()).toHaveText("Revoked");
  await page.locator("tr", { hasText: label }).getByRole("button", { name: "Manage" }).click();
  await page.locator("dialog.sa-sheet[open]").getByRole("button", { name: "Delete" }).click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Delete" }).click({ timeout: 5000 })]);
  await expect(page.locator("tr", { hasText: label })).toHaveCount(0);

  const name = `e2e credential ${Date.now()}`;
  await page.locator('[data-sheet="ak-cc-new"]').click();
  await page.locator("dialog#ak-cc-new #cc-label").fill(name);
  await page.locator("dialog#ak-cc-new #cc-secret").fill("e2e-secret-value");
  await Promise.all([page.waitForEvent("load"), page.locator("dialog#ak-cc-new #cc-add-btn").click()]);
  const cred = page.locator(".settings-row", { hasText: name });
  await expect(cred.locator(".sa-indicator")).toHaveText("On");
  await cred.getByRole("button", { name: "Edit" }).click();
  const credSheet = page.locator("dialog.sa-sheet[open]");
  await credSheet.getByRole("button", { name: "Delete" }).click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Delete" }).click({ timeout: 5000 })]);
  await expect(page.locator(".settings-row", { hasText: name })).toHaveCount(0);
});

// VayuMCP: a connector is governed from its sheet, and disconnecting it asks
// first, above the sheet.
test("a connector is disconnected from its sheet", async ({ page }) => {
  await openConsole(page);
  await page.goto("/os/connector");
  await page.getByRole("button", { name: "Grant read-only access" }).click();
  await expect(page.locator("#cx-token-banner")).toBeVisible();
  await Promise.all([page.waitForEvent("load"), page.locator("#cx-token-done").click()]);

  const rows = page.locator(".settings-row", { hasText: "VayuMCP (read-only)" });
  const before = await rows.count();
  expect(before).toBeGreaterThan(0);
  await rows.last().getByRole("button", { name: "Details" }).click();
  const sheet = page.locator("dialog.sa-sheet[open]");
  await sheet.getByRole("button", { name: "Remove" }).click();
  await Promise.all([page.waitForEvent("load"), page.locator(".vp-confirm").getByRole("button", { name: "Remove" }).click({ timeout: 5000 })]);
  await expect(page.locator(".settings-row", { hasText: "VayuMCP (read-only)" })).toHaveCount(before - 1);
});
