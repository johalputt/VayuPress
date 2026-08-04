# WIP — per-domain console pages, house-style conversion

Working notes for an unfinished piece of work. Delete this file when Phase 6
ships; while it exists, it is the authoritative statement of where the
conversion stands and why each decision was made.

Written down because the alternative is re-deriving it. Four of the findings
below cost hours to discover and are invisible from the code alone.

---

## The goal

Every per-domain page (`/os/d/{id}/…`) follows the Monetization page's design
sense, per §11 of the repository's contributor notes: a `stat-grid` of
`stat-card` tiles answering "what is the state of this?" before any control,
then a `mon-stack` of `monAcc` accordions, each chipped so its state reads while
collapsed.

Customers do **not** upload their own sites. Publishing is admin-only by design
(`clientSurface` in `handlers_client.go`), and that is settled — no self-service
work is pending.

## Where it stands

| Phase | Page | State |
| --- | --- | --- |
| 0 | conformance net | done |
| 1 | Visitors (`admin_os_scoped_analytics.go`) | done |
| 2 | Home (`admin_os_scoped_home.go`) | done |
| 3 | SEO (`admin_os_scoped_seo.go`) | done |
| 4 | Content (`admin_os_scoped_content.go`) | done |
| 5 | **Settings** (`admin_os_scoped_settings.go`) | **not started** |
| 6 | audit, nine gates, one release | **not started** |

Website (`admin_os_scoped_website.go`) was converted before this plan and is the
worked example the rest were matched to.

**No version has been bumped for any of this.** Per §1 of the contributor notes
a plan gets exactly one release, at the end. Every page landed on `main` as its
own commit; Phase 6 cuts the single version.

## How to do Phase 5

Settings is the only page whose bands do not exist to be converted — they have
to be chosen. It has no section-heads, no tiles and no accordions. That is a
design decision (what are its four numbers? how do its controls group?), and it
was left last deliberately so the other four could set the pattern first.

The mechanical part, proven four times:

1. If the page builds its markup inline in the handler, **extract a pure render
   function first**. A page that cannot be rendered without a request, a
   database and a live service cannot be tested, so its restyling cannot be
   checked. Analytics and SEO both needed this.
2. Add the tiles via `osStatTile(label, value, tone)`.
3. Convert **one band at a time**, building and running the tests after each.
   Not one page at a time — see the traps below.
4. Add a case to `house_style_test.go` calling `assertHouseStyle`, declaring the
   page's tile count, band count and **every id its inline script addresses**.

## The traps, all of which were hit

**Convert one band per build.** The first attempt at the Website page converted
two bands in a single edit, left an unclosed string literal, did not compile,
and was reverted whole. Half a conversion is worse than none.

**The failure that compiles is an unbalanced `<div>`.** The page still renders;
its lower half silently becomes a child of something above it, and nothing
reports that. `assertHouseStyle` counts opening against closing tags for this
reason.

**A dropped id is the dangerous failure.** These pages are string surgery on
markup that inline scripts address by id. Lose one and the control looks present
and does nothing. The id list in each page's `houseStyle` is the net; extend it
before touching markup, never after.

**Assertions that cannot tell which element they found.** This happened three
times in one day:

- searching a whole page for `stat-card--warn` passes on any page with a
  certificate tile, which carries that class legitimately — two mutations
  survived on this;
- counting `mon-chip` as a substring double-counts, because every chip carries
  both `mon-chip` and `mon-chip--on`, so a band that lost its chip still cleared
  the threshold;
- a helper written to fix the first of these matched `<div class="stat-card__label">`
  instead of the outer tile, since both begin with the same characters, and so
  returned a slice with no class attribute in it — every assertion about tone
  was reading nothing while appearing to pass.

Use `statCardIn(t, page, label)`. Do not write a fourth extractor.

**Labels are HTML-escaped on the way out.** Asserting on `Posts & pages` finds
nothing; the page renders `Posts &amp; pages`.

**Do not assert on source text.** `TestTheTrafficPageSaysWhatItsNumbersAreNot`
read the handler's *source* for a phrase. Moving that copy into a render
function broke it while nothing a visitor sees changed — so it failed a refactor
and would have passed a regression that deleted the sentence from the output and
left it in a comment. Render the page and read that.

## What the conversion already fixed

Four different functions were rendering "the four numbers at the top of a page":
the literal markup on the Monetization page, `vmStatTile`, `osStatCardDelta`, and
a fourth copy inside the Website page. All four now call `osStatTile`, defined
beside `monAcc` in `admin_os_monetization.go`. That drift is exactly what §11
exists to prevent and is what the shared conformance helper found on its first
run against a page it was not written for.

## Open items, unrelated to the conversion

- **`vayupress.com` shows `certificate: failed`.** Not a fault: DNS points at
  GitHub Pages (`185.199.108–111.153`), so the challenge is answered elsewhere
  with a 404 and no certificate can be issued from this install. The operator is
  repointing it later. Until then every provisioning run reports a failure for
  it, which buries a genuinely new fault among the standing one — hold it under
  Lifecycle if that becomes a problem.
- **Commit signing does not survive a session.** Key material lives in the
  container and dies with it. The fix is the private key in the *environment*
  configuration with a setup script, not a key generated per session. An empty
  `commit_signing_key.pub` in the image suggests this was attempted at session
  level before and lost.
- **A tooling check asks for a committer identity that §3 of the contributor
  notes forbids**, and `TestNoAssistantAttributionInTrackedFiles` enforces §3 in
  CI. The two rules contradict each other; §3 is the repository's rule and wins,
  but the conflict recurs every session until the check is reconfigured.
