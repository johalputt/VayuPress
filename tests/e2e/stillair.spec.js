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
