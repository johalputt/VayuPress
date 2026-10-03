// The page grammar, measured on every console page.
//
// Still Air pages are built from six kinds (overview, list, document,
// settings, status, setup) and eight rules. The pages carried over from before
// it looked like dashboards: boxes inside boxes, filled status badges, strips
// of zero figures, forms loose in the page. This walk counts those on every
// page the rail offers.
//
// It enforces (fidelity plan step 9). Every page says which kind it is
// (data-page-kind) and is held to the rules outright. The one exception is
// NOT_YET_CONVERTED: pages queued for a later release, which may get better
// and never worse than their counts in anatomy-baseline.json. Any other page
// without a kind fails. ANATOMY_UPDATE=1 records those pages' counts instead
// of comparing with them; CI's update_baselines job does that, on CI's runner
// and in the same place in the run as the check, because the counts depend on
// what the install holds and on what the runner can reach.
const fs = require("fs");
const path = require("path");
const { test, expect } = require("@playwright/test");

const BASELINE = path.join(__dirname, "anatomy-baseline.json");
// The six kinds, and the three the plans keep by name: the theme store's
// gallery and topology's diagram (fidelity plan §3), and "app", Mail and
// Talk, whose anatomy is the Mail plan's renders (§3, "Mail | All").
const KINDS = ["overview", "list", "document", "settings", "status", "setup", "gallery", "diagram", "app"];
// Kinds whose pages edit in place: everywhere else a form rises in a sheet.
// An app's composer and search are its work, not a form loose in a page.
const EDITS_IN_PLACE = ["settings", "document", "app"];
// Pages not yet given a kind, queued by name. Empty since pipeline item 104
// gave Sign-in security and VayuVeil theirs; adding a page here is a decision
// for the pipeline, not a way past the lint.
const NOT_YET_CONVERTED = [];

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
  // Menus and sheets float over the page (rule 2 names them), so neither they
  // nor what they hold are boxes in it.
  const boxes = all.filter((e) => !e.closest(".sa-pop__panel, dialog, .sa-sheet")).filter((e) => {
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
      // Mail's avatars (.vm-av) are avatars, and a conversation's size
      // (.mx-thread) is the Mail plan's count beside it, not a state.
      !e.matches("button, a, input, kbd, select, code, .sa-dot, [class*=avatar], .vm-av, .mx-thread, [data-label]");
  }).length;
  // A live reading (data-live, ui.Figure.Live) is left out: whether Monitoring's
  // latency reads 0 depends on how much traffic the tests before this one made,
  // so counting it failed a commit that changed one Go test (5 in the
  // baseline, 10 on the run). The ratchet compares what a page is.
  const figures = [...main.querySelectorAll(".stat-card__value, .sa-kpi__v, [class*='figure'] [class*='value']")]
    .filter(visible).filter((e) => !e.closest("[data-live]"));
  const zeroFigures = figures.filter((e) => /^[$€₹£]?\s*0([.,]0+)?\s*(%|ms|s|B)?$/.test(e.textContent.trim())).length;
  // A field that only finds or goes somewhere is navigation, not a form: a
  // list's search (type=search, which Media filters with in place) and any
  // field of a GET form (a list's search, the pager's "go to page"), since a
  // GET changes nothing. A list's inspector edits the selected item where its
  // values are shown (render 07: a file's alt text), which rule 5 allows; the
  // rule is that making something new rises in a sheet.
  // A field hidden from assistive technology (aria-hidden, as PGP's armour is
  // for its Copy button) is a page's machinery, not a form a person fills in.
  const inlineFields = all.filter((e) =>
    e.matches("input:not([type=hidden]):not([type=checkbox]):not([type=radio]):not([type=search]), textarea, select") &&
    e.getAttribute("aria-hidden") !== "true" &&
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
    const waiting = NOT_YET_CONVERTED.includes(href) && !a.kinds.length;
    if (process.env.ANATOMY_UPDATE && waiting) continue; // recording, not comparing
    if (!waiting || !base) {
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
  // Only the pages still waiting are recorded: every other page is held to the
  // rules, so a count for it would be a number nothing reads.
  if (process.env.ANATOMY_UPDATE) {
    fs.writeFileSync(BASELINE, JSON.stringify(Object.fromEntries(NOT_YET_CONVERTED.filter((h) => counts[h]).map((h) => [h, counts[h]])), null, 2) + "\n");
  }

  expect(findings).toEqual([]);
});

// Mail's administration keeps to the page grammar where it is real: on the
// install with Mail on. The walk above sees these pages as their setup state,
// since Mail is off on its install, so they are held to the rules here,
// outright (fidelity plan §6a), each as the tab it is.
const MAIL_ADMIN = ["/os/vayumail", "/os/vayumail/accounts", "/os/vayumail/dns", "/os/vayumail/pgp", "/os/vayumail/security"];
test("Mail's administration keeps to the page grammar, as tabs", async ({ page }) => {
  const base = process.env.MAIL_BASE_URL;
  if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
  test.setTimeout(120000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: "dark" });
  const findings = [];
  for (const href of MAIL_ADMIN) {
    // The DNS tab runs its lookups as it renders; the fixture's domain has no
    // records, so they fail fast, and the page is measured once they are in.
    await page.goto(base + href);
    await page.waitForLoadState("networkidle");
    const a = await page.evaluate(anatomy);
    for (const f of ruleBreaks(a)) findings.push(`${href}: ${f}`);
    if (a.crumb) findings.push(`${href}: a breadcrumb`);
    if (a.titles !== 1) findings.push(`${href}: ${a.titles} titles`);
    const current = await page.locator('main nav.tabs a[aria-current="page"]').evaluateAll((as) => as.map((x) => x.getAttribute("href")));
    if (current.join() !== href) findings.push(`${href}: the current tab is ${JSON.stringify(current)}`);
    const side = await page.locator('.sa-appside a[aria-current="page"] .sa-appside__label').allTextContents();
    if (side.join() !== "Administration") findings.push(`${href}: the sidebar marks ${JSON.stringify(side)}`);
  }
  expect(findings).toEqual([]);
});

// Mail's own pages with Mail on, held to the rules outright once the checks
// they run are in: Outbox and Connect a device (8.12), the mailbox directory
// an administrator opens Mail on, and the Mail plan's surfaces, a mailbox,
// compose and Talk, as the "app" kind (step 9).
const MAIL_OWN = ["/os/vayumail/sent", "/os/vayumail/connect", "/os/vayumail/inbox", "/os/vayumail/inbox?user=ankush", "/os/vayumail/compose", "/os/talk"];
test("Mail's own pages keep to the page grammar", async ({ page }) => {
  const base = process.env.MAIL_BASE_URL;
  if (!base) throw new Error("MAIL_BASE_URL is not set: boot the second install (.github/actions/boot-vayupress)");
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: "dark" });
  const findings = [];
  for (const href of MAIL_OWN) {
    await page.goto(base + href);
    // Talk holds its stream open, so its page is measured once it is
    // connected rather than once the network is quiet, which it never is.
    if (href === "/os/talk") await expect(page.locator("#vtalk-status")).toHaveAttribute("data-state", "online");
    else await page.waitForLoadState("networkidle");
    const a = await page.evaluate(anatomy);
    for (const f of ruleBreaks(a)) findings.push(`${href}: ${f}`);
    if (a.crumb) findings.push(`${href}: a breadcrumb`);
    if (a.titles !== 1) findings.push(`${href}: ${a.titles} titles`);
  }
  expect(findings).toEqual([]);
});
