# ADR-0166 — A theme may bring its own layout: Halcyon

- **Status:** Accepted
- **Date:** 2026-10-10
- **Extends** ADR-0071 (Theme Studio) and ADR-0086 (a theme designs the whole site).

## 0. The question

Every theme was CSS over one set of templates. That made a theme cheap to add
and safe to switch, and it set a ceiling: a lead story, an outline beside the
text, reading settings, a topic index by month and a phone dock are markup,
not paint, so no theme could have them. The operator asked for a theme in the
sense of the console's Still Air (calm, content first, one accent, depth that
explains) that would be VayuPress's own and the theme new sites start on, with
every one of its styles a choice in Theme Studio, its ads shown properly, and
no speed lost.

## 1. Decision

**A theme may name a template set.** `theme.Tokens` gains `Layout`; Halcyon's
preset sets `"halcyon"`, and no other value is recognised. `render.ActivateTheme`
is the one place a theme becomes live (boot, Theme Studio, the API and the
connector's `apply_theme`), so the stylesheet and the templates can never
belong to different themes. The public entry points (Home, a post, search, the
missing page, the topic index and a topic) dispatch to Halcyon's templates only
while its layout is active. Every other theme renders exactly as before:
`TestSharedTemplatesUnchangedByHalcyon` holds each of those pages byte for byte
to what the commit before Halcyon rendered from the same inputs. The one
difference is the version in three widget script URLs, below.

**Halcyon's own files.** A stylesheet (9.4 KB gzipped against a 16 KB budget)
and a script (3.8 KB against 6 KB) are compiled into the binary and served
immutable under content hashes. Newsreader (SIL OFL) joins the self-hosted
fonts. Halcyon pages load neither Pico nor `article.css`. The editor's
component rules (video facades, embed cards, `vp-*` blocks) are taken from
`article.css` at start-up, so there is one source for them. `/theme.css`
still follows, carrying the accent and any custom CSS.

**The reader's own choices.** Face, size (17–23 px), line width and
appearance are saved in that browser only, under the appearance key the
shared theme switch already uses. They are applied before first paint by an
inline script admitted by its hash, which is added to `script-src` on every
page. A site default never overrides a reader's saved choice, and "Use the
site's settings" clears it.

**Its options are per-theme options.** Home (front page, river, index),
archive pages and topics (index, same as Home), articles (column, margin),
reading face, text size, running text (small caps, as written), accent (site,
Still Air blue, custom), appearance for new readers, and the sections and
reading tools, each on or off. The theme compiler refuses a Halcyon accent
below 4.5:1 on any of its surfaces, in either scheme. The refusal is in the
compiler, so every path that applies a theme meets it.

**Presentation at render time, never in the stored post.** On the sanitised
HTML, in one token pass:

- the opening quotation becomes the standfirst;
- table cells carry their column's name, so a phone can set a row as
  labelled values;
- section headings carry anchors for the outline;
- runs of capitals are set in small caps.

The stored post is not touched, and tests check that no text is lost.

**Data without counting on a request.** Topic counts and the front page's
desks come from the topic index's own memo, refreshed off the request path.
Related reading is ranked by the topics a post shares with the one being
read. A topic carried by more than half of all posts is ignored, as it is
for the desks; on johal.in, every post carries `tutorial`. On a multi-domain
install every figure is the active domain's.

**Ads.** Halcyon's templates keep every anchor the ad injectors use, where
the design wants each slot. The sidebar placement, never shown before because
no theme had a side column, now fills Halcyon's rail; on narrower screens it
follows the post rather than hiding, because ad networks forbid a unit that is
served and never shown.

**Member pages** (sign-in, sign-up, the account, plans, the paywall,
checkout, the author's page) keep their markup and `signup.css`. Under Halcyon
they take its stylesheets after it, its header, and no pictographs in their
headings. Under any other theme they are returned unchanged.

**Theme Studio** shows Halcyon's options alone while it is loaded. Its preview
is the real page (Home, a topic, the newest post, a search, sign-in) rendered
from the site's posts with the options being edited, at Desktop (1440 px,
scaled to fit and saying so), Tablet and Phone, and in either scheme. A
stylesheet swap cannot show a layout option.

**New installs start on Halcyon; existing installs do not move.** Migration
106 saves `Default()`, exactly as `theme.Load` returned it, for every install
that already has a user or a post and no saved theme. `theme.Load` now returns
Halcyon for a missing row, which only a new install has.

## 2. Consequences

- **Shared widgets change slightly, for every theme.** The pictographs in the
  comments and trending widgets now sit in `aria-hidden` spans (`.vayu-ico`),
  so screen readers stop reading emoji and Halcyon can hide them. Trending
  groups carry `data-group`. The search sheet gives focus back to whatever
  opened it. Other themes look the same; their pages' script versions
  change.
- **Every page's `script-src` names one more hash.**
- **`theme.ContrastRatio` is the one contrast formula.** The console's copy is
  gone, and its guard that rated black on black as 21:1 is removed.
- **E2E boots a new install, so it runs on Halcyon.** The existing public
  specs do not depend on the shared templates' markup; `halcyon.spec.js` adds
  checks that:
  - nothing in a post is wider than its column, at 390, 820 and 1440 px and
    reader sizes 17 to 23;
  - every overlay follows its rules;
  - the reader's choices apply before paint;
  - motion stops under reduced motion;
  - no request leaves the host;
  - a link copies where the clipboard API is not offered (a .onion).
- **Not done here:**
  - Inline SVG diagrams are removed by the article sanitiser on every theme
    and their labels leak as loose text. Admitting or dropping them is a
    sanitiser decision.
  - A topic page's scoped search and "Most read" sort need backend support.
  - Your own comment awaiting moderation would need the comments API to
    return it.
