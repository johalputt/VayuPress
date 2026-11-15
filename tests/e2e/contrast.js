// SPDX-License-Identifier: Apache-2.0
//
// The contrast probe the specs share: Halcyon's pages, the console in both
// schemes, and the console in the Tor world all read their text with it.

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
      // Text hidden from a screen reader is still text on the screen, and a
      // page audit measures it like any other (the end mark of a post was
      // 2.3:1). One character on its own (a separator dot) it leaves alone.
      if (!el.getClientRects().length || el.closest(".h-vh, [hidden], script, style, noscript, :disabled")) continue;
      if (el.closest("[aria-hidden='true']") && n.data.trim().length < 2) continue;
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

module.exports = { lowContrast };
