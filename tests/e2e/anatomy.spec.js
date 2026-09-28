// The page grammar, measured on every console page.
//
// Still Air pages are built from six kinds (overview, list, document,
// settings, status, setup) and eight rules. The pages carried over from before
// it looked like dashboards: boxes inside boxes, filled status badges, strips
// of zero figures, forms loose in the page. This walk counts those on every
// page the rail offers.
//
// It is a ratchet. anatomy-baseline.json holds each page's counts; a page may
// get better and never worse. A page that says which kind it is
// (data-page-kind) is held to the rules outright, so a page is converted once
// and stays converted. A page the baseline has never seen is held to the rules
// too. ANATOMY_UPDATE=1 records the counts as the baseline instead of
// comparing with it; CI's update_baselines job does that, on CI's runner and
// in the same place in the run as the check, because the counts depend on
// what the install holds and on what the runner can reach.
const fs = require("fs");
const path = require("path");
const { test, expect } = require("@playwright/test");

const BASELINE = path.join(__dirname, "anatomy-baseline.json");
const KINDS = ["overview", "list", "document", "settings", "status", "setup"];
// Kinds whose pages edit in place: everywhere else a form rises in a sheet.
const EDITS_IN_PLACE = ["settings", "document"];

function anatomy() {
  const main = document.querySelector("main#main-content");
  if (!main) return null;
  const visible = (e) => {
    const b = e.getBoundingClientRect();
    const s = getComputedStyle(e);
    return b.width > 0 && b.height > 0 && s.visibility !== "hidden" && s.display !== "none";
  };
  const all = [...main.querySelectorAll("*")].filter(visible);
  // A box: bordered on every side, rounded, and big enough to hold content.
  const boxes = all.filter((e) => {
    const s = getComputedStyle(e);
    const b = e.getBoundingClientRect();
    return ["Top", "Right", "Bottom", "Left"].every((k) => parseFloat(s[`border${k}Width`]) > 0 && s[`border${k}Style`] !== "none") &&
      parseFloat(s.borderTopLeftRadius) >= 6 && b.width * b.height > 30000;
  });
  const nested = boxes.filter((e) => boxes.some((o) => o !== e && o.contains(e))).length;
  // A filled badge: a short run of text on a fill of its own, not a control.
  const badges = all.filter((e) => {
    const s = getComputedStyle(e);
    const b = e.getBoundingClientRect();
    const t = e.textContent.trim();
    return b.height >= 14 && b.height <= 24 && s.backgroundColor !== "rgba(0, 0, 0, 0)" &&
      getComputedStyle(e.parentElement).backgroundColor !== s.backgroundColor &&
      e.children.length <= 2 && t.length > 1 && t.length < 24 &&
      !e.matches("button, a, input, kbd, select, code, .sa-dot, [class*=avatar], [data-label]");
  }).length;
  // A live reading (data-live, ui.Figure.Live) is left out: whether Monitoring's
  // latency reads 0 depends on how much traffic the tests before this one made,
  // so counting it failed a commit that changed one Go test (5 in the
  // baseline, 10 on the run). The ratchet compares what a page is.
  const figures = [...main.querySelectorAll(".stat-card__value, [class*='figure'] [class*='value']")]
    .filter(visible).filter((e) => !e.closest("[data-live]"));
  const zeroFigures = figures.filter((e) => /^[$€₹£]?\s*0([.,]0+)?\s*(%|ms|s|B)?$/.test(e.textContent.trim())).length;
  // A field that only finds or goes somewhere is navigation, not a form: a
  // list's search (type=search, which Media filters with in place) and any
  // field of a GET form (a list's search, the pager's "go to page"), since a
  // GET changes nothing. A list's inspector edits the selected item where its
  // values are shown (render 07: a file's alt text), which rule 5 allows; the
  // rule is that making something new rises in a sheet.
  const inlineFields = all.filter((e) =>
    e.matches("input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=search]), textarea, select") &&
    !e.closest("dialog, [role=dialog], .sa-sheet, form[method=get i], [data-list-inspector]")).length;
  const crumb = !!document.querySelector(".sa-crumb, .sa-apphead, [aria-label='You are here'], [aria-label='Breadcrumb']");
  const kinds = [...main.querySelectorAll("[data-page-kind]")].map((e) => e.getAttribute("data-page-kind"));
  // Every h1, shown or not: the editor's is for screen readers, since the
  // post's title field is its visible title.
  const titles = main.querySelectorAll("h1").length;
  return { boxes: boxes.length, nested, badges, zeroFigures, inlineFields, crumb, kinds, titles };
}

// What the grammar allows a page of a given kind.
function ruleBreaks(a) {
  const out = [];
  if (a.kinds.length !== 1) out.push(`${a.kinds.length} page kinds (${a.kinds.join(", ")}); a page is exactly one`);
  const kind = a.kinds[0];
  if (kind && !KINDS.includes(kind)) out.push(`unknown page kind "${kind}"`);
  if (a.nested) out.push(`${a.nested} box(es) inside another box`);
  if (a.badges) out.push(`${a.badges} filled badge(s); state is a dot and a word`);
  if (a.zeroFigures) out.push(`${a.zeroFigures} zero figure(s); a figure shows only when it says something`);
  if (a.inlineFields && !EDITS_IN_PLACE.includes(kind)) out.push(`${a.inlineFields} form field(s) in the page; a form rises in a sheet`);
  return out;
}

const METRICS = ["boxes", "nested", "badges", "zeroFigures", "inlineFields"];

// A retry would measure an install the first attempt has already walked, so it
// would not be the same measurement.
test.describe.configure({ retries: 0 });

test("every console page keeps to the page grammar, or at least no further from it", async ({ page }) => {
  test.setTimeout(300000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/os");
  await page.waitForLoadState("networkidle");
  const hrefs = await page.evaluate(() => [...new Set([...document.querySelectorAll("[data-sa-index] a")].map((a) => a.getAttribute("href")))]);
  expect(hrefs.length).toBeGreaterThan(30); // an empty index would walk nothing and pass

  const baseline = fs.existsSync(BASELINE) ? JSON.parse(fs.readFileSync(BASELINE, "utf8")) : {};
  const counts = {};
  const findings = [];
  for (const href of hrefs.sort()) {
    await page.goto(href);
    // Measured once the page has filled in: several load their sections after
    // the document does, and counting at "load" saw an empty Analytics page.
    await page.waitForLoadState("networkidle");
    const a = await page.evaluate(anatomy);
    if (!a) {
      findings.push(`${href}: no main landmark`);
      continue;
    }
    counts[href] = Object.fromEntries(METRICS.map((m) => [m, a[m]]));
    // Rules every page keeps from today: the rail says where you are, and the
    // page says what it is in one title.
    if (a.crumb) findings.push(`${href}: a breadcrumb; the rail already says where you are`);
    if (a.titles !== 1) findings.push(`${href}: ${a.titles} titles (h1); a page has exactly one`);
    const base = baseline[href];
    if (process.env.ANATOMY_UPDATE && !a.kinds.length) continue; // recording, not comparing
    if (a.kinds.length || !base) {
      for (const f of ruleBreaks(a)) findings.push(`${href}: ${f}`);
    } else {
      for (const m of METRICS) {
        if (a[m] > base[m]) findings.push(`${href}: ${m} rose from ${base[m]} to ${a[m]}`);
      }
    }
  }

  // The report, in the log and as a file, so progress is visible on every run.
  const totals = Object.fromEntries(METRICS.map((m) => [m, Object.values(counts).reduce((s, c) => s + c[m], 0)]));
  console.log(["page".padEnd(34) + METRICS.map((m) => m.padStart(13)).join(""),
    ...Object.entries(counts).map(([h, c]) => h.padEnd(34) + METRICS.map((m) => String(c[m]).padStart(13)).join("")),
    "total".padEnd(34) + METRICS.map((m) => String(totals[m]).padStart(13)).join("")].join("\n"));
  fs.mkdirSync(test.info().outputDir, { recursive: true });
  fs.writeFileSync(path.join(test.info().outputDir, "anatomy.json"), JSON.stringify(counts, null, 2));
  if (process.env.ANATOMY_UPDATE) fs.writeFileSync(BASELINE, JSON.stringify(counts, null, 2) + "\n");

  expect(findings).toEqual([]);
});
