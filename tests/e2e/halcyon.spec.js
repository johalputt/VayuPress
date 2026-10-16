// Halcyon, the theme a new install starts on (this CI install is new).
//
// The checks the plan names before the release: nothing in a post wider than
// its column, at every width and every reader size; the overlay rules; the
// reader's choices applied before paint and reset on request; motion that
// stops for a reader who asked for less; and no request off this host.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

const FIX = path.join(__dirname, "..", "..", "internal", "render", "testdata", "halcyon");
const POSTS = {
  "halcyon-wide": { title: "GitComet 0.2.5 vs GitKraken vs Sourcetree vs lazygit 0.65.1: The Git Client Field Test, Refreshed", file: "gitcomet-wide-content.html", tags: ["git", "tools"] },
  "halcyon-long": { title: "Technical Debt Without the Drowning: Classify, Price, Service, Prevent", file: "technical-debt-visible-paydown-guide.html", tags: ["engineering-culture", "architecture"] },
  // johal.in's own, as stored: an imported post with its own <style> (two
  // third-party @imports, rules for every element), its own <h1>, a
  // full-width wrapper and its own JSON-LD; its style rules and middle
  // sections are shortened, every kind of construct kept.
  "halcyon-legacy": { title: "Cilium and Istio 1.20: The Performance Battle observability for Security", file: "legacy-own-style.html", tags: ["cilium", "istio", "performance", "observability", "tutorial"] },
  // johal.in's longest title, with its post's opening sections, and a topic
  // as long as a topic may be (tag rules: 40 characters).
  "halcyon-title": { title: "Service Worker Offline Patterns That Hold Up in Production: Caching Strategies, Update Choreography, and Quota Discipline", file: "long-title-opening.html", tags: ["web", "pwa", "offline first progressive web app design"] },
  "halcyon-edges": { title: "Edge shapes", file: "edge-shapes.html", tags: ["e2e"] },
};

test.beforeAll(async ({ request }) => {
  for (const [slug, p] of Object.entries(POSTS)) {
    const have = await request.get(`/api/v1/articles/${slug}`);
    if (have.ok()) continue;
    const r = await request.post("/api/v1/articles", {
      data: { title: p.title, slug, content: fs.readFileSync(path.join(FIX, p.file), "utf8"), tags: p.tags },
    });
    expect(r.ok(), `seeding ${slug}: ${r.status()}`).toBeTruthy();
  }
  for (const slug of Object.keys(POSTS)) {
    await expect.poll(async () => (await request.get(`/api/v1/articles/${slug}`)).status(), { timeout: 30000 }).toBe(200);
  }
});

// What in a post's body would make a reader scroll sideways: an element that
// reaches past the column (code may run to the screen's edges on a phone, by
// design, but never past them), an element that scrolls inside itself, and
// how far the page itself scrolls sideways. What is inside a code block is
// held to the block's own text edge, not the screen's: spaces left hanging at
// a wrap sit in its padding here and past the screen on CI's newer Chromium,
// so "within the screen" passed or failed by browser version.
async function overflow(page) {
  return page.evaluate(() => {
    const body = document.querySelector(".h .content");
    const column = body.getBoundingClientRect().right + 1;
    const screen = document.documentElement.clientWidth + 1;
    const name = (e) => e.tagName.toLowerCase() + (e.className ? "." + e.className : "");
    const textEdge = (pre) => pre.getBoundingClientRect().right - parseFloat(getComputedStyle(pre).paddingRight) + 1;
    const all = [...body.querySelectorAll("*")].filter((e) => e.getBoundingClientRect().width > 0);
    const over = all
      .filter((e) => {
        const pre = e.tagName === "PRE" ? null : e.closest("pre");
        return e.getBoundingClientRect().right > (pre ? textEdge(pre) : e.tagName === "PRE" ? screen : column);
      })
      .map(name);
    const scrolls = all.filter((e) => e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).overflowX !== "visible").map(name);
    return { over, scrolls, scroll: document.documentElement.scrollWidth - document.documentElement.clientWidth };
  });
}

// WCAG 2.2 AA, measured on the page as drawn: every visible piece of text
// against the background it actually sits on (4.5:1, 3:1 when large),
// composited through any translucent layers. Text a reader cannot see is
// skipped: visually hidden, aria-hidden, and disabled controls (WCAG exempts
// inactive components).
async function lowContrast(page) {
  return page.evaluate(() => {
    const rgba = (s) => { const m = s.match(/rgba?\(([^)]+)\)/); if (!m) return null; const p = m[1].split(/[ ,/]+/).filter(Boolean).map(Number); return [p[0], p[1], p[2], p.length > 3 ? p[3] : 1]; };
    const over = (top, under) => { const a = top[3]; return [0, 1, 2].map((i) => top[i] * a + under[i] * (1 - a)).concat(1); };
    const bg = (el) => { const chain = []; for (let e = el; e; e = e.parentElement) chain.push(rgba(getComputedStyle(e).backgroundColor) || [0, 0, 0, 0]); let c = [255, 255, 255, 1]; for (let i = chain.length - 1; i >= 0; i--) if (chain[i][3] > 0) c = over(chain[i], c); return c; };
    const lum = (c) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
    const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
    const out = [], seen = new Set();
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let n = w.nextNode(); n; n = w.nextNode()) {
      const el = n.parentElement;
      if (!n.data.trim() || !el || seen.has(el)) continue;
      seen.add(el);
      if (!el.getClientRects().length || el.closest("[aria-hidden='true'], .h-vh, [hidden], script, style, noscript, :disabled")) continue;
      const cs = getComputedStyle(el);
      if (cs.visibility === "hidden" || +cs.opacity === 0) continue;
      const back = bg(el), fg = over(rgba(cs.color), back);
      const size = parseFloat(cs.fontSize), bold = +cs.fontWeight >= 700;
      const need = size >= 24 || (size >= 18.66 && bold) ? 3 : 4.5;
      const r = ratio(fg, back);
      if (r < need) out.push(`${el.tagName.toLowerCase()}.${el.className} "${n.data.trim().slice(0, 30)}" ${r.toFixed(2)}:1 < ${need}`);
    }
    return out;
  });
}

// Every control a keyboard reaches says what it is.
async function unnamed(page) {
  return page.evaluate(() => {
    const name = (el) => {
      const by = el.getAttribute("aria-labelledby");
      const t = el.getAttribute("aria-label") || (by && by.split(/\s+/).map((id) => (document.getElementById(id) || {}).textContent || "").join(" ")) ||
        (el.id && (document.querySelector(`label[for="${CSS.escape(el.id)}"]`) || {}).textContent) || (el.closest("label") || {}).textContent ||
        el.textContent || el.getAttribute("title") || (el.querySelector("img[alt]") || {}).alt || (el.type === "submit" ? el.value : "") || "";
      return t.trim();
    };
    return [...document.querySelectorAll("a[href], button, input:not([type=hidden]), select, textarea, [tabindex]:not([tabindex='-1'])")]
      .filter((el) => el.getClientRects().length && !el.closest("[hidden], [aria-hidden='true']"))
      .filter((el) => !name(el)).map((el) => el.outerHTML.slice(0, 90));
  });
}

const A11Y_PAGES = ["/", "/halcyon-long", "/tags/architecture", "/search?q=GitComet", "/search?q=zzzqqq", "/members"];

test.describe("Halcyon", () => {
  test("is the theme a new install starts on", async ({ page }) => {
    await page.goto("/halcyon-long");
    await expect(page.locator("html")).toHaveClass(/\bh\b/);
    await expect(page.locator(".h-post h1")).toHaveText(POSTS["halcyon-long"].title);
  });

  for (const width of [390, 820, 1440]) {
    for (const size of [17, 20, 23]) {
      test(`nothing in a post is wider than its column at ${width} px, text ${size} px`, async ({ page }) => {
        await page.setViewportSize({ width, height: 900 });
        await page.addInitScript((s) => localStorage.setItem("vp-read", JSON.stringify({ size: s })), size);
        for (const slug of Object.keys(POSTS)) {
          await page.goto(`/${slug}`);
          await expect(page.locator("html")).toHaveAttribute("data-size", String(size));
          const o = await overflow(page);
          expect(o.over, `${slug}: elements past the column`).toEqual([]);
          expect(o.scrolls, `${slug}: elements that scroll sideways inside`).toEqual([]);
          expect(o.scroll, `${slug}: the page scrolls sideways`).toBeLessThanOrEqual(0);
        }
      });
    }
  }

  // One switch in Settings › Writing, saved in one click, asks search engines
  // not to follow links to other sites in every post and every comment, and
  // switched back off they are followed again: off is the default, where
  // bluemonday's preset had marked every link, the site's own included. The
  // comment list is the server's own answer, with one comment carrying a link
  // added to it, so the comment's rel is the server's switch read by the widget.
  test("one switch marks the site's links to other sites nofollow, in posts and comments", async ({ page }) => {
    await page.route("**/api/v1/articles/halcyon-long/comments", async (route) => {
      if (route.request().method() !== "GET") return route.continue();
      const real = await (await route.fetch()).json();
      real.comments = [{ id: "c1", parent_id: "", author: "Asha", body: "See https://example.org/notes for more.", created_at: new Date().toISOString(), status: "approved" }];
      return route.fulfill({ contentType: "application/json", body: JSON.stringify(real) });
    });
    const sw = page.locator("#s-links-nofollow");
    const postLink = page.locator('.h .content a[href="https://vayupress.com"]');
    const commentLink = page.locator('.vayu-comment-body a[href="https://example.org/notes"]');
    const expectLinks = async (on) => {
      await expect(async () => {
        await page.goto("/halcyon-long");
        if (on) await expect(postLink).toHaveAttribute("rel", "nofollow", { timeout: 1000 });
        else await expect(postLink).not.toHaveAttribute("rel", /nofollow/, { timeout: 1000 });
      }).toPass({ timeout: 15000, intervals: [1000, 2000] });
      await expect(commentLink).toHaveAttribute("rel", on ? "ugc nofollow noopener" : "noopener");
    };

    await page.goto("/os/settings/writing");
    await expect(sw, "off by default").not.toBeChecked();
    await expectLinks(false);
    try {
      await page.goto("/os/settings/writing");
      await sw.click();
      await expect(page.locator("[data-settings-bar]"), "one click, no Save").toBeHidden();
      await expect(sw).toBeEnabled();
      await page.reload();
      await expect(sw, "the click was saved").toBeChecked();
      await expectLinks(true);
    } finally {
      await page.goto("/os/settings/writing");
      if (await sw.isChecked()) {
        await sw.click();
        await expect(sw).toBeEnabled();
      }
      await page.reload();
      await expect(sw).not.toBeChecked();
    }
    await expectLinks(false);
  });

  // A diagram the author drew in the post is drawn: the technical-debt
  // guide's capacity chart, as johal.in stores it, as wide as the column at
  // every width, in its own proportions (its viewBox, written in lower case
  // by the sanitiser, is the browser's viewBox), its labels inside it and on
  // its own dark ground.
  test("a post's own diagram is drawn, in the column, with its labels", async ({ page }) => {
    for (const width of [390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto("/halcyon-long");
      const d = await page.evaluate(() => {
        const svg = document.querySelector(".h .content svg.vp-svg");
        if (!svg) return null;
        const box = svg.getBoundingClientRect(), col = document.querySelector(".h .content").getBoundingClientRect();
        const label = [...svg.querySelectorAll("text")].find((t) => t.textContent.startsWith("STANDING ALLOCATION"));
        const lb = label && label.getBoundingClientRect();
        return {
          viewBox: svg.viewBox.baseVal && svg.viewBox.baseVal.width,
          ratio: box.width / box.height, fits: box.left >= col.left - 1 && box.right <= col.right + 1,
          label: !!lb && lb.width > 0 && lb.left >= box.left && lb.right <= box.right && lb.top >= box.top && lb.bottom <= box.bottom,
          ground: getComputedStyle(svg.querySelector("rect")).fill,
        };
      });
      expect(d, `${width}: the diagram is not on the page`).not.toBeNull();
      expect(d.viewBox, `${width}: the browser did not read its viewBox`).toBe(720);
      expect(d.ratio, `${width}: drawn out of its proportions`).toBeCloseTo(720 / 150, 1);
      expect(d.fits, `${width}: wider than the column`).toBe(true);
      expect(d.label, `${width}: a label is not drawn inside it`).toBe(true);
      expect(d.ground, `${width}: its dark ground`).toBe("rgb(11, 18, 32)");
    }
  });

  // Most read's range tabs sit on the heading's line when the ranks come
  // first, and on a row of their own after pinned posts: the one pull put
  // them on the last pinned title (operator's screenshot, 1446 px). The
  // widget's feed is stubbed, so the pinned rows exist on this install.
  test("Most read's range tabs never sit on a post, with pinned posts or without", async ({ page }) => {
    await page.setViewportSize({ width: 1446, height: 900 });
    const item = (n) => ({ slug: "halcyon-long", title: `A ranked post with a title of an ordinary length, number ${n}` });
    let feed;
    await page.route("**/api/trending*", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify(feed) }));
    const measure = () => page.evaluate(() => {
      const box = (e) => e.getBoundingClientRect();
      const tabs = box(document.querySelector(".h-trend .vayu-trending-tabs"));
      const head = box(document.querySelector(".h-trend > h2"));
      const hit = [...document.querySelectorAll(".h-trend .vayu-trending-card")].map(box)
        .filter((c) => tabs.left < c.right && c.left < tabs.right && tabs.top < c.bottom && c.top < tabs.bottom).length;
      return { hit, onHeadLine: tabs.top < head.bottom && head.top < tabs.bottom };
    });
    const ranked = { "7": [1, 2, 3, 4].map(item), "30": [5, 6].map(item) };
    const long = "VayuPress: My Sovereign Publishing & Personal Infrastructure Platform – Built for Real Needs";
    for (const [name, pinned, onHeadLine] of [["with pinned posts", [item(7), item(8), { slug: "halcyon-long", title: long }], false], ["without", [], true]]) {
      feed = { enabled: true, pinned, windows: ranked };
      await page.goto("/");
      await expect(page.locator(".h-trend .vayu-trending-tab").first()).toBeVisible();
      const m = await measure();
      expect(m.hit, `${name}: the range tabs overlap a post`).toBe(0);
      expect(m.onHeadLine, `${name}: the range tabs on the heading's line`).toBe(onHeadLine);
    }
  });

  // The author's About holds 500 characters, shown whole with its line
  // breaks, and the server refuses a 501st, whatever the form allows. The
  // author's picture, hovered, is large enough to see a face (about 160 px),
  // in front of what it covers. This install's author has no picture, so one
  // is put where the initial is; that the page draws the picture is
  // TestHalcyonDrawsTheAuthorsPictureAndWholeBio's.
  test("the author's About holds 500 characters, and their picture grows on hover", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/os");
    await page.waitForFunction(() => /(?:^|;\s*)vp_csrf=/.test(document.cookie));
    const setBio = (v) => page.evaluate(async (v) => {
      const csrf = decodeURIComponent(document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/)[1]);
      const r = await fetch("/os/api/settings", { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify({ key: "site.author_bio", value: v }) });
      return { status: r.status, text: await r.text() };
    }, v);
    const head = "Writes about the systems under a small publishing platform.\n";
    const bio = head + "x".repeat(500 - head.length);
    expect(bio.length).toBe(500);
    expect((await setBio(bio)).status, "a 500-character bio").toBe(200);
    try {
      const over = await setBio(bio + "y");
      expect(over.status, "a 501-character bio").toBe(400);
      expect(over.text).toContain("The author bio must be at most 500 characters");

      const about = page.locator(".h-author p + p");
      await expect(async () => {
        await page.goto("/halcyon-long");
        await expect(about).toHaveText(bio.replace("\n", " "), { timeout: 1000, useInnerText: false });
      }).toPass({ timeout: 15000, intervals: [1000, 2000] });
      expect(await about.evaluate((e) => e.innerText), "the bio's line break").toContain("platform.\n");

      await page.evaluate(() => document.querySelectorAll(".h-av--i").forEach((s) => {
        const img = document.createElement("img");
        img.className = "h-av"; img.src = "/static/brand-48.png"; img.width = 40; img.height = 40; img.alt = "";
        s.replaceWith(img);
      }));
      for (const where of [".h-who", ".h-author"]) {
        const pic = page.locator(`${where} img.h-av`);
        await pic.scrollIntoViewIfNeeded();
        const before = await pic.boundingBox();
        await pic.hover();
        await page.waitForTimeout(400);
        const after = await pic.evaluate((e) => {
          const r = e.getBoundingClientRect();
          return { w: r.width, front: document.elementFromPoint(r.right - 12, r.top + r.height / 2) === e };
        });
        expect(before.width, `${where}: the picture before hovering`).toBeLessThan(60);
        expect(after.w, `${where}: the picture hovered is too small to see`).toBeGreaterThanOrEqual(150);
        expect(after.front, `${where}: the hovered picture is behind the page`).toBe(true);
        await page.mouse.move(0, 0);
      }
    } finally {
      expect((await setBio("")).status).toBe(200);
    }
  });

  // A signed-in reader is named where Sign in and Subscribe were: in the bar,
  // in the phone menu, and no newsletter form asks them to join. The page is
  // cached for everyone, so it is drawn signed out and turned by the portal
  // once /members/me answers (stubbed: this run has no member session). The
  // account panel is Halcyon's sectioned view, and every button's label sits
  // in the middle of its button. One stage per rule.
  test("a signed-in reader is named in the bar, and the account panel is Halcyon's", async ({ page }) => {
    await page.goto("/os");
    await page.waitForFunction(() => /(?:^|;\s*)vp_csrf=/.test(document.cookie));
    const setMembership = (on) => page.evaluate(async (v) => {
      const csrf = decodeURIComponent(document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/)[1]);
      const r = await fetch("/os/api/settings", { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf }, body: JSON.stringify({ key: "site.membership_buttons", value: v }) });
      return r.status;
    }, on ? "true" : "false");
    expect(await setMembership(true)).toBe(200);
    try {
      const me = { enabled: true, authenticated: false, member: null };
      await page.route("**/api/v1/members/me", (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify(me) }));
      const bar = page.locator(".h-bar");

      // A settings change marks the cached pages stale, and the first request
      // after it is still answered from the old page while it re-renders
      // (stale-while-revalidate, cache_swr.go): read until the new one comes,
      // a second apart, since the Shield refuses a page asked for over and
      // over in the same instant.
      await expect(async () => {
        await page.goto("/halcyon-long");
        await expect(bar.locator("[data-vp-signin]")).toHaveText("Sign in", { timeout: 1000 });
      }).toPass({ timeout: 15000, intervals: [1000, 2000] });
      await expect(bar.locator("[data-vp-signup]")).toHaveText("Subscribe");
      await expect(page.locator(".vp-portal-trigger"), "a second way in, floating over the page").toHaveCount(0);
      await expect(async () => {
        await page.goto("/");
        await expect(page.locator(".h-letter"), "Home's newsletter form, signed out").toHaveCount(1, { timeout: 1000 });
      }).toPass({ timeout: 15000, intervals: [1000, 2000] });

      me.authenticated = true;
      me.member = { name: "Asha Rao", email: "asha@example.com", paid: false };
      await page.goto("/halcyon-long");
      await expect(bar.locator("[data-vp-signin] .vayu-nav-name"), "the bar names the reader").toHaveText("Asha Rao");
      await expect(bar.locator("[data-vp-signin] .vayu-nav-initial"), "with their initial for a picture").toHaveText("A");
      await expect(page.locator("[data-vp-signup]"), "a signed-in reader is asked to subscribe").toHaveCount(0);
      await expect(page.locator("#h-menu [data-vp-signin]"), "the phone menu names the reader").toContainText("Asha Rao");
      await page.goto("/");
      await expect(page.locator(".h-bar .vayu-nav-name")).toHaveText("Asha Rao");
      await expect(page.locator(".h-letter"), "Home asks a member to subscribe").toHaveCount(0);
      await page.goto("/halcyon-long");

      await bar.locator("[data-vp-signin]").click();
      const panel = page.locator(".vp-portal-panel");
      await expect(panel.locator(".vp-sec__name")).toHaveText("Asha Rao");
      await expect(panel.locator(".vp-sec__label")).toHaveText(["Your plan"]);
      await expect(panel.locator(".vp-portal-brand"), "the panel is headed by the site").toHaveText("VayuPress");
      await expect(panel.locator(".vp-sec__state")).toHaveText(["Free"]);
      await expect(panel.locator("details"), "the account folded into boxes").toHaveCount(0);
      const off = await panel.locator(".vp-portal-btn").evaluateAll((bs) => bs.map((b) => {
        const r = document.createRange(); r.selectNodeContents(b);
        const t = r.getBoundingClientRect(), o = b.getBoundingClientRect();
        return { label: b.textContent, off: Math.abs((t.top + t.bottom) / 2 - (o.top + o.bottom) / 2) };
      }).filter((x) => x.off > 1.5));
      expect(off, "a button's label is not in its middle").toEqual([]);
    } finally {
      expect(await setMembership(false)).toBe(200);
    }
  });

  // An imported post's own stylesheet is not the page's: nothing of it is
  // shown or fetched, and the post still reads.
  test("an imported post's own style is neither shown nor fetched", async ({ page }) => {
    const asked = [];
    page.on("request", (r) => asked.push(new URL(r.url()).host));
    await page.goto("/halcyon-legacy");
    await expect(page.locator(".h-post h1")).toHaveText(POSTS["halcyon-legacy"].title);
    const text = await page.locator(".h .content").innerText();
    for (const leak of ["@import", "FORCE CENTERING", "magazine-article", "schema.org"]) expect(text, leak).not.toContain(leak);
    expect(asked.filter((h) => /googleapis|cdnjs/.test(h))).toEqual([]);
    await expect(page.locator(".h .content")).toContainText("Cilium leads in raw performance");
  });

  // A topic as long as a topic may be still sits on a phone.
  test("a topic page with the longest name fits a phone", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    await page.goto("/tags/offline-first-progressive-web-app-design");
    await expect(page.locator("h1")).toHaveText("offline first progressive web app design");
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
  });

  // An image that does not arrive is left out with its caption, not shown as
  // a hole; one that does stays. Both ways it can fail: before the script
  // runs (found by decode()), and after (its error event), the second by
  // holding the missing image's answer back until the page has settled.
  for (const late of [false, true]) {
    test(`an image that does not arrive is left out, with its caption${late ? ", failing late" : ""}`, async ({ page }) => {
      if (late) {
        await page.route("**/media/does-not-exist-e2e.png", async (route) => {
          await new Promise((r) => setTimeout(r, 1500));
          await route.fulfill({ status: 404, body: "" });
        });
      }
      // A plain goto waits for every image, the held one included.
      await page.goto("/halcyon-edges", { waitUntil: late ? "domcontentloaded" : "load" });
      const figs = page.locator(".h .content figure");
      if (late) await expect(figs.nth(1)).toBeVisible();
      await expect(figs.nth(1)).toBeHidden();
      await expect(figs.nth(0)).toBeVisible();
      await expect(page.getByText("A caption for an image that will not arrive.")).toBeHidden();
    });
  }

  test("every cell of the wide table is still there on a phone, labelled", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 900 });
    await page.goto("/halcyon-wide");
    const cells = page.locator(".h .content .tbl[data-stack] td");
    expect(await cells.count()).toBe(35);
    await expect(page.locator('.h .content td[data-label="GitKraken"]').first()).toBeVisible();
    await expect(page.locator(".h .content thead")).toBeHidden();
  });

  test("reading settings: open on Aa, focus inside, Escape closes, focus returns", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    const aa = page.locator(".h-byline [data-h-open='h-reader']");
    await aa.click();
    const panel = page.locator("#h-reader");
    await expect(panel).toBeVisible();
    await expect(aa).toHaveAttribute("aria-expanded", "true");
    expect(await panel.evaluate((p) => p.contains(document.activeElement))).toBe(true);
    // Aa leaves the page scrollable: only full-screen sheets lock it.
    await expect(page.locator("html")).not.toHaveClass(/h-lock/);
    await page.keyboard.press("Escape");
    await expect(panel).toBeHidden();
    await expect(aa).toBeFocused();
  });

  test("reading settings: a click outside closes, and focus is not pulled back", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.mouse.click(20, 500);
    await expect(page.locator("#h-reader")).toBeHidden();
  });

  test("the phone menu is a sheet: it locks the page, keeps Tab inside, closes on Escape", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/");
    const btn = page.locator("[data-h-open='h-menu']");
    await btn.click();
    const sheet = page.locator("#h-menu");
    await expect(sheet).toBeVisible();
    await expect(page.locator("html")).toHaveClass(/h-lock/);
    for (let i = 0; i < 12; i++) await page.keyboard.press("Tab");
    expect(await sheet.evaluate((s) => s.contains(document.activeElement))).toBe(true);
    await page.keyboard.press("Escape");
    await expect(sheet).toBeHidden();
    await expect(page.locator("html")).not.toHaveClass(/h-lock/);
    await expect(btn).toBeFocused();
  });

  test("a reader's choices apply before paint, on that device, and reset on request", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.locator("#h-reader [data-h-face='sans']").click();
    await page.locator("#h-reader [data-h-scheme='dark']").click();
    await page.locator("#h-reader [data-h-size]").fill("23");
    // Read the attributes the moment the document exists, before any deferred
    // script has run: the inline pre-paint script alone must have set them.
    await page.route("**/static/js/halcyon.js*", (r) => r.abort());
    await page.reload();
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-face", "sans");
    await expect(html).toHaveAttribute("data-size", "23");
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.unroute("**/static/js/halcyon.js*");
    await page.reload();
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    await page.locator("#h-reader [data-h-reset]").click();
    await expect(html).toHaveAttribute("data-face", "serif");
    await expect(html).toHaveAttribute("data-size", "20");
    await expect(html).not.toHaveAttribute("data-theme", /./);
  });

  test("motion stops for a reader who asked for less", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/halcyon-long");
    await page.locator(".h-byline [data-h-open='h-reader']").click();
    const anim = await page.locator("#h-reader").evaluate((e) => getComputedStyle(e).animationName);
    expect(anim).toBe("none");
  });

  test("asks nothing of any other host", async ({ page, baseURL }) => {
    const host = new URL(baseURL).host;
    const off = [];
    page.on("request", (r) => {
      const u = new URL(r.url());
      if (/^https?:$/.test(u.protocol) && u.host !== host) off.push(r.url());
    });
    for (const p of ["/", "/halcyon-long", "/tags", "/members"]) await page.goto(p);
    expect(off).toEqual([]);
  });

  // A topic page offers newest and most read, and its search stays in the
  // topic and keeps the order. The seeded posts carry different topics, so a
  // search for one post's title finds it from its own topic and not another's.
  test("a topic page has its two orders, and a search that stays in the topic", async ({ page }) => {
    await page.goto("/tags/git");
    const order = page.getByRole("navigation", { name: "Order" });
    await expect(order.getByRole("link", { name: "Newest" })).toHaveAttribute("aria-current", "true");
    await order.getByRole("link", { name: "Most read" }).click();
    await expect(page).toHaveURL(/\/tags\/git\?sort=read$/);
    await expect(order.getByRole("link", { name: "Most read" })).toHaveAttribute("aria-current", "true");
    await expect(page.locator(".h-count, .h-empty").first()).toContainText("in the last 30 days");
    await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", "noindex,follow");

    const q = "GitComet";
    await expect.poll(async () => {
      await page.goto("/tags/git?sort=read");
      await page.locator("#h-find-q").fill(q);
      await page.locator("#h-find-q").press("Enter");
      return page.locator(".h-results li").count();
    }, { timeout: 30000 }).toBeGreaterThan(0);
    await expect(page).toHaveURL(/topic=git/);
    await expect(page).toHaveURL(/sort=read/);
    await expect(page.locator("h1")).toHaveText("Search git");
    await expect(page.locator(".h-count")).toContainText("most read first");
    await expect(page.locator(".h-results")).toContainText(POSTS["halcyon-wide"].title);

    await page.goto(`/search?q=${q}&topic=architecture`);
    await expect(page.locator(".h-count")).toHaveText(`Nothing in architecture matches “${q}”.`);
    await page.getByRole("link", { name: "Search every topic" }).click();
    await expect(page.locator(".h-results")).toContainText(POSTS["halcyon-wide"].title);
  });

  // Your own comment before moderation stays on the page for you, marked, until
  // it is approved; it is kept in that browser only, and goes when nobody is
  // signed in there. The comments API is stubbed as comments.spec.js does: this
  // run has no member session. One stage per rule.
  test("your own comment awaiting moderation is shown to you alone, until it is approved", async ({ page }) => {
    const slug = "halcyon-long";
    let approved = false, signedIn = true;
    const mine = { id: "p1", parent_id: "", author: "Asha", body: "Does the 15% hold for platform teams?", created_at: new Date().toISOString(), status: "pending" };
    await page.route("**/api/v1/members/me", (route) => signedIn
      ? route.fulfill({ contentType: "application/json", body: JSON.stringify({ authenticated: true, member: { name: "Asha", email: "asha@example.com" } }) })
      : route.fulfill({ status: 401, contentType: "application/json", body: "{}" }));
    await page.route(`**/api/v1/articles/${slug}/comments`, (route) => route.request().method() === "POST"
      ? route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify(mine) })
      : route.fulfill({ contentType: "application/json", body: JSON.stringify({ comments: approved ? [{ ...mine, status: "approved" }] : [] }) }));
    const note = page.locator(".vayu-comment-pending");

    await page.goto(`/${slug}`);
    await page.locator(".vayu-comment-compose textarea").fill(mine.body);
    await page.locator(".vayu-comment-compose .vayu-comment-submit").click();
    await expect(note).toHaveText("Waiting for moderation · only you can see this");
    await expect(page.locator(".vayu-comment--pending .vayu-comment-body")).toHaveText(mine.body);

    await page.reload();
    await expect(note, "kept across a reload").toHaveCount(1);

    approved = true;
    await page.reload();
    await expect(page.locator(".vayu-comment-body")).toHaveCount(1);
    await expect(note, "gone once the approved list carries it").toHaveCount(0);
    expect(await page.evaluate((k) => localStorage.getItem(k), `vp-pending:${slug}`)).toBeNull();

    // Another member signed in on the same browser is not shown it.
    approved = false;
    await page.evaluate(([k, v]) => localStorage.setItem(k, v), [`vp-pending:${slug}`, JSON.stringify([{ ...mine, owner: "0" }])]);
    await page.reload();
    await expect(page.locator(".vayu-comment-compose textarea")).toBeVisible();
    await expect(note, "never shown to another member").toHaveCount(0);

    await page.evaluate(([k, v]) => localStorage.setItem(k, v), [`vp-pending:${slug}`, JSON.stringify([mine])]);
    signedIn = false;
    await page.reload();
    await expect(page.locator(".vayu-comment-gate")).toBeVisible();
    await expect(note, "never shown with nobody signed in").toHaveCount(0);
    expect(await page.evaluate((k) => localStorage.getItem(k), `vp-pending:${slug}`)).toBeNull();
  });

  for (const scheme of ["light", "dark"]) {
    test(`every text on every page type reaches its contrast, ${scheme}`, async ({ page }) => {
      await page.addInitScript((s) => localStorage.setItem("vayu-theme", s), scheme);
      for (const p of A11Y_PAGES) {
        await page.goto(p);
        await expect(page.locator("html")).toHaveAttribute("data-theme", scheme);
        expect(await lowContrast(page), `${p} (${scheme})`).toEqual([]);
      }
    });
  }

  test("every control a keyboard reaches has a name", async ({ page }) => {
    for (const p of A11Y_PAGES) {
      await page.goto(p);
      expect(await unnamed(page), p).toEqual([]);
    }
  });

  // Keyboard focus is always visible, in a colour that stands out from what
  // is behind it (3:1, WCAG 1.4.11), and placeholders are readable text.
  for (const scheme of ["light", "dark"]) {
    test(`focus is visible and placeholders are readable, ${scheme}`, async ({ page }) => {
      await page.addInitScript((s) => localStorage.setItem("vayu-theme", s), scheme);
      await page.goto("/halcyon-long");
      const misses = [];
      for (let i = 0; i < 14; i++) {
        await page.keyboard.press("Tab");
        const r = await page.evaluate(() => {
          const el = document.activeElement, cs = getComputedStyle(el);
          const ring = cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) >= 2;
          return { what: el.outerHTML.slice(0, 70), seen: ring || cs.boxShadow !== "none" };
        });
        if (!r.seen) misses.push(r.what);
      }
      expect(misses, "focused with nothing to show it").toEqual([]);
      await page.goto("/search?q=GitComet");
      for (const sel of ["#h-q", "#h-letter-email"]) {
        await page.goto(sel === "#h-q" ? "/search?q=GitComet" : "/");
        if (!(await page.locator(sel).count())) continue;
        const ph = await page.evaluate((sel) => {
          const rgba = (s) => { const p = s.match(/[\d.]+/g).map(Number); return [p[0], p[1], p[2], p.length > 3 ? p[3] : 1]; };
          const over = (top, under) => { const a = top[3]; return [0, 1, 2].map((i) => top[i] * a + under[i] * (1 - a)).concat(1); };
          const lum = (c) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
          const el = document.querySelector(sel);
          const chain = [];
          for (let e = el; e; e = e.parentElement) chain.push(rgba(getComputedStyle(e).backgroundColor));
          let b = [255, 255, 255, 1];
          for (let i = chain.length - 1; i >= 0; i--) if (chain[i][3] > 0) b = over(chain[i], b);
          const ps = getComputedStyle(el, "::placeholder");
          const c = over([...rgba(ps.color).slice(0, 3), rgba(ps.color)[3] * +ps.opacity], b);
          const x = lum(c), y = lum(b);
          return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
        }, sel);
        expect(ph, `${sel} placeholder contrast`).toBeGreaterThanOrEqual(4.5);
      }
    });
  }

  // Reflow (WCAG 1.4.10): at 320 CSS px, what a 1280 px screen shows at 400%
  // zoom, no page type scrolls sideways.
  test("every page type reflows at 320 px", async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 640 });
    for (const p of [...A11Y_PAGES, "/halcyon-title", "/halcyon-edges", "/halcyon-legacy", "/tags", "/tags/offline-first-progressive-web-app-design"]) {
      await page.goto(p);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth), p).toBeLessThanOrEqual(0);
    }
  });

  // Light to load (PLAN.md §5): a first visit fetches only the font faces
  // the page draws with, and no page type grows past its weight.
  //
  // The budget is a first visit's transfer, fonts, script and stylesheets
  // included: 320 KB. Measured on 2026-10-10 at 147 KB (sign-in) to 286 KB
  // (the long post, seven faces with code); the margin is for a post with
  // more to draw, and growth past it is a decision, made by raising this
  // number with its reason here.
  test("a first visit fetches only the fonts it draws with, within its weight", async ({ browser }) => {
    for (const p of ["/", "/halcyon-long", "/halcyon-wide", "/tags/architecture", "/search?q=GitComet", "/members"]) {
      const ctx = await browser.newContext();
      const page = await ctx.newPage();
      const cdp = await ctx.newCDPSession(page);
      await cdp.send("Network.enable");
      let bytes = 0;
      const faces = [];
      cdp.on("Network.loadingFinished", (e) => { bytes += e.encodedDataLength; });
      page.on("request", (r) => { if (r.resourceType() === "font") faces.push(new URL(r.url()).pathname.split("/").pop()); });
      await page.goto(p, { waitUntil: "networkidle" });
      const drawn = await page.evaluate(() => {
        const out = new Set();
        for (const e of document.querySelectorAll("body *")) {
          if (!e.getClientRects().length || ![...e.childNodes].some((n) => n.nodeType === 3 && n.data.trim())) continue;
          const s = getComputedStyle(e);
          out.add([s.fontFamily.split(",")[0].replace(/["']/g, "").trim().toLowerCase().replace(/ /g, "-"), s.fontWeight, s.fontStyle].join(" "));
        }
        return [...out];
      });
      // A face file is named family-latin-weight[-style]: newsreader-latin-400-italic.
      const unused = faces.filter((f) => {
        const m = f.match(/^(.+?)-latin-(\d+)(?:-(normal|italic))?\.woff2$/);
        if (!m) return true;
        const [, fam, w, st = "normal"] = m;
        return !drawn.some((d) => { const [df, dw, ds] = d.split(" "); return df === fam && ds === st && Math.abs(+dw - +w) <= 100; });
      });
      expect(unused, `${p}: fonts fetched that nothing draws with`).toEqual([]);
      expect(bytes / 1024, `${p}: a first visit's transfer, KB`).toBeLessThanOrEqual(320);
      await ctx.close();
    }
  });

  // A site served over plain HTTP (a .onion) has no clipboard API. Localhost
  // counts as secure, so the page is told otherwise to reach that path.
  test("copies a link where the clipboard API is not offered", async ({ page }) => {
    await page.goto("/halcyon-long");
    await page.evaluate(() => Object.defineProperty(window, "isSecureContext", { value: false }));
    await page.locator(".h-byline [data-h-copylink]").click();
    await expect(page.locator("#h-live")).toHaveText("Link copied");
    await expect(page.locator("textarea")).toHaveCount(0);
  });
});
